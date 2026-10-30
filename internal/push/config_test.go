package push

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Endpoint:       "https://enterprise.example.test",
		Token:          "test-token",
		Timeout:        5 * time.Second,
		MaxBundleBytes: DefaultMaxBundleBytes,
	}
}

func TestConfigValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	httpsLoopback := validConfig()
	httpsLoopback.Endpoint = "http://127.0.0.1:8080"
	if err := httpsLoopback.Validate(); err != nil {
		t.Fatalf("loopback http should be allowed: %v", err)
	}
	for _, endpoint := range []string{"http://localhost:8080", "http://[::1]:8080"} {
		cfg := validConfig()
		cfg.Endpoint = endpoint
		if err := cfg.Validate(); err != nil {
			t.Fatalf("loopback endpoint %q should be allowed: %v", endpoint, err)
		}
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing endpoint", func(c *Config) { c.Endpoint = "" }, "缺少企业端点"},
		{"blank token", func(c *Config) { c.Token = "  " }, "缺少企业令牌"},
		{"bad scheme", func(c *Config) { c.Endpoint = "ftp://enterprise.example.test" }, "https"},
		{"plain http non-loopback", func(c *Config) { c.Endpoint = "http://enterprise.example.test" }, "loopback"},
		{"missing host", func(c *Config) { c.Endpoint = "https://" }, "主机名"},
		{"userinfo rejected", func(c *Config) { c.Endpoint = "https://user:pass@enterprise.example.test" }, "用户信息"},
		{"query rejected", func(c *Config) { c.Endpoint = "https://enterprise.example.test?x=1" }, "查询参数"},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, "timeout"},
		{"negative timeout", func(c *Config) { c.Timeout = -time.Second }, "timeout"},
		{"zero bundle limit", func(c *Config) { c.MaxBundleBytes = 0 }, "max-bundle-bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected config error")
			}
			var configErr *ConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("expected *ConfigError, got %T: %v", err, err)
			}
			if tc.want != "" && !strings.Contains(configErr.Reason, tc.want) {
				t.Fatalf("reason %q should contain %q", configErr.Reason, tc.want)
			}
		})
	}
}

func TestConfigPushURL(t *testing.T) {
	cases := map[string]string{
		"https://enterprise.example.test":     "https://enterprise.example.test/v1/push",
		"https://enterprise.example.test/":    "https://enterprise.example.test/v1/push",
		"https://enterprise.example.test/api": "https://enterprise.example.test/api/v1/push",
		"http://127.0.0.1:1234":               "http://127.0.0.1:1234/v1/push",
	}
	for endpoint, want := range cases {
		cfg := validConfig()
		cfg.Endpoint = endpoint
		if got := cfg.pushURL(); got != want {
			t.Fatalf("pushURL(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{Endpoint: "https://enterprise.example.test", Token: "t"}
	if got := cfg.timeout(); got != DefaultTimeout {
		t.Fatalf("timeout default = %v", got)
	}
	if got := cfg.maxBundleBytes(); got != DefaultMaxBundleBytes {
		t.Fatalf("max bundle default = %d", got)
	}
}

func TestUUIDv4Shape(t *testing.T) {
	id, err := newUUIDv4()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("uuid %q is not canonical", id)
	}
	if id[14] != '4' {
		t.Fatalf("uuid %q is not version 4", id)
	}
	if !strings.Contains("89ab", string(id[19])) {
		t.Fatalf("uuid %q has invalid variant", id)
	}
}
