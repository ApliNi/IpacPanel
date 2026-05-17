package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/metrics"
	web "IpacPanel/controller/src/web"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type dashboardMetricCollector interface {
	ApplyConfig(config metrics.Config)
	Snapshot(minutes int, nic string, disk string, maxPoints int) metrics.Snapshot
	Latest(nic string, disk string) (metrics.PublicSample, bool)
	Metadata() metrics.Metadata
}

var dashboardCollector dashboardMetricCollector

const dashboardMaxPoints = 1000
const dashboardPublicMaxMinutes = 1440

type dashboardSnapshotResponse struct {
	Enabled         bool                   `json:"enabled"`
	MemoryMaxMin    int                    `json:"memory_max_min"`
	SelectedMinutes int                    `json:"selected_minutes"`
	Interfaces      []string               `json:"interfaces"`
	Disks           []string               `json:"disks"`
	Samples         []metrics.PublicSample `json:"samples"`
	Latest          *metrics.PublicSample  `json:"latest"`
}

type dashboardStreamFilter struct {
	NIC  string `json:"nic"`
	Disk string `json:"disk"`
}

type dashboardFullEvent struct {
	Seq             int64                 `json:"seq"`
	Enabled         bool                  `json:"enabled"`
	MemoryMaxMin    int                   `json:"memory_max_min"`
	SelectedMinutes int                   `json:"selected_minutes"`
	Filter          dashboardStreamFilter `json:"filter"`
	Interfaces      []string              `json:"interfaces"`
	Disks           []string              `json:"disks"`
	BaseTS          int64                 `json:"base_ts"`
	SampleSchema    []string              `json:"sample_schema"`
	ScaleHints      dashboardScaleHints   `json:"scale_hints"`
	Samples         [][]int64             `json:"samples"`
}

type dashboardScaleHints struct {
	MemoryTotalMax int64 `json:"memory_total_max"`
	SwapTotalMax   int64 `json:"swap_total_max"`
	DiskBPSMax     int64 `json:"disk_bps_max"`
	NetworkBPSMax  int64 `json:"network_bps_max"`
	ConnectionMax  int64 `json:"connection_max"`
}

type dashboardFullMetaEvent struct {
	Seq             int64                 `json:"seq"`
	Enabled         bool                  `json:"enabled"`
	MemoryMaxMin    int                   `json:"memory_max_min"`
	SelectedMinutes int                   `json:"selected_minutes"`
	Filter          dashboardStreamFilter `json:"filter"`
	Interfaces      []string              `json:"interfaces"`
	Disks           []string              `json:"disks"`
	BaseTS          int64                 `json:"base_ts"`
	SampleSchema    []string              `json:"sample_schema"`
	ScaleHints      dashboardScaleHints   `json:"scale_hints"`
	SampleTotal     int                   `json:"sample_total"`
}

type dashboardFullSamplesEvent struct {
	Seq     int64     `json:"seq"`
	BaseTS  int64     `json:"base_ts"`
	Samples [][]int64 `json:"samples"`
}

type dashboardAppendEvent struct {
	Seq    int64   `json:"seq"`
	BaseTS int64   `json:"base_ts"`
	Sample []int64 `json:"sample"`
}

type dashboardOptionsEvent struct {
	Seq        int64    `json:"seq"`
	Interfaces []string `json:"interfaces"`
	Disks      []string `json:"disks"`
}

type dashboardDisabledEvent struct {
	Enabled         bool      `json:"enabled"`
	MemoryMaxMin    int       `json:"memory_max_min"`
	SelectedMinutes int       `json:"selected_minutes"`
	Interfaces      []string  `json:"interfaces"`
	Disks           []string  `json:"disks"`
	BaseTS          int64     `json:"base_ts"`
	Samples         [][]int64 `json:"samples"`
}

type dashboardErrorEvent struct {
	Message string `json:"message"`
}

var dashboardSampleSchema = []string{"dt", "cpu10", "mem_used", "mem_total", "swap_used", "swap_total", "net_rx_bps", "net_tx_bps", "disk_read_bps", "disk_write_bps", "tcp_conn", "udp_conn"}

func SetDashboardCollector(collector *metrics.Collector) {
	dashboardCollector = collector
}

func isDashboardAdmin(user *cfg.AuthUser) bool {
	return user != nil && user.Perm == 7
}

func visibleDashboardDeviceList(values []string, isAdmin bool) []string {
	if !isAdmin {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}

func isPublicDashboardEnabled() bool {
	metricsConfig := cfg.GetMetricsConfig()
	return metricsConfig.PublicDashboard
}

func resolveDashboardRequestUser(w http.ResponseWriter, r *http.Request) (*cfg.AuthUser, bool) {
	authedUser, authed := web.GetAuthedUserFromRequest(r)
	if authed {
		web.MarkRequestUser(w, authedUser.User)
		return authedUser, true
	}
	if isPublicDashboardEnabled() {
		return nil, true
	}
	web.WriteAPIError(w, http.StatusUnauthorized, "未授权", nil)
	return nil, false
}

func HandleApiDashboardSnapshot(w http.ResponseWriter, r *http.Request) {
	if !web.RequireMethod(w, r, http.MethodGet) {
		return
	}
	authedUser, ok := resolveDashboardRequestUser(w, r)
	if !ok {
		return
	}
	if dashboardCollector == nil {
		web.WriteAPIError(w, http.StatusServiceUnavailable, "仪表板统计未初始化", nil)
		return
	}
	isAdmin := isDashboardAdmin(authedUser)
	minutes, ok := parseDashboardMinutes(w, r, isAdmin)
	if !ok {
		return
	}
	nic := ""
	disk := ""
	if isAdmin {
		nic = strings.TrimSpace(r.URL.Query().Get("nic"))
		disk = strings.TrimSpace(r.URL.Query().Get("disk"))
	}
	snapshot := dashboardCollector.Snapshot(minutes, nic, disk, dashboardMaxPoints)
	web.WriteOK(w, dashboardSnapshotResponse{
		Enabled:         snapshot.Enabled,
		MemoryMaxMin:    snapshot.MemoryMaxMin,
		SelectedMinutes: minutes,
		Interfaces:      visibleDashboardDeviceList(snapshot.Interfaces, isAdmin),
		Disks:           visibleDashboardDeviceList(snapshot.Disks, isAdmin),
		Samples:         snapshot.Samples,
		Latest:          snapshot.Latest,
	})
}

func HandleApiDashboardEvents(w http.ResponseWriter, r *http.Request) {
	web.MarkRequestRouteKind(w, "sse")
	if !web.RequireMethod(w, r, http.MethodGet) {
		return
	}
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	authedUser, authed := web.GetAuthedUserFromRequest(r)
	if authed {
		web.MarkRequestUser(w, authedUser.User)
		if !web.ValidateCSRFFromQuery(r) {
			web.MarkAPIError(w, http.StatusForbidden, "CSRF 验证失败", nil)
			_ = sse.SendEvent("auth_required", map[string]bool{"auth_required": true})
			web.LogWebAccess(w, r, http.StatusOK)
			return
		}
	} else if !isPublicDashboardEnabled() {
		web.MarkAPIError(w, http.StatusUnauthorized, "未授权", nil)
		_ = sse.SendEvent("auth_required", map[string]bool{"auth_required": true})
		web.LogWebAccess(w, r, http.StatusOK)
		return
	}
	web.LogWebAccess(w, r, http.StatusOK)
	if dashboardCollector == nil {
		_ = sse.SendEvent("dashboard_error", dashboardErrorEvent{Message: "仪表板统计未初始化."})
		return
	}
	isAdmin := isDashboardAdmin(authedUser)
	minutes, ok := parseDashboardStreamMinutes(sse, r, isAdmin)
	if !ok {
		return
	}
	nic := ""
	disk := ""
	if isAdmin {
		nic = strings.TrimSpace(r.URL.Query().Get("nic"))
		disk = strings.TrimSpace(r.URL.Query().Get("disk"))
	}
	var lastSeq int64
	var lastBaseTS int64
	var lastSampleTime int64
	var lastMemoryMaxMin int
	var lastEnabled bool
	var lastInterfaces string
	var lastDisks string
	sendFull := func(snapshot metrics.Snapshot) bool {
		seq, baseTS, fullSamples := buildDashboardStreamSamples(snapshot.Samples, 0)
		if snapshot.Enabled && baseTS <= 0 {
			baseTS = time.Now().Unix()
		}
		lastSeq = seq
		lastBaseTS = baseTS
		lastSampleTime = lastPublicSampleUnix(snapshot.Latest)
		lastMemoryMaxMin = snapshot.MemoryMaxMin
		lastEnabled = snapshot.Enabled
		visibleInterfaces := visibleDashboardDeviceList(snapshot.Interfaces, isAdmin)
		visibleDisks := visibleDashboardDeviceList(snapshot.Disks, isAdmin)
		lastInterfaces = strings.Join(visibleInterfaces, "\x00")
		lastDisks = strings.Join(visibleDisks, "\x00")
		if !snapshot.Enabled {
			return sse.SendEvent("dashboard_disabled", dashboardDisabledEvent{
				Enabled: false, MemoryMaxMin: snapshot.MemoryMaxMin, SelectedMinutes: minutes,
				Interfaces: visibleInterfaces, Disks: visibleDisks, BaseTS: 0, Samples: [][]int64{},
			}) == nil
		}
		fullMeta := dashboardFullMetaEvent{
			Seq: seq, Enabled: snapshot.Enabled, MemoryMaxMin: snapshot.MemoryMaxMin, SelectedMinutes: minutes,
			Filter: dashboardStreamFilter{NIC: nic, Disk: disk}, Interfaces: visibleInterfaces, Disks: visibleDisks,
			BaseTS: baseTS, SampleSchema: dashboardSampleSchema, ScaleHints: buildDashboardScaleHints(snapshot.Samples), SampleTotal: len(fullSamples),
		}
		if err := sse.SendEvent("dashboard_full_meta", fullMeta); err != nil {
			return false
		}
		if err := sse.SendEvent("dashboard_full_samples", dashboardFullSamplesEvent{Seq: seq, BaseTS: baseTS, Samples: fullSamples}); err != nil {
			return false
		}
		return true
	}
	if !sendFull(dashboardCollector.Snapshot(minutes, nic, disk, dashboardMaxPoints)) {
		return
	}
	ctx := r.Context()
	keepaliveTicker := time.NewTicker(sseKeepaliveInterval)
	defer keepaliveTicker.Stop()
	sampleTicker := time.NewTicker(time.Second)
	defer sampleTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepaliveTicker.C:
			if err := sse.SendComment(); err != nil {
				return
			}
		case <-sampleTicker.C:
			metadata := dashboardCollector.Metadata()
			if metadata.Enabled != lastEnabled || metadata.MemoryMaxMin != lastMemoryMaxMin {
				if !sendFull(dashboardCollector.Snapshot(minutes, nic, disk, dashboardMaxPoints)) {
					return
				}
				continue
			}
			visibleInterfaces := visibleDashboardDeviceList(metadata.Interfaces, isAdmin)
			visibleDisks := visibleDashboardDeviceList(metadata.Disks, isAdmin)
			interfacesKey := strings.Join(visibleInterfaces, "\x00")
			disksKey := strings.Join(visibleDisks, "\x00")
			if interfacesKey != lastInterfaces || disksKey != lastDisks {
				if err := sse.SendEvent("dashboard_options", dashboardOptionsEvent{Seq: time.Now().Unix(), Interfaces: visibleInterfaces, Disks: visibleDisks}); err != nil {
					return
				}
				lastInterfaces = interfacesKey
				lastDisks = disksKey
			}
			if !metadata.Enabled {
				continue
			}
			latest, exists := dashboardCollector.Latest(nic, disk)
			if !exists || !latest.Time.After(time.Unix(lastSampleTime, 0)) {
				continue
			}
			if lastBaseTS <= 0 {
				lastBaseTS = latest.Time.Unix()
			}
			lastSeq += 1
			if err := sse.SendEvent("dashboard_append", dashboardAppendEvent{Seq: lastSeq, BaseTS: lastBaseTS, Sample: encodeDashboardSample(latest, lastBaseTS)}); err != nil {
				return
			}
			lastSampleTime = latest.Time.Unix()
		}
	}
}

func parseDashboardMinutes(w http.ResponseWriter, r *http.Request, isAdmin bool) (int, bool) {
	minutesText := strings.TrimSpace(r.URL.Query().Get("minutes"))
	if minutesText == "" {
		return 30, true
	}
	minutes, err := strconv.Atoi(minutesText)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, "minutes 必须是整数", err)
		return 0, false
	}
	if minutes <= 0 {
		web.WriteAPIError(w, http.StatusBadRequest, "minutes 必须大于 0", nil)
		return 0, false
	}
	if !isAdmin && minutes > dashboardPublicMaxMinutes {
		minutes = dashboardPublicMaxMinutes
	}
	return minutes, true
}

func parseDashboardStreamMinutes(sse *web.SSEWriter, r *http.Request, isAdmin bool) (int, bool) {
	minutesText := strings.TrimSpace(r.URL.Query().Get("minutes"))
	if minutesText == "" {
		return 30, true
	}
	minutes, err := strconv.Atoi(minutesText)
	if err != nil || minutes <= 0 {
		_ = sse.SendEvent("dashboard_error", dashboardErrorEvent{Message: "minutes 必须是大于 0 的整数."})
		return 0, false
	}
	if !isAdmin && minutes > dashboardPublicMaxMinutes {
		minutes = dashboardPublicMaxMinutes
	}
	return minutes, true
}

func buildDashboardStreamSamples(samples []metrics.PublicSample, baseTS int64) (int64, int64, [][]int64) {
	if len(samples) == 0 {
		return 0, 0, [][]int64{}
	}
	if baseTS <= 0 {
		baseTS = samples[0].Time.Unix()
	}
	rows := make([][]int64, 0, len(samples))
	for i := range samples {
		rows = append(rows, encodeDashboardSample(samples[i], baseTS))
	}
	return int64(len(rows)), baseTS, rows
}

func buildDashboardScaleHints(samples []metrics.PublicSample) dashboardScaleHints {
	var hints dashboardScaleHints
	for i := range samples {
		sample := samples[i]
		hints.MemoryTotalMax = maxInt64(hints.MemoryTotalMax, safeDashboardUint64(sample.MemoryTotalBytes))
		hints.SwapTotalMax = maxInt64(hints.SwapTotalMax, safeDashboardUint64(sample.SwapTotalBytes))
		hints.DiskBPSMax = maxInt64(hints.DiskBPSMax, safeDashboardUint64(sample.DiskReadBPS))
		hints.DiskBPSMax = maxInt64(hints.DiskBPSMax, safeDashboardUint64(sample.DiskWriteBPS))
		hints.NetworkBPSMax = maxInt64(hints.NetworkBPSMax, safeDashboardUint64(sample.NetworkRxBPS))
		hints.NetworkBPSMax = maxInt64(hints.NetworkBPSMax, safeDashboardUint64(sample.NetworkTxBPS))
		hints.ConnectionMax = maxInt64(hints.ConnectionMax, safeDashboardUint64(sample.TCPConnectionCount))
		hints.ConnectionMax = maxInt64(hints.ConnectionMax, safeDashboardUint64(sample.UDPConnectionCount))
	}
	return hints
}

func maxInt64(a int64, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

func lastPublicSampleUnix(sample *metrics.PublicSample) int64 {
	if sample == nil {
		return 0
	}
	return sample.Time.Unix()
}

func encodeDashboardSample(sample metrics.PublicSample, baseTS int64) []int64 {
	return []int64{
		sample.Time.Unix() - baseTS,
		encodeDashboardCPU(sample.CPUPercent),
		safeDashboardUint64(sample.MemoryUsedBytes),
		safeDashboardUint64(sample.MemoryTotalBytes),
		safeDashboardUint64(sample.SwapUsedBytes),
		safeDashboardUint64(sample.SwapTotalBytes),
		safeDashboardUint64(sample.NetworkRxBPS),
		safeDashboardUint64(sample.NetworkTxBPS),
		safeDashboardUint64(sample.DiskReadBPS),
		safeDashboardUint64(sample.DiskWriteBPS),
		safeDashboardUint64(sample.TCPConnectionCount),
		safeDashboardUint64(sample.UDPConnectionCount),
	}
}

func encodeDashboardCPU(value float64) int64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return int64(math.Round(value * 10))
}

func safeDashboardUint64(value uint64) int64 {
	const maxSafeInteger = uint64(9007199254740991)
	if value > maxSafeInteger {
		return int64(maxSafeInteger)
	}
	return int64(value)
}
