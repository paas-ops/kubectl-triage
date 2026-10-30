package push

import (
	"net/url"
	"path"
	"strings"
	"time"
)

// ProtocolVersion is the frozen public push protocol version (ADR-006 D2/D4).
const ProtocolVersion = "1.0"

const (
	// DefaultTimeout bounds a whole push run, including retries and evidence
	// packing (ADR-006 D3).
	DefaultTimeout = 30 * time.Second
	// DefaultMaxBundleBytes caps the optional gzipped evidence bundle. The CLI
	// uses the same default for --max-bundle-bytes.
	DefaultMaxBundleBytes = 5 << 20

	// EndpointEnv and TokenEnv are the only accepted configuration sources
	// besides the --endpoint flag. The token never has a flag.
	EndpointEnv = "KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT"
	TokenEnv    = "KUBECTL_TRIAGE_ENTERPRISE_TOKEN"
)

// Config is the explicit enterprise-push configuration. The token is only
// accepted from the environment by the CLI and is never written to a report,
// dry-run summary, payload, or error message.
type Config struct {
	Endpoint        string
	Token           string
	Timeout         time.Duration
	IncludeEvidence bool
	MaxBundleBytes  int64
	// Now and PushID are deterministic injection points for tests; zero values
	// mean time.Now().UTC() and a freshly generated UUID v4.
	Now    func() time.Time
	PushID string
}

// Validate reports whether the configuration can be used without touching the
// network. It returns a *ConfigError for every documented configuration
// mistake (CLI exit code 1).
func (c Config) Validate() error {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return &ConfigError{Reason: "缺少企业端点：请通过 --endpoint 或环境变量 " + EndpointEnv + " 配置"}
	}
	if strings.TrimSpace(c.Token) == "" {
		return &ConfigError{Reason: "缺少企业令牌：请设置环境变量 " + TokenEnv}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return &ConfigError{Reason: "企业端点不是合法 URL"}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return &ConfigError{Reason: "企业端点必须使用 https（仅 loopback 允许 http）"}
	}
	if u.Host == "" {
		return &ConfigError{Reason: "企业端点缺少主机名"}
	}
	if u.User != nil {
		return &ConfigError{Reason: "企业端点不得包含用户信息"}
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return &ConfigError{Reason: "企业端点不得包含查询参数或片段"}
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return &ConfigError{Reason: "企业端点不允许使用 http（仅 127.0.0.1 / localhost / ::1 等 loopback 允许）"}
	}
	if c.Timeout <= 0 {
		return &ConfigError{Reason: "--timeout 必须大于 0"}
	}
	if c.MaxBundleBytes <= 0 {
		return &ConfigError{Reason: "--max-bundle-bytes 必须大于 0"}
	}
	return nil
}

// isLoopbackHost reports whether host is an explicit loopback name/address. It
// intentionally does not resolve DNS: push endpoints must be local for plain
// http (ADR-006 D1).
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

func (c Config) maxBundleBytes() int64 {
	if c.MaxBundleBytes > 0 {
		return c.MaxBundleBytes
	}
	return DefaultMaxBundleBytes
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// pushURL joins the configured endpoint with the frozen /v1/push route without
// duplicating slashes. A base path is preserved.
func (c Config) pushURL() string {
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	u, err := url.Parse(base)
	if err != nil {
		return base + "/v1/push"
	}
	u.Path = path.Join(u.Path, "/v1/push")
	return u.String()
}

// sanitize removes a secret from a user-facing string. It is a defence in depth
// for server-provided error text.
func sanitize(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
