package logbuf

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func resetBuffersForTest() {
	mu.Lock()
	defer mu.Unlock()
	errorBuf.clearLocked()
	infoBuf.clearLocked()
	nextSeq = 0
}

func TestDualBufferPhysicalIsolationAndInfoSurge(t *testing.T) {
	resetBuffersForTest()

	// 1. 写入 50 条错误与警告日志.
	for i := 1; i <= 50; i++ {
		level := LevelError
		if i%2 == 0 {
			level = LevelWarn
		}
		if err := EmitInstance(level, "srv-1", fmt.Sprintf("error-msg-%d", i)); err != nil {
			t.Fatalf("EmitInstance failed: %v", err)
		}
	}

	if count := ErrorCount(Filter{}); count != 50 {
		t.Fatalf("expected 50 error entries before surge, got %d", count)
	}

	// 2. 写入 5000 条普通 INFO 日志, 大幅超出 MaxEntries (2000).
	for i := 1; i <= 5000; i++ {
		if err := EmitInstance(LevelInfo, "srv-1", fmt.Sprintf("info-msg-%d", i)); err != nil {
			t.Fatalf("EmitInstance info failed: %v", err)
		}
	}

	// 3. 验证 errorBuf 完全未被冲刷覆盖, 依然保持 50 条.
	if count := ErrorCount(Filter{}); count != 50 {
		t.Fatalf("expected 50 error entries after surge, got %d", count)
	}

	errorSet, err := NormalizeLevelSet([]string{"error"})
	if err != nil {
		t.Fatalf("NormalizeLevelSet failed: %v", err)
	}

	errorEntries, totalError, err := Snapshot(0, 100, Filter{Levels: errorSet})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if totalError != 50 {
		t.Fatalf("expected totalError to be 50, got %d", totalError)
	}
	if len(errorEntries) != 50 {
		t.Fatalf("expected 50 error entries, got %d", len(errorEntries))
	}

	// 验证错误日志内容与序号顺序完好无损.
	for i, entry := range errorEntries {
		expectedSeq := uint64(50 - i)
		if entry.Seq != expectedSeq {
			t.Errorf("entry[%d] expected seq %d, got %d", i, expectedSeq, entry.Seq)
		}
		expectedMsg := fmt.Sprintf("error-msg-%d", 50-i)
		if entry.Message != expectedMsg {
			t.Errorf("entry[%d] expected msg %s, got %s", i, expectedMsg, entry.Message)
		}
	}

	// 4. 验证 infoBuf 被合理限制在 MaxEntries (2000) 条.
	infoSet, err := NormalizeLevelSet([]string{"info"})
	if err != nil {
		t.Fatalf("NormalizeLevelSet failed: %v", err)
	}

	infoEntries, totalInfo, err := Snapshot(0, 100, Filter{Levels: infoSet})
	if err != nil {
		t.Fatalf("Snapshot info failed: %v", err)
	}
	if totalInfo != MaxEntries {
		t.Fatalf("expected totalInfo to be %d, got %d", MaxEntries, totalInfo)
	}
	// 最新的一条 info msg 应该是 5000.
	if len(infoEntries) > 0 && infoEntries[0].Message != "info-msg-5000" {
		t.Errorf("expected newest info to be info-msg-5000, got %s", infoEntries[0].Message)
	}
}

func TestChronologicalMergeAll(t *testing.T) {
	resetBuffersForTest()

	// 交替交错写入: INFO (1), ERROR (2), INFO (3), WARN (4), INFO (5).
	_ = EmitInstance(LevelInfo, "srv-1", "info-1")
	_ = EmitInstance(LevelError, "srv-1", "error-2")
	_ = EmitInstance(LevelInfo, "srv-1", "info-3")
	_ = EmitInstance(LevelWarn, "srv-1", "warn-4")
	_ = EmitInstance(LevelInfo, "srv-1", "info-5")

	// 查询 ALL (无 levels 过滤).
	entries, total, err := Snapshot(0, 100, Filter{})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if total != 5 || len(entries) != 5 {
		t.Fatalf("expected 5 entries, got total=%d len=%d", total, len(entries))
	}

	// Snapshot 返回逆序: seq 应该严格为 5, 4, 3, 2, 1.
	expectedSeqs := []uint64{5, 4, 3, 2, 1}
	expectedMessages := []string{"info-5", "warn-4", "info-3", "error-2", "info-1"}
	for i, entry := range entries {
		if entry.Seq != expectedSeqs[i] {
			t.Errorf("index %d expected seq %d, got %d", i, expectedSeqs[i], entry.Seq)
		}
		if entry.Message != expectedMessages[i] {
			t.Errorf("index %d expected msg %s, got %s", i, expectedMessages[i], entry.Message)
		}
	}

	// 测试分页 beforeSeq = 4 (仅返回 seq < 4 的日志).
	page2, total2, err := Snapshot(4, 100, Filter{})
	if err != nil {
		t.Fatalf("Snapshot page2 failed: %v", err)
	}
	if total2 != 3 || len(page2) != 3 {
		t.Fatalf("expected 3 entries in page2, got total=%d len=%d", total2, len(page2))
	}
	if page2[0].Seq != 3 || page2[1].Seq != 2 || page2[2].Seq != 1 {
		t.Errorf("unexpected page2 seqs: %d, %d, %d", page2[0].Seq, page2[1].Seq, page2[2].Seq)
	}

	// 测试 Since (返回 seq > afterSeq, 升序).
	sinceList := Since(2, Filter{})
	if len(sinceList) != 3 {
		t.Fatalf("expected 3 entries in Since, got %d", len(sinceList))
	}
	if sinceList[0].Seq != 3 || sinceList[1].Seq != 4 || sinceList[2].Seq != 5 {
		t.Errorf("unexpected Since seqs: %d, %d, %d", sinceList[0].Seq, sinceList[1].Seq, sinceList[2].Seq)
	}
}

func TestNormalizeLevelSetAndFilter(t *testing.T) {
	// 1. error 与 warn 合并归一化.
	setErr, err := NormalizeLevelSet([]string{"error"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := setErr[LevelError]; !ok {
		t.Errorf("expected LevelError in set")
	}
	if _, ok := setErr[LevelWarn]; !ok {
		t.Errorf("expected LevelWarn in set")
	}

	setWarn, err := NormalizeLevelSet([]string{"warn"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := setWarn[LevelError]; !ok {
		t.Errorf("expected LevelError in setWarn")
	}
	if _, ok := setWarn[LevelWarn]; !ok {
		t.Errorf("expected LevelWarn in setWarn")
	}

	// 2. info 独立归一化.
	setInfo, err := NormalizeLevelSet([]string{"info"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := setInfo[LevelInfo]; !ok {
		t.Errorf("expected LevelInfo in setInfo")
	}
	if _, ok := setInfo[LevelError]; ok {
		t.Errorf("LevelError should not be in setInfo")
	}

	// 3. 无效级别报错.
	if _, err := NormalizeLevelSet([]string{"unknown"}); err == nil {
		t.Errorf("expected error for invalid level")
	}
}

func TestVisibleInstancesAndInstanceFilter(t *testing.T) {
	resetBuffersForTest()

	_ = Emit(LevelError, "panel-err-1")
	_ = EmitInstance(LevelError, "srv-allowed", "srv-err-1")
	_ = EmitInstance(LevelError, "srv-denied", "srv-err-2")
	_ = EmitInstance(LevelInfo, "srv-allowed", "srv-info-1")
	_ = EmitInstance(LevelInfo, "srv-denied", "srv-info-2")

	// 过滤: 用户只能访问 srv-allowed 与面板日志.
	filter := Filter{
		VisibleInstances: map[string]bool{
			"srv-allowed": true,
		},
	}

	entries, total, err := Snapshot(0, 100, filter)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if total != 3 || len(entries) != 3 {
		t.Fatalf("expected 3 visible entries, got total=%d len=%d", total, len(entries))
	}

	for _, e := range entries {
		if e.Instance == "srv-denied" {
			t.Errorf("found denied instance in entries: %v", e)
		}
	}

	// 进一步按 Instance = srv-allowed 筛选.
	filter.Instance = "srv-allowed"
	entries, total, err = Snapshot(0, 100, filter)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected 2 instance entries, got %d", total)
	}
}

func TestClearAndMonotonicSequence(t *testing.T) {
	resetBuffersForTest()

	_ = Emit(LevelError, "err-1")
	_ = Emit(LevelInfo, "info-1")
	if Len() != 2 {
		t.Fatalf("expected Len 2, got %d", Len())
	}

	cleared := Clear()
	if cleared != 2 {
		t.Fatalf("expected 2 cleared, got %d", cleared)
	}
	if Len() != 0 {
		t.Fatalf("expected Len 0 after Clear, got %d", Len())
	}

	// 清空后再次写入, seq 必须单调递增, 不能从 0 重置.
	_ = Emit(LevelError, "err-2")
	entries, _, _ := Snapshot(0, 10, Filter{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Seq <= 2 {
		t.Errorf("expected seq > 2 after clear, got %d", entries[0].Seq)
	}
}

func TestMessageTruncationAndEmptySkip(t *testing.T) {
	resetBuffersForTest()

	// 1. 空消息应跳过写入.
	if err := Emit(LevelError, ""); err != nil {
		t.Fatalf("Emit empty failed: %v", err)
	}
	if Len() != 0 {
		t.Fatalf("expected empty message to be skipped, len=%d", Len())
	}

	// 2. 超长消息截断至 MaxMessageBytes.
	longMsg := strings.Repeat("a", MaxMessageBytes+500)
	if err := Emit(LevelError, longMsg); err != nil {
		t.Fatalf("Emit long message failed: %v", err)
	}
	entries, _, _ := Snapshot(0, 10, Filter{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Message) != MaxMessageBytes {
		t.Errorf("expected message len %d, got %d", MaxMessageBytes, len(entries[0].Message))
	}
}

func TestConcurrentEmitsAndQueries(t *testing.T) {
	resetBuffersForTest()

	const goroutines = 8
	const perGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// 并发写 INFO.
	for g := range goroutines {
		go func(gid int) {
			defer wg.Done()
			for i := range perGoroutine {
				_ = EmitInstance(LevelInfo, "srv-c", fmt.Sprintf("info-%d-%d", gid, i))
			}
		}(g)
	}

	// 并发写 ERROR / WARN.
	for g := range goroutines {
		go func(gid int) {
			defer wg.Done()
			for i := range perGoroutine {
				level := LevelError
				if i%2 == 0 {
					level = LevelWarn
				}
				_ = EmitInstance(level, "srv-c", fmt.Sprintf("err-%d-%d", gid, i))
			}
		}(g)
	}

	// 同时并发读.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_, _, _ = Snapshot(0, 50, Filter{})
				_ = Count(Filter{})
				_ = ErrorCount(Filter{})
				_ = LatestSeq()
			}
		}
	}()

	wg.Wait()
	close(done)

	// 验证错误条数准确性.
	expectedErrors := goroutines * perGoroutine
	if count := ErrorCount(Filter{}); count != expectedErrors {
		t.Fatalf("expected %d errors, got %d", expectedErrors, count)
	}
}
