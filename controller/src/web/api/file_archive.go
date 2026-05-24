package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
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

type fileArchiveResolvedRule struct {
	Path  string
	IsDir bool
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
	Include     []fileArchiveResolvedRule
	Exclude     []fileArchiveResolvedRule
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

	rootPath, err := getInstanceRootPath(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	rootReal, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, fmt.Errorf(msg.InstanceRootPathInvalidFmt, err))
		return
	}

	include, err := resolveArchiveRules(sp, req.Include, true)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if len(include) == 0 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}
	exclude, err := resolveArchiveRules(sp, req.Exclude, false)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	archiveBase, archiveName, err := resolveArchiveLayout(rootPath, sp.InstanceSnapshot().Name, include)
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
	web.MarkRequestAction(w, "archive-download")
	web.MarkRequestInstance(w, archive.Instance)
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
				log.Printf("archive download stopped: %v", err)
				web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
				_ = zw.Close()
				return
			}
			continue
		}
		if err := writeArchiveFile(w, zw, buffer, archive.RootReal, archive.ArchiveBase, rule.Path); err != nil {
			log.Printf("archive download stopped: %v", err)
			web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
			_ = zw.Close()
			return
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("archive zip close failed: %v", err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
	}
}

func resolveArchiveRules(sp *process.InstanceProcess, rules []fileBatchRule, requireExisting bool) ([]fileArchiveResolvedRule, error) {
	resolved := make([]fileArchiveResolvedRule, 0, len(rules))
	for _, rule := range rules {
		rootPath, relPath, err := resolveInstanceFilePath(sp, rule.Path)
		if err != nil {
			return nil, err
		}
		if relPath == "" {
			return nil, errors.New(msg.FilePathRequired)
		}
		absPath := filepath.Join(rootPath, filepath.FromSlash(relPath))
		if err := ensurePathComponentsWithinRoot(rootPath, absPath, false); err != nil {
			return nil, err
		}
		if requireExisting {
			info, err := os.Lstat(absPath)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if err := ensureResolvedPathWithinInstanceRoot(sp, absPath); err != nil {
				return nil, err
			}
			resolved = append(resolved, fileArchiveResolvedRule{Path: filepath.Clean(absPath), IsDir: info.IsDir()})
			continue
		}
		resolved = append(resolved, fileArchiveResolvedRule{Path: filepath.Clean(absPath), IsDir: rule.IsDir})
	}
	return resolved, nil
}

func newFileBatchExcludeMatcherFromResolved(exclude []fileArchiveResolvedRule) fileBatchExcludeMatcher {
	rules := make([]fileBatchRule, 0, len(exclude))
	for _, rule := range exclude {
		rules = append(rules, fileBatchRule{Path: rule.Path, IsDir: rule.IsDir})
	}
	return newFileBatchExcludeMatcher(rules)
}

func resolveArchiveLayout(rootPath string, instanceName string, include []fileArchiveResolvedRule) (string, string, error) {
	if len(include) == 0 {
		return "", "", errors.New(msg.FilePathRequired)
	}
	if len(include) == 1 && include[0].IsDir {
		base := filepath.Dir(include[0].Path)
		return base, safeArchiveDownloadName(filepath.Base(include[0].Path)), nil
	}
	commonParent := filepath.Dir(include[0].Path)
	for _, rule := range include[1:] {
		commonParent = commonArchiveParent(commonParent, filepath.Dir(rule.Path))
	}
	rootClean := filepath.Clean(rootPath)
	if filepath.Clean(commonParent) == rootClean {
		return commonParent, safeArchiveDownloadName(instanceName), nil
	}
	return commonParent, safeArchiveDownloadName(filepath.Base(commonParent)), nil
}

func commonArchiveParent(a string, b string) string {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	for {
		if isPathWithinRoot(a, b) {
			return a
		}
		parent := filepath.Dir(a)
		if parent == a {
			return parent
		}
		a = parent
	}
}

func safeArchiveDownloadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" || name == "." || name == ".." {
		name = "archive"
	}
	return name + ".zip"
}

func writeArchiveDirectory(r *http.Request, w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, dirPath string, excludes fileBatchExcludeMatcher) error {
	err := filepath.WalkDir(dirPath, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			archiveDebugLogf("archive skipped walk entry: %s: %v", p, walkErr)
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
			archiveDebugLogf("archive skipped stat entry: %s: %v", p, err)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			archiveDebugLogf("archive skipped symlink entry: %s", p)
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
		log.Printf("archive walk failed: %s: %v", dirPath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}

func writeArchiveDirEntry(w http.ResponseWriter, zw *zip.Writer, archiveBase string, dirPath string, info os.FileInfo) error {
	entryName, ok := safeArchiveEntryName(archiveBase, dirPath)
	if !ok {
		archiveDebugLogf("archive skipped unsafe directory entry: %s", dirPath)
		return nil
	}
	entryName = ensureTrailingSlash(entryName)
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		archiveDebugLogf("archive skipped directory header: %s: %v", dirPath, err)
		return nil
	}
	header.Name = entryName
	header.Method = zip.Store
	if _, err := zw.CreateHeader(header); err != nil {
		log.Printf("archive write directory entry failed: %s: %v", dirPath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}

func writeArchiveFile(w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, filePath string) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		archiveDebugLogf("archive skipped stat file: %s: %v", filePath, err)
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		archiveDebugLogf("archive skipped non-regular file: %s", filePath)
		return nil
	}
	return writeArchiveFileWithInfo(w, zw, buffer, rootReal, archiveBase, filePath, info)
}

func writeArchiveFileWithInfo(w http.ResponseWriter, zw *zip.Writer, buffer []byte, rootReal string, archiveBase string, filePath string, info os.FileInfo) error {
	if err := ensureArchiveFileWithinRoot(rootReal, filePath); err != nil {
		archiveDebugLogf("archive skipped escaped file path: %s: %v", filePath, err)
		return nil
	}
	file, err := os.Open(filePath)
	if err != nil {
		archiveDebugLogf("archive skipped open file: %s: %v", filePath, err)
		return nil
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		archiveDebugLogf("archive skipped opened file stat: %s: %v", filePath, err)
		return nil
	}
	latestInfo, err := os.Lstat(filePath)
	if err != nil {
		archiveDebugLogf("archive skipped latest file stat: %s: %v", filePath, err)
		return nil
	}
	if latestInfo.Mode()&os.ModeSymlink != 0 || !latestInfo.Mode().IsRegular() || !openedInfo.Mode().IsRegular() || !os.SameFile(openedInfo, latestInfo) {
		archiveDebugLogf("archive skipped changed or non-regular file: %s", filePath)
		return nil
	}
	if err := ensureArchiveFileWithinRoot(rootReal, filePath); err != nil {
		archiveDebugLogf("archive skipped escaped opened file path: %s: %v", filePath, err)
		return nil
	}
	entryName, ok := safeArchiveEntryName(archiveBase, filePath)
	if !ok {
		archiveDebugLogf("archive skipped unsafe file entry: %s", filePath)
		return nil
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		archiveDebugLogf("archive skipped file header: %s: %v", filePath, err)
		return nil
	}
	header.Name = entryName
	header.Method = zip.Deflate
	writer, err := zw.CreateHeader(header)
	if err != nil {
		log.Printf("archive create file entry failed: %s: %v", filePath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	if _, err := io.CopyBuffer(writer, file, buffer); err != nil {
		log.Printf("archive copy file failed: %s: %v", filePath, err)
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return err
	}
	return nil
}

func ensureArchiveFileWithinRoot(rootReal string, filePath string) error {
	realPath, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return err
	}
	if !isPathWithinRoot(rootReal, realPath) {
		return errors.New(msg.PathOutsideInstanceRoot)
	}
	return nil
}

func safeArchiveEntryName(basePath string, targetPath string) (string, bool) {
	if filepath.VolumeName(targetPath) != filepath.VolumeName(basePath) {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(basePath), filepath.Clean(targetPath))
	if err != nil || rel == "." || rel == "" {
		return "", false
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	name := filepath.ToSlash(rel)
	if strings.Contains(name, "\x00") || strings.HasPrefix(name, "/") {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || filepath.VolumeName(part) != "" {
			return "", false
		}
	}
	return name, true
}
