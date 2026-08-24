// Package logbuf 维护进程内运行日志环形缓冲, 供前端运行日志视图分页拉取与实时推送.
// 条目不持久化: 管理进程重启后缓冲清空. 写入按条数与总字节双重上限丢弃最旧条目.
package logbuf

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// MaxEntries 缓冲条数上限, 超出后丢弃最旧条目.
	MaxEntries = 2000
	// MaxTotalBytes 缓冲消息总字节上限, 超出后丢弃最旧条目.
	MaxTotalBytes = 512 * 1024
	// MaxMessageBytes 单条消息字节上限, 超出部分截断.
	MaxMessageBytes = 4096

	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"

	// 实例名称最多包含 32 个字符 (与 config 约束一致), 预留余量取 64.
	instanceNameMaxRunes = 64
)

var (
	mu        sync.RWMutex
	nextSeq   uint64
	totalByte int

	buf = &buffer{}
)

// Entry 是一条运行日志. Instance 为空表示面板级条目.
type Entry struct {
	Seq      uint64 `json:"seq"`
	Time     int64  `json:"time"`
	Level    string `json:"level"`
	Instance string `json:"instance,omitempty"`
	Message  string `json:"message"`
}

func validLevel(level string) bool {
	return level == LevelInfo || level == LevelWarn || level == LevelError
}

func truncateMessage(message string) string {
	if len(message) <= MaxMessageBytes {
		return message
	}
	cut := MaxMessageBytes
	for cut > 0 && (message[cut]&0xC0) == 0x80 {
		cut--
	}
	return message[:cut]
}

func normalizeInstance(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	runes := []rune(name)
	if len(runes) > instanceNameMaxRunes {
		runes = runes[:instanceNameMaxRunes]
	}
	return string(runes)
}

// Emit 记录面板级条目.
func Emit(level, message string) error {
	return emit(level, "", message)
}

// EmitInstance 记录归属实例的条目.
func EmitInstance(level, instanceName, message string) error {
	return emit(level, instanceName, message)
}

func emit(level, instanceName, message string) error {
	if !validLevel(level) {
		return errors.New("日志级别无效")
	}
	entry := Entry{
		Time:     time.Now().Unix(),
		Level:    level,
		Instance: normalizeInstance(instanceName),
		Message:  truncateMessage(message),
	}
	if entry.Message == "" {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()
	nextSeq += 1
	entry.Seq = nextSeq
	buf.pushLocked(entry)
	return nil
}

// LatestSeq 返回当前最大序号; 缓冲为空时返回 0.
func LatestSeq() uint64 {
	mu.RLock()
	defer mu.RUnlock()
	return nextSeq
}

// Len 返回缓冲中的当前条目数.
func Len() int {
	mu.RLock()
	defer mu.RUnlock()
	return buf.lenLocked()
}
