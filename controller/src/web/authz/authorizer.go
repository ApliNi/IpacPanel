package authz

import (
	"IpacPanel/controller/src/msg"
	"strings"

	process "IpacPanel/controller/src/process"
)

const UngroupedScopeLabel = "UNGROUPED"

type Authorizer struct{}

func NewAuthorizer() *Authorizer {
	return &Authorizer{}
}

func (a *Authorizer) RequireAdmin(principal *Principal, forbiddenMessage string) error {
	if principal != nil && principal.Role == UserRoleAdmin {
		return nil
	}
	if strings.TrimSpace(forbiddenMessage) == "" {
		forbiddenMessage = msg.Forbidden
	}
	return newError(ErrorCodeForbidden, forbiddenMessage, nil)
}

func (a *Authorizer) CanAccessInstance(principal *Principal, instanceName string) bool {
	if principal == nil || principal.Role == UserRoleNone {
		return false
	}
	if principal.Role == UserRoleAdmin {
		return true
	}
	if principal.Role != UserRoleInstance {
		return false
	}
	name := strings.TrimSpace(instanceName)
	if name == "" {
		return false
	}
	for _, allowed := range principal.AllowInstances {
		if strings.TrimSpace(allowed) == name {
			return true
		}
	}
	sp, ok := process.Get(name)
	if !ok || sp == nil {
		return false
	}
	group := strings.TrimSpace(sp.InstanceSnapshot().Group)
	if group == "" {
		group = UngroupedScopeLabel
	}
	for _, allowed := range principal.AllowGroups {
		if strings.TrimSpace(allowed) == group {
			return true
		}
	}
	return false
}

func (a *Authorizer) CanAccessInstanceExact(principal *Principal, instanceName string) bool {
	if principal == nil || principal.Role == UserRoleNone {
		return false
	}
	if principal.Role == UserRoleAdmin {
		return true
	}
	if principal.Role != UserRoleInstance || instanceName == "" {
		return false
	}
	for _, allowed := range principal.AllowInstances {
		if strings.TrimSpace(allowed) == instanceName {
			return true
		}
	}
	sp, ok := process.Get(instanceName)
	if !ok || sp == nil {
		return false
	}
	group := strings.TrimSpace(sp.InstanceSnapshot().Group)
	if group == "" {
		group = UngroupedScopeLabel
	}
	for _, allowed := range principal.AllowGroups {
		if strings.TrimSpace(allowed) == group {
			return true
		}
	}
	return false
}

func (a *Authorizer) RequireInstanceProcess(principal *Principal, name string) (*process.InstanceProcess, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, newError(ErrorCodeInstanceRequired, msg.InstanceNameMissing, nil)
	}
	if !a.CanAccessInstance(principal, name) {
		return nil, newError(ErrorCodeInstanceNotFound, msg.InstanceNotFound, nil)
	}
	sp, ok := process.Get(name)
	if !ok || sp == nil || sp.IsDeleting() {
		return nil, newError(ErrorCodeInstanceNotFound, msg.InstanceNotFound, nil)
	}
	return sp, nil
}

func (a *Authorizer) RequireInstanceProcessExact(principal *Principal, name string) (*process.InstanceProcess, error) {
	if name == "" {
		return nil, newError(ErrorCodeInstanceRequired, msg.InstanceNameMissing, nil)
	}
	if !a.CanAccessInstanceExact(principal, name) {
		return nil, newError(ErrorCodeInstanceNotFound, msg.InstanceNotFound, nil)
	}
	sp, ok := process.Get(name)
	if !ok || sp == nil || sp.IsDeleting() {
		return nil, newError(ErrorCodeInstanceNotFound, msg.InstanceNotFound, nil)
	}
	return sp, nil
}

func (a *Authorizer) RequireAccessibleInstanceName(principal *Principal, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", newError(ErrorCodeInstanceRequired, msg.InstanceNameMissing, nil)
	}
	if !a.CanAccessInstance(principal, name) {
		return "", newError(ErrorCodeInstanceNotFound, msg.InstanceNotFound, nil)
	}
	return name, nil
}
