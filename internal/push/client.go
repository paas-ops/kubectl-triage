package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Result summarizes a built payload and/or a completed push. Dry-run uses the
// Build result; a successful run additionally carries the server response.
type Result struct {
	Accepted        bool
	IncidentID      string
	ProtocolVersion string
	PushID          string
	CreatedAt       string
	ReportSHA256    string
	EvidenceCount   int
	BundleIncluded  bool
	BundleSize      int64
}

// retryBackoff is the fixed backoff between retryable attempts (ADR-006 D3).
// It is a variable so package tests can shorten it without changing behavior.
var retryBackoff = []time.Duration{500 * time.Millisecond, time.Second}

const maxResponseBytes = 1 << 20

type acceptedResponse struct {
	Accepted        bool   `json:"accepted"`
	ProtocolVersion string `json:"protocol_version"`
	IncidentID      string `json:"incident_id"`
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Send performs the HTTP exchange with retries. Callers should pass a context
// carrying the overall run timeout. The payload — and therefore push_id and
// Idempotency-Key — is reused unchanged for every retry (ADR-006 D3/D4).
func Send(ctx context.Context, cfg Config, payload *Payload) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, &ConfigError{Reason: "推送 payload 不能为空"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("编码推送请求失败：%w", err)
	}

	client := &http.Client{
		Timeout: cfg.timeout(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not allowed")
		},
	}

	var lastErr error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		if attempt > 0 {
			if err := sleepContext(ctx, retryBackoff[attempt-1]); err != nil {
				return nil, &NetworkError{Op: "重试等待", Err: err}
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.pushURL(), bytes.NewReader(body))
		if err != nil {
			return nil, &ConfigError{Reason: "企业端点不可用：无法构造请求"}
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", payload.PushID)
		req.Header.Set("User-Agent", "kubectl-triage/"+payload.CLI.Version)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = &NetworkError{Op: "POST /v1/push", Err: err}
			if ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			lastErr = &NetworkError{Op: "读取响应", Err: readErr}
			continue
		}

		status := resp.StatusCode
		switch {
		case status >= 200 && status < 300:
			var accepted acceptedResponse
			if err := json.Unmarshal(respBody, &accepted); err != nil || !accepted.Accepted {
				lastErr = &NetworkError{Op: "解析响应", Err: errors.New("服务端返回 2xx 但响应体不是有效的 accepted 响应")}
				continue
			}
			return &Result{
				Accepted:        true,
				IncidentID:      accepted.IncidentID,
				ProtocolVersion: firstNonEmpty(accepted.ProtocolVersion, payload.ProtocolVersion),
				PushID:          payload.PushID,
				CreatedAt:       payload.CreatedAt,
				ReportSHA256:    payload.ReportSHA256,
				EvidenceCount:   len(payload.Evidence),
				BundleIncluded:  payload.Bundle.Included,
				BundleSize:      payload.Bundle.SizeBytes,
			}, nil

		case status == http.StatusTooManyRequests || status >= 500:
			lastErr = &NetworkError{Op: fmt.Sprintf("HTTP %d", status), Err: errors.New("服务端暂时不可用")}
			continue

		default:
			// Every non-retryable response (4xx, and unexpected 1xx/3xx) is a
			// protocol rejection mapped to exit code 1.
			var failure errorResponse
			_ = json.Unmarshal(respBody, &failure)
			return nil, &RemoteError{
				StatusCode: status,
				Code:       failure.Error.Code,
				Message:    sanitize(failure.Error.Message, cfg.Token),
			}
		}
	}
	if lastErr == nil {
		lastErr = &NetworkError{Op: "POST /v1/push", Err: errors.New("重试次数耗尽")}
	}
	return nil, lastErr
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
