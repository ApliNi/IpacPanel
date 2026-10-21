package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/logbuf"
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type processState uint8

const (
	processStateStopped processState = iota
	processStateRunning
	processStateStopping
	processStateStoppingForRestart
	processStateRestartWaiting
)

type InstanceProcess struct {
	Ins                          cfg.Instance
	State                        processState
	Deleting                     bool
	Starting                     bool
	Updating                     bool
	Running                      bool
	Restarting                   bool
	RestartCancel                chan struct{}
	StopCancel                   chan struct{}
	StartCancel                  chan struct{}
	Cols                         uint16
	Rows                         uint16
	StartTime                    time.Time
	RestartCount                 int
	ActiveTerminalMode           int
	Mu                           sync.RWMutex
	outputMu                     sync.Mutex
	Clients                      map[*WSClient]bool
	History                      TerminalHistory
	ProxySeq                     uint64
	StopSeq                      uint64
	StartSeq                     uint64
	PTYAlternateScreenActive     bool
	PTYAlternateScreenPending    []byte
	InputMu                      sync.Mutex
	overrideCommand              string
}

type InstanceStatus struct {
	cfg.Instance
	Running        bool   `json:"running"`
	Updating       bool   `json:"updating"`
	Restarting     bool   `json:"restarting"`
	StartTime      string `json:"start_time,omitempty"`
	RestartCount   int    `json:"restart_count"`
	ActiveTerminal int    `json:"active_terminal"`
}

// InstanceStatusProjection 是实例轻量只读状态投影, 不深拷贝 Task 或指针字段.
type InstanceStatusProjection struct {
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

func (sp *InstanceProcess) StatusProjectionLocked() InstanceStatusProjection {
	startTime := ""
	if !sp.StartTime.IsZero() {
		startTime = sp.StartTime.Format(cfg.DisplayTimeLayout)
	}
	return InstanceStatusProjection{
		Name:           sp.Ins.Name,
		Group:          sp.Ins.Group,
		AccessLinks:    sp.Ins.AccessLinks,
		Terminal:       cfg.NormalizeTerminalMode(sp.Ins.Terminal),
		ActiveTerminal: sp.activeTerminalLocked(),
		Running:        sp.Running,
		Updating:       sp.Updating,
		Restarting:     sp.Restarting,
		StartTime:      startTime,
		RestartCount:   sp.RestartCount,
	}
}

func (sp *InstanceProcess) StatusProjection() InstanceStatusProjection {
	if sp == nil {
		return InstanceStatusProjection{}
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.StatusProjectionLocked()
}

func (sp *InstanceProcess) StatusSnapshotLocked() InstanceStatus {
	startTime := ""
	if !sp.StartTime.IsZero() {
		startTime = sp.StartTime.Format(cfg.DisplayTimeLayout)
	}
	return InstanceStatus{
		Instance:       sp.InstanceSnapshotLocked(),
		Running:        sp.Running,
		Updating:       sp.Updating,
		Restarting:     sp.Restarting,
		StartTime:      startTime,
		RestartCount:   sp.RestartCount,
		ActiveTerminal: sp.activeTerminalLocked(),
	}
}

func (sp *InstanceProcess) activeTerminalLocked() int {
	if sp == nil {
		return cfg.Terminal
	}
	terminal := cfg.NormalizeTerminalMode(sp.Ins.Terminal)
	if (sp.Running || sp.Restarting) && sp.ActiveTerminalMode != 0 {
		terminal = cfg.NormalizeTerminalMode(sp.ActiveTerminalMode)
	}
	return terminal
}

func (sp *InstanceProcess) ActiveTerminalLocked() int {
	return sp.activeTerminalLocked()
}

func (sp *InstanceProcess) ActiveTerminalModeSnapshot() int {
	if sp == nil {
		return cfg.Terminal
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.activeTerminalLocked()
}

func (sp *InstanceProcess) StatusSnapshot() InstanceStatus {
	if sp == nil {
		return InstanceStatus{}
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.StatusSnapshotLocked()
}

func (sp *InstanceProcess) InstanceSnapshotLocked() cfg.Instance {
	if sp == nil {
		return cfg.Instance{}
	}
	ins := sp.Ins
	ins.StartPriority = cfg.CloneIntPtr(ins.StartPriority)
	ins.RestartInterval = cfg.CloneIntPtr(ins.RestartInterval)
	ins.Tasks = append([]cfg.Task(nil), ins.Tasks...)
	return ins
}

func (sp *InstanceProcess) InstanceSnapshot() cfg.Instance {
	if sp == nil {
		return cfg.Instance{}
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.InstanceSnapshotLocked()
}

func (sp *InstanceProcess) IsDeleting() bool {
	if sp == nil {
		return false
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.Deleting
}

func (sp *InstanceProcess) IsRunning() bool {
	if sp == nil {
		return false
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.Running
}

func (sp *InstanceProcess) IsUpdating() bool {
	if sp == nil {
		return false
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.Updating
}

func (sp *InstanceProcess) IsActive() bool {
	if sp == nil {
		return false
	}
	sp.Mu.RLock()
	defer sp.Mu.RUnlock()
	return sp.Running || sp.Restarting || sp.Starting
}


type restartRequestMode uint8

const (
	restartRequestModeDefault restartRequestMode = iota
	restartRequestModeStrict
)

type RestartRequestResult uint8

const (
	RestartRequestAccepted RestartRequestResult = iota
	RestartRequestNoopStarting
	RestartRequestNoopAlreadyRestarting
	RestartRequestSkippedStopped
	RestartRequestRejectedDeleting
)

func (result RestartRequestResult) IsAccepted() bool {
	return result == RestartRequestAccepted
}

func (result RestartRequestResult) IsAllowed() bool {
	return result == RestartRequestAccepted || result == RestartRequestNoopStarting || result == RestartRequestNoopAlreadyRestarting
}

type startReservation struct {
	ins                      cfg.Instance
	historyLimit             int
	instanceUpdateStagingDir string
	cols                     uint16
	rows                     uint16
	startSeq                 uint64
	resetRestarting          bool
	instanceName             string
	cancelCh                 chan struct{}
}

type preparedStart struct {
	state     *DaemonRuntimeState
	startTime time.Time
}

func (sp *InstanceProcess) syncStatusFlagsLocked() {
	switch sp.State {
	case processStateRunning:
		sp.Running = true
		sp.Restarting = false
	case processStateStopping:
		sp.Running = true
		sp.Restarting = false
	case processStateStoppingForRestart:
		sp.Running = true
		sp.Restarting = true
	case processStateRestartWaiting:
		sp.Running = false
		sp.Restarting = true
	default:
		sp.Running = false
		sp.Restarting = false
	}
}

func (sp *InstanceProcess) setStateLocked(state processState) {
	sp.State = state
	sp.syncStatusFlagsLocked()
}

func (sp *InstanceProcess) enterStoppedStateLocked() {
	sp.overrideCommand = ""
	sp.setStateLocked(processStateStopped)
}

func (sp *InstanceProcess) enterRunningStateLocked() {
	sp.setStateLocked(processStateRunning)
}

func (sp *InstanceProcess) enterStoppingStateLocked() {
	sp.setStateLocked(processStateStopping)
}

func (sp *InstanceProcess) enterStoppingForRestartLocked() {
	sp.setStateLocked(processStateStoppingForRestart)
}

func (sp *InstanceProcess) enterRestartWaitingLocked() {
	sp.setStateLocked(processStateRestartWaiting)
}

func (sp *InstanceProcess) cancelRestartLocked() {
	sp.overrideCommand = ""
	if sp.RestartCancel == nil {
		return
	}
	close(sp.RestartCancel)
	sp.RestartCancel = nil
}

func (sp *InstanceProcess) cancelStopLocked() {
	if sp.StopCancel == nil {
		return
	}
	close(sp.StopCancel)
	sp.StopCancel = nil
	sp.StopSeq++
}

func (sp *InstanceProcess) cancelStartLocked() {
	if sp.StartCancel != nil {
		close(sp.StartCancel)
		sp.StartCancel = nil
	}
	sp.Starting = false
	sp.Updating = false
	sp.StartSeq++
}

func (sp *InstanceProcess) beginStartLocked() chan struct{} {
	sp.cancelStartLocked()
	sp.Starting = true
	sp.StartCancel = make(chan struct{})
	return sp.StartCancel
}

func (sp *InstanceProcess) beginStopLocked(state processState) {
	sp.cancelStopLocked()
	sp.setStateLocked(state)
	sp.StopCancel = make(chan struct{})
}

func (sp *InstanceProcess) setProcessExitedLocked() {
	sp.cancelStopLocked()
	sp.cancelStartLocked()
	sp.ProxySeq = 0
	sp.resetPTYAlternateScreenStateLocked()
	sp.enterStoppedStateLocked()
	sp.shrinkTerminalHistoryLocked()
}

func (sp *InstanceProcess) setProcessStartedLocked(startTime time.Time, restartCount int) uint64 {
	sp.Starting = false
	sp.Updating = false
	sp.StartCancel = nil
	sp.ProxySeq++
	if !startTime.IsZero() {
		sp.StartTime = startTime
	}
	sp.RestartCount = restartCount
	sp.enterRunningStateLocked()
	return sp.ProxySeq
}

func (sp *InstanceProcess) markUpdatingLocked(reserved *startReservation) bool {
	if reserved == nil || !sp.Starting || sp.StartSeq != reserved.startSeq || sp.Deleting {
		return false
	}
	sp.Updating = true
	NotifyInstanceStatusChanged(reserved.instanceName)
	return true
}

func (sp *InstanceProcess) clearUpdatingLocked(reserved *startReservation) bool {
	if reserved == nil || !sp.Starting || sp.StartSeq != reserved.startSeq {
		return false
	}
	sp.Updating = false
	NotifyInstanceStatusChanged(reserved.instanceName)
	return true
}

func (sp *InstanceProcess) reserveStartLocked(historyLimit int, instanceUpdateStagingDir string) (*startReservation, error) {
	if sp.Deleting {
		return nil, errors.New(msg.InstanceBeingDeleted)
	}
	if sp.State == processStateStopping {
		return nil, errors.New(msg.InstanceStopping)
	}
	if sp.State == processStateStoppingForRestart {
		return nil, errors.New(msg.InstanceRestarting)
	}
	if sp.Starting || sp.Running {
		sp.overrideCommand = ""
		return nil, nil
	}
	ins := sp.InstanceSnapshotLocked()
	if sp.overrideCommand != "" {
		ins.Command = sp.overrideCommand
		sp.overrideCommand = ""
	}
	reserved := &startReservation{
		ins:                      ins,
		historyLimit:             historyLimit,
		instanceUpdateStagingDir: instanceUpdateStagingDir,
		cols:                     sp.Cols,
		rows:                     sp.Rows,
		instanceName:             ins.Name,
	}
	if sp.Restarting {
		sp.cancelRestartLocked()
		sp.cancelStopLocked()
		sp.enterStoppedStateLocked()
		NotifyInstanceStatusChanged(reserved.instanceName)
		reserved.resetRestarting = true
	}
	reserved.cancelCh = sp.beginStartLocked()
	reserved.startSeq = sp.StartSeq
	return reserved, nil
}

func (sp *InstanceProcess) prepareStart(reserved *startReservation) (*preparedStart, error) {
	if reserved == nil {
		return nil, nil
	}
	resolvedPath, err := cfg.ResolveInstancePath(reserved.ins.Path)
	if err != nil {
		return nil, err
	}
	resolvedPath = filepath.Clean(resolvedPath)
	unlockStartPath := lockInstanceStartPath(resolvedPath)
	defer unlockStartPath()
	if err := os.MkdirAll(resolvedPath, 0755); err != nil {
		return nil, err
	}
	sp.Mu.Lock()
	canUpdate := sp.markUpdatingLocked(reserved)
	sp.Mu.Unlock()
	if !canUpdate {
		return nil, errors.New(msg.InstanceUpdateCanceled)
	}
	if err := ApplyStagedInstanceUpdate(resolvedPath, reserved.instanceUpdateStagingDir, reserved.cancelCh); err != nil {
		log.Printf(msg.InstanceUpdateFailedLogFmt, reserved.ins.Name, err)
		_ = logbuf.EmitInstance(logbuf.LevelError, reserved.ins.Name, fmt.Sprintf(msg.InstanceUpdateFailedLogFmt, reserved.ins.Name, err))
		return nil, err
	}
	sp.Mu.Lock()
	canStart := sp.clearUpdatingLocked(reserved)
	sp.Mu.Unlock()
	if !canStart {
		return nil, errors.New(msg.InstanceUpdateCanceled)
	}
	sp.Mu.Lock()
	if sp.Starting && sp.StartSeq == reserved.startSeq {
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, BuildNormalTerminalSystemMessage(msg.StartingInstance), reserved.historyLimit)
	}
	sp.Mu.Unlock()
	commandArgv, err := CompileInstanceCommandArgv(reserved.ins.Command, resolvedPath, reserved.ins.Terminal)
	if err != nil {
		return nil, err
	}
	cleanupCommandArgv, err := CompileInstanceCleanupCommandArgv(reserved.ins.CleanupCommand, resolvedPath)
	if err != nil {
		return nil, err
	}
	state, err := startDaemonInstance(reserved.ins.Name, commandArgv, cleanupCommandArgv, resolvedPath, reserved.ins.Terminal, reserved.ins.InputEncoding, reserved.ins.OutputEncoding, reserved.cols, reserved.rows)
	if err != nil {
		return nil, err
	}
	startTime := time.Now()
	if state != nil && !state.StartTime.IsZero() {
		startTime = state.StartTime
	}
	return &preparedStart{state: state, startTime: startTime}, nil
}

func (sp *InstanceProcess) commitPreparedStartLocked(reserved *startReservation, prepared *preparedStart) (uint64, bool) {
	if reserved == nil || prepared == nil {
		return 0, false
	}
	if !sp.Starting || sp.StartSeq != reserved.startSeq || sp.Deleting {
		return 0, false
	}
	if sp.State == processStateStopping || sp.State == processStateStoppingForRestart {
		return 0, false
	}
	// StartCount 语义: 未曾启动为 -1, 每次成功启动 +1.
	// 优先采用守护进程回传的计数; 守护进程不可达时本地递增.
	startCount := sp.RestartCount
	if prepared.state != nil {
		startCount = prepared.state.RestartCount
	} else {
		startCount++
	}
	sp.ActiveTerminalMode = cfg.NormalizeTerminalMode(reserved.ins.Terminal)
	sp.resetPTYAlternateScreenStateLocked()
	proxySeq := sp.setProcessStartedLocked(prepared.startTime, startCount)
	NotifyInstanceStatusChanged(reserved.ins.Name)
	return proxySeq, true
}

func (sp *InstanceProcess) scheduleAutoRestart() {
	restartInterval := cfg.GetAutoRestartInterval()
	historyLimit := cfg.GetHistoryLimit() * 1024
	instanceUpdateStagingDir := cfg.GetInstanceUpdateStagingDir()
	sp.Mu.Lock()
	ins := sp.InstanceSnapshotLocked()
	if ins.RestartInterval != nil {
		restartInterval = *ins.RestartInterval
	}
	if sp.RestartCancel == nil {
		sp.RestartCancel = make(chan struct{})
	}
	sp.cancelStopLocked()
	cancelCh := sp.RestartCancel
	sp.Mu.Unlock()
	if restartInterval > 0 {
		timer := time.NewTimer(time.Duration(restartInterval) * time.Millisecond)
		select {
		case <-timer.C:
		case <-cancelCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}

	sp.Mu.Lock()
	if sp.State != processStateRestartWaiting || sp.RestartCancel != cancelCh {
		sp.Mu.Unlock()
		return
	}
	sp.RestartCancel = nil
	reserved, err := sp.reserveStartLocked(historyLimit, instanceUpdateStagingDir)
	sp.Mu.Unlock()
	if err != nil {
		sp.Mu.Lock()
		restartMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
		_ = logbuf.EmitInstance(logbuf.LevelError, sp.InstanceSnapshotLocked().Name, fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
		sp.enterStoppedStateLocked()
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, restartMsg, historyLimit)
		NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
		sp.Mu.Unlock()
		return
	}
	if reserved == nil {
		return
	}
	prepared, err := sp.prepareStart(reserved)
	if err != nil {
		sp.Mu.Lock()
		if sp.Starting && sp.StartSeq == reserved.startSeq {
			sp.cancelStartLocked()
			restartMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			_ = logbuf.EmitInstance(logbuf.LevelError, sp.InstanceSnapshotLocked().Name, fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			sp.enterStoppedStateLocked()
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, restartMsg, historyLimit)
			NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
		}
		sp.Mu.Unlock()
		return
	}
	sp.Mu.Lock()
	proxySeq, committed := sp.commitPreparedStartLocked(reserved, prepared)
	sp.Mu.Unlock()
	if !committed {
		_ = stopDaemonInstance(reserved.instanceName, true)
		return
	}
	_ = proxySeq
}

func (sp *InstanceProcess) handleDaemonProcessExit(state *DaemonRuntimeState) bool {
	limit := cfg.GetHistoryLimit() * 1024
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	if sp.State == processStateStopped && !sp.Starting {
		return false
	}

	prevState := sp.State
	sp.setProcessExitedLocked()

	ins := sp.InstanceSnapshotLocked()
	runtimeCode := RuntimeCodeUnknown
	if state != nil {
		runtimeCode = state.RuntimeCode
		if state.RestartCount > sp.RestartCount {
			sp.RestartCount = state.RestartCount
		}
	}
	shouldAutoRestart := prevState == processStateStoppingForRestart || runtimeCode == RuntimeCodeRestarting || (ins.AutoRestart && runtimeCode == RuntimeCodeUnexpectedExit)
	terminalMsg := BuildNormalTerminalSystemMessage(msg.ProcessExited)
	if shouldAutoRestart {
		sp.enterRestartWaitingLocked()
		terminalMsg = BuildNormalTerminalSystemMessage(msg.ProcessExitedWaitingRestart)
	}
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	if !shouldAutoRestart {
		sp.shrinkTerminalHistoryLocked()
	}

	NotifyInstanceStatusChanged(ins.Name)

	return shouldAutoRestart
}

func (sp *InstanceProcess) ScheduleRecoveredAutoRestart(state DaemonRuntimeState) {
	if sp == nil {
		return
	}
	restartInterval := cfg.GetAutoRestartInterval()
	sp.Mu.Lock()
	ins := sp.InstanceSnapshotLocked()
	if ins.RestartInterval != nil {
		restartInterval = *ins.RestartInterval
	}
	delay := time.Duration(restartInterval) * time.Millisecond
	if !state.ExitTime.IsZero() {
		elapsed := time.Since(state.ExitTime)
		if elapsed >= delay {
			delay = 0
		} else {
			delay -= elapsed
		}
	}
	sp.cancelRestartLocked()
	sp.cancelStopLocked()
	sp.enterRestartWaitingLocked()
	sp.RestartCancel = make(chan struct{})
	cancelCh := sp.RestartCancel
	NotifyInstanceStatusChanged(ins.Name)
	sp.Mu.Unlock()

	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-cancelCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}

		sp.Mu.Lock()
		if sp.State != processStateRestartWaiting || sp.RestartCancel != cancelCh {
			sp.Mu.Unlock()
			return
		}
		sp.RestartCancel = nil
		historyLimit := cfg.GetHistoryLimit() * 1024
		instanceUpdateStagingDir := cfg.GetInstanceUpdateStagingDir()
		reserved, err := sp.reserveStartLocked(historyLimit, instanceUpdateStagingDir)
		sp.Mu.Unlock()
		if err != nil {
			sp.Mu.Lock()
			restartMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			_ = logbuf.EmitInstance(logbuf.LevelError, sp.InstanceSnapshotLocked().Name, fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			sp.enterStoppedStateLocked()
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, restartMsg, historyLimit)
			NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
			sp.Mu.Unlock()
			return
		}
		if reserved == nil {
			return
		}
		prepared, err := sp.prepareStart(reserved)
		if err != nil {
			sp.Mu.Lock()
			if sp.Starting && sp.StartSeq == reserved.startSeq {
				sp.cancelStartLocked()
				restartMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
				_ = logbuf.EmitInstance(logbuf.LevelError, sp.InstanceSnapshotLocked().Name, fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
				sp.enterStoppedStateLocked()
				sp.appendAndBroadcastLocked(websocket.BinaryMessage, restartMsg, historyLimit)
				NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
			}
			sp.Mu.Unlock()
			return
		}
		sp.Mu.Lock()
		proxySeq, committed := sp.commitPreparedStartLocked(reserved, prepared)
		sp.Mu.Unlock()
		if !committed {
			_ = stopDaemonInstance(reserved.instanceName, true)
			return
		}
		_ = proxySeq
	}()
}

func formatRuntimeCommand(command string) []byte {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	if command == "^C" {
		return []byte{3}
	}
	return []byte(command + "\r")
}

func sendDaemonStopRequest(instanceName string, stopCommand string, noTerminal bool, runtimeCode int, markErrLogFmt string, writeErrLogFmt string) {
	if strings.TrimSpace(stopCommand) != "" && !noTerminal {
		if err := markDaemonInstanceStopping(instanceName, runtimeCode); err != nil {
			log.Printf(markErrLogFmt, instanceName, err)
		}
		if err := writeDaemonInstanceStdin(instanceName, formatRuntimeCommand(stopCommand)); err != nil {
			log.Printf(writeErrLogFmt, instanceName, err)
		}
		return
	}
	_ = stopDaemonInstanceWithCode(instanceName, false, runtimeCode)
}

func (sp *InstanceProcess) requestRestart(mode restartRequestMode) RestartRequestResult {
	return sp.requestRestartWithKillStopAndCommand(mode, false, "")
}

func (sp *InstanceProcess) requestRestartWithKillStop(mode restartRequestMode, useKillStop bool) RestartRequestResult {
	return sp.requestRestartWithKillStopAndCommand(mode, useKillStop, "")
}

func (sp *InstanceProcess) requestRestartWithKillStopAndCommand(mode restartRequestMode, useKillStop bool, command string) RestartRequestResult {
	shouldSchedule := false

	sp.Mu.Lock()
	if sp.Deleting {
		sp.Mu.Unlock()
		return RestartRequestRejectedDeleting
	}
	if sp.State == processStateStoppingForRestart || sp.State == processStateRestartWaiting {
		sp.Mu.Unlock()
		return RestartRequestNoopAlreadyRestarting
	}
	if sp.Starting {
		sp.Mu.Unlock()
		return RestartRequestNoopStarting
	}
	if sp.State == processStateStopping {
		sp.overrideCommand = strings.TrimSpace(command)
		instanceName := sp.InstanceSnapshotLocked().Name
		sp.beginStopLocked(processStateStoppingForRestart)
		NotifyInstanceStatusChanged(instanceName)
		sp.Mu.Unlock()
		if err := markDaemonInstanceRuntimeCode(instanceName, RuntimeCodeRestarting); err != nil {
			log.Printf(msg.MarkInstanceRestartingIntentFailedLogFmt, instanceName, err)
		}
		if useKillStop {
			_ = stopDaemonInstance(instanceName, true)
		}
		return RestartRequestAccepted
	}
	if sp.Running {
		sp.overrideCommand = strings.TrimSpace(command)
		instanceName := sp.InstanceSnapshotLocked().Name
		sp.beginStopLocked(processStateStoppingForRestart)
		stopCommand := sp.InstanceSnapshotLocked().StopCommand
		noTerminal := cfg.IsNoTerminal(sp.activeTerminalLocked())
		NotifyInstanceStatusChanged(instanceName)
		sp.Mu.Unlock()
		if useKillStop {
			_ = stopDaemonInstanceWithCode(instanceName, true, RuntimeCodeRestarting)
		} else {
			sendDaemonStopRequest(instanceName, stopCommand, noTerminal, RuntimeCodeRestarting, msg.MarkInstanceRestartingIntentFailedLogFmt, msg.WriteInstanceRestartStopCommandFailedLogFmt)
		}
		return RestartRequestAccepted
	}
	if mode == restartRequestModeStrict {
		sp.Mu.Unlock()
		return RestartRequestSkippedStopped
	}
	sp.overrideCommand = strings.TrimSpace(command)
	sp.enterRestartWaitingLocked()
	NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
	shouldSchedule = true
	sp.Mu.Unlock()

	if shouldSchedule {
		go sp.scheduleAutoRestart()
	}
	return RestartRequestAccepted
}

func (sp *InstanceProcess) RequestRestart() bool {
	return sp.RequestRestartResult().IsAllowed()
}

func (sp *InstanceProcess) RequestRestartResult() RestartRequestResult {
	return sp.requestRestart(restartRequestModeDefault)
}

func (sp *InstanceProcess) RequestRestartWithKillStopResult(useKillStop bool) RestartRequestResult {
	return sp.RequestRestartWithKillStopAndCommand(useKillStop, "")
}

func (sp *InstanceProcess) RequestRestartWithKillStopAndCommand(useKillStop bool, command string) RestartRequestResult {
	return sp.requestRestartWithKillStopAndCommand(restartRequestModeDefault, useKillStop, command)
}

func (sp *InstanceProcess) RequestStrictRestart() RestartRequestResult {
	return sp.requestRestart(restartRequestModeStrict)
}

func (sp *InstanceProcess) RequestStrictRestartWithKillStop(useKillStop bool) RestartRequestResult {
	return sp.RequestStrictRestartWithKillStopAndCommand(useKillStop, "")
}

func (sp *InstanceProcess) RequestStrictRestartWithKillStopAndCommand(useKillStop bool, command string) RestartRequestResult {
	return sp.requestRestartWithKillStopAndCommand(restartRequestModeStrict, useKillStop, command)
}

func (sp *InstanceProcess) Start() error {
	return sp.StartWithCommand("")
}

func (sp *InstanceProcess) StartWithCommand(command string) error {
	historyLimit := cfg.GetHistoryLimit() * 1024
	instanceUpdateStagingDir := cfg.GetInstanceUpdateStagingDir()
	sp.Mu.Lock()
	sp.overrideCommand = strings.TrimSpace(command)
	reserved, err := sp.reserveStartLocked(historyLimit, instanceUpdateStagingDir)
	if err != nil {
		sp.overrideCommand = ""
		sp.Mu.Unlock()
		return err
	}
	sp.Mu.Unlock()
	if reserved == nil {
		return nil
	}
	prepared, err := sp.prepareStart(reserved)
	if err != nil {
		sp.Mu.Lock()
		if sp.Starting && sp.StartSeq == reserved.startSeq {
			sp.cancelStartLocked()
			sp.enterStoppedStateLocked()
			NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
		}
		sp.Mu.Unlock()
		return err
	}
	sp.Mu.Lock()
	proxySeq, committed := sp.commitPreparedStartLocked(reserved, prepared)
	sp.Mu.Unlock()
	if !committed {
		_ = stopDaemonInstance(reserved.instanceName, true)
		return nil
	}
	_ = proxySeq
	return nil
}

func (sp *InstanceProcess) Stop(force bool) {
	limit := cfg.GetHistoryLimit() * 1024
	sp.Mu.Lock()
	if sp.Deleting {
		sp.Mu.Unlock()
		return
	}
	if sp.State == processStateRestartWaiting {
		instanceName := sp.InstanceSnapshotLocked().Name
		sp.cancelRestartLocked()
		sp.cancelStopLocked()
		sp.enterStoppedStateLocked()
		msg := BuildNormalTerminalSystemMessage(msg.AutoRestartStopped)
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, limit)
		sp.shrinkTerminalHistoryLocked()
		NotifyInstanceStatusChanged(instanceName)
		sp.Mu.Unlock()
		if force {
			_ = stopDaemonInstance(instanceName, true)
		}
		return
	}
	if sp.State == processStateStoppingForRestart {
		sp.cancelRestartLocked()
		instanceName := sp.InstanceSnapshotLocked().Name
		stopCommand := sp.InstanceSnapshotLocked().StopCommand
		noTerminal := cfg.IsNoTerminal(sp.activeTerminalLocked())
		sp.beginStopLocked(processStateStopping)
		NotifyInstanceStatusChanged(instanceName)
		sp.Mu.Unlock()
		intentCode := RuntimeCodeManualStop
		if force {
			intentCode = RuntimeCodeManualKill
		}
		if err := markDaemonInstanceRuntimeCode(instanceName, intentCode); err != nil {
			log.Printf(msg.MarkInstanceStopIntentFailedLogFmt, instanceName, err)
		}
		if force {
			_ = stopDaemonInstance(instanceName, true)
		} else {
			sendDaemonStopRequest(instanceName, stopCommand, noTerminal, RuntimeCodeManualStop, msg.MarkInstanceManualStopIntentFailedLogFmt, msg.WriteInstanceStopCommandFailedLogFmt)
		}
		return
	}
	if sp.State == processStateStopping {
		if force {
			instanceName := sp.InstanceSnapshotLocked().Name
			sp.cancelStopLocked()
			sp.cancelStartLocked()
			sp.Mu.Unlock()
			_ = stopDaemonInstance(instanceName, true)
			return
		}
		if sp.Running {
			instanceName := sp.InstanceSnapshotLocked().Name
			stopCommand := sp.InstanceSnapshotLocked().StopCommand
			noTerminal := cfg.IsNoTerminal(sp.activeTerminalLocked())
			sp.Mu.Unlock()
			sendDaemonStopRequest(instanceName, stopCommand, noTerminal, RuntimeCodeManualStop, msg.MarkInstanceManualStopIntentFailedLogFmt, msg.WriteInstanceStopCommandFailedLogFmt)
			return
		}
		sp.Mu.Unlock()
		return
	}
	if !sp.Running {
		sp.cancelRestartLocked()
		sp.cancelStopLocked()
		sp.cancelStartLocked()
		sp.enterStoppedStateLocked()
		sp.Mu.Unlock()
		return
	}
	instanceName := sp.InstanceSnapshotLocked().Name
	stopCommand := sp.InstanceSnapshotLocked().StopCommand
	noTerminal := cfg.IsNoTerminal(sp.activeTerminalLocked())
	sp.beginStopLocked(processStateStopping)
	NotifyInstanceStatusChanged(instanceName)
	intentCode := RuntimeCodeManualStop
	if force {
		intentCode = RuntimeCodeManualKill
	}
	sp.Mu.Unlock()

	if err := markDaemonInstanceRuntimeCode(instanceName, intentCode); err != nil {
		log.Printf(msg.MarkInstanceStopIntentFailedLogFmt, instanceName, err)
	}
	if force {
		_ = stopDaemonInstance(instanceName, true)
		return
	}
	sendDaemonStopRequest(instanceName, stopCommand, noTerminal, RuntimeCodeManualStop, msg.MarkInstanceManualStopIntentFailedLogFmt, msg.WriteInstanceStopCommandFailedLogFmt)
}

func (sp *InstanceProcess) BeginDelete() (running bool, restarting bool, starting bool, deleting bool) {
	if sp == nil {
		return false, false, false, true
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	if sp.Running || sp.Restarting || sp.Starting || sp.Deleting {
		return sp.Running, sp.Restarting, sp.Starting, sp.Deleting
	}
	sp.Deleting = true
	return false, false, false, false
}

func (sp *InstanceProcess) CancelDelete() {
	if sp == nil {
		return
	}
	sp.Mu.Lock()
	sp.Deleting = false
	sp.Mu.Unlock()
}

func (sp *InstanceProcess) RetireDeletedInstance() {
	if sp == nil {
		return
	}
	sp.forceShutdown()
}

func (sp *InstanceProcess) forceShutdown() {
	if sp == nil {
		return
	}
	sp.Mu.Lock()
	sp.cancelRestartLocked()
	sp.cancelStopLocked()
	sp.cancelStartLocked()
	clients := sp.detachClientsLocked()
	instanceName := sp.InstanceSnapshotLocked().Name
	wasRunning := sp.Running || sp.Starting
	if wasRunning {
		sp.enterStoppingStateLocked()
	}
	sp.Mu.Unlock()
	if wasRunning {
		_ = stopDaemonInstance(instanceName, true)
	}
	sp.closeDetachedClients(clients)
}

func (sp *InstanceProcess) SendCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New(msg.CommandEmpty)
	}

	sp.Mu.Lock()
	if sp.Deleting {
		sp.Mu.Unlock()
		return errors.New(msg.InstanceBeingDeleted)
	}
	if !sp.Running {
		sp.Mu.Unlock()
		return errors.New(msg.InstanceNotRunning)
	}
	if cfg.IsNoTerminal(sp.activeTerminalLocked()) {
		sp.Mu.Unlock()
		return errors.New(msg.NoTerminalInputUnsupported)
	}
	instanceName := sp.InstanceSnapshotLocked().Name
	sp.Mu.Unlock()
	if err := writeDaemonInstanceStdin(instanceName, formatRuntimeCommand(command)); err != nil {
		return fmt.Errorf("%s: %w", msg.WriteCommandFailed, err)
	}
	return nil
}

func (sp *InstanceProcess) SendInput(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > maxTerminalInputBytes {
		return fmt.Errorf(msg.ReceivedMoreThanFmt, maxTerminalInputBytes)
	}

	sp.InputMu.Lock()
	defer sp.InputMu.Unlock()

	sp.Mu.Lock()
	if sp.Deleting {
		sp.Mu.Unlock()
		return errors.New(msg.InstanceBeingDeleted)
	}
	if !sp.Running {
		sp.Mu.Unlock()
		return errors.New(msg.InstanceNotRunning)
	}
	activeTerminal := sp.activeTerminalLocked()
	if cfg.IsNoTerminal(activeTerminal) {
		sp.Mu.Unlock()
		return errors.New(msg.NoTerminalInputUnsupported)
	}
	if cfg.IsTerminal(activeTerminal) {
		if echo := buildPlainTerminalInputEcho(data); len(echo) > 0 {
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, echo, cfg.GetHistoryLimit()*1024)
		}
	}
	instanceName := sp.InstanceSnapshotLocked().Name
	sp.Mu.Unlock()
	return writeDaemonInstanceInputFastPath(instanceName, data)
}

func (sp *InstanceProcess) ResizeTerminal(cols uint16, rows uint16) error {
	if sp == nil {
		return nil
	}
	if cols == 0 || rows == 0 {
		return fmt.Errorf(msg.InvalidTerminalSizeFmt, cols, rows)
	}
	if cols > maxTerminalCols || rows > maxTerminalRows {
		return fmt.Errorf(msg.TerminalSizeTooLargeFmt, maxTerminalCols, maxTerminalRows)
	}
	sp.Mu.Lock()
	sp.Cols = cols
	sp.Rows = rows
	if !sp.Running || cfg.IsNoTerminal(sp.activeTerminalLocked()) {
		sp.Mu.Unlock()
		return nil
	}
	instanceName := sp.InstanceSnapshotLocked().Name
	sp.Mu.Unlock()
	if err := resizeDaemonInstanceTerminal(instanceName, cols, rows); err != nil {
		return fmt.Errorf(msg.ResizeInstanceTerminalFailedFmt, err)
	}
	return nil
}
