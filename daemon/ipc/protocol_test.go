package ipc

import (
	"bytes"
	"testing"
)

func TestWriteAndReadInstanceStream(t *testing.T) {
	var buf bytes.Buffer
	conn := NewConn(&buf, &buf, nil)

	data := []byte("hello world")
	resp := Response{
		Type:     "o",
		Instance: "test-instance",
		Body:     data,
	}
	if err := conn.WriteInstanceOutputResponse(resp, true); err != nil {
		t.Fatalf("WriteInstanceOutputResponse failed: %v", err)
	}

	header, err := conn.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}
	if header.Type != FrameTypeInstanceOut {
		t.Fatalf("unexpected frame type: %d", header.Type)
	}

	instance, payload, err := conn.ReadInstancePayload(header.PayloadLen, nil, nil)
	if err != nil {
		t.Fatalf("ReadInstancePayload failed: %v", err)
	}
	if instance != "test-instance" {
		t.Fatalf("expected test-instance, got %s", instance)
	}
	if string(payload) != "hello world" {
		t.Fatalf("expected hello world, got %s", string(payload))
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func BenchmarkWriteInstanceOutputZeroAllocs(b *testing.B) {
	conn := NewConn(nil, discardWriter{}, nil)
	data := []byte("benchmark test output log data")
	instance := "bench-instance"

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		resp := Response{
			Type:     "o",
			Instance: instance,
			Body:     data,
		}
		if err := conn.WriteInstanceOutputResponse(resp, false); err != nil {
			b.Fatalf("WriteInstanceOutputResponse failed: %v", err)
		}
	}
}

func BenchmarkHeaderEncodeZeroAllocs(b *testing.B) {
	conn := NewConn(nil, discardWriter{}, nil)
	h := Header{
		Magic:      MagicByte,
		Type:       FrameTypeInstanceOut,
		PayloadLen: 128,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		conn.writeHeaderBuf(h)
	}
}
