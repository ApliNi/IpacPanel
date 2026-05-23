package web

import (
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"
	"encoding/json"
	"errors"
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
	WriteAPIError(w, http.StatusNotFound, msg.InstanceNotFound, nil)
}

func WriteUnauthorized(w http.ResponseWriter) {
	writeUnauthorized(w)
}

func WriteInstanceNameRequired(w http.ResponseWriter) {
	WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameMissing, nil)
}

func RequireAccessibleInstanceNameByName(w http.ResponseWriter, authedUser *cfg.AuthUser, instance string) (string, bool) {
	name := strings.TrimSpace(instance)
	if name == "" {
		WriteInstanceNameRequired(w)
		return "", false
	}
	principal, _ := authz.PrincipalFromAuthUser(authedUser)
	accessibleName, err := authz.DefaultRuntime.Authorizer.RequireAccessibleInstanceName(principal, name)
	if err != nil {
		writeInstanceNotFound(w)
		return "", false
	}
	MarkRequestInstance(w, accessibleName)
	return accessibleName, true
}

func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}, options ...DecodeJSONBodyOption) bool {
	if dst == nil {
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, errors.New("json decode destination is nil"))
		return false
	}
	if r == nil || r.Body == nil {
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, errors.New("request body is nil"))
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
		if err == nil {
			err = errors.New("request body contains multiple JSON values")
		}
		WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, err)
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

func RequireInstanceProcessByName(w http.ResponseWriter, authedUser *cfg.AuthUser, name string) (*process.InstanceProcess, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		WriteInstanceNameRequired(w)
		return nil, false
	}
	principal, _ := authz.PrincipalFromAuthUser(authedUser)
	sp, err := authz.DefaultRuntime.Authorizer.RequireInstanceProcess(principal, name)
	if err != nil {
		writeInstanceNotFound(w)
		return nil, false
	}
	MarkRequestInstance(w, name)
	return sp, true
}

func RequireInstanceProcessByExactName(w http.ResponseWriter, authedUser *cfg.AuthUser, name string) (*process.InstanceProcess, bool) {
	if name == "" {
		WriteInstanceNameRequired(w)
		return nil, false
	}
	principal, _ := authz.PrincipalFromAuthUser(authedUser)
	sp, err := authz.DefaultRuntime.Authorizer.RequireInstanceProcessExact(principal, name)
	if err != nil {
		writeInstanceNotFound(w)
		return nil, false
	}
	MarkRequestInstance(w, name)
	return sp, true
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
	name := r.URL.Query().Get("instance")
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
	principal, _ := authz.PrincipalFromAuthUser(authedUser)
	if !opts.requireProcess {
		accessibleName, err := authz.DefaultRuntime.Authorizer.RequireAccessibleInstanceName(principal, name)
		if err != nil {
			writeInstanceNotFound(w)
			return nil, "", false
		}
		MarkRequestInstance(w, accessibleName)
		return nil, accessibleName, true
	}
	sp, err := authz.DefaultRuntime.Authorizer.RequireInstanceProcess(principal, name)
	if err != nil {
		writeInstanceNotFound(w)
		return nil, "", false
	}
	MarkRequestInstance(w, name)
	return sp, name, true
}

type instanceControlRequest struct {
	Instance string `json:"instance"`
	Action   string `json:"action"`
}

func ParseInstanceControlParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var req instanceControlRequest
	if !DecodeJSONBody(w, r, &req) {
		return "", "", false
	}
	name := strings.TrimSpace(req.Instance)
	action := strings.TrimSpace(req.Action)
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

func RequireCSRFFromRequest(w http.ResponseWriter, r *http.Request) bool {
	if err := authz.DefaultRuntime.CSRF.RequireFromRequest(w, r); err != nil {
		WriteAPIError(w, http.StatusForbidden, msg.CSRFValidationFailed, nil)
		return false
	}
	return true
}
