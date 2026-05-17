package web

import (
	"IpacPanel/controller/src/msg"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"
)

var (
	authMu      sync.RWMutex
	userToToken = make(map[string]string)
	tokenToUser = make(map[string]string)
)

const (
	authCookieName   = "auth"
	authCookieMaxAge = 30 * 24 * 60 * 60
	csrfCookieName   = "csrf"
	csrfHeaderName   = "X-CSRF-Token"
)

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

func FindAuthUserLocked(username string) (*cfg.AuthUser, bool) {
	for i := range cfg.CurrentConfig.Auth {
		if cfg.CurrentConfig.Auth[i].User == username {
			return &cfg.CurrentConfig.Auth[i], true
		}
	}
	return nil, false
}

func CanAccessInstance(user *cfg.AuthUser, instanceName string) bool {
	if user == nil || user.Perm == 0 {
		return false
	}
	if user.Perm == 7 {
		return true
	}
	if user.Perm != 2 {
		return false
	}

	name := strings.TrimSpace(instanceName)
	if name == "" {
		return false
	}
	for _, n := range user.AllowInstances {
		if strings.TrimSpace(n) == name {
			return true
		}
	}

	sp, ok := process.Get(name)
	if !ok || sp == nil {
		return false
	}

	group := strings.TrimSpace(sp.InstanceSnapshot().Group)
	if group == "" {
		group = "UNGROUPED"
	}
	for _, g := range user.AllowGroups {
		if strings.TrimSpace(g) == group {
			return true
		}
	}
	return false
}

func GetAuthedUserFromRequest(r *http.Request) (*cfg.AuthUser, bool) {
	token := getAuthTokenFromCookie(r)
	username, ok := ValidateBearerToken(token)
	if !ok {
		return nil, false
	}
	cfg.ManagerMu.RLock()
	u, ok := FindAuthUserLocked(username)
	cfg.ManagerMu.RUnlock()
	if !ok || u == nil || u.Perm == 0 {
		return nil, false
	}
	return u, true
}

func getAuthTokenFromCookie(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(authCookieName)
	if err != nil || cookie == nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func getCSRFCookieToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie == nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func newRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func shouldUseSecureCookie(r *http.Request) bool {
	// Cookie security must follow the external scheme seen by the browser.
	// This keeps all direct/proxy HTTP and HTTPS combinations consistent.
	origin, err := requestEffectiveOrigin(r)
	if err == nil {
		return origin.Scheme == "https"
	}
	return r != nil && r.TLS != nil
}

func WriteAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    strings.TrimSpace(token),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldUseSecureCookie(r),
		MaxAge:   authCookieMaxAge,
	})
}

func WriteCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	if w == nil {
		return
	}
	// CSRF cookie must remain readable by JS so the frontend can mirror it into X-CSRF-Token.
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    strings.TrimSpace(token),
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldUseSecureCookie(r),
		MaxAge:   authCookieMaxAge,
	})
}

func ClearAuthCookie(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldUseSecureCookie(r),
		MaxAge:   -1,
	})
}

func ClearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	// Keep logout deletion attributes aligned with WriteCSRFCookie.
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldUseSecureCookie(r),
		MaxAge:   -1,
	})
}

func EnsureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	token := getCSRFCookieToken(r)
	if token != "" {
		return token
	}
	token, err := newRandomToken()
	if err != nil {
		return ""
	}
	WriteCSRFCookie(w, r, token)
	return token
}

func GetOrCreateUserToken(username string) (string, error) {
	authMu.Lock()
	defer authMu.Unlock()
	if t, ok := userToToken[username]; ok && t != "" {
		return t, nil
	}
	t, err := newRandomToken()
	if err != nil {
		return "", err
	}
	userToToken[username] = t
	tokenToUser[t] = username
	return t, nil
}

func ResetUserToken(username string, oldToken string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf(msg.InvalidTokenLength)
	}
	newToken, err := newRandomToken()
	if err != nil {
		return "", err
	}

	authMu.Lock()
	defer authMu.Unlock()
	if strings.TrimSpace(oldToken) != "" {
		delete(tokenToUser, oldToken)
	}
	if currentToken, ok := userToToken[username]; ok && strings.TrimSpace(currentToken) != "" {
		delete(tokenToUser, currentToken)
	}
	userToToken[username] = newToken
	tokenToUser[newToken] = username
	return newToken, nil
}

func RenameUserTokenOwner(oldUsername string, newUsername string) {
	oldUsername = strings.TrimSpace(oldUsername)
	newUsername = strings.TrimSpace(newUsername)
	if oldUsername == "" || newUsername == "" || oldUsername == newUsername {
		return
	}

	authMu.Lock()
	defer authMu.Unlock()
	token, ok := userToToken[oldUsername]
	if !ok || strings.TrimSpace(token) == "" {
		return
	}
	delete(userToToken, oldUsername)
	userToToken[newUsername] = token
	tokenToUser[token] = newUsername
}

func RemoveUserToken(username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}

	authMu.Lock()
	defer authMu.Unlock()
	token, ok := userToToken[username]
	if !ok || strings.TrimSpace(token) == "" {
		return
	}
	delete(userToToken, username)
	delete(tokenToUser, token)
}

func ValidateBearerToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	authMu.RLock()
	defer authMu.RUnlock()
	u, ok := tokenToUser[token]
	if !ok || strings.TrimSpace(u) == "" {
		return "", false
	}
	return u, true
}

func DisconnectUserWs(username string) {
	user := strings.TrimSpace(username)
	if user == "" {
		return
	}

	processes := process.List()

	for _, sp := range processes {
		if sp == nil {
			continue
		}
		toClose := make([]*process.WSClient, 0)
		sp.Mu.Lock()
		for client := range sp.Clients {
			if client == nil || client.User != user {
				continue
			}
			delete(sp.Clients, client)
			toClose = append(toClose, client)
		}
		sp.Mu.Unlock()

		for _, client := range toClose {
			_ = client.Close()
		}
	}
}
