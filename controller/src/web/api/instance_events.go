package api

import (
	web "IpacPanel/controller/src/web"

	"net/http"
	"time"
)

const sseKeepaliveInterval = 15 * time.Second

func HandleApiInstanceEvents(w http.ResponseWriter, r *http.Request) {
	web.MarkRequestRouteKind(w, "sse")
	if !web.RequireMethod(w, r, http.MethodGet) {
		return
	}
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	if !web.ValidateCSRFFromQuery(r) {
		web.MarkAPIError(w, http.StatusForbidden, "CSRF 验证失败", nil)
		_ = sse.SendEvent("auth_required", map[string]bool{"auth_required": true})
		web.LogWebAccess(w, r, http.StatusOK)
		return
	}
	authedUser, authed := web.GetAuthedUserFromRequest(r)
	if !authed {
		web.MarkAPIError(w, http.StatusUnauthorized, "未授权", nil)
		_ = sse.SendEvent("auth_required", map[string]bool{"auth_required": true})
		web.LogWebAccess(w, r, http.StatusOK)
		return
	}
	web.MarkRequestUser(w, authedUser.User)
	web.LogWebAccess(w, r, http.StatusOK)
	subscriber := subscribeInstanceListUpdates()
	defer unsubscribeInstanceListUpdates(subscriber)
	lastVersion := int64(0)

	sendSnapshot := func(version int64) bool {
		payload := instanceListEventPayload{Version: version, Items: getInstanceListResponse(authedUser)}
		if err := sse.SendEvent("instances_full", payload); err != nil {
			return false
		}
		lastVersion = version
		return true
	}
	sendPatch := func(seq int64) bool {
		patch := getInstanceStatusPatchResponse(authedUser, seq)
		payload := instanceListEventPayload{Version: seq, Items: patch}
		if err := sse.SendEvent("instances_patch", payload); err != nil {
			return false
		}
		lastVersion = seq
		return true
	}

	instanceEvents.mu.Lock()
	initialVersion := instanceEvents.eventVersion
	subscriber.needFull = false
	instanceEvents.mu.Unlock()
	if !sendSnapshot(initialVersion) {
		return
	}

	ctx := r.Context()
	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case signal := <-subscriber.ch:
			instanceEvents.mu.Lock()
			needFull := subscriber.needFull
			if signal.full {
				subscriber.needFull = false
			}
			instanceEvents.mu.Unlock()
			if needFull {
				if !sendSnapshot(signal.seq) {
					return
				}
				continue
			}
			if signal.full {
				if signal.seq <= lastVersion {
					continue
				}
				if !sendSnapshot(signal.seq) {
					return
				}
				continue
			}
			if signal.seq != lastVersion+1 {
				instanceEvents.mu.Lock()
				subscriber.needFull = false
				instanceEvents.mu.Unlock()
				if !sendSnapshot(signal.seq) {
					return
				}
				continue
			}
			if !sendPatch(signal.seq) {
				return
			}
		case <-ticker.C:
			if err := sse.SendComment(); err != nil {
				return
			}
		}
	}
}
