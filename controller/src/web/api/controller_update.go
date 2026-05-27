package api

import (
	"IpacPanel/controller/src/web/authz"
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	controllerUpdateDocsMarker    = ".controller-update-docs-ready"
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

type controllerUpdateBinaryIdentity struct {
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
	SHA256Hex string `json:"sha256"`
}

type controllerUpdateInitRequest struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ChunkSize  int64  `json:"chunk_size"`
	ChunkCount int    `json:"chunk_count"`
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

func controllerUpdateDir() string {
	return cfg.ResolveDataPath("update")
}

func controllerUpdateBinaryPath() string {
	return filepath.Join(controllerUpdateDir(), controllerBinaryName())
}

func controllerUpdateDocsStagingDir() string {
	return filepath.Join(controllerUpdateDir(), controllerUpdateDocsStageDir)
}

func controllerUpdateDocsMarkerPath() string {
	return filepath.Join(controllerUpdateDocsStagingDir(), controllerUpdateDocsMarker)
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

func parseControllerVersion(binaryPath string) (*controllerUpdateVersionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf(msg.ControllerVersionCheckTimeoutFmt, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf(msg.ControllerVersionCheckFailedFmt, err)
	}
	var wrapper struct {
		Version controllerUpdateVersionInfo `yaml:"version"`
	}
	if err := yaml.Unmarshal(output, &wrapper); err != nil {
		return nil, fmt.Errorf(msg.ControllerVersionOutputParseFailedFmt, err)
	}
	if wrapper.Version.Role != "controller" {
		return nil, fmt.Errorf(msg.ControllerVersionRoleInvalidFmt, wrapper.Version.Role)
	}
	if wrapper.Version.DaemonProtocol != version.DaemonProtocol {
		return nil, fmt.Errorf(msg.ControllerUpdateDaemonProtocolMismatchFmt, version.DaemonProtocol, wrapper.Version.DaemonProtocol)
	}
	return &wrapper.Version, nil
}

func extractControllerFromZip(zipPath string, targetPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf(msg.OpenControllerReleaseArchiveFailedFmt, err)
	}
	defer reader.Close()

	expectedName := controllerBinaryName()
	var candidate *zip.File
	for _, zipEntry := range reader.File {
		if zipEntry == nil || zipEntry.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(filepath.Clean(zipEntry.Name)) != expectedName {
			continue
		}
		if zipEntry.FileInfo().Mode()&os.ModeType != 0 {
			return fmt.Errorf(msg.ControllerBinaryTypeInvalidFmt, zipEntry.Name)
		}
		if zipEntry.UncompressedSize64 > controllerUpdateMaxBinarySize {
			return fmt.Errorf(msg.ControllerBinaryTooLargeFmt, zipEntry.Name)
		}
		candidate = zipEntry
		break
	}
	if candidate == nil {
		return fmt.Errorf(msg.ControllerBinaryNotFoundFmt, expectedName)
	}

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

func controllerUpdateBinaryIdentityForPath(path string) (*controllerUpdateBinaryIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Mode()&os.ModeType != 0 {
		return nil, fmt.Errorf("管理进程更新文件不是普通文件: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return nil, err
	}
	return &controllerUpdateBinaryIdentity{
		Size:      info.Size(),
		ModTimeNS: info.ModTime().UnixNano(),
		SHA256Hex: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func sameControllerUpdateBinaryIdentity(a *controllerUpdateBinaryIdentity, b *controllerUpdateBinaryIdentity) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Size == b.Size && a.ModTimeNS == b.ModTimeNS && a.SHA256Hex == b.SHA256Hex
}

func writeControllerUpdateDocsMarker(binaryPath string) error {
	identity, err := controllerUpdateBinaryIdentityForPath(binaryPath)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(identity)
	if err != nil {
		return err
	}
	return os.WriteFile(controllerUpdateDocsMarkerPath(), data, 0600)
}

func readControllerUpdateDocsMarker() (*controllerUpdateBinaryIdentity, error) {
	data, err := os.ReadFile(controllerUpdateDocsMarkerPath())
	if err != nil {
		return nil, err
	}
	var identity controllerUpdateBinaryIdentity
	if err := yaml.Unmarshal(data, &identity); err != nil {
		return nil, err
	}
	if identity.Size < 0 || strings.TrimSpace(identity.SHA256Hex) == "" {
		return nil, errors.New("管理进程更新文档暂存标记无效")
	}
	return &identity, nil
}

func extractControllerUpdateDocsFromZip(zipPath string, stagingDir string) (int, error) {
	if err := os.RemoveAll(stagingDir); err != nil {
		return 0, fmt.Errorf("清理管理进程更新文档暂存目录失败: %w", err)
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
			log.Printf("跳过更新压缩包中的文档特殊文件: path=%s", zipEntry.Name)
			continue
		}
		targetPath := filepath.Join(stagingDir, filepath.FromSlash(cleanName))
		if !strings.HasPrefix(targetPath, filepath.Clean(stagingDir)+string(os.PathSeparator)) {
			log.Printf("跳过更新压缩包中的文档非法路径: path=%s", zipEntry.Name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			log.Printf("创建管理进程更新文档暂存目录失败: path=%s err=%v", targetPath, err)
			continue
		}
		src, err := zipEntry.Open()
		if err != nil {
			log.Printf("读取发布压缩包中的文档文件失败: path=%s err=%v", zipEntry.Name, err)
			continue
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			_ = src.Close()
			log.Printf("创建管理进程更新文档暂存文件失败: path=%s err=%v", targetPath, err)
			continue
		}
		_, copyErr := io.Copy(out, src)
		closeOutErr := out.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			log.Printf("提取发布压缩包中的文档文件失败: source=%s target=%s err=%v", zipEntry.Name, targetPath, copyErr)
			_ = os.Remove(targetPath)
			continue
		}
		if closeOutErr != nil {
			log.Printf("关闭管理进程更新文档暂存文件失败: path=%s err=%v", targetPath, closeOutErr)
			_ = os.Remove(targetPath)
			continue
		}
		if closeSrcErr != nil {
			log.Printf("关闭发布压缩包中的文档文件失败: path=%s err=%v", zipEntry.Name, closeSrcErr)
			_ = os.Remove(targetPath)
			continue
		}
		extracted++
	}
	return extracted, nil
}

func cleanupControllerUpdateDocsStaging() {
	if err := os.RemoveAll(controllerUpdateDocsStagingDir()); err != nil {
		log.Printf("清理管理进程更新文档暂存目录失败: %v", err)
	}
}

func applyControllerUpdateStagedDocs() (int, int, int) {
	stagingDir := controllerUpdateDocsStagingDir()
	if info, err := os.Stat(stagingDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("检查管理进程更新文档暂存目录失败: %v", err)
		}
		cleanupControllerUpdateDocsStaging()
		return 0, 0, 0
	} else if !info.IsDir() {
		log.Printf("管理进程更新文档暂存路径不是目录: path=%s", stagingDir)
		cleanupControllerUpdateDocsStaging()
		return 0, 0, 1
	}
	markerIdentity, err := readControllerUpdateDocsMarker()
	if err != nil {
		log.Printf("读取管理进程更新文档暂存标记失败, 跳过文档覆盖: %v", err)
		cleanupControllerUpdateDocsStaging()
		return 0, 0, 0
	}
	currentIdentity, err := controllerUpdateBinaryIdentityForPath(controllerUpdateBinaryPath())
	if err != nil {
		log.Printf("读取当前管理进程待更新文件身份失败, 跳过文档覆盖: %v", err)
		cleanupControllerUpdateDocsStaging()
		return 0, 0, 0
	}
	if !sameControllerUpdateBinaryIdentity(markerIdentity, currentIdentity) {
		log.Printf("管理进程更新文档暂存标记与当前待更新文件不匹配, 跳过文档覆盖")
		cleanupControllerUpdateDocsStaging()
		return 0, 0, 0
	}
	baseDir := cfg.GetAppBaseDir()
	applied := 0
	skipped := 0
	failed := 0
	walkErr := filepath.WalkDir(stagingDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			failed++
			log.Printf("读取管理进程更新文档暂存项失败: path=%s err=%v", path, walkErr)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if path == controllerUpdateDocsMarkerPath() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			failed++
			log.Printf("读取管理进程更新文档暂存项信息失败: path=%s err=%v", path, err)
			return nil
		}
		if info.Mode()&os.ModeType != 0 {
			failed++
			log.Printf("跳过管理进程更新文档特殊文件: path=%s", path)
			return nil
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			failed++
			log.Printf("计算管理进程更新文档相对路径失败: path=%s err=%v", path, err)
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := cleanControllerUpdateZipPath(relSlash); !ok || !isAllowedControllerUpdateDocPath(relSlash) {
			failed++
			log.Printf("跳过管理进程更新文档非法暂存路径: path=%s", relSlash)
			return nil
		}
		targetPath := filepath.Join(baseDir, filepath.FromSlash(relSlash))
		targetInfo, err := os.Stat(targetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				skipped++
				log.Printf("跳过不存在的管理进程更新文档目标: path=%s", targetPath)
			} else {
				failed++
				log.Printf("检查管理进程更新文档目标失败: path=%s err=%v", targetPath, err)
			}
			return nil
		}
		if targetInfo.IsDir() || targetInfo.Mode()&os.ModeType != 0 {
			failed++
			log.Printf("跳过管理进程更新文档非普通目标文件: path=%s", targetPath)
			return nil
		}
		if err := copyControllerUpdateDocFile(path, targetPath, targetInfo.Mode().Perm()); err != nil {
			failed++
			log.Printf("覆盖管理进程更新文档失败: source=%s target=%s err=%v", path, targetPath, err)
			return nil
		}
		applied++
		return nil
	})
	if walkErr != nil {
		failed++
		log.Printf("遍历管理进程更新文档暂存目录失败: %v", walkErr)
	}
	cleanupControllerUpdateDocsStaging()
	log.Printf("管理进程更新文档覆盖完成: applied=%d skipped=%d failed=%d", applied, skipped, failed)
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

func prepareControllerUpdateBinary(uploadPath string, tempDir string) (string, *controllerUpdateVersionInfo, error) {
	extractDir := filepath.Join(tempDir, "extracted-controller")
	extractedPath := filepath.Join(extractDir, controllerBinaryName())
	if err := extractControllerFromZip(uploadPath, extractedPath); err != nil {
		return "", nil, fmt.Errorf(msg.ControllerUpdatePackageInvalidFmt, err)
	}
	versionInfo, err := parseControllerVersion(extractedPath)
	if err != nil {
		return "", nil, fmt.Errorf(msg.ControllerBinaryInvalidFmt, err)
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
	tempDir, _, err := cancelUploadSession(controllerUpdateUploadID, func(session *fileUploadSession) error {
		return requireControllerUpdateSession(session, ownerUser)
	})
	if err == nil && tempDir != "" {
		_ = file.RemoveRegisteredTempDir(tempDir)
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

func HandleApiControllerUpdateStatus(w http.ResponseWriter, r *http.Request) {
	path := controllerUpdateBinaryPath()
	info, err := os.Stat(path)
	pending := err == nil && !info.IsDir()
	web.WriteOK(w, map[string]interface{}{
		"pending": pending,
		"size": func() int64 {
			if pending {
				return info.Size()
			}
			return 0
		}(),
	})
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
	updateDir := controllerUpdateDir()
	if err := os.MkdirAll(updateDir, 0755); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateControllerUpdateDirFailed, err)
		return
	}
	if req.Size > 0 {
		freeBytes, err := compat.GetFreeDiskBytes(updateDir)
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.GetDiskSpaceFailed, err)
			return
		}
		if uint64(req.Size) >= freeBytes {
			web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.InsufficientDiskSpace, nil)
			return
		}
	}
	tempDir, err := file.CreateTempDir(updateDir, 0700)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	stagePath := filepath.Join(tempDir, "controller-update.zip.stage")
	session := &fileUploadSession{
		UploadID:       controllerUpdateUploadID,
		Scope:          uploadScopeControllerUpdate,
		OwnerUser:      authedUser.User,
		FileName:       strings.TrimSpace(req.Name),
		TargetPath:     controllerUpdateBinaryPath(),
		TempDir:        tempDir,
		StagePath:      stagePath,
		Size:           req.Size,
		ChunkSize:      chunkSize,
		ChunkCount:     chunkCount,
		Overwrite:      true,
		CreatedAt:      time.Now(),
		LastChunkAt:    time.Now(),
		UploadedChunks: newUploadedChunkBitset(chunkCount),
		ChunkLocks:     make([]sync.Mutex, uploadChunkLockStripes),
		Status:         uploadSessionActive,
		CompleteDone:   make(chan struct{}),
	}
	old := replaceUploadSession(session)
	if old != nil {
		removeUploadTempDirIfIdle(old)
	}
	web.WriteOK(w, map[string]string{"upload_id": controllerUpdateUploadID})
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
		web.WriteAPIError(w, http.StatusInternalServerError, msg.UploadSessionInvalid, errors.New("controller update upload chunk lock is nil"))
		return
	}
	chunkLock.Lock()
	defer chunkLock.Unlock()
	uploads.mu.RLock()
	alreadyReceived := isUploadChunkReceivedLocked(session, index)
	uploads.mu.RUnlock()
	if alreadyReceived {
		drainUploadRequestBody(r.Body)
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
	if err := syncUploadStageFile(session.StagePath, session.Size); err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, err)
		return
	}
	if isUploadSessionCanceled(session) {
		completionStatus = uploadSessionCanceled
		writeUploadCanceled(w)
		return
	}
	updateBinaryPath, versionInfo, err := prepareControllerUpdateBinary(session.StagePath, session.TempDir)
	if err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusBadRequest, controllerUpdatePrepareUserMessage(err), err)
		return
	}
	stagedDocCount, err := extractControllerUpdateDocsFromZip(session.StagePath, controllerUpdateDocsStagingDir())
	if err != nil {
		log.Printf("管理进程更新文档暂存失败, 将继续提交二进制更新: %v", err)
		cleanupControllerUpdateDocsStaging()
	} else {
		log.Printf("管理进程更新文档暂存完成: count=%d", stagedDocCount)
	}
	docsStaged := err == nil
	finalPath := controllerUpdateBinaryPath()
	uploads.mu.Lock()
	current, currentOK := uploads.sessions[session.UploadID]
	currentActive := currentOK && current == session && !session.CancelRequested && session.Status != uploadSessionCanceled
	if currentActive {
		err = compat.ReplaceFileAtomic(updateBinaryPath, finalPath)
		if err == nil {
			session.LastChunkAt = time.Now()
			session.Status = uploadSessionCommitted
			delete(uploads.sessions, session.UploadID)
		}
	}
	uploads.mu.Unlock()
	if !currentActive {
		cleanupControllerUpdateDocsStaging()
		completionStatus = uploadSessionCanceled
		writeUploadCanceled(w)
		return
	}
	if err != nil {
		cleanupControllerUpdateDocsStaging()
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CommitControllerUpdateFileFailed, err)
		return
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(finalPath, 0755)
	}
	if docsStaged {
		if err := writeControllerUpdateDocsMarker(finalPath); err != nil {
			log.Printf("写入管理进程更新文档暂存标记失败, 将跳过本次文档覆盖: %v", err)
			cleanupControllerUpdateDocsStaging()
		}
	}
	signalUploadCompletion(session)
	if session.TempDir != "" {
		_ = file.RemoveRegisteredTempDir(session.TempDir)
	}
	completionStatus = uploadSessionCommitted
	web.WriteOK(w, map[string]interface{}{
		"pending":         true,
		"version":         versionInfo.Version,
		"daemon_protocol": versionInfo.DaemonProtocol,
	})
}

func controllerUpdatePrepareUserMessage(err error) string {
	if err == nil {
		return msg.ControllerUpdatePackageInvalid
	}
	message := err.Error()
	if strings.Contains(message, msg.ControllerBinaryInvalid) {
		return msg.ControllerBinaryInvalid
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
	tempDir, _, err := cancelUploadSession(controllerUpdateUploadID, func(session *fileUploadSession) error {
		return requireControllerUpdateSession(session, authedUser.User)
	})
	if err != nil {
		writeControllerUpdateSessionError(w, err)
		return
	}
	if tempDir != "" {
		_ = file.RemoveRegisteredTempDir(tempDir)
	}
	cleanupControllerUpdateDocsStaging()
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiControllerUpdateApply(w http.ResponseWriter, r *http.Request) {
	path := controllerUpdateBinaryPath()
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		web.WriteAPIError(w, http.StatusConflict, msg.ControllerUpdateFileMissing, err)
		return
	}
	if _, err := parseControllerVersion(path); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.ControllerUpdateFileInvalid, err)
		return
	}
	applyControllerUpdateStagedDocs()
	if err := process.RestartControllerForUpdate(); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.RestartControllerFailed, err)
		return
	}
	web.WriteOK(w, map[string]bool{"restarting": true})
	requestControllerShutdown()
}
