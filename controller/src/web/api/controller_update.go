package api

import (
	"IpacPanel/controller/src/web/authz"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/compat"
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/process"
	web "IpacPanel/controller/src/web"
	"IpacPanel/daemon/version"

	"gopkg.in/yaml.v3"
)

const (
	controllerUpdateUploadID      = "controller-update"
	controllerUpdateMaxBinarySize = 512 * 1024 * 1024
	controllerUpdateDocsStageDir  = "controller-docs"

	updateRoleController = "controller"
	updateRoleDaemon     = "daemon"
)

var controllerUpdateRootDocNames = map[string]struct{}{
	"README.md":   {},
	"README.txt":  {},
	"README.rst":  {},
	"README":      {},
	"LICENSE":     {},
	"LICENSE.md":  {},
	"LICENSE.txt": {},
	"COPYING":     {},
	"COPYING.md":  {},
	"COPYING.txt": {},
}

type controllerUpdateVersionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
}

type controllerUpdateInitRequest struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ChunkSize   int64  `json:"chunk_size"`
	ChunkCount  int    `json:"chunk_count"`
	ReplaceMode bool   `json:"replace_mode,omitempty"`
}

type controllerUpdateCompleteRequest struct {
	UploadID string `json:"upload_id"`
}

type controllerUpdateAbortRequest struct {
	UploadID string `json:"upload_id"`
}

func controllerBinaryName() string {
	name := "IpacPanel_Controller"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func daemonBinaryName() string {
	name := "IpacPanel"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func replaceModeStagingName(binaryName string) string {
	return binaryName + ".replace"
}

// controllerUpdateDir 返回更新任务的临时根目录 ./data/temp/, 启动时会被整体清空.
func controllerUpdateDir() string {
	return cfg.ResolveDataPath("temp")
}

// updateSelfBinaryPath 返回当前管理进程可执行文件路径, 自替换更新的安装目标.
func updateSelfBinaryPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	return execPath
}

func validateControllerUpdatePackageName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(msg.ControllerUpdatePackageNameRequired)
	}
	if strings.HasSuffix(name, string(os.PathSeparator)) || filepath.Base(filepath.Clean(name)) != name {
		return errors.New(msg.ControllerUpdatePackageNameInvalid)
	}
	if strings.ToLower(filepath.Ext(name)) != ".zip" {
		return errors.New(msg.ControllerUpdatePackageTypeInvalid)
	}
	return nil
}

// controllerUpdateBinaryError 区分管理进程二进制校验失败的具体原因,
// 用户消息 (userMessage) 与内部日志 (err) 分离.
type controllerUpdateBinaryError struct {
	userMessage string
	err         error
}

func (e *controllerUpdateBinaryError) Error() string {
	if e.err != nil {
		return e.userMessage + ": " + e.err.Error()
	}
	return e.userMessage
}

func (e *controllerUpdateBinaryError) Unwrap() error {
	return e.err
}

// parseUpdateBinaryVersion 运行新二进制的 --version 命令 (60 秒超时) 并解析 YAML 输出,
// 校验角色与 expectedRole 一致. 不校验守护进程协议, 允许跨协议版本更新.
func parseUpdateBinaryVersion(binaryPath string, expectedRole string) (*controllerUpdateVersionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, &controllerUpdateBinaryError{userMessage: msg.ControllerBinaryVersionCheckTimeout, err: ctx.Err()}
	}
	if err != nil {
		return nil, &controllerUpdateBinaryError{userMessage: msg.ControllerBinaryVersionCheckFailed, err: err}
	}
	var wrapper struct {
		Version controllerUpdateVersionInfo `yaml:"version"`
	}
	if err := yaml.Unmarshal(output, &wrapper); err != nil {
		return nil, &controllerUpdateBinaryError{userMessage: msg.ControllerBinaryVersionOutputInvalid, err: err}
	}
	if wrapper.Version.Role != expectedRole {
		return nil, &controllerUpdateBinaryError{userMessage: msg.ControllerBinaryRoleInvalid}
	}
	return &wrapper.Version, nil
}

func extractControllerFromZip(zipPath string, targetPath string) error {
	reader, err := openControllerUpdateZipReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	candidate, err := findControllerBinaryInZip(reader, controllerBinaryName())
	if err != nil {
		return err
	}
	return extractControllerZipEntry(candidate, targetPath)
}

func openControllerUpdateZipReader(zipPath string) (*zip.ReadCloser, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf(msg.OpenControllerReleaseArchiveFailedFmt, err)
	}
	return reader, nil
}

func findControllerBinaryInZip(reader *zip.ReadCloser, expectedName string) (*zip.File, error) {
	for _, zipEntry := range reader.File {
		if zipEntry == nil || zipEntry.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(filepath.Clean(zipEntry.Name)) != expectedName {
			continue
		}
		if zipEntry.FileInfo().Mode()&os.ModeType != 0 {
			return nil, fmt.Errorf(msg.ControllerBinaryTypeInvalidFmt, zipEntry.Name)
		}
		if zipEntry.UncompressedSize64 > controllerUpdateMaxBinarySize {
			return nil, fmt.Errorf(msg.ControllerBinaryTooLargeFmt, zipEntry.Name)
		}
		return zipEntry, nil
	}
	return nil, fmt.Errorf(msg.ControllerBinaryNotFoundFmt, expectedName)
}

func extractControllerZipEntry(candidate *zip.File, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	src, err := candidate.Open()
	if err != nil {
		return fmt.Errorf(msg.ReadControllerBinaryFromArchiveFailedFmt, err)
	}
	defer src.Close()

	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	cleanupTarget := true
	defer func() {
		_ = out.Close()
		if cleanupTarget {
			_ = os.Remove(targetPath)
		}
	}()
	written, err := io.Copy(out, io.LimitReader(src, controllerUpdateMaxBinarySize+1))
	if err != nil {
		return fmt.Errorf(msg.ExtractControllerBinaryFromArchiveFailedFmt, err)
	}
	if written > controllerUpdateMaxBinarySize {
		return fmt.Errorf(msg.ControllerBinaryTooLargeFmt, candidate.Name)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(targetPath, 0755)
	}
	cleanupTarget = false
	return nil
}

func cleanControllerUpdateZipPath(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if cleaned == "." || cleaned == "" || strings.Contains(cleaned, ":") || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", false
	}
	return cleaned, true
}

func isAllowedControllerUpdateDocPath(path string) bool {
	if strings.Contains(path, "/") {
		return strings.HasPrefix(path, "doc/") && path != "doc/"
	}
	_, ok := controllerUpdateRootDocNames[path]
	return ok
}

func extractControllerUpdateDocsFromZip(zipPath string, stagingDir string) (int, error) {
	if err := os.RemoveAll(stagingDir); err != nil {
		return 0, fmt.Errorf(msg.CleanupControllerUpdateDocsStagingDirFailedErrFmt, err)
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf(msg.OpenControllerReleaseArchiveFailedFmt, err)
	}
	defer reader.Close()

	extracted := 0
	for _, zipEntry := range reader.File {
		if zipEntry == nil || zipEntry.FileInfo().IsDir() {
			continue
		}
		cleanName, ok := cleanControllerUpdateZipPath(zipEntry.Name)
		if !ok || !isAllowedControllerUpdateDocPath(cleanName) {
			continue
		}
		if zipEntry.FileInfo().Mode()&os.ModeType != 0 {
			log.Printf(msg.SkipControllerUpdateDocSpecialFileLogFmt, zipEntry.Name)
			continue
		}
		targetPath := filepath.Join(stagingDir, filepath.FromSlash(cleanName))
		if !strings.HasPrefix(targetPath, filepath.Clean(stagingDir)+string(os.PathSeparator)) {
			log.Printf(msg.SkipControllerUpdateDocInvalidPathLogFmt, zipEntry.Name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			log.Printf(msg.CreateControllerUpdateDocsStagingDirFailedLogFmt, targetPath, err)
			continue
		}
		src, err := zipEntry.Open()
		if err != nil {
			log.Printf(msg.ReadControllerUpdateDocFromArchiveFailedLogFmt, zipEntry.Name, err)
			continue
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			_ = src.Close()
			log.Printf(msg.CreateControllerUpdateDocsStagingFileFailedLogFmt, targetPath, err)
			continue
		}
		_, copyErr := io.Copy(out, src)
		closeOutErr := out.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			log.Printf(msg.ExtractControllerUpdateDocFromArchiveFailedLogFmt, zipEntry.Name, targetPath, copyErr)
			_ = os.Remove(targetPath)
			continue
		}
		if closeOutErr != nil {
			log.Printf(msg.CloseControllerUpdateDocsStagingFileFailedLogFmt, targetPath, closeOutErr)
			_ = os.Remove(targetPath)
			continue
		}
		if closeSrcErr != nil {
			log.Printf(msg.CloseControllerUpdateDocFromArchiveFailedLogFmt, zipEntry.Name, closeSrcErr)
			_ = os.Remove(targetPath)
			continue
		}
		extracted++
	}
	return extracted, nil
}

func cleanupControllerUpdateDocsStaging(stagingDir string) {
	if err := os.RemoveAll(stagingDir); err != nil {
		log.Printf(msg.CleanupControllerUpdateDocsStagingDirFailedLogFmt, err)
	}
}

func applyControllerUpdateStagedDocs(stagingDir string) (int, int, int) {
	if info, err := os.Stat(stagingDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf(msg.CheckControllerUpdateDocsStagingDirFailedLogFmt, err)
		}
		cleanupControllerUpdateDocsStaging(stagingDir)
		return 0, 0, 0
	} else if !info.IsDir() {
		log.Printf(msg.ControllerUpdateDocsStagingPathNotDirLogFmt, stagingDir)
		cleanupControllerUpdateDocsStaging(stagingDir)
		return 0, 0, 1
	}
	baseDir := cfg.GetAppBaseDir()
	applied := 0
	skipped := 0
	failed := 0
	walkErr := filepath.WalkDir(stagingDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			failed++
			log.Printf(msg.ReadControllerUpdateDocsStagingItemFailedLogFmt, path, walkErr)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			failed++
			log.Printf(msg.ReadControllerUpdateDocsStagingItemInfoFailedLogFmt, path, err)
			return nil
		}
		if info.Mode()&os.ModeType != 0 {
			failed++
			log.Printf(msg.SkipControllerUpdateDocsStagingSpecialFileLogFmt, path)
			return nil
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			failed++
			log.Printf(msg.ControllerUpdateDocsRelativePathFailedLogFmt, path, err)
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := cleanControllerUpdateZipPath(relSlash); !ok || !isAllowedControllerUpdateDocPath(relSlash) {
			failed++
			log.Printf(msg.SkipControllerUpdateDocsInvalidStagingPathLogFmt, relSlash)
			return nil
		}
		targetPath := filepath.Join(baseDir, filepath.FromSlash(relSlash))
		targetInfo, err := os.Stat(targetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				skipped++
				log.Printf(msg.SkipMissingControllerUpdateDocsTargetLogFmt, targetPath)
			} else {
				failed++
				log.Printf(msg.CheckControllerUpdateDocsTargetFailedLogFmt, targetPath, err)
			}
			return nil
		}
		if targetInfo.IsDir() || targetInfo.Mode()&os.ModeType != 0 {
			failed++
			log.Printf(msg.SkipControllerUpdateDocsNonRegularTargetLogFmt, targetPath)
			return nil
		}
		if err := copyControllerUpdateDocFile(path, targetPath, targetInfo.Mode().Perm()); err != nil {
			failed++
			log.Printf(msg.OverwriteControllerUpdateDocsFailedLogFmt, path, targetPath, err)
			return nil
		}
		applied++
		return nil
	})
	if walkErr != nil {
		failed++
		log.Printf(msg.WalkControllerUpdateDocsStagingDirFailedLogFmt, walkErr)
	}
	cleanupControllerUpdateDocsStaging(stagingDir)
	log.Printf(msg.ControllerUpdateDocsApplyCompletedLogFmt, applied, skipped, failed)
	return applied, skipped, failed
}

func copyControllerUpdateDocFile(sourcePath string, targetPath string, mode os.FileMode) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".controller-doc-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		_ = tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.Copy(tempFile, src); err != nil {
		return err
	}
	if err := tempFile.Sync(); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	if err := compat.ReplaceFileAtomic(tempPath, targetPath); err != nil {
		return err
	}
	cleanupTemp = false
	return nil
}

func prepareControllerUpdateBinary(uploadPath string, workDir string, expectedRole string) (string, *controllerUpdateVersionInfo, error) {
	extractDir := filepath.Join(workDir, "extracted-controller")
	extractedPath := filepath.Join(extractDir, controllerBinaryName())
	if err := extractControllerFromZip(uploadPath, extractedPath); err != nil {
		return "", nil, fmt.Errorf("%s: %w", msg.ControllerUpdatePackageInvalid, err)
	}
	versionInfo, err := parseUpdateBinaryVersion(extractedPath, expectedRole)
	if err != nil {
		return "", nil, err
	}
	return extractedPath, versionInfo, nil
}

func requireControllerUpdateSession(session *fileUploadSession, ownerUser string) error {
	if session == nil || session.Scope != uploadScopeControllerUpdate {
		return errors.New(msg.UploadSessionNotFound)
	}
	if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(ownerUser) {
		return errors.New(msg.UploadSessionForbidden)
	}
	return nil
}

func cleanupControllerUpdateUploadSession(ownerUser string) {
	cleanupPath, _, err := cancelUploadSession(controllerUpdateUploadID, func(session *fileUploadSession) error {
		return requireControllerUpdateSession(session, ownerUser)
	})
	if err == nil && cleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(cleanupPath)
	}
}

func failControllerUpdateUploadCompletion(session *fileUploadSession) {
	if session == nil {
		return
	}
	failUploadCompletion(session)
}

func writeControllerUpdateCompletionResult(w http.ResponseWriter, uploadID string) {
	refreshed, exists := loadUploadSession(uploadID)
	if !exists {
		web.WriteOK(w, map[string]bool{"pending": true})
		return
	}
	if isUploadSessionCanceled(refreshed) {
		writeUploadCanceled(w)
		return
	}
	switch refreshed.Status {
	case uploadSessionCommitted:
		web.WriteOK(w, map[string]bool{"pending": true})
	case uploadSessionActive:
		web.WriteAPIError(w, http.StatusConflict, msg.UploadFinalizingRetryLater, nil)
	default:
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionFinished, nil)
	}
}

func HandleApiControllerUpdateUploadInit(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	var req controllerUpdateInitRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	if err := validateControllerUpdatePackageName(req.Name); err != nil {
		writeControllerUpdatePackageNameValidationError(w, err)
		return
	}
	chunkSize, chunkCount, err := validateUploadPlan(req.Size, req.ChunkSize, req.ChunkCount)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadParamsInvalid, err)
		return
	}
	tempRoot := controllerUpdateDir()
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateControllerUpdateDirFailed, err)
		return
	}
	// 每次上传任务使用独立的临时文件夹, 任务结束或管理进程启动时清理.
	taskDir, err := file.CreateRegisteredTempDir(tempRoot, 0755)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	taskDirReady := false
	defer func() {
		if !taskDirReady {
			_ = file.RemoveRegisteredTempPath(taskDir)
		}
	}()
	if req.Size > 0 {
		freeBytes, err := compat.GetFreeDiskBytes(tempRoot)
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.GetDiskSpaceFailed, err)
			return
		}
		if uint64(req.Size) >= freeBytes {
			web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.InsufficientDiskSpace, nil)
			return
		}
	}
	stageFile, stagePath, err := file.CreateRegisteredTempFileForTarget(filepath.Join(taskDir, strings.TrimSpace(req.Name)), 0644)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	stageClosed := false
	stageCommitted := false
	defer func() {
		if !stageClosed {
			_ = stageFile.Close()
		}
		if !stageCommitted {
			_ = file.RemoveRegisteredTempPath(stagePath)
		}
	}()
	if err := stageFile.Truncate(req.Size); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	if err := stageFile.Sync(); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	if err := stageFile.Close(); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	stageClosed = true
	stageInfo, err := os.Lstat(stagePath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	session := &fileUploadSession{
		UploadID:       controllerUpdateUploadID,
		Scope:          uploadScopeControllerUpdate,
		OwnerUser:      authedUser.User,
		FileName:       strings.TrimSpace(req.Name),
		StageRoot:      taskDir,
		StagePath:      stagePath,
		CleanupPath:    taskDir,
		StageInfo:      stageInfo,
		Size:           req.Size,
		ChunkSize:      chunkSize,
		ChunkCount:     chunkCount,
		Overwrite:      true,
		ReplaceMode:    req.ReplaceMode,
		TargetPath:     updateSelfBinaryPath(),
		LastChunkAt:    time.Now(),
		UploadedChunks: newUploadedChunkBitset(chunkCount),
		ChunkLocks:     make([]sync.Mutex, uploadChunkLockStripes),
		Status:         uploadSessionActive,
		CompleteDone:   make(chan struct{}),
	}
	old := replaceUploadSession(session)
	stageCommitted = true
	taskDirReady = true
	if old != nil {
		removeUploadCleanupPathIfIdle(old)
	}
	response := map[string]interface{}{"upload_id": controllerUpdateUploadID}
	if req.ReplaceMode {
		response["replace_mode"] = true
	}
	web.WriteOK(w, response)
}

func HandleApiControllerUpdateUploadChunk(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	uploadID := strings.TrimSpace(r.Header.Get(uploadIDHeaderName))
	if uploadID != controllerUpdateUploadID {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	index, err := strconv.Atoi(strings.TrimSpace(r.Header.Get(uploadChunkHeaderName)))
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidChunkIndex, err)
		return
	}
	session, ok := acquireUploadSession(uploadID)
	if !ok {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	defer releaseUploadSession(session)
	if err := requireControllerUpdateSession(session, authedUser.User); err != nil {
		writeControllerUpdateSessionError(w, err)
		return
	}
	plan, err := planSingleFileUploadChunkWrite(session, index)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkParamsInvalid, err)
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != plan.Size {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkSizeMismatch, fmt.Errorf(msg.ExpectedGotFmt, plan.Size, r.ContentLength))
		return
	}
	chunkLock := getUploadChunkLock(session, index)
	if chunkLock == nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.UploadSessionInvalid, errors.New(msg.ControllerUpdateUploadChunkLockNil))
		return
	}
	chunkLock.Lock()
	defer chunkLock.Unlock()
	uploads.mu.RLock()
	alreadyReceived := isUploadChunkReceivedLocked(session, index)
	uploads.mu.RUnlock()
	if alreadyReceived {
		if err := validateUploadStageFileIdentity(session); err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInvalid, err)
			return
		}
		if err := drainUploadRequestBodyLimited(r.Body, plan.Size); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.UploadChunkTooLarge, nil)
				return
			}
			web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
			return
		}
		web.WriteOK(w, map[string]bool{"ok": true})
		return
	}
	if err := writeUploadChunkToStage(session, plan, r.Body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.UploadChunkTooLarge, nil)
			return
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkIncomplete, err)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
		return
	}
	var extra [1]byte
	if n, err := r.Body.Read(extra[:]); n > 0 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkDataExceedsExpected, fmt.Errorf(msg.ReceivedMoreThanFmt, plan.Size))
		return
	} else if err != nil && !errors.Is(err, io.EOF) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
		return
	}
	markUploadChunkReceived(session, index)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiControllerUpdateUploadComplete(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	var req controllerUpdateCompleteRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.UploadID) != controllerUpdateUploadID {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	session, ok := acquireUploadSessionForComplete(req.UploadID)
	if !ok {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	defer releaseUploadSession(session)
	if err := requireControllerUpdateSession(session, authedUser.User); err != nil {
		writeControllerUpdateSessionError(w, err)
		return
	}
	status, waitCh := beginUploadCompletion(session)
	switch status {
	case uploadSessionCanceled:
		writeUploadCanceled(w)
		return
	case uploadSessionCommitted:
		web.WriteOK(w, map[string]bool{"pending": true})
		return
	case uploadSessionCompleting:
		if waitCh != nil {
			select {
			case <-waitCh:
			case <-r.Context().Done():
				return
			}
		}
		writeControllerUpdateCompletionResult(w, req.UploadID)
		return
	}
	completionStatus := uploadSessionActive
	defer func() {
		switch completionStatus {
		case uploadSessionCommitted:
			return
		case uploadSessionActive:
			resetUploadCompletionToActive(session)
		default:
			failControllerUpdateUploadCompletion(session)
		}
	}()
	uploads.mu.RLock()
	received := session.UploadedCount
	chunkCount := session.ChunkCount
	missing := listMissingUploadChunks(session, 32)
	uploads.mu.RUnlock()
	if received < chunkCount {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusConflict, msg.UploadChunksIncomplete, fmt.Errorf(msg.MissingChunksFmt, chunkCount-received, chunkCount, missing))
		return
	}
	if isUploadSessionCanceled(session) {
		completionStatus = uploadSessionCanceled
		writeUploadCanceled(w)
		return
	}
	if err := syncUploadStageFile(session, session.Size); err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, err)
		return
	}
	if isUploadSessionCanceled(session) {
		completionStatus = uploadSessionCanceled
		writeUploadCanceled(w)
		return
	}
	// 上传暂存文件所在的任务目录, 提取与文档暂存都在其中, 结束后整体清理.
	workDir := filepath.Dir(session.StagePath)
	workDirRemoved := false
	defer func() {
		if !workDirRemoved {
			_ = file.RemoveRegisteredTempPath(workDir)
		}
	}()
	var updateBinaryPath string
	var versionInfo *controllerUpdateVersionInfo
	var err error
	if !session.ReplaceMode {
		updateBinaryPath, versionInfo, err = prepareControllerUpdateBinary(session.StagePath, workDir, updateRoleController)
		if err != nil {
			completionStatus = uploadSessionActive
			web.WriteAPIError(w, http.StatusBadRequest, controllerUpdatePrepareUserMessage(err), err)
			return
		}
		if versionInfo.DaemonProtocol != version.DaemonProtocol {
			completionStatus = uploadSessionActive
			err := errors.New(msg.ControllerProtocolMismatch)
			web.WriteAPIError(w, http.StatusBadRequest, err.Error(), err)
			return
		}
	}
	docsStagingDir := filepath.Join(workDir, controllerUpdateDocsStageDir)
	stagedDocCount, err := extractControllerUpdateDocsFromZip(session.StagePath, docsStagingDir)
	if err != nil {
		log.Printf(msg.ControllerUpdateDocsStagingFailedContinueLogFmt, err)
	} else {
		log.Printf(msg.ControllerUpdateDocsStagingCompletedLogFmt, stagedDocCount)
	}
	docsStaged := err == nil
	// 清理任务目录, 无论清理是否成功都不再由 defer 重试.
	removeWorkDir := func() {
		_ = file.RemoveRegisteredTempPath(workDir)
		workDirRemoved = true
	}
	// 提交会话并应用文档, 返回 false 表示会话已被取消或被其他请求接管.
	commitAndApplyDocs := func() bool {
		if !commitControllerUpdateSession(session) {
			removeWorkDir()
			completionStatus = uploadSessionCanceled
			writeUploadCanceled(w)
			return false
		}
		completionStatus = uploadSessionCommitted
		signalUploadCompletion(session)
		if docsStaged {
			applyControllerUpdateStagedDocs(docsStagingDir)
		}
		// 文档应用完成后整体删除任务目录.
		if session.CleanupPath != "" {
			if err := file.RemoveRegisteredTempPath(session.CleanupPath); err != nil {
				log.Printf(msg.RemoveControllerUpdateTaskDirFailedLogFmt, err)
			}
			workDirRemoved = true
		}
		return true
	}
	if session.ReplaceMode {
		if err := installControllerUpdateReplace(session.StagePath, workDir); err != nil {
			removeWorkDir()
			completionStatus = uploadSessionActive
			writeControllerUpdateInstallError(w, err)
			return
		}
		if !commitAndApplyDocs() {
			return
		}
		web.WriteOK(w, map[string]interface{}{"replaced": true})
		return
	}
	// 常规模式: 校验通过后管理进程替换自身并退出, 由守护进程重启加载新版本.
	execPath, err := os.Executable()
	if err != nil {
		removeWorkDir()
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CommitControllerUpdateFileFailed, err)
		return
	}
	if err := installReplacedBinary(updateBinaryPath, execPath); err != nil {
		removeWorkDir()
		completionStatus = uploadSessionActive
		writeControllerUpdateInstallError(w, err)
		return
	}
	if !commitAndApplyDocs() {
		return
	}
	web.WriteOK(w, map[string]interface{}{"restarting": true})
	// 先让响应发出, 再通知守护进程立即重启并退出自身.
	if err := process.NotifyControllerUpdateExit(); err != nil {
		log.Printf(msg.NotifyControllerUpdateExitFailedLogFmt, err)
	}
	requestControllerShutdown()
}

// writeControllerUpdateInstallError 统一响应安装阶段错误:
// controllerUpdateReplaceError 携带的状态码与用户消息, 其余按内部错误处理.
func writeControllerUpdateInstallError(w http.ResponseWriter, err error) {
	var replaceErr *controllerUpdateReplaceError
	if errors.As(err, &replaceErr) {
		web.WriteAPIError(w, replaceErr.status, replaceErr.userMessage, replaceErr.err)
		return
	}
	web.WriteAPIError(w, http.StatusInternalServerError, msg.CommitControllerUpdateFileFailed, err)
}

// controllerUpdatePrepareUserMessage 将准备阶段错误映射为用户消息:
// 二进制校验错误使用其细分消息, 其余一律按更新压缩包无效处理.
func controllerUpdatePrepareUserMessage(err error) string {
	if err == nil {
		return msg.ControllerUpdatePackageInvalid
	}
	var binaryErr *controllerUpdateBinaryError
	if errors.As(err, &binaryErr) {
		return binaryErr.userMessage
	}
	return msg.ControllerUpdatePackageInvalid
}

func HandleApiControllerUpdateUploadAbort(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	var req controllerUpdateAbortRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.UploadID) != controllerUpdateUploadID {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	cleanupPath, _, err := cancelUploadSession(controllerUpdateUploadID, func(session *fileUploadSession) error {
		return requireControllerUpdateSession(session, authedUser.User)
	})
	if err != nil {
		writeControllerUpdateSessionError(w, err)
		return
	}
	if cleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(cleanupPath)
	}
	web.WriteOK(w, map[string]bool{"ok": true})
}

// controllerUpdateReplaceError 携带替换模式安装失败时返回的 HTTP 状态码与用户消息,
// 用户消息 (userMessage) 与内部日志 (err) 分离.
type controllerUpdateReplaceError struct {
	status      int
	userMessage string
	err         error
}

func (e *controllerUpdateReplaceError) Error() string {
	if e.err != nil {
		return e.userMessage + ": " + e.err.Error()
	}
	return e.userMessage
}

func (e *controllerUpdateReplaceError) Unwrap() error {
	return e.err
}

// commitControllerUpdateSession 将更新会话标记为已提交并移出会话表,
// 常规模式与替换模式共用, 返回 false 表示会话已被取消或被其他请求接管.
func commitControllerUpdateSession(session *fileUploadSession) bool {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	current, ok := uploads.sessions[session.UploadID]
	if !ok || current != session || session.CancelRequested || session.Status == uploadSessionCanceled {
		return false
	}
	session.LastChunkAt = time.Now()
	session.Status = uploadSessionCommitted
	delete(uploads.sessions, session.UploadID)
	return true
}
// 先提取到 workDir 并以 *.replace 名暂存在任务目录 data/temp/ 中, 再原子替换可执行文件
// 所在目录中的旧文件; 每个二进制先并行运行 --version 校验角色 (守护进程=daemon, 管理进程=controller),
// 不校验守护进程协议, 不比较版本号新旧. 原文件不存在 *.old 备份时先重命名备份,
// 已有备份则直接尝试覆盖 (Windows 上运行中的守护进程镜像无法覆盖时整体失败, 不回滚).
func installControllerUpdateReplace(stageZipPath string, workDir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	baseDir := filepath.Dir(execPath)
	reader, err := openControllerUpdateZipReader(stageZipPath)
	if err != nil {
		return &controllerUpdateReplaceError{status: http.StatusBadRequest, userMessage: msg.ControllerUpdatePackageInvalid, err: err}
	}
	defer reader.Close()

	type controllerUpdateReplaceTarget struct {
		zipName      string
		workPath     string
		stagedPath   string
		targetPath   string
		isDaemon     bool
		expectedRole string
	}
	daemonName := daemonBinaryName()
	controllerName := controllerBinaryName()
	targets := []controllerUpdateReplaceTarget{
		{
			zipName:      daemonName,
			workPath:     filepath.Join(workDir, daemonName),
			stagedPath:   filepath.Join(workDir, replaceModeStagingName(daemonName)),
			targetPath:   filepath.Join(baseDir, daemonName),
			isDaemon:     true,
			expectedRole: updateRoleDaemon,
		},
		{
			zipName:      controllerName,
			workPath:     filepath.Join(workDir, controllerName),
			stagedPath:   filepath.Join(workDir, replaceModeStagingName(controllerName)),
			targetPath:   filepath.Join(baseDir, controllerName),
			expectedRole: updateRoleController,
		},
	}
	for i := range targets {
		candidate, err := findControllerBinaryInZip(reader, targets[i].zipName)
		if err != nil {
			if targets[i].isDaemon {
				return &controllerUpdateReplaceError{status: http.StatusBadRequest, userMessage: fmt.Sprintf(msg.ControllerReplaceDaemonBinaryNotFoundFmt, targets[i].zipName)}
			}
			return &controllerUpdateReplaceError{status: http.StatusBadRequest, userMessage: msg.ControllerUpdatePackageInvalid, err: err}
		}
		if err := extractControllerZipEntry(candidate, targets[i].workPath); err != nil {
			return &controllerUpdateReplaceError{status: http.StatusBadRequest, userMessage: msg.ControllerUpdatePackageInvalid, err: err}
		}
	}
	// 并行运行各二进制的 --version 角色校验, 任一失败即整体中止.
	versionErrs := make([]error, len(targets))
	var versionWg sync.WaitGroup
	for i := range targets {
		versionWg.Add(1)
		go func(i int) {
			defer versionWg.Done()
			_, err := parseUpdateBinaryVersion(targets[i].workPath, targets[i].expectedRole)
			versionErrs[i] = err
		}(i)
	}
	versionWg.Wait()
	for i := range targets {
		if versionErrs[i] != nil {
			return &controllerUpdateReplaceError{status: http.StatusBadRequest, userMessage: controllerUpdatePrepareUserMessage(versionErrs[i]), err: versionErrs[i]}
		}
	}
	for i := range targets {
		// 暂存到任务目录中, 覆盖已有同名暂存文件.
		if err := compat.ReplaceFileAtomic(targets[i].workPath, targets[i].stagedPath); err != nil {
			return &controllerUpdateReplaceError{status: http.StatusInternalServerError, userMessage: msg.CommitControllerUpdateFileFailed, err: err}
		}
	}
	for i := range targets {
		if err := installReplacedBinary(targets[i].stagedPath, targets[i].targetPath); err != nil {
			var replaceErr *controllerUpdateReplaceError
			if errors.As(err, &replaceErr) {
				return replaceErr
			}
			return &controllerUpdateReplaceError{status: http.StatusInternalServerError, userMessage: msg.CommitControllerUpdateFileFailed, err: err}
		}
	}
	return nil
}

// installReplacedBinary 将新二进制安装到 targetPath: 原文件不存在 *.old 备份时先重命名备份,
// 已有备份则直接尝试覆盖 (Windows 上目标为运行中的守护进程镜像时会失败), 然后原子替换.
func installReplacedBinary(sourcePath string, targetPath string) error {
	oldPath := targetPath + ".old"
	if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(targetPath, oldPath); err != nil {
			return &controllerUpdateReplaceError{
				status:      http.StatusBadRequest,
				userMessage: fmt.Sprintf(msg.ControllerReplaceBackupRenameFailedFmt, filepath.Base(targetPath), filepath.Base(oldPath), err),
				err:         err,
			}
		}
	} else if err != nil {
		return &controllerUpdateReplaceError{status: http.StatusInternalServerError, userMessage: msg.CommitControllerUpdateFileFailed, err: err}
	}
	if err := compat.ReplaceFileAtomic(sourcePath, targetPath); err != nil {
		return &controllerUpdateReplaceError{status: http.StatusInternalServerError, userMessage: msg.CommitControllerUpdateFileFailed, err: err}
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(targetPath, 0755)
	}
	return nil
}

// CleanupReplacedOldBinaries 删除替换模式更新遗留的 *.old 备份二进制,
// 在管理进程每次成功启动后尽力而为地执行: 文件不存在则忽略, 其他失败仅记日志.
func CleanupReplacedOldBinaries() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(execPath)
	for _, name := range []string{daemonBinaryName() + ".old", controllerBinaryName() + ".old"} {
		oldPath := filepath.Join(baseDir, name)
		if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf(msg.RemoveReplacedOldBinaryFailedLogFmt, oldPath, err)
		}
	}
}

// CleanupUpdateTempDir 清空更新任务临时根目录 ./data/temp/,
// 在管理进程每次成功启动后执行, 失败仅记日志.
func CleanupUpdateTempDir() {
	if err := os.RemoveAll(controllerUpdateDir()); err != nil {
		log.Printf(msg.CleanupUpdateTempDirFailedLogFmt, controllerUpdateDir(), err)
		return
	}
	if err := os.MkdirAll(controllerUpdateDir(), 0755); err != nil {
		log.Printf(msg.CleanupUpdateTempDirFailedLogFmt, controllerUpdateDir(), err)
	}
}
