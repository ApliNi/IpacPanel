package api

import (
	"IpacPanel/controller/src/msg"
	"math/rand"
	"net/http"
	"strings"
	"time"

	cfg "IpacPanel/controller/src/config"
	web "IpacPanel/controller/src/web"
)

var loginDelayRng = rand.New(rand.NewSource(time.Now().UnixNano()))

// loginFailureDelay sleeps a random duration between 20-100ms before returning
// login failures, preventing timing attacks that could distinguish valid
// usernames, password correctness, or PoW validity by response latency.
func loginFailureDelay() {
	ms := 20 + loginDelayRng.Intn(81) // [20, 100]
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

type loginRequest struct {
	User         string   `json:"user"`
	Pass         string   `json:"pass"`
	PowTimestamp int64    `json:"pow_timestamp"`
	PowNonces    []uint64 `json:"pow_nonces"`
}

func HandleApiAuthPow(w http.ResponseWriter, r *http.Request) {
	if !web.RequireMethod(w, r, http.MethodGet) {
		return
	}

	challenge := web.GetLoginPowChallenge()
	if challenge == nil {
		web.WriteOK(w, map[string]any{"enabled": false})
		return
	}
	web.WriteOK(w, map[string]any{
		"enabled":   true,
		"salt":      challenge.Salt,
		"timestamp": challenge.Timestamp,
		"k":         challenge.K,
		"d":         challenge.D,
	})
}

func HandleApiAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !web.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var req loginRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	username := web.NormalizeUsername(req.User)
	password := req.Pass
	if err := cfg.ValidateUserName(username); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := cfg.ValidateUserPassword(password); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if username == "" || strings.TrimSpace(password) == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameOrPasswordRequired, nil)
		return
	}
	if err := web.ValidateLoginPow(username, password, req.PowTimestamp, req.PowNonces); err != nil {
		loginFailureDelay()
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	cfg.ManagerMu.RLock()
	u, ok := web.FindAuthUserLocked(username)
	cfg.ManagerMu.RUnlock()
	if !ok {
		loginFailureDelay()
		web.WriteAPIError(w, http.StatusUnauthorized, msg.InvalidUsernameOrPassword, nil)
		return
	}
	if !cfg.CheckPassword(u.Pass, password) {
		loginFailureDelay()
		web.WriteAPIError(w, http.StatusUnauthorized, msg.InvalidUsernameOrPassword, nil)
		return
	}
	if u.Perm == 0 {
		loginFailureDelay()
		web.WriteAPIError(w, http.StatusUnauthorized, msg.UserDisabled, nil)
		return
	}
	web.MarkRequestUser(w, username)

	token, err := web.GetOrCreateUserToken(username)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateTokenFailed, err)
		return
	}
	csrfToken := web.EnsureCSRFCookie(w, r)
	if csrfToken == "" {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateCSRFTokenFailed, nil)
		return
	}

	web.WriteAuthCookie(w, r, token)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiAuthLogout(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:     true,
		Methods:         []string{http.MethodPost},
		CSRFFromRequest: true,
	})
	if !ok {
		return
	}

	username := guard.User.User
	web.DisconnectUserWs(username)
	web.ClearAuthCookie(w, r)
	web.ClearCSRFCookie(w, r)

	web.MarkRequestUser(w, username)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiAuthReset(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:     true,
		Methods:         []string{http.MethodPost},
		CSRFFromRequest: true,
	})
	if !ok {
		return
	}
	authedUser := guard.User

	username := authedUser.User
	_, err := web.ResetUserToken(username, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateTokenFailed, err)
		return
	}

	web.DisconnectUserWs(username)
	web.ClearAuthCookie(w, r)
	web.ClearCSRFCookie(w, r)

	web.MarkRequestUser(w, username)
	web.WriteOK(w, map[string]bool{"ok": true})
}
