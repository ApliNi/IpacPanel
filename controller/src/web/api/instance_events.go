package api

import (
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	"net/http"
	"time"
)

type instanceEventsRequest struct{}

const sseKeepaliveInterval = 15 * time.Second

func HandleApiInstanceEvents(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	var req instanceEventsRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
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
