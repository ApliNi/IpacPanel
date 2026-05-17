package api

import (
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/process"
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"

	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

func HandleApiInstanceControl(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:     true,
		Methods:         []string{http.MethodPost},
		CSRFFromRequest: true,
	})
	if !ok {
		return
	}
	authedUser := guard.User

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
			terminalMsg := []byte(fmt.Sprintf("\r\n\r\n\x1b[31m\x1b[1m[IpacPanel] %s\x1b[0m\r\n\r\n", err.Error()))
			ip.AppendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
			ip.Mu.Unlock()
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
