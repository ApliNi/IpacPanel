package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	fileArchiveCopyBufferSize = 128 * 1024
	fileArchiveTokenTTL       = 5 * time.Minute
	fileArchiveCleanupEvery   = time.Minute
)

type fileArchiveRequest struct {
	Instance string          `json:"instance"`
	Include  []fileBatchRule `json:"include"`
	Exclude  []fileBatchRule `json:"exclude"`
}

type fileArchiveCreateResponse struct {
	DownloadURL string `json:"download_url"`
	Filename    string `json:"filename"`
}

type fileArchiveDownloadToken struct {
	User        string
	Instance    string
	RootPath    string
	RootReal    string
	ArchiveBase string
	Filename    string
	Include     []instancefs.ArchiveRule
	Exclude     []instancefs.ArchiveRule
	ExpiresAt   time.Time
}

type fileArchiveTokenStore struct {
	mu          sync.Mutex
	tokens      map[string]fileArchiveDownloadToken
	lastCleanup time.Time
}

var archiveTokens = fileArchiveTokenStore{tokens: make(map[string]fileArchiveDownloadToken)}

func (s *fileArchiveTokenStore) create(value fileArchiveDownloadToken) (string, error) {
	token, err := newArchiveTokenString()
	if err != nil {
		return "", err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastCleanup) >= fileArchiveCleanupEvery {
		s.cleanupLocked(now)
	}
	s.tokens[token] = value
	return token, nil
}

func (s *fileArchiveTokenStore) consume(token string, user string) (fileArchiveDownloadToken, bool) {
	var empty fileArchiveDownloadToken
	if token == "" || user == "" {
		return empty, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastCleanup) >= fileArchiveCleanupEvery {
		s.cleanupLocked(now)
	}
	value, ok := s.tokens[token]
	if !ok {
		return empty, false
	}
	delete(s.tokens, token)
	if value.User != user || now.After(value.ExpiresAt) {
		return empty, false
	}
	return value, true
}

func (s *fileArchiveTokenStore) cleanupLocked(now time.Time) {
	for token, value := range s.tokens {
		if now.After(value.ExpiresAt) {
			delete(s.tokens, token)
		}
	}
	s.lastCleanup = now
}

func newArchiveTokenString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func archiveDebugLogf(format string, args ...interface{}) {
	if !cfg.GetDebug() {
		return
	}
	log.Printf(format, args...)
}

func HandleApiFileArchive(w http.ResponseWriter, r *http.Request) {
	var req fileArchiveRequest
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
	web.MarkRequestAction(w, "archive")

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	rootPath := fs.RootPath()
	rootReal, err := fs.EvalRootReal()
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	include, err := fs.ResolveArchiveRules(toInstanceArchiveRules(req.Include), true)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if len(include) == 0 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}
	exclude, err := fs.ResolveArchiveRules(toInstanceArchiveRules(req.Exclude), false)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	archiveBase, archiveName, err := fs.ResolveArchiveLayout(sp.InstanceSnapshot().Name, include)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	token, err := archiveTokens.create(fileArchiveDownloadToken{
		User:        authedUser.User,
		Instance:    strings.TrimSpace(req.Instance),
		RootPath:    rootPath,
		RootReal:    rootReal,
		ArchiveBase: archiveBase,
		Filename:    archiveName,
		Include:     include,
		Exclude:     exclude,
		ExpiresAt:   time.Now().Add(fileArchiveTokenTTL),
	})
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateTokenFailed, err)
		return
	}

	web.WriteOK(w, fileArchiveCreateResponse{DownloadURL: "/api/file/archive/download?token=" + token, Filename: archiveName})
}

func HandleApiFileArchiveDownload(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	archive, ok := archiveTokens.consume(token, authedUser.User)
	if !ok {
		web.WriteAPIError(w, http.StatusNotFound, msg.TargetNotFound, nil)
		return
	}
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, archive.Instance)
	if !ok {
		return
	}
	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusNotFound, msg.TargetNotFound, nil)
		return
	}
	currentRootPath := fs.RootPath()
	currentRootReal, err := fs.EvalRootReal()
	if err != nil {
		web.WriteAPIError(w, http.StatusNotFound, msg.TargetNotFound, nil)
		return
	}
	if !instancefs.SameCleanPath(currentRootPath, archive.RootPath) || !instancefs.SameCleanPath(currentRootReal, archive.RootReal) {
		web.WriteAPIError(w, http.StatusNotFound, msg.TargetNotFound, nil)
		return
	}
	web.MarkRequestAction(w, "archive-download")
	excludes := newFileBatchExcludeMatcherFromResolved(archive.Exclude)

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": archive.Filename})
	if disposition == "" {
		disposition = fmt.Sprintf("attachment; filename=%q", archive.Filename)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	web.LogWebAccess(w, r, http.StatusOK)

	zw := zip.NewWriter(w)
	buffer := make([]byte, fileArchiveCopyBufferSize)
	for _, rule := range archive.Include {
		select {
		case <-r.Context().Done():
			_ = zw.Close()
			return
		default:
		}
		if excludes.excludes(rule.Path, rule.IsDir) {
			continue
		}
		if rule.IsDir {
			if err := writeArchiveDirectory(r, w, zw, buffer, archive.RootReal, archive.ArchiveBase, rule.Path, excludes); err != nil {
				log.Printf(msg.ArchiveDownloadStoppedLogFmt, err)
				web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
				_ = zw.Close()
				return
			}
			continue
		}
		if err := writeArchiveFile(w, zw, buffer, archive.RootReal, archive.ArchiveBase, rule.Path); err != nil {
			log.Printf(msg.ArchiveDownloadStoppedLogFmt, err)
			web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
			_ = zw.Close()
			return
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf(msg.ArchiveZipCloseFailedLogFmt, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
	}
}

func toInstanceArchiveRules(rules []fileBatchRule) []instancefs.ArchiveRule {
	converted := make([]instancefs.ArchiveRule, 0, len(rules))
	for _, rule := range rules {
		converted = append(converted, instancefs.ArchiveRule{Path: rule.Path, IsDir: rule.IsDir})
	}
	return converted
}

func newFileBatchExcludeMatcherFromResolved(exclude []instancefs.ArchiveRule) fileBatchExcludeMatcher {
	rules := make([]fileBatchRule, 0, len(exclude))
	for _, rule := range exclude {
		rules = append(rules, fileBatchRule{Path: rule.Path, IsDir: rule.IsDir})
	}
	return newFileBatchExcludeMatcherFromAbsoluteRules(rules)
}

func writeArchiveDirectory(r *http.Request, w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, dirPath string, excludes fileBatchExcludeMatcher) error {
	err := filepath.WalkDir(dirPath, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			archiveDebugLogf(msg.ArchiveSkippedWalkEntryLogFmt, p, walkErr)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		default:
		}
		info, err := d.Info()
		if err != nil {
			archiveDebugLogf(msg.ArchiveSkippedStatEntryLogFmt, p, err)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			archiveDebugLogf(msg.ArchiveSkippedSymlinkEntryLogFmt, p)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		isDir := info.IsDir()
		if excludes.excludes(p, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir {
			return writeArchiveDirEntry(w, zw, archiveBase, p, info)
		}
		if err := writeArchiveFileWithInfo(w, zw, buffer, rootReal, archiveBase, p, info); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, r.Context().Err()) {
			return err
		}
		log.Printf(msg.ArchiveWalkFailedLogFmt, dirPath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}

func writeArchiveDirEntry(w http.ResponseWriter, zw *zip.Writer, archiveBase string, dirPath string, info os.FileInfo) error {
	entryName, ok := instancefs.SafeArchiveEntryName(archiveBase, dirPath)
	if !ok {
		archiveDebugLogf(msg.ArchiveSkippedUnsafeDirectoryEntryLogFmt, dirPath)
		return nil
	}
	entryName = ensureTrailingSlash(entryName)
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedDirectoryHeaderLogFmt, dirPath, err)
		return nil
	}
	header.Name = entryName
	header.Method = zip.Store
	if _, err := zw.CreateHeader(header); err != nil {
		log.Printf(msg.ArchiveWriteDirectoryEntryFailedLogFmt, dirPath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}

func writeArchiveFile(w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, filePath string) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedStatFileLogFmt, filePath, err)
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		archiveDebugLogf(msg.ArchiveSkippedNonRegularFileLogFmt, filePath)
		return nil
	}
	return writeArchiveFileWithInfo(w, zw, buffer, rootReal, archiveBase, filePath, info)
}

func writeArchiveFileWithInfo(w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, filePath string, info os.FileInfo) error {
	if err := instancefs.EnsureArchiveFileWithinRootStatic(rootReal, filePath); err != nil {
		archiveDebugLogf(msg.ArchiveSkippedEscapedFilePathLogFmt, filePath, err)
		return nil
	}
	file, err := os.Open(filePath)
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedOpenFileLogFmt, filePath, err)
		return nil
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedOpenedFileStatLogFmt, filePath, err)
		return nil
	}
	latestInfo, err := os.Lstat(filePath)
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedLatestFileStatLogFmt, filePath, err)
		return nil
	}
	if latestInfo.Mode()&os.ModeSymlink != 0 || !latestInfo.Mode().IsRegular() || !openedInfo.Mode().IsRegular() || !os.SameFile(openedInfo, latestInfo) {
		archiveDebugLogf(msg.ArchiveSkippedChangedOrNonRegularFileLogFmt, filePath)
		return nil
	}
	if err := instancefs.EnsureArchiveFileWithinRootStatic(rootReal, filePath); err != nil {
		archiveDebugLogf(msg.ArchiveSkippedEscapedOpenedFilePathLogFmt, filePath, err)
		return nil
	}
	entryName, ok := instancefs.SafeArchiveEntryName(archiveBase, filePath)
	if !ok {
		archiveDebugLogf(msg.ArchiveSkippedUnsafeFileEntryLogFmt, filePath)
		return nil
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		archiveDebugLogf(msg.ArchiveSkippedFileHeaderLogFmt, filePath, err)
		return nil
	}
	header.Name = entryName
	header.Method = zip.Deflate
	writer, err := zw.CreateHeader(header)
	if err != nil {
		log.Printf(msg.ArchiveCreateFileEntryFailedLogFmt, filePath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	if _, err := io.CopyBuffer(writer, file, buffer); err != nil {
		log.Printf(msg.ArchiveCopyFileFailedLogFmt, filePath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}
