package config

import (
	"IpacPanel/daemon/version"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestCleanStringList(t *testing.T) {
	input := []string{"  foo  ", "", "bar", "foo", "  ", "baz", "bar"}
	expected := []string{"foo", "bar", "baz"}
	actual := CleanStringList(input)
	if len(actual) != len(expected) {
		t.Fatalf("expected len %d, got %d: %v", len(expected), len(actual), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expected[i], actual[i])
		}
	}

	emptyActual := CleanStringList(nil)
	if len(emptyActual) != 0 {
		t.Errorf("expected empty slice for nil input, got %v", emptyActual)
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
func TestUnmarshalConfigIgnoresLegacyNestedKeys(t *testing.T) {
	yamlData := []byte(`
data_version: 2
web_title: TestPanel
auth:
  - user: admin
    pass: secret
instances:
  - name: inst1
    path: ./instances/inst1
`)
	var cfg Config
	if err := yaml.Unmarshal(yamlData, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if len(cfg.Auth) != 0 {
		t.Errorf("expected Auth to remain empty when nested in config.yml, got %d items", len(cfg.Auth))
	}
	if len(cfg.Instances) != 0 {
		t.Errorf("expected Instances to remain empty when nested in config.yml, got %d items", len(cfg.Instances))
	}
}

func TestInstanceTasksStrictRestartActionRejected(t *testing.T) {
	tasks := NormalizeInstanceTasks([]Task{
		{
			Name:    "t1",
			Enabled: true,
			Expr:    "0 * * * *",
			Action:  "strict_restart",
		},
	})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Action != "strict_restart" || tasks[0].StrictRestart {
		t.Errorf("expected strict_restart action alias not to be converted, got action=%q strict_restart=%v", tasks[0].Action, tasks[0].StrictRestart)
	}
	if err := validateInstanceTasks(tasks); err == nil {
		t.Errorf("expected strict_restart action to fail validation")
	}

	validTasks := []Task{
		{
			Name:          "t2",
			Enabled:       true,
			Expr:          "0 * * * *",
			Action:        "restart",
			StrictRestart: true,
		},
	}
	if err := validateInstanceTasks(validTasks); err != nil {
		t.Errorf("expected restart with StrictRestart=true to be valid, got: %v", err)
	}
}

func TestAuthPasswordStrictP2(t *testing.T) {
	hashed, err := HashPassword("mySecretPass123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !strings.HasPrefix(hashed, storedHashPrefix+storedPasswordV2Prefix) {
		t.Fatalf("expected HASH/P2/ prefix, got %q", hashed)
	}
	if !CheckPassword(hashed, "mySecretPass123") {
		t.Errorf("expected CheckPassword to succeed for valid P2 hash")
	}
	if CheckPassword(hashed, "wrongPass") {
		t.Errorf("expected CheckPassword to fail for wrong password")
	}

	rawBcrypt := strings.TrimPrefix(hashed, storedHashPrefix+storedPasswordV2Prefix)
	legacyV1 := storedHashPrefix + rawBcrypt
	if CheckPassword(rawBcrypt, "mySecretPass123") {
		t.Errorf("expected bare bcrypt hash to be rejected")
	}
	if CheckPassword(legacyV1, "mySecretPass123") {
		t.Errorf("expected legacy V1 HASH/<bcrypt> format to be rejected")
	}

	usersLegacy := []AuthUser{{User: "u1", Pass: legacyV1}}
	if _, err := NormalizeAuthUserPasswords(usersLegacy); err == nil {
		t.Errorf("expected NormalizeAuthUserPasswords to reject legacy V1 hash")
	}

	usersBare := []AuthUser{{User: "u2", Pass: rawBcrypt}}
	if _, err := NormalizeAuthUserPasswords(usersBare); err == nil {
		t.Errorf("expected NormalizeAuthUserPasswords to reject bare bcrypt hash")
	}

	usersPlain := []AuthUser{{User: "u3", Pass: "plainPass123"}}
	changed, err := NormalizeAuthUserPasswords(usersPlain)
	if err != nil {
		t.Fatalf("expected plain password normalization to succeed, got: %v", err)
	}
	if !changed || !strings.HasPrefix(usersPlain[0].Pass, storedHashPrefix+storedPasswordV2Prefix) {
		t.Errorf("expected plain password to be hashed with P2 prefix, changed=%v pass=%q", changed, usersPlain[0].Pass)
	}
}
