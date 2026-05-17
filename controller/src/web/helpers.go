package web

import (
	"IpacPanel/controller/src/msg"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"
)

const defaultJSONBodyLimit int64 = 1 << 20

type DecodeJSONBodyOption func(*decodeJSONBodyConfig)

type decodeJSONBodyConfig struct {
	bodyLimit int64
}

type GuardOptions struct {
	Methods             []string
	RequireAuth         bool
	CSRFFromRequest     bool
	CSRFFromQuery       bool
	RequireAdmin        bool
	ForbiddenMessage    string
	InstanceFromQuery   bool
	WSInstanceFromQuery bool
}

type GuardResult struct {
	User         *cfg.AuthUser
	Instance     *process.InstanceProcess
	InstanceName string
}

type instanceQueryOptions struct {
	trimName          bool
	requireProcess    bool
	missingAsNotFound bool
}

func WithJSONBodyLimit(limit int64) DecodeJSONBodyOption {
	return func(cfg *decodeJSONBodyConfig) {
		if cfg == nil || limit <= 0 {
			return
		}
		cfg.bodyLimit = limit
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	WriteAPIError(w, http.StatusUnauthorized, msg.Unauthorized, nil)
}

func writeInstanceNotFound(w http.ResponseWriter) {
	WriteAPIError(w, http.StatusNotFound, "实例不存在", nil)
}

func WriteUnauthorized(w http.ResponseWriter) {
	writeUnauthorized(w)
}

func WriteInstanceNameRequired(w http.ResponseWriter) {
	WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameMissing, nil)
}

func RequireAuthedUser(w http.ResponseWriter, r *http.Request) (*cfg.AuthUser, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{RequireAuth: true})
	if !ok {
		return nil, false
	}
	return guard.User, true
}

func RequireAuthedUserWithMethod(w http.ResponseWriter, r *http.Request, methods ...string) (*cfg.AuthUser, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{
		RequireAuth:     true,
		Methods:         methods,
		CSRFFromRequest: true,
	})
	if !ok {
		return nil, false
	}
	return guard.User, true
}

func RequireAuthedAccessibleInstanceName(w http.ResponseWriter, r *http.Request, methods ...string) (*cfg.AuthUser, string, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{
		RequireAuth:     true,
		Methods:         methods,
		CSRFFromRequest: true,
	})
	if !ok {
		return nil, "", false
	}
	name, ok := RequireAccessibleInstanceNameFromQuery(w, r, guard.User)
	if !ok {
		return nil, "", false
	}
	return guard.User, name, true
}

func RequireAuthedAccessibleInstanceProcess(w http.ResponseWriter, r *http.Request, methods ...string) (*cfg.AuthUser, *process.InstanceProcess, string, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{
		RequireAuth:     true,
		Methods:         methods,
		CSRFFromRequest: true,
	})
	if !ok {
		return nil, nil, "", false
	}
	sp, name, ok := RequireInstanceProcessFromQuery(w, r, guard.User)
	if !ok {
		return nil, nil, "", false
	}
	return guard.User, sp, name, true
}

func RequireAuthedUserWithQueryCSRF(w http.ResponseWriter, r *http.Request, methods ...string) (*cfg.AuthUser, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{
		RequireAuth:   true,
		Methods:       methods,
		CSRFFromQuery: true,
	})
	if !ok {
		return nil, false
	}
	return guard.User, true
}

func RequireAuthedUserWithInstanceFromQuery(w http.ResponseWriter, r *http.Request, methods ...string) (*cfg.AuthUser, *process.InstanceProcess, bool) {
	guard, sp, _, ok := RequireAuthedAccessibleInstanceProcess(w, r, methods...)
	if !ok {
		return nil, nil, false
	}
	return guard, sp, true
}

func RequireAdminAuthedUserWithMethod(w http.ResponseWriter, r *http.Request, forbiddenMessage string, methods ...string) (*cfg.AuthUser, bool) {
	guard, ok := GuardRequest(w, r, GuardOptions{
		RequireAuth:      true,
		Methods:          methods,
		CSRFFromRequest:  true,
		RequireAdmin:     true,
		ForbiddenMessage: forbiddenMessage,
	})
	if !ok {
		return nil, false
	}
	return guard.User, true
}

func GuardRequest(w http.ResponseWriter, r *http.Request, opts GuardOptions) (*GuardResult, bool) {
	result := &GuardResult{}
	if opts.RequireAuth {
		u, ok := GetAuthedUserFromRequest(r)
		if !ok {
			writeUnauthorized(w)
			return nil, false
		}
		MarkRequestUser(w, u.User)
		result.User = u
	}
	if len(opts.Methods) > 0 && !RequireMethod(w, r, opts.Methods...) {
		return nil, false
	}
	if opts.CSRFFromRequest && !RequireCSRFFromRequest(w, r) {
		return nil, false
	}
	if opts.CSRFFromQuery && !RequireCSRFFromQuery(w, r) {
		return nil, false
	}
	if opts.RequireAdmin && !RequireAdmin(w, result.User, opts.ForbiddenMessage) {
		return nil, false
	}
	if opts.InstanceFromQuery {
		sp, name, ok := RequireInstanceProcessFromQuery(w, r, result.User)
		if !ok {
			return nil, false
		}
		result.Instance = sp
		result.InstanceName = name
	}
	if opts.WSInstanceFromQuery {
		sp, name, ok := RequireWsInstanceProcessFromQuery(w, r, result.User)
		if !ok {
			return nil, false
		}
		result.Instance = sp
		result.InstanceName = name
	}
	return result, true
}

func RequireAdmin(w http.ResponseWriter, user *cfg.AuthUser, forbiddenMessage string) bool {
	if user == nil || user.Perm != 7 {
		if strings.TrimSpace(forbiddenMessage) == "" {
			forbiddenMessage = msg.Forbidden
		}
		WriteAPIError(w, http.StatusForbidden, forbiddenMessage, nil)
		return false
	}
	return true
}

func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}, options ...DecodeJSONBodyOption) bool {
	if dst == nil {
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, nil)
		return false
	}
	if r == nil || r.Body == nil {
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, nil)
		return false
	}
	config := decodeJSONBodyConfig{bodyLimit: defaultJSONBodyLimit}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if config.bodyLimit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, config.bodyLimit)
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, err)
		return false
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		WriteAPIError(w, http.StatusBadRequest, msg.RequestBodyHasTrailingData, nil)
		return false
	}
	return true
}

func RequireInstanceProcessFromQuery(w http.ResponseWriter, r *http.Request, authedUser *cfg.AuthUser) (*process.InstanceProcess, string, bool) {
	return requireInstanceFromQuery(w, r, authedUser, instanceQueryOptions{
		trimName:          true,
		requireProcess:    true,
		missingAsNotFound: false,
	})
}

func RequireAccessibleInstanceNameFromQuery(w http.ResponseWriter, r *http.Request, authedUser *cfg.AuthUser) (string, bool) {
	_, name, ok := requireInstanceFromQuery(w, r, authedUser, instanceQueryOptions{
		trimName:          true,
		requireProcess:    false,
		missingAsNotFound: false,
	})
	if !ok {
		return "", false
	}
	return name, true
}

func RequireInstanceProcessByName(w http.ResponseWriter, authedUser *cfg.AuthUser, name string) (*process.InstanceProcess, bool) {
	if strings.TrimSpace(name) == "" {
		WriteInstanceNameRequired(w)
		return nil, false
	}
	if !CanAccessInstance(authedUser, name) {
		writeInstanceNotFound(w)
		return nil, false
	}
	sp, ok := process.Get(name)
	if !ok {
		writeInstanceNotFound(w)
		return nil, false
	}
	if sp.IsDeleting() {
		writeInstanceNotFound(w)
		return nil, false
	}
	MarkRequestInstance(w, name)
	return sp, true
}

func RequireWsInstanceProcessFromQuery(w http.ResponseWriter, r *http.Request, authedUser *cfg.AuthUser) (*process.InstanceProcess, string, bool) {
	return requireInstanceFromQuery(w, r, authedUser, instanceQueryOptions{
		trimName:          false,
		requireProcess:    true,
		missingAsNotFound: true,
	})
}

func requireInstanceFromQuery(w http.ResponseWriter, r *http.Request, authedUser *cfg.AuthUser, opts instanceQueryOptions) (*process.InstanceProcess, string, bool) {
	if r == nil || r.URL == nil {
		if opts.missingAsNotFound {
			writeInstanceNotFound(w)
		} else {
			WriteInstanceNameRequired(w)
		}
		return nil, "", false
	}
	name := r.URL.Query().Get("name")
	if opts.trimName {
		name = strings.TrimSpace(name)
	}
	if name == "" {
		if opts.missingAsNotFound {
			writeInstanceNotFound(w)
		} else {
			WriteInstanceNameRequired(w)
		}
		return nil, "", false
	}
	if !CanAccessInstance(authedUser, name) {
		writeInstanceNotFound(w)
		return nil, "", false
	}
	MarkRequestInstance(w, name)
	if !opts.requireProcess {
		return nil, name, true
	}
	sp, ok := process.Get(name)
	if !ok {
		writeInstanceNotFound(w)
		return nil, "", false
	}
	if sp.IsDeleting() {
		writeInstanceNotFound(w)
		return nil, "", false
	}
	return sp, name, true
}

type instanceControlRequest struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

func ParseInstanceControlParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	if name != "" && action != "" {
		return name, action, true
	}
	var req instanceControlRequest
	if !DecodeJSONBody(w, r, &req) {
		return "", "", false
	}
	if name == "" {
		name = strings.TrimSpace(req.Name)
	}
	if action == "" {
		action = strings.TrimSpace(req.Action)
	}
	if name == "" {
		WriteInstanceNameRequired(w)
		return "", "", false
	}
	if action == "" {
		WriteAPIError(w, http.StatusBadRequest, msg.ActionRequired, nil)
		return "", "", false
	}
	return name, action, true
}

func ParseInstanceNameFromQueryOrBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name != "" {
		return name, true
	}
	var req struct {
		Name string `json:"name"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return "", false
	}
	name = strings.TrimSpace(req.Name)
	if name == "" {
		WriteInstanceNameRequired(w)
		return "", false
	}
	return name, true
}

func RequireMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	WriteAPIError(w, http.StatusMethodNotAllowed, msg.MethodNotAllowed, nil)
	return false
}

func RequireCSRFFromRequest(w http.ResponseWriter, r *http.Request) bool {
	if r == nil {
		WriteAPIError(w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		EnsureCSRFCookie(w, r)
		return true
	}
	cookieToken := getCSRFCookieToken(r)
	headerToken := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if cookieToken == "" || headerToken == "" || cookieToken != headerToken {
		WriteAPIError(w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return false
	}
	return true
}

func RequireCSRFFromQuery(w http.ResponseWriter, r *http.Request) bool {
	if ValidateCSRFFromQuery(r) {
		return true
	}
	WriteAPIError(w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
	return false
}

func ValidateCSRFFromQuery(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	cookieToken := getCSRFCookieToken(r)
	queryToken := strings.TrimSpace(r.URL.Query().Get("csrf"))
	return cookieToken != "" && queryToken != "" && cookieToken == queryToken
}
