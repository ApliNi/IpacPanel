package api

import (
	"IpacPanel/controller/src/msg"
	"bufio"

	web "IpacPanel/controller/src/web"

	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"IpacPanel/controller/src/compat"
)

const fileSaveJSONBodyLimit int64 = maxOpenFileSize + (256 * 1024)

func writeJSONStringChunk(w io.Writer, text string, escapeBuf *[]byte) error {
	quoted := strconv.AppendQuote((*escapeBuf)[:0], text)
	*escapeBuf = quoted
	if len(quoted) <= 2 {
		return nil
	}
	_, err := w.Write(quoted[1 : len(quoted)-1])
	return err
}

func writeFileReadResponse(w http.ResponseWriter, relativePath string, targetPath string, file *os.File) error {
	if w == nil || file == nil {
		return errors.New(msg.EmptyDest)
	}

	bufWriter := bufio.NewWriterSize(w, 32*1024)
	defer bufWriter.Flush()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	pathJSON := strconv.Quote(relativePath)
	nameJSON := strconv.Quote(filepath.Base(targetPath))
	if _, err := io.WriteString(bufWriter, `{"ok":true,"data":{"path":`+pathJSON+`,"name":`+nameJSON+`,"content":"`); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(file, 32*1024)
	escapeBuf := make([]byte, 0, 64*1024)
	for {
		chunk, err := reader.ReadString('\n')
		if chunk != "" {
			if !utf8.ValidString(chunk) {
				chunk = strings.ToValidUTF8(chunk, string(utf8.RuneError))
			}
			if err := writeJSONStringChunk(bufWriter, chunk, &escapeBuf); err != nil {
				return err
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return err
	}

	_, err := io.WriteString(bufWriter, `"}}`)
	return err
}

func HandleApiFileList(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodGet},
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	requestedPath := r.URL.Query().Get("path")
	fallback := parseOptionalBoolQuery(r.URL.Query().Get("fallback"))
	jump := parseOptionalBoolQuery(r.URL.Query().Get("jump"))
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	var resp *fileListResponse
	var err error
	if jump {
		resp, err = buildFileListJumpResponse(sp, requestedPath, cursor, query)
	} else {
		resp, err = buildFileListResponse(sp, requestedPath, cursor, query)
	}
	if err != nil {
		if fallback && strings.TrimSpace(requestedPath) != "" && errors.Is(err, os.ErrNotExist) {
			fallbackResp, fbErr := buildFileListResponse(sp, "", "", query)
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
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileCreateFileRequest
	if !web.DecodeJSONBody(w, r, &req, web.WithJSONBodyLimit(fileSaveJSONBodyLimit)) {
		return
	}
	if int64(len([]byte(req.Content))) > maxOpenFileSize {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	fileName, err := ensureFileName(req.Name)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	targetDir := rootPath
	if relativePath != "" {
		targetDir = filepath.Join(rootPath, filepath.FromSlash(relativePath))
	}
	dirInfo, err := os.Stat(targetDir)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if !dirInfo.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.PathNotDirectory))
		return
	}
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetDir); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetPath := filepath.Join(targetDir, fileName)
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

	resp, err := buildFileListResponse(sp, relativePath, "", "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileCreateDir(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileCreateDirRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	dirName, err := ensureFileName(req.Name)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	targetDir := rootPath
	if relativePath != "" {
		targetDir = filepath.Join(rootPath, filepath.FromSlash(relativePath))
	}
	dirInfo, err := os.Stat(targetDir)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if !dirInfo.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.PathNotDirectory))
		return
	}
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetDir); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetPath := filepath.Join(targetDir, dirName)

	if err := os.Mkdir(targetPath, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			targetInfo, statErr := os.Stat(targetPath)
			if statErr != nil {
				web.WriteAPIError(w, http.StatusBadRequest, msg.CreateDirectoryFailed, statErr)
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
			resp, err := buildFileListResponse(sp, relativePath, "", "")
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
		_ = os.Remove(targetPath)
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	resp, err := buildFileListResponse(sp, relativePath, "", "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileRename(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileRenameRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	newName, err := ensureFileName(req.NewName)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if relativePath == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}

	oldPath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	if err := ensureResolvedPathWithinInstanceRoot(sp, oldPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	parentRelative := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relativePath)))
	if parentRelative == "." {
		parentRelative = ""
	}
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if err := ensureNewPathWithinInstanceRoot(sp, newPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if filepath.Clean(oldPath) == filepath.Clean(newPath) {
		resp, err := buildFileListResponse(sp, parentRelative, "", "")
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

	resp, err := buildFileListResponse(sp, parentRelative, "", "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}

func HandleApiFileRead(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodGet},
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	rootPath, relativePath, err := resolveInstanceFilePath(sp, r.URL.Query().Get("path"))
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if relativePath == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}

	targetPath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	info, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, err)
		return
	}
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if info.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
		return
	}
	allowLarge := parseOptionalBoolQuery(r.URL.Query().Get("allow_large"))
	if info.Size() > maxOpenFileSize && !allowLarge {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	file, err := os.Open(targetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
		return
	}
	defer file.Close()

	if err := writeFileReadResponse(w, relativePath, targetPath, file); err != nil {
		web.MarkAPIError(w, http.StatusInternalServerError, msg.ReadFileFailed, err)
	}
}

func HandleApiFileSave(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileSaveRequest
	if !web.DecodeJSONBody(w, r, &req, web.WithJSONBodyLimit(fileSaveJSONBodyLimit)) {
		return
	}
	if int64(len([]byte(req.Content))) > maxOpenFileSize {
		web.WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.FileSizeExceeds10MB, nil)
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if relativePath == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}

	targetPath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	info, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, err)
		return
	}
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if info.IsDir() {
		web.WriteAPIError(w, http.StatusBadRequest, msg.TargetIsDirectory, nil)
		return
	}

	if err := writeFileAtomic(rootPath, targetPath, []byte(req.Content), info.Mode()); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveFileFailed, err)
		return
	}

	web.WriteOK(w, &fileContentResponse{
		Path:    relativePath,
		Name:    filepath.Base(targetPath),
		Content: req.Content,
	})
}

func HandleApiFileDelete(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodPost},
		CSRFFromRequest:   true,
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	var req fileDeleteRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	rootPath, relativePath, err := resolveInstanceFilePath(sp, req.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	if relativePath == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathRequired, nil)
		return
	}

	targetPath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			web.WriteAPIError(w, http.StatusNotFound, msg.FileNotFound, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.ReadFileInfoFailed, err)
		return
	}

	if info.IsDir() {
		if err := os.RemoveAll(targetPath); err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.DeleteDirectoryFailed, err)
			return
		}
	} else {
		if err := os.Remove(targetPath); err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.DeleteFileFailed, err)
			return
		}
	}

	parentRelative := filepath.ToSlash(filepath.Dir(relativePath))
	if parentRelative == "." {
		parentRelative = ""
	}
	resp, err := buildFileListResponse(sp, parentRelative, "", "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ReadFileListFailed, err)
		return
	}

	web.WriteOK(w, resp)
}
