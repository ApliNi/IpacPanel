package authz

import (
	cfg "IpacPanel/controller/src/config"
	"strings"
)

type UserRole int

const (
	UserRoleNone UserRole = iota
	UserRoleInstance
	UserRoleAdmin
)

type Principal struct {
	Username       string
	Role           UserRole
	AllowInstances []string
	AllowGroups    []string
}

func PrincipalFromAuthUser(user *cfg.AuthUser) (*Principal, bool) {
	if user == nil || user.Perm == 0 {
		return nil, false
	}
	principal := &Principal{
		Username:       strings.TrimSpace(user.User),
		Role:           roleFromPerm(user.Perm),
		AllowInstances: normalizeStringSet(user.AllowInstances),
		AllowGroups:    normalizeStringSet(user.AllowGroups),
	}
	if principal.Username == "" || principal.Role == UserRoleNone {
		return nil, false
	}
	return principal, true
}

func PrincipalFromUsername(username string) (*Principal, bool) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, false
	}
	cfg.ManagerMu.RLock()
	defer cfg.ManagerMu.RUnlock()
	for i := range cfg.CurrentConfig.Auth {
		if cfg.CurrentConfig.Auth[i].User != username {
			continue
		}
		return PrincipalFromAuthUser(&cfg.CurrentConfig.Auth[i])
	}
	return nil, false
}

func roleFromPerm(perm int) UserRole {
	switch perm {
	case 7:
		return UserRoleAdmin
	case 2:
		return UserRoleInstance
	default:
		return UserRoleNone
	}
}

func normalizeStringSet(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
