package sysnet

import (
	"bytes"
	"testing"
)

func TestReadSocketCounts(t *testing.T) {
	counts, err := ReadSocketCounts()
	if err != nil {
		t.Fatalf("ReadSocketCounts failed: %v", err)
	}

	t.Logf("Socket counts: TCP=%d, UDP=%d", counts.TCP, counts.UDP)

	if counts.TCP == 0 && counts.UDP == 0 {
		t.Fatalf("expected non-zero socket counts, got TCP=%d, UDP=%d", counts.TCP, counts.UDP)
	}
}

func TestParseSockstatData(t *testing.T) {
	data4 := []byte("sockets: used 112\nTCP: inuse 4 orphan 0 tw 1 alloc 7 mem 2\nUDP: inuse 3 mem 1\nUDPLITE: inuse 0\nRAW: inuse 0\nFRAG: inuse 0 memory 0\n")
	data6 := []byte("TCP6: inuse 2\nUDP6: inuse 1\n")

	var counts Counts
	if err := parseSockstatData(data4, &counts); err != nil {
		t.Fatalf("parseSockstatData failed: %v", err)
	}
	if err := parseSockstatData(data6, &counts); err != nil {
		t.Fatalf("parseSockstatData failed: %v", err)
	}

	if counts.TCP != 6 || counts.UDP != 4 {
		t.Fatalf("expected TCP=6, UDP=4; got TCP=%d, UDP=%d", counts.TCP, counts.UDP)
	}
}

func TestParseSockstatStream(t *testing.T) {
	data4 := []byte("sockets: used 112\nTCP: inuse 10 orphan 0 tw 1 alloc 7 mem 2\nUDP: inuse 5 mem 1\n")
	data6 := []byte("TCP6: inuse 3\nUDP6: inuse 2\n")

	var counts Counts
	if err := parseSockstatStream(bytes.NewReader(data4), &counts); err != nil {
		t.Fatalf("parseSockstatStream failed: %v", err)
	}
	if err := parseSockstatStream(bytes.NewReader(data6), &counts); err != nil {
		t.Fatalf("parseSockstatStream failed: %v", err)
	}

	if counts.TCP != 13 || counts.UDP != 7 {
		t.Fatalf("expected TCP=13, UDP=7; got TCP=%d, UDP=%d", counts.TCP, counts.UDP)
	}
}

func BenchmarkParseSockstatData(b *testing.B) {
	data := []byte("sockets: used 112\nTCP: inuse 4 orphan 0 tw 1 alloc 7 mem 2\nUDP: inuse 3 mem 1\nUDPLITE: inuse 0\nRAW: inuse 0\nFRAG: inuse 0 memory 0\n")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var counts Counts
		_ = parseSockstatData(data, &counts)
	}
}

func BenchmarkReadSocketCounts(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		counts, err := ReadSocketCounts()
		if err != nil {
			b.Fatalf("ReadSocketCounts failed: %v", err)
		}
		_ = counts
	}
}
