package api

import (
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"time"
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

	fs, err := newInstanceFS(sp)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	targetSafePath, _, err := fs.ResolveRequiredExistingFile(r.URL.Query().Get("path"))
	if err != nil {
		writeRequiredFileAccessError(w, err)
		return
	}

	targetPath := targetSafePath.AbsPath()
	file, info, err := instancefs.OpenExistingFileSafe(fs.RootPath(), targetPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	defer file.Close()

	// Raw file responses are always served as downloads.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(targetPath)})
	if disposition == "" {
		disposition = fmt.Sprintf("attachment; filename=%q", filepath.Base(targetPath))
	}
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(targetPath), fileModTime(info), file)
}

func fileModTime(info interface{ ModTime() time.Time }) time.Time {
	if info == nil {
		return time.Time{}
	}
	return info.ModTime()
}
