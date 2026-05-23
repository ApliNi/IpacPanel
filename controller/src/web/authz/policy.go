package authz

import (
	"IpacPanel/controller/src/msg"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// AuthMode declares the authentication requirement for a route.
type AuthMode string

const (
	AuthModeNone     AuthMode = "none"
	AuthModeOptional AuthMode = "optional"
	AuthModeUser     AuthMode = "user"
	AuthModeAdmin    AuthMode = "admin"
)

// CSRFMode declares how a route expects CSRF protection to be supplied.
type CSRFMode string

const (
	CSRFModeNone   CSRFMode = "none"
	CSRFModeHeader CSRFMode = "header"
	// CSRFModeHeaderWhenAuthenticated is only for optional-auth endpoints with a
	// public fallback. Requests without auth credentials skip CSRF so handlers can
	// serve public data; requests with valid credentials must pass header CSRF;
	// requests with invalid credentials are rejected before handlers run.
	CSRFModeHeaderWhenAuthenticated CSRFMode = "header_when_authenticated"
	CSRFModeWebSocketProtocol       CSRFMode = "websocket_protocol"
)

// OriginMode declares the origin validation model for a route.
type OriginMode string

const (
	OriginModeNone      OriginMode = "none"
	OriginModeSame      OriginMode = "same_origin"
	OriginModeWebSocket OriginMode = "websocket_same_origin"
)

// RouteKind describes transport/logging semantics for a route.
type RouteKind string

const (
	RouteKindAPI       RouteKind = "api"
	RouteKindSSE       RouteKind = "sse"
	RouteKindWebSocket RouteKind = "websocket"
)

// RoutePolicy is route-level authorization metadata. The API registry validates
// route kind constraints before handlers are exposed, and Guard executes the
// declared auth, method, CSRF, and origin policies for each request.
type RoutePolicy struct {
	Methods []string
	Auth    AuthMode
	CSRF    CSRFMode
	Origin  OriginMode
	Kind    RouteKind
}

func (p RoutePolicy) Validate() error {
	if err := validateRouteMethods(p.Methods); err != nil {
		return err
	}
	switch p.Auth {
	case AuthModeNone, AuthModeOptional, AuthModeUser, AuthModeAdmin:
	default:
		return fmt.Errorf("%s: %s", msg.RouteAuthPolicyInvalid, p.Auth)
	}
	switch p.CSRF {
	case CSRFModeNone, CSRFModeHeader, CSRFModeHeaderWhenAuthenticated, CSRFModeWebSocketProtocol:
	default:
		return fmt.Errorf("%s: %s", msg.RouteCSRFPolicyInvalid, p.CSRF)
	}
	switch p.Origin {
	case OriginModeNone, OriginModeSame, OriginModeWebSocket:
	default:
		return fmt.Errorf("%s: %s", msg.RouteOriginPolicyInvalid, p.Origin)
	}
	switch p.Kind {
	case RouteKindAPI, RouteKindSSE, RouteKindWebSocket:
	default:
		return fmt.Errorf("%s: %s", msg.RouteKindPolicyInvalid, p.Kind)
	}

	switch p.Kind {
	case RouteKindWebSocket:
		if len(p.Methods) != 1 || p.Methods[0] != http.MethodGet {
			return fmt.Errorf("%s: websocket route must only allow GET", msg.RouteKindPolicyInvalid)
		}
		if p.CSRF != CSRFModeWebSocketProtocol {
			return fmt.Errorf("%s: websocket route must use websocket CSRF", msg.RouteCSRFPolicyInvalid)
		}
		if p.Origin != OriginModeWebSocket {
			return fmt.Errorf("%s: websocket route must use websocket origin", msg.RouteOriginPolicyInvalid)
		}
	case RouteKindAPI, RouteKindSSE:
		if p.CSRF == CSRFModeWebSocketProtocol {
			return fmt.Errorf("%s: api/sse route cannot use websocket CSRF", msg.RouteCSRFPolicyInvalid)
		}
		if p.Origin == OriginModeWebSocket {
			return fmt.Errorf("%s: api/sse route cannot use websocket origin", msg.RouteOriginPolicyInvalid)
		}
	}
	return nil
}

func validateRouteMethods(methods []string) error {
	if len(methods) == 0 {
		return fmt.Errorf("%s: route must declare at least one method", msg.MethodNotAllowed)
	}
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		switch method {
		case http.MethodGet, http.MethodPost:
		default:
			return fmt.Errorf("%s: %s", msg.MethodNotAllowed, method)
		}
		if _, ok := seen[method]; ok {
			return fmt.Errorf("%s: duplicate method %s", msg.MethodNotAllowed, method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

type ErrorWriter func(w http.ResponseWriter, status int, message string, err error)

type UserMarker func(w http.ResponseWriter, username string)

type GuardConfig struct {
	Policy         RoutePolicy
	SessionStore   SessionStore
	CookieManager  *CookieManager
	CSRFValidator  *CSRFValidator
	OriginResolver *OriginResolver
	ErrorWriter    ErrorWriter
	UserMarker     UserMarker
}

func Wrap(next http.HandlerFunc, config GuardConfig) http.HandlerFunc {
	if next == nil {
		panic(msg.AuthzWrapNilHandler)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		guardedRequest, ok := Guard(w, r, config)
		if !ok {
			return
		}
		next(w, guardedRequest)
	}
}

func Guard(w http.ResponseWriter, r *http.Request, config GuardConfig) (*http.Request, bool) {
	policy := config.Policy
	principal, authenticated, credentialsProvided := requestPrincipal(r, config)
	switch policy.Auth {
	case AuthModeNone, AuthModeOptional:
	case AuthModeUser:
		if !authenticated {
			writeGuardError(config.ErrorWriter, w, http.StatusUnauthorized, msg.Unauthorized, nil)
			return nil, false
		}
	case AuthModeAdmin:
		if !authenticated {
			writeGuardError(config.ErrorWriter, w, http.StatusUnauthorized, msg.Unauthorized, nil)
			return nil, false
		}
		if principal.Role != UserRoleAdmin {
			writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.Forbidden, nil)
			return nil, false
		}
	default:
		writeGuardError(config.ErrorWriter, w, http.StatusInternalServerError, msg.RouteAuthPolicyInvalid, fmt.Errorf("invalid route auth policy: %s", policy.Auth))
		return nil, false
	}
	if policy.Auth == AuthModeOptional && policy.CSRF == CSRFModeHeaderWhenAuthenticated && credentialsProvided && !authenticated {
		writeGuardError(config.ErrorWriter, w, http.StatusUnauthorized, msg.Unauthorized, nil)
		return nil, false
	}
	if authenticated {
		markGuardUser(config.UserMarker, w, principal)
	}
	if len(policy.Methods) > 0 && !methodAllowed(r, policy.Methods) {
		writeGuardError(config.ErrorWriter, w, http.StatusMethodNotAllowed, msg.MethodNotAllowed, nil)
		return nil, false
	}

	guardedRequest, ok := validateCSRF(w, r, policy.CSRF, authenticated, config)
	if !ok {
		return nil, false
	}
	r = guardedRequest
	guardedRequest, ok = validateOrigin(w, r, policy.Origin, config)
	if !ok {
		return nil, false
	}
	r = guardedRequest
	if authenticated {
		r = RequestWithPrincipal(r, principal)
	}
	return r, true
}

func markGuardUser(marker UserMarker, w http.ResponseWriter, principal *Principal) {
	if marker == nil || principal == nil {
		return
	}
	marker(w, principal.Username)
}

func methodAllowed(r *http.Request, methods []string) bool {
	if r == nil {
		return false
	}
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	return false
}

func requestPrincipal(r *http.Request, config GuardConfig) (*Principal, bool, bool) {
	if r == nil {
		return nil, false, false
	}
	token, credentialsProvided := cookieManager(config).AuthTokenWithPresence(r)
	if !credentialsProvided {
		return nil, false, false
	}
	if config.SessionStore == nil {
		return nil, false, true
	}
	username, ok := config.SessionStore.ValidateBearerToken(token)
	if !ok {
		return nil, false, true
	}
	principal, ok := PrincipalFromUsername(username)
	return principal, ok, true
}

func validateOrigin(w http.ResponseWriter, r *http.Request, mode OriginMode, config GuardConfig) (*http.Request, bool) {
	switch mode {
	case OriginModeNone:
		return r, true
	case OriginModeSame:
		if originResolver(config).IsSameOriginRequest(r) {
			return r, true
		}
		writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.SameOriginValidationFailed, nil)
		return nil, false
	case OriginModeWebSocket:
		if originResolver(config).IsSameOriginRequest(r) {
			return RequestWithWebSocketOriginValidated(r), true
		}
		writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.SameOriginValidationFailed, nil)
		return nil, false
	default:
		writeGuardError(config.ErrorWriter, w, http.StatusInternalServerError, msg.RouteOriginPolicyInvalid, fmt.Errorf("invalid route origin policy: %s", mode))
		return nil, false
	}
}

func validateCSRF(w http.ResponseWriter, r *http.Request, mode CSRFMode, authenticated bool, config GuardConfig) (*http.Request, bool) {
	switch mode {
	case CSRFModeNone:
		return r, true
	case CSRFModeHeader:
		if csrfValidator(config).RequireFromRequest(w, r) == nil {
			return r, true
		}
		writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return nil, false
	case CSRFModeHeaderWhenAuthenticated:
		if !authenticated {
			return r, true
		}
		if csrfValidator(config).RequireFromRequest(w, r) == nil {
			return r, true
		}
		writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return nil, false
	case CSRFModeWebSocketProtocol:
		instance, token, selectedProtocol, err := csrfValidator(config).ParseTerminalWebSocketSubprotocolParams(r)
		if err != nil {
			log.Printf("route_kind=ws action=parse_subprotocol error=%q detail=%q", msg.CSRFValidationFailed, err.Error())
			writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.CSRFValidationFailed, err)
			return nil, false
		}
		if csrfValidator(config).ValidateWebSocketTokenExact(r, token) {
			return RequestWithTerminalWebSocketParams(r, TerminalWebSocketParams{Instance: instance, SelectedProtocol: selectedProtocol}), true
		}
		log.Printf("route_kind=ws action=validate_csrf error=%q", msg.CSRFValidationFailed)
		writeGuardError(config.ErrorWriter, w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return nil, false
	default:
		writeGuardError(config.ErrorWriter, w, http.StatusInternalServerError, msg.RouteCSRFPolicyInvalid, fmt.Errorf("invalid route CSRF policy: %s", mode))
		return nil, false
	}
}

func csrfValidator(config GuardConfig) *CSRFValidator {
	if config.CSRFValidator != nil {
		return config.CSRFValidator
	}
	return NewCSRFValidator(cookieManager(config))
}

func cookieManager(config GuardConfig) *CookieManager {
	if config.CookieManager != nil {
		return config.CookieManager
	}
	return NewCookieManager(originResolver(config))
}

func originResolver(config GuardConfig) *OriginResolver {
	if config.OriginResolver != nil {
		return config.OriginResolver
	}
	return NewOriginResolver()
}

func writeGuardError(errorWriter ErrorWriter, w http.ResponseWriter, status int, message string, err error) {
	if errorWriter != nil {
		errorWriter(w, status, message, err)
		return
	}
	http.Error(w, strings.TrimSpace(message), status)
	_ = err
}
