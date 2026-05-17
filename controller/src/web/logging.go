package web

import (
	cfg "IpacPanel/controller/src/config"
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remote
}

func trustedForwardedProto(r *http.Request) string {
	if r == nil {
		return ""
	}
	if !cfg.IsTrustedProxyIP(remoteIP(r)) {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for _, part := range parts {
			segments := strings.Split(part, ";")
			for _, segment := range segments {
				kv := strings.SplitN(strings.TrimSpace(segment), "=", 2)
				if len(kv) != 2 {
					continue
				}
				if !strings.EqualFold(strings.TrimSpace(kv[0]), "proto") {
					continue
				}
				proto := strings.Trim(strings.TrimSpace(kv[1]), `"`)
				if strings.EqualFold(proto, "https") {
					return "https"
				}
				if strings.EqualFold(proto, "http") {
					return "http"
				}
			}
		}
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		return ""
	}
	if idx := strings.Index(proto, ","); idx >= 0 {
		proto = proto[:idx]
	}
	proto = strings.TrimSpace(proto)
	if strings.EqualFold(proto, "https") {
		return "https"
	}
	if strings.EqualFold(proto, "http") {
		return "http"
	}
	return ""
}

type requestLogState struct {
	requestID    string
	user         string
	instance     string
	routeKind    string
	action       string
	errorStatus  int
	errorMessage string
	errorDetail  string
	accessLogged bool
}

type requestLogCarrier interface {
	RequestLogState() *requestLogState
	StatusCode() int
	BytesWritten() int
	WroteHeader() bool
}

type responseWriterUnwrapper interface {
	Unwrap() http.ResponseWriter
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
	wroteHeader  bool
	state        *requestLogState
}

func newLoggingResponseWriter(w http.ResponseWriter, requestID string) *loggingResponseWriter {
	return &loggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		state: &requestLogState{
			requestID: requestID,
			routeKind: "http",
		},
	}
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(statusCode)
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytesWritten += n
	return n, err
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("响应写入器不支持劫持连接")
	}
	return hijacker.Hijack()
}

func (w *loggingResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	readerFrom, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(w, r)
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := readerFrom.ReadFrom(r)
	w.bytesWritten += int(n)
	return n, err
}

func (w *loggingResponseWriter) RequestLogState() *requestLogState {
	return w.state
}

func (w *loggingResponseWriter) StatusCode() int {
	return w.statusCode
}

func (w *loggingResponseWriter) BytesWritten() int {
	return w.bytesWritten
}

func (w *loggingResponseWriter) WroteHeader() bool {
	return w.wroteHeader
}

func WithRequestLogging(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		lw := newLoggingResponseWriter(w, requestID)
		lw.Header().Set("X-Request-Id", requestID)
		next.ServeHTTP(lw, r)
		if !shouldLogRequest(r) {
			return
		}
		statusCode := lw.StatusCode()
		LogWebAccess(lw, r, statusCode)
	})
}

func WithRecover(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			message := fmt.Sprintf("panic: %v", recovered)
			fields := baseRequestFields(w, r)
			fields = append(fields,
				logField{Key: "level", Value: "error"},
				logField{Key: "type", Value: "panic"},
				logField{Key: "error", Value: message},
				logField{Key: "stack", Value: string(debug.Stack())},
			)
			LogEvent(fields...)
			MarkAPIError(w, http.StatusInternalServerError, "服务器内部错误", fmt.Errorf("%s", message))
			if rw, ok := w.(requestLogCarrier); ok && rw.WroteHeader() {
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				WriteJSONStatus(w, http.StatusInternalServerError, APIResponse{OK: false, Message: "服务器内部错误"}, "返回 panic 错误失败")
				return
			}
			http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

func MarkRequestUser(w http.ResponseWriter, username string) {
	state := GetRequestLogState(w)
	if state == nil {
		return
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}
	state.user = username
}

func MarkRequestInstance(w http.ResponseWriter, instanceName string) {
	state := GetRequestLogState(w)
	if state == nil {
		return
	}
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		return
	}
	state.instance = instanceName
}

func MarkRequestRouteKind(w http.ResponseWriter, routeKind string) {
	state := GetRequestLogState(w)
	if state == nil {
		return
	}
	routeKind = strings.TrimSpace(routeKind)
	if routeKind == "" {
		return
	}
	state.routeKind = routeKind
}

func MarkRequestAction(w http.ResponseWriter, action string) {
	state := GetRequestLogState(w)
	if state == nil {
		return
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return
	}
	state.action = action
}

func MarkAPIError(w http.ResponseWriter, statusCode int, userMessage string, err error) {
	state := GetRequestLogState(w)
	if state != nil {
		state.errorStatus = statusCode
		state.errorMessage = strings.TrimSpace(userMessage)
		if err != nil {
			state.errorDetail = err.Error()
		}
	}
}

func LogWebAccess(w http.ResponseWriter, r *http.Request, statusCode int) {
	state := GetRequestLogState(w)
	if state != nil {
		if state.accessLogged {
			return
		}
		state.accessLogged = true
	}
	log.Print(formatWebAccessLog(r, state, statusCode, clientIP(r)))
}

type logField struct {
	Key   string
	Value interface{}
}

type APIResponse struct {
	OK      bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Message string      `json:"message,omitempty"`
}

func WriteJSONStatus(w http.ResponseWriter, statusCode int, data interface{}, _ string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func WriteAPIError(w http.ResponseWriter, statusCode int, userMessage string, err error) {
	MarkAPIError(w, statusCode, userMessage, err)
	WriteJSONStatus(w, statusCode, APIResponse{OK: false, Message: strings.TrimSpace(userMessage)}, "")
}

func WriteOK(w http.ResponseWriter, data interface{}) {
	WriteJSONStatus(w, http.StatusOK, APIResponse{OK: true, Data: data}, "")
}

func LogEvent(fields ...logField) {
	parts := make([]string, 0, len(fields))
	for i := range fields {
		key := strings.TrimSpace(fields[i].Key)
		if key == "" || fields[i].Value == nil {
			continue
		}
		parts = append(parts, key+"="+formatLogValue(fields[i].Value))
	}
	if len(parts) == 0 {
		return
	}
	log.Print(strings.Join(parts, " "))
}

func formatWebAccessLog(r *http.Request, state *requestLogState, statusCode int, ip string) string {
	username := "-"
	if state != nil && strings.TrimSpace(state.user) != "" {
		username = strings.TrimSpace(state.user)
	}
	requestLine := "-"
	if r != nil {
		requestPath := ""
		if r.URL != nil {
			requestPath = strings.TrimSpace(r.URL.Path)
		}
		requestLine = fmt.Sprintf("%s %s", strings.TrimSpace(r.Method), requestPath)
		requestLine = strings.TrimSpace(requestLine)
		if requestLine == "" {
			requestLine = "-"
		}
	}
	message := ""
	if state != nil {
		message = strings.TrimSpace(state.errorMessage)
		if message == "" {
			message = strings.TrimSpace(state.errorDetail)
		}
	}
	line := fmt.Sprintf("[WEB] [%s] %s - %q %d", username, strings.TrimSpace(ip), requestLine, statusCode)
	if message != "" {
		line += " " + message
	}
	return line
}

func formatLogValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strconv.Quote(strings.TrimSpace(v))
	case fmt.Stringer:
		return strconv.Quote(strings.TrimSpace(v.String()))
	default:
		return fmt.Sprint(value)
	}
}

func RequestStateFields(state *requestLogState) []logField {
	if state == nil {
		return nil
	}
	fields := []logField{{Key: "request_id", Value: state.requestID}}
	if state.routeKind != "" {
		fields = append(fields, logField{Key: "route_kind", Value: state.routeKind})
	}
	if state.user != "" {
		fields = append(fields, logField{Key: "user", Value: state.user})
	}
	if state.instance != "" {
		fields = append(fields, logField{Key: "instance", Value: state.instance})
	}
	if state.action != "" {
		fields = append(fields, logField{Key: "action", Value: state.action})
	}
	return fields
}

func baseRequestFields(w http.ResponseWriter, r *http.Request) []logField {
	fields := RequestStateFields(GetRequestLogState(w))
	fields = append(fields,
		logField{Key: "method", Value: r.Method},
		logField{Key: "path", Value: r.URL.Path},
		logField{Key: "ip", Value: clientIP(r)},
	)
	return fields
}

func GetRequestLogState(w http.ResponseWriter) *requestLogState {
	for w != nil {
		if carrier, ok := w.(requestLogCarrier); ok {
			return carrier.RequestLogState()
		}
		unwrapper, ok := w.(responseWriterUnwrapper)
		if !ok {
			return nil
		}
		w = unwrapper.Unwrap()
	}
	return nil
}

func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	requestIP := remoteIP(r)
	if cfg.IsTrustedProxyIP(requestIP) {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
			return realIP
		}
	}
	return requestIP
}

func shouldLogRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	return true
}

func levelForStatus(statusCode int) string {
	if statusCode >= http.StatusInternalServerError {
		return "error"
	}
	if statusCode >= http.StatusBadRequest {
		return "warn"
	}
	return "info"
}
