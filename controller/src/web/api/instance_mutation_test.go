package api

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateInstanceUpdateStagingDir(t *testing.T) {
	tempDir := t.TempDir()
	instanceDir := filepath.Join(tempDir, "instance_test")
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		t.Fatalf("failed to create temp instance dir: %v", err)
	}

	cfg.ManagerMu.Lock()
	cfg.CurrentConfig.InstanceUpdateAutoCreate = false
	cfg.CurrentConfig.InstanceUpdateStagingDir = "!InstanceUpdate"
	cfg.ManagerMu.Unlock()

	// 1. When InstanceUpdateAutoCreate is false, createInstanceUpdateStagingDir should not create the staging dir
	if err := createInstanceUpdateStagingDir(instanceDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stagingPath := filepath.Join(instanceDir, "!InstanceUpdate")
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Errorf("expected staging dir to not exist when auto-create is disabled")
	}

	// 2. createInstanceDirAfterPlan should only create the instance dir, NOT the staging dir
	cfg.ManagerMu.Lock()
	cfg.CurrentConfig.InstanceUpdateAutoCreate = true
	cfg.ManagerMu.Unlock()

	instanceDir2 := filepath.Join(tempDir, "instance_test_2")
	created, err := createInstanceDirAfterPlan(instanceDir2)
	if err != nil {
		t.Fatalf("createInstanceDirAfterPlan failed: %v", err)
	}
	if !created {
		t.Errorf("expected created to be true for new instance dir")
	}
	stagingPath2 := filepath.Join(instanceDir2, "!InstanceUpdate")
	if _, err := os.Stat(stagingPath2); !os.IsNotExist(err) {
		t.Errorf("expected createInstanceDirAfterPlan to NOT create staging dir")
	}

	// 3. createInstanceUpdateStagingDir should create the staging dir when auto-create is enabled
	if err := createInstanceUpdateStagingDir(instanceDir2); err != nil {
		t.Fatalf("createInstanceUpdateStagingDir failed: %v", err)
	}
	if info, err := os.Stat(stagingPath2); err != nil || !info.IsDir() {
		t.Errorf("expected staging dir to exist after createInstanceUpdateStagingDir, err: %v", err)
	}
}
func TestInstanceConfigRequestUsesNameField(t *testing.T) {
	rawWithName := []byte(`{"name":"my-server","path":"./instances/my-server","command":"run"}`)
	var req1 instanceConfigRequest
	if err := json.Unmarshal(rawWithName, &req1); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	ins1 := instanceConfigFromRequest(req1)
	if ins1.Name != "my-server" {
		t.Errorf("expected Name 'my-server', got %q", ins1.Name)
	}

	rawWithLegacyInstance := []byte(`{"instance":"legacy-server","path":"./instances/legacy-server","command":"run"}`)
	var req2 instanceConfigRequest
	if err := json.Unmarshal(rawWithLegacyInstance, &req2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	ins2 := instanceConfigFromRequest(req2)
	if ins2.Name != "" {
		t.Errorf("expected legacy 'instance' key to be ignored and Name empty, got %q", ins2.Name)
	}
}

func TestDashboardSnapshotRouteRemoved(t *testing.T) {
	hasEvents := false
	for _, r := range Routes {
		if r.Path == "/api/dashboard/snapshot" {
			t.Errorf("legacy route /api/dashboard/snapshot must not exist")
		}
		if r.Path == "/api/dashboard/events" {
			hasEvents = true
		}
	}
	if !hasEvents {
		t.Errorf("expected /api/dashboard/events route to exist")
	}
}

func TestInstanceConfigRequestNotesField(t *testing.T) {
	raw := []byte(`{"name":"test-ins","path":"./instances/test-ins","command":"run","notes":"hello\r\nworld"}`)
	var req instanceConfigRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if req.Notes != "hello\r\nworld" {
		t.Errorf("expected Notes 'hello\\r\\nworld', got %q", req.Notes)
	}

	ins := instanceConfigFromRequest(req)
	if ins.Notes != "hello\r\nworld" {
		t.Errorf("expected ins.Notes 'hello\\r\\nworld', got %q", ins.Notes)
	}

	cfg.NormalizeInstanceRequest(&ins)
	if ins.Notes != "hello\nworld" {
		t.Errorf("expected normalized notes 'hello\\nworld', got %q", ins.Notes)
	}
}

func TestWriteInstanceConfigValidationErrorNotesTooLong(t *testing.T) {
	rec := httptest.NewRecorder()
	writeInstanceConfigValidationError(rec, errors.New(msg.NotesTooLong))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, msg.NotesTooLong) {
		t.Errorf("expected response to contain %q, got %q", msg.NotesTooLong, body)
	}
}

func TestInstanceDetailResponseNotesSerialization(t *testing.T) {
	resp := instanceDetailResponse{
		instanceListItem: instanceListItem{Name: "demo"},
		Path:             "./instances/demo",
		Command:          "run",
		Notes:            "my notes",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed["notes"] != "my notes" {
		t.Errorf("expected notes 'my notes', got %v", parsed["notes"])
	}

	emptyResp := instanceDetailResponse{
		instanceListItem: instanceListItem{Name: "demo"},
	}
	emptyData, err := json.Marshal(emptyResp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var emptyParsed map[string]interface{}
	if err := json.Unmarshal(emptyData, &emptyParsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if _, ok := emptyParsed["notes"]; ok {
		t.Errorf("expected empty notes to be omitted from JSON")
	}
}
