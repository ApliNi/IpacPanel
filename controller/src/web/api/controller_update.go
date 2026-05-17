package api

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
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
	web "IpacPanel/controller/src/web"
	"IpacPanel/daemon/version"

	"gopkg.in/yaml.v3"
)

const (
	controllerUpdateUploadID      = "controller-update"
	controllerUpdateMaxBinarySize = 512 * 1024 * 1024
)

type controllerUpdateVersionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
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

func validateControllerUpdatePackageName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("更新压缩包文件名不能为空, 只支持 .zip 文件")
	}
	if strings.HasSuffix(name, string(os.PathSeparator)) || filepath.Base(filepath.Clean(name)) != name {
		return fmt.Errorf("更新压缩包文件名无效, 只支持 .zip 文件")
	}
	if strings.ToLower(filepath.Ext(name)) != ".zip" {
		return fmt.Errorf("更新文件类型无效, 只支持 .zip 压缩包")
	}
	return nil
}

func parseControllerVersion(binaryPath string) (*controllerUpdateVersionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("版本检查超时: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("版本检查失败: %w", err)
	}
	var wrapper struct {
		Version controllerUpdateVersionInfo `yaml:"version"`
	}
	if err := yaml.Unmarshal(output, &wrapper); err != nil {
		return nil, fmt.Errorf("版本输出解析失败: %w", err)
	}
	if wrapper.Version.Role != "controller" {
		return nil, fmt.Errorf("版本角色无效: %s", wrapper.Version.Role)
	}
	if wrapper.Version.DaemonProtocol != version.DaemonProtocol {
		return nil, fmt.Errorf("守护协议不匹配, expected=%d, got=%d", version.DaemonProtocol, wrapper.Version.DaemonProtocol)
	}
	return &wrapper.Version, nil
}

func extractControllerFromZip(zipPath string, targetPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开发布压缩包失败: %w", err)
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
			return fmt.Errorf("发布压缩包中的管理进程二进制文件类型无效: %s", zipEntry.Name)
		}
		if zipEntry.UncompressedSize64 > controllerUpdateMaxBinarySize {
			return fmt.Errorf("发布压缩包中的管理进程二进制文件过大: %s", zipEntry.Name)
		}
		candidate = zipEntry
		break
	}
	if candidate == nil {
		return fmt.Errorf("发布压缩包中未找到 %s", expectedName)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	src, err := candidate.Open()
	if err != nil {
		return fmt.Errorf("读取发布压缩包中的管理进程二进制失败: %w", err)
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
		return fmt.Errorf("提取发布压缩包中的管理进程二进制失败: %w", err)
	}
	if written > controllerUpdateMaxBinarySize {
		return fmt.Errorf("发布压缩包中的管理进程二进制文件过大: %s", candidate.Name)
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

func prepareControllerUpdateBinary(uploadPath string, tempDir string) (string, *controllerUpdateVersionInfo, error) {
	extractDir := filepath.Join(tempDir, "extracted-controller")
	extractedPath := filepath.Join(extractDir, controllerBinaryName())
	if err := extractControllerFromZip(uploadPath, extractedPath); err != nil {
		return "", nil, fmt.Errorf("更新压缩包无效: %w", err)
	}
	versionInfo, err := parseControllerVersion(extractedPath)
	if err != nil {
		return "", nil, fmt.Errorf("发布压缩包中的管理进程二进制无效: %w", err)
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
	if _, ok := web.RequireAdminAuthedUserWithMethod(w, r, "只有管理员可以查看面板更新状态", http.MethodGet); !ok {
		return
	}
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
	if !web.RequireCSRFFromRequest(w, r) {
		return
	}
	authedUser, ok := web.RequireAdminAuthedUserWithMethod(w, r, "只有管理员可以上传面板更新", http.MethodPost)
	if !ok {
		return
	}
	var req controllerUpdateInitRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	if err := validateControllerUpdatePackageName(req.Name); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), err)
		return
	}
	chunkSize, chunkCount, err := validateUploadPlan(req.Size, req.ChunkSize, req.ChunkCount)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadParamsInvalid, err)
		return
	}
	updateDir := controllerUpdateDir()
	if err := os.MkdirAll(updateDir, 0755); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, "创建更新目录失败", err)
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
		Kind:           fileUploadKindFile,
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
	if !web.RequireCSRFFromRequest(w, r) {
		return
	}
	authedUser, ok := web.RequireAdminAuthedUserWithMethod(w, r, "只有管理员可以上传面板更新", http.MethodPost)
	if !ok {
		return
	}
	uploadID := strings.TrimSpace(r.URL.Query().Get("upload_id"))
	if uploadID != controllerUpdateUploadID {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	index, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("index")))
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, "分片索引无效", err)
		return
	}
	session, ok := acquireUploadSession(uploadID)
	if !ok {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	defer releaseUploadSession(session)
	if err := requireControllerUpdateSession(session, authedUser.User); err != nil {
		web.WriteAPIError(w, http.StatusForbidden, err.Error(), err)
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
		web.WriteAPIError(w, http.StatusInternalServerError, msg.UploadSessionInvalid, nil)
		return
	}
	chunkLock.Lock()
	defer chunkLock.Unlock()
	uploads.mu.RLock()
	alreadyReceived := isUploadChunkReceivedLocked(session, index)
	uploads.mu.RUnlock()
	if alreadyReceived {
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
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkSizeMismatch, err)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
		return
	}
	var extra [1]byte
	if n, err := r.Body.Read(extra[:]); n > 0 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkSizeMismatch, fmt.Errorf(msg.ReceivedMoreThanFmt, plan.Size))
		return
	} else if err != nil && !errors.Is(err, io.EOF) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
		return
	}
	markUploadChunkReceived(session, index)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiControllerUpdateUploadComplete(w http.ResponseWriter, r *http.Request) {
	if !web.RequireCSRFFromRequest(w, r) {
		return
	}
	authedUser, ok := web.RequireAdminAuthedUserWithMethod(w, r, "只有管理员可以上传面板更新", http.MethodPost)
	if !ok {
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
		web.WriteAPIError(w, http.StatusForbidden, err.Error(), err)
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
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), err)
		return
	}
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
		completionStatus = uploadSessionCanceled
		writeUploadCanceled(w)
		return
	}
	if err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, "提交更新文件失败", err)
		return
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(finalPath, 0755)
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

func HandleApiControllerUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !web.RequireCSRFFromRequest(w, r) {
		return
	}
	if _, ok := web.RequireAdminAuthedUserWithMethod(w, r, "只有管理员可以应用面板更新", http.MethodPost); !ok {
		return
	}
	path := controllerUpdateBinaryPath()
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		web.WriteAPIError(w, http.StatusConflict, "没有待应用的更新文件", err)
		return
	}
	if _, err := parseControllerVersion(path); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, "更新文件无效", err)
		return
	}
	web.WriteOK(w, map[string]bool{"restarting": true})
	requestControllerShutdown()
}
