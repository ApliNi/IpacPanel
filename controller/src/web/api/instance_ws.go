package api

import (
	cfg "IpacPanel/controller/src/config"
	web "IpacPanel/controller/src/web"
	"encoding/base64"
	"fmt"
	"log"

	process "IpacPanel/controller/src/process"

	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: web.IsSameOriginRequest}

const (
	wsReadLimit      = 64 * 1024
	wsPongWait       = 75 * time.Second
	terminalResetMsg = "\x1bc"
)

type instanceWSRequest struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
	Data string `json:"data"`
}

func HandleApiInstanceWs(w http.ResponseWriter, r *http.Request) {
	web.MarkRequestRouteKind(w, "ws")
	if r.ProtoMajor != 1 {
		web.WriteAPIError(w, http.StatusHTTPVersionNotSupported, "WebSocket 终端仅支持 HTTP/1.1", nil)
		return
	}
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:         true,
		Methods:             []string{http.MethodGet},
		CSRFFromQuery:       true,
		WSInstanceFromQuery: true,
	})
	if !ok {
		return
	}
	authedUser := guard.User
	username := authedUser.User
	sp := guard.Instance
	if cfg.IsNoTerminal(sp.ActiveTerminalModeSnapshot()) {
		web.WriteAPIError(w, http.StatusConflict, "实例当前为无终端模式, 不支持 WebSocket 终端", nil)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("route_kind=ws error=%q detail=%q", "websocket handshake failed", err.Error())
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
		messageType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var termMsg instanceWSRequest
		if err := json.Unmarshal(msg, &termMsg); err != nil {
			continue
		}

		switch termMsg.Type {
		case "resize":
			sp.ResizeTerminal(termMsg.Cols, termMsg.Rows)
			continue
		case "input":
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(termMsg.Data))
			if err != nil {
				log.Printf("route_kind=ws action=input error=%q detail=%q", "invalid websocket input", fmt.Sprintf("decode ws input: %v", err))
				continue
			}
			_ = sp.SendInput(decoded)
		}
	}
}
