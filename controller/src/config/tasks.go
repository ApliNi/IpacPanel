package config

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/reugn/go-quartz/quartz"
)

func NormalizeTaskExpr(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", errors.New(msg.TaskExprRequired)
	}

	parts := strings.Fields(expr)
	if len(parts) == 5 {
		parts = append([]string{"0"}, parts...)
	}
	if len(parts) != 6 && len(parts) != 7 {
		return "", errors.New(msg.TaskExprInvalid)
	}

	AdjustQuartzTaskFields(parts)
	return strings.Join(parts, " "), nil
}

func AdjustQuartzTaskFields(parts []string) {
	if len(parts) != 6 && len(parts) != 7 {
		return
	}
	domIndex := 3
	dowIndex := 5
	dom := parts[domIndex]
	dow := parts[dowIndex]
	if strings.Contains(dom, "?") || strings.Contains(dow, "?") {
		return
	}
	if dom == "*" && dow == "*" {
		parts[dowIndex] = "?"
		return
	}
	if dom == "*" {
		parts[domIndex] = "?"
		return
	}
	if dow == "*" {
		parts[dowIndex] = "?"
	}
}

func NewTaskTrigger(expr string) (quartz.Trigger, string, error) {
	normalized, err := NormalizeTaskExpr(expr)
	if err != nil {
		return nil, "", err
	}
	location := time.Local
	ManagerMu.RLock()
	timezone := NormalizeTaskTimezone(CurrentConfig.TaskTimezone)
	ManagerMu.RUnlock()
	if timezone != "" {
		location, err = time.LoadLocation(timezone)
		if err != nil {
			return nil, "", fmt.Errorf(msg.SettingsTaskTimezoneInvalidFmt, err)
		}
	}
	trigger, err := quartz.NewCronTriggerWithLoc(normalized, location)
	if err != nil {
		return nil, "", errors.New(msg.TaskExprInvalid)
	}
	return trigger, normalized, nil
}

func IsValidTaskExpr(expr string) bool {
	_, _, err := NewTaskTrigger(expr)
	if err != nil {
		return false
	}
	return true
}
