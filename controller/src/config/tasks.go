package config

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"
)

// NormalizeTaskExpr 校验并规范化严格 5 段标准 Unix/Linux cron 表达式:
//
//	分 时 日 月 周   (minute hour day-of-month month day-of-week)
//
// 旧版 Quartz 特性 (秒字段、年字段、'?' 与 L/W/# 特殊字符) 及自动补全行为被刻意排除.
// 同时拒绝 "@daily" 类描述符, 使调度从不内嵌描述符, 时间源管理保持显式.
// TZ= 或 CRON_TZ= 前缀由 BuildTaskSchedule 处理, 不在此校验器内.
func NormalizeTaskExpr(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", errors.New(msg.TaskExprRequired)
	}
	if strings.HasPrefix(expr, "@") {
		return "", errors.New(msg.TaskExprInvalid)
	}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return "", errors.New(msg.TaskExprInvalid)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return "", errors.New(msg.TaskExprInvalid)
	}
	// Standard cron forbids the Quartz '?' placeholder and the L/W/# modifiers.
	for _, part := range parts {
		if strings.ContainsAny(part, "?LW#") {
			return "", errors.New(msg.TaskExprInvalid)
		}
	}
	// gocron 的 defaultCron.IsValid 会把解析结果写入内部字段, 并发调用同一校验器会触发数据竞态,
	// 因此每次校验新建 validator, 与 gocron 自身为每个 job 复制 defaultCron 的方式保持一致.
	if err := gocron.NewDefaultCron(false).IsValid(strings.Join(parts, " "), time.Local, time.Now()); err != nil {
		return "", errors.New(msg.TaskExprInvalid)
	}
	return strings.Join(parts, " "), nil
}

// DefaultTaskTimezone returns the effective IANA timezone id for scheduled
// tasks. An empty value means the scheduler targets the server local time.
func DefaultTaskTimezone() (string, error) {
	ManagerMu.RLock()
	timezone := NormalizeTaskTimezone(CurrentConfig.TaskTimezone)
	ManagerMu.RUnlock()
	if timezone == "" {
		return "", nil
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", errors.New(msg.SettingsTaskTimezoneInvalid)
	}
	return timezone, nil
}

func IsValidTaskExpr(expr string) bool {
	_, err := NormalizeTaskExpr(expr)
	if err == nil {
		return true
	}
	// Also accept expressions with an inline TZ prefix.
	_, _, err2 := BuildTaskSchedule(expr)
	return err2 == nil
}

// BuildTaskSchedule returns the normalized 5-field expression plus the
// gocron-ready spec encoded with the appropriate timezone.
//
// An optional inline timezone prefix (TZ=<IANA> or CRON_TZ=<IANA>) at the
// start of expr overrides the global TaskTimezone for this task. If neither
// inline nor global timezone is set, the server local time is used.
//
// First return value: the full spec string (including CRON_TZ= prefix when a
// timezone applies) suitable for passing to gocron.CronJob.
// Second return value: the plain normalized 5-field expression.
func BuildTaskSchedule(expr string) (spec string, normalized string, err error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", "", errors.New(msg.TaskExprRequired)
	}

	// Extract optional inline timezone prefix.
	inlineTZ := ""
	rest := expr
	if strings.HasPrefix(rest, "CRON_TZ=") || strings.HasPrefix(rest, "TZ=") {
		parts := strings.Fields(rest)
		if len(parts) < 2 {
			return "", "", errors.New(msg.TaskExprInvalid)
		}
		prefix := parts[0]
		var tz string
		if strings.HasPrefix(prefix, "CRON_TZ=") {
			tz = strings.TrimPrefix(prefix, "CRON_TZ=")
		} else {
			tz = strings.TrimPrefix(prefix, "TZ=")
		}
		if tz == "" {
			return "", "", errors.New(msg.TaskExprInvalid)
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return "", "", errors.New(msg.TaskExprInvalid)
		}
		inlineTZ = tz
		rest = strings.Join(parts[1:], " ")
	}

	normalized, err = NormalizeTaskExpr(rest)
	if err != nil {
		return "", "", err
	}

	// Use inline timezone, or fall back to the global TaskTimezone.
	tz := inlineTZ
	if tz == "" {
		tz, err = DefaultTaskTimezone()
		if err != nil {
			return "", "", err
		}
	}

	if tz == "" {
		return normalized, normalized, nil
	}
	return "CRON_TZ=" + tz + " " + normalized, normalized, nil
}
