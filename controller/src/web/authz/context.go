package authz

import (
	"context"
	"net/http"
	"strings"
)

type principalContextKey struct{}
type terminalWebSocketParamsContextKey struct{}
type webSocketOriginValidatedContextKey struct{}

type TerminalWebSocketParams struct {
	Instance         string
	SelectedProtocol string
}

func RequestWithPrincipal(r *http.Request, principal *Principal) *http.Request {
	if r == nil || principal == nil {
		return r
	}
	return r.WithContext(ContextWithPrincipal(r.Context(), principal))
}

func ContextWithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if principal == nil {
		return ctx
	}
	copyPrincipal := *principal
	copyPrincipal.Username = strings.TrimSpace(copyPrincipal.Username)
	copyPrincipal.AllowInstances = append([]string(nil), principal.AllowInstances...)
	copyPrincipal.AllowGroups = append([]string(nil), principal.AllowGroups...)
	return context.WithValue(ctx, principalContextKey{}, &copyPrincipal)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(*Principal)
	if !ok || principal == nil || strings.TrimSpace(principal.Username) == "" || principal.Role == UserRoleNone {
		return nil, false
	}
	copyPrincipal := *principal
	copyPrincipal.AllowInstances = append([]string(nil), principal.AllowInstances...)
	copyPrincipal.AllowGroups = append([]string(nil), principal.AllowGroups...)
	return &copyPrincipal, true
}

func PrincipalFromRequest(r *http.Request) (*Principal, bool) {
	if r == nil {
		return nil, false
	}
	return PrincipalFromContext(r.Context())
}

func UsernameFromRequest(r *http.Request) (string, bool) {
	principal, ok := PrincipalFromRequest(r)
	if !ok {
		return "", false
	}
	username := strings.TrimSpace(principal.Username)
	return username, username != ""
}

func RequestWithTerminalWebSocketParams(r *http.Request, params TerminalWebSocketParams) *http.Request {
	if r == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), terminalWebSocketParamsContextKey{}, params))
}

func TerminalWebSocketParamsFromRequest(r *http.Request) (TerminalWebSocketParams, bool) {
	if r == nil {
		return TerminalWebSocketParams{}, false
	}
	params, ok := r.Context().Value(terminalWebSocketParamsContextKey{}).(TerminalWebSocketParams)
	if !ok {
		return TerminalWebSocketParams{}, false
	}
	if params.Instance == "" || params.SelectedProtocol == "" {
		return TerminalWebSocketParams{}, false
	}
	return params, true
}

func RequestWithWebSocketOriginValidated(r *http.Request) *http.Request {
	if r == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), webSocketOriginValidatedContextKey{}, true))
}

func WebSocketOriginValidated(r *http.Request) bool {
	if r == nil {
		return false
	}
	validated, ok := r.Context().Value(webSocketOriginValidatedContextKey{}).(bool)
	return ok && validated
}
