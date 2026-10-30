// Package telemetry implements the opt-in anonymous telemetry contract from
// ADR-005 D6: a local config file, explicit enable/disable/status commands, a
// frozen event field whitelist, and best-effort delivery that is disabled by
// default.
//
// Telemetry never collects incident content. The only data that can leave the
// machine is the ten-field Event payload defined in event.go; nothing is sent
// or written unless the operator explicitly runs "kubectl-triage telemetry
// enable" and an endpoint is configured.
package telemetry

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Environment variables and file names that make up the public telemetry
// surface.
const (
	// ConfigDirEnv overrides the directory that holds config.yaml.
	ConfigDirEnv = "KUBECTL_TRIAGE_CONFIG_DIR"
	// XDGConfigHomeEnv is honoured when ConfigDirEnv is empty.
	XDGConfigHomeEnv = "XDG_CONFIG_HOME"
	// TelemetryEnv force-disables sending when set to "off" (case-insensitive).
	TelemetryEnv = "KUBECTL_TRIAGE_TELEMETRY"
	// EndpointEnv overrides Config.Endpoint; without an endpoint events are
	// dropped instead of stored or retried.
	EndpointEnv = "KUBECTL_TRIAGE_TELEMETRY_URL"

	appDirName     = "kubectl-triage"
	configFileName = "config.yaml"
	configDirMode  = 0o700
	configFileMode = 0o600
)

// FirstRunHint is the single non-interactive line printed to stderr by analyze
// while telemetry has never been configured. It is suppressed once enable or
// disable has written a config file, and when TelemetryEnv=off. Analyze never
// prompts the user.
const FirstRunHint = "匿名统计默认关闭；启用：kubectl-triage telemetry enable"

// configHeader is prepended to the YAML so the file documents its own purpose.
var configHeader = []byte(
	"# kubectl-triage opt-in anonymous telemetry (ADR-005 D6).\n" +
		"# Disabled by default. Only the documented field whitelist is ever sent.\n" +
		"# Run \"kubectl-triage telemetry disable\" (or delete this file) to opt out.\n",
)

// Config is the persisted telemetry configuration. Telemetry is disabled by
// default: only an explicit "kubectl-triage telemetry enable" sets Enabled.
type Config struct {
	Enabled   bool   `yaml:"enabled"`
	InstallID string `yaml:"install_id,omitempty"`
	Endpoint  string `yaml:"endpoint,omitempty"`
}

// State is the resolved telemetry state used by the CLI.
type State struct {
	// Config is the loaded configuration (zero value when no file exists).
	Config Config
	// Path is the resolved config.yaml path in use.
	Path string
	// Exists reports whether a config file is present.
	Exists bool
	// EnvDisabled reports whether TelemetryEnv=off force-disables sending.
	EnvDisabled bool
}

// Endpoint returns the effective ingestion endpoint. The environment variable
// takes precedence over the config file; an empty result means events are
// dropped.
func (s State) Endpoint() string { return effectiveEndpoint(s.Config) }

// Dir returns the directory that holds config.yaml:
// $KUBECTL_TRIAGE_CONFIG_DIR, else $XDG_CONFIG_HOME/kubectl-triage, else
// ~/.config/kubectl-triage.
func Dir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(ConfigDirEnv)); dir != "" {
		return filepath.Clean(dir), nil
	}
	if xdg := strings.TrimSpace(os.Getenv(XDGConfigHomeEnv)); xdg != "" {
		return filepath.Join(xdg, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定遥测配置目录：请设置 %s 或 %s（%w）", ConfigDirEnv, XDGConfigHomeEnv, err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("无法确定遥测配置目录：请设置 %s 或 %s", ConfigDirEnv, XDGConfigHomeEnv)
	}
	return filepath.Join(home, ".config", appDirName), nil
}

// ConfigPath returns the full path of config.yaml.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load reads the telemetry config. A missing file is not an error: it yields
// the default (disabled) config and exists=false. A malformed or invalid file
// returns an error; analyze must ignore that error and keep running.
func Load() (cfg Config, exists bool, err error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, false, err
	}
	return loadFile(path)
}

func loadFile(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("读取遥测配置 %s 失败：%w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, true, fmt.Errorf("遥测配置 %s 已损坏：%w", path, err)
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, true, fmt.Errorf("遥测配置 %s 无效：%w", path, err)
	}
	return cfg, true, nil
}

// Save atomically persists cfg (temp file + rename, 0600) in the resolved
// config directory, creating the directory (0700) when needed.
func Save(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, cfg)
}

// writeFileAtomic writes the file through a temporary sibling so a crash can
// never leave a half-written config behind.
func writeFileAtomic(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("编码遥测配置失败：%w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return fmt.Errorf("创建遥测配置目录 %s 失败：%w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("创建遥测配置临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: after a successful rename the temp name no longer
	// exists, so this is a no-op.
	defer func() { _ = os.Remove(tmpName) }()

	content := make([]byte, 0, len(configHeader)+len(data))
	content = append(content, configHeader...)
	content = append(content, data...)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入遥测配置失败：%w", err)
	}
	if err := tmp.Chmod(configFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置遥测配置权限失败：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("刷新遥测配置失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭遥测配置临时文件失败：%w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("保存遥测配置 %s 失败：%w", path, err)
	}
	return nil
}

// Status resolves the current telemetry state without writing anything.
func Status() (State, error) {
	path, err := ConfigPath()
	if err != nil {
		return State{}, err
	}
	cfg, exists, err := loadFile(path)
	if err != nil {
		return State{}, err
	}
	return State{Config: cfg, Path: path, Exists: exists, EnvDisabled: DisabledByEnv()}, nil
}

// Enable turns telemetry on and generates a random install ID on first use.
// The install ID is stable across disable/enable cycles.
func Enable() (State, error) {
	state, err := Status()
	if err != nil {
		return State{}, err
	}
	cfg := state.Config
	if cfg.InstallID == "" {
		id, idErr := newInstallID()
		if idErr != nil {
			return State{}, idErr
		}
		cfg.InstallID = id
	}
	cfg.Enabled = true
	if err := Save(cfg); err != nil {
		return State{}, err
	}
	state.Config = cfg
	state.Exists = true
	return state, nil
}

// Disable turns telemetry off. The install ID is kept so a later enable reuses
// the same pseudonymous identifier; the config file records the explicit
// opt-out.
func Disable() (State, error) {
	state, err := Status()
	if err != nil {
		return State{}, err
	}
	cfg := state.Config
	cfg.Enabled = false
	if err := Save(cfg); err != nil {
		return State{}, err
	}
	state.Config = cfg
	state.Exists = true
	return state, nil
}

// DisabledByEnv reports whether TelemetryEnv=off force-disables sending.
func DisabledByEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(TelemetryEnv)), "off")
}

// effectiveEndpoint resolves the endpoint with the environment override first.
func effectiveEndpoint(cfg Config) string {
	if endpoint := strings.TrimSpace(os.Getenv(EndpointEnv)); endpoint != "" {
		return endpoint
	}
	return strings.TrimSpace(cfg.Endpoint)
}

// newInstallID returns a random UUID v4 generated with crypto/rand.
func newInstallID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成匿名安装 ID 失败：%w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// validInstallID reports whether s is a canonical UUID v4 string.
func validInstallID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			c := s[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	if s[14] != '4' {
		return false
	}
	switch s[19] {
	case '8', '9', 'a', 'A', 'b', 'B':
		return true
	default:
		return false
	}
}

func validateConfig(cfg Config) error {
	if cfg.InstallID != "" && !validInstallID(cfg.InstallID) {
		return fmt.Errorf("install_id 不是合法的 UUID v4：%q", cfg.InstallID)
	}
	if cfg.Enabled && cfg.InstallID == "" {
		return errors.New("enabled=true 但缺少 install_id；请运行 kubectl-triage telemetry enable 重新生成配置")
	}
	return nil
}
