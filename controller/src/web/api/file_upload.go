package api

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"

	web "IpacPanel/controller/src/web"

	process "IpacPanel/controller/src/process"

	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"IpacPanel/controller/src/compat"
)

type uploadManager struct {
	mu       sync.RWMutex
	sessions map[string]*fileUploadSession
}

var uploads = &uploadManager{
	sessions: make(map[string]*fileUploadSession),
}

var uploadCommitMu sync.Mutex

func resetUploadSessions() {
	sessions := make([]*fileUploadSession, 0)
	uploads.mu.Lock()
	for _, session := range uploads.sessions {
		if session != nil {
			sessions = append(sessions, session)
		}
	}
	uploads.sessions = make(map[string]*fileUploadSession)
	uploads.mu.Unlock()
	for _, session := range sessions {
		if session.TempDir != "" {
			_ = file.RemoveRegisteredTempDir(session.TempDir)
		}
	}
}

func ResetUploadSessions() {
	resetUploadSessions()
}

const (
	maxOpenFileSize        = 10 * 1024 * 1024
	maxUploadChunkSize     = 10 * 1024 * 1024
	maxUploadChunkCount    = 4096
	uploadChunkLockStripes = 64
	uploadIDHeaderName     = "X-Ipac-Upload-Id"
	uploadChunkHeaderName  = "X-Ipac-Chunk-Index"
	uploadInstanceHeader   = "X-Ipac-Instance"
)

type uploadSessionStatus string

type uploadScope string

const (
	uploadSessionActive     uploadSessionStatus = "active"
	uploadSessionCompleting uploadSessionStatus = "completing"
	uploadSessionCommitted  uploadSessionStatus = "committed"
	uploadSessionCanceled   uploadSessionStatus = "canceled"
)

const (
	uploadScopeInstanceFile     uploadScope = "instance_file"
	uploadScopeControllerUpdate uploadScope = "controller_update"
)

type uploadChunkPlan struct {
	Index  int
	Offset int64
	Size   int64
}

type fileUploadInitRequest struct {
	Instance   string `json:"instance"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ChunkSize  int64  `json:"chunk_size"`
	ChunkCount int    `json:"chunk_count"`
	Overwrite  bool   `json:"overwrite"`
}

type fileUploadInitResponse struct {
	UploadID string `json:"upload_id"`
}

type fileUploadCompleteRequest struct {
	Instance string `json:"instance"`
	UploadID string `json:"upload_id"`
}

type fileUploadAbortRequest struct {
	Instance string `json:"instance"`
	UploadID string `json:"upload_id"`
}

type fileUploadSession struct {
	UploadID        string
	Scope           uploadScope
	OwnerUser       string
	InstanceName    string
	DirPath         string
	FileName        string
	TargetPath      string
	TempDir         string
	StagePath       string
	Size            int64
	ChunkSize       int64
	ChunkCount      int
	Overwrite       bool
	CreatedAt       time.Time
	LastChunkAt     time.Time
	UploadedCount   int
	UploadedChunks  []uint64
	ChunkLocks      []sync.Mutex
	ActiveRequests  int32
	CancelRequested bool
	Status          uploadSessionStatus
	CompleteDone    chan struct{}
	CompleteOnce    sync.Once
}

func getUploadChunkLock(session *fileUploadSession, index int) *sync.Mutex {
	if session == nil || len(session.ChunkLocks) == 0 {
		return nil
	}
	if index < 0 {
		index = 0
	}
	return &session.ChunkLocks[index%len(session.ChunkLocks)]
}

func isUploadSessionCanceled(session *fileUploadSession) bool {
	if session == nil {
		return true
	}
	uploads.mu.RLock()
	canceled := session.CancelRequested || session.Status == uploadSessionCanceled
	uploads.mu.RUnlock()
	return canceled
}

func signalUploadCompletion(session *fileUploadSession) {
	if session == nil || session.CompleteDone == nil {
		return
	}
	session.CompleteOnce.Do(func() {
		close(session.CompleteDone)
	})
}

func resetUploadCompletionSignal(session *fileUploadSession) {
	if session == nil {
		return
	}
	session.CompleteDone = make(chan struct{})
	session.CompleteOnce = sync.Once{}
}

func writeUploadCanceled(w http.ResponseWriter) {
	web.WriteAPIError(w, http.StatusGone, msg.UploadCanceled, nil)
}

func isUploadRequestCanceled(r *http.Request) bool {
	if r == nil {
		return false
	}
	err := r.Context().Err()
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return true
}

func newUploadedChunkBitset(chunkCount int) []uint64 {
	if chunkCount <= 0 {
		return nil
	}
	words := (chunkCount + 63) / 64
	return make([]uint64, words)
}

func acquireUploadSession(uploadID string) (*fileUploadSession, bool) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, false
	}
	now := time.Now()
	uploads.mu.Lock()
	session, ok := uploads.sessions[uploadID]
	if !ok || session == nil {
		uploads.mu.Unlock()
		return nil, false
	}
	if session.CancelRequested || session.Status == uploadSessionCanceled || session.Status == uploadSessionCompleting || session.Status == uploadSessionCommitted {
		uploads.mu.Unlock()
		return nil, false
	}
	atomic.AddInt32(&session.ActiveRequests, 1)
	session.LastChunkAt = now
	uploads.mu.Unlock()
	return session, true
}

func releaseUploadSession(session *fileUploadSession) {
	if session == nil {
		return
	}
	left := atomic.AddInt32(&session.ActiveRequests, -1)
	if left != 0 {
		return
	}
	if !isUploadSessionCanceled(session) {
		return
	}

	// CancelRequested: try to cleanup immediately when no active requests.
	tempDir := session.TempDir
	uploads.mu.Lock()
	if current, ok := uploads.sessions[session.UploadID]; ok && current == session {
		delete(uploads.sessions, session.UploadID)
	}
	uploads.mu.Unlock()
	if tempDir != "" {
		_ = file.RemoveRegisteredTempDir(tempDir)
	}
}

func removeUploadTempDirIfIdle(session *fileUploadSession) bool {
	if session == nil || atomic.LoadInt32(&session.ActiveRequests) != 0 {
		return false
	}
	if session.TempDir != "" {
		_ = file.RemoveRegisteredTempDir(session.TempDir)
	}
	return true
}

func uploadStagePath(session *fileUploadSession, plan uploadChunkPlan) (string, error) {
	if session == nil {
		return "", errors.New(msg.UploadSessionRequired)
	}
	stageRoot := strings.TrimSpace(session.StagePath)
	if stageRoot == "" {
		return "", errors.New(msg.UploadTempDirMissing)
	}
	return stageRoot, nil
}

func writeUploadChunkToStage(session *fileUploadSession, plan uploadChunkPlan, src io.Reader) error {
	stagePath, err := uploadStagePath(session, plan)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(stagePath), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()
	buf := make([]byte, 256*1024)
	written := int64(0)
	for written < plan.Size {
		want := int64(len(buf))
		remaining := plan.Size - written
		if remaining < want {
			want = remaining
		}
		n, readErr := io.ReadFull(src, buf[:want])
		if n > 0 {
			if _, err := out.WriteAt(buf[:n], plan.Offset+written); err != nil {
				return err
			}
			written += int64(n)
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func drainUploadRequestBody(src io.Reader) {
	if src == nil {
		return
	}
	_, _ = io.Copy(io.Discard, src)
}

func syncUploadStageFile(stagePath string, expectedSize int64) error {
	stagePath = strings.TrimSpace(stagePath)
	if stagePath == "" {
		return errors.New(msg.UploadTempDirMissing)
	}
	if expectedSize < 0 {
		return errors.New(msg.InvalidFileSize)
	}
	if err := os.MkdirAll(filepath.Dir(stagePath), 0755); err != nil {
		return err
	}
	openFlag := os.O_RDWR
	if expectedSize == 0 {
		openFlag |= os.O_CREATE
	}
	f, err := os.OpenFile(stagePath, openFlag, 0644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New(msg.UploadTargetIsDirectory)
	}
	if info.Size() != expectedSize {
		return fmt.Errorf(msg.ExpectedGotFmt, expectedSize, info.Size())
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func commitUploadStageFileWithinRoot(rootPath string, tempPath string, targetPath string, overwrite bool) (bool, error) {
	tempPath = strings.TrimSpace(tempPath)
	targetPath = strings.TrimSpace(targetPath)
	if tempPath == "" || targetPath == "" {
		return false, errors.New(msg.EmptyDest)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, tempPath, true); err != nil {
		return false, err
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return false, err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, errors.New(msg.PathOutsideInstanceRoot)
		}
		if info.IsDir() {
			return false, errors.New(msg.UploadTargetIsDirectory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if overwrite {
		if err := compat.ReplaceFileAtomic(tempPath, targetPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
			if renameErr := compat.RenameNoReplace(tempPath, targetPath); renameErr != nil {
				return false, renameErr
			}
		}
	} else {
		if err := compat.RenameNoReplace(tempPath, targetPath); err != nil {
			return false, err
		}
	}

	committed := true
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		_ = os.Remove(targetPath)
		return false, err
	}
	if err := file.SyncDir(filepath.Dir(targetPath)); err != nil {
		return committed, err
	}
	return committed, nil
}

func touchUploadSessionChunk(uploadID string) {
	if strings.TrimSpace(uploadID) == "" {
		return
	}
	now := time.Now()
	uploads.mu.Lock()
	if session, ok := uploads.sessions[uploadID]; ok && session != nil {
		session.LastChunkAt = now
	}
	uploads.mu.Unlock()
}

func markUploadChunkReceived(session *fileUploadSession, index int) {
	if session == nil || strings.TrimSpace(session.UploadID) == "" {
		return
	}
	if index < 0 {
		return
	}
	now := time.Now()
	uploads.mu.Lock()
	current, ok := uploads.sessions[session.UploadID]
	if !ok || current != session {
		uploads.mu.Unlock()
		return
	}
	if index >= session.ChunkCount {
		uploads.mu.Unlock()
		return
	}
	word := index / 64
	bit := uint(index % 64)
	if word >= 0 && word < len(session.UploadedChunks) {
		mask := uint64(1) << bit
		if (session.UploadedChunks[word] & mask) == 0 {
			session.UploadedChunks[word] |= mask
			session.UploadedCount += 1
		}
	}
	session.LastChunkAt = now
	uploads.mu.Unlock()
}

func isUploadChunkReceivedLocked(session *fileUploadSession, index int) bool {
	if session == nil || index < 0 || index >= session.ChunkCount {
		return false
	}
	word := index / 64
	bit := uint(index % 64)
	if word < 0 || word >= len(session.UploadedChunks) {
		return false
	}
	mask := uint64(1) << bit
	return (session.UploadedChunks[word] & mask) != 0
}

func listMissingUploadChunks(session *fileUploadSession, limit int) []int {
	if session == nil || session.ChunkCount <= 0 {
		return nil
	}
	if limit <= 0 {
		limit = 32
	}
	missing := make([]int, 0)
	for i := 0; i < session.ChunkCount && len(missing) < limit; i++ {
		word := i / 64
		bit := uint(i % 64)
		mask := uint64(1) << bit
		if word < 0 || word >= len(session.UploadedChunks) {
			missing = append(missing, i)
			continue
		}
		if (session.UploadedChunks[word] & mask) == 0 {
			missing = append(missing, i)
		}
	}
	return missing
}

func CleanupUploadTempDir() {
	sessions := make([]*fileUploadSession, 0)
	uploads.mu.Lock()
	for id, session := range uploads.sessions {
		if session == nil {
			delete(uploads.sessions, id)
			continue
		}
		sessions = append(sessions, session)
	}
	uploads.sessions = make(map[string]*fileUploadSession)
	uploads.mu.Unlock()
	for _, session := range sessions {
		if session.TempDir != "" {
			_ = file.RemoveRegisteredTempDir(session.TempDir)
		}
	}
}

func createUploadTempDir(targetPath string) (string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return "", errors.New(msg.EmptyDest)
	}
	return file.CreateTempDir(filepath.Dir(targetPath), 0700)
}

func createUploadID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(randomBytes), nil
}

func setUploadSession(session *fileUploadSession) {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	uploads.sessions[session.UploadID] = session
}

func replaceUploadSession(session *fileUploadSession) *fileUploadSession {
	if session == nil || strings.TrimSpace(session.UploadID) == "" {
		return nil
	}
	now := time.Now()
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	old := uploads.sessions[session.UploadID]
	if old != nil {
		old.CancelRequested = true
		old.Status = uploadSessionCanceled
		old.LastChunkAt = now
		signalUploadCompletion(old)
	}
	uploads.sessions[session.UploadID] = session
	return old
}

func deleteUploadSession(uploadID string) {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	delete(uploads.sessions, uploadID)
}

func loadUploadSession(uploadID string) (*fileUploadSession, bool) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, false
	}
	uploads.mu.RLock()
	defer uploads.mu.RUnlock()
	session, ok := uploads.sessions[uploadID]
	if !ok || session == nil {
		return nil, false
	}
	return session, true
}

func cancelUploadSession(uploadID string, validate func(*fileUploadSession) error) (string, bool, error) {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return "", false, errors.New(msg.UploadIDRequired)
	}
	now := time.Now()
	var tempDir string
	var shouldRemove bool
	uploads.mu.Lock()
	session, ok := uploads.sessions[uploadID]
	if !ok || session == nil {
		uploads.mu.Unlock()
		return "", false, nil
	}
	if validate != nil {
		if err := validate(session); err != nil {
			uploads.mu.Unlock()
			return "", true, err
		}
	}
	if session.Status == uploadSessionCommitted {
		session.LastChunkAt = now
		uploads.mu.Unlock()
		return "", true, nil
	}
	session.CancelRequested = true
	session.Status = uploadSessionCanceled
	session.LastChunkAt = now
	signalUploadCompletion(session)
	if atomic.LoadInt32(&session.ActiveRequests) == 0 {
		delete(uploads.sessions, uploadID)
		tempDir = session.TempDir
		shouldRemove = true
	}
	uploads.mu.Unlock()
	if shouldRemove && tempDir != "" {
		return tempDir, true, nil
	}
	return "", true, nil
}

func acquireUploadSessionForComplete(uploadID string) (*fileUploadSession, bool) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, false
	}
	now := time.Now()
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	session, ok := uploads.sessions[uploadID]
	if !ok || session == nil {
		return nil, false
	}
	atomic.AddInt32(&session.ActiveRequests, 1)
	session.LastChunkAt = now
	return session, true
}

func beginUploadCompletion(session *fileUploadSession) (uploadSessionStatus, <-chan struct{}) {
	if session == nil {
		return uploadSessionCanceled, nil
	}
	now := time.Now()
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	session.LastChunkAt = now
	status := session.Status
	if session.CancelRequested && status != uploadSessionCommitted {
		session.Status = uploadSessionCanceled
		status = uploadSessionCanceled
	}
	switch status {
	case uploadSessionCommitted:
		return uploadSessionCommitted, session.CompleteDone
	case uploadSessionCanceled:
		return uploadSessionCanceled, nil
	case uploadSessionCompleting:
		return uploadSessionCompleting, session.CompleteDone
	default:
		resetUploadCompletionSignal(session)
		session.Status = uploadSessionCompleting
		return uploadSessionActive, nil
	}
}

func finishUploadCompletion(session *fileUploadSession, status uploadSessionStatus) {
	if session == nil {
		return
	}
	now := time.Now()
	uploads.mu.Lock()
	if current, ok := uploads.sessions[session.UploadID]; ok && current == session {
		session.LastChunkAt = now
		session.Status = status
	}
	uploads.mu.Unlock()
	signalUploadCompletion(session)
}

func failUploadCompletion(session *fileUploadSession) {
	if session == nil {
		return
	}
	now := time.Now()
	var tempDir string
	var shouldRemove bool
	uploads.mu.Lock()
	if current, ok := uploads.sessions[session.UploadID]; ok && current == session {
		session.LastChunkAt = now
		session.CancelRequested = true
		session.Status = uploadSessionCanceled
		if atomic.LoadInt32(&session.ActiveRequests) == 0 {
			delete(uploads.sessions, session.UploadID)
			tempDir = session.TempDir
			shouldRemove = true
		}
	}
	uploads.mu.Unlock()
	signalUploadCompletion(session)
	if shouldRemove && tempDir != "" {
		_ = file.RemoveRegisteredTempDir(tempDir)
	}
}

func resetUploadCompletionToActive(session *fileUploadSession) {
	finishUploadCompletion(session, uploadSessionActive)
}

func writeUploadCompleteSuccess(w http.ResponseWriter, sp *process.InstanceProcess, session *fileUploadSession) {
	if w == nil || sp == nil || session == nil {
		return
	}
	resp, err := buildFileListResponse(sp, session.DirPath, 1, "")
	if err == nil {
		web.WriteOK(w, resp)
		return
	}
	log.Printf(msg.ReadFileListAfterUploadFailedLogFmt, session.UploadID, session.DirPath, err)
	web.WriteOK(w, map[string]any{
		"ok":        true,
		"upload_id": session.UploadID,
		"path":      session.DirPath,
		"name":      session.FileName,
		"status":    string(uploadSessionCommitted),
	})
}

func getInstanceFileTargetPath(sp *process.InstanceProcess, dirPath string, fileName string) (string, string, error) {
	rootPath, relativePath, err := resolveInstanceFilePath(sp, dirPath)
	if err != nil {
		return "", "", err
	}

	targetDir := rootPath
	if relativePath != "" {
		targetDir = filepath.Join(rootPath, filepath.FromSlash(relativePath))
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetDir, false); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(targetDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New(msg.PathOutsideInstanceRoot)
		}
		if !info.IsDir() {
			return "", "", errors.New(msg.PathNotDirectory)
		}
		if err := ensurePathComponentsWithinRoot(rootPath, targetDir, true); err != nil {
			return "", "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}

	return relativePath, filepath.Join(targetDir, fileName), nil
}

func validateUploadPlan(size int64, chunkSize int64, chunkCount int) (int64, int, error) {
	if size < 0 {
		return 0, 0, errors.New(msg.InvalidFileSize)
	}
	if chunkSize <= 0 || chunkSize > maxUploadChunkSize {
		return 0, 0, fmt.Errorf("%w: "+msg.InvalidChunkSizeRangeFmt, errors.New(msg.InvalidChunkSize), maxUploadChunkSize)
	}
	if chunkCount <= 0 {
		return 0, 0, errors.New(msg.InvalidChunkCount)
	}
	expectedCount := int64(1)
	if size > 0 {
		expectedCount = (size + chunkSize - 1) / chunkSize
	}
	if expectedCount <= 0 || expectedCount > maxUploadChunkCount {
		return 0, 0, fmt.Errorf("%w: "+msg.InvalidChunkCountRangeFmt, errors.New(msg.InvalidChunkCount), maxUploadChunkCount)
	}
	if int(expectedCount) != chunkCount {
		return 0, 0, errors.New(msg.ChunkCountMismatch)
	}
	return chunkSize, int(expectedCount), nil
}

func planUploadChunkWrite(session *fileUploadSession, index int) (uploadChunkPlan, error) {
	return planSingleFileUploadChunkWrite(session, index)
}

func planSingleFileUploadChunkWrite(session *fileUploadSession, index int) (uploadChunkPlan, error) {
	if session == nil {
		return uploadChunkPlan{}, errors.New(msg.UploadSessionRequired)
	}
	if index < 0 || index >= session.ChunkCount {
		return uploadChunkPlan{}, errors.New(msg.InvalidChunkIndex)
	}
	if session.ChunkSize <= 0 {
		return uploadChunkPlan{}, errors.New(msg.InvalidChunkSize)
	}
	if session.Size < 0 {
		return uploadChunkPlan{}, errors.New(msg.InvalidFileSize)
	}
	offset := int64(index) * session.ChunkSize
	if offset > session.Size {
		return uploadChunkPlan{}, errors.New(msg.ChunkOffsetOutsideSize)
	}
	expectedSize := session.ChunkSize
	remaining := session.Size - offset
	if remaining < expectedSize {
		expectedSize = remaining
	}
	if session.Size == 0 {
		expectedSize = 0
	}
	if expectedSize < 0 {
		return uploadChunkPlan{}, errors.New(msg.InvalidExpectedChunkSz)
	}
	return uploadChunkPlan{Index: index, Offset: offset, Size: expectedSize}, nil
}

func HandleApiFileUploadInit(w http.ResponseWriter, r *http.Request) {
	var req fileUploadInitRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	name := strings.TrimSpace(req.Instance)
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return
	}

	fileName, err := ensureFileName(req.Name)
	if err != nil {
		writeFileNameValidationError(w, err)
		return
	}

	normalizedSize := req.Size
	normalizedChunkSize, normalizedChunkCount, err := validateUploadPlan(req.Size, req.ChunkSize, req.ChunkCount)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadParamsInvalid, err)
		return
	}

	relativePath, targetPath, err := getInstanceFileTargetPath(sp, req.Path, fileName)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	// 自动创建目标目录（如果不存在）, 减少前端逐级创建目录的请求
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.CreateDirectoryFailed, err)
		return
	}
	if err := ensureCreatedPathWithinInstanceRoot(sp, targetDir); err != nil {
		// 创建后路径逃逸根目录（如父目录被替换为符号链接）, 回滚
		_ = os.Remove(targetDir)
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	if info, err := os.Stat(targetPath); err == nil {
		if info.IsDir() {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadTargetIsDirectory, nil)
			return
		}
		if !req.Overwrite {
			web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.CheckUploadTargetFileFailed, err)
		return
	}

	tempDir, err := createUploadTempDir(targetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}
	tempDirCommitted := false
	defer func() {
		if !tempDirCommitted {
			_ = file.RemoveRegisteredTempDir(tempDir)
		}
	}()
	if err := ensurePathComponentsWithinRoot(filepath.Dir(targetPath), tempDir, true); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateUploadTempDirFailed, err)
		return
	}

	if req.Size > 0 {
		freeTemp, err := compat.GetFreeDiskBytes(tempDir)
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.GetDiskSpaceFailed, err)
			return
		}
		// Do not allow uploading a file that is >= available disk space.
		if uint64(req.Size) >= freeTemp {
			web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.InsufficientDiskSpace, nil)
			return
		}
	}

	uploadID, err := createUploadID()
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateUploadIDFailed, err)
		return
	}

	stagePath := filepath.Join(tempDir, "upload.stage")

	setUploadSession(&fileUploadSession{
		UploadID:     uploadID,
		Scope:        uploadScopeInstanceFile,
		OwnerUser:    authedUser.User,
		InstanceName: name,
		DirPath:      relativePath,
		FileName:     fileName,
		TargetPath:   targetPath,
		TempDir:      tempDir,
		StagePath:    stagePath,
		Size:         normalizedSize,
		ChunkSize:    normalizedChunkSize,
		ChunkCount:   normalizedChunkCount,
		Overwrite:    req.Overwrite,
		CreatedAt:    time.Now(),
		LastChunkAt:  time.Now(),
		UploadedCount: func() int {
			if normalizedChunkCount == 0 || normalizedSize == 0 {
				return normalizedChunkCount
			}
			return 0
		}(),
		UploadedChunks: func() []uint64 {
			bits := newUploadedChunkBitset(normalizedChunkCount)
			if normalizedSize == 0 {
				// For empty files, there is nothing to upload. Mark all chunks as done.
				for i := 0; i < normalizedChunkCount; i++ {
					word := i / 64
					bit := uint(i % 64)
					if word >= 0 && word < len(bits) {
						bits[word] |= uint64(1) << bit
					}
				}
			}
			return bits
		}(),
		ChunkLocks:   make([]sync.Mutex, uploadChunkLockStripes),
		Status:       uploadSessionActive,
		CompleteDone: make(chan struct{}),
	})
	tempDirCommitted = true

	web.WriteOK(w, &fileUploadInitResponse{UploadID: uploadID})
}

func HandleApiFileUploadChunk(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	name, ok := web.RequireAccessibleInstanceNameByName(w, authedUser, r.Header.Get(uploadInstanceHeader))
	if !ok {
		return
	}

	uploadID := strings.TrimSpace(r.Header.Get(uploadIDHeaderName))
	if uploadID == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadIDRequired, nil)
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
	if session.Scope != uploadScopeInstanceFile {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
		return
	}
	if session.InstanceName != name {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
		return
	}
	if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(authedUser.User) {
		web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, nil)
		return
	}

	plan, err := planUploadChunkWrite(session, index)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkParamsInvalid, err)
		return
	}

	// Validate known Content-Length without buffering the whole chunk.
	if r.ContentLength >= 0 && r.ContentLength != plan.Size {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkSizeMismatch, fmt.Errorf(msg.ExpectedGotFmt, plan.Size, r.ContentLength))
		return
	}

	chunkLock := getUploadChunkLock(session, index)
	if chunkLock == nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.UploadSessionInvalid, errors.New("upload chunk lock is nil"))
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
	// Ensure we didn't receive extra bytes beyond expectedSize.
	// This is cheap (reads at most 1 byte) and keeps chunk boundaries strict.
	var extra [1]byte
	if n, err := r.Body.Read(extra[:]); n > 0 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkDataExceedsExpected, fmt.Errorf(msg.ReceivedMoreThanFmt, plan.Size))
		return
	} else if err != nil && !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.UploadChunkTooLarge, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
		return
	}
	// Mark the upload active only after chunk data is written to staging.
	markUploadChunkReceived(session, index)

	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiFileUploadComplete(w http.ResponseWriter, r *http.Request) {
	var req fileUploadCompleteRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	name := strings.TrimSpace(req.Instance)
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return
	}
	req.UploadID = strings.TrimSpace(req.UploadID)
	if req.UploadID == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadIDRequired, nil)
		return
	}

	session, ok := acquireUploadSessionForComplete(req.UploadID)
	if !ok {
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, nil)
		return
	}
	defer releaseUploadSession(session)
	if session.Scope != uploadScopeInstanceFile {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
		return
	}
	if session.InstanceName != name {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
		return
	}
	if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(authedUser.User) {
		web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, nil)
		return
	}
	status, waitCh := beginUploadCompletion(session)
	switch status {
	case uploadSessionCanceled:
		writeUploadCanceled(w)
		return
	case uploadSessionCommitted:
		writeUploadCompleteSuccess(w, sp, session)
		return
	case uploadSessionCompleting:
		if waitCh != nil {
			select {
			case <-waitCh:
			case <-r.Context().Done():
				return
			}
		}
		refreshed, exists := loadUploadSession(req.UploadID)
		if !exists {
			writeUploadCompleteSuccess(w, sp, session)
			return
		}
		session = refreshed
		if session.InstanceName != name {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
			return
		}
		if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(authedUser.User) {
			web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, nil)
			return
		}
		if isUploadSessionCanceled(session) {
			writeUploadCanceled(w)
			return
		}
		if session.Status == uploadSessionCommitted {
			writeUploadCompleteSuccess(w, sp, session)
			return
		}
		if session.Status == uploadSessionActive {
			web.WriteAPIError(w, http.StatusConflict, msg.UploadFinalizingRetryLater, nil)
			return
		}
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionFinished, nil)
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
			failUploadCompletion(session)
		}
	}()
	if isUploadRequestCanceled(r) {
		return
	}

	touchUploadSessionChunk(req.UploadID)

	uploadCommitMu.Lock()
	defer uploadCommitMu.Unlock()

	// Verify all chunks are uploaded before committing.
	if session.ChunkCount > 0 {
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
	}
	if isUploadSessionCanceled(session) {
		writeUploadCanceled(w)
		return
	}
	if isUploadRequestCanceled(r) {
		return
	}

	if err := syncUploadStageFile(session.StagePath, session.Size); err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, err)
		return
	}
	if isUploadSessionCanceled(session) {
		writeUploadCanceled(w)
		return
	}
	if isUploadRequestCanceled(r) {
		return
	}

	if targetInfo, err := os.Stat(session.TargetPath); err == nil {
		if targetInfo.IsDir() {
			completionStatus = uploadSessionActive
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadTargetIsDirectory, nil)
			return
		}
		if !session.Overwrite {
			completionStatus = uploadSessionActive
			web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CheckUploadTargetFileFailed, err)
		return
	}

	// Chunk data is already persisted in the staging file. Commit the staging file
	// with an atomic replace/rename so interrupted uploads never expose partial target files.
	rootPath, err := getInstanceRootPath(sp)
	if err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusInternalServerError, msg.FilePathInvalid, err)
		return
	}
	if err := ensurePathComponentsWithinRoot(rootPath, filepath.Dir(session.TargetPath), true); err != nil {
		completionStatus = uploadSessionActive
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if isUploadSessionCanceled(session) {
		writeUploadCanceled(w)
		return
	}
	if isUploadRequestCanceled(r) {
		return
	}

	var commitErr error
	var stageCommitted bool
	uploads.mu.Lock()
	current, currentOK := uploads.sessions[session.UploadID]
	currentActive := currentOK && current == session && !session.CancelRequested && session.Status != uploadSessionCanceled
	if currentActive {
		stageCommitted, commitErr = commitUploadStageFileWithinRoot(rootPath, session.StagePath, session.TargetPath, session.Overwrite)
		if commitErr == nil || stageCommitted {
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
	if commitErr != nil && !stageCommitted {
		if errors.Is(commitErr, os.ErrExist) {
			completionStatus = uploadSessionActive
			web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			return
		}
		completionStatus = uploadSessionActive
		log.Printf(msg.CompleteUploadWriteFailedLogFmt, session.UploadID, session.TargetPath, commitErr)
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, commitErr)
		return
	}
	if commitErr != nil {
		log.Printf(msg.CompleteUploadWriteFailedLogFmt, session.UploadID, session.TargetPath, commitErr)
	}
	signalUploadCompletion(session)

	// Cleanup session and chunk files.
	if session.TempDir != "" {
		_ = file.RemoveRegisteredTempDir(session.TempDir)
	}
	completionStatus = uploadSessionCommitted
	writeUploadCompleteSuccess(w, sp, session)
}

func HandleApiFileUploadAbort(w http.ResponseWriter, r *http.Request) {
	var req fileUploadAbortRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	name, ok := web.RequireAccessibleInstanceNameByName(w, authedUser, req.Instance)
	if !ok {
		return
	}
	req.UploadID = strings.TrimSpace(req.UploadID)
	if req.UploadID == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadIDRequired, nil)
		return
	}

	// Mark session canceled and cleanup temp file when possible.
	now := time.Now()
	var tempDir string
	var shouldRemove bool

	uploads.mu.Lock()
	session, ok := uploads.sessions[req.UploadID]
	if ok && session != nil {
		if session.Scope != uploadScopeInstanceFile {
			uploads.mu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
			return
		}
		if session.InstanceName != name {
			uploads.mu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
			return
		}
		if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(authedUser.User) {
			uploads.mu.Unlock()
			web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, nil)
			return
		}
		if session.Status == uploadSessionCommitted {
			session.LastChunkAt = now
			uploads.mu.Unlock()
			web.WriteOK(w, map[string]bool{"ok": true})
			return
		}
		session.CancelRequested = true
		session.Status = uploadSessionCanceled
		session.LastChunkAt = now
		signalUploadCompletion(session)
		if atomic.LoadInt32(&session.ActiveRequests) == 0 {
			delete(uploads.sessions, req.UploadID)
			tempDir = session.TempDir
			shouldRemove = true
		}
	}
	uploads.mu.Unlock()

	if shouldRemove && tempDir != "" {
		_ = file.RemoveRegisteredTempDir(tempDir)
	}

	web.WriteOK(w, map[string]bool{"ok": true})
}
