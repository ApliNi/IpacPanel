package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	cfg "IpacPanel/controller/src/config"

	"net/http"
	"strings"
)

func HandleApiUserGet(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	username := authedUser.User

	cfg.ManagerMu.RLock()
	u, ok := authz.FindAuthUserLocked(username)
	if !ok || u == nil || u.Perm == 0 {
		cfg.ManagerMu.RUnlock()
		web.WriteAPIError(w, http.StatusUnauthorized, msg.Unauthorized, nil)
		return
	}
	userCopy := *u
	insCnt, grpCnt := countUserScopeLocked(&userCopy)
	cfg.ManagerMu.RUnlock()

	resp := struct {
		User              string `json:"user"`
		Perm              int    `json:"perm"`
		AllowInstancesCnt int    `json:"allow_instances_cnt"`
		AllowGroupsCnt    int    `json:"allow_groups_cnt"`
	}{
		User:              userCopy.User,
		Perm:              userCopy.Perm,
		AllowInstancesCnt: insCnt,
		AllowGroupsCnt:    grpCnt,
	}

	web.WriteOK(w, resp)
}

type userUpdateRequest struct {
	Name string `json:"name"`
	Pass string `json:"pass"`
}

func findAuthUserIndex(users []cfg.AuthUser, username string) int {
	for i := range users {
		if users[i].User == username {
			return i
		}
	}
	return -1
}

func HandleApiUserUpdate(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	var req userUpdateRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	newNameRaw := strings.TrimSpace(req.Name)
	newPass := strings.TrimSpace(req.Pass)
	if err := cfg.ValidateUserPassword(newPass); err != nil {
		writeUserPasswordValidationError(w, err)
		return
	}
	if newNameRaw == "" && newPass == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.NoUserChangesProvided, nil)
		return
	}

	newName := ""
	if newNameRaw != "" {
		newName = authz.NormalizeUsername(newNameRaw)
		if newName == "" {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameInvalid, nil)
			return
		}
		if err := cfg.ValidateUserName(newName); err != nil {
			writeUserNameValidationError(w, err)
			return
		}
	}

	newPassHash := ""
	if newPass != "" {
		stored, err := cfg.HashPassword(newPass)
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.PasswordHashFailed, err)
			return
		}
		newPassHash = stored
	}

	username := authedUser.User

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()
	idx := findAuthUserIndex(savedCfg.Auth, username)
	if idx == -1 || savedCfg.Auth[idx].Perm == 0 {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusUnauthorized, msg.Unauthorized, nil)
		return
	}

	instances, totalInstances, totalGroups := collectInstanceScopeMetaLocked()
	u := &savedCfg.Auth[idx]
	oldName := u.User
	changed := false
	passwordChanged := newPassHash != ""
	if newName != "" && newName != u.User {
		for i := range savedCfg.Auth {
			if savedCfg.Auth[i].User == newName && savedCfg.Auth[i].User != oldName {
				cfg.ManagerMu.Unlock()
				web.WriteAPIError(w, http.StatusConflict, msg.UserNameDuplicate, nil)
				return
			}
		}
		u.User = newName
		changed = true
	}
	if newPassHash != "" {
		u.Pass = newPassHash
		changed = true
	}
	userCopy := *u
	cfg.ManagerMu.Unlock()
	if changed {
		plan := cfg.MutationPlan{NextCfg: savedCfg}
		plan.Publish = func() {
			cfg.ManagerMu.Lock()
			cfg.CurrentConfig = savedCfg
			cfg.ManagerMu.Unlock()
		}
		plan.AddPostCommit(msg.SyncAuthSessionState, func() error {
			if passwordChanged {
				authz.DefaultRuntime.Sessions.RemoveUserToken(oldName)
				if newName != "" && newName != oldName {
					authz.DefaultRuntime.Sessions.RemoveUserToken(newName)
				}
				authz.DefaultRuntime.Cookies.ClearAuthCookie(w, r)
				authz.DefaultRuntime.Cookies.ClearCSRFCookie(w, r)
				disconnectUserWS(oldName)
				if newName != "" && newName != oldName {
					disconnectUserWS(newName)
				}
			} else if newName != "" && newName != oldName {
				authz.DefaultRuntime.Sessions.RenameUserTokenOwner(oldName, newName)
			}
			return nil
		})
		if err := cfg.CommitMutationPlan(plan); err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
			return
		}
		result := cfg.RunMutationPostCommit(plan)
		if !result.RuntimeSynced {
			writeMutationRuntimeSyncError(w, http.StatusInternalServerError, msg.ConfigSavedRuntimeSyncFailed, result)
			return
		}
	}

	insCnt, grpCnt := countUserScopeWithInstances(&userCopy, instances, totalInstances, totalGroups)

	resp := struct {
		User              string `json:"user"`
		Perm              int    `json:"perm"`
		AllowInstancesCnt int    `json:"allow_instances_cnt"`
		AllowGroupsCnt    int    `json:"allow_groups_cnt"`
	}{
		User:              userCopy.User,
		Perm:              userCopy.Perm,
		AllowInstancesCnt: insCnt,
		AllowGroupsCnt:    grpCnt,
	}
	web.WriteOK(w, resp)
}

func HandleApiUserList(w http.ResponseWriter, r *http.Request) {
	type userItem struct {
		User              string `json:"user"`
		Perm              int    `json:"perm"`
		AllowInstancesCnt int    `json:"allow_instances_cnt"`
		AllowGroupsCnt    int    `json:"allow_groups_cnt"`
	}

	resp := struct {
		Users []userItem `json:"users"`
	}{
		Users: make([]userItem, 0),
	}

	cfg.ManagerMu.RLock()
	instances, totalInstances, totalGroups := collectInstanceScopeMetaLocked()

	for i := range cfg.CurrentConfig.Auth {
		u := strings.TrimSpace(cfg.CurrentConfig.Auth[i].User)
		if u == "" {
			continue
		}
		perm := cfg.CurrentConfig.Auth[i].Perm
		allowInstancesCnt, allowGroupsCnt := countUserScopeWithInstances(&cfg.CurrentConfig.Auth[i], instances, totalInstances, totalGroups)

		resp.Users = append(resp.Users, userItem{
			User:              u,
			Perm:              perm,
			AllowInstancesCnt: allowInstancesCnt,
			AllowGroupsCnt:    allowGroupsCnt,
		})
	}
	cfg.ManagerMu.RUnlock()

	web.WriteOK(w, resp)
}
