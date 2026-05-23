package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"
	"fmt"
	"log"

	process "IpacPanel/controller/src/process"

	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: authz.WebSocketOriginValidated}

const (
	wsReadLimit      = 16 * 1024
	wsPongWait       = 75 * time.Second
	terminalResetMsg = "\x1bc"
)

type instanceWSRequest struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func parseInstanceWSControlFrame(rawMsg []byte) (instanceWSRequest, error) {
	var request instanceWSRequest
	if err := json.Unmarshal(rawMsg, &request); err != nil {
		return instanceWSRequest{}, fmt.Errorf(msg.WSControlFrameJSONInvalidFmt, err)
	}
	return request, nil
}

func disconnectUserWS(username string) {
	user := strings.TrimSpace(username)
	if user == "" {
		return
	}
	for _, sp := range process.List() {
		if sp == nil {
			continue
		}
		toClose := make([]*process.WSClient, 0)
		sp.Mu.Lock()
		for client := range sp.Clients {
			if client == nil || client.User != user {
				continue
			}
			delete(sp.Clients, client)
			toClose = append(toClose, client)
		}
		sp.Mu.Unlock()
		for _, client := range toClose {
			_ = client.Close()
		}
	}
}

func HandleApiInstanceWs(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 1 {
		web.WriteAPIError(w, http.StatusHTTPVersionNotSupported, msg.WSTerminalHTTP11Required, nil)
		return
	}
	params, ok := authz.TerminalWebSocketParamsFromRequest(r)
	if !ok {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.InternalServerError, nil)
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	username := authedUser.User
	sp, ok := web.RequireInstanceProcessByExactName(w, authedUser, params.Instance)
	if !ok {
		return
	}
	if cfg.IsNoTerminal(sp.ActiveTerminalModeSnapshot()) {
		web.WriteAPIError(w, http.StatusConflict, msg.NoTerminalWebSocketUnsupported, nil)
		return
	}

	conn, err := upgrader.Upgrade(w, r, http.Header{"Sec-WebSocket-Protocol": []string{params.SelectedProtocol}})
	if err != nil {
		log.Printf(msg.WSHandshakeFailedLogFmt, msg.WSHandshakeFailed, err.Error())
		return
	}
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	client := &process.WSClient{Conn: conn, User: username}

	sp.Mu.Lock()
	initial := sp.AddClientTerminalInitialLocked(client, []byte(terminalResetMsg))
	sp.Mu.Unlock()

	if err := client.SendInitialTerminal(initial); err != nil {
		_ = client.Close()
		return
	}
	sp.Mu.Lock()
	sp.AttachClientLocked(client)
	sp.Mu.Unlock()
	client.EnableTerminalHistory()

	defer func() {
		sp.Mu.Lock()
		delete(sp.Clients, client)
		sp.Mu.Unlock()
		_ = client.Close()
	}()

	for {
		messageType, rawMsg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		switch messageType {
		case websocket.BinaryMessage:
			if err := sp.SendInput(rawMsg); err != nil {
				log.Printf(msg.WSInputFailedLogFmt, msg.SendInputFailed, err.Error())
				_ = client.SendControlError(err.Error())
				break
			}
			continue
		case websocket.TextMessage:
			termMsg, err := parseInstanceWSControlFrame(rawMsg)
			if err != nil {
				log.Printf(msg.WSControlFrameInvalidLogFmt, msg.WSControlFrameInvalid, err.Error())
				_ = client.SendControlError(err.Error())
				break
			}

			switch termMsg.Type {
			case "resize":
				if termMsg.Cols <= 0 || termMsg.Rows <= 0 || termMsg.Cols > 4000 || termMsg.Rows > 2500 {
					err := fmt.Errorf(msg.InvalidTerminalSizeFmt, termMsg.Cols, termMsg.Rows)
					log.Printf(msg.WSResizeTerminalFailedLogFmt, msg.ResizeTerminalFailed, err.Error())
					_ = client.SendControlError(err.Error())
					break
				}
				if err := sp.ResizeTerminal(uint16(termMsg.Cols), uint16(termMsg.Rows)); err != nil {
					log.Printf(msg.WSResizeTerminalFailedLogFmt, msg.ResizeTerminalFailed, err.Error())
					_ = client.SendControlError(err.Error())
					break
				}
				continue
			default:
				log.Printf(msg.WSControlFrameInvalidLogFmt, msg.WSControlFrameUnknown, termMsg.Type)
				_ = client.SendControlError(msg.WSControlFrameUnknown)
			}
		default:
			log.Printf(msg.WSControlFrameInvalidLogFmt, msg.WSControlFrameUnknown, fmt.Sprintf("message_type=%d", messageType))
			_ = client.SendControlError(msg.WSControlFrameUnknown)
		}
		break
	}
}
