package main

import (
	"testing"
)

func BenchmarkDaemonBufferPoolZeroAllocs(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bufObj, buf := getDaemonOutputBuffer()
		if len(buf) == 0 {
			b.Fatal("empty buf")
		}
		resp := IPCResponse{
			Type:        "o",
			Instance:    "bench",
			Body:        buf[:100],
			ReleaseBuf:  bufObj,
			ReleasePool: &daemonOutputBufferPool,
		}
		resp.Release()
	}
}

func TestDaemonBufferPoolAcquireAndRelease(t *testing.T) {
	bufObj, buf := getDaemonOutputBuffer()
	if len(buf) != daemonOutputReadBufferSize {
		t.Fatalf("expected buffer size %d, got %d", daemonOutputReadBufferSize, len(buf))
	}
	resp := IPCResponse{
		Type:        "o",
		Instance:    "test",
		Body:        buf[:10],
		ReleaseBuf:  bufObj,
		ReleasePool: &daemonOutputBufferPool,
	}
	resp.Release()
	if resp.ReleaseBuf != nil || resp.ReleasePool != nil {
		t.Fatalf("expected release cleanup, got non-nil pointers")
	}
}
