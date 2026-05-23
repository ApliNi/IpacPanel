package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"

	"net/http"
	"strings"
)

func isUngroupedLabel(input string) bool {
	return strings.EqualFold(strings.TrimSpace(input), "UNGROUPED")
}

type groupUpdateRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func HandleApiGroupUpdate(w http.ResponseWriter, r *http.Request) {
	var req groupUpdateRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	fromRaw := strings.TrimSpace(req.From)
	toRaw := strings.TrimSpace(req.To)
	isFromUngrouped := isUngroupedLabel(fromRaw)
	from := cfg.NormalizeGroupNameForStorage(fromRaw)
	to := cfg.NormalizeGroupNameForStorage(toRaw)

	if from == "" && !isFromUngrouped {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FromGroupNameInvalid, nil)
		return
	}
	if !isFromUngrouped {
		if err := cfg.ValidateGroupName(from); err != nil {
			switch err.Error() {
			case msg.GroupNameTooLong:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FromGroupNameTooLong, nil)
			case msg.GroupNameInvalidChars:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FromGroupNameInvalidChars, nil)
			default:
				web.WriteAPIError(w, http.StatusBadRequest, msg.FromGroupNameInvalid, nil)
			}
			return
		}
	}
	if err := cfg.ValidateGroupName(to); err != nil {
		switch err.Error() {
		case msg.GroupNameTooLong:
			web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameTooLong, nil)
		case msg.GroupNameInvalidChars:
			web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameInvalidChars, nil)
		default:
			web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameInvalid, nil)
		}
		return
	}
	if to == "" && !isUngroupedLabel(toRaw) {
		web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameInvalid, nil)
		return
	}
	// `from` can be UNGROUPED (stored as empty string).
	fromKey := from
	if isFromUngrouped {
		fromKey = ""
	}
	if fromKey == to {
		web.WriteOK(w, map[string]int{"updated": 0})
		return
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()

	updated := 0
	fromExists := false
	toExistsBefore := false
	for i := range savedCfg.Instances {
		g := cfg.NormalizeGroupNameForStorage(savedCfg.Instances[i].Group)
		if g == fromKey {
			fromExists = true
			savedCfg.Instances[i].Group = to
			updated += 1
			continue
		}
		if to != "" && g == to {
			toExistsBefore = true
		}
	}

	if !fromExists {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.GroupNotFound, nil)
		return
	}

	// Sync allow_groups:
	// - Always remove `from` from user scope.
	// - Only add `to` when:
	//   - to is not UNGROUPED (clearing), and
	//   - to does not exist before (no conflict/merge).
	// If merge/conflict, user loses old group and does not gain new.
	oldLabel := fromKey
	newLabel := to
	if oldLabel == "" {
		oldLabel = "UNGROUPED"
	}
	if newLabel == "" {
		newLabel = "UNGROUPED"
	}

	for i := range savedCfg.Auth {
		u := &savedCfg.Auth[i]
		if len(u.AllowGroups) == 0 {
			continue
		}
		hasOld := false
		hasNew := false
		replaced := make([]string, 0, len(u.AllowGroups))
		for _, g := range u.AllowGroups {
			v := strings.TrimSpace(g)
			if v == "" {
				continue
			}
			if v == oldLabel {
				hasOld = true
				continue
			}
			if v == newLabel {
				hasNew = true
			}
			replaced = append(replaced, v)
		}
		if !hasOld {
			continue
		}
		if to != "" && !toExistsBefore {
			if !hasNew {
				replaced = append(replaced, newLabel)
			}
		}
		u.AllowGroups = cleanStringList(replaced)
	}
	cfg.ManagerMu.Unlock()

	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		cfg.ManagerMu.Unlock()
		process.SyncInstancePointers()
	}

	if err := cfg.CommitMutationPlan(plan); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
		return
	}

	BroadcastInstanceListUpdates()
	web.WriteOK(w, map[string]int{"updated": updated})
}
