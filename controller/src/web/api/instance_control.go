package api

import (
	"IpacPanel/controller/src/logbuf"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/process"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	cfg "IpacPanel/controller/src/config"

	"fmt"
	"log"
	"net/http"
)

func HandleApiInstanceControl(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	name, action, ok := web.ParseInstanceControlParams(w, r)
	if !ok {
		return
	}

	ip, ok := web.RequireInstanceProcessByName(w, authedUser, name)
	if !ok {
		return
	}

	log.Printf(msg.InstanceActionLogFmt, action, name)
	web.MarkRequestAction(w, action)

	switch action {
	case "start":
		if err := ip.Start(); err != nil {
			limit := cfg.GetHistoryLimit() * 1024
			ip.Mu.Lock()
			ip.AppendAndBroadcastWarningSystemMessageLocked(err.Error(), limit)
			ip.Mu.Unlock()
			_ = logbuf.EmitInstance(logbuf.LevelError, name, fmt.Sprintf("%s: %s", msg.StartInstanceFailed, err.Error()))
			web.WriteAPIError(w, http.StatusInternalServerError, msg.StartInstanceFailed, err)
			return
		}
	case "stop":
		ip.Stop(false)
	case "kill":
		ip.Stop(true)
	case "restart":
		result := ip.RequestRestartResult()
		if result == process.RestartRequestRejectedDeleting {
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceBeingDeleted, nil)
			return
		}
		if !result.IsAllowed() {
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceRestartingConflict, nil)
			return
		}
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidOperation, nil)
		return
	}

	web.WriteOK(w, map[string]bool{"ok": true})
}
