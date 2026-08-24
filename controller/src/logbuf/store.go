package logbuf

import (
	"errors"
	"strings"
)

// Filter 限制快照返回的条目范围; 零值表示不过滤.
type Filter struct {
	// Instance 为空时不过滤实例归属, 非空时只保留该实例的条目.
	Instance string
	// Levels 为空时不过滤级别, 非空时只保留列出的级别.
	Levels map[string]struct{}
	// VisibleInstances 为 nil 表示不过滤可见性; 非 nil 时仅保留
	// Instance 为空 (面板级) 或命中集合的条目.
	VisibleInstances map[string]bool
}

func (f Filter) match(entry Entry) bool {
	if f.Instance != "" && entry.Instance != f.Instance {
		return false
	}
	if f.VisibleInstances != nil && entry.Instance != "" && !f.VisibleInstances[entry.Instance] {
		return false
	}
	if len(f.Levels) > 0 {
		if _, ok := f.Levels[entry.Level]; !ok {
			return false
		}
	}
	return true
}

var errLimitOutOfRange = errors.New("运行日志拉取数量超出范围")

const (
	limitMin = 1
	limitMax = MaxEntries
)

// NormalizeLevelSet 将级别列表转换为过滤集合, 空白项被忽略.
func NormalizeLevelSet(levels []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(levels))
	for _, level := range levels {
		level = strings.TrimSpace(level)
		if level == "" {
			continue
		}
		if !validLevel(level) {
			return nil, errors.New("日志级别无效")
		}
		set[level] = struct{}{}
	}
	return set, nil
}

type buffer struct {
	entries []Entry
	start   int
}

func (b *buffer) lenLocked() int {
	return len(b.entries) - b.start
}

func (b *buffer) pushLocked(entry Entry) {
	b.entries = append(b.entries, entry)
	totalByte += messageByteLen(entry.Message)

	for b.start < len(b.entries) && (b.lenLocked() > MaxEntries || totalByte > MaxTotalBytes) {
		totalByte -= messageByteLen(b.entries[b.start].Message)
		b.entries[b.start] = Entry{}
		b.start += 1
	}

	// 全部丢弃后重置切片, 避免底层数组无限增长.
	if b.start == len(b.entries) {
		b.entries = b.entries[:0]
		b.start = 0
		totalByte = 0
	} else if b.start > 0 && b.start == cap(b.entries)/2 {
		copy(b.entries, b.entries[b.start:])
		b.entries = b.entries[:b.lenLocked()]
		b.start = 0
	}
}

func messageByteLen(message string) int {
	return len(message)
}

// Snapshot 返回 seq 小于 beforeSeq 的最新 limit 条匹配条目 (按 seq 倒序),
// 以及调用方视角下缓冲中的匹配条目总数. beforeSeq 为 0 表示从最新开始.
func Snapshot(beforeSeq uint64, limit int, filter Filter) ([]Entry, int, error) {
	if limit < limitMin || limit > limitMax {
		return nil, 0, errLimitOutOfRange
	}

	mu.RLock()
	defer mu.RUnlock()

	end := len(buf.entries)
	if beforeSeq > 0 {
		for end > buf.start && buf.entries[end-1].Seq >= beforeSeq {
			end -= 1
		}
	}

	total := 0
	for i := buf.start; i < end; i += 1 {
		if filter.match(buf.entries[i]) {
			total += 1
		}
	}

	result := make([]Entry, 0, min(limit, total))
	for i := end - 1; i >= buf.start && len(result) < limit; i -= 1 {
		entry := buf.entries[i]
		if filter.match(entry) {
			result = append(result, entry)
		}
	}
	return result, total, nil
}

// Clear 清空缓冲, 返回被清除的条目数.
// 序号保持单调递增不重置, 使客户端能通过 seq 持续对齐增量.
func Clear() int {
	mu.Lock()
	defer mu.Unlock()
	count := buf.lenLocked()
	buf.entries = nil
	buf.start = 0
	totalByte = 0
	return count
}

// Since 返回 seq 大于 afterSeq 且匹配的全部条目, 按 seq 升序; 上限 MaxEntries.
func Since(afterSeq uint64, filter Filter) []Entry {
	mu.RLock()
	defer mu.RUnlock()

	result := make([]Entry, 0, 16)
	for i := buf.start; i < len(buf.entries); i += 1 {
		entry := buf.entries[i]
		if entry.Seq <= afterSeq || !filter.match(entry) {
			continue
		}
		result = append(result, entry)
	}

	return result
}

// Count 返回缓冲中匹配 filter 的当前条目数.
func Count(filter Filter) int {
	mu.RLock()
	defer mu.RUnlock()
	total := 0
	for i := buf.start; i < len(buf.entries); i += 1 {
		if filter.match(buf.entries[i]) {
			total += 1
		}
	}
	return total
}
