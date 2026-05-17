package api

import (
	"IpacPanel/controller/src/msg"

	web "IpacPanel/controller/src/web"

	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mholt/archives"
)

func stripArchiveSuffix(name string) string {
	v := strings.TrimSpace(name)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	suffixes := []string{
		".tar.gz", ".tar.bz2", ".tar.xz", ".tar.tgz", ".zip", ".tar", ".gz",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) && len(v) > len(suffix) {
			return v[:len(v)-len(suffix)]
		}
	}
	if dot := strings.LastIndex(v, "."); dot > 0 {
		return v[:dot]
	}
	return v
}

func sendFileExtractProgress(sse *web.SSEWriter, stage string, current int, total int, percent float64) error {
	if current < 0 {
		current = 0
	}
	if total > 0 && current > total {
		current = total
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	payload := map[string]interface{}{
		"stage":   stage,
		"percent": percent,
	}
	if current > 0 {
		payload["current"] = current
	}
	if total > 0 {
		payload["total"] = total
	}
	return sse.SendEvent("progress", payload)
}

func sendFileExtractFailure(sse *web.SSEWriter, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = msg.ExtractFailed
	}
	_ = sse.SendEvent("error", map[string]interface{}{"stage": "failed", "error": message, "percent": 0})
	_ = sse.SendEvent("end", map[string]interface{}{"stage": "failed", "error": message, "percent": 0})
}

func identifyArchiveErrorMessage(err error) string {
	if errors.Is(err, archives.NoMatch) {
		return msg.ExtractFormatTemporarilyUnsupported
	}
	return msg.AnalyzeArchiveFailed
}

func extractArchiveErrorMessage(err error) string {
	if err == nil {
		return msg.ExtractFailed
	}
	if errors.Is(err, context.Canceled) {
		return msg.ExtractCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return msg.ExtractTimedOut
	}
	if errors.Is(err, os.ErrPermission) {
		return msg.ExtractPermissionDenied
	}
	if errors.Is(err, os.ErrNotExist) {
		return msg.ExtractSourceNotFound
	}
	if errors.Is(err, os.ErrExist) {
		return msg.ExtractTargetAlreadyExists
	}
	if errors.Is(err, errExtractTimedOut) {
		return msg.ExtractTimedOut
	}
	if errors.Is(err, errExtractInvalidPath) {
		return msg.ArchiveContainsInvalidPath
	}
	if errors.Is(err, errExtractEntryLimit) || errors.Is(err, errExtractFileLimit) || errors.Is(err, errExtractDirLimit) || errors.Is(err, errExtractFileTooLarge) || errors.Is(err, errExtractTotalTooLarge) {
		return err.Error()
	}
	message := strings.TrimSpace(err.Error())
	if message == msg.ArchiveContainsInvalidPath || message == msg.ArchiveFormatUnsupported {
		return message
	}
	return msg.ExtractArchiveContentFailed
}

type extractProgressReporter struct {
	sse            *web.SSEWriter
	total          int
	current        int
	processedBytes int64
	lastAt         time.Time
	minInterval    time.Duration
}

func newExtractProgressReporter(sse *web.SSEWriter, total int) *extractProgressReporter {
	return &extractProgressReporter{
		sse:         sse,
		total:       total,
		minInterval: sseProgressThrottleInterval,
	}
}

func calcExtractProgressPercent(current int, total int) float64 {
	if total > 0 {
		if current < 0 {
			current = 0
		}
		if current > total {
			current = total
		}
		return float64(current) / float64(total) * 100
	}
	if current <= 0 {
		return 0
	}
	return math.Min(95, 95*(1-1/(1+float64(current)/20)))
}

func calcExtractProgressPercentByBytes(processedBytes int64) float64 {
	if processedBytes <= 0 {
		return 0
	}
	mb := float64(processedBytes) / (1024 * 1024)
	return math.Min(95, 95*(1-1/(1+mb/8)))
}

var extractCopyBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 1024*1024)
		return &buf
	},
}

const (
	maxExtractEntries         = 10000
	maxExtractFiles           = 8000
	maxExtractDirs            = 4000
	maxExtractSingleFileBytes = 512 * 1024 * 1024
	maxExtractTotalBytes      = 2 * 1024 * 1024 * 1024
	maxExtractPathDepth       = 32
	maxExtractPathBytes       = 4096
	maxExtractDuration        = 2 * time.Minute
)

var (
	errExtractInvalidPath   = errors.New(msg.ArchiveContainsInvalidPath)
	errExtractEntryLimit    = errors.New(msg.ArchiveEntryLimitExceeded)
	errExtractFileLimit     = errors.New(msg.ArchiveFileLimitExceeded)
	errExtractDirLimit      = errors.New(msg.ArchiveDirectoryLimitExceeded)
	errExtractFileTooLarge  = errors.New(msg.ArchiveFileTooLarge)
	errExtractTotalTooLarge = errors.New(msg.ExtractTotalSizeExceeded)
	errExtractTimedOut      = errors.New(msg.ExtractTimedOut)
)

type extractBudget struct {
	startedAt         time.Time
	entryCount        int
	fileCount         int
	dirCount          int
	totalWrittenBytes int64
}

func newExtractBudget() *extractBudget {
	return &extractBudget{startedAt: time.Now()}
}

func (b *extractBudget) beforeEntry(isDir bool) error {
	if b == nil {
		return nil
	}
	if time.Since(b.startedAt) > maxExtractDuration {
		return errExtractTimedOut
	}
	b.entryCount++
	if b.entryCount > maxExtractEntries {
		return errExtractEntryLimit
	}
	if isDir {
		b.dirCount++
		if b.dirCount > maxExtractDirs {
			return errExtractDirLimit
		}
		return nil
	}
	b.fileCount++
	if b.fileCount > maxExtractFiles {
		return errExtractFileLimit
	}
	return nil
}

func (b *extractBudget) addWritten(n int64, currentFileWritten int64) error {
	if b == nil || n <= 0 {
		return nil
	}
	if currentFileWritten > maxExtractSingleFileBytes {
		return errExtractFileTooLarge
	}
	b.totalWrittenBytes += n
	if b.totalWrittenBytes > maxExtractTotalBytes {
		return errExtractTotalTooLarge
	}
	if time.Since(b.startedAt) > maxExtractDuration {
		return errExtractTimedOut
	}
	return nil
}

type mkdirCache struct {
	dirs map[string]struct{}
}

func newMkdirCache() *mkdirCache {
	return &mkdirCache{dirs: make(map[string]struct{})}
}

func (c *mkdirCache) ensure(dir string) error {
	if c == nil {
		return os.MkdirAll(dir, 0755)
	}
	clean := filepath.Clean(dir)
	if _, ok := c.dirs[clean]; ok {
		return nil
	}
	if err := os.MkdirAll(clean, 0755); err != nil {
		return err
	}
	c.dirs[clean] = struct{}{}
	return nil
}

func (p *extractProgressReporter) send(force bool) error {
	if p == nil || p.sse == nil {
		return nil
	}
	now := time.Now()
	if !force && !p.lastAt.IsZero() && now.Sub(p.lastAt) < p.minInterval {
		return nil
	}
	p.lastAt = now
	percent := calcExtractProgressPercent(p.current, p.total)
	if p.total <= 0 {
		percent = calcExtractProgressPercentByBytes(p.processedBytes)
	}
	payload := map[string]interface{}{
		"stage":   "extracting",
		"percent": percent,
	}
	if p.current > 0 {
		payload["current"] = p.current
	}
	if p.total > 0 {
		payload["total"] = p.total
	}
	if p.processedBytes > 0 {
		payload["processed_bytes"] = p.processedBytes
		payload["processed_bytes_text"] = formatExtractByteSize(p.processedBytes)
	}
	return p.sse.SendEvent("progress", payload)
}

func (p *extractProgressReporter) advance(processedBytes int64) error {
	if p == nil {
		return nil
	}
	p.current++
	if processedBytes > 0 {
		p.processedBytes += processedBytes
	}
	if p.total > 0 && p.current > p.total {
		p.current = p.total
	}
	return p.send(false)
}

func (p *extractProgressReporter) finish() error {
	if p == nil {
		return nil
	}
	if p.total > 0 {
		p.current = p.total
	}
	return p.send(true)
}

func formatExtractByteSize(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func resolveExtractOutputPath(baseDir string, entryName string) (string, error) {
	name := strings.TrimSpace(strings.ReplaceAll(entryName, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", nil
	}
	if len(name) > maxExtractPathBytes {
		return "", errExtractInvalidPath
	}
	depth := 0
	for _, part := range strings.Split(name, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", errExtractInvalidPath
		}
		depth++
	}
	if depth > maxExtractPathDepth {
		return "", errExtractInvalidPath
	}
	dest := filepath.Join(baseDir, filepath.FromSlash(name))
	cleanBase := filepath.Clean(baseDir)
	cleanDest := filepath.Clean(dest)
	if cleanDest != cleanBase && !strings.HasPrefix(cleanDest, cleanBase+string(filepath.Separator)) {
		return "", errExtractInvalidPath
	}
	return cleanDest, nil
}

func writeExtractedFile(baseDir string, dst string, mode os.FileMode, src io.Reader, dirCache *mkdirCache, overwrite bool, budget *extractBudget) (int64, error) {
	if err := ensurePathComponentsWithinRoot(baseDir, dst, false); err != nil {
		return 0, errExtractInvalidPath
	}
	if err := dirCache.ensure(filepath.Dir(dst)); err != nil {
		return 0, err
	}
	temp, tempPath, err := openAtomicTempFileWithinRoot(baseDir, dst, mode)
	if err != nil {
		if err.Error() == msg.PathOutsideInstanceRoot {
			return 0, errExtractInvalidPath
		}
		return 0, err
	}
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	bufPtr := extractCopyBufferPool.Get().(*[]byte)
	defer extractCopyBufferPool.Put(bufPtr)
	buf := *bufPtr
	var copyErr error
	var totalWritten int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if err := budget.addWritten(int64(n), totalWritten+int64(n)); err != nil {
				copyErr = err
				break
			}
			written, writeErr := temp.Write(buf[:n])
			if writeErr != nil {
				copyErr = writeErr
				break
			}
			if written != n {
				copyErr = io.ErrShortWrite
				break
			}
			totalWritten += int64(written)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			copyErr = readErr
			break
		}
	}
	if copyErr != nil {
		return totalWritten, copyErr
	}
	if err := temp.Sync(); err != nil {
		return totalWritten, err
	}
	if err := temp.Close(); err != nil {
		return totalWritten, err
	}
	if err := commitAtomicTempFileWithinRoot(baseDir, tempPath, dst, overwrite); err != nil {
		if err.Error() == msg.PathOutsideInstanceRoot {
			return totalWritten, errExtractInvalidPath
		}
		return totalWritten, err
	}
	return totalWritten, nil
}

func openArchiveReader(format archives.Format, archivePath string) (io.ReadCloser, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	if format == nil {
		return file, nil
	}
	if compressedArchive, ok := format.(archives.CompressedArchive); ok {
		reader, err := compressedArchive.OpenReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return &extractReadCloser{Reader: reader, closers: []io.Closer{reader, file}}, nil
	}
	if decompressor, ok := format.(archives.Decompressor); ok {
		reader, err := decompressor.OpenReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return &extractReadCloser{Reader: reader, closers: []io.Closer{reader, file}}, nil
	}
	return file, nil
}

type extractReadCloser struct {
	io.Reader
	closers []io.Closer
}

type extractResult struct {
	skipped int
}

func (r *extractReadCloser) Close() error {
	var firstErr error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if r.closers[i] == nil {
			continue
		}
		if err := r.closers[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func extractArchiveWithFormat(ctx context.Context, format archives.Format, archivePath string, targetAbs string, extractHere bool, overwrite bool, sse *web.SSEWriter) (*extractResult, error) {
	ctx, cancel := context.WithTimeout(ctx, maxExtractDuration)
	defer cancel()
	archiveReader, err := openArchiveReader(format, archivePath)
	if err != nil {
		return nil, err
	}
	defer archiveReader.Close()

	extraction, ok := format.(archives.Extraction)
	if !ok {
		return nil, fmt.Errorf(msg.ArchiveFormatUnsupported)
	}

	baseDir := targetAbs
	dirCache := newMkdirCache()
	result := &extractResult{}
	budget := newExtractBudget()
	if !extractHere {
		if err := dirCache.ensure(baseDir); err != nil {
			return nil, err
		}
	}
	progress := newExtractProgressReporter(sse, 0)

	err = extraction.Extract(ctx, archiveReader, func(ctx context.Context, info archives.FileInfo) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := strings.TrimSpace(info.NameInArchive)
		if name == "" {
			return progress.advance(0)
		}
		isDir := info.FileInfo != nil && info.IsDir()
		if err := budget.beforeEntry(isDir); err != nil {
			return err
		}
		cleanDest, err := resolveExtractOutputPath(baseDir, name)
		if err != nil {
			return err
		}
		if cleanDest == "" {
			return progress.advance(0)
		}
		if isDir {
			if err := ensurePathComponentsWithinRoot(baseDir, cleanDest, true); err != nil {
				return errExtractInvalidPath
			}
			if err := dirCache.ensure(cleanDest); err != nil {
				return err
			}
			if err := ensurePathComponentsWithinRoot(baseDir, cleanDest, true); err != nil {
				return errExtractInvalidPath
			}
			return progress.advance(0)
		}
		if info.Open == nil {
			return progress.advance(0)
		}
		f, err := info.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info.FileInfo != nil {
			mode = info.Mode()
		}
		writtenBytes, writeErr := writeExtractedFile(baseDir, cleanDest, mode, f, dirCache, overwrite, budget)
		closeErr := f.Close()
		if writeErr != nil {
			if errors.Is(writeErr, os.ErrExist) {
				result.skipped++
				return progress.advance(0)
			}
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if writtenBytes <= 0 && info.FileInfo != nil {
			writtenBytes = info.Size()
		}
		return progress.advance(writtenBytes)
	})
	if err != nil {
		return nil, err
	}
	if err := progress.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

func HandleApiFileExtract(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileExtractRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if relativePath == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}

	archivePath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	if err := ensureResolvedPathWithinInstanceRoot(sp, archivePath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, err)
		return
	}
	if archiveInfo.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
		return
	}

	targetRoot, targetRel, err := resolveInstanceFilePath(sp, req.TargetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetAbs := targetRoot
	if targetRel != "" {
		targetAbs = filepath.Join(targetRoot, filepath.FromSlash(targetRel))
	}
	if strings.TrimSpace(targetAbs) == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.EmptyDest))
		return
	}
	if req.ExtractHere {
		info, err := os.Stat(targetAbs)
		if err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		if !info.IsDir() {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.DestinationNotDirectory))
			return
		}
		if err := ensureResolvedPathWithinInstanceRoot(sp, targetAbs); err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		if err := ensurePathComponentsWithinRoot(rootPath, targetAbs, true); err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errExtractInvalidPath)
			return
		}
	} else {
		if req.Overwrite {
			if err := ensureResolvedPathWithinInstanceRoot(sp, targetAbs); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
					return
				}
			}
			if info, err := os.Stat(targetAbs); err == nil {
				if !info.IsDir() {
					web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.DestinationNotDirectory))
					return
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				web.WriteAPIError(w, http.StatusBadRequest, msg.CheckExtractTargetPathFailed, err)
				return
			}
		} else {
			if err := ensureNewPathWithinInstanceRoot(sp, targetAbs); err != nil {
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
				return
			}
			if _, err := os.Stat(targetAbs); err == nil {
				web.WriteAPIError(w, http.StatusConflict, msg.ExtractTargetDirectoryExists, nil)
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				web.WriteAPIError(w, http.StatusBadRequest, msg.CheckExtractTargetPathFailed, err)
				return
			}
		}
		if _, err := os.Stat(targetAbs); err != nil && !errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.CheckExtractTargetPathFailed, err)
			return
		}
	}

	archiveName := filepath.Base(archivePath)
	defaultDirName := stripArchiveSuffix(archiveName)
	if !req.ExtractHere && strings.TrimSpace(filepath.Base(targetAbs)) == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.ExtractDirectoryNameRequired, nil)
		return
	}
	if req.ExtractHere && strings.TrimSpace(defaultDirName) == "" {
		defaultDirName = archiveName
	}

	web.MarkRequestRouteKind(w, "sse")
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	web.LogWebAccess(w, r, http.StatusOK)

	keepAliveStop := make(chan struct{})
	defer close(keepAliveStop)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := sse.SendComment(); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			case <-keepAliveStop:
				return
			}
		}
	}()

	if err := sendFileExtractProgress(sse, "identifying", 0, 0, 0); err != nil {
		web.MarkAPIError(w, http.StatusInternalServerError, msg.WriteExtractProgressFailed, err)
		return
	}

	format, _, err := archives.Identify(r.Context(), archiveName, nil)
	if err != nil {
		message := identifyArchiveErrorMessage(err)
		web.MarkAPIError(w, http.StatusBadRequest, message, err)
		sendFileExtractFailure(sse, message)
		return
	}
	result, err := extractArchiveWithFormat(r.Context(), format, archivePath, targetAbs, req.ExtractHere, req.Overwrite, sse)
	if err != nil {
		message := extractArchiveErrorMessage(err)
		web.MarkAPIError(w, http.StatusBadRequest, message, err)
		sendFileExtractFailure(sse, message)
		return
	}
	_ = sse.SendEvent("end", map[string]interface{}{"stage": "completed", "percent": 100, "skipped": result.skipped})
}
