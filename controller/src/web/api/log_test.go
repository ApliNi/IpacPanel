package api

import (
	"IpacPanel/controller/src/logbuf"
	"encoding/json"
	"testing"
)

func TestLogEventPayloadSerialization(t *testing.T) {
	payload := logEventPayload{
		Version:    100,
		Count:      42,
		ErrorCount: 3,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal logEventPayload failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal logEventPayload failed: %v", err)
	}

	if parsed["version"] != float64(100) {
		t.Errorf("expected version 100, got %v", parsed["version"])
	}
	if parsed["count"] != float64(42) {
		t.Errorf("expected count 42, got %v", parsed["count"])
	}
	if parsed["error_count"] != float64(3) {
		t.Errorf("expected error_count 3, got %v", parsed["error_count"])
	}
}

func TestNormalizeLevelSetInApi(t *testing.T) {
	// ERROR 标签包含 error 与 warn.
	set, err := logbuf.NormalizeLevelSet([]string{"error"})
	if err != nil {
		t.Fatalf("NormalizeLevelSet failed: %v", err)
	}
	if _, ok := set[logbuf.LevelError]; !ok {
		t.Errorf("expected LevelError")
	}
	if _, ok := set[logbuf.LevelWarn]; !ok {
		t.Errorf("expected LevelWarn")
	}

	// INFO 标签仅包含 info.
	setInfo, err := logbuf.NormalizeLevelSet([]string{"info"})
	if err != nil {
		t.Fatalf("NormalizeLevelSet failed: %v", err)
	}
	if _, ok := setInfo[logbuf.LevelInfo]; !ok {
		t.Errorf("expected LevelInfo")
	}
	if len(setInfo) != 1 {
		t.Errorf("expected only 1 level in setInfo, got %d", len(setInfo))
	}
}

func TestLogGetResponseSerialization(t *testing.T) {
	resp := logGetResponse{
		Entries:    []logbuf.Entry{},
		TotalCount: 50,
		ErrorCount: 5,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal logGetResponse failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal logGetResponse failed: %v", err)
	}

	if parsed["total_count"] != float64(50) {
		t.Errorf("expected total_count 50, got %v", parsed["total_count"])
	}
	if parsed["error_count"] != float64(5) {
		t.Errorf("expected error_count 5, got %v", parsed["error_count"])
	}
}
