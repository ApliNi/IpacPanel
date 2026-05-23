package authz

import (
	"net/http"
	"strings"
	"unicode"

	cfg "IpacPanel/controller/src/config"
)

type Runtime struct {
	Sessions   SessionStore
	Origins    *OriginResolver
	Cookies    *CookieManager
	CSRF       *CSRFValidator
	Authorizer *Authorizer
}

var DefaultRuntime = NewRuntime()

func NewRuntime() *Runtime {
	origins := NewOriginResolver()
	cookies := NewCookieManager(origins)
	return &Runtime{
		Sessions:   NewMemorySessionStore(),
		Origins:    origins,
		Cookies:    cookies,
		CSRF:       NewCSRFValidator(cookies),
		Authorizer: NewAuthorizer(),
	}
}

func (rt *Runtime) GuardConfig(policy RoutePolicy, errorWriter ErrorWriter, userMarker UserMarker) GuardConfig {
	runtime := rt.runtime()
	return GuardConfig{
		Policy:         policy,
		SessionStore:   runtime.Sessions,
		CookieManager:  runtime.Cookies,
		CSRFValidator:  runtime.CSRF,
		OriginResolver: runtime.Origins,
		ErrorWriter:    errorWriter,
		UserMarker:     userMarker,
	}
}

func (rt *Runtime) CurrentAuthUser(r *http.Request) (*cfg.AuthUser, bool) {
	username, ok := UsernameFromRequest(r)
	if !ok {
		return nil, false
	}
	return AuthUserByUsername(username)
}

func (rt *Runtime) CanAccessInstance(user *cfg.AuthUser, instanceName string) bool {
	principal, ok := PrincipalFromAuthUser(user)
	if !ok {
		return false
	}
	return rt.runtime().Authorizer.CanAccessInstance(principal, instanceName)
}

func (rt *Runtime) runtime() *Runtime {
	if rt == nil {
		return DefaultRuntime
	}
	return rt
}

func AuthUserByUsername(username string) (*cfg.AuthUser, bool) {
	cfg.ManagerMu.RLock()
	user, ok := FindAuthUserLocked(username)
	cfg.ManagerMu.RUnlock()
	if !ok || user == nil || user.Perm == 0 {
		return nil, false
	}
	return user, true
}

func FindAuthUserLocked(username string) (*cfg.AuthUser, bool) {
	for i := range cfg.CurrentConfig.Auth {
		if cfg.CurrentConfig.Auth[i].User == username {
			return &cfg.CurrentConfig.Auth[i], true
		}
	}
	return nil, false
}

func NormalizeUsername(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.IndexFunc(u, unicode.IsSpace) >= 0 {
		return ""
	}
	return u
}
