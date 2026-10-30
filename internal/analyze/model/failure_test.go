package model

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnhanceCorrectionRoundOnInvalidJSON(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		if call == 1 {
			return stubResponse{Body: openAIChatCompletion("not a JSON object at all", 10, 10)}
		}
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 20, 20)}
	})

	enhanced, err := Enhance(b, offline, openAIConfig(stub.URL()))
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if got := enhanced.Versions["model_rounds"]; got != "2" {
		t.Fatalf("model_rounds = %q, want 2", got)
	}
	if stub.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", stub.Calls())
	}
	requests := stub.Requests()
	correction := userPromptOf(t, requests[1].Body)
	for _, want := range []string{"rejected", "JSON"} {
		if !strings.Contains(correction, want) {
			t.Fatalf("correction prompt lacks %q: %s", want, correction)
		}
	}
	assertReportSchemaValid(t, enhanced)
}

func TestEnhanceCorrectionRoundOnSchemaViolation(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	invalid := d5Document(t, map[string]any{
		"hypotheses": []any{d5Hypothesis("可疑根因", "certain", []string{"ev-001"})},
	})
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		if call == 1 {
			return stubResponse{Body: openAIChatCompletion(invalid, 10, 10)}
		}
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10, 10)}
	})

	enhanced, err := Enhance(b, offline, openAIConfig(stub.URL()))
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if got := enhanced.Versions["model_rounds"]; got != "2" {
		t.Fatalf("model_rounds = %q, want 2", got)
	}
	correction := userPromptOf(t, stub.Requests()[1].Body)
	if !strings.Contains(correction, "confidence") {
		t.Fatalf("correction prompt does not mention confidence: %s", correction)
	}
}

func TestEnhanceFailsAfterAllRoundsInvalid(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion("still not json", 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 2

	enhanced, err := Enhance(b, offline, cfg)
	if enhanced != nil {
		t.Fatalf("Enhance returned a report on failure: %+v", enhanced)
	}
	assertErrorContains(t, err, "未通过校验")
	if stub.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (MaxRounds)", stub.Calls())
	}
	// The offline fallback must be untouched after a model failure.
	if offline.Mode != "offline" || offline.Degraded || offline.Versions["model"] != "none" {
		t.Fatalf("offline report was mutated: mode=%q degraded=%v versions=%v", offline.Mode, offline.Degraded, offline.Versions)
	}
	if offline.Summary != "离线规则摘要：容器疑似 OOMKilled。" {
		t.Fatalf("offline summary was mutated: %q", offline.Summary)
	}
}

func TestEnhanceRejectsOutOfRangeCitation(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-999"), 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 2

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "不存在的证据")
	if stub.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", stub.Calls())
	}
}

func TestEnhanceRejectsSupportContradictionOverlap(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	overlap := d5Document(t, map[string]any{
		"hypotheses": []any{map[string]any{
			"statement":    "疑似 OOMKilled",
			"confidence":   "mid",
			"evidence_ids": []string{"ev-001"},
			"contradictions": []any{map[string]any{
				"evidence_id": "ev-001",
				"note":        "同一证据不能既支持又反驳",
			}},
		}},
	})
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(overlap, 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 1

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "同时作为支持证据与反证")
	if stub.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.Calls())
	}
}

func TestEnhanceRejectsOverlongString(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	tooLong := d5Document(t, map[string]any{"summary": strings.Repeat("长", 501)})
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(tooLong, 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 1

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "超过 500 字符上限")
}

func TestEnhanceRejectsMissingSummary(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(`{"hypotheses":[],"recommended_checks":[],"suggested_fix":""}`, 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 1

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "summary")
}

func TestEnhanceRejectsMarkdownFencedJSON(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	fenced := "```json\n" + validD5(t, "ev-001") + "\n```"
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(fenced, 10, 10)}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 1

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "纯 JSON 对象")
}

func TestEnhanceTimeout(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Answer well after the client-side timeout; the client must give up
		// first and must not retry a timed-out request.
		time.Sleep(500 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	cfg := openAIConfig(server.URL)
	cfg.Timeout = 120 * time.Millisecond
	cfg.MaxRounds = 1
	start := time.Now()
	_, err := Enhance(b, offline, cfg)
	elapsed := time.Since(start)
	assertErrorContains(t, err, "timed out")
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (timeout must not retry)", calls.Load())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took %s, want the 120ms budget", elapsed)
	}
}

func TestEnhanceRetriesOn500ThenSucceeds(t *testing.T) {
	fastRetries(t)
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		if call == 1 {
			return stubResponse{Status: http.StatusInternalServerError, Body: `{"error":{"message":"upstream boom"}}`}
		}
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10, 10)}
	})

	enhanced, err := Enhance(b, offline, openAIConfig(stub.URL()))
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if stub.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (one retry)", stub.Calls())
	}
	if got := enhanced.Versions["model_rounds"]; got != "1" {
		t.Fatalf("model_rounds = %q, want 1 (transport retries are not model rounds)", got)
	}
}

func TestEnhanceRetriesOn429ThenFails(t *testing.T) {
	fastRetries(t)
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Status: http.StatusTooManyRequests, Body: `{"error":{"message":"rate limited"}}`}
	})
	cfg := openAIConfig(stub.URL())
	cfg.MaxRounds = 1

	_, err := Enhance(b, offline, cfg)
	assertErrorContains(t, err, "429")
	if stub.Calls() != 3 {
		t.Fatalf("provider calls = %d, want 3 (initial + 2 retries)", stub.Calls())
	}
}

func TestEnhanceDoesNotRetryOn4xx(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Status: http.StatusUnauthorized, Body: `{"error":{"message":"bad key"}}`}
	})
	_, err := Enhance(b, offline, openAIConfig(stub.URL()))
	assertErrorContains(t, err, "401")
	if stub.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1 (4xx must not retry)", stub.Calls())
	}
}

func TestEnhanceRetriesNetworkErrorsThenFails(t *testing.T) {
	fastRetries(t)
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: "unreachable"}
	})
	url := stub.URL()
	stub.server.Close() // every attempt now fails to connect

	failing := openAIConfig(url)
	failing.MaxRounds = 1
	_, err := Enhance(b, offline, failing)
	assertErrorContains(t, err, "已重试 2 次")
}

func TestEnhanceBudgetExceeded(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10_000_000, 0)}
	})

	_, err := Enhance(b, offline, openAIConfig(stub.URL()))
	assertErrorContains(t, err, "budget")
	if stub.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1 (stop once the budget is exceeded)", stub.Calls())
	}
}

func TestEnhanceRejectsRedirects(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())

	target := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 1, 1)}
	})
	primary := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		return stubResponse{
			Status: http.StatusFound,
			Header: http.Header{"Location": []string{target.URL() + "/v1/chat/completions"}},
		}
	})

	_, err := Enhance(b, offline, openAIConfig(primary.URL()))
	assertErrorContains(t, err, "redirect")
	if primary.Calls() != 1 {
		t.Fatalf("primary provider calls = %d, want 1", primary.Calls())
	}
	if target.Calls() != 0 {
		t.Fatalf("redirect target was called %d times, want 0", target.Calls())
	}
}

func TestEnhancePromptExcerptIsBounded(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": strings.Repeat("A", 200_000)})
	evidence := testLogEvidence()
	secondEvidence := evidence
	secondEvidence.ID = "ev-002"
	offline := testOfflineReport(t, b, evidence, secondEvidence)

	var captured []byte
	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		captured = append([]byte{}, req.Body...)
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10, 10)}
	})
	if _, err := Enhance(b, offline, openAIConfig(stub.URL())); err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if len(captured) > 72<<10 {
		t.Fatalf("provider request body = %d bytes, want <= 72 KiB", len(captured))
	}
	user := userPromptOf(t, captured)
	if !strings.Contains(user, "EVIDENCE-BEGIN") {
		t.Fatalf("user prompt has no evidence excerpt: %s", user)
	}
	if !strings.Contains(user, "already included above") {
		t.Fatalf("duplicate evidence file was not deduplicated: %s", user)
	}
}

func TestEnhanceInvalidUnknownProvider(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())
	_, err := Enhance(b, offline, Config{Provider: "anthropic", URL: "https://api.example.com", APIKey: "sk-test"})
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("Enhance error = %v (%T), want *ConfigError", err, err)
	}
}
