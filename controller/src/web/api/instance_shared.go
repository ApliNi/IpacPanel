package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"errors"
	"strings"
)

var (
	errInstanceNotFound       = errors.New(msg.InstanceNotFound)
	errInstanceNameExists     = errors.New(msg.InstanceNameDuplicate)
	errInstanceConfigNotFound = errors.New(msg.InstanceConfigNotFound)
)

func replaceAuthInstanceReference(users []cfg.AuthUser, oldName string, newName string) {
	if oldName == newName {
		return
	}
	for i := range users {
		u := &users[i]
		if len(u.AllowInstances) == 0 {
			continue
		}
		replaced := make([]string, 0, len(u.AllowInstances))
		changed := false
		for _, n := range u.AllowInstances {
			v := strings.TrimSpace(n)
			if v == "" {
				continue
			}
			if v == oldName {
				replaced = append(replaced, newName)
				changed = true
			} else {
				replaced = append(replaced, v)
			}
		}
		if changed {
			u.AllowInstances = cleanStringList(replaced)
		}
	}
}

func removeUnusedAuthGroupReference(users []cfg.AuthUser, instances []cfg.Instance, oldGroup string) {
	oldGroup = strings.TrimSpace(oldGroup)
	if oldGroup == "" {
		return
	}
	for i := range instances {
		if strings.TrimSpace(instances[i].Group) == oldGroup {
			return
		}
	}
	for i := range users {
		u := &users[i]
		if len(u.AllowGroups) == 0 {
			continue
		}
		replaced := make([]string, 0, len(u.AllowGroups))
		changed := false
		for _, g := range u.AllowGroups {
			v := strings.TrimSpace(g)
			if v == "" {
				continue
			}
			if v == oldGroup {
				changed = true
				continue
			}
			replaced = append(replaced, v)
		}
		if changed {
			u.AllowGroups = cleanStringList(replaced)
		}
	}
}

func buildInstanceUpdateMutationPlan(name string, req cfg.Instance) (cfg.MutationPlan, error) {
	cfg.ManagerMu.Lock()
	defer cfg.ManagerMu.Unlock()

	ip, ok := process.InstanceProcesses[name]
	if !ok {
		return cfg.MutationPlan{}, errInstanceNotFound
	}
	if req.Name != name {
		if _, exists := process.InstanceProcesses[req.Name]; exists {
			return cfg.MutationPlan{}, errInstanceNameExists
		}
	}

	index := -1
	for i := range cfg.CurrentConfig.Instances {
		if cfg.CurrentConfig.Instances[i].Name == name {
			index = i
			break
		}
	}
	if index == -1 {
		return cfg.MutationPlan{}, errInstanceConfigNotFound
	}

	savedCfg := cfg.CloneConfigLocked()
	oldInstance := savedCfg.Instances[index]
	oldName := oldInstance.Name
	oldGroup := strings.TrimSpace(oldInstance.Group)
	newGroup := strings.TrimSpace(req.Group)

	savedCfg.Instances[index] = req
	replaceAuthInstanceReference(savedCfg.Auth, oldName, req.Name)
	if oldGroup != "" && oldGroup != newGroup {
		removeUnusedAuthGroupReference(savedCfg.Auth, savedCfg.Instances, oldGroup)
	}

	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		if oldName != req.Name {
			process.RenameInstanceProcessAndKeepRuntimeAliasLocked(oldName, req.Name, ip)
		}
		cfg.ManagerMu.Unlock()
		process.SyncInstancePointers()
	}
	if name != req.Name {
		plan.AddPostCommit(msg.SyncDaemonInstanceRuntimeName, func() error {
			return process.RenameDaemonInstance(oldName, req.Name)
		})
		plan.AddPostCommit(msg.CleanupOldInstanceTasks, func() error {
			return process.RebuildInstanceTasks(name)
		})
	}
	plan.AddPostCommit(msg.SyncDaemonInstanceRuntimeConfig, func() error {
		resolvedPath, err := cfg.ResolveInstancePath(req.Path)
		if err != nil {
			return err
		}
		cleanupCommandArgv, err := process.CompileInstanceCleanupCommandArgv(req.CleanupCommand, resolvedPath)
		if err != nil {
			return err
		}
		return process.UpdateDaemonInstanceConfig(req.Name, cleanupCommandArgv)
	})
	plan.AddPostCommit(msg.RebuildInstanceTasks, func() error {
		return process.RebuildInstanceTasks(req.Name)
	})
	return plan, nil
}
