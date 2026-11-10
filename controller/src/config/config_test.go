package config

import (
	"IpacPanel/daemon/version"
	"strings"
	"testing"
)

func TestValidateSettingsInstanceUpdateStagingDir(t *testing.T) {
	validCases := []string{
		"!InstanceUpdate",
		"update",
		"updates_2026",
		"my-update-dir",
		"",
		"   ",
	}
	for _, tc := range validCases {
		if err := ValidateSettingsInstanceUpdateStagingDir(tc); err != nil {
			t.Errorf("expected %q to be valid, got: %v", tc, err)
		}
	}

	invalidCases := []string{
		"./!InstanceUpdate/",
		"!InstanceUpdate/",
		"/!InstanceUpdate",
		"sub/dir",
		"sub\\dir",
		".",
		"..",
		"trailing.",
		"CON",
		"prn",
		"aux.txt",
		"COM1",
		"invalid*char",
		"invalid?char",
		"invalid<char",
		"invalid>char",
		"invalid|char",
		"invalid:char",
		"invalid\"char",
		strings.Repeat("a", 256),
	}
	for _, tc := range invalidCases {
		if err := ValidateSettingsInstanceUpdateStagingDir(tc); err == nil {
			t.Errorf("expected %q to be invalid, got nil error", tc)
		}
	}
}

func TestMigrateInstanceUpdateStagingDir(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"./!InstanceUpdate/", "!InstanceUpdate"},
		{"!InstanceUpdate/", "!InstanceUpdate"},
		{"!InstanceUpdate", "!InstanceUpdate"},
		{".\\!InstanceUpdate\\", "!InstanceUpdate"},
		{"./some/sub/dir/", "dir"},
		{"", "!InstanceUpdate"},
		{"   ", "!InstanceUpdate"},
		{".", "!InstanceUpdate"},
		{"..", "!InstanceUpdate"},
		{"/trailing./", "!InstanceUpdate"},
		{"CON", "!InstanceUpdate"},
	}
	for _, tc := range cases {
		actual := migrateInstanceUpdateStagingDir(tc.input)
		if actual != tc.expected {
			t.Errorf("migrateInstanceUpdateStagingDir(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}

func TestMigrateConfig(t *testing.T) {
	oldCfg := Config{
		DataVersion:              1,
		InstanceUpdateStagingDir: "./!InstanceUpdate/",
	}
	migrated := migrateConfig(oldCfg)
	if migrated.DataVersion != version.ControllerDataVersion {
		t.Errorf("expected DataVersion %d, got %d", version.ControllerDataVersion, migrated.DataVersion)
	}
	if migrated.InstanceUpdateStagingDir != "!InstanceUpdate" {
		t.Errorf("expected InstanceUpdateStagingDir !InstanceUpdate, got %q", migrated.InstanceUpdateStagingDir)
	}
}

func TestUnmarshalPersistedConfigAutoCreate(t *testing.T) {
	yamlData := []byte(`
data_version: 2
web_title: TestPanel
listen: 127.0.0.1:25555
instance_update_staging_dir: MyUpdate
instance_update_auto_create: true
`)
	cfg, err := unmarshalPersistedConfig(yamlData)
	if err != nil {
		t.Fatalf("unmarshalPersistedConfig failed: %v", err)
	}
	if cfg.InstanceUpdateStagingDir != "MyUpdate" {
		t.Errorf("expected InstanceUpdateStagingDir MyUpdate, got %q", cfg.InstanceUpdateStagingDir)
	}
	if !cfg.InstanceUpdateAutoCreate {
		t.Errorf("expected InstanceUpdateAutoCreate true, got false")
	}

	marshaled, err := marshalPersistedConfig(cfg)
	if err != nil {
		t.Fatalf("marshalPersistedConfig failed: %v", err)
	}
	if !strings.Contains(string(marshaled), "instance_update_auto_create: true") {
		t.Errorf("expected marshaled output to contain 'instance_update_auto_create: true', got:\n%s", string(marshaled))
	}
}
