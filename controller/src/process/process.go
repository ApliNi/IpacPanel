package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsSendQueueSize         = 64
	maxTerminalCols         = 4000
	maxTerminalRows         = 2500
	maxTerminalInputBytes   = 16 * 1024
	wsWriteTimeout          = 5 * time.Second
	wsPingInterval          = 25 * time.Second
	wsTerminalFrameMaxBytes = 256 * 1024
	terminalSystemBlue      = "\x1b[34m"
	terminalSystemYellow    = "\x1b[33m"
)

func buildTerminalMessage(colorCode string, text string) []byte {
	timestamp := time.Now().Format(cfg.DisplayTimeLayout)
	return []byte(fmt.Sprintf("\r\n\r\n%s\x1b[1m[%s] [IpacPanel] %s\x1b[0m\r\n\r\n", colorCode, timestamp, text))
}

func BuildNormalTerminalSystemMessage(text string) []byte {
	return buildTerminalMessage(terminalSystemBlue, text)
}

func BuildWarningTerminalSystemMessage(text string) []byte {
	return buildTerminalMessage(terminalSystemYellow, text)
}

func appendPlainTerminalInputRune(dst []byte, r rune) []byte {
	switch {
	case r == '\t':
		return append(dst, '\t')
	case r == 0x1b:
		return append(dst, '^', '[')
	case r == 0x7f:
		return append(dst, '^', '?')
	case r >= 0 && r < 0x20:
		return append(dst, '^', byte('@'+r))
	case r >= 0x80 && r <= 0x9f:
		return append(dst, fmt.Sprintf("\\x%02X", r)...)
	default:
		return append(dst, string(r)...)
	}
}

func buildPlainTerminalInputEcho(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}

	echo := make([]byte, 0, len(data)+len(lines)*4)
	for _, line := range lines {
		echo = append(echo, '>', ' ')
		for _, r := range line {
			echo = appendPlainTerminalInputRune(echo, r)
		}
		echo = append(echo, '\r', '\n')
	}
	return echo
}

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
	Mu                           sync.Mutex
	Clients                      map[*WSClient]bool
	History                      TerminalHistory
	ProxySeq                     uint64
	StopSeq                      uint64
	StartSeq                     uint64
	TerminalStartupProtecting    bool
	TerminalStartupPendingEscape []byte
	PTYAlternateScreenActive     bool
	PTYAlternateScreenPending    []byte
	InputMu                      sync.Mutex
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
	hadStarted               bool
	startSeq                 uint64
	resetRestarting          bool
	instanceName             string
	cancelCh                 chan struct{}
}

type preparedStart struct {
	state     *DaemonRuntimeState
	startTime time.Time
}

type WSClient struct {
	Conn *websocket.Conn
	User string

	Send           chan wsOutMessage
	Done           chan struct{}
	WriteMu        sync.Mutex
	TerminalWake   chan struct{}
	StartOnce      sync.Once
	CloseOnce      sync.Once
	Instance       *InstanceProcess
	TerminalCursor uint64
	TerminalReady  atomic.Bool
}

type wsOutMessage struct {
	Type                     int
	Data                     []byte
	SkipTerminalFlush        bool
	EnableTerminalAfterWrite bool
}

type TerminalInitialMessage struct {
	Reset   []byte
	History []byte
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
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.activeTerminalLocked()
}

func (sp *InstanceProcess) StatusSnapshot() InstanceStatus {
	if sp == nil {
		return InstanceStatus{}
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
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
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.InstanceSnapshotLocked()
}

func (sp *InstanceProcess) IsDeleting() bool {
	if sp == nil {
		return false
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.Deleting
}

func (sp *InstanceProcess) IsRunning() bool {
	if sp == nil {
		return false
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.Running
}

func (sp *InstanceProcess) IsUpdating() bool {
	if sp == nil {
		return false
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.Updating
}

func (sp *InstanceProcess) IsActive() bool {
	if sp == nil {
		return false
	}
	sp.Mu.Lock()
	defer sp.Mu.Unlock()
	return sp.Running || sp.Restarting || sp.Starting
}

func (client *WSClient) startWriter() {
	if client == nil || client.Conn == nil {
		return
	}
	client.StartOnce.Do(func() {
		if client.Send == nil {
			client.Send = make(chan wsOutMessage, wsSendQueueSize)
		}
		if client.Done == nil {
			client.Done = make(chan struct{})
		}
		if client.TerminalWake == nil {
			client.TerminalWake = make(chan struct{}, 1)
		}
		go func() {
			pingTicker := time.NewTicker(wsPingInterval)
			defer pingTicker.Stop()
			for {
				select {
				case msg, ok := <-client.Send:
					if !ok {
						return
					}
					if err := client.writeQueuedMessage(msg); err != nil {
						_ = client.Close()
						return
					}
					continue
				default:
				}
				select {
				case <-client.Done:
					return
				case <-client.TerminalWake:
					if err := client.flushTerminalHistory(); err != nil {
						_ = client.Close()
						return
					}
				case <-pingTicker.C:
					deadline := time.Now().Add(wsWriteTimeout)
					_ = client.Conn.SetWriteDeadline(deadline)
					if err := client.Conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
						_ = client.Close()
						return
					}
				case msg, ok := <-client.Send:
					if !ok {
						return
					}
					if err := client.writeQueuedMessage(msg); err != nil {
						_ = client.Close()
						return
					}
				}
			}
		}()
	})
}

func (client *WSClient) writeQueuedMessage(msg wsOutMessage) error {
	if !msg.SkipTerminalFlush {
		if err := client.flushTerminalHistory(); err != nil {
			return err
		}
	}
	if err := client.writeMessage(msg.Type, msg.Data); err != nil {
		return err
	}
	if msg.EnableTerminalAfterWrite {
		client.EnableTerminalHistory()
	}
	return nil
}

func (client *WSClient) writeMessage(messageType int, data []byte) error {
	if client == nil || client.Conn == nil {
		return nil
	}
	client.WriteMu.Lock()
	defer client.WriteMu.Unlock()
	_ = client.Conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return client.Conn.WriteMessage(messageType, data)
}

func (client *WSClient) SendControlError(message string) error {
	if strings.TrimSpace(message) == "" {
		message = msg.WSControlFrameInvalid
	}
	data := []byte(fmt.Sprintf(`{"type":"error","message":%q}`, message))
	return client.writeMessage(websocket.TextMessage, data)
}

func (client *WSClient) SendInitialTerminal(initial TerminalInitialMessage) error {
	if client == nil {
		return nil
	}
	if len(initial.Reset) > 0 {
		if err := client.writeMessage(websocket.BinaryMessage, initial.Reset); err != nil {
			return err
		}
	}
	if len(initial.History) > 0 {
		if err := client.writeMessage(websocket.BinaryMessage, initial.History); err != nil {
			return err
		}
	}
	return nil
}

func (client *WSClient) flushTerminalHistory() error {
	if client == nil || client.Conn == nil {
		return nil
	}
	sp := client.Instance
	if sp == nil {
		return nil
	}
	if !client.TerminalReady.Load() {
		return nil
	}
	sp.Mu.Lock()
	payload, nextSeq, dropped := sp.History.ReadFrom(client.TerminalCursor, wsTerminalFrameMaxBytes)
	client.TerminalCursor = nextSeq
	more := client.TerminalCursor < sp.History.EndSeq()
	sp.Mu.Unlock()

	if dropped {
		notice := BuildWarningTerminalSystemMessage(msg.TerminalOutputDroppedWarning)
		payload = append(notice, payload...)
	}

	if len(payload) == 0 {
		return nil
	}
	_ = client.Conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if err := client.Conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		return err
	}
	if more {
		client.wakeTerminalWriter()
	}
	return nil
}

func (client *WSClient) StartWriter() {
	client.startWriter()
}

func (client *WSClient) Enqueue(messageType int, data []byte) bool {
	if client == nil {
		return false
	}
	if messageType == websocket.BinaryMessage {
		if !client.TerminalReady.Load() {
			return true
		}
	}
	client.startWriter()
	if client.Send == nil {
		return false
	}
	select {
	case <-client.Done:
		return false
	case client.Send <- wsOutMessage{Type: messageType, Data: data}:
		return true
	default:
		// Slow client. Drop message and close.
		_ = client.Close()
		return false
	}
}

func (client *WSClient) EnqueueTerminalControl(data []byte) bool {
	if client == nil {
		return false
	}
	client.startWriter()
	if client.Send == nil {
		return false
	}
	select {
	case <-client.Done:
		return false
	case client.Send <- wsOutMessage{Type: websocket.BinaryMessage, Data: data, SkipTerminalFlush: true, EnableTerminalAfterWrite: true}:
		return true
	default:
		_ = client.Close()
		return false
	}
}

func (client *WSClient) EnqueueTerminalLive(data []byte) bool {
	if client == nil {
		return false
	}
	client.startWriter()
	if client.Send == nil {
		return false
	}
	payload := append([]byte(nil), data...)
	select {
	case <-client.Done:
		return false
	case client.Send <- wsOutMessage{Type: websocket.BinaryMessage, Data: payload, SkipTerminalFlush: true}:
		return true
	default:
		_ = client.Close()
		return false
	}
}

func (client *WSClient) EnqueueTerminal() bool {
	if client == nil {
		return false
	}
	if !client.TerminalReady.Load() {
		return true
	}
	client.startWriter()
	if client.Done == nil || client.TerminalWake == nil {
		return false
	}
	client.wakeTerminalWriter()
	return true
}

func (client *WSClient) wakeTerminalWriter() {
	if client == nil || client.TerminalWake == nil {
		return
	}
	select {
	case <-client.Done:
		return
	case client.TerminalWake <- struct{}{}:
	default:
	}
}

func (client *WSClient) EnableTerminalHistory() {
	if client == nil {
		return
	}
	client.startWriter()
	client.TerminalReady.Store(true)
	client.wakeTerminalWriter()
}

func (client *WSClient) Close() error {
	if client == nil {
		return nil
	}
	var err error
	client.CloseOnce.Do(func() {
		if client.Done != nil {
			select {
			case <-client.Done:
			default:
				close(client.Done)
			}
		}
		// NOTE: Do not close Send here.
		// Enqueue may be called concurrently and sending on a closed channel panics.
		if client.Conn != nil {
			err = client.Conn.Close()
		}
	})
	return err
}

func (sp *InstanceProcess) writeClientsLocked(messageType int, data []byte) {
	if messageType == websocket.BinaryMessage {
		for client := range sp.Clients {
			if !client.EnqueueTerminal() {
				delete(sp.Clients, client)
			}
		}
		return
	}
	for client := range sp.Clients {
		if !client.Enqueue(messageType, data) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) writeTerminalLiveClientsLocked(data []byte) {
	if len(data) == 0 {
		return
	}
	endSeq := sp.History.EndSeq()
	for client := range sp.Clients {
		if !client.TerminalReady.Load() {
			continue
		}
		client.TerminalCursor = endSeq
		if !client.EnqueueTerminalLive(data) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) sendClientsLocked(messageType int, data []byte) {
	for client := range sp.Clients {
		if !client.Enqueue(messageType, data) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) resetTerminalClientsLocked() {
	reset := []byte("\x1b[3J")
	for client := range sp.Clients {
		client.TerminalReady.Store(false)
		if !client.EnqueueTerminalControl(reset) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) detachClientsLocked() []*WSClient {
	clients := make([]*WSClient, 0, len(sp.Clients))
	for client := range sp.Clients {
		clients = append(clients, client)
		delete(sp.Clients, client)
	}
	return clients
}

func (sp *InstanceProcess) DetachClientsLocked() []*WSClient {
	return sp.detachClientsLocked()
}

func (sp *InstanceProcess) attachClientsLocked(clients []*WSClient) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		sp.Clients[client] = true
	}
}

func (sp *InstanceProcess) AttachClientsLocked(clients []*WSClient) {
	sp.attachClientsLocked(clients)
}

func (sp *InstanceProcess) closeDetachedClients(clients []*WSClient) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		_ = client.Close()
	}
}

func (sp *InstanceProcess) CloseDetachedClients(clients []*WSClient) {
	sp.closeDetachedClients(clients)
}

func (sp *InstanceProcess) DetachAndCloseAllClients() {
	if sp == nil {
		return
	}
	sp.Mu.Lock()
	clients := sp.detachClientsLocked()
	sp.Mu.Unlock()
	sp.closeDetachedClients(clients)
}

func (sp *InstanceProcess) disconnectAllClients() {
	sp.DetachAndCloseAllClients()
}

func (sp *InstanceProcess) appendAndBroadcastLocked(messageType int, data []byte, limit int) {
	if cfg.IsNoTerminal(sp.activeTerminalLocked()) {
		return
	}
	if messageType == websocket.BinaryMessage {
		sp.appendHistoryLocked(data, limit)
	}
	sp.writeClientsLocked(messageType, data)
}

func (sp *InstanceProcess) AppendAndBroadcastLocked(messageType int, data []byte, limit int) {
	sp.appendAndBroadcastLocked(messageType, data, limit)
}

func (sp *InstanceProcess) AppendAndBroadcastNormalSystemMessageLocked(text string, limit int) {
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, BuildNormalTerminalSystemMessage(text), limit)
}

func (sp *InstanceProcess) AppendAndBroadcastWarningSystemMessageLocked(text string, limit int) {
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, BuildWarningTerminalSystemMessage(text), limit)
}

func (sp *InstanceProcess) AddClientWithHistoryLocked(client *WSClient) []byte {
	if sp == nil || client == nil {
		return nil
	}
	historyCopy, cursor := sp.History.Snapshot()
	if sp.shouldSkipHistoryForTerminalInitialLocked() {
		historyCopy = nil
	}
	client.Instance = sp
	client.TerminalCursor = cursor
	client.TerminalReady.Store(false)
	sp.Clients[client] = true
	return historyCopy
}

func (sp *InstanceProcess) AddClientTerminalInitialLocked(client *WSClient, reset []byte) TerminalInitialMessage {
	if sp == nil || client == nil {
		return TerminalInitialMessage{Reset: append([]byte(nil), reset...)}
	}
	historyCopy, cursor := sp.History.Snapshot()
	if sp.shouldSkipHistoryForTerminalInitialLocked() {
		historyCopy = nil
	}
	client.Instance = sp
	client.TerminalCursor = cursor
	client.TerminalReady.Store(false)
	return TerminalInitialMessage{Reset: append([]byte(nil), reset...), History: historyCopy}
}

func (sp *InstanceProcess) AttachClientLocked(client *WSClient) {
	if sp == nil || client == nil {
		return
	}
	client.Instance = sp
	sp.Clients[client] = true
}

func (sp *InstanceProcess) shouldSkipHistoryForTerminalInitialLocked() bool {
	return cfg.IsPTYTerminal(sp.activeTerminalLocked()) && sp.PTYAlternateScreenActive
}

func (sp *InstanceProcess) resetPTYAlternateScreenStateLocked() {
	sp.PTYAlternateScreenActive = false
	sp.PTYAlternateScreenPending = nil
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
		return nil, nil
	}
	ins := sp.InstanceSnapshotLocked()
	reserved := &startReservation{
		ins:                      ins,
		historyLimit:             historyLimit,
		instanceUpdateStagingDir: instanceUpdateStagingDir,
		cols:                     sp.Cols,
		rows:                     sp.Rows,
		hadStarted:               !sp.StartTime.IsZero(),
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
	restartCount := sp.RestartCount
	if prepared.state != nil {
		restartCount = prepared.state.RestartCount
	} else if reserved.hadStarted {
		restartCount++
	}
	sp.ActiveTerminalMode = cfg.NormalizeTerminalMode(reserved.ins.Terminal)
	sp.resetPTYAlternateScreenStateLocked()
	proxySeq := sp.setProcessStartedLocked(prepared.startTime, restartCount)
	if cfg.IsPTYTerminal(reserved.ins.Terminal) {
		sp.TerminalStartupProtecting = true
		sp.TerminalStartupPendingEscape = nil
	}
	NotifyInstanceStatusChanged(reserved.ins.Name)
	return proxySeq, true
}

func (sp *InstanceProcess) appendHistoryLocked(data []byte, limit int) {
	if len(data) > limit {
		limit = len(data)
	}
	sp.History.Append(data, limit)
}

func (sp *InstanceProcess) resetHistoryLocked(data []byte, limit int) {
	if len(data) > limit {
		limit = len(data)
	}
	sp.History.Reset(data, limit)
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
		msg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
		sp.enterStoppedStateLocked()
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, historyLimit)
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
			msg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			sp.enterStoppedStateLocked()
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, historyLimit)
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
	if sp.State == processStateStopped {
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
		if sp.RestartCancel != cancelCh || sp.State != processStateRestartWaiting {
			sp.Mu.Unlock()
			return
		}
		sp.RestartCancel = nil
		sp.Mu.Unlock()

		if err := sp.Start(); err != nil {
			limit := cfg.GetHistoryLimit() * 1024
			sp.Mu.Lock()
			terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.AutoRestartFailedFmt, err.Error()))
			sp.enterStoppedStateLocked()
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
			NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
			sp.Mu.Unlock()
		}
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
	return sp.requestRestartWithKillStop(mode, false)
}

func (sp *InstanceProcess) requestRestartWithKillStop(mode restartRequestMode, useKillStop bool) RestartRequestResult {
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
	return sp.requestRestartWithKillStop(restartRequestModeDefault, useKillStop)
}

func (sp *InstanceProcess) RequestStrictRestart() RestartRequestResult {
	return sp.requestRestart(restartRequestModeStrict)
}

func (sp *InstanceProcess) RequestStrictRestartWithKillStop(useKillStop bool) RestartRequestResult {
	return sp.requestRestartWithKillStop(restartRequestModeStrict, useKillStop)
}

func (sp *InstanceProcess) Start() error {
	historyLimit := cfg.GetHistoryLimit() * 1024
	instanceUpdateStagingDir := cfg.GetInstanceUpdateStagingDir()
	sp.Mu.Lock()
	reserved, err := sp.reserveStartLocked(historyLimit, instanceUpdateStagingDir)
	sp.Mu.Unlock()
	if err != nil {
		return err
	}
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
		if !sp.Running && sp.Starting {
			sp.cancelStopLocked()
			sp.cancelStartLocked()
			sp.enterStoppedStateLocked()
			NotifyInstanceStatusChanged(sp.InstanceSnapshotLocked().Name)
		}
		sp.Mu.Unlock()
		return
	}
	if sp.Starting {
		instanceName := sp.InstanceSnapshotLocked().Name
		sp.cancelRestartLocked()
		sp.cancelStopLocked()
		sp.cancelStartLocked()
		sp.enterStoppedStateLocked()
		msg := BuildNormalTerminalSystemMessage(msg.StartingInstanceCanceled)
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, limit)
		NotifyInstanceStatusChanged(instanceName)
		sp.Mu.Unlock()
		if force {
			_ = stopDaemonInstance(instanceName, true)
		}
		return
	}
	if !sp.Running {
		sp.Mu.Unlock()
		return
	}
	instanceName := sp.InstanceSnapshotLocked().Name
	sp.beginStopLocked(processStateStopping)
	stopCommand := sp.InstanceSnapshotLocked().StopCommand
	noTerminal := cfg.IsNoTerminal(sp.activeTerminalLocked())
	NotifyInstanceStatusChanged(instanceName)
	if force {
		sp.Mu.Unlock()
		_ = stopDaemonInstance(instanceName, true)
		return
	}
	sp.Mu.Unlock()
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
