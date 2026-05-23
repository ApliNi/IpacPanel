package authz

import (
	"net/http"
	"strings"
)

const (
	DefaultAuthCookieName = "auth"
	DefaultCSRFCookieName = "csrf"
	DefaultCookieMaxAge   = 30 * 24 * 60 * 60
)

type CookieManager struct {
	AuthCookieName string
	CSRFCookieName string
	MaxAge         int
	OriginResolver *OriginResolver
}

func NewCookieManager(originResolver *OriginResolver) *CookieManager {
	return &CookieManager{AuthCookieName: DefaultAuthCookieName, CSRFCookieName: DefaultCSRFCookieName, MaxAge: DefaultCookieMaxAge, OriginResolver: originResolver}
}

func (m *CookieManager) AuthToken(r *http.Request) string {
	return m.cookieToken(r, m.authCookieName())
}

func (m *CookieManager) AuthTokenWithPresence(r *http.Request) (string, bool) {
	return m.cookieTokenWithPresence(r, m.authCookieName())
}

func (m *CookieManager) CSRFToken(r *http.Request) string {
	return m.cookieToken(r, m.csrfCookieName())
}

func (m *CookieManager) WriteAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	m.writeCookie(w, r, m.authCookieName(), token, true, m.maxAge())
}

func (m *CookieManager) WriteCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	// CSRF cookie must stay JS-readable so the frontend can mirror it into X-CSRF-Token.
	m.writeCookie(w, r, m.csrfCookieName(), token, false, m.maxAge())
}

func (m *CookieManager) ClearAuthCookie(w http.ResponseWriter, r *http.Request) {
	m.writeCookie(w, r, m.authCookieName(), "", true, -1)
}

func (m *CookieManager) ClearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	m.writeCookie(w, r, m.csrfCookieName(), "", false, -1)
}

func (m *CookieManager) EnsureCSRFCookie(w http.ResponseWriter, r *http.Request) (string, error) {
	token := m.CSRFToken(r)
	if token != "" {
		return token, nil
	}
	token, err := newRandomToken()
	if err != nil {
		return "", err
	}
	m.WriteCSRFCookie(w, r, token)
	return token, nil
}

func (m *CookieManager) cookieToken(r *http.Request, name string) string {
	token, _ := m.cookieTokenWithPresence(r, name)
	return token
}

func (m *CookieManager) cookieTokenWithPresence(r *http.Request, name string) (string, bool) {
	if r == nil {
		return "", false
	}
	cookie, err := r.Cookie(name)
	if err != nil || cookie == nil {
		return "", false
	}
	// An auth cookie that exists but has an empty value still provides auth credentials.
	// Keep presence true so downstream guards reject it as invalid instead of letting
	// dashboard events public fallback swallow the invalid login state.
	return strings.TrimSpace(cookie.Value), true
}

func (m *CookieManager) writeCookie(w http.ResponseWriter, r *http.Request, name string, value string, httpOnly bool, maxAge int) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: strings.TrimSpace(value), Path: "/", HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, Secure: m.secure(r), MaxAge: maxAge})
}

func (m *CookieManager) secure(r *http.Request) bool {
	resolver := m.OriginResolver
	if resolver == nil {
		resolver = NewOriginResolver()
	}
	return resolver.IsExternalHTTPS(r)
}

func (m *CookieManager) authCookieName() string {
	if strings.TrimSpace(m.AuthCookieName) == "" {
		return DefaultAuthCookieName
	}
	return strings.TrimSpace(m.AuthCookieName)
}

func (m *CookieManager) csrfCookieName() string {
	if strings.TrimSpace(m.CSRFCookieName) == "" {
		return DefaultCSRFCookieName
	}
	return strings.TrimSpace(m.CSRFCookieName)
}

func (m *CookieManager) maxAge() int {
	if m.MaxAge <= 0 {
		return DefaultCookieMaxAge
	}
	return m.MaxAge
}
