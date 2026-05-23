package web

import (
	"IpacPanel/controller/src/msg"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type SSEWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

func BeginSSE(w http.ResponseWriter) (*SSEWriter, bool) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		WriteAPIError(w, http.StatusInternalServerError, msg.StreamingFlushUnsupported, err)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	sse, ok := NewSSEWriter(w)
	if !ok {
		WriteAPIError(w, http.StatusInternalServerError, msg.StreamingUnsupported, fmt.Errorf("response writer does not implement http.Flusher"))
		return nil, false
	}
	if err := sse.SendComment(); err != nil {
		MarkAPIError(w, http.StatusInternalServerError, msg.SSEWriteFailed, err)
		return nil, false
	}
	return sse, true
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	return &SSEWriter{w: w, flusher: flusher}, true
}

func (s *SSEWriter) SendEvent(eventName string, payload interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if eventName == "" {
		eventName = "message"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\n", eventName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", string(data)); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *SSEWriter) SendComment() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprint(s.w, ":\n\n"); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
