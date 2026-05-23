package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"
	process "IpacPanel/controller/src/process"

	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type deleteInstanceRequest struct {
	Instance            string `json:"instance"`
	DeleteFiles         bool   `json:"delete_files"`
	ConfirmSharedDelete bool   `json:"confirm_shared_delete"`
}

type updateInstanceRequest struct {
	Instance string                `json:"instance"`
	Config   instanceConfigRequest `json:"config"`
}

type instanceConfigRequest struct {
	Instance        string     `json:"instance"`
	Group           string     `json:"group,omitempty"`
	Path            string     `json:"path"`
	Command         string     `json:"command"`
	AccessLinks     string     `json:"access_links,omitempty"`
	Terminal        int        `json:"terminal,omitempty"`
	InputEncoding   string     `json:"input_encoding,omitempty"`
	OutputEncoding  string     `json:"output_encoding,omitempty"`
	StopCommand     string     `json:"stop_command,omitempty"`
	CleanupCommand  string     `json:"cleanup_command,omitempty"`
	AutoStart       bool       `json:"auto_start"`
	StartPriority   *int       `json:"start_priority,omitempty"`
	AutoRestart     bool       `json:"auto_restart"`
	RestartInterval *int       `json:"restart_interval,omitempty"`
	Tasks           []cfg.Task `json:"tasks,omitempty"`
}

func instanceConfigFromRequest(req instanceConfigRequest) cfg.Instance {
	return cfg.Instance{
		Name:            req.Instance,
		Group:           req.Group,
		Path:            req.Path,
		Command:         req.Command,
		AccessLinks:     req.AccessLinks,
		Terminal:        req.Terminal,
		InputEncoding:   req.InputEncoding,
		OutputEncoding:  req.OutputEncoding,
		StopCommand:     req.StopCommand,
		CleanupCommand:  req.CleanupCommand,
		AutoStart:       req.AutoStart,
		StartPriority:   req.StartPriority,
		AutoRestart:     req.AutoRestart,
		RestartInterval: req.RestartInterval,
		Tasks:           req.Tasks,
	}
}

func normalizePathForSamePathCompare(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func isSameResolvedPath(left string, right string) bool {
	return normalizePathForSamePathCompare(left) == normalizePathForSamePathCompare(right)
}

func isResolvedDangerousDeletePath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return true
	}
	if parent := filepath.Dir(cleaned); parent == cleaned {
		return true
	}
	dangerous := []string{
		filepath.Clean(cfg.GetAppBaseDir()),
		filepath.Clean(cfg.ResolveDataPath("")),
		filepath.Clean(cfg.ResolveAppPath("src")),
		filepath.Clean(cfg.GetPublicDir()),
	}
	for _, item := range dangerous {
		if isSameResolvedPath(cleaned, item) {
			return true
		}
	}
	return false
}

func buildInstanceDeleteTombstonePath(instancePath string, instanceName string) string {
	parent := filepath.Dir(instancePath)
	trashRoot := filepath.Join(parent, ".IpacPanel-trash")
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	name := strings.TrimSpace(instanceName)
	if name == "" {
		name = "instance"
	}
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.ReplaceAll(name, ":", "_")
	return filepath.Join(trashRoot, fmt.Sprintf("%s-%s", stamp, name))
}

func rollbackDeletedInstance(name string, oldCfg cfg.Config, ip *process.InstanceProcess, originalPath string, tombstonePath string, pathMoved bool) error {
	var errs []string
	if pathMoved {
		if err := os.MkdirAll(filepath.Dir(originalPath), 0755); err != nil {
			errs = append(errs, fmt.Sprintf(msg.RestoreInstanceDirParentFailedFmt, err))
		} else if err := os.Rename(tombstonePath, originalPath); err != nil {
			errs = append(errs, fmt.Sprintf(msg.RestoreInstanceDirFailedFmt, err))
		}
	}
	if err := cfg.SaveConfigSnapshot(oldCfg); err != nil {
		errs = append(errs, fmt.Sprintf(msg.RestoreConfigSnapshotFailedFmt, err))
	}
	cfg.ManagerMu.Lock()
	cfg.CurrentConfig = oldCfg
	process.InstanceProcesses[name] = ip
	process.RegisterInstanceProcessAliasLocked(name, ip)
	cfg.ManagerMu.Unlock()
	process.SyncInstancePointers()
	if err := process.RebuildInstanceTasks(name); err != nil {
		errs = append(errs, fmt.Sprintf(msg.RestoreInstanceTasksFailedFmt, err))
	}
	ip.CancelDelete()
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

func createInstanceDirAfterPlan(resolvedPath string) (bool, error) {
	resolvedPath = strings.TrimSpace(resolvedPath)
	if resolvedPath == "" {
		return false, errors.New(msg.EmptyDest)
	}
	created := false
	if _, err := os.Stat(resolvedPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		created = true
	}
	return created, os.MkdirAll(resolvedPath, 0755)
}

func removeCreatedInstanceDirOnFailure(resolvedPath string, created bool) {
	if !created || strings.TrimSpace(resolvedPath) == "" {
		return
	}
	_ = os.Remove(resolvedPath)
}

func HandleApiInstanceCreate(w http.ResponseWriter, r *http.Request) {
	var req instanceConfigRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	instanceConfig := instanceConfigFromRequest(req)

	cfg.NormalizeInstanceRequest(&instanceConfig)
	if err := cfg.ValidateInstanceConfig(&instanceConfig); err != nil {
		writeInstanceConfigValidationError(w, err)
		return
	}

	resolvedPath, err := cfg.ResolveInstancePath(instanceConfig.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ResolveInstanceDirFailed, err)
		return
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	if _, ok := process.InstanceProcesses[instanceConfig.Name]; ok {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusConflict, msg.InstanceNameDuplicate, nil)
		return
	}

	savedCfg := cfg.CloneConfigLocked()
	savedCfg.Instances = append(savedCfg.Instances, instanceConfig)
	createdProcess := process.NewInstanceProcess(&savedCfg.Instances[len(savedCfg.Instances)-1])
	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		process.InstanceProcesses[instanceConfig.Name] = createdProcess
		process.RegisterInstanceProcessAliasLocked(instanceConfig.Name, createdProcess)
		cfg.ManagerMu.Unlock()
		process.SyncInstancePointers()
	}
	plan.AddPostCommit(msg.RebuildInstanceTasks, func() error {
		return process.RebuildInstanceTasks(instanceConfig.Name)
	})
	cfg.ManagerMu.Unlock()

	createdDir, err := createInstanceDirAfterPlan(resolvedPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateInstanceDirFailed, err)
		return
	}
	if err := cfg.CommitMutationPlan(plan); err != nil {
		removeCreatedInstanceDirOnFailure(resolvedPath, createdDir)
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
		return
	}
	result := cfg.RunMutationPostCommit(plan)
	if !result.RuntimeSynced {
		BroadcastInstanceListUpdates()
		writeMutationRuntimeSyncError(w, http.StatusInternalServerError, msg.ConfigSavedRuntimeSyncFailed, result)
		return
	}
	BroadcastInstanceListUpdates()

	web.WriteOK(w, instanceConfig)
}

func HandleApiInstanceUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateInstanceRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Instance)
	if name == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameMissing, nil)
		return
	}
	if err := cfg.ValidateInstanceName(name); err != nil {
		switch err.Error() {
		case msg.InstanceNameTooLong:
			web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameTooLong, nil)
			return
		case msg.InstanceNameInvalidChars:
			web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameInvalidChars, nil)
			return
		}
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameInvalid, nil)
		return
	}

	instanceConfig := instanceConfigFromRequest(req.Config)
	cfg.NormalizeInstanceRequest(&instanceConfig)
	if err := cfg.ValidateInstanceConfig(&instanceConfig); err != nil {
		writeInstanceConfigValidationError(w, err)
		return
	}

	resolvedPath, err := cfg.ResolveInstancePath(instanceConfig.Path)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.ResolveInstanceDirFailed, err)
		return
	}

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	plan, err := buildInstanceUpdateMutationPlan(name, instanceConfig)
	if err != nil {
		switch {
		case errors.Is(err, errInstanceNotFound):
			web.WriteAPIError(w, http.StatusNotFound, msg.InstanceNotFound, nil)
		case errors.Is(err, errInstanceNameExists):
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceNameDuplicate, nil)
		case errors.Is(err, errInstanceConfigNotFound):
			web.WriteAPIError(w, http.StatusNotFound, msg.InstanceConfigNotFound, nil)
		default:
			web.WriteAPIError(w, http.StatusInternalServerError, msg.BuildInstanceUpdatePlanFailed, err)
		}
		return
	}

	createdDir, err := createInstanceDirAfterPlan(resolvedPath)
	if err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.CreateInstanceDirFailed, err)
		return
	}
	if err := cfg.CommitMutationPlan(plan); err != nil {
		removeCreatedInstanceDirOnFailure(resolvedPath, createdDir)
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
		return
	}
	result := cfg.RunMutationPostCommit(plan)
	if !result.RuntimeSynced {
		BroadcastInstanceListUpdates()
		writeMutationRuntimeSyncError(w, http.StatusInternalServerError, msg.ConfigSavedRuntimeSyncFailed, result)
		return
	}
	BroadcastInstanceListUpdates()

	web.WriteOK(w, instanceConfig)
}

func HandleApiInstanceDelete(w http.ResponseWriter, r *http.Request) {
	var req deleteInstanceRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Instance)
	if name == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameMissing, nil)
		return
	}

	var ip *process.InstanceProcess
	var oldCfg cfg.Config
	var savedCfg cfg.Config
	var originalPath string
	var tombstonePath string
	var ok bool
	pathMoved := false

	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	ip, ok = process.InstanceProcesses[name]
	if !ok {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.InstanceNotFound, nil)
		return
	}

	idx := -1
	for i := range cfg.CurrentConfig.Instances {
		if cfg.CurrentConfig.Instances[i].Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		cfg.ManagerMu.Unlock()
		web.WriteAPIError(w, http.StatusNotFound, msg.InstanceConfigNotFound, nil)
		return
	}

	oldCfg = cfg.CloneConfigLocked()
	savedCfg = cfg.CloneConfigLocked()
	instanceCfg := cfg.CurrentConfig.Instances[idx]
	savedCfg.Instances = append(savedCfg.Instances[:idx], savedCfg.Instances[idx+1:]...)
	if req.DeleteFiles {
		resolvedPath, err := cfg.ResolveInstancePath(instanceCfg.Path)
		if err != nil {
			cfg.ManagerMu.Unlock()
			web.WriteAPIError(w, http.StatusInternalServerError, msg.ResolveInstanceDirFailed, err)
			return
		}
		resolvedPath = filepath.Clean(resolvedPath)
		if isResolvedDangerousDeletePath(resolvedPath) {
			cfg.ManagerMu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.UnsafeInstanceDirDeleteRejected, nil)
			return
		}
		for i := range cfg.CurrentConfig.Instances {
			other := cfg.CurrentConfig.Instances[i]
			if other.Name == name {
				continue
			}
			otherIP, exists := process.InstanceProcesses[other.Name]
			if !exists {
				continue
			}
			otherResolvedPath, err := cfg.ResolveInstancePath(other.Path)
			if err != nil {
				cfg.ManagerMu.Unlock()
				web.WriteAPIError(w, http.StatusInternalServerError, msg.ResolveOtherInstanceDirsFailed, err)
				return
			}
			if isSameResolvedPath(otherResolvedPath, resolvedPath) {
				if otherIP.IsActive() {
					cfg.ManagerMu.Unlock()
					web.WriteAPIError(w, http.StatusConflict, msg.ActiveInstanceUsesSameDir, nil)
					return
				}
				if !req.ConfirmSharedDelete {
					cfg.ManagerMu.Unlock()
					web.MarkAPIError(w, http.StatusConflict, msg.InactiveInstanceUsesSameDir, nil)
					web.WriteJSONStatus(w, http.StatusConflict, web.APIResponse{
						OK:      false,
						Message: msg.InactiveInstanceUsesSameDir,
						Data: map[string]any{
							"confirm_required": true,
						},
					}, "")
					return
				}
			}
		}
		if stat, err := os.Stat(resolvedPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				cfg.ManagerMu.Unlock()
				web.WriteAPIError(w, http.StatusInternalServerError, msg.CheckInstanceDirFailed, err)
				return
			}
		} else if !stat.IsDir() {
			cfg.ManagerMu.Unlock()
			web.WriteAPIError(w, http.StatusBadRequest, msg.InstancePathNotDirectory, nil)
			return
		} else {
			originalPath = resolvedPath
			tombstonePath = buildInstanceDeleteTombstonePath(resolvedPath, name)
		}
	}
	cfg.ManagerMu.Unlock()

	running, restarting, starting, deleting := ip.BeginDelete()
	if running || restarting || starting || deleting {
		switch {
		case running:
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceRunning, nil)
		case restarting:
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceRestartingState, nil)
		case starting:
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceStartingState, nil)
		default:
			web.WriteAPIError(w, http.StatusConflict, msg.InstanceStateChangedRetry, nil)
		}
		return
	}
	defer func() {
		if ip == nil {
			return
		}
		ip.CancelDelete()
	}()

	if originalPath != "" {
		if err := os.MkdirAll(filepath.Dir(tombstonePath), 0755); err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.PrepareInstanceTombstoneDirFailed, err)
			return
		}
		if err := os.Rename(originalPath, tombstonePath); err != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.MoveInstanceDirBeforeDeleteFailed, err)
			return
		}
		pathMoved = true
	}

	if err := cfg.SaveConfigSnapshot(savedCfg); err != nil {
		if rollbackErr := rollbackDeletedInstance(name, oldCfg, ip, originalPath, tombstonePath, pathMoved); rollbackErr != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveDeleteTransactionFailedRollbackIncomplete, rollbackErr)
			return
		}
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveDeleteTransactionFailed, err)
		return
	}

	cfg.ManagerMu.Lock()
	if _, stillExists := process.InstanceProcesses[name]; !stillExists {
		cfg.ManagerMu.Unlock()
		if rollbackErr := rollbackDeletedInstance(name, oldCfg, ip, originalPath, tombstonePath, pathMoved); rollbackErr != nil {
			web.WriteAPIError(w, http.StatusConflict, msg.DeleteStateChangedRollbackFailed, rollbackErr)
			return
		}
		web.WriteAPIError(w, http.StatusConflict, msg.DeleteStateChangedRetry, nil)
		return
	}
	cfg.CurrentConfig = savedCfg
	delete(process.InstanceProcesses, name)
	process.UnregisterInstanceProcessAliasesLocked(ip)
	cfg.ManagerMu.Unlock()
	process.SyncInstancePointers()

	if err := process.RebuildInstanceTasks(name); err != nil {
		if rollbackErr := rollbackDeletedInstance(name, oldCfg, ip, originalPath, tombstonePath, pathMoved); rollbackErr != nil {
			web.WriteAPIError(w, http.StatusInternalServerError, msg.RuntimeSyncFailedRollbackIncomplete, rollbackErr)
			return
		}
		web.WriteAPIError(w, http.StatusInternalServerError, msg.RuntimeSyncAfterDeleteFailed, err)
		return
	}

	if pathMoved {
		if err := os.RemoveAll(tombstonePath); err != nil {
			if rollbackErr := rollbackDeletedInstance(name, oldCfg, ip, originalPath, tombstonePath, pathMoved); rollbackErr != nil {
				web.WriteAPIError(w, http.StatusInternalServerError, msg.DeleteInstanceFilesFailedRollbackIncomplete, rollbackErr)
				return
			}
			web.WriteAPIError(w, http.StatusInternalServerError, msg.InstanceFilesPartiallyDeletedRollbackAttempted, err)
			return
		}
	}

	ip.RetireDeletedInstance()
	BroadcastInstanceListUpdates()
	ip = nil
	web.WriteOK(w, map[string]any{"ok": true, "delete_files": req.DeleteFiles})
}
