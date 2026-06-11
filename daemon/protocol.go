package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const maxIPCHeaderSize = 64 * 1024
const maxIPCBodySize = 16 * 1024 * 1024
const ipcRequestInputStdin = "input_stdin"

const (
	ControllerShutdownPurposeRestart = "restart"
	ControllerShutdownPurposeUpdate  = "update"
)

type IPCRequest struct {
	Type                      string   `json:"-"`
	ID                        uint64   `json:"id,omitempty"`
	Msg                       string   `json:"msg,omitempty"`
	Debug                     bool     `json:"debug,omitempty"`
	Instance                  string   `json:"instance,omitempty"`
	NewName                   string   `json:"new_name,omitempty"`
	CommandArgv               []string `json:"command_argv,omitempty"`
	CleanupCommandArgv        []string `json:"cleanup_command_argv,omitempty"`
	Path                      string   `json:"path,omitempty"`
	Terminal                  int      `json:"terminal,omitempty"`
	InputEnc                  string   `json:"input_encoding,omitempty"`
	OutputEnc                 string   `json:"output_encoding,omitempty"`
	RuntimeCode               int      `json:"runtime_code,omitempty"`
	Force                     bool     `json:"force,omitempty"`
	Cols                      uint16   `json:"cols,omitempty"`
	Rows                      uint16   `json:"rows,omitempty"`
	ControllerShutdownPurpose string   `json:"controller_shutdown_purpose,omitempty"`
	BodyLen                   int      `json:"body_len,omitempty"`
	Data                      []byte   `json:"-"`
}

type IPCResponse struct {
	Type           string                 `json:"-"`
	ID             uint64                 `json:"id,omitempty"`
	Msg            string                 `json:"msg,omitempty"`
	Placeholder    string                 `json:"placeholder,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Instance       string                 `json:"instance,omitempty"`
	BodyLen        int                    `json:"body_len,omitempty"`
	Body           []byte                 `json:"-"`
	Error          string                 `json:"error,omitempty"`
	DaemonProtocol int                    `json:"daemon_protocol,omitempty"`
	Runtime        []InstanceRuntimeState `json:"runtime,omitempty"`
	State          *InstanceRuntimeState  `json:"state,omitempty"`
	release        func()
}

func (r *IPCResponse) Release() {
	if r == nil || r.release == nil {
		return
	}
	release := r.release
	r.release = nil
	release()
}

func NewIPCResponse(typ string, instance string, data []byte, errMsg string) IPCResponse {
	resp := IPCResponse{
		Type:     typ,
		Instance: instance,
		Error:    errMsg,
	}
	if len(data) > 0 {
		resp.Body = data
	}
	return resp
}

type IPCConn struct {
	closer io.Closer
	reader *bufio.Reader
	writer *bufio.Writer
	debug  atomic.Bool
}

func NewIPCConn(reader io.Reader, writer io.Writer, closer io.Closer) *IPCConn {
	return &IPCConn{
		closer: closer,
		reader: bufio.NewReaderSize(reader, 65536),
		writer: bufio.NewWriterSize(writer, 65536),
	}
}

func (c *IPCConn) SetDebug(debug bool) {
	c.debug.Store(debug)
}

func decodeIPCFrame(line []byte, target interface{ setType(string) }) error {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 || line[0] != ':' {
		return fmt.Errorf("invalid IPC frame prefix")
	}
	line = line[1:]
	sep := bytes.Index(line, []byte(": "))
	if sep <= 0 {
		return fmt.Errorf("invalid IPC frame header")
	}
	frameType := string(line[:sep])
	if err := json.Unmarshal(line[sep+len(": "):], target); err != nil {
		return err
	}
	target.setType(frameType)
	return nil
}

func encodeIPCFrame(frameType string, payload interface{}) ([]byte, error) {
	if frameType == "" {
		return nil, fmt.Errorf("IPC frame type is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, 1+len(frameType)+len(": ")+len(data)+1)
	frame = append(frame, ':')
	frame = append(frame, frameType...)
	frame = append(frame, ": "...)
	frame = append(frame, data...)
	frame = append(frame, '\n')
	return frame, nil
}

func readIPCHeaderLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxIPCHeaderSize {
			return nil, fmt.Errorf("IPC header too large: %d bytes", len(line))
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

func readIPCInputToken(reader *bufio.Reader, delimiter byte, name string) ([]byte, error) {
	var token []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read IPC input %s: %w", name, err)
		}
		if b == delimiter {
			if len(token) == 0 {
				return nil, fmt.Errorf("IPC input %s is required", name)
			}
			return token, nil
		}
		token = append(token, b)
		if len(token)+len(":i::") > maxIPCHeaderSize {
			return nil, fmt.Errorf("IPC input header too large: %d bytes", len(token)+len(":i::"))
		}
	}
}

func parseIPCBodyLen(raw []byte) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("IPC body length is required")
	}
	value := 0
	for _, b := range raw {
		if b < '0' || b > '9' {
			return 0, fmt.Errorf("IPC body length is not decimal")
		}
		digit := int(b - '0')
		if value > (maxIPCBodySize-digit)/10 {
			return 0, fmt.Errorf("IPC body too large")
		}
		value = value*10 + digit
	}
	if err := validateIPCBodyLen(value); err != nil {
		return 0, err
	}
	return value, nil
}

func (c *IPCConn) readIPCInputRequest() (*IPCRequest, error) {
	prefix, err := c.reader.Peek(len(":i:"))
	if err != nil {
		return nil, fmt.Errorf("read IPC input prefix: %w", err)
	}
	if !bytes.Equal(prefix, []byte(":i:")) {
		return nil, fmt.Errorf("invalid IPC input prefix")
	}
	if _, err := c.reader.Discard(len(":i:")); err != nil {
		return nil, fmt.Errorf("discard IPC input prefix: %w", err)
	}

	instance, err := readIPCInputToken(c.reader, ':', "instance")
	if err != nil {
		return nil, err
	}
	bodyLenRaw, err := readIPCInputToken(c.reader, ':', "body length")
	if err != nil {
		return nil, err
	}
	space, err := c.reader.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read IPC input header terminator: %w", err)
	}
	if space != ' ' {
		return nil, fmt.Errorf("invalid IPC input header terminator")
	}

	bodyLen, err := parseIPCBodyLen(bodyLenRaw)
	if err != nil {
		return nil, err
	}
	req := IPCRequest{
		Type:     ipcRequestInputStdin,
		Instance: string(instance),
		BodyLen:  bodyLen,
	}
	if bodyLen > 0 {
		req.Data = make([]byte, bodyLen)
		if _, err := io.ReadFull(c.reader, req.Data); err != nil {
			return nil, fmt.Errorf("read IPC input body: %w", err)
		}
	}
	return &req, nil
}

func validateIPCBodyLen(bodyLen int) error {
	if bodyLen < 0 {
		return fmt.Errorf("IPC body length is negative: %d", bodyLen)
	}
	if bodyLen > maxIPCBodySize {
		return fmt.Errorf("IPC body too large: %d bytes", bodyLen)
	}
	return nil
}

func debugIPCHeader(header []byte, hasBody bool) string {
	text := strings.TrimRight(string(header), "\r\n")
	if hasBody {
		return text + " [skip body]"
	}
	return text
}

func (r *IPCRequest) setType(frameType string) {
	r.Type = frameType
}

func (r IPCResponse) setType(frameType string) {}

func (c *IPCConn) ReadRequest() (*IPCRequest, error) {
	prefix, err := c.reader.Peek(len(":i:"))
	if err == nil && bytes.Equal(prefix, []byte(":i:")) {
		return c.readIPCInputRequest()
	}
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("read IPC message prefix: %w", err)
	}
	line, err := readIPCHeaderLine(c.reader)
	if err != nil {
		return nil, fmt.Errorf("read IPC message: %w", err)
	}
	var req IPCRequest
	if err := decodeIPCFrame(line, &req); err != nil {
		return nil, fmt.Errorf("decode IPC message: %w", err)
	}
	if err := validateIPCBodyLen(req.BodyLen); err != nil {
		return nil, err
	}
	if c.debug.Load() {
		log.Printf("[<] %s", debugIPCHeader(line, req.BodyLen > 0))
	}
	if req.BodyLen > 0 {
		req.Data = make([]byte, req.BodyLen)
		if _, err := io.ReadFull(c.reader, req.Data); err != nil {
			return nil, fmt.Errorf("read IPC body: %w", err)
		}
	}
	return &req, nil
}

func (c *IPCConn) WriteResponse(resp IPCResponse) error {
	if resp.Type == "o" {
		return c.WriteInstanceOutputResponse(resp, true)
	}
	defer resp.Release()
	return c.writeResponseFrame(resp, true)
}

func (c *IPCConn) writeResponseFrame(resp IPCResponse, flush bool) error {
	resp.BodyLen = len(resp.Body)
	if err := validateIPCBodyLen(resp.BodyLen); err != nil {
		return err
	}
	data, err := encodeIPCFrame(resp.Type, resp)
	if err != nil {
		return fmt.Errorf("encode IPC response: %w", err)
	}
	if c.debug.Load() {
		log.Printf("[>] %s", debugIPCHeader(data, resp.BodyLen > 0))
	}
	if _, err := c.writer.Write(data); err != nil {
		return fmt.Errorf("write IPC response: %w", err)
	}
	if resp.BodyLen > 0 {
		if _, err := c.writer.Write(resp.Body); err != nil {
			return fmt.Errorf("write IPC body: %w", err)
		}
	}
	if flush {
		return c.writer.Flush()
	}
	return nil
}

func (c *IPCConn) WriteInstanceOutputResponse(resp IPCResponse, flush bool) error {
	defer resp.Release()
	bodyLen := len(resp.Body)
	if err := validateIPCBodyLen(bodyLen); err != nil {
		return err
	}
	if resp.Instance == "" {
		return fmt.Errorf("instance output instance is required")
	}
	if strings.Contains(resp.Instance, ":") {
		return fmt.Errorf("instance output instance contains reserved separator")
	}
	header := make([]byte, 0, len(":o:")+len(resp.Instance)+1+20+len(": "))
	header = append(header, ":o:"...)
	header = append(header, resp.Instance...)
	header = append(header, ':')
	header = strconv.AppendInt(header, int64(bodyLen), 10)
	header = append(header, ": "...)
	if _, err := c.writer.Write(header); err != nil {
		return fmt.Errorf("write IPC instance output header: %w", err)
	}
	if bodyLen > 0 {
		if _, err := c.writer.Write(resp.Body); err != nil {
			return fmt.Errorf("write IPC instance output body: %w", err)
		}
	}
	if flush {
		return c.writer.Flush()
	}
	return nil
}

func (c *IPCConn) Flush() error {
	return c.writer.Flush()
}

type InstanceRuntimeState struct {
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
	InstanceLifecycleStopped  = "stopped"
	InstanceLifecycleRunning  = "running"
	InstanceLifecycleStopping = "stopping"
	InstanceLifecycleCleaning = "cleaning"
)

const (
	NoTerminal  = 1
	Terminal    = 2
	PTYTerminal = 3
)

func NormalizeTerminalMode(mode int) int {
	switch mode {
	case NoTerminal, Terminal, PTYTerminal:
		return mode
	default:
		return Terminal
	}
}

func IsNoTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == NoTerminal
}

func IsPTYTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == PTYTerminal
}

const (
	RuntimeCodeUnknown        = 0
	RuntimeCodeRunning        = 1
	RuntimeCodeManualStop     = 10
	RuntimeCodeManualKill     = 11
	RuntimeCodeRestarting     = 12
	RuntimeCodeUnexpectedExit = 20
)

func (c *IPCConn) Close() error {
	if c.closer == nil {
		return nil
	}
	return c.closer.Close()
}
