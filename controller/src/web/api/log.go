package api

import (
	"IpacPanel/controller/src/logbuf"
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"
	"fmt"
	"net/http"
	"strings"
)

const (
	logQueryLimitMin = 1
	logQueryLimitMax = logbuf.MaxEntries
)

type logGetRequest struct {
	BeforeSeq uint64   `json:"before_seq"`
	Limit     int      `json:"limit"`
	Instance  string   `json:"instance"`
	Levels    []string `json:"levels"`
}

type logGetResponse struct {
	Entries    []logbuf.Entry `json:"entries"`
	TotalCount int            `json:"total_count"`
}

func HandleApiLogGet(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	var req logGetRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	filter := logbufVisibleFilter(authedUser)
	if instance := strings.TrimSpace(req.Instance); instance != "" {
		if !authz.DefaultRuntime.CanAccessInstance(authedUser, instance) {
			web.WriteAPIError(w, http.StatusForbidden, msg.Forbidden, nil)
			return
		}
		// 指定实例查询时收窄到该实例; 可见性集合仍然生效.
		filter.Instance = instance
	}
	levelSet, err := logbuf.NormalizeLevelSet(req.Levels)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.LogQueryFailed, err)
		return
	}
	filter.Levels = levelSet

	limit := req.Limit
	if limit < logQueryLimitMin || limit > logQueryLimitMax {
		web.WriteAPIError(w, http.StatusBadRequest, fmt.Sprintf(msg.LogQueryLimitInvalidFmt, logQueryLimitMin, logQueryLimitMax), nil)
		return
	}
	entries, total, err := logbuf.Snapshot(req.BeforeSeq, limit, filter)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.LogQueryFailed, err)
		return
	}
	web.WriteOK(w, logGetResponse{Entries: entries, TotalCount: total})
}

func HandleApiLogClear(w http.ResponseWriter, _ *http.Request) {
	count := logbuf.Clear()
	web.WriteOK(w, map[string]int{"cleared": count})
}
