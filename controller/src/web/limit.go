package web

import (
	"IpacPanel/controller/src/msg"
	"net/http"
	"strings"
)

// NOTE: Frontend upload chunk size defaults to 10MiB.
// Keep a small buffer so chunk uploads and JSON payloads can pass.
const maxRequestBodyBytes int64 = 10*1024*1024 + 512*1024 // 10.5MiB

func writeRequestEntityTooLarge(w http.ResponseWriter, r *http.Request) {
	// Prefer JSON shape for API routes.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		WriteAPIError(w, http.StatusRequestEntityTooLarge, msg.RequestBodyTooLarge, nil)
		return
	}
	http.Error(w, msg.RequestBodyTooLarge, http.StatusRequestEntityTooLarge)
}

func WithMaxRequestBody(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce on requests that may include a body.
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if r.ContentLength > maxRequestBodyBytes {
				writeRequestEntityTooLarge(w, r)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}

		next.ServeHTTP(w, r)
	})
}
