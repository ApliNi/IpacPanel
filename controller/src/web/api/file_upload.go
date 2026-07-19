package api

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/instancefs"
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
	mu             sync.RWMutex
	sessions       map[string]*fileUploadSession
	cleanupRunning bool
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
		if session.CleanupPath != "" {
			_ = file.RemoveRegisteredTempPath(session.CleanupPath)
		}
	}
}

func cleanupExpiredCommittedUploadSessions() (hasRemaining bool) {
	now := time.Now()
	var idleSessions []*fileUploadSession
	uploads.mu.Lock()
	for id, session := range uploads.sessions {
		if session == nil {
			delete(uploads.sessions, id)
			continue
		}
		status := session.Status
		if atomic.LoadInt32(&session.ActiveRequests) != 0 {
			hasRemaining = true
			continue
		}
		if status == uploadSessionCommitted && now.Sub(session.LastChunkAt) > uploadSessionIdleTTL {
			delete(uploads.sessions, id)
			continue
		}
		if status == uploadSessionActive && now.Sub(session.LastChunkAt) > uploadSessionIdleTTL {
			delete(uploads.sessions, id)
			if session.CleanupPath != "" {
				idleSessions = append(idleSessions, session)
			}
			log.Printf("upload session %s idle timeout, cleaned up", session.UploadID)
			continue
		}
		if status == uploadSessionCanceled {
			delete(uploads.sessions, id)
			if session.CleanupPath != "" {
				idleSessions = append(idleSessions, session)
			}
			log.Printf("upload session %s canceled, cleaned up", session.UploadID)
			continue
		}
		hasRemaining = true
	}
	uploads.mu.Unlock()
	for _, session := range idleSessions {
		_ = file.RemoveRegisteredTempPath(session.CleanupPath)
	}
	return
}

func uploadSessionStatusSnapshot(session *fileUploadSession) (uploadSessionStatus, bool) {
	if session == nil {
		return uploadSessionCanceled, true
	}
	uploads.mu.RLock()
	status := session.Status
	cancelRequested := session.CancelRequested
	uploads.mu.RUnlock()
	return status, cancelRequested
}

func uploadSessionIsCommittedOrCompleting(session *fileUploadSession) bool {
	status, _ := uploadSessionStatusSnapshot(session)
	return status == uploadSessionCommitted || status == uploadSessionCompleting
}

func ensureUploadCleanupTimerLocked() {
	if uploads.cleanupRunning {
		return
	}
	if len(uploads.sessions) == 0 {
		return
	}
	uploads.cleanupRunning = true
	go uploadCleanupLoop()
}

func uploadCleanupLoop() {
	ticker := time.NewTicker(uploadCleanupCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		hasRemaining := cleanupExpiredCommittedUploadSessions()
		if !hasRemaining {
			uploads.mu.Lock()
			if len(uploads.sessions) > 0 {
				// 新 session 在此期间被添加,继续运行
				uploads.mu.Unlock()
				continue
			}
			uploads.cleanupRunning = false
			uploads.mu.Unlock()
			return
		}
	}
}

func ResetUploadSessions() {
	resetUploadSessions()
}

const (
	maxOpenFileSize            = 10 * 1024 * 1024
	maxUploadChunkSize         = 10 * 1024 * 1024
	maxUploadChunkCount        = 4096
	uploadChunkLockStripes     = 64
	uploadSessionIdleTTL       = 10 * time.Minute
	uploadCleanupCheckInterval = 2 * time.Minute
	uploadIDHeaderName         = "X-Ipac-Upload-Id"
	uploadChunkHeaderName      = "X-Ipac-Chunk-Index"
	uploadInstanceHeader       = "X-Ipac-Instance"
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

type fileUploadAbortRequest struct {
	Instance string `json:"instance"`
	UploadID string `json:"upload_id"`
}

type fileUploadCompleteRequest struct {
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
	StageRoot       string
	StagePath       string
	CleanupPath     string
	StageInfo       os.FileInfo
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

func signalUploadCompletionLocked(session *fileUploadSession) {
	if session == nil || session.CompleteDone == nil {
		return
	}
	session.CompleteOnce.Do(func() {
		close(session.CompleteDone)
	})
}

func signalUploadCompletion(session *fileUploadSession) {
	if session == nil {
		return
	}
	uploads.mu.Lock()
	signalUploadCompletionLocked(session)
	uploads.mu.Unlock()
}

func resetUploadCompletionSignalLocked(session *fileUploadSession) {
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
	cleanupExpiredCommittedUploadSessions()
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

func acquireUploadSessionForChunk(uploadID string) (*fileUploadSession, bool) {
	cleanupExpiredCommittedUploadSessions()
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
	if session.CancelRequested || session.Status == uploadSessionCanceled {
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
	cleanupPath := session.CleanupPath
	uploads.mu.Lock()
	if current, ok := uploads.sessions[session.UploadID]; ok && current == session {
		delete(uploads.sessions, session.UploadID)
	}
	uploads.mu.Unlock()
	if cleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(cleanupPath)
	}
}

func removeUploadCleanupPathIfIdle(session *fileUploadSession) bool {
	if session == nil || atomic.LoadInt32(&session.ActiveRequests) != 0 {
		return false
	}
	if session.CleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(session.CleanupPath)
	}
	return true
}

func uploadStagePath(session *fileUploadSession, plan uploadChunkPlan) (string, error) {
	if session == nil {
		return "", errors.New(msg.UploadSessionRequired)
	}
	stageRoot := strings.TrimSpace(session.StageRoot)
	stagePath := strings.TrimSpace(session.StagePath)
	if stageRoot == "" || stagePath == "" {
		return "", errors.New(msg.UploadTempDirMissing)
	}
	return stagePath, nil
}

func validateUploadStageFileIdentity(session *fileUploadSession) error {
	if session == nil {
		return errors.New(msg.UploadSessionRequired)
	}
	stageRoot := strings.TrimSpace(session.StageRoot)
	stagePath := strings.TrimSpace(session.StagePath)
	if stageRoot == "" || stagePath == "" {
		return errors.New(msg.UploadTempDirMissing)
	}
	if err := instancefs.EnsurePathComponentsWithinRoot(stageRoot, stagePath, true); err != nil {
		return err
	}
	if !file.IsAtomicTempRegistryPath(stagePath) {
		return errors.New(msg.UploadSessionInvalid)
	}
	info, err := os.Lstat(stagePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeType != 0 {
		return instancefs.ErrUploadTargetIsDirectory
	}
	if session.StageInfo == nil || !os.SameFile(info, session.StageInfo) {
		return errors.New(msg.UploadSessionInvalid)
	}
	return nil
}

func openUploadStageFile(session *fileUploadSession, flag int) (*os.File, os.FileInfo, error) {
	if err := validateUploadStageFileIdentity(session); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(session.StagePath, flag, 0600)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if session.StageInfo == nil || !os.SameFile(session.StageInfo, info) {
		_ = f.Close()
		return nil, nil, errors.New(msg.UploadSessionInvalid)
	}
	return f, info, nil
}

func writeUploadChunkToStage(session *fileUploadSession, plan uploadChunkPlan, src io.Reader) error {
	if _, err := uploadStagePath(session, plan); err != nil {
		return err
	}
	out, _, err := openUploadStageFile(session, os.O_WRONLY)
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

func drainUploadRequestBodyLimited(src io.Reader, expectedSize int64) error {
	if src == nil {
		return nil
	}
	if expectedSize < 0 {
		return errors.New(msg.InvalidExpectedChunkSz)
	}
	limit := expectedSize + 1
	maxLimit := int64(maxUploadChunkSize + 1)
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}
	n, err := io.Copy(io.Discard, io.LimitReader(src, limit))
	if err != nil {
		return err
	}
	if n > expectedSize {
		return &http.MaxBytesError{Limit: expectedSize}
	}
	return nil
}

func syncUploadStageFile(session *fileUploadSession, expectedSize int64) error {
	if session == nil {
		return errors.New(msg.UploadSessionRequired)
	}
	if expectedSize < 0 {
		return errors.New(msg.InvalidFileSize)
	}
	f, info, err := openUploadStageFile(session, os.O_RDWR)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	if info.IsDir() {
		return instancefs.ErrUploadTargetIsDirectory
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

func markUploadChunkReceived(session *fileUploadSession, index int) bool {
	if session == nil || strings.TrimSpace(session.UploadID) == "" {
		return false
	}
	if index < 0 {
		return false
	}
	now := time.Now()
	uploads.mu.Lock()
	current, ok := uploads.sessions[session.UploadID]
	if !ok || current != session {
		uploads.mu.Unlock()
		return false
	}
	if index >= session.ChunkCount {
		uploads.mu.Unlock()
		return false
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
	completed := session.ChunkCount > 0 && session.UploadedCount >= session.ChunkCount
	uploads.mu.Unlock()
	return completed
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
	CleanupUploadCleanupPath()
}

func CleanupUploadCleanupPath() {
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
		if session.CleanupPath != "" {
			_ = file.RemoveRegisteredTempPath(session.CleanupPath)
		}
	}
}

func createUploadID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(randomBytes), nil
}

func setUploadSession(session *fileUploadSession) {
	cleanupExpiredCommittedUploadSessions()
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	uploads.sessions[session.UploadID] = session
	ensureUploadCleanupTimerLocked()
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
		signalUploadCompletionLocked(old)
	}
	uploads.sessions[session.UploadID] = session
	ensureUploadCleanupTimerLocked()
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
	var cleanupPath string
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
	signalUploadCompletionLocked(session)
	if atomic.LoadInt32(&session.ActiveRequests) == 0 {
		delete(uploads.sessions, uploadID)
		cleanupPath = session.CleanupPath
		shouldRemove = true
	}
	uploads.mu.Unlock()
	if shouldRemove && cleanupPath != "" {
		return cleanupPath, true, nil
	}
	return "", true, nil
}

func acquireUploadSessionForComplete(uploadID string) (*fileUploadSession, bool) {
	cleanupExpiredCommittedUploadSessions()
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
		resetUploadCompletionSignalLocked(session)
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
		signalUploadCompletionLocked(session)
	}
	uploads.mu.Unlock()
}

func failUploadCompletion(session *fileUploadSession) {
	if session == nil {
		return
	}
	now := time.Now()
	var cleanupPath string
	var shouldRemove bool
	uploads.mu.Lock()
	if current, ok := uploads.sessions[session.UploadID]; ok && current == session {
		session.LastChunkAt = now
		session.CancelRequested = true
		session.Status = uploadSessionCanceled
		signalUploadCompletionLocked(session)
		if atomic.LoadInt32(&session.ActiveRequests) == 0 {
			delete(uploads.sessions, session.UploadID)
			cleanupPath = session.CleanupPath
			shouldRemove = true
		}
	}
	uploads.mu.Unlock()
	if shouldRemove && cleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(cleanupPath)
	}
}

func resetUploadCompletionToActive(session *fileUploadSession) {
	finishUploadCompletion(session, uploadSessionActive)
}

func writeUploadCompleteSuccess(w http.ResponseWriter, sp *process.InstanceProcess, session *fileUploadSession) {
	if w == nil || session == nil {
		return
	}
	writeFileUploadCompleteResponse(w, session.DirPath, session.FileName, session.UploadID)
}

func writeFileUploadCompleteResponse(w http.ResponseWriter, dirPath string, fileName string, uploadID string) {
	if w == nil {
		return
	}
	web.WriteOK(w, map[string]any{
		"ok":        true,
		"completed": true,
		"upload_id": uploadID,
		"path":      dirPath,
		"name":      fileName,
		"status":    string(uploadSessionCommitted),
	})
}

func writeUploadResponseIfRequestActive(w http.ResponseWriter, r *http.Request, writeResponse func()) {
	if writeResponse == nil || isUploadRequestCanceled(r) {
		return
	}
	writeResponse()
}

func completeUploadSession(w http.ResponseWriter, r *http.Request, sp *process.InstanceProcess, session *fileUploadSession, instanceName string, ownerUser string) {
	status, waitCh := beginUploadCompletion(session)
	switch status {
	case uploadSessionCanceled:
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCanceled(w)
		})
		return
	case uploadSessionCommitted:
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCompleteSuccess(w, sp, session)
		})
		return
	case uploadSessionCompleting:
		if waitCh != nil {
			select {
			case <-waitCh:
			case <-r.Context().Done():
				return
			}
		}
		refreshed, exists := loadUploadSession(session.UploadID)
		if !exists {
			writeUploadResponseIfRequestActive(w, r, func() {
				writeUploadCompleteSuccess(w, sp, session)
			})
			return
		}
		session = refreshed
		if session.InstanceName != instanceName {
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusBadRequest, msg.UploadSessionInstanceMismatch, nil)
			})
			return
		}
		if strings.TrimSpace(session.OwnerUser) != strings.TrimSpace(ownerUser) {
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, nil)
			})
			return
		}
		if isUploadSessionCanceled(session) {
			writeUploadResponseIfRequestActive(w, r, func() {
				writeUploadCanceled(w)
			})
			return
		}
		refreshedStatus, _ := uploadSessionStatusSnapshot(session)
		if refreshedStatus == uploadSessionCommitted {
			writeUploadResponseIfRequestActive(w, r, func() {
				writeUploadCompleteSuccess(w, sp, session)
			})
			return
		}
		if refreshedStatus == uploadSessionActive {
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusConflict, msg.UploadFinalizingRetryLater, nil)
			})
			return
		}
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionFinished, nil)
		})
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
	touchUploadSessionChunk(session.UploadID)

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
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusConflict, msg.UploadChunksIncomplete, fmt.Errorf(msg.MissingChunksFmt, chunkCount-received, chunkCount, missing))
			})
			return
		}
	}
	if isUploadSessionCanceled(session) {
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCanceled(w)
		})
		return
	}

	if err := syncUploadStageFile(session, session.Size); err != nil {
		completionStatus = uploadSessionActive
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, err)
		})
		return
	}
	if isUploadSessionCanceled(session) {
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCanceled(w)
		})
		return
	}

	// Chunk data is already persisted in the staging file. Commit the staging file
	// with an atomic replace/rename so interrupted uploads never expose partial target files.
	fs, err := newInstanceFS(sp)
	if err != nil {
		completionStatus = uploadSessionActive
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.FilePathInvalid, err)
		})
		return
	}
	target, err := fs.ResolveUploadTarget(session.DirPath, session.FileName)
	if err != nil {
		completionStatus = uploadSessionActive
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		})
		return
	}
	if err := fs.CheckUploadTargetFile(target, session.Overwrite); err != nil {
		completionStatus = uploadSessionActive
		if errors.Is(err, os.ErrExist) {
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			})
			return
		}
		if errors.Is(err, instancefs.ErrUploadTargetIsDirectory) {
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusBadRequest, msg.UploadTargetIsDirectory, nil)
			})
			return
		}
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.CheckUploadTargetFileFailed, err)
		})
		return
	}
	if isUploadSessionCanceled(session) {
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCanceled(w)
		})
		return
	}

	uploads.mu.Lock()
	current, currentOK := uploads.sessions[session.UploadID]
	currentActive := currentOK && current == session && !session.CancelRequested && session.Status != uploadSessionCanceled
	uploads.mu.Unlock()
	if !currentActive {
		completionStatus = uploadSessionCanceled
		writeUploadResponseIfRequestActive(w, r, func() {
			writeUploadCanceled(w)
		})
		return
	}
	stageCommitted, commitErr := fs.CommitRegisteredUploadFile(session.StagePath, target, session.Overwrite)
	if commitErr == nil || stageCommitted {
		uploads.mu.Lock()
		current, currentOK = uploads.sessions[session.UploadID]
		currentActive = currentOK && current == session && !session.CancelRequested && session.Status != uploadSessionCanceled
		if currentActive {
			session.LastChunkAt = time.Now()
			session.Status = uploadSessionCommitted
			signalUploadCompletionLocked(session)
		}
		uploads.mu.Unlock()
		if !currentActive {
			completionStatus = uploadSessionCanceled
			writeUploadResponseIfRequestActive(w, r, func() {
				writeUploadCanceled(w)
			})
			return
		}
	}
	if commitErr != nil && !stageCommitted {
		if errors.Is(commitErr, os.ErrExist) {
			completionStatus = uploadSessionActive
			writeUploadResponseIfRequestActive(w, r, func() {
				web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			})
			return
		}
		completionStatus = uploadSessionActive
		log.Printf(msg.CompleteUploadWriteFailedLogFmt, session.UploadID, session.TargetPath, commitErr)
		writeUploadResponseIfRequestActive(w, r, func() {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveUploadFileFailed, commitErr)
		})
		return
	}
	if commitErr != nil {
		log.Printf(msg.CompleteUploadWriteFailedLogFmt, session.UploadID, session.TargetPath, commitErr)
	}
	// Cleanup session and unregister committed staging file.
	if session.CleanupPath != "" {
		if err := file.UnregisterTempPath(session.CleanupPath); err != nil {
			log.Printf("注销上传临时文件失败: %v", err)
		}
	}
	completionStatus = uploadSessionCommitted
	writeUploadResponseIfRequestActive(w, r, func() {
		writeUploadCompleteSuccess(w, sp, session)
	})
}

func getInstanceFileTarget(sp *process.InstanceProcess, dirPath string, fileName string) (*instancefs.UploadTarget, error) {
	fs, err := newInstanceFS(sp)
	if err != nil {
		return nil, err
	}
	target, err := fs.ResolveUploadTarget(dirPath, fileName)
	if err != nil {
		return nil, err
	}
	return &target, nil
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

	target, err := getInstanceFileTarget(sp, req.Path, fileName)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	relativePath := target.DirRel()
	targetPath := target.TargetPath()

	// 自动创建目标目录（如果不存在）, 减少前端逐级创建目录的请求
	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.FilePathInvalid, err)
		return
	}
	if err := fs.EnsureUploadTargetDirectory(*target); err != nil {
		var pathErr *instancefs.PathAccessError
		if errors.As(err, &pathErr) && pathErr.Kind == instancefs.PathAccessErrorWithinRoot {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.CreateDirectoryFailed, err)
		return
	}

	if err := fs.CheckUploadTargetFile(*target, req.Overwrite); err != nil {
		if errors.Is(err, os.ErrExist) {
			web.WriteAPIError(w, http.StatusConflict, msg.UploadTargetFileAlreadyExists, nil)
			return
		}
		if errors.Is(err, instancefs.ErrUploadTargetIsDirectory) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UploadTargetIsDirectory, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.CheckUploadTargetFileFailed, err)
		return
	}

	stageFile, stagePath, err := fs.OpenRegisteredUploadAtomicFile(*target, 0644)
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
	if err := stageFile.Truncate(normalizedSize); err != nil {
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

	if req.Size > 0 {
		freeTemp, err := compat.GetFreeDiskBytes(filepath.Dir(stagePath))
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

	setUploadSession(&fileUploadSession{
		UploadID:       uploadID,
		Scope:          uploadScopeInstanceFile,
		OwnerUser:      authedUser.User,
		InstanceName:   name,
		DirPath:        relativePath,
		FileName:       fileName,
		TargetPath:     targetPath,
		StageRoot:      fs.RootPath(),
		StagePath:      stagePath,
		CleanupPath:    stagePath,
		StageInfo:      stageInfo,
		Size:           normalizedSize,
		ChunkSize:      normalizedChunkSize,
		ChunkCount:     normalizedChunkCount,
		Overwrite:      req.Overwrite,
		CreatedAt:      time.Now(),
		LastChunkAt:    time.Now(),
		UploadedCount:  0,
		UploadedChunks: newUploadedChunkBitset(normalizedChunkCount),
		ChunkLocks:     make([]sync.Mutex, uploadChunkLockStripes),
		Status:         uploadSessionActive,
		CompleteDone:   make(chan struct{}),
	})
	stageCommitted = true

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

	session, ok := acquireUploadSessionForChunk(uploadID)
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
	if uploadSessionIsCommittedOrCompleting(session) {
		if err := drainUploadRequestBodyLimited(r.Body, plan.Size); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.UploadChunkTooLarge, nil)
				return
			}
			web.WriteAPIError(w, http.StatusBadRequest, msg.ReadUploadChunkFailed, err)
			return
		}
		sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
		if !ok {
			return
		}
		completeUploadSession(w, r, sp, session, name, authedUser.User)
		return
	}

	// Validate known Content-Length without buffering the whole chunk.
	if r.ContentLength >= 0 && r.ContentLength != plan.Size {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadChunkSizeMismatch, fmt.Errorf(msg.ExpectedGotFmt, plan.Size, r.ContentLength))
		return
	}

	chunkLock := getUploadChunkLock(session, index)
	if chunkLock == nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.UploadSessionInvalid, errors.New(msg.UploadChunkLockNil))
		return
	}
	chunkLock.Lock()
	chunkLocked := true
	defer func() {
		if chunkLocked {
			chunkLock.Unlock()
		}
	}()

	uploads.mu.RLock()
	alreadyReceived := isUploadChunkReceivedLocked(session, index)
	alreadyCompleted := session.ChunkCount > 0 && session.UploadedCount >= session.ChunkCount
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
		if alreadyCompleted {
			chunkLock.Unlock()
			chunkLocked = false
			sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
			if !ok {
				return
			}
			completeUploadSession(w, r, sp, session, name, authedUser.User)
			return
		}
		web.WriteOK(w, map[string]bool{"ok": true, "completed": false})
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
	completed := markUploadChunkReceived(session, index)
	chunkLock.Unlock()
	chunkLocked = false
	if !completed {
		web.WriteOK(w, map[string]bool{"ok": true, "completed": false})
		return
	}

	sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return
	}
	completeUploadSession(w, r, sp, session, name, authedUser.User)
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
	name, ok := web.RequireAccessibleInstanceNameByName(w, authedUser, req.Instance)
	if !ok {
		return
	}
	req.UploadID = strings.TrimSpace(req.UploadID)
	if req.UploadID == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UploadIDRequired, nil)
		return
	}

	session, ok := acquireUploadSessionForChunk(req.UploadID)
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
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return
	}
	completeUploadSession(w, r, sp, session, name, authedUser.User)
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
	var cleanupPath string
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
		if session.Status == uploadSessionCompleting {
			session.LastChunkAt = now
			uploads.mu.Unlock()
			web.WriteAPIError(w, http.StatusConflict, msg.UploadFinalizingRetryLater, nil)
			return
		}
		session.CancelRequested = true
		session.Status = uploadSessionCanceled
		session.LastChunkAt = now
		signalUploadCompletionLocked(session)
		if atomic.LoadInt32(&session.ActiveRequests) == 0 {
			delete(uploads.sessions, req.UploadID)
			cleanupPath = session.CleanupPath
			shouldRemove = true
		}
	}
	uploads.mu.Unlock()

	if shouldRemove && cleanupPath != "" {
		_ = file.RemoveRegisteredTempPath(cleanupPath)
	}

	web.WriteOK(w, map[string]bool{"ok": true})
}
