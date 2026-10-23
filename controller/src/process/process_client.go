package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"fmt"
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
	wsTerminalFrameMaxBytes = 32 * 1024
	terminalSystemBlue      = "\x1b[34m"
	terminalSystemYellow    = "\x1b[33m"
)

var wsFrameBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, wsTerminalFrameMaxBytes)
		return &b
	},
}

func buildTerminalMessage(colorCode string, text string) []byte {
	timestamp := time.Now().Format(cfg.DisplayTimeLayout)
	return []byte(fmt.Sprintf("\r\n%s\x1b[1m[%s] [IpacPanel] %s\x1b[0m\r\n", colorCode, timestamp, text))
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

type WSClient struct {
	Conn               *websocket.Conn
	User               string
	Send               chan wsOutMessage
	Done               chan struct{}
	StartOnce          sync.Once
	CloseOnce          sync.Once
	closeTransportOnce sync.Once
	WriteMu            sync.Mutex
	TerminalWake       chan struct{}
	TerminalCursor     uint64
	TerminalReady      atomic.Bool
	Instance           *InstanceProcess
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
					client.WriteMu.Lock()
					err := client.Conn.WriteControl(websocket.PingMessage, []byte{}, deadline)
					client.WriteMu.Unlock()
					if err != nil {
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
		client.TerminalReady.Store(true)
		client.wakeTerminalWriter()
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
	if sp == nil || !client.TerminalReady.Load() {
		return nil
	}

	bufPtr := wsFrameBufferPool.Get().(*[]byte)
	defer wsFrameBufferPool.Put(bufPtr)

	sp.outputMu.Lock()
	n, nextSeq, dropped := sp.History.ReadInto(*bufPtr, client.TerminalCursor)
	client.TerminalCursor = nextSeq
	more := client.TerminalCursor < sp.History.EndSeq()
	sp.outputMu.Unlock()

	if dropped {
		notice := BuildWarningTerminalSystemMessage(msg.TerminalOutputDroppedWarning)
		if err := client.writeMessage(websocket.BinaryMessage, notice); err != nil {
			return err
		}
	}

	if n > 0 {
		if err := client.writeMessage(websocket.BinaryMessage, (*bufPtr)[:n]); err != nil {
			return err
		}
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
		_ = client.closeTransportOnly()
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
		_ = client.closeTransportOnly()
		return false
	}
}

func (client *WSClient) EnqueueTerminalLive(data []byte) bool {
	if client == nil {
		return false
	}
	if !client.TerminalReady.Load() {
		return true
	}
	client.startWriter()
	if client.Send == nil {
		return false
	}
	select {
	case <-client.Done:
		return false
	case client.Send <- wsOutMessage{Type: websocket.BinaryMessage, Data: data, SkipTerminalFlush: true}:
		return true
	default:
		_ = client.closeTransportOnly()
		return false
	}
}

func (client *WSClient) EnqueueTerminal(data []byte) bool {
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
	case client.Send <- wsOutMessage{Type: websocket.BinaryMessage, Data: data}:
		return true
	default:
		_ = client.closeTransportOnly()
		return false
	}
}

func (client *WSClient) wakeTerminalWriter() {
	if client == nil || client.TerminalWake == nil {
		return
	}
	select {
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

func (client *WSClient) closeTransportOnly() error {
	if client == nil {
		return nil
	}
	var err error
	client.closeTransportOnce.Do(func() {
		if client.Done != nil {
			close(client.Done)
		}
		if client.Conn != nil {
			err = client.Conn.Close()
		}
	})
	return err
}

func (client *WSClient) Close() error {
	if client == nil {
		return nil
	}
	var err error
	client.CloseOnce.Do(func() {
		err = client.closeTransportOnly()
		if client.Instance != nil {
			client.Instance.outputMu.Lock()
			delete(client.Instance.Clients, client)
			client.Instance.outputMu.Unlock()
		}
	})
	return err
}

func (sp *InstanceProcess) writeClientsLocked(messageType int, data []byte) {
	for client := range sp.Clients {
		if client == nil {
			delete(sp.Clients, client)
			continue
		}
		if !client.Enqueue(messageType, data) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) writeTerminalLiveClientsLocked(data []byte) {
	for client := range sp.Clients {
		if client == nil {
			delete(sp.Clients, client)
			continue
		}
		if !client.EnqueueTerminalLive(data) {
			delete(sp.Clients, client)
		}
	}
}
func (sp *InstanceProcess) wakeTerminalClientsLocked() {
	for client := range sp.Clients {
		if client == nil {
			delete(sp.Clients, client)
			continue
		}
		if !client.TerminalReady.Load() {
			continue
		}
		client.wakeTerminalWriter()
	}
}

func (sp *InstanceProcess) sendClientsLocked(messageType int, data []byte) {
	sp.writeClientsLocked(messageType, data)
}

func (sp *InstanceProcess) resetTerminalClientsLocked(reset []byte) {
	for client := range sp.Clients {
		if client == nil {
			delete(sp.Clients, client)
			continue
		}
		if !client.EnqueueTerminalControl(reset) {
			delete(sp.Clients, client)
		}
	}
}

func (sp *InstanceProcess) detachClientsLocked() []*WSClient {
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
	clients := make([]*WSClient, 0, len(sp.Clients))
	for client := range sp.Clients {
		if client != nil {
			clients = append(clients, client)
		}
		delete(sp.Clients, client)
	}
	return clients
}

func (sp *InstanceProcess) DetachClientsLocked() []*WSClient {
	return sp.detachClientsLocked()
}

func (sp *InstanceProcess) attachClientsLocked(clients []*WSClient) {
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
	for _, client := range clients {
		if client == nil {
			continue
		}
		client.Instance = sp
		sp.Clients[client] = true
	}
}

func (sp *InstanceProcess) closeDetachedClients(clients []*WSClient) {
	for _, client := range clients {
		if client != nil {
			_ = client.Close()
		}
	}
}

func (sp *InstanceProcess) DetachAndCloseAllClients() {
	if sp == nil {
		return
	}
	sp.Mu.Lock()
	clients := sp.detachClientsLocked()
	if sp.State == processStateStopped {
		sp.shrinkTerminalHistoryLocked()
	}
	sp.Mu.Unlock()
	sp.closeDetachedClients(clients)
}
func (sp *InstanceProcess) DisconnectClientsByUser(user string) {
	if sp == nil || strings.TrimSpace(user) == "" {
		return
	}
	sp.outputMu.Lock()
	toClose := make([]*WSClient, 0)
	for client := range sp.Clients {
		if client == nil || client.User != user {
			continue
		}
		delete(sp.Clients, client)
		toClose = append(toClose, client)
	}
	sp.outputMu.Unlock()
	for _, client := range toClose {
		_ = client.Close()
	}
}

func (sp *InstanceProcess) disconnectAllClients() {
	sp.DetachAndCloseAllClients()
}

func (sp *InstanceProcess) appendAndBroadcastLocked(messageType int, data []byte, limit int) {
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
	if messageType == websocket.BinaryMessage {
		sp.appendHistoryLocked(data, limit)
		sp.wakeTerminalClientsLocked()
		return
	}
	sp.writeClientsLocked(messageType, data)
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
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
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
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
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
	sp.outputMu.Lock()
	defer sp.outputMu.Unlock()
	client.Instance = sp
	sp.Clients[client] = true
}

func (sp *InstanceProcess) shouldSkipHistoryForTerminalInitialLocked() bool {
	return cfg.IsPTYTerminal(sp.activeTerminalLocked()) && sp.PTYAlternateScreenActive
}

func (sp *InstanceProcess) resetPTYAlternateScreenStateLocked() {
	if sp == nil {
		return
	}
	sp.outputMu.Lock()
	sp.PTYAlternateScreenActive = false
	sp.PTYAlternateScreenPending = nil
	sp.outputMu.Unlock()
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

// ShrinkTerminalHistoryLocked 压缩终端历史缓冲区 (调用者须持有 sp.Mu 或确保并发安全).
func (sp *InstanceProcess) ShrinkTerminalHistoryLocked() {
	if sp == nil {
		return
	}
	sp.outputMu.Lock()
	sp.History.ShrinkToFit()
	sp.outputMu.Unlock()
}

func (sp *InstanceProcess) shrinkTerminalHistoryLocked() {
	sp.ShrinkTerminalHistoryLocked()
}

// ShrinkTerminalHistory 压缩终端历史缓冲区.
func (sp *InstanceProcess) ShrinkTerminalHistory() {
	sp.ShrinkTerminalHistoryLocked()
}
