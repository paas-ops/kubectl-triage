// Package push implements the OSS enterprise-bridge client described by
// ADR-006: a single explicit-opt-in upload of the local report (and optionally
// the redacted evidence bundle) to a user-configured enterprise endpoint.
//
// The package never talks to anything except the configured endpoint, never
// follows redirects, never disables TLS verification, and never logs the
// enterprise token.
package push

import (
	"errors"
	"fmt"
)

// ConfigError is a configuration or input problem (CLI exit code 1): missing
// endpoint/token, a non-loopback plain-http endpoint, invalid limits, a missing
// or schema-invalid report, or an evidence bundle that exceeds its size limit.
type ConfigError struct {
	Reason string
}

func (e *ConfigError) Error() string { return e.Reason }

// SecurityError blocks a push because sensitive content was detected before the
// request was built (CLI exit code 4). The reason never contains the matched
// secret.
type SecurityError struct {
	Reason string
}

func (e *SecurityError) Error() string { return e.Reason }

// RemoteError is a non-retryable rejection returned by the server (4xx, CLI
// exit code 1). Message is server-provided but sanitized so the enterprise
// token can never be echoed back.
type RemoteError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *RemoteError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("推送被服务端拒绝（HTTP %d，%s）：%s", e.StatusCode, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("推送被服务端拒绝（HTTP %d）：%s", e.StatusCode, e.Message)
	default:
		return fmt.Sprintf("推送被服务端拒绝（HTTP %d）", e.StatusCode)
	}
}

// NetworkError is a retryable transport/timeout/429/5xx failure after retries
// are exhausted (CLI exit code 3). Its message never contains request headers.
type NetworkError struct {
	Op  string
	Err error
}

func (e *NetworkError) Error() string {
	if e.Err == nil {
		return "网络请求失败（" + e.Op + "）"
	}
	return "网络请求失败（" + e.Op + "）：" + e.Err.Error()
}

func (e *NetworkError) Unwrap() error { return e.Err }

// IsConfigError reports whether err is a *ConfigError.
func IsConfigError(err error) bool {
	var target *ConfigError
	return errors.As(err, &target)
}
