package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// isolateConfig points telemetry at a fresh, non-existent config directory and
// clears the environment variables that could leak in from the surrounding
// shell or a previous test.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "telemetry-config")
	t.Setenv(ConfigDirEnv, dir)
	t.Setenv(EndpointEnv, "")
	t.Setenv(TelemetryEnv, "")
	return dir
}

// requireWriteConfig plants a raw config file to simulate hand edits/corruption.
func requireWriteConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func requireValidInstallID(t *testing.T, id string) {
	t.Helper()
	if !validInstallID(id) {
		t.Fatalf("install_id %q is not a valid UUID v4", id)
	}
	if id != strings.ToLower(id) {
		t.Fatalf("install_id %q must be lowercase", id)
	}
}

func TestDefaultStateIsDisabledAndWritesNothing(t *testing.T) {
	dir := isolateConfig(t)

	state, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state.Exists {
		t.Fatal("no config file should exist by default")
	}
	if state.Config.Enabled || state.Config.InstallID != "" || state.Config.Endpoint != "" {
		t.Fatalf("default config must be disabled/empty, got %+v", state.Config)
	}
	if state.EnvDisabled {
		t.Fatal("EnvDisabled must be false with an unset environment")
	}
	if want := filepath.Join(dir, configFileName); state.Path != want {
		t.Fatalf("config path = %q, want %q", state.Path, want)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("reading state must not create the config directory: %v", err)
	}
}

func TestEnablePersistsStableInstallIDAndDisableKeepsIt(t *testing.T) {
	dir := isolateConfig(t)

	enabled, err := Enable()
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !enabled.Exists || !enabled.Config.Enabled {
		t.Fatalf("Enable must persist an enabled config, got %+v", enabled)
	}
	requireValidInstallID(t, enabled.Config.InstallID)
	id := enabled.Config.InstallID

	path := filepath.Join(dir, configFileName)
	if enabled.Path != path {
		t.Fatalf("config path = %q, want %q", enabled.Path, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file must exist: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config file mode = %o, want 600", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var onDisk Config
	if err := yaml.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("config file is not valid YAML: %v", err)
	}
	if onDisk != enabled.Config {
		t.Fatalf("on-disk config = %+v, want %+v", onDisk, enabled.Config)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode config map: %v", err)
	}
	if doc["enabled"] != true {
		t.Fatalf("config file must contain enabled: true, got %v", doc["enabled"])
	}
	if doc["install_id"] != id {
		t.Fatalf("config file install_id = %v, want %q", doc["install_id"], id)
	}

	// The atomic write must not leave temporary siblings behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != configFileName {
		t.Fatalf("config dir should only contain %s, got %v", configFileName, entries)
	}

	// The install ID is stable across re-enable and reload.
	again, err := Enable()
	if err != nil {
		t.Fatalf("second Enable: %v", err)
	}
	if again.Config.InstallID != id {
		t.Fatalf("install_id changed on re-enable: %q -> %q", id, again.Config.InstallID)
	}
	loaded, exists, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !exists || !loaded.Enabled || loaded.InstallID != id {
		t.Fatalf("Load = %+v exists=%v, want enabled with install_id %q", loaded, exists, id)
	}

	disabled, err := Disable()
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !disabled.Exists || disabled.Config.Enabled {
		t.Fatalf("Disable must persist a disabled config, got %+v", disabled)
	}
	loaded, exists, err = Load()
	if err != nil || !exists {
		t.Fatalf("Load after disable: cfg=%+v exists=%v err=%v", loaded, exists, err)
	}
	if loaded.Enabled {
		t.Fatal("config must be disabled after disable")
	}
	if loaded.InstallID != id {
		t.Fatalf("disable must keep install_id %q, got %q", id, loaded.InstallID)
	}

	reenabled, err := Enable()
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if reenabled.Config.InstallID != id {
		t.Fatalf("install_id changed after disable/enable: %q -> %q", id, reenabled.Config.InstallID)
	}
}

func TestDisableWithoutConfigCreatesExplicitOptOut(t *testing.T) {
	dir := isolateConfig(t)

	state, err := Disable()
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !state.Exists || state.Config.Enabled || state.Config.InstallID != "" {
		t.Fatalf("explicit opt-out config = %+v", state)
	}
	if _, err := os.Stat(filepath.Join(dir, configFileName)); err != nil {
		t.Fatalf("disable must record the opt-out in a config file: %v", err)
	}
	status, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Exists || status.Config.Enabled {
		t.Fatalf("status after disable = %+v", status)
	}
}

func TestCorruptConfigBreaksCommands(t *testing.T) {
	dir := isolateConfig(t)
	requireWriteConfig(t, dir, "enabled: [1, 2\n")

	if _, err := Status(); err == nil {
		t.Fatal("Status must fail on a corrupt config")
	} else if !strings.Contains(err.Error(), "已损坏") {
		t.Fatalf("Status error should explain corruption: %v", err)
	}
	if _, err := Enable(); err == nil {
		t.Fatal("Enable must fail on a corrupt config")
	}
	if _, err := Disable(); err == nil {
		t.Fatal("Disable must fail on a corrupt config")
	}
	if _, _, err := Load(); err == nil {
		t.Fatal("Load must fail on a corrupt config")
	}
}

func TestInvalidConfigValuesAreRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"install id is not a uuid", "enabled: true\ninstall_id: not-a-uuid\n"},
		{"install id is not version 4", "enabled: true\ninstall_id: 6f2b0ad0-9a1c-3d2e-8f00-111122223333\n"},
		{"enabled without install id", "enabled: true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateConfig(t)
			requireWriteConfig(t, dir, tc.body)
			if _, err := Status(); err == nil {
				t.Fatal("Status must reject an invalid config")
			} else if !strings.Contains(err.Error(), "无效") {
				t.Fatalf("error should explain invalidity: %v", err)
			}
		})
	}
}

func TestConfigDirPrecedence(t *testing.T) {
	t.Run("explicit config dir wins", func(t *testing.T) {
		t.Setenv(ConfigDirEnv, "/tmp/telemetry-config-dir")
		t.Setenv(XDGConfigHomeEnv, "/tmp/telemetry-xdg")
		dir, err := Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		if want := filepath.Clean("/tmp/telemetry-config-dir"); dir != want {
			t.Fatalf("Dir = %q, want %q", dir, want)
		}
	})

	t.Run("xdg config home fallback", func(t *testing.T) {
		t.Setenv(ConfigDirEnv, " ")
		t.Setenv(XDGConfigHomeEnv, "/tmp/telemetry-xdg")
		dir, err := Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		if want := filepath.Join("/tmp/telemetry-xdg", appDirName); dir != want {
			t.Fatalf("Dir = %q, want %q", dir, want)
		}
	})

	t.Run("home fallback", func(t *testing.T) {
		t.Setenv(ConfigDirEnv, "")
		t.Setenv(XDGConfigHomeEnv, "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir, err := Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		if want := filepath.Join(home, ".config", appDirName); dir != want {
			t.Fatalf("Dir = %q, want %q", dir, want)
		}
	})
}

func TestDisabledByEnv(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"off", true},
		{"OFF", true},
		{" off ", true},
		{"", false},
		{"on", false},
		{"true", false},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(TelemetryEnv, tc.value)
			if got := DisabledByEnv(); got != tc.want {
				t.Fatalf("DisabledByEnv() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestEndpointEnvironmentOverride(t *testing.T) {
	isolateConfig(t)
	state := State{Config: Config{Endpoint: "https://config.example/ingest"}}

	t.Setenv(EndpointEnv, "https://env.example/ingest")
	if got := state.Endpoint(); got != "https://env.example/ingest" {
		t.Fatalf("env endpoint must win, got %q", got)
	}

	t.Setenv(EndpointEnv, " ")
	if got := state.Endpoint(); got != "https://config.example/ingest" {
		t.Fatalf("blank env endpoint must fall back to config, got %q", got)
	}

	t.Setenv(EndpointEnv, "")
	if got := (State{}).Endpoint(); got != "" {
		t.Fatalf("empty state endpoint = %q, want empty", got)
	}
}

func TestNewInstallIDIsRandomUUIDv4(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := newInstallID()
		if err != nil {
			t.Fatalf("newInstallID: %v", err)
		}
		requireValidInstallID(t, id)
		if seen[id] {
			t.Fatalf("duplicate install_id generated: %q", id)
		}
		seen[id] = true
	}
}
