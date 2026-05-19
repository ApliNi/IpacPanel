package process

import (
	cfg "IpacPanel/controller/src/config"
	"bufio"
	"bytes"
	"encoding/json"
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

const maxDaemonIPCHeaderSize = 64 * 1024
const maxDaemonIPCBodySize = 16 * 1024 * 1024
const daemonIPCFrameSeparator = ": "
const daemonIPCFramePrefix byte = ':'
const daemonHelloMsgPrefix = "shanghai_crab"

const daemonIPCFrameTypeInstanceOutput = "o"
const daemonIPCFrameTypeCleanupMessage = "cleanup_message"
const daemonIPCFrameInstanceOutputPrefix = ":o:"

const daemonOutputMaxBatchBytes = 512 * 1024

const (
	RuntimeCodeUnknown        = 0
	RuntimeCodeRunning        = 1
	RuntimeCodeManualStop     = 10
	RuntimeCodeManualKill     = 11
	RuntimeCodeRestarting     = 12
	RuntimeCodeUnexpectedExit = 20
)

type DaemonRuntimeState struct {
	InstanceName string    `json:"instance_name"`
	RuntimeAlias string    `json:"runtime_alias,omitempty"`
	Lifecycle    string    `json:"lifecycle"`
	RuntimeCode  int       `json:"runtime_code"`
	PID          int       `json:"pid,omitempty"`
	StartTime    time.Time `json:"start_time,omitempty"`
	ExitTime     time.Time `json:"exit_time,omitempty"`
	RestartCount int       `json:"restart_count"`
	Terminal     int       `json:"terminal,omitempty"`
}

const (
	DaemonLifecycleStopped  = "stopped"
	DaemonLifecycleRunning  = "running"
	DaemonLifecycleStopping = "stopping"
	DaemonLifecycleCleaning = "cleaning"
)

type daemonIPCRequest struct {
	Type           string `json:"-"`
	ID             uint64 `json:"id,omitempty"`
	Msg            string `json:"msg,omitempty"`
	Debug          bool   `json:"debug,omitempty"`
	Instance       string `json:"instance,omitempty"`
	NewName        string `json:"new_name,omitempty"`
	Command        string `json:"command,omitempty"`
	CleanupCommand string `json:"cleanup_command,omitempty"`
	Path           string `json:"path,omitempty"`
	Terminal       int    `json:"terminal,omitempty"`
	InputEnc       string `json:"input_encoding,omitempty"`
	OutputEnc      string `json:"output_encoding,omitempty"`
	RuntimeCode    int    `json:"runtime_code,omitempty"`
	Force          bool   `json:"force,omitempty"`
	Cols           uint16 `json:"cols,omitempty"`
	Rows           uint16 `json:"rows,omitempty"`
	BodyLen        int    `json:"body_len,omitempty"`
	Body           []byte `json:"-"`
}

type daemonIPCResponse struct {
	Type           string               `json:"-"`
	ID             uint64               `json:"id,omitempty"`
	Msg            string               `json:"msg,omitempty"`
	Placeholder    string               `json:"placeholder,omitempty"`
	Args           []string             `json:"args,omitempty"`
	Instance       string               `json:"instance,omitempty"`
	BodyLen        int                  `json:"body_len,omitempty"`
	Body           []byte               `json:"-"`
	Error          string               `json:"error,omitempty"`
	DaemonProtocol int                  `json:"daemon_protocol,omitempty"`
	Runtime        []DaemonRuntimeState `json:"runtime,omitempty"`
	State          *DaemonRuntimeState  `json:"state,omitempty"`
}

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

const daemonOutputBufferPoolMaxBytes = daemonOutputMaxBatchBytes

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

func decodeDaemonIPCFrame(line []byte, resp *daemonIPCResponse) error {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 || line[0] != daemonIPCFramePrefix {
		return fmt.Errorf("invalid daemon IPC frame prefix")
	}
	line = line[1:]
	sep := bytes.Index(line, []byte(daemonIPCFrameSeparator))
	if sep <= 0 {
		return fmt.Errorf("invalid daemon IPC frame header")
	}
	frameType := string(line[:sep])
	if err := json.Unmarshal(line[sep+len(daemonIPCFrameSeparator):], resp); err != nil {
		return err
	}
	resp.Type = frameType
	return nil
}

func encodeDaemonIPCFrame(frameType string, payload interface{}) ([]byte, error) {
	if frameType == "" {
		return nil, fmt.Errorf("daemon IPC frame type is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, 1+len(frameType)+len(daemonIPCFrameSeparator)+len(data)+1)
	frame = append(frame, daemonIPCFramePrefix)
	frame = append(frame, frameType...)
	frame = append(frame, daemonIPCFrameSeparator...)
	frame = append(frame, data...)
	frame = append(frame, '\n')
	return frame, nil
}

func readDaemonIPCHeaderLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxDaemonIPCHeaderSize {
			return nil, fmt.Errorf("daemon IPC header too large: %d bytes", len(line))
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
}

func validateDaemonIPCBodyLen(bodyLen int) error {
	if bodyLen < 0 {
		return fmt.Errorf("daemon IPC body length is negative: %d", bodyLen)
	}
	if bodyLen > maxDaemonIPCBodySize {
		return fmt.Errorf("daemon IPC body too large: %d bytes", bodyLen)
	}
	return nil
}

func parseDaemonIPCPositiveInt(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty integer")
	}
	value := 0
	for _, b := range data {
		if b < '0' || b > '9' {
			return 0, fmt.Errorf("invalid digit %q", b)
		}
		digit := int(b - '0')
		if value > (maxDaemonIPCBodySize-digit)/10 {
			return 0, fmt.Errorf("integer too large")
		}
		value = value*10 + digit
	}
	return value, nil
}

func readDaemonIPCUntil(reader *bufio.Reader, delim byte, fieldName string) ([]byte, error) {
	part, err := reader.ReadSlice(delim)
	if err != nil {
		return nil, fmt.Errorf("read daemon IPC %s: %w", fieldName, err)
	}
	if len(part) > maxDaemonIPCHeaderSize {
		return nil, fmt.Errorf("daemon IPC %s too large: %d bytes", fieldName, len(part))
	}
	return part[:len(part)-1], nil
}

func readDaemonInstanceOutputFrame(reader *bufio.Reader) (string, []byte, error) {
	if _, err := reader.Discard(len(daemonIPCFrameInstanceOutputPrefix)); err != nil {
		return "", nil, fmt.Errorf("read daemon instance output prefix: %w", err)
	}
	instance, err := readDaemonIPCUntil(reader, ':', "instance output instance")
	if err != nil {
		return "", nil, err
	}
	if len(instance) == 0 {
		return "", nil, fmt.Errorf("daemon instance output instance is empty")
	}
	bodyLenText, err := readDaemonIPCUntil(reader, ':', "instance output body length")
	if err != nil {
		return "", nil, err
	}
	bodyLen, err := parseDaemonIPCPositiveInt(bodyLenText)
	if err != nil {
		return "", nil, fmt.Errorf("invalid daemon instance output body length: %w", err)
	}
	if err := validateDaemonIPCBodyLen(bodyLen); err != nil {
		return "", nil, err
	}
	space, err := reader.ReadByte()
	if err != nil {
		return "", nil, fmt.Errorf("read daemon instance output body separator: %w", err)
	}
	if space != ' ' {
		return "", nil, fmt.Errorf("invalid daemon instance output body separator")
	}
	body := getDaemonOutputBuffer(bodyLen)
	if bodyLen > 0 {
		if _, err := io.ReadFull(reader, body); err != nil {
			putDaemonOutputBuffer(body)
			return "", nil, fmt.Errorf("read daemon instance output body: %w", err)
		}
	}
	return string(instance), body, nil
}

func isDaemonInstanceOutputPrefix(peek []byte) bool {
	return len(peek) == 3 && peek[0] == ':' && peek[1] == 'o' && peek[2] == ':'
}

func readDaemonIPCResponse(reader *bufio.Reader) (daemonIPCResponse, error) {
	line, err := readDaemonIPCHeaderLine(reader)
	if err != nil {
		return daemonIPCResponse{}, err
	}
	var resp daemonIPCResponse
	if err := decodeDaemonIPCFrame(line, &resp); err != nil {
		return daemonIPCResponse{}, err
	}
	if err := validateDaemonIPCBodyLen(resp.BodyLen); err != nil {
		return daemonIPCResponse{}, err
	}
	if resp.BodyLen > 0 {
		resp.Body = make([]byte, resp.BodyLen)
		if _, err := io.ReadFull(reader, resp.Body); err != nil {
			return daemonIPCResponse{}, err
		}
	}
	return resp, nil
}

func (c *daemonIPCClient) readLoop() {
	defer c.closeWithPendingError()
	for {
		peek, err := c.reader.Peek(len(daemonIPCFrameInstanceOutputPrefix))
		if err == nil && isDaemonInstanceOutputPrefix(peek) {
			instance, body, err := readDaemonInstanceOutputFrame(c.reader)
			if err != nil {
				return
			}
			handleDaemonInstanceOutputFrame(instance, body)
			continue
		}
		resp, err := readDaemonIPCResponse(c.reader)
		if err != nil {
			return
		}
		switch resp.Type {
		case "instance_exited":
			HandleDaemonInstanceExited(resp.State)
		case daemonIPCFrameTypeCleanupMessage:
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
	c.pending = make(map[uint64]chan daemonIPCResponse)
	c.pendingMu.Unlock()
	close(c.done)
}

func handleDaemonInstanceOutputFrame(instanceName string, data []byte) {
	defer putDaemonOutputBuffer(data)
	if instanceName == "" || len(data) == 0 {
		return
	}
	HandleDaemonInstanceOutput(instanceName, data)
}

func queueDaemonInstanceOutput(instanceName string, data []byte) {
	if instanceName == "" || len(data) == 0 {
		putDaemonOutputBuffer(data)
		return
	}
	handleDaemonInstanceOutputFrame(instanceName, data)
}

func daemonRequest(req daemonIPCRequest) (daemonIPCResponse, error) {
	client := daemonClient
	if client == nil {
		return daemonIPCResponse{}, fmt.Errorf("daemon IPC is not connected")
	}
	req.ID = client.nextID.Add(1)
	respCh := client.registerPending(req.ID)
	defer client.unregisterPending(req.ID)

	req.BodyLen = len(req.Body)
	if err := validateDaemonIPCBodyLen(req.BodyLen); err != nil {
		return daemonIPCResponse{}, err
	}
	data, err := encodeDaemonIPCFrame(req.Type, req)
	if err != nil {
		return daemonIPCResponse{}, fmt.Errorf("encode daemon IPC request: %w", err)
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
		return daemonIPCResponse{}, fmt.Errorf("write daemon IPC request: %w", err)
	}

	select {
	case resp, ok := <-respCh:
		if !ok {
			return daemonIPCResponse{}, fmt.Errorf("daemon IPC disconnected")
		}
		if resp.Error != "" {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	case <-client.done:
		return daemonIPCResponse{}, fmt.Errorf("daemon IPC disconnected")
	case <-time.After(30 * time.Second):
		return daemonIPCResponse{}, fmt.Errorf("daemon IPC request timeout")
	}
}

func DaemonHello() (int, error) {
	helloMsg := fmt.Sprintf("%s %d", daemonHelloMsgPrefix, time.Now().UnixNano())
	resp, err := daemonRequest(daemonIPCRequest{Type: "hello", Msg: helloMsg})
	if err != nil {
		return 0, err
	}
	if resp.Msg != helloMsg {
		return 0, fmt.Errorf("daemon hello msg mismatch: expected %q, got %q", helloMsg, resp.Msg)
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

func RestartController() error {
	_, err := daemonRequest(daemonIPCRequest{Type: "restart_controller"})
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

func startDaemonInstance(insName string, command string, cleanupCommand string, path string, terminal int, inputEnc string, outputEnc string, cols uint16, rows uint16) (*DaemonRuntimeState, error) {
	resp, err := daemonRequest(daemonIPCRequest{Type: "start_instance", Instance: insName, Command: command, CleanupCommand: cleanupCommand, Path: path, Terminal: terminal, InputEnc: inputEnc, OutputEnc: outputEnc, Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return resp.State, nil
}

func UpdateDaemonInstanceConfig(insName string, cleanupCommand string) error {
	_, err := daemonRequest(daemonIPCRequest{Type: "update_instance_config", Instance: insName, CleanupCommand: cleanupCommand})
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
	if cfg.IsNoTerminal(sp.ActiveTerminalLocked()) {
		sp.Mu.Unlock()
		return
	}
	if cfg.IsPTYTerminal(sp.ActiveTerminalLocked()) && sp.TerminalStartupProtecting {
		data = sp.sanitizeTerminalStartupOutputLocked(data)
		if len(data) == 0 {
			sp.Mu.Unlock()
			return
		}
		sp.appendHistoryLocked(data, limit)
		sp.writeClientsLocked(websocket.BinaryMessage, data)
		sp.Mu.Unlock()
		return
	}
	idx := bytes.LastIndex(data, []byte("\x1b[3J"))
	if idx != -1 {
		sp.resetTerminalClientsLocked()
		sp.resetHistoryLocked(data[idx+4:], limit)
	} else {
		sp.appendHistoryLocked(data, limit)
	}
	sp.writeClientsLocked(websocket.BinaryMessage, data)
	sp.Mu.Unlock()
}

func HandleDaemonCleanupMessage(instanceName string, placeholder string, args []string) {
	sp, ok := GetByRuntimeAlias(instanceName)
	if !ok || sp == nil || placeholder == "" {
		return
	}
	message, colorCode, ok := renderDaemonCleanupMessage(placeholder, args)
	if !ok || message == "" {
		log.Printf("invalid daemon cleanup message: instance=%s placeholder=%q args=%q", instanceName, placeholder, args)
		return
	}
	data := buildTerminalMessage(colorCode, message)
	limit := cfg.GetHistoryLimit() * 1024
	sp.Mu.Lock()
	sp.appendHistoryLocked(data, limit)
	sp.writeClientsLocked(websocket.BinaryMessage, data)
	sp.Mu.Unlock()
}

func renderDaemonCleanupMessage(placeholder string, args []string) (string, string, bool) {
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
			return "", "", false
		}
		return fmt.Sprintf("清理命令构建失败: %s", normalizedArgs[0]), "\x1b[31m", true
	case "cleanup_command.stdout_failed":
		if !requireArgs(1) {
			return "", "", false
		}
		return fmt.Sprintf("清理命令输出读取失败: %s", normalizedArgs[0]), "\x1b[31m", true
	case "cleanup_command.stderr_failed":
		if !requireArgs(1) {
			return "", "", false
		}
		return fmt.Sprintf("清理命令错误输出读取失败: %s", normalizedArgs[0]), "\x1b[31m", true
	case "cleanup_command.start_failed":
		if !requireArgs(1) {
			return "", "", false
		}
		return fmt.Sprintf("清理命令启动失败: %s", normalizedArgs[0]), "\x1b[31m", true
	case "cleanup_command.started":
		if !requireArgs(0) {
			return "", "", false
		}
		return "清理命令开始.", "\x1b[34m", true
	case "cleanup_command.exited":
		if !requireArgs(1) {
			return "", "", false
		}
		return fmt.Sprintf("清理命令退出: %s", normalizedArgs[0]), "\x1b[31m", true
	case "cleanup_command.completed":
		if !requireArgs(0) {
			return "", "", false
		}
		return "清理命令完成.", "\x1b[34m", true
	default:
		return "", "", false
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
