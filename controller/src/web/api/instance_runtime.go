package api

import (
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"

	"sort"
	"strings"
	"sync"
	"time"
)

type instanceDetailResponse struct {
	instanceListItem
	Path                     string     `json:"path"`
	Command                  string     `json:"command"`
	AccessLinks              string     `json:"access_links,omitempty"`
	Terminal                 int        `json:"terminal"`
	ActiveTerminal           int        `json:"active_terminal"`
	InputEncoding            string     `json:"input_encoding,omitempty"`
	OutputEncoding           string     `json:"output_encoding,omitempty"`
	StopCommand              string     `json:"stop_command,omitempty"`
	CleanupCommand           string     `json:"cleanup_command,omitempty"`
	AutoStart                bool       `json:"auto_start"`
	StartPriority            *int       `json:"start_priority,omitempty"`
	AutoRestart              bool       `json:"auto_restart"`
	RestartInterval          *int       `json:"restart_interval,omitempty"`
	Tasks                    []cfg.Task `json:"tasks,omitempty"`
	HistorySize              int        `json:"history_size"`
	InstanceUpdateStagingDir string     `json:"instance_update_staging_dir"`
}

type instanceListItem struct {
	Name           string `json:"name"`
	Group          string `json:"group,omitempty"`
	AccessLinks    string `json:"access_links,omitempty"`
	Terminal       int    `json:"terminal"`
	ActiveTerminal int    `json:"active_terminal"`
	Running        bool   `json:"running"`
	Updating       bool   `json:"updating"`
	Restarting     bool   `json:"restarting"`
	StartTime      string `json:"start_time,omitempty"`
	RestartCount   int    `json:"restart_count"`
}

type instanceEventSignal struct {
	full bool
	seq  int64
}

type instanceListSubscriber struct {
	ch       chan instanceEventSignal
	needFull bool
}

type instanceListEventPayload struct {
	Version int64              `json:"version"`
	Items   []instanceListItem `json:"items"`
}

type instanceStatusMeta struct {
	changedAt    time.Time
	broadcastAt  time.Time
	broadcastSeq int64
}

const (
	instanceStatusBroadcastTick     = 500 * time.Millisecond
	instanceStatusBroadcastInterval = time.Second
)

var (
	instanceEvents = &instanceEventHub{
		subs:          make(map[*instanceListSubscriber]struct{}),
		statusPending: make(map[string]instanceStatusMeta),
	}
)

type instanceEventHub struct {
	mu                 sync.Mutex
	subs               map[*instanceListSubscriber]struct{}
	statusPending      map[string]instanceStatusMeta
	statusTickerStop   chan struct{}
	eventVersion       int64
	structureBroadcast time.Time
}

func subscribeInstanceListUpdates() *instanceListSubscriber {
	sub := &instanceListSubscriber{ch: make(chan instanceEventSignal, 8)}
	instanceEvents.mu.Lock()
	instanceEvents.subs[sub] = struct{}{}
	instanceEvents.mu.Unlock()
	return sub
}

func unsubscribeInstanceListUpdates(sub *instanceListSubscriber) {
	if sub == nil {
		return
	}
	instanceEvents.mu.Lock()
	delete(instanceEvents.subs, sub)
	instanceEvents.mu.Unlock()
}

func broadcastInstanceSignalLocked(signal instanceEventSignal) {
	for sub := range instanceEvents.subs {
		if sub == nil {
			continue
		}
		if sub.needFull {
			continue
		}
		select {
		case sub.ch <- signal:
		default:
			sub.needFull = true
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- instanceEventSignal{full: true, seq: signal.seq}:
			default:
			}
		}
	}
}

func BroadcastInstanceListUpdates() {
	instanceEvents.mu.Lock()
	if !instanceEvents.structureBroadcast.IsZero() && time.Since(instanceEvents.structureBroadcast) < time.Second {
		instanceEvents.mu.Unlock()
		return
	}
	instanceEvents.structureBroadcast = time.Now()
	instanceEvents.eventVersion += 1
	clear(instanceEvents.statusPending)
	broadcastInstanceSignalLocked(instanceEventSignal{full: true, seq: instanceEvents.eventVersion})
	instanceEvents.mu.Unlock()
}

func startInstanceStatusTicker() {
	instanceEvents.mu.Lock()
	if instanceEvents.statusTickerStop != nil {
		instanceEvents.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	instanceEvents.statusTickerStop = stopCh
	instanceEvents.mu.Unlock()
	go runInstanceStatusTicker(stopCh)
}

func runInstanceStatusTicker(stopCh <-chan struct{}) {
	now := time.Now()
	next := now.Truncate(instanceStatusBroadcastTick).Add(instanceStatusBroadcastTick)
	initialDelay := time.Until(next)
	if initialDelay < 0 {
		initialDelay = 0
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-stopCh:
		return
	}

	ticker := time.NewTicker(instanceStatusBroadcastTick)
	defer ticker.Stop()
	for {
		flushInstanceStatusUpdates(time.Now())
		select {
		case <-ticker.C:
		case <-stopCh:
			return
		}
	}
}

func StopInstanceStatusTicker() {
	instanceEvents.mu.Lock()
	stopCh := instanceEvents.statusTickerStop
	instanceEvents.statusTickerStop = nil
	clear(instanceEvents.statusPending)
	instanceEvents.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
}

func BroadcastInstanceStatusUpdate(instanceName string) {
	name := strings.TrimSpace(instanceName)
	if name == "" {
		BroadcastInstanceListUpdates()
		return
	}
	startInstanceStatusTicker()
	instanceEvents.mu.Lock()
	meta := instanceEvents.statusPending[name]
	meta.changedAt = time.Now()
	instanceEvents.statusPending[name] = meta
	instanceEvents.mu.Unlock()
}

func flushInstanceStatusUpdates(now time.Time) {
	instanceEvents.mu.Lock()
	defer instanceEvents.mu.Unlock()
	if len(instanceEvents.statusPending) == 0 {
		return
	}
	readyNames := make([]string, 0, len(instanceEvents.statusPending))
	for name, meta := range instanceEvents.statusPending {
		if meta.changedAt.IsZero() {
			delete(instanceEvents.statusPending, name)
			continue
		}
		if !meta.broadcastAt.IsZero() && !meta.changedAt.After(meta.broadcastAt) {
			delete(instanceEvents.statusPending, name)
			continue
		}
		if !meta.broadcastAt.IsZero() && now.Sub(meta.broadcastAt) < instanceStatusBroadcastInterval {
			continue
		}
		readyNames = append(readyNames, name)
	}
	if len(readyNames) == 0 {
		return
	}
	instanceEvents.eventVersion += 1
	patchSeq := instanceEvents.eventVersion
	for _, name := range readyNames {
		meta := instanceEvents.statusPending[name]
		meta.broadcastAt = now
		meta.broadcastSeq = patchSeq
		instanceEvents.statusPending[name] = meta
	}
	broadcastInstanceSignalLocked(instanceEventSignal{full: false, seq: patchSeq})
}

func getInstanceListResponse(authedUser *cfg.AuthUser) []instanceListItem {
	resp := make([]instanceListItem, 0)
	processes := process.List()

	for _, ip := range processes {
		ins := ip.InstanceSnapshot()
		if !web.CanAccessInstance(authedUser, ins.Name) {
			continue
		}
		status := ip.StatusSnapshot()
		resp = append(resp, instanceListItem{
			Name:           status.Name,
			Group:          status.Group,
			AccessLinks:    ins.AccessLinks,
			Terminal:       cfg.NormalizeTerminalMode(ins.Terminal),
			ActiveTerminal: status.ActiveTerminal,
			Running:        status.Running,
			Updating:       status.Updating,
			Restarting:     status.Restarting,
			StartTime:      status.StartTime,
			RestartCount:   status.RestartCount,
		})
	}

	sort.Slice(resp, func(i, j int) bool {
		return resp[i].Name < resp[j].Name
	})
	return resp
}

func getInstanceStatusPatchResponse(authedUser *cfg.AuthUser, seq int64) []instanceListItem {
	resp := make([]instanceListItem, 0)
	if seq <= 0 {
		return resp
	}

	instanceEvents.mu.Lock()
	pendingNames := make([]string, 0, len(instanceEvents.statusPending))
	for name, meta := range instanceEvents.statusPending {
		if meta.changedAt.IsZero() {
			delete(instanceEvents.statusPending, name)
			continue
		}
		if meta.broadcastSeq != seq {
			continue
		}
		pendingNames = append(pendingNames, name)
	}
	instanceEvents.mu.Unlock()
	if len(pendingNames) == 0 {
		return resp
	}

	processes := make([]*process.InstanceProcess, 0, len(pendingNames))
	for _, name := range pendingNames {
		ip, ok := process.Get(name)
		if !ok || ip == nil {
			continue
		}
		processes = append(processes, ip)
	}

	for _, ip := range processes {
		status := ip.StatusSnapshot()
		if !web.CanAccessInstance(authedUser, status.Name) {
			continue
		}
		resp = append(resp, instanceListItem{
			Name:           status.Name,
			Group:          status.Group,
			AccessLinks:    ip.InstanceSnapshot().AccessLinks,
			Terminal:       cfg.NormalizeTerminalMode(ip.InstanceSnapshot().Terminal),
			ActiveTerminal: status.ActiveTerminal,
			Running:        status.Running,
			Updating:       status.Updating,
			Restarting:     status.Restarting,
			StartTime:      status.StartTime,
			RestartCount:   status.RestartCount,
		})
	}

	sort.Slice(resp, func(i, j int) bool {
		return resp[i].Name < resp[j].Name
	})
	return resp
}
