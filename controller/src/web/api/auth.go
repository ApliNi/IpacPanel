package api

import (
	"errors"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"
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
	User         string    `json:"user"`
	Pass         string    `json:"pass"`
	PowTimestamp *int64    `json:"pow_timestamp"`
	PowNonces    *[]uint64 `json:"pow_nonces"`
}

func HandleApiAuthPow(w http.ResponseWriter, r *http.Request) {
	challenge := authz.GetLoginPowChallenge()
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
	var req loginRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	username := authz.NormalizeUsername(req.User)
	password := req.Pass
	if err := cfg.ValidateUserName(username); err != nil {
		writeUserNameValidationError(w, err)
		return
	}
	if err := cfg.ValidateUserPassword(password); err != nil {
		writeUserPasswordValidationError(w, err)
		return
	}
	if username == "" || strings.TrimSpace(password) == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameOrPasswordRequired, nil)
		return
	}
	if cfg.IsPowEnabled() {
		if req.PowTimestamp == nil || *req.PowTimestamp <= 0 || req.PowNonces == nil {
			loginFailureDelay()
			web.WriteAPIError(w, http.StatusBadRequest, msg.PoWParamsInvalid, nil)
			return
		}
	}
	var powTimestamp int64
	var powNonces []uint64
	if req.PowTimestamp != nil {
		powTimestamp = *req.PowTimestamp
	}
	if req.PowNonces != nil {
		powNonces = *req.PowNonces
	}
	if err := authz.ValidateLoginPow(username, password, powTimestamp, powNonces); err != nil {
		loginFailureDelay()
		switch err.Error() {
		case msg.PoWTimestampExpired:
			web.WriteAPIError(w, http.StatusBadRequest, msg.PoWTimestampExpired, err)
		case msg.PoWParamsInvalid:
			web.WriteAPIError(w, http.StatusBadRequest, msg.PoWParamsInvalid, err)
		case msg.PoWVerificationFailed:
			web.WriteAPIError(w, http.StatusBadRequest, msg.PoWVerificationFailed, err)
		default:
			web.WriteAPIError(w, http.StatusBadRequest, msg.PoWVerificationFailed, err)
		}
		return
	}

	cfg.ManagerMu.RLock()
	u, ok := authz.FindAuthUserLocked(username)
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

	token, err := authz.DefaultRuntime.Sessions.GetOrCreateUserToken(username)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateTokenFailed, err)
		return
	}
	csrfToken, err := authz.DefaultRuntime.Cookies.EnsureCSRFCookie(w, r)
	if err != nil || csrfToken == "" {
		if err == nil {
			err = errors.New("generated CSRF token is empty")
		}
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateCSRFTokenFailed, err)
		return
	}

	authz.DefaultRuntime.Cookies.WriteAuthCookie(w, r, token)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiAuthLogout(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	username := authedUser.User
	disconnectUserWS(username)
	authz.DefaultRuntime.Cookies.ClearAuthCookie(w, r)
	authz.DefaultRuntime.Cookies.ClearCSRFCookie(w, r)

	web.MarkRequestUser(w, username)
	web.WriteOK(w, map[string]bool{"ok": true})
}

func HandleApiAuthReset(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	username := authedUser.User
	_, err := authz.DefaultRuntime.Sessions.ResetUserToken(username, "")
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.GenerateTokenFailed, err)
		return
	}

	disconnectUserWS(username)
	authz.DefaultRuntime.Cookies.ClearAuthCookie(w, r)
	authz.DefaultRuntime.Cookies.ClearCSRFCookie(w, r)

	web.MarkRequestUser(w, username)
	web.WriteOK(w, map[string]bool{"ok": true})
}
