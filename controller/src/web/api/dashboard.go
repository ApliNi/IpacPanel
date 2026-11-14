package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/metrics"
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"
	"math"
	"net/http"
	"strings"
	"sync"
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
type dashboardBroadcastItem struct {
	metadata   metrics.Metadata
	sample     metrics.PublicSample
	hasSample  bool
	sampleTime int64
}

type dashboardHub struct {
	mu          sync.Mutex
	subscribers map[chan dashboardBroadcastItem]struct{}
	running     bool
	stopCh      chan struct{}
}

var globalDashboardHub = &dashboardHub{
	subscribers: make(map[chan dashboardBroadcastItem]struct{}),
}

func (h *dashboardHub) subscribe() chan dashboardBroadcastItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan dashboardBroadcastItem, 4)
	h.subscribers[ch] = struct{}{}
	if !h.running {
		h.running = true
		h.stopCh = make(chan struct{})
		go h.run(h.stopCh)
	}
	return ch
}

func (h *dashboardHub) unsubscribe(ch chan dashboardBroadcastItem) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, ch)
	close(ch)
	if len(h.subscribers) == 0 && h.running {
		h.running = false
		if h.stopCh != nil {
			close(h.stopCh)
			h.stopCh = nil
		}
	}
}

func (h *dashboardHub) run(stopCh chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastSampleTime int64
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if dashboardCollector == nil {
				continue
			}
			metadata := dashboardCollector.Metadata()
			var item dashboardBroadcastItem
			item.metadata = metadata
			if metadata.Enabled {
				if latest, exists := dashboardCollector.Latest("", ""); exists {
					ts := latest.Time.Unix()
					if ts > lastSampleTime {
						lastSampleTime = ts
						item.sampleTime = ts
						item.sample = latest
						item.hasSample = true
					}
				}
			}
			h.mu.Lock()
			for ch := range h.subscribers {
				select {
				case ch <- item:
				default:
				}
			}
			h.mu.Unlock()
		}
	}
}


type dashboardStreamFilter struct {
	NIC  string `json:"nic"`
	Disk string `json:"disk"`
}

type dashboardRequest struct {
	Minutes int    `json:"minutes"`
	NIC     string `json:"nic"`
	Disk    string `json:"disk"`
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
	Columns [][]int64 `json:"columns"`
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
	Columns         [][]int64 `json:"columns"`
}

type dashboardErrorEvent struct {
	Error string `json:"error"`
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

func HandleApiDashboardEvents(w http.ResponseWriter, r *http.Request) {
	var req dashboardRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	// Guard owns optional authentication for dashboard events: invalid auth
	// cookies are rejected before this handler, valid users are written to request
	// context, and missing cookies may use the public-dashboard fallback.
	authedUser, authed := authz.DefaultRuntime.CurrentAuthUser(r)
	if !authed && !isPublicDashboardEnabled() {
		web.WriteAPIError(w, http.StatusUnauthorized, msg.Unauthorized, nil)
		return
	}
	release, ok := web.AcquireSSESlot(r)
	if !ok {
		web.WriteAPIError(w, http.StatusTooManyRequests, msg.TooManyStreams, nil)
		return
	}
	defer release()
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	web.LogWebAccess(w, r, http.StatusOK)
	if dashboardCollector == nil {
		_ = sse.SendEvent("dashboard_error", dashboardErrorEvent{Error: msg.DashboardMetricsNotInitialized})
		return
	}
	isAdmin := isDashboardAdmin(authedUser)
	var username string
	if authedUser != nil {
		username = authedUser.User
	}
	minutes, ok := parseDashboardStreamMinutes(sse, req.Minutes)
	if !ok {
		return
	}
	nic := ""
	disk := ""
	if isAdmin {
		nic = strings.TrimSpace(req.NIC)
		disk = strings.TrimSpace(req.Disk)
	}
	var lastSeq int64
	var lastBaseTS int64
	var lastSampleTime int64
	var lastMemoryMaxMin int
	var lastEnabled bool
	var lastInterfaces string
	var lastDisks string
	sendFull := func(snapshot metrics.Snapshot) bool {
		seq, baseTS, columns := buildDashboardStreamColumns(snapshot.Samples, 0)
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
				Interfaces: visibleInterfaces, Disks: visibleDisks, BaseTS: 0, Columns: [][]int64{},
			}) == nil
		}
		fullMeta := dashboardFullMetaEvent{
			Seq: seq, Enabled: snapshot.Enabled, MemoryMaxMin: snapshot.MemoryMaxMin, SelectedMinutes: minutes,
			Filter: dashboardStreamFilter{NIC: nic, Disk: disk}, Interfaces: visibleInterfaces, Disks: visibleDisks,
			BaseTS: baseTS, SampleSchema: dashboardSampleSchema, ScaleHints: buildDashboardScaleHints(snapshot.Samples), SampleTotal: len(snapshot.Samples),
		}
		if err := sse.SendEvent("dashboard_full_meta", fullMeta); err != nil {
			return false
		}
		if err := sse.SendEvent("dashboard_full_samples", dashboardFullSamplesEvent{Seq: seq, BaseTS: baseTS, Columns: columns}); err != nil {
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
	subCh := globalDashboardHub.subscribe()
	defer globalDashboardHub.unsubscribe(subCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepaliveTicker.C:
			if err := sse.SendComment(); err != nil {
				return
			}
		case item, ok := <-subCh:
			if !ok {
				return
			}
			if username != "" {
				// 公共仪表板 (username 为空) 跳过重验; 登录用户被禁用或删除时断开连接.
				freshUser, stillValid := authz.AuthUserByUsername(username)
				if !stillValid {
					return
				}
				if freshUser != authedUser {
					authedUser = freshUser
					// isAdmin 依赖 authedUser 指针, 重验后需重建; nic/disk 随权限同步.
					isAdmin = isDashboardAdmin(authedUser)
					if isAdmin {
						nic = strings.TrimSpace(req.NIC)
						disk = strings.TrimSpace(req.Disk)
					} else {
						nic = ""
						disk = ""
					}
				}
			}
			metadata := item.metadata
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
			if !metadata.Enabled || !item.hasSample || item.sampleTime <= lastSampleTime {
				continue
			}
			sample := item.sample
			if nic != "" || disk != "" {
				if customLatest, exists := dashboardCollector.Latest(nic, disk); exists {
					sample = customLatest
				}
			}
			if lastBaseTS <= 0 {
				lastBaseTS = sample.Time.Unix()
			}
			lastSeq += 1
			if err := sse.SendEvent("dashboard_append", dashboardAppendEvent{Seq: lastSeq, BaseTS: lastBaseTS, Sample: encodeDashboardSample(sample, lastBaseTS)}); err != nil {
				return
			}
			lastSampleTime = sample.Time.Unix()
		}
	}
}

func parseDashboardStreamMinutes(sse *web.SSEWriter, minutes int) (int, bool) {
	if minutes == 0 {
		return 30, true
	}
	if minutes <= 0 {
		_ = sse.SendEvent("dashboard_error", dashboardErrorEvent{Error: msg.DashboardMinutesMustBePositiveInteger})
		return 0, false
	}
	return minutes, true
}

func buildDashboardStreamColumns(samples []metrics.PublicSample, baseTS int64) (int64, int64, [][]int64) {
	n := len(samples)
	if n == 0 {
		return 0, baseTS, make([][]int64, len(dashboardSampleSchema))
	}
	if baseTS <= 0 {
		baseTS = samples[0].Time.Unix()
	}
	columns := make([][]int64, len(dashboardSampleSchema))
	for i := range columns {
		columns[i] = make([]int64, n)
	}
	for i := range samples {
		sample := samples[i]
		columns[0][i] = sample.Time.Unix() - baseTS
		columns[1][i] = encodeDashboardCPU(sample.CPUPercent)
		columns[2][i] = safeDashboardUint64(sample.MemoryUsedBytes)
		columns[3][i] = safeDashboardUint64(sample.MemoryTotalBytes)
		columns[4][i] = safeDashboardUint64(sample.SwapUsedBytes)
		columns[5][i] = safeDashboardUint64(sample.SwapTotalBytes)
		columns[6][i] = safeDashboardUint64(sample.NetworkRxBPS)
		columns[7][i] = safeDashboardUint64(sample.NetworkTxBPS)
		columns[8][i] = safeDashboardUint64(sample.DiskReadBPS)
		columns[9][i] = safeDashboardUint64(sample.DiskWriteBPS)
		columns[10][i] = safeDashboardUint64(sample.TCPConnectionCount)
		columns[11][i] = safeDashboardUint64(sample.UDPConnectionCount)
	}
	return int64(n), baseTS, columns
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
