package authz

import (
	"IpacPanel/controller/src/msg"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const DefaultCSRFHeaderName = "X-CSRF-Token"

const maxTerminalWebSocketSubprotocolLength = 4096

type CSRFValidator struct {
	CookieManager *CookieManager
	HeaderName    string
}

type terminalWebSocketSubprotocolParams struct {
	Instance string `json:"instance"`
	CSRF     string `json:"csrf"`
}

func NewCSRFValidator(cookieManager *CookieManager) *CSRFValidator {
	return &CSRFValidator{CookieManager: cookieManager, HeaderName: DefaultCSRFHeaderName}
}

func (v *CSRFValidator) RequireFromRequest(w http.ResponseWriter, r *http.Request) error {
	if r == nil {
		return newError(ErrorCodeCSRFInvalid, msg.CSRFValidationFailed, nil)
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		_, err := v.cookieManager().EnsureCSRFCookie(w, r)
		return err
	}
	if !v.ValidateHeaderAndCookie(r) {
		return newError(ErrorCodeCSRFInvalid, msg.CSRFValidationFailed, nil)
	}
	return nil
}

func (v *CSRFValidator) ValidateHeaderAndCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	cookieToken := v.cookieManager().CSRFToken(r)
	headerToken := strings.TrimSpace(r.Header.Get(v.headerName()))
	return cookieToken != "" && headerToken != "" && cookieToken == headerToken
}

func (v *CSRFValidator) ValidateToken(r *http.Request, token string) bool {
	cookieToken := v.cookieManager().CSRFToken(r)
	token = strings.TrimSpace(token)
	return cookieToken != "" && token != "" && cookieToken == token
}

func (v *CSRFValidator) ParseTerminalWebSocketSubprotocolParams(r *http.Request) (string, string, string, error) {
	if r == nil {
		return "", "", "", errors.New("websocket subprotocol request is nil")
	}
	headerValues := r.Header.Values("Sec-WebSocket-Protocol")
	if len(headerValues) != 1 {
		return "", "", "", errors.New("websocket subprotocol must have exactly one header value")
	}
	header := strings.TrimSpace(headerValues[0])
	if header == "" {
		return "", "", "", errors.New("websocket subprotocol header is empty")
	}

	var selectedProtocol string
	for _, raw := range strings.Split(header, ",") {
		token := strings.TrimSpace(raw)
		if token == "" || len(token) > maxTerminalWebSocketSubprotocolLength || !isRawBase64URLToken(token) {
			return "", "", "", errors.New("websocket subprotocol token is invalid")
		}
		if selectedProtocol != "" {
			// 当前终端握手设计只接受单个子协议 token, 多 token 明确失败。
			return "", "", "", errors.New("websocket subprotocol has multiple tokens")
		}
		selectedProtocol = token
	}

	decoded, err := base64.RawURLEncoding.DecodeString(selectedProtocol)
	if err != nil {
		return "", "", "", err
	}
	var params terminalWebSocketSubprotocolParams
	if err := json.Unmarshal(decoded, &params); err != nil {
		return "", "", "", err
	}
	instance := params.Instance
	csrf := params.CSRF
	if instance == "" || csrf == "" {
		return "", "", "", errors.New("websocket subprotocol params are incomplete")
	}
	return instance, csrf, selectedProtocol, nil
}

func isRawBase64URLToken(token string) bool {
	for _, char := range token {
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= 'a' && char <= 'z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		if char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func (v *CSRFValidator) ValidateWebSocketTokenExact(r *http.Request, token string) bool {
	cookieToken := v.cookieManager().CSRFToken(r)
	return cookieToken != "" && token != "" && cookieToken == token
}

func (v *CSRFValidator) cookieManager() *CookieManager {
	if v.CookieManager == nil {
		return NewCookieManager(NewOriginResolver())
	}
	return v.CookieManager
}

func (v *CSRFValidator) headerName() string {
	if strings.TrimSpace(v.HeaderName) == "" {
		return DefaultCSRFHeaderName
	}
	return strings.TrimSpace(v.HeaderName)
}
