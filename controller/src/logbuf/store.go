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
	limitMax = MaxEntries * 2
)

// NormalizeLevelSet 将级别列表转换为过滤集合, 空白项被忽略.
// WARN 与 ERROR 合并, 传入 warn 或 error 时均包含 error 与 warn.
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
		if level == LevelWarn || level == LevelError {
			set[LevelError] = struct{}{}
			set[LevelWarn] = struct{}{}
		} else {
			set[level] = struct{}{}
		}
	}
	return set, nil
}

type buffer struct {
	entries    []Entry
	start      int
	totalBytes int
}

func (b *buffer) lenLocked() int {
	return len(b.entries) - b.start
}

func (b *buffer) clearLocked() {
	b.entries = nil
	b.start = 0
	b.totalBytes = 0
}

func (b *buffer) pushLocked(entry Entry) {
	b.entries = append(b.entries, entry)
	b.totalBytes += messageByteLen(entry.Message)

	for b.start < len(b.entries) && (b.lenLocked() > MaxEntries || b.totalBytes > MaxTotalBytes) {
		b.totalBytes -= messageByteLen(b.entries[b.start].Message)
		b.entries[b.start] = Entry{}
		b.start += 1
	}

	// 全部丢弃后重置切片, 避免底层数组无限增长.
	if b.start == len(b.entries) {
		b.entries = b.entries[:0]
		b.start = 0
		b.totalBytes = 0
	} else if b.start > 0 && b.start >= cap(b.entries)/2 {
		copy(b.entries, b.entries[b.start:])
		newLen := b.lenLocked()
		for i := newLen; i < len(b.entries); i++ {
			b.entries[i] = Entry{}
		}
		b.entries = b.entries[:newLen]
		b.start = 0
	}
}

func messageByteLen(message string) int {
	return len(message)
}

func resolveFilterBuffers(filter Filter) (bool, bool) {
	if len(filter.Levels) == 0 {
		return true, true
	}
	var hasError, hasInfo bool
	for level := range filter.Levels {
		if level == LevelError || level == LevelWarn {
			hasError = true
		} else if level == LevelInfo {
			hasInfo = true
		}
	}
	return hasError, hasInfo
}

// Snapshot 返回 seq 小于 beforeSeq 的最新 limit 条匹配条目 (按 seq 倒序),
// 以及调用方视角下缓冲中的匹配条目总数. beforeSeq 为 0 表示从最新开始.
// 当同时包含两类日志时, 按时间/seq 倒序归并两个缓冲区.
func Snapshot(beforeSeq uint64, limit int, filter Filter) ([]Entry, int, error) {
	if limit < limitMin || limit > limitMax {
		return nil, 0, errLimitOutOfRange
	}

	mu.RLock()
	defer mu.RUnlock()

	includeError, includeInfo := resolveFilterBuffers(filter)

	endE := 0
	if includeError {
		endE = len(errorBuf.entries)
		if beforeSeq > 0 {
			for endE > errorBuf.start && errorBuf.entries[endE-1].Seq >= beforeSeq {
				endE -= 1
			}
		}
	}

	endI := 0
	if includeInfo {
		endI = len(infoBuf.entries)
		if beforeSeq > 0 {
			for endI > infoBuf.start && infoBuf.entries[endI-1].Seq >= beforeSeq {
				endI -= 1
			}
		}
	}

	total := 0
	if includeError {
		for i := errorBuf.start; i < endE; i += 1 {
			if filter.match(errorBuf.entries[i]) {
				total += 1
			}
		}
	}
	if includeInfo {
		for i := infoBuf.start; i < endI; i += 1 {
			if filter.match(infoBuf.entries[i]) {
				total += 1
			}
		}
	}

	result := make([]Entry, 0, min(limit, total))
	ptrE := endE - 1
	ptrI := endI - 1

	for len(result) < limit {
		hasE := includeError && ptrE >= errorBuf.start
		hasI := includeInfo && ptrI >= infoBuf.start

		if !hasE && !hasI {
			break
		}

		if hasE && hasI {
			if errorBuf.entries[ptrE].Seq >= infoBuf.entries[ptrI].Seq {
				entry := errorBuf.entries[ptrE]
				ptrE -= 1
				if filter.match(entry) {
					result = append(result, entry)
				}
			} else {
				entry := infoBuf.entries[ptrI]
				ptrI -= 1
				if filter.match(entry) {
					result = append(result, entry)
				}
			}
		} else if hasE {
			entry := errorBuf.entries[ptrE]
			ptrE -= 1
			if filter.match(entry) {
				result = append(result, entry)
			}
		} else {
			entry := infoBuf.entries[ptrI]
			ptrI -= 1
			if filter.match(entry) {
				result = append(result, entry)
			}
		}
	}
	return result, total, nil
}

// Clear 清空所有缓冲, 返回被清除的总条目数.
// 序号保持单调递增不重置, 使客户端能通过 seq 持续对齐增量.
func Clear() int {
	mu.Lock()
	defer mu.Unlock()
	count := errorBuf.lenLocked() + infoBuf.lenLocked()
	errorBuf.clearLocked()
	infoBuf.clearLocked()
	return count
}

// Since 返回 seq 大于 afterSeq 且匹配的全部条目, 按 seq 升序; 上限 limitMax.
func Since(afterSeq uint64, filter Filter) []Entry {
	mu.RLock()
	defer mu.RUnlock()

	includeError, includeInfo := resolveFilterBuffers(filter)

	ptrE := 0
	if includeError {
		ptrE = errorBuf.start
		for ptrE < len(errorBuf.entries) && errorBuf.entries[ptrE].Seq <= afterSeq {
			ptrE += 1
		}
	}
	ptrI := 0
	if includeInfo {
		ptrI = infoBuf.start
		for ptrI < len(infoBuf.entries) && infoBuf.entries[ptrI].Seq <= afterSeq {
			ptrI += 1
		}
	}

	result := make([]Entry, 0, 16)
	for len(result) < limitMax {
		hasE := includeError && ptrE < len(errorBuf.entries)
		hasI := includeInfo && ptrI < len(infoBuf.entries)

		if !hasE && !hasI {
			break
		}

		if hasE && hasI {
			if errorBuf.entries[ptrE].Seq <= infoBuf.entries[ptrI].Seq {
				entry := errorBuf.entries[ptrE]
				ptrE += 1
				if filter.match(entry) {
					result = append(result, entry)
				}
			} else {
				entry := infoBuf.entries[ptrI]
				ptrI += 1
				if filter.match(entry) {
					result = append(result, entry)
				}
			}
		} else if hasE {
			entry := errorBuf.entries[ptrE]
			ptrE += 1
			if filter.match(entry) {
				result = append(result, entry)
			}
		} else {
			entry := infoBuf.entries[ptrI]
			ptrI += 1
			if filter.match(entry) {
				result = append(result, entry)
			}
		}
	}

	return result
}

// Count 返回缓冲中匹配 filter 的当前条目数.
func Count(filter Filter) int {
	mu.RLock()
	defer mu.RUnlock()

	includeError, includeInfo := resolveFilterBuffers(filter)
	total := 0
	if includeError {
		for i := errorBuf.start; i < len(errorBuf.entries); i += 1 {
			if filter.match(errorBuf.entries[i]) {
				total += 1
			}
		}
	}
	if includeInfo {
		for i := infoBuf.start; i < len(infoBuf.entries); i += 1 {
			if filter.match(infoBuf.entries[i]) {
				total += 1
			}
		}
	}
	return total
}

// ErrorCount 返回缓冲中错误与警告条目数 (匹配 filter 实例范围).
func ErrorCount(filter Filter) int {
	mu.RLock()
	defer mu.RUnlock()

	total := 0
	for i := errorBuf.start; i < len(errorBuf.entries); i += 1 {
		entry := errorBuf.entries[i]
		if filter.Instance != "" && entry.Instance != filter.Instance {
			continue
		}
		if filter.VisibleInstances != nil && entry.Instance != "" && !filter.VisibleInstances[entry.Instance] {
			continue
		}
		total += 1
	}
	return total
}
