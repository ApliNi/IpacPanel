package ipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MagicByte   byte = 0x49 // 'I'
	HeaderSize       = 12
	MaxBodySize      = 16 * 1024 * 1024
	MaxHeaderSize    = 64 * 1024
)

// 帧类型
const (
	FrameTypeControlReq  byte = 0x01 // 控制请求 (JSON)
	FrameTypeControlResp byte = 0x02 // 控制响应 (JSON)
	FrameTypeInstanceOut byte = 0x03 // 实例输出 (NameLen 1B + Name + RawData)
	FrameTypeInstanceIn  byte = 0x04 // 实例输入 (NameLen 1B + Name + RawData)
	FrameTypeEvent       byte = 0x05 // 异步事件 (JSON)
)

const RequestInputStdin = "input_stdin"

const (
	ControllerShutdownPurposeRestart = "restart"
	ControllerShutdownPurposeUpdate  = "update"
)

type Header struct {
	Magic      byte
	Type       byte
	Flags      uint16
	ID         uint32
	PayloadLen uint32
}

func (h Header) Encode() [HeaderSize]byte {
	var buf [HeaderSize]byte
	buf[0] = h.Magic
	buf[1] = h.Type
	binary.BigEndian.PutUint16(buf[2:4], h.Flags)
	binary.BigEndian.PutUint32(buf[4:8], h.ID)
	binary.BigEndian.PutUint32(buf[8:12], h.PayloadLen)
	return buf
}

func DecodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderSize {
		return Header{}, errors.New("IPC header buffer too small")
	}
	if buf[0] != MagicByte {
		return Header{}, fmt.Errorf("invalid IPC frame magic: 0x%02x", buf[0])
	}
	return Header{
		Magic:      buf[0],
		Type:       buf[1],
		Flags:      binary.BigEndian.Uint16(buf[2:4]),
		ID:         binary.BigEndian.Uint32(buf[4:8]),
		PayloadLen: binary.BigEndian.Uint32(buf[8:12]),
	}, nil
}

type Request struct {
	Type                      string   `json:"type,omitempty"`
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
	Body                      []byte   `json:"-"`
}

func (r *Request) Payload() []byte {
	if r.Body != nil {
		return r.Body
	}
	return r.Data
}

func (r *Request) SetPayload(data []byte) {
	r.Data = data
	r.Body = data
}

type Response struct {
	Type           string                 `json:"type,omitempty"`
	ID             uint64                 `json:"id,omitempty"`
	Msg            string                 `json:"msg,omitempty"`
	Placeholder    string                 `json:"placeholder,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Instance       string                 `json:"instance,omitempty"`
	BodyLen        int                    `json:"body_len,omitempty"`
	Body           []byte                 `json:"body,omitempty"`
	Error          string                 `json:"error,omitempty"`
	DaemonProtocol int                    `json:"daemon_protocol,omitempty"`
	Runtime        []InstanceRuntimeState `json:"runtime,omitempty"`
	State          *InstanceRuntimeState  `json:"state,omitempty"`
	ReleaseFunc    func()                 `json:"-"`
}

func (r *Response) Release() {
	if r == nil || r.ReleaseFunc == nil {
		return
	}
	release := r.ReleaseFunc
	r.ReleaseFunc = nil
	release()
}

func NewResponse(typ string, instance string, data []byte, errMsg string) Response {
	resp := Response{Type: typ, Instance: instance, Error: errMsg}
	if len(data) > 0 {
		resp.Body = data
		resp.BodyLen = len(data)
	}
	return resp
}

type Conn struct {
	closer  io.Closer
	reader  *bufio.Reader
	writer  *bufio.Writer
	writeMu sync.Mutex
	debug   atomic.Bool
}

func NewConn(reader io.Reader, writer io.Writer, closer io.Closer) *Conn {
	return &Conn{
		closer: closer,
		reader: bufio.NewReaderSize(reader, 65536),
		writer: bufio.NewWriterSize(writer, 65536),
	}
}

func (c *Conn) SetDebug(debug bool) {
	c.debug.Store(debug)
}

func (c *Conn) Close() error {
	if c.closer == nil {
		return nil
	}
	return c.closer.Close()
}

func (c *Conn) Flush() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writer.Flush()
}

func ValidateBodyLen(bodyLen int) error {
	if bodyLen < 0 {
		return fmt.Errorf("IPC body length is negative: %d", bodyLen)
	}
	if bodyLen > MaxBodySize {
		return fmt.Errorf("IPC body too large: %d bytes", bodyLen)
	}
	return nil
}

func (c *Conn) ReadHeader() (Header, error) {
	var buf [HeaderSize]byte
	if _, err := io.ReadFull(c.reader, buf[:]); err != nil {
		return Header{}, err
	}
	header, err := DecodeHeader(buf[:])
	if err != nil {
		return Header{}, err
	}
	if header.PayloadLen > MaxBodySize {
		return Header{}, fmt.Errorf("IPC payload too large: %d bytes", header.PayloadLen)
	}
	return header, nil
}

func (c *Conn) ReadPayload(payloadLen uint32) ([]byte, error) {
	if payloadLen == 0 {
		return nil, nil
	}
	buf := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// ReadInstancePayload 读取高速数据流负载, 格式为 [NameLen 1B][Name (NameLen)][Data (Remaining)]
func (c *Conn) ReadInstancePayload(payloadLen uint32, getBuf func(int) []byte, putBuf func([]byte)) (string, []byte, error) {
	if payloadLen == 0 {
		return "", nil, errors.New("empty instance payload")
	}
	nameLenByte, err := c.reader.ReadByte()
	if err != nil {
		return "", nil, fmt.Errorf("read instance name len: %w", err)
	}
	nameLen := int(nameLenByte)
	if uint32(1+nameLen) > payloadLen {
		return "", nil, errors.New("invalid instance name length in payload")
	}
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(c.reader, nameBuf); err != nil {
		return "", nil, fmt.Errorf("read instance name: %w", err)
	}
	instanceName := string(nameBuf)

	dataLen := int(payloadLen - 1 - uint32(nameLen))
	var data []byte
	if dataLen > 0 {
		if getBuf != nil {
			data = getBuf(dataLen)
		} else {
			data = make([]byte, dataLen)
		}
		if _, err := io.ReadFull(c.reader, data[:dataLen]); err != nil {
			if putBuf != nil {
				putBuf(data)
			}
			return "", nil, fmt.Errorf("read instance data: %w", err)
		}
		data = data[:dataLen]
	}
	return instanceName, data, nil
}

// ReadRequest 供 Daemon 端统一读取请求 (支持 ControlReq 与 InstanceIn)
func (c *Conn) ReadRequest() (*Request, error) {
	header, err := c.ReadHeader()
	if err != nil {
		return nil, err
	}
	switch header.Type {
	case FrameTypeInstanceIn:
		instance, data, err := c.ReadInstancePayload(header.PayloadLen, nil, nil)
		if err != nil {
			return nil, err
		}
		req := &Request{
			Type:     RequestInputStdin,
			Instance: instance,
			BodyLen:  len(data),
		}
		req.SetPayload(data)
		return req, nil
	case FrameTypeControlReq:
		payload, err := c.ReadPayload(header.PayloadLen)
		if err != nil {
			return nil, err
		}
		var req Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal IPC request: %w", err)
		}
		req.ID = uint64(header.ID)
		if c.debug.Load() {
			log.Printf("[<] ControlReq #%d type=%s inst=%s", req.ID, req.Type, req.Instance)
		}
		return &req, nil
	default:
		return nil, fmt.Errorf("unexpected frame type for request: 0x%02x", header.Type)
	}
}

// WriteControlRequest 供 Controller 发送控制请求
func (c *Conn) WriteControlRequest(req *Request) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal IPC request: %w", err)
	}
	header := Header{
		Magic:      MagicByte,
		Type:       FrameTypeControlReq,
		ID:         uint32(req.ID),
		PayloadLen: uint32(len(payload)),
	}
	hdrBuf := header.Encode()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.writer.Write(hdrBuf[:]); err != nil {
		return err
	}
	if _, err := c.writer.Write(payload); err != nil {
		return err
	}
	return c.writer.Flush()
}

// WriteControlResponse 供 Daemon 发送控制响应
func (c *Conn) WriteControlResponse(resp Response, flush bool) error {
	defer resp.Release()
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal IPC response: %w", err)
	}
	header := Header{
		Magic:      MagicByte,
		Type:       FrameTypeControlResp,
		ID:         uint32(resp.ID),
		PayloadLen: uint32(len(payload)),
	}
	hdrBuf := header.Encode()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.writer.Write(hdrBuf[:]); err != nil {
		return err
	}
	if _, err := c.writer.Write(payload); err != nil {
		return err
	}
	if flush {
		return c.writer.Flush()
	}
	return nil
}

// WriteEvent 供 Daemon 发送事件
func (c *Conn) WriteEvent(resp Response, flush bool) error {
	defer resp.Release()
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal IPC event: %w", err)
	}
	header := Header{
		Magic:      MagicByte,
		Type:       FrameTypeEvent,
		ID:         uint32(resp.ID),
		PayloadLen: uint32(len(payload)),
	}
	hdrBuf := header.Encode()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.writer.Write(hdrBuf[:]); err != nil {
		return err
	}
	if _, err := c.writer.Write(payload); err != nil {
		return err
	}
	if flush {
		return c.writer.Flush()
	}
	return nil
}

// IsEventResponse 判断响应类型是否为事件
func IsEventResponse(typ string) bool {
	return typ == "instance_exited" || typ == "cleanup_message"
}

// WriteResponse 统一兼容 Daemon 的响应写入
func (c *Conn) WriteResponse(resp Response) error {
	if resp.Type == "o" {
		return c.WriteInstanceOutputResponse(resp, true)
	}
	if IsEventResponse(resp.Type) {
		return c.WriteEvent(resp, true)
	}
	return c.WriteControlResponse(resp, true)
}

// WriteInstanceOutputResponse 供 Daemon 转发实例输出
func (c *Conn) WriteInstanceOutputResponse(resp Response, flush bool) error {
	defer resp.Release()
	return c.writeInstanceStream(FrameTypeInstanceOut, resp.Instance, resp.Body, flush)
}

// WriteInstanceInput 供 Controller 写入实例输入
func (c *Conn) WriteInstanceInput(instance string, data []byte) error {
	return c.writeInstanceStream(FrameTypeInstanceIn, instance, data, true)
}

func (c *Conn) writeInstanceStream(frameType byte, instance string, data []byte, flush bool) error {
	nameBytes := []byte(instance)
	nameLen := len(nameBytes)
	if nameLen > 255 {
		nameLen = 255
		nameBytes = nameBytes[:255]
	}
	payloadLen := 1 + nameLen + len(data)
	header := Header{
		Magic:      MagicByte,
		Type:       frameType,
		PayloadLen: uint32(payloadLen),
	}
	hdrBuf := header.Encode()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.writer.Write(hdrBuf[:]); err != nil {
		return err
	}
	if err := c.writer.WriteByte(byte(nameLen)); err != nil {
		return err
	}
	if nameLen > 0 {
		if _, err := c.writer.Write(nameBytes); err != nil {
			return err
		}
	}
	if len(data) > 0 {
		if _, err := c.writer.Write(data); err != nil {
			return err
		}
	}
	if flush {
		return c.writer.Flush()
	}
	return nil
}

// ReadResponse 供 Controller 读取 JSON 类型的响应与事件
func (c *Conn) ReadResponse(payloadLen uint32) (Response, error) {
	payload, err := c.ReadPayload(payloadLen)
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return Response{}, fmt.Errorf("unmarshal IPC response: %w", err)
	}
	return resp, nil
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
