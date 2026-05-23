package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	cfg "IpacPanel/controller/src/config"

	"net/http"
	"strings"
)

func HandleApiAdminGet(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("user"))
	if username == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UserParamMissing, nil)
		return
	}

	cfg.ManagerMu.RLock()
	u, ok := authz.FindAuthUserLocked(username)
	if !ok || u == nil {
		cfg.ManagerMu.RUnlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.UserNotFound, nil)
		return
	}
	copyUser := *u
	cfg.ManagerMu.RUnlock()

	resp := struct {
		User           string   `json:"user"`
		Perm           int      `json:"perm"`
		AllowInstances []string `json:"allow_instances"`
		AllowGroups    []string `json:"allow_groups"`
	}{
		User:           copyUser.User,
		Perm:           copyUser.Perm,
		AllowInstances: append([]string(nil), copyUser.AllowInstances...),
		AllowGroups:    append([]string(nil), copyUser.AllowGroups...),
	}

	web.WriteOK(w, resp)
}

type adminUserUpdateRequest struct {
	User           string    `json:"user"`
	NewUser        string    `json:"new_user"`
	Pass           string    `json:"pass"`
	Perm           *int      `json:"perm"`
	AllowInstances *[]string `json:"allow_instances"`
	AllowGroups    *[]string `json:"allow_groups"`
}

func countAdminUsers(users []cfg.AuthUser, skipIndex int) int {
	admins := 0
	for i := range users {
		if i == skipIndex {
			continue
		}
		if users[i].Perm == 7 {
			admins += 1
		}
	}
	return admins
}

func buildInstanceAndGroupSet(instances []cfg.Instance) (map[string]struct{}, map[string]struct{}) {
	instanceSet := make(map[string]struct{})
	groupSet := make(map[string]struct{})
	groupSet["UNGROUPED"] = struct{}{}
	for i := range instances {
		name := strings.TrimSpace(instances[i].Name)
		if name != "" {
			instanceSet[name] = struct{}{}
		}
		grp := strings.TrimSpace(instances[i].Group)
		if grp == "" {
			continue
		}
		groupSet[grp] = struct{}{}
	}
	return instanceSet, groupSet
}

func filterExisting(values []string, allowSet map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for i := range values {
		v := strings.TrimSpace(values[i])
		if v == "" {
			continue
		}
		if _, ok := allowSet[v]; ok {
			out = append(out, v)
		}
	}
	return cleanStringList(out)
}

func filterExistingInstances(values []string, allowSet map[string]struct{}) []string {
	return filterExisting(cfg.NormalizeUserScopeInstances(values), allowSet)
}

func filterExistingGroups(values []string, allowSet map[string]struct{}) []string {
	return filterExisting(cfg.NormalizeUserScopeGroups(values), allowSet)
}

func HandleApiAdminUpdate(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	web.MarkRequestUser(w, authedUser.User)

	var req adminUserUpdateRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	oldName := strings.TrimSpace(req.User)
	if oldName == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UserParamMissing, nil)
		return
	}

	newName := ""
	if strings.TrimSpace(req.NewUser) != "" {
		newName = authz.NormalizeUsername(req.NewUser)
		if newName == "" {
			web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameInvalid, nil)
			return
		}
		if err := cfg.ValidateUserName(newName); err != nil {
			writeUserNameValidationError(w, err)
			return
		}
	}
	newPass := strings.TrimSpace(req.Pass)
	if err := cfg.ValidateUserPassword(newPass); err != nil {
		writeUserPasswordValidationError(w, err)
		return
	}
	newPerm := req.Perm

	newPassHash := ""
	if newPass != "" {
		stored, err := cfg.HashPassword(newPass)
		if err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.PasswordHashFailed, err)
			return
		}
		newPassHash = stored
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()
	instanceSet, groupSet := buildInstanceAndGroupSet(savedCfg.Instances)
	idx := findAuthUserIndex(savedCfg.Auth, oldName)
	if idx == -1 {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.UserNotFound, nil)
		return
	}

	u := &savedCfg.Auth[idx]
	changed := false
	finalName := u.User
	passwordChanged := newPassHash != ""
	if newName != "" && newName != u.User {
		for i := range savedCfg.Auth {
			if savedCfg.Auth[i].User == newName && savedCfg.Auth[i].User != oldName {
				cfg.ManagerMu.Unlock()
				web.WriteAPIError(w, http.StatusConflict, msg.UserNameDuplicate, nil)
				return
			}
		}
		finalName = newName
		u.User = newName
		changed = true
	}
	if newPerm != nil {
		if *newPerm != 0 && *newPerm != 2 && *newPerm != 7 {
			cfg.ManagerMu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.PermissionLevelInvalid, nil)
			return
		}
		if u.Perm == 7 && *newPerm != 7 {
			admins := countAdminUsers(savedCfg.Auth, idx)
			if admins == 0 {
				cfg.ManagerMu.Unlock()
				web.WriteAPIError(w, http.StatusBadRequest, msg.LastAdminUserRequired, nil)
				return
			}
		}
		if u.Perm != *newPerm {
			u.Perm = *newPerm
			changed = true
		}
	}
	if req.AllowInstances != nil {
		u.AllowInstances = filterExistingInstances(*req.AllowInstances, instanceSet)
		changed = true
	}
	if req.AllowGroups != nil {
		u.AllowGroups = filterExistingGroups(*req.AllowGroups, groupSet)
		changed = true
	}
	if newPassHash != "" {
		u.Pass = newPassHash
		changed = true
	}

	updated := *u
	cfg.ManagerMu.Unlock()
	if changed {
		plan := cfg.MutationPlan{NextCfg: savedCfg}
		plan.Publish = func() {
			cfg.ManagerMu.Lock()
			cfg.CurrentConfig = savedCfg
			cfg.ManagerMu.Unlock()
		}
		plan.AddPostCommit(msg.SyncUserRuntimeState, func() error {
			disconnectUserWS(oldName)
			if passwordChanged {
				authz.DefaultRuntime.Sessions.RemoveUserToken(oldName)
				if finalName != oldName {
					authz.DefaultRuntime.Sessions.RemoveUserToken(finalName)
					disconnectUserWS(finalName)
				}
				if authedUser.User == oldName || authedUser.User == finalName {
					authz.DefaultRuntime.Cookies.ClearAuthCookie(w, r)
					authz.DefaultRuntime.Cookies.ClearCSRFCookie(w, r)
				}
			} else if finalName != oldName {
				authz.DefaultRuntime.Sessions.RenameUserTokenOwner(oldName, finalName)
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

	resp := struct {
		User           string   `json:"user"`
		Perm           int      `json:"perm"`
		AllowInstances []string `json:"allow_instances"`
		AllowGroups    []string `json:"allow_groups"`
	}{
		User:           updated.User,
		Perm:           updated.Perm,
		AllowInstances: append([]string(nil), updated.AllowInstances...),
		AllowGroups:    append([]string(nil), updated.AllowGroups...),
	}

	web.WriteOK(w, resp)
}

type adminUserCreateRequest struct {
	User           string   `json:"user"`
	Pass           string   `json:"pass"`
	Perm           int      `json:"perm"`
	AllowInstances []string `json:"allow_instances"`
	AllowGroups    []string `json:"allow_groups"`
}

func HandleApiAdminCreate(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	web.MarkRequestUser(w, authedUser.User)

	var req adminUserCreateRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	username := authz.NormalizeUsername(req.User)
	if username == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameInvalid, nil)
		return
	}
	if err := cfg.ValidateUserName(username); err != nil {
		writeUserNameValidationError(w, err)
		return
	}
	pass := strings.TrimSpace(req.Pass)
	if pass == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.PasswordRequired, nil)
		return
	}
	if err := cfg.ValidateUserPassword(pass); err != nil {
		writeUserPasswordValidationError(w, err)
		return
	}
	perm := req.Perm
	if perm != 0 && perm != 2 && perm != 7 {
		web.WriteAPIError(w, http.StatusBadRequest, msg.PermissionLevelInvalid, nil)
		return
	}

	stored, err := cfg.HashPassword(pass)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.PasswordHashFailed, err)
		return
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()
	instanceSet, groupSet := buildInstanceAndGroupSet(savedCfg.Instances)
	if idx := findAuthUserIndex(savedCfg.Auth, username); idx != -1 {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusConflict, msg.UserNameDuplicate, nil)
		return
	}

	newUser := cfg.AuthUser{
		User:           username,
		Pass:           stored,
		Perm:           perm,
		AllowInstances: filterExistingInstances(req.AllowInstances, instanceSet),
		AllowGroups:    filterExistingGroups(req.AllowGroups, groupSet),
	}
	savedCfg.Auth = append(savedCfg.Auth, newUser)
	cfg.ManagerMu.Unlock()
	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		cfg.ManagerMu.Unlock()
	}
	if err := cfg.CommitMutationPlan(plan); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
		return
	}
	web.WriteOK(w, map[string]string{"user": username})
}

type adminUserDeleteRequest struct {
	User string `json:"user"`
}

func HandleApiAdminDelete(w http.ResponseWriter, r *http.Request) {
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}

	var req adminUserDeleteRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.User)
	if username == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.UserParamMissing, nil)
		return
	}

	if strings.TrimSpace(authedUser.User) != "" && username == authedUser.User {
		web.WriteAPIError(w, http.StatusBadRequest, msg.CannotDeleteCurrentUser, nil)
		return
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()
	idx := findAuthUserIndex(savedCfg.Auth, username)
	if idx == -1 {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.UserNotFound, nil)
		return
	}
	if savedCfg.Auth[idx].Perm == 7 {
		admins := countAdminUsers(savedCfg.Auth, idx)
		if admins == 0 {
			cfg.ManagerMu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.LastAdminUserRequired, nil)
			return
		}
	}

	savedCfg.Auth = append(savedCfg.Auth[:idx], savedCfg.Auth[idx+1:]...)
	cfg.ManagerMu.Unlock()
	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		cfg.ManagerMu.Unlock()
	}
	plan.AddPostCommit(msg.CleanupDeletedUserSessions, func() error {
		authz.DefaultRuntime.Sessions.RemoveUserToken(username)
		if authedUser.User == username {
			authz.DefaultRuntime.Cookies.ClearAuthCookie(w, r)
			authz.DefaultRuntime.Cookies.ClearCSRFCookie(w, r)
		}
		disconnectUserWS(username)
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

	web.WriteOK(w, map[string]bool{"ok": true})
}
