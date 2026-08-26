package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"IpacPanel/daemon/ipc"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const daemonOutputBufferPoolMaxBytes = 512 * 1024

const (
	RuntimeCodeUnknown        = ipc.RuntimeCodeUnknown
	RuntimeCodeRunning        = ipc.RuntimeCodeRunning
	RuntimeCodeManualStop     = ipc.RuntimeCodeManualStop
	RuntimeCodeManualKill     = ipc.RuntimeCodeManualKill
	RuntimeCodeRestarting     = ipc.RuntimeCodeRestarting
	RuntimeCodeUnexpectedExit = ipc.RuntimeCodeUnexpectedExit

	ControllerShutdownPurposeRestart = ipc.ControllerShutdownPurposeRestart
	ControllerShutdownPurposeUpdate  = ipc.ControllerShutdownPurposeUpdate
)

type DaemonRuntimeState = ipc.InstanceRuntimeState

const (
	DaemonLifecycleStopped  = ipc.InstanceLifecycleStopped
	DaemonLifecycleRunning  = ipc.InstanceLifecycleRunning
	DaemonLifecycleStopping = ipc.InstanceLifecycleStopping
	DaemonLifecycleCleaning = ipc.InstanceLifecycleCleaning
)

type daemonIPCRequest = ipc.Request
type daemonIPCResponse = ipc.Response

type daemonIPCClient struct {
	closer    io.Closer
	reader    *bufio.Reader
	writer    *bufio.Writer
	writeMu   sync.Mutex
	nextID    atomic.Uint64
	pendingMu sync.Mutex
	pending   map[uint64]chan daemonIPCResponse
	done      chan struct{}
}

var daemonOutputBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, daemonOutputBufferPoolMaxBytes)
		return &buf
	},
}

func getDaemonOutputBuffer(size int) []byte {
	if size <= 0 {
		return nil
	}
	if size > daemonOutputBufferPoolMaxBytes {
		return make([]byte, size)
	}
	buf := *(daemonOutputBufferPool.Get().(*[]byte))
	if cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

func putDaemonOutputBuffer(buf []byte) {
	if cap(buf) != daemonOutputBufferPoolMaxBytes {
		return
	}
	buf = buf[:cap(buf)]
	daemonOutputBufferPool.Put(&buf)
}

var daemonClient *daemonIPCClient

func ConnectDaemonStdio() error {
	client := &daemonIPCClient{
		closer:  daemonStdioCloser{},
		reader:  bufio.NewReaderSize(os.Stdin, 65536),
		writer:  bufio.NewWriterSize(os.Stdout, 65536),
		pending: make(map[uint64]chan daemonIPCResponse),
		done:    make(chan struct{}),
	}
	daemonClient = client
	go client.readLoop()
	return nil
}

type daemonStdioCloser struct{}

func (daemonStdioCloser) Close() error { return nil }

func DisconnectDaemon() {
	client := daemonClient
	if client == nil {
		return
	}
	_ = client.closer.Close()
	daemonClient = nil
}

func DaemonDisconnected() <-chan struct{} {
	client := daemonClient
	if client == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return client.done
}

func (c *daemonIPCClient) readLoop() {
	defer c.closeWithPendingError()
	for {
		peek, err := c.reader.Peek(len(":o:"))
		if err == nil && ipc.IsInstanceOutputPrefix(peek) {
			instance, body, err := ipc.ReadInstanceOutputFrame(c.reader, getDaemonOutputBuffer, putDaemonOutputBuffer)
			if err != nil {
				return
			}
			handleDaemonInstanceOutputFrame(instance, body)
			continue
		}
		resp, err := ipc.ReadResponse(c.reader)
		if err != nil {
			return
		}
		switch resp.Type {
		case "instance_exited":
			HandleDaemonInstanceExited(resp.State)
		case "cleanup_message":
			HandleDaemonCleanupMessage(resp.Instance, resp.Placeholder, resp.Args)
		default:
			c.deliverResponse(resp)
		}
	}
}

func (c *daemonIPCClient) registerPending(id uint64) chan daemonIPCResponse {
	ch := make(chan daemonIPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	return ch
}

func (c *daemonIPCClient) unregisterPending(id uint64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *daemonIPCClient) deliverResponse(resp daemonIPCResponse) {
	if resp.ID == 0 {
		return
	}
	c.pendingMu.Lock()
	ch := c.pending[resp.ID]
	delete(c.pending, resp.ID)
	c.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	case <-c.done:
	}
}

func (c *daemonIPCClient) closeWithPendingError() {
	c.pendingMu.Lock()
	old := c.pending
	c.pending = make(map[uint64]chan daemonIPCResponse)
	c.pendingMu.Unlock()
	for _, ch := range old {
		close(ch)
	}
	close(c.done)
}

func handleDaemonInstanceOutputFrame(instanceName string, data []byte) {
	defer putDaemonOutputBuffer(data)
	if instanceName == "" || len(data) == 0 {
		return
	}
	HandleDaemonInstanceOutput(instanceName, data)
}

func daemonRequest(req daemonIPCRequest) (daemonIPCResponse, error) {
	client := daemonClient
	if client == nil {
		return daemonIPCResponse{}, errors.New(msg.DaemonIPCNotConnected)
	}
	req.ID = client.nextID.Add(1)
	respCh := client.registerPending(req.ID)
	defer client.unregisterPending(req.ID)

	req.BodyLen = len(req.Body)
	if err := ipc.ValidateBodyLen(req.BodyLen); err != nil {
		return daemonIPCResponse{}, err
	}
	data, err := ipc.EncodeFrame(req.Type, req)
	if err != nil {
		return daemonIPCResponse{}, fmt.Errorf(msg.EncodeDaemonIPCRequestFailedFmt, err)
	}
	client.writeMu.Lock()
	_, err = client.writer.Write(data)
	if err == nil && req.BodyLen > 0 {
		_, err = client.writer.Write(req.Body)
	}
	if err == nil {
		err = client.writer.Flush()
	}
	client.writeMu.Unlock()
	if err != nil {
		return daemonIPCResponse{}, fmt.Errorf(msg.WriteDaemonIPCRequestFailedFmt, err)
	}

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case resp, ok := <-respCh:
		if !ok {
			return daemonIPCResponse{}, errors.New(msg.DaemonIPCDisconnected)
		}
		if resp.Error != "" {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	case <-client.done:
		return daemonIPCResponse{}, errors.New(msg.DaemonIPCDisconnected)
	case <-timer.C:
		return daemonIPCResponse{}, errors.New(msg.DaemonIPCRequestTimeout)
	}
}

func DaemonHello() (int, error) {
	helloMsg := fmt.Sprintf("%s %d", "shanghai_crab", time.Now().UnixNano())
	resp, err := daemonRequest(daemonIPCRequest{Type: "hello", Msg: helloMsg})
	if err != nil {
		return 0, err
	}
	if resp.Msg != helloMsg {
		return 0, fmt.Errorf(msg.DaemonHelloMessageMismatchFmt, helloMsg, resp.Msg)
	}
	return resp.DaemonProtocol, nil
}

func ListDaemonRuntime() ([]DaemonRuntimeState, error) {
	resp, err := daemonRequest(daemonIPCRequest{Type: "list_runtime"})
	if err != nil {
		return nil, err
	}
	return append([]DaemonRuntimeState(nil), resp.Runtime...), nil
}

func SetDaemonDebug(debug bool) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "set_debug", Debug: debug})
	return err
}

// RestartController 请求守护进程重启管理进程 (设置页手动重启).
func RestartController() error {
	return restartControllerWithPurpose(ControllerShutdownPurposeRestart)
}

// NotifyControllerUpdateExit 在自替换更新完成、管理进程即将退出前调用,
// 守护进程收到后立即重启, 无需等待常规重启延时.
func NotifyControllerUpdateExit() error {
	return restartControllerWithPurpose(ipc.ControllerShutdownPurposeUpdate)
}

func restartControllerWithPurpose(purpose string) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "restart_controller", ControllerShutdownPurpose: purpose})
	return err
}

func NotifyControllerReady() error {
	_, err := daemonRequest(daemonIPCRequest{Type: "controller_ready"})
	return err
}

func RenameDaemonInstance(oldName string, newName string) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "rename_daemon_instance", Instance: oldName, NewName: newName})
	return err
}

func startDaemonInstance(insName string, commandArgv []string, cleanupCommandArgv []string, path string, terminal int, inputEnc string, outputEnc string, cols uint16, rows uint16) (*DaemonRuntimeState, error) {
	resp, err := daemonRequest(daemonIPCRequest{Type: "start_instance", Instance: insName, CommandArgv: commandArgv, CleanupCommandArgv: cleanupCommandArgv, Path: path, Terminal: terminal, InputEnc: inputEnc, OutputEnc: outputEnc, Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return resp.State, nil
}

func UpdateDaemonInstanceConfig(insName string, cleanupCommandArgv []string) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "update_instance_config", Instance: insName, CleanupCommandArgv: cleanupCommandArgv})
	return err
}

func stopDaemonInstanceWithCode(insName string, force bool, runtimeCode int) error {
	reqType := "stop_instance"
	if force {
		reqType = "kill_instance"
	}
	_, err := daemonRequest(daemonIPCRequest{Type: reqType, Instance: insName, Force: force, RuntimeCode: runtimeCode})
	return err
}

func markDaemonInstanceRuntimeCode(insName string, runtimeCode int) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "mark_runtime_code", Instance: insName, RuntimeCode: runtimeCode})
	return err
}

func markDaemonInstanceStopping(insName string, runtimeCode int) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "mark_stopping", Instance: insName, RuntimeCode: runtimeCode})
	return err
}

func stopDaemonInstance(insName string, force bool) error {
	runtimeCode := RuntimeCodeManualStop
	if force {
		runtimeCode = RuntimeCodeManualKill
	}
	return stopDaemonInstanceWithCode(insName, force, runtimeCode)
}

func writeDaemonInstanceStdin(insName string, data []byte) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "write_stdin", Instance: insName, Body: data})
	return err
}

func writeDaemonInstanceInputFastPath(insName string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if insName == "" {
		return errors.New(msg.DaemonInstanceInputInstanceEmpty)
	}
	if strings.Contains(insName, ":") {
		return errors.New(msg.DaemonInstanceInputInstanceContainsSeparator)
	}
	client := daemonClient
	if client == nil {
		return errors.New(msg.DaemonIPCNotConnected)
	}

	header, err := ipc.BuildInputHeader(insName, len(data))
	if err != nil {
		return err
	}

	client.writeMu.Lock()
	_, err = client.writer.Write(header)
	if err == nil {
		_, err = client.writer.Write(data)
	}
	if err == nil {
		err = client.writer.Flush()
	}
	client.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf(msg.WriteDaemonIPCRequestFailedFmt, err)
	}
	return nil
}

func resizeDaemonInstanceTerminal(insName string, cols uint16, rows uint16) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "resize_terminal", Instance: insName, Cols: cols, Rows: rows})
	return err
}

func HandleDaemonInstanceOutput(instanceName string, data []byte) {
	sp, ok := GetByRuntimeAlias(instanceName)
	if !ok || sp == nil || len(data) == 0 {
		return
	}
	limit := cfg.GetHistoryLimit() * 1024

	sp.Mu.Lock()
	defer sp.Mu.Unlock()

	if cfg.IsNoTerminal(sp.ActiveTerminalLocked()) {
		return
	}

	isPTY := cfg.IsPTYTerminal(sp.ActiveTerminalLocked())
	var historyData, liveData []byte
	liveData = data

	if isPTY {
		historyData = sp.filterPTYHistoryOutputLocked(data)
		if sp.TerminalStartupProtecting {
			// 启动保护期: 实时和历史数据均需过滤破坏性序列
			if len(historyData) > 0 {
				historyData, _, _, _ = sanitizeTerminalStartupOutput(historyData)
			}
			liveData = sp.sanitizeTerminalStartupOutputLocked(data)
			if len(liveData) == 0 {
				return
			}
		} else {
			// 常态 PTY: 过滤 \x1b[3J 以保留滚动条/历史
			historyData = bytes.ReplaceAll(historyData, []byte("\x1b[3J"), nil)
			liveData = bytes.ReplaceAll(liveData, []byte("\x1b[3J"), nil)
		}
	} else {
		// 普通终端模式: 直接透传
		historyData = data
	}

	if len(historyData) > 0 {
		sp.appendHistoryLocked(historyData, limit)
	}

	if len(liveData) > 0 {
		if isPTY {
			sp.writeTerminalLiveClientsLocked(liveData)
		} else {
			sp.writeClientsLocked(websocket.BinaryMessage, liveData)
		}
	}
}

func HandleDaemonCleanupMessage(instanceName string, placeholder string, args []string) {
	sp, ok := GetByRuntimeAlias(instanceName)
	if !ok || sp == nil || placeholder == "" {
		return
	}
	message, warning, ok := renderDaemonCleanupMessage(placeholder, args)
	if !ok || message == "" {
		log.Printf(msg.InvalidDaemonCleanupMessageLogFmt, instanceName, placeholder, args)
		return
	}
	data := BuildNormalTerminalSystemMessage(message)
	if warning {
		data = BuildWarningTerminalSystemMessage(message)
	}
	limit := cfg.GetHistoryLimit() * 1024
	sp.Mu.Lock()
	sp.appendHistoryLocked(data, limit)
	sp.writeClientsLocked(websocket.BinaryMessage, data)
	sp.Mu.Unlock()
}

func renderDaemonCleanupMessage(placeholder string, args []string) (string, bool, bool) {
	normalizedArgs := normalizeDaemonMessageArgs(args)
	requireArgs := func(count int) bool {
		if len(normalizedArgs) != count {
			return false
		}
		for _, arg := range normalizedArgs {
			if arg == "" {
				return false
			}
		}
		return true
	}
	switch placeholder {
	case "cleanup_command.build_failed":
		if !requireArgs(1) {
			return "", false, false
		}
		return fmt.Sprintf(msg.CleanupCommandBuildFailedFmt, normalizedArgs[0]), true, true
	case "cleanup_command.stdout_failed":
		if !requireArgs(1) {
			return "", false, false
		}
		return fmt.Sprintf(msg.CleanupCommandStdoutFailedFmt, normalizedArgs[0]), true, true
	case "cleanup_command.stderr_failed":
		if !requireArgs(1) {
			return "", false, false
		}
		return fmt.Sprintf(msg.CleanupCommandStderrFailedFmt, normalizedArgs[0]), true, true
	case "cleanup_command.start_failed":
		if !requireArgs(1) {
			return "", false, false
		}
		return fmt.Sprintf(msg.CleanupCommandStartFailedFmt, normalizedArgs[0]), true, true
	case "cleanup_command.started":
		if !requireArgs(0) {
			return "", false, false
		}
		return msg.CleanupCommandStarted, false, true
	case "cleanup_command.exited":
		if !requireArgs(1) {
			return "", false, false
		}
		return fmt.Sprintf(msg.CleanupCommandExitedFmt, normalizedArgs[0]), true, true
	case "cleanup_command.completed":
		if !requireArgs(0) {
			return "", false, false
		}
		return msg.CleanupCommandCompleted, false, true
	default:
		return "", false, false
	}
}

func normalizeDaemonMessageArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	normalized := make([]string, len(args))
	for i, arg := range args {
		arg = strings.ReplaceAll(arg, "\r", " ")
		arg = strings.ReplaceAll(arg, "\n", " ")
		normalized[i] = strings.TrimSpace(arg)
	}
	return normalized
}

func HandleDaemonInstanceExited(state *DaemonRuntimeState) {
	runtimeAlias := daemonRuntimeRouteAlias(state)
	sp, ok := GetByRuntimeAlias(runtimeAlias)
	if !ok || sp == nil {
		return
	}
	if sp.handleDaemonProcessExit(state) {
		go sp.scheduleAutoRestart()
	}
}

func daemonRuntimeRouteAlias(state *DaemonRuntimeState) string {
	if state == nil {
		return ""
	}
	if state.RuntimeAlias != "" {
		return state.RuntimeAlias
	}
	return state.InstanceName
}
