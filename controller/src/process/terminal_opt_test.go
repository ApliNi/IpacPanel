package process

import (
	"bytes"
	"testing"
)

func TestPTYAlternateScreenFastPathZeroAllocs(t *testing.T) {
	plain := []byte("hello world, this is a plain text line without escapes\n")
	ansiColored := []byte("\x1b[32m[INFO]\x1b[0m user logged in successfully at \x1b[1;34m12:00:00\x1b[0m\n")

	// Fast path in normal screen with plain text.
	out, active, pending := filterPTYAlternateScreenHistory(false, nil, plain)
	if active || pending != nil || !bytes.Equal(out, plain) {
		t.Fatalf("unexpected result: active=%v, pending=%v, out=%q", active, pending, out)
	}

	allocs := testing.AllocsPerRun(100, func() {
		filterPTYAlternateScreenHistory(false, nil, plain)
	})
	if allocs != 0 {
		t.Errorf("expected 0 allocs for plain text, got %v", allocs)
	}

	// Fast path in normal screen with ANSI color escapes.
	out, active, pending = filterPTYAlternateScreenHistory(false, nil, ansiColored)
	if active || pending != nil || !bytes.Equal(out, ansiColored) {
		t.Fatalf("unexpected result: active=%v, pending=%v, out=%q", active, pending, out)
	}

	allocs = testing.AllocsPerRun(100, func() {
		filterPTYAlternateScreenHistory(false, nil, ansiColored)
	})
	if allocs != 0 {
		t.Errorf("expected 0 allocs for ANSI color text, got %v", allocs)
	}

	// Fast path in alternate screen with plain text.
	out, active, pending = filterPTYAlternateScreenHistory(true, nil, plain)
	if !active || pending != nil || len(out) != 0 {
		t.Fatalf("unexpected result: active=%v, pending=%v, out=%q", active, pending, out)
	}

	allocs = testing.AllocsPerRun(100, func() {
		filterPTYAlternateScreenHistory(true, nil, plain)
	})
	if allocs != 0 {
		t.Errorf("expected 0 allocs in alternate screen, got %v", allocs)
	}

	// Fast path in alternate screen with ANSI color escapes without switch.
	out, active, pending = filterPTYAlternateScreenHistory(true, nil, ansiColored)
	if !active || pending != nil || len(out) != 0 {
		t.Fatalf("unexpected result: active=%v, pending=%v, out=%q", active, pending, out)
	}

	allocs = testing.AllocsPerRun(100, func() {
		filterPTYAlternateScreenHistory(true, nil, ansiColored)
	})
	if allocs != 0 {
		t.Errorf("expected 0 allocs in alternate screen with ANSI colors, got %v", allocs)
	}
}

func TestPTYAlternateScreenTransitions(t *testing.T) {
	// Enter alternate screen (?1049h).
	enterSeq := []byte("prefix\x1b[?1049halternate content")
	out, active, pending := filterPTYAlternateScreenHistory(false, nil, enterSeq)
	if !active {
		t.Fatalf("expected active=true")
	}
	if pending != nil {
		t.Fatalf("expected pending=nil")
	}
	if string(out) != "prefix" {
		t.Fatalf("expected out to be 'prefix', got %q", string(out))
	}

	// Exit alternate screen (?1049l).
	exitSeq := []byte("screen content\x1b[?1049lsuffix")
	out, active, pending = filterPTYAlternateScreenHistory(true, nil, exitSeq)
	if active {
		t.Fatalf("expected active=false")
	}
	if pending != nil {
		t.Fatalf("expected pending=nil")
	}
	if string(out) != "suffix" {
		t.Fatalf("expected out to be 'suffix', got %q", string(out))
	}

	// Other alternate screen codes (?47h, ?1047h, ?1048h).
	for _, code := range []string{"47", "1047", "1048", "1049"} {
		seq := []byte("\x1b[?" + code + "h")
		_, act, _ := filterPTYAlternateScreenHistory(false, nil, seq)
		if !act {
			t.Errorf("expected active=true for ?%sh", code)
		}
		seqExit := []byte("\x1b[?" + code + "l")
		_, actExit, _ := filterPTYAlternateScreenHistory(true, nil, seqExit)
		if actExit {
			t.Errorf("expected active=false for ?%sl", code)
		}
	}
}

func TestPTYAlternateScreenPendingSplit(t *testing.T) {
	// Incomplete escape sequence across chunks.
	chunk1 := []byte("hello\x1b[?")
	out1, active1, pending1 := filterPTYAlternateScreenHistory(false, nil, chunk1)
	if active1 {
		t.Fatalf("expected active=false")
	}
	if string(out1) != "hello" {
		t.Fatalf("expected out1='hello', got %q", string(out1))
	}
	if string(pending1) != "\x1b[?" {
		t.Fatalf("expected pending1='\\x1b[?', got %q", string(pending1))
	}

	chunk2 := []byte("1049hworld")
	out2, active2, pending2 := filterPTYAlternateScreenHistory(active1, pending1, chunk2)
	if !active2 {
		t.Fatalf("expected active2=true")
	}
	if pending2 != nil {
		t.Fatalf("expected pending2=nil")
	}
	if len(out2) != 0 {
		t.Fatalf("expected out2 to be empty, got %q", string(out2))
	}
}

func TestTieredIPCBufferPool(t *testing.T) {
	testSizes := []struct {
		requested int
		expected  int
	}{
		{64, 4 * 1024},
		{4 * 1024, 4 * 1024},
		{4*1024 + 1, 16 * 1024},
		{16 * 1024, 16 * 1024},
		{16*1024 + 1, 64 * 1024},
		{64 * 1024, 64 * 1024},
		{64*1024 + 1, 256 * 1024},
		{256 * 1024, 256 * 1024},
		{256*1024 + 1, 512 * 1024},
		{512 * 1024, 512 * 1024},
	}

	for _, tc := range testSizes {
		buf := getDaemonOutputBuffer(tc.requested)
		if len(buf) != tc.requested {
			t.Errorf("size %d: expected len %d, got %d", tc.requested, tc.requested, len(buf))
		}
		if cap(buf) != tc.expected {
			t.Errorf("size %d: expected cap %d, got %d", tc.requested, tc.expected, cap(buf))
		}
		putDaemonOutputBuffer(buf)
	}

	// Test unpooled size (> 512KB).
	hugeSize := 600 * 1024
	hugeBuf := getDaemonOutputBuffer(hugeSize)
	if len(hugeBuf) != hugeSize || cap(hugeBuf) < hugeSize {
		t.Errorf("expected huge buffer of at least %d, got len=%d, cap=%d", hugeSize, len(hugeBuf), cap(hugeBuf))
	}
	putDaemonOutputBuffer(hugeBuf)

	// Test 0 allocations for repeated get/put.
	for _, size := range []int{100, 8000, 32000, 100000, 400000} {
		allocs := testing.AllocsPerRun(100, func() {
			b := getDaemonOutputBuffer(size)
			putDaemonOutputBuffer(b)
		})
		if allocs != 0 {
			t.Errorf("size %d: expected 0 allocs on get/put, got %v", size, allocs)
		}
	}
}

func TestTerminalHistoryShrinkToFit(t *testing.T) {
	h := &TerminalHistory{}

	// Shrink on empty history.
	h.ShrinkToFit()
	if h.buf != nil {
		t.Fatalf("expected nil buf on empty shrink, got cap=%d", cap(h.buf))
	}

	limit := 1024 // capLimit = 2048
	data := bytes.Repeat([]byte("a"), 200)
	h.Append(data, limit)

	if len(h.buf) != 200 || cap(h.buf) != 2*limit {
		t.Fatalf("expected len 200, cap %d, got len=%d, cap=%d", 2*limit, len(h.buf), cap(h.buf))
	}

	// ShrinkToFit should reduce cap to active length (200).
	h.ShrinkToFit()
	if len(h.buf) != 200 || cap(h.buf) != 200 {
		t.Fatalf("after ShrinkToFit: expected len=200, cap=200, got len=%d, cap=%d", len(h.buf), cap(h.buf))
	}
	if string(h.buf) != string(data) {
		t.Fatalf("data corrupted after shrink")
	}

	// Test ShrinkToFit when h.start > 0 (dead prefix exists).
	data2 := bytes.Repeat([]byte("b"), 900)
	h.Append(data2, limit) // activeLen = 200 + 900 = 1100 > limit (1024). Excess = 76.
	if h.start <= 0 {
		t.Fatalf("expected h.start > 0 after overflow, got %d", h.start)
	}

	activeLenBefore := len(h.buf) - h.start
	snapBefore, _ := h.Snapshot()

	h.ShrinkToFit()
	if h.start != 0 {
		t.Fatalf("expected h.start == 0 after ShrinkToFit, got %d", h.start)
	}
	if len(h.buf) != activeLenBefore || cap(h.buf) != activeLenBefore {
		t.Fatalf("expected len=cap=%d, got len=%d, cap=%d", activeLenBefore, len(h.buf), cap(h.buf))
	}
	snapAfter, _ := h.Snapshot()
	if !bytes.Equal(snapBefore, snapAfter) {
		t.Fatalf("snapshot mismatch after ShrinkToFit")
	}

	// Append after shrink should re-expand to capLimit.
	h.Append([]byte("c"), limit)
	if cap(h.buf) != 2*limit {
		t.Fatalf("expected cap re-expansion to %d, got %d", 2*limit, cap(h.buf))
	}

	// Reset with empty data frees buffer.
	h.Reset(nil, limit)
	if h.buf != nil {
		t.Fatalf("expected nil buf after Reset(nil), got cap=%d", cap(h.buf))
	}
}

func BenchmarkPTYFilterFastPath(b *testing.B) {
	plain := []byte("2026-10-21 12:00:00 [INFO] Worker process initialized successfully with pid 12345\n")
	b.ReportAllocs()
	for b.Loop() {
		out, _, _ := filterPTYAlternateScreenHistory(false, nil, plain)
		_ = out
	}
}

func BenchmarkTieredIPCPool(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		buf := getDaemonOutputBuffer(1024)
		putDaemonOutputBuffer(buf)
	}
}
