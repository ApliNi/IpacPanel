package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

func HandleApiFileRaw(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	sp, _, ok := web.RequireInstanceProcessFromQuery(w, r, authedUser)
	if !ok {
		return
	}

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

	// Raw file responses are always served as downloads.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(targetPath)})
	if disposition == "" {
		disposition = fmt.Sprintf("attachment; filename=%q", filepath.Base(targetPath))
	}
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, targetPath)
}
