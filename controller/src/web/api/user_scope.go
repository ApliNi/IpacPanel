package api

import (
	cfg "IpacPanel/controller/src/config"
	"strings"
)

const ungroupedScopeLabel = "UNGROUPED"

type instanceScopeMeta struct {
	name  string
	group string
}

func collectInstanceScopeMetaLocked() ([]instanceScopeMeta, int, int) {
	totalInstances := len(cfg.CurrentConfig.Instances)
	instances := make([]instanceScopeMeta, 0, totalInstances)
	groupSet := make(map[string]struct{})
	for i := range cfg.CurrentConfig.Instances {
		name := strings.TrimSpace(cfg.CurrentConfig.Instances[i].Name)
		groupName := strings.TrimSpace(cfg.CurrentConfig.Instances[i].Group)
		if groupName == "" {
			groupName = ungroupedScopeLabel
		}
		if name != "" {
			instances = append(instances, instanceScopeMeta{name: name, group: groupName})
		}
		groupSet[groupName] = struct{}{}
	}
	return instances, totalInstances, len(groupSet)
}

func countUserScopeWithInstances(user *cfg.AuthUser, instances []instanceScopeMeta, totalInstances int, totalGroups int) (int, int) {
	if user == nil {
		return 0, 0
	}
	if user.Perm == 7 {
		return totalInstances, totalGroups
	}
	if user.Perm != 2 {
		return 0, 0
	}

	allowedInstances := make(map[string]struct{})
	for _, n := range user.AllowInstances {
		name := strings.TrimSpace(n)
		if name == "" {
			continue
		}
		allowedInstances[name] = struct{}{}
	}
	allowedGroups := make(map[string]struct{})
	for _, g := range user.AllowGroups {
		grp := strings.TrimSpace(g)
		if grp == "" {
			continue
		}
		allowedGroups[grp] = struct{}{}
	}

	accessibleGroups := make(map[string]struct{})
	accessibleInstances := 0
	for _, ins := range instances {
		_, byName := allowedInstances[ins.name]
		_, byGroup := allowedGroups[ins.group]
		if !byName && !byGroup {
			continue
		}
		accessibleInstances += 1
		accessibleGroups[ins.group] = struct{}{}
	}
	return accessibleInstances, len(accessibleGroups)
}

func countUserScopeLocked(user *cfg.AuthUser) (int, int) {
	instances, totalInstances, totalGroups := collectInstanceScopeMetaLocked()
	return countUserScopeWithInstances(user, instances, totalInstances, totalGroups)
}
