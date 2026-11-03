//go:build windows

package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWindowsExecutable(t *testing.T) {
	tempDir := t.TempDir()

	openlistExe := filepath.Join(tempDir, "openlist.exe")
	if err := os.WriteFile(openlistExe, []byte("dummy"), 0755); err != nil {
		t.Fatalf("failed to create openlist.exe: %v", err)
	}

	resolved, err := resolveWindowsExecutable(tempDir, "openlist.exe")
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed: %v", err)
	}
	if !strings.EqualFold(resolved, openlistExe) {
		t.Errorf("expected %s, got %s", openlistExe, resolved)
	}

	resolved, err = resolveWindowsExecutable(tempDir, "openlist")
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed: %v", err)
	}
	if !strings.EqualFold(resolved, openlistExe) {
		t.Errorf("expected %s, got %s", openlistExe, resolved)
	}

	subDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}
	subExe := filepath.Join(subDir, "tool.exe")
	if err := os.WriteFile(subExe, []byte("dummy"), 0755); err != nil {
		t.Fatalf("failed to create tool.exe: %v", err)
	}

	resolved, err = resolveWindowsExecutable(tempDir, "bin/tool.exe")
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed: %v", err)
	}
	if !strings.EqualFold(resolved, subExe) {
		t.Errorf("expected %s, got %s", subExe, resolved)
	}

	resolved, err = resolveWindowsExecutable(tempDir, "bin/tool")
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed: %v", err)
	}
	if !strings.EqualFold(resolved, subExe) {
		t.Errorf("expected %s, got %s", subExe, resolved)
	}

	resolved, err = resolveWindowsExecutable(tempDir, "cmd.exe")
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed for cmd.exe: %v", err)
	}
	if !strings.EqualFold(filepath.Base(resolved), "cmd.exe") {
		t.Errorf("expected cmd.exe, got %s", resolved)
	}

	_, err = resolveWindowsExecutable(tempDir, "non_existent_binary_xyz_123.exe")
	if err == nil {
		t.Fatalf("expected error for non-existent executable, got nil")
	}

	_, err = resolveWindowsExecutable(tempDir, "   ")
	if err == nil {
		t.Fatalf("expected error for empty command, got nil")
	}
}
