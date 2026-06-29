package api

import (
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"IpacPanel/controller/src/web/authz"

	web "IpacPanel/controller/src/web"

	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"IpacPanel/controller/src/compat"
)

const fileSaveJSONBodyLimit int64 = maxOpenFileSize + (256 * 1024)

type fileListRequest struct {
	Instance   string `json:"instance"`
	Path       string `json:"path"`
	Fallback   bool   `json:"fallback"`
	Jump       bool   `json:"jump"`
	Query      string `json:"query"`
	Page       int    `json:"page"`
	AllowLarge bool   `json:"allow_large"`
}

func requireFileRequestInstance(w http.ResponseWriter, r *http.Request, instance string) (*process.InstanceProcess, string, bool) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return nil, "", false
	}
	name := strings.TrimSpace(instance)
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return nil, "", false
	}
	return sp, name, true
}

func writeFileReadResponse(w http.ResponseWriter, relativePath string, targetPath string, file *os.File, size int64) error {
	if w == nil || file == nil {
		return errors.New(msg.EmptyDest)
	}

	fileName := filepath.Base(targetPath)
	disposition := mime.FormatMediaType("inline", map[string]string{"filename": fileName})
	if disposition == "" {
		return errors.New("格式化文件响应头失败")
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-File-Path", url.PathEscape(relativePath))
	w.Header().Set("X-File-Name", url.PathEscape(fileName))
	w.Header().Set("X-File-Size", fmt.Sprintf("%d", size))
	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 32*1024)
	_, err := io.CopyBuffer(w, file, buf)
	return err
}

func writePartialDeleteError(w http.ResponseWriter, partialErr *instancefs.PartialDeleteError) {
	web.WriteAPIError(w, http.StatusInternalServerError, msg.PartialDeleteFailed, partialErr)
}

func deleteFailureReason(err error) string {
	if err == nil {
		return msg.PartialDeleteFailed
	}
	if errors.Is(err, os.ErrPermission) {
		return msg.PermissionDenied
	}
	if errors.Is(err, os.ErrNotExist) {
		return msg.TargetNotFound
	}
	if errors.Is(err, instancefs.ErrPathOutsideInstanceRoot) {
		return msg.PathOutsideInstanceRoot
	}
	return msg.DeleteFailed
}

func writeRequiredFileAccessError(w http.ResponseWriter, err error) {
	var accessErr *instancefs.PathAccessError
	if !errors.As(err, &accessErr) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	switch accessErr.Kind {
	case instancefs.PathAccessErrorResolve:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, accessErr.Err)
	case instancefs.PathAccessErrorRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
	case instancefs.PathAccessErrorStat:
		if errors.Is(accessErr.Err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, accessErr.Err)
	case instancefs.PathAccessErrorWithinRoot:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, accessErr.Err)
	case instancefs.PathAccessErrorDirectory:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, fmt.Errorf("unknown path access error kind %q: %w", accessErr.Kind, accessErr.Err))
	}
}

func HandleApiFileList(w http.ResponseWriter, r *http.Request) {
	var req fileListRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}
	requestedPath := req.Path
	fallback := req.Fallback
	jump := req.Jump
	query := strings.TrimSpace(req.Query)
	page := req.Page

	var resp *fileListResponse
	var err error
	if jump {
		resp, err = buildFileListJumpResponse(sp, requestedPath, page, query)
	} else {
		resp, err = buildFileListResponse(sp, requestedPath, page, query)
	}
	if err != nil {
		if fallback && strings.TrimSpace(requestedPath) != "" && errors.Is(err, os.ErrNotExist) {
			fallbackResp, fbErr := buildFileListResponse(sp, "", page, query)
			if fbErr != nil {
				web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileListFailed, fbErr)
				return
			}
			fallbackResp.RequestedPath = requestedPath
			fallbackResp.Fallback = true
			fallbackResp.Missing = true
			web.WriteOK(w, fallbackResp)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileListFailed, err)
		return
	}

	if strings.TrimSpace(requestedPath) != "" {
		resp.RequestedPath = requestedPath
	}
	web.WriteOK(w, resp)
}

func HandleApiFileCreateFile(w http.ResponseWriter, r *http.Request) {
	var req fileCreateFileRequest
	if !web.DecodeJSONBody(w, r, &req, web.WithJSONBodyLimit(fileSaveJSONBodyLimit)) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}
	if int64(len([]byte(req.Content))) > maxOpenFileSize {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	fileName, err := ensureFileName(req.Name)
	if err != nil {
		writeFileNameValidationError(w, err)
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	parentPath, targetFilePath, err := fs.ResolveNewChild(req.Path, fileName)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetPath := targetFilePath.AbsPath()
	if !req.Overwrite {
		if _, err := os.Stat(targetPath); err == nil {
			web.WriteAPIError(w, http.StatusConflict, msg.FileObjectAlreadyExists, nil)
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.CreateFileFailed, err)
			return
		}
	}
	if err := writeNewFileAtomic(sp, targetPath, []byte(req.Content), req.Overwrite, 0644); err != nil {
		if errors.Is(err, os.ErrExist) {
			web.WriteAPIError(w, http.StatusConflict, msg.FileObjectAlreadyExists, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.CreateFileFailed, err)
		return
	}

	resp, err := buildFileListResponse(sp, parentPath.RelSlash(), 1, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileCreateDir(w http.ResponseWriter, r *http.Request) {
	var req fileCreateDirRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}

	dirName, err := ensureFileName(req.Name)
	if err != nil {
		writeFileNameValidationError(w, err)
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	parentPath, targetDirPath, err := fs.ResolveNewChild(req.Path, dirName)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetPath := targetDirPath.AbsPath()

	if err := instancefs.EnsureDirectoryStepwise(fs.RootPath(), targetPath, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			targetInfo, statErr := os.Lstat(targetPath)
			if statErr != nil {
				web.WriteAPIError(w, http.StatusBadRequest, msg.CreateDirectoryFailed, statErr)
				return
			}
			if targetInfo.Mode()&os.ModeSymlink != 0 {
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, instancefs.ErrPathOutsideInstanceRoot)
				return
			}
			if !targetInfo.IsDir() {
				web.WriteAPIError(w, http.StatusConflict, msg.FileObjectAlreadyExists, nil)
				return
			}
			if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
				web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
				return
			}
			resp, err := buildFileListResponse(sp, parentPath.RelSlash(), 1, "")
			if err != nil {
				web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
				return
			}

			web.WriteOK(w, resp)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.CreateDirectoryFailed, err)
		return
	}
	if err := ensureCreatedPathWithinInstanceRoot(sp, targetPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	resp, err := buildFileListResponse(sp, parentPath.RelSlash(), 1, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileRename(w http.ResponseWriter, r *http.Request) {
	var req fileRenameRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}

	newName, err := ensureFileName(req.NewName)
	if err != nil {
		writeFileNameValidationError(w, err)
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	oldSafePath, err := fs.ResolveRequiredWithinRoot(req.Path)
	if err != nil {
		writeRequiredFileAccessError(w, err)
		return
	}

	oldPath := oldSafePath.AbsPath()
	parentRelative := filepath.ToSlash(filepath.Dir(filepath.FromSlash(oldSafePath.RelSlash())))
	if parentRelative == "." {
		parentRelative = ""
	}
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if err := ensureNewPathWithinInstanceRoot(sp, newPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if filepath.Clean(oldPath) == filepath.Clean(newPath) {
		resp, err := buildFileListResponse(sp, parentRelative, 1, "")
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
			return
		}
		web.WriteOK(w, resp)
		return
	}

	if err := compat.RenameNoReplace(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			web.WriteAPIError(w, http.StatusConflict, msg.TargetNameAlreadyExists, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.RenameFileFailed, err)
		return
	}

	resp, err := buildFileListResponse(sp, parentRelative, 1, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileRead(w http.ResponseWriter, r *http.Request) {
	var req fileListRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetSafePath, info, err := fs.ResolveRequiredExistingFile(req.Path)
	if err != nil {
		writeRequiredFileAccessError(w, err)
		return
	}

	targetPath := targetSafePath.AbsPath()
	allowLarge := req.AllowLarge
	if !info.Mode().IsRegular() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New("目标不是普通文件"))
		return
	}
	if info.Size() > maxOpenFileSize && !allowLarge {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	file, openedInfo, err := instancefs.OpenExistingFileSafe(fs.RootPath(), targetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return
	}
	defer file.Close()
	if !openedInfo.Mode().IsRegular() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New("目标不是普通文件"))
		return
	}
	if openedInfo.Size() > maxOpenFileSize && !allowLarge {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	if err := writeFileReadResponse(w, targetSafePath.RelSlash(), targetPath, file, openedInfo.Size()); err != nil {
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
	}
}

func HandleApiFileSave(w http.ResponseWriter, r *http.Request) {
	var req fileSaveRequest
	if !web.DecodeJSONBody(w, r, &req, web.WithJSONBodyLimit(fileSaveJSONBodyLimit)) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}
	if int64(len([]byte(req.Content))) > maxOpenFileSize {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetSafePath, info, err := fs.ResolveRequiredExistingFile(req.Path)
	if err != nil {
		writeRequiredFileAccessError(w, err)
		return
	}

	targetPath := targetSafePath.AbsPath()
	openedFile, openedInfo, err := instancefs.OpenExistingFileSafe(fs.RootPath(), targetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if err := openedFile.Close(); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveFileFailed, err)
		return
	}
	if !os.SameFile(info, openedInfo) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, instancefs.ErrPathOutsideInstanceRoot)
		return
	}

	if err := writeFileAtomic(fs.RootPath(), targetPath, []byte(req.Content), info.Mode()); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveFileFailed, err)
		return
	}

	web.WriteOK(w, &fileContentResponse{
		Path:    targetSafePath.RelSlash(),
		Name:    filepath.Base(targetPath),
		Content: req.Content,
	})
}

func HandleApiFileDelete(w http.ResponseWriter, r *http.Request) {
	var req fileDeleteRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sp, _, ok := requireFileRequestInstance(w, r, req.Instance)
	if !ok {
		return
	}

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetSafePath, isDir, err := fs.DeleteExisting(req.Path)
	if err != nil {
		var accessErr *instancefs.PathAccessError
		if errors.As(err, &accessErr) {
			writeRequiredFileAccessError(w, err)
			return
		}
		var partialErr *instancefs.PartialDeleteError
		if errors.As(err, &partialErr) {
			writePartialDeleteError(w, partialErr)
			return
		}
		if isDir {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.DeleteDirectoryFailed, err)
			return
		}
		web.WriteAPIError(w, http.StatusInternalServerError, msg.DeleteFileFailed, err)
		return
	}

	parentRelative := filepath.ToSlash(filepath.Dir(targetSafePath.RelSlash()))
	if parentRelative == "." {
		parentRelative = ""
	}
	resp, err := buildFileListResponse(sp, parentRelative, 1, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}
