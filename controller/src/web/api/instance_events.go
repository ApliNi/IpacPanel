package api

import (
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"
	"IpacPanel/controller/src/logbuf"

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
	logFilter := logbufVisibleFilter(authedUser)
	lastLogCount := -1
	sendLogCount := func() bool {
		count := logbuf.Count(logFilter)
		if count == lastLogCount {
			return true
		}
		payload := logEventPayload{Version: logbuf.LatestSeq(), Count: count}
		if err := sse.SendEvent("log_count", payload); err != nil {
			return false
		}
		lastLogCount = count
		return true
	}

	sendSnapshot := func(version int64) bool {
		payload := instanceListEventPayload{Version: version, Items: getInstanceListResponse(authedUser)}
		if err := sse.SendEvent("instances_full", payload); err != nil {
			return false
		}
		lastVersion = version
		return sendLogCount()
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
			if !sendLogCount() {
				return
			}
		case <-ticker.C:
			if err := sse.SendComment(); err != nil {
				return
			}
		}
	}
}

// logbufVisibleFilter 构建当前用户视角的日志过滤器:
// 面板级条目 (Instance 为空) 对所有登录用户可见, 实例条目按访问权限过滤.
func logbufVisibleFilter(authedUser *cfg.AuthUser) logbuf.Filter {
	filter := logbuf.Filter{VisibleInstances: map[string]bool{}}
	for _, ip := range process.List() {
		ins := ip.InstanceSnapshot()
		if authz.DefaultRuntime.CanAccessInstance(authedUser, ins.Name) {
			filter.VisibleInstances[ins.Name] = true
		}
	}
	return filter
}
