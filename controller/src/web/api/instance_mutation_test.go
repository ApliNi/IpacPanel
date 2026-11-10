package api

import (
	cfg "IpacPanel/controller/src/config"
	"os"
	"path/filepath"
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
