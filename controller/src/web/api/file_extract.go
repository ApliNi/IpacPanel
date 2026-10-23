package api

import (
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"

	web "IpacPanel/controller/src/web"

	"archive/tar"
	"archive/zip"
	"compress/gzip"
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
	if err := sse.SendEvent("progress", payload); err != nil {
		return newFileExtractProgressError(err)
	}
	return nil
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
	if errors.Is(err, errArchiveFormatUnsupported) {
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
	if errors.Is(err, os.ErrPermission) {
		return msg.ExtractPermissionDenied
	}
	if errors.Is(err, os.ErrNotExist) {
		return msg.ExtractSourceNotFound
	}
	if errors.Is(err, os.ErrExist) {
		return msg.ExtractTargetAlreadyExists
	}
	if errors.Is(err, errExtractInvalidPath) {
		return msg.ArchiveContainsInvalidPath
	}
	if errors.Is(err, errArchiveFormatUnsupported) {
		return msg.ArchiveFormatUnsupported
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
	userValid      func() bool
}

func newExtractProgressReporter(sse *web.SSEWriter, total int, userValid func() bool) *extractProgressReporter {
	return &extractProgressReporter{
		sse:         sse,
		total:       total,
		minInterval: sseProgressThrottleInterval,
		userValid:   userValid,
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

const extractCopyBufferSize = 32 * 1024

var extractCopyBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, extractCopyBufferSize)
		return &buf
	},
}

var errExtractInvalidPath = instancefs.ErrArchiveInvalidPath
var errArchiveFormatUnsupported = errors.New(msg.ArchiveFormatUnsupported)
var errFileExtractProgressSend = errors.New(msg.WriteExtractProgressFailed)

type fileExtractProgressError struct {
	err error
}

func newFileExtractProgressError(err error) error {
	if err == nil {
		return nil
	}
	return &fileExtractProgressError{err: err}
}

func (e *fileExtractProgressError) Error() string {
	if e == nil || e.err == nil {
		return msg.WriteExtractProgressFailed
	}
	return fmt.Sprintf("%s: %v", msg.WriteExtractProgressFailed, e.err)
}

func (e *fileExtractProgressError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *fileExtractProgressError) Is(target error) bool {
	return target == errFileExtractProgressSend
}

func (p *extractProgressReporter) send(force bool) error {
	if p == nil || p.sse == nil {
		return nil
	}
	// 进度上报前重验用户: 已禁用或被删除则中止任务.
	if p.userValid != nil && !p.userValid() {
		return newFileExtractProgressError(errUserStreamInactive)
	}
	now := time.Now()
	if !force && !p.lastAt.IsZero() && now.Sub(p.lastAt) < p.minInterval {
		return nil
	}
	p.lastAt = now
	percent := calcExtractProgressPercent(p.current, p.total)
	if p.total <= 0 {
		if force {
			percent = 100
		} else {
			percent = calcExtractProgressPercentByBytes(p.processedBytes)
		}
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
	if err := p.sse.SendEvent("progress", payload); err != nil {
		return newFileExtractProgressError(err)
	}
	return nil
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

type extractResult struct {
	skipped int
}

type archiveKind int

const (
	archiveKindUnknown archiveKind = iota
	archiveKindZip
	archiveKindTarGz
	archiveKindTar
)

func isGzipTar(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return false
	}
	defer gz.Close()

	buf := make([]byte, 512)
	n, err := io.ReadFull(gz, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	buf = buf[:n]
	return len(buf) >= 262 && string(buf[257:262]) == "ustar"
}

func detectArchiveKind(filePath string, fileName string) (archiveKind, error) {
	lowerName := strings.ToLower(strings.TrimSpace(fileName))

	f, err := os.Open(filePath)
	if err != nil {
		return archiveKindUnknown, err
	}
	defer f.Close()

	header := make([]byte, 512)
	n, readErr := io.ReadFull(f, header)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return archiveKindUnknown, readErr
	}
	header = header[:n]

	if len(header) >= 4 && header[0] == 'P' && header[1] == 'K' {
		if (header[2] == 0x03 && header[3] == 0x04) ||
			(header[2] == 0x05 && header[3] == 0x06) ||
			(header[2] == 0x07 && header[3] == 0x08) {
			return archiveKindZip, nil
		}
	}

	if len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b {
		if strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".tgz") || strings.HasSuffix(lowerName, ".tar.gzip") {
			return archiveKindTarGz, nil
		}
		if isGzipTar(filePath) {
			return archiveKindTarGz, nil
		}
		return archiveKindUnknown, errArchiveFormatUnsupported
	}

	if len(header) >= 262 && string(header[257:262]) == "ustar" {
		return archiveKindTar, nil
	}

	if strings.HasSuffix(lowerName, ".zip") {
		return archiveKindZip, nil
	}
	if strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".tgz") || strings.HasSuffix(lowerName, ".tar.gzip") {
		return archiveKindTarGz, nil
	}
	if strings.HasSuffix(lowerName, ".tar") {
		return archiveKindTar, nil
	}

	return archiveKindUnknown, errArchiveFormatUnsupported
}

func extractZipArchive(ctx context.Context, fs *instancefs.InstanceFS, archivePath string, baseDir string, dirCache *instancefs.ExtractDirCache, overwrite bool, sse *web.SSEWriter, userValid func() bool, result *extractResult) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	progress := newExtractProgressReporter(sse, len(reader.File), userValid)

	for _, file := range reader.File {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := strings.TrimSpace(file.Name)
		if name == "" {
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		cleanDest, err := fs.ResolveExtractOutputPath(baseDir, name)
		if err != nil {
			return err
		}
		if cleanDest == "" {
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		fileInfo := file.FileInfo()
		if fileInfo.IsDir() || strings.HasSuffix(file.Name, "/") {
			if err := fs.EnsureExtractDirectory(baseDir, cleanDest, dirCache); err != nil {
				return err
			}
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return err
		}

		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}

		writtenBytes, writeErr := func() (int64, error) {
			bufPtr := extractCopyBufferPool.Get().(*[]byte)
			defer extractCopyBufferPool.Put(bufPtr)
			return fs.WriteExtractedFile(ctx, baseDir, cleanDest, mode, rc, dirCache, overwrite, *bufPtr)
		}()
		closeErr := rc.Close()
		if writeErr != nil {
			if errors.Is(writeErr, os.ErrExist) {
				result.skipped++
				if err := progress.advance(0); err != nil {
					return err
				}
				continue
			}
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if writtenBytes <= 0 {
			writtenBytes = fileInfo.Size()
		}
		if err := progress.advance(writtenBytes); err != nil {
			return err
		}
	}

	return progress.finish()
}

func extractTarArchive(ctx context.Context, fs *instancefs.InstanceFS, archivePath string, isGz bool, baseDir string, dirCache *instancefs.ExtractDirCache, overwrite bool, sse *web.SSEWriter, userValid func() bool, result *extractResult) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var tarReader *tar.Reader
	if isGz {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gzReader.Close()
		tarReader = tar.NewReader(gzReader)
	} else {
		tarReader = tar.NewReader(file)
	}

	progress := newExtractProgressReporter(sse, 0, userValid)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		name := strings.TrimSpace(header.Name)
		if name == "" {
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		cleanDest, err := fs.ResolveExtractOutputPath(baseDir, name)
		if err != nil {
			return err
		}
		if cleanDest == "" {
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		hdrInfo := header.FileInfo()
		if hdrInfo.IsDir() || header.Typeflag == tar.TypeDir || strings.HasSuffix(header.Name, "/") {
			if err := fs.EnsureExtractDirectory(baseDir, cleanDest, dirCache); err != nil {
				return err
			}
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			if err := progress.advance(0); err != nil {
				return err
			}
			continue
		}

		mode := hdrInfo.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}

		writtenBytes, writeErr := func() (int64, error) {
			bufPtr := extractCopyBufferPool.Get().(*[]byte)
			defer extractCopyBufferPool.Put(bufPtr)
			return fs.WriteExtractedFile(ctx, baseDir, cleanDest, mode, tarReader, dirCache, overwrite, *bufPtr)
		}()
		if writeErr != nil {
			if errors.Is(writeErr, os.ErrExist) {
				result.skipped++
				if err := progress.advance(0); err != nil {
					return err
				}
				continue
			}
			return writeErr
		}
		if writtenBytes <= 0 {
			writtenBytes = hdrInfo.Size()
		}
		if err := progress.advance(writtenBytes); err != nil {
			return err
		}
	}

	return progress.finish()
}

func extractArchive(ctx context.Context, fs *instancefs.InstanceFS, kind archiveKind, archivePath string, targetAbs string, extractHere bool, overwrite bool, sse *web.SSEWriter, userValid func() bool) (*extractResult, error) {
	baseDir := targetAbs
	dirCache := fs.NewExtractDirCache()
	result := &extractResult{}
	if !extractHere {
		if err := dirCache.Ensure(baseDir, baseDir); err != nil {
			return nil, err
		}
		if err := ensurePathComponentsWithinRoot(fs.RootPath(), baseDir, true); err != nil {
			return nil, errExtractInvalidPath
		}
	}

	switch kind {
	case archiveKindZip:
		if err := extractZipArchive(ctx, fs, archivePath, baseDir, dirCache, overwrite, sse, userValid, result); err != nil {
			return nil, err
		}
	case archiveKindTarGz:
		if err := extractTarArchive(ctx, fs, archivePath, true, baseDir, dirCache, overwrite, sse, userValid, result); err != nil {
			return nil, err
		}
	case archiveKindTar:
		if err := extractTarArchive(ctx, fs, archivePath, false, baseDir, dirCache, overwrite, sse, userValid, result); err != nil {
			return nil, err
		}
	default:
		return nil, errArchiveFormatUnsupported
	}
	return result, nil
}

func HandleApiFileExtract(w http.ResponseWriter, r *http.Request) {
	var req fileExtractRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	release, ok := web.AcquireSSESlot(r)
	if !ok {
		web.WriteAPIError(w, http.StatusTooManyRequests, msg.TooManyStreams, nil)
		return
	}
	defer release()
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, req.Instance)
	if !ok {
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	archiveSafePath, archiveInfo, err := fs.ResolveExtractSource(req.Path)
	if err != nil {
		var accessErr *instancefs.PathAccessError
		if errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		if errors.As(err, &accessErr) {
			switch accessErr.Kind {
			case instancefs.PathAccessErrorRequired:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
			case instancefs.PathAccessErrorDirectory:
				web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
			case instancefs.PathAccessErrorStat:
				web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, accessErr.Err)
			case instancefs.PathAccessErrorResolve, instancefs.PathAccessErrorWithinRoot:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, accessErr.Err)
			default:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, fmt.Errorf("unknown path access error kind %q: %w", accessErr.Kind, accessErr.Err))
			}
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	archivePath := archiveSafePath.AbsPath()
	if archiveInfo.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
		return
	}

	targetSafePath, err := fs.ResolveExtractTarget(req.TargetPath, req.ExtractHere, req.Overwrite)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			web.WriteAPIError(w, http.StatusConflict, msg.ExtractTargetDirectoryExists, nil)
			return
		}
		if errors.Is(err, instancefs.ErrExtractTargetInvalidPath) || errors.Is(err, errExtractInvalidPath) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetAbs := targetSafePath.AbsPath()

	archiveName := filepath.Base(archivePath)
	defaultDirName := stripArchiveSuffix(archiveName)
	if !req.ExtractHere && strings.TrimSpace(filepath.Base(targetAbs)) == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.ExtractDirectoryNameRequired, nil)
		return
	}
	if req.ExtractHere && strings.TrimSpace(defaultDirName) == "" {
		defaultDirName = archiveName
	}

	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	web.LogWebAccess(w, r, http.StatusOK)

	keepAliveStop := make(chan struct{})
	defer close(keepAliveStop)
	go func() {
		t := time.NewTicker(sseKeepaliveInterval)
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

	kind, err := detectArchiveKind(archivePath, archiveName)
	if err != nil {
		message := identifyArchiveErrorMessage(err)
		web.MarkAPIError(w, http.StatusBadRequest, message, err)
		sendFileExtractFailure(sse, message)
		return
	}
	username := authedUser.User
	result, err := extractArchive(r.Context(), fs, kind, archivePath, targetAbs, req.ExtractHere, req.Overwrite, sse, func() bool { return userStreamActive(username) })
	if err != nil {
		if errors.Is(err, errFileExtractProgressSend) {
			web.MarkAPIError(w, http.StatusInternalServerError, msg.WriteExtractProgressFailed, err)
			return
		}
		message := extractArchiveErrorMessage(err)
		web.MarkAPIError(w, http.StatusBadRequest, message, err)
		sendFileExtractFailure(sse, message)
		return
	}
	if err := sse.SendEvent("end", map[string]interface{}{"stage": "completed", "percent": 100, "skipped": result.skipped}); err != nil {
		web.MarkAPIError(w, http.StatusInternalServerError, msg.WriteExtractProgressFailed, newFileExtractProgressError(err))
	}
}
