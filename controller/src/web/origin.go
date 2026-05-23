package web

import (
	"IpacPanel/controller/src/web/authz"
	"net/http"
)

// IsExternalHTTPS reports whether the browser-facing request scheme is HTTPS.
// It follows trusted proxy headers so redirect and cookie decisions stay aligned.
func IsExternalHTTPS(r *http.Request) bool {
	return authz.DefaultRuntime.Origins.IsExternalHTTPS(r)
}

func IsSameOriginRequest(r *http.Request) bool {
	return authz.DefaultRuntime.Origins.IsSameOriginRequest(r)
}
