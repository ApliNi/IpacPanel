package api

import (
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"

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

func extractArchiveWithFormat(ctx context.Context, fs *instancefs.InstanceFS, format archives.Format, archivePath string, targetAbs string, extractHere bool, overwrite bool, sse *web.SSEWriter) (*extractResult, error) {
	archiveReader, err := openArchiveReader(format, archivePath)
	if err != nil {
		return nil, err
	}
	defer archiveReader.Close()

	extraction, ok := format.(archives.Extraction)
	if !ok {
		return nil, errArchiveFormatUnsupported
	}

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
		cleanDest, err := fs.ResolveExtractOutputPath(baseDir, name)
		if err != nil {
			return err
		}
		if cleanDest == "" {
			return progress.advance(0)
		}
		if isDir {
			if err := fs.EnsureExtractDirectory(baseDir, cleanDest, dirCache); err != nil {
				return err
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
		writtenBytes, writeErr := func() (int64, error) {
			bufPtr := extractCopyBufferPool.Get().(*[]byte)
			defer extractCopyBufferPool.Put(bufPtr)
			return fs.WriteExtractedFile(ctx, baseDir, cleanDest, mode, f, dirCache, overwrite, *bufPtr)
		}()
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
	var req fileExtractRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
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

	format, _, err := archives.Identify(r.Context(), archiveName, nil)
	if err != nil {
		message := identifyArchiveErrorMessage(err)
		web.MarkAPIError(w, http.StatusBadRequest, message, err)
		sendFileExtractFailure(sse, message)
		return
	}
	result, err := extractArchiveWithFormat(r.Context(), fs, format, archivePath, targetAbs, req.ExtractHere, req.Overwrite, sse)
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
