package model

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty provider disables model mode", Config{}, false},
		{"none provider disables model mode", Config{Provider: "none"}, false},
		{"ollama defaults url", Config{Provider: "ollama", ModelName: "llama3.1"}, false},
		{"ollama full config", Config{Provider: "ollama", URL: "http://127.0.0.1:11434", ModelName: "llama3.1"}, false},
		{"openai defaults model name", Config{Provider: "openai-compatible", URL: "https://api.example.com", APIKey: "sk-test"}, false},
		{"openai accepts /v1 base", Config{Provider: "openai-compatible", URL: "https://api.example.com/v1", APIKey: "sk-test"}, false},
		{"unknown provider", Config{Provider: "anthropic", URL: "https://api.example.com", APIKey: "sk-test"}, true},
		{"url with ftp scheme", Config{Provider: "ollama", URL: "ftp://127.0.0.1:11434", ModelName: "llama3.1"}, true},
		{"url without scheme", Config{Provider: "ollama", URL: "127.0.0.1:11434", ModelName: "llama3.1"}, true},
		{"url with credentials", Config{Provider: "ollama", URL: "http://user:pass@127.0.0.1:11434", ModelName: "llama3.1"}, true},
		{"openai missing url", Config{Provider: "openai-compatible", APIKey: "sk-test"}, true},
		{"openai missing key", Config{Provider: "openai-compatible", URL: "https://api.example.com"}, true},
		{"ollama missing model name", Config{Provider: "ollama", URL: "http://127.0.0.1:11434"}, true},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateConfig(testCase.cfg)
			if !testCase.wantErr {
				if err != nil {
					t.Fatalf("ValidateConfig(%+v) = %v, want nil", testCase.cfg, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateConfig(%+v) = nil, want *ConfigError", testCase.cfg)
			}
			var configErr *ConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("ValidateConfig(%+v) error type = %T, want *ConfigError", testCase.cfg, err)
			}
			if strings.TrimSpace(configErr.Reason) == "" {
				t.Fatalf("ConfigError.Reason is empty")
			}
			if testCase.cfg.APIKey != "" && strings.Contains(configErr.Reason, testCase.cfg.APIKey) {
				t.Fatalf("ConfigError leaks API key: %q", configErr.Reason)
			}
		})
	}
}

func TestEnhanceOpenAISuccess(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())

	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		if req.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", req.Method)
		}
		if req.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", req.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-test-key" {
			t.Errorf("Authorization = %q, want Bearer sk-test-key", got)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		payload := requestPayload(t, req.Body)
		if got := payload["model"]; got != "gpt-4o-mini" {
			t.Errorf("model = %v, want default gpt-4o-mini", got)
		}
		if got := payload["temperature"]; got != float64(0) {
			t.Errorf("temperature = %v, want 0", got)
		}
		format, ok := payload["response_format"].(map[string]any)
		if !ok || format["type"] != "json_object" {
			t.Errorf("response_format = %v, want {type: json_object}", payload["response_format"])
		}
		system := systemPromptOf(t, req.Body)
		for _, want := range []string{"UNTRUSTED DATA", "never", "JSON"} {
			if !strings.Contains(system, want) {
				t.Errorf("system prompt lacks %q: %s", want, system)
			}
		}
		user := userPromptOf(t, req.Body)
		for _, want := range []string{"Incident metadata", "ev-001", "logs/app.log", "OOMKilled", "EVIDENCE-BEGIN"} {
			if !strings.Contains(user, want) {
				t.Errorf("user prompt lacks %q: %s", want, user)
			}
		}
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 100, 50)}
	})

	enhanced, err := Enhance(b, offline, openAIConfig(stub.URL()))
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if stub.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.Calls())
	}
	if enhanced.Mode != "model" || enhanced.Degraded {
		t.Fatalf("mode/degraded = %q/%v, want model/false", enhanced.Mode, enhanced.Degraded)
	}
	wantVersions := map[string]string{
		"model":          "openai-compatible/gpt-4o-mini",
		"model_rounds":   "1",
		"model_cost_usd": "0.0000",
		"cli":            "test",
		"rules":          "rule-test",
	}
	for key, want := range wantVersions {
		if got := enhanced.Versions[key]; got != want {
			t.Fatalf("versions[%q] = %q, want %q", key, got, want)
		}
	}
	if enhanced.Summary == "" {
		t.Fatal("summary is empty")
	}
	if len(enhanced.Hypotheses) != 1 {
		t.Fatalf("hypotheses = %+v, want 1", enhanced.Hypotheses)
	}
	hypothesis := enhanced.Hypotheses[0]
	if hypothesis.Rank != 1 || hypothesis.Confidence != "high" || len(hypothesis.EvidenceIDs) != 1 || hypothesis.EvidenceIDs[0] != "ev-001" {
		t.Fatalf("hypothesis not merged correctly: %+v", hypothesis)
	}
	if len(enhanced.RecommendedChecks) == 0 || enhanced.SuggestedFix == "" {
		t.Fatalf("recommended_checks/suggested_fix not merged: %+v / %q", enhanced.RecommendedChecks, enhanced.SuggestedFix)
	}
	if len(enhanced.Evidence) != len(offline.Evidence) || enhanced.Evidence[0] != offline.Evidence[0] {
		t.Fatalf("evidence not preserved from offline report: %+v", enhanced.Evidence)
	}
	if !reflect.DeepEqual(enhanced.MissingData, offline.MissingData) {
		t.Fatalf("missing_data not preserved: %+v", enhanced.MissingData)
	}
	if enhanced.GeneratedAt != offline.GeneratedAt || !reflect.DeepEqual(enhanced.Incident, offline.Incident) {
		t.Fatalf("generated_at/incident not preserved: %q / %+v", enhanced.GeneratedAt, enhanced.Incident)
	}
	limitations := strings.Join(enhanced.Limitations, "\n")
	if !strings.Contains(limitations, "模型模式") || !strings.Contains(limitations, "安全说明") {
		t.Fatalf("limitations missing model/safety notes: %v", enhanced.Limitations)
	}
	if strings.Contains(limitations, "未接入模型") {
		t.Fatalf("offline model disclaimer not replaced: %v", enhanced.Limitations)
	}
	assertReportSchemaValid(t, enhanced)

	// The offline fallback report must not be mutated on success.
	if offline.Mode != "offline" || offline.Degraded || offline.Versions["model"] != "none" {
		t.Fatalf("offline report was mutated: mode=%q degraded=%v versions=%v", offline.Mode, offline.Degraded, offline.Versions)
	}
}

func TestEnhanceOllamaSuccess(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())

	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		if req.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", req.Path)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("ollama request must not send Authorization, got %q", got)
		}
		payload := requestPayload(t, req.Body)
		if got := payload["model"]; got != "llama3.1" {
			t.Errorf("model = %v, want llama3.1", got)
		}
		if got := payload["stream"]; got != false {
			t.Errorf("stream = %v, want false", got)
		}
		options, ok := payload["options"].(map[string]any)
		if !ok || options["temperature"] != float64(0) {
			t.Errorf("options = %v, want temperature 0", payload["options"])
		}
		return stubResponse{Body: ollamaChatCompletion(validD5(t, "ev-001"), 200, 100)}
	})

	cfg := Config{Provider: "ollama", URL: stub.URL(), ModelName: "llama3.1", Timeout: 5e9, BudgetUSD: 1, MaxRounds: 3}
	enhanced, err := Enhance(b, offline, cfg)
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if enhanced.Mode != "model" || enhanced.Degraded {
		t.Fatalf("mode/degraded = %q/%v", enhanced.Mode, enhanced.Degraded)
	}
	if got := enhanced.Versions["model"]; got != "ollama/llama3.1" {
		t.Fatalf("versions.model = %q, want ollama/llama3.1", got)
	}
	if got := enhanced.Versions["model_cost_usd"]; got != "0.0001" {
		t.Fatalf("versions.model_cost_usd = %q, want 0.0001 (200 prompt + 100 completion tokens)", got)
	}
	assertReportSchemaValid(t, enhanced)
}

func TestEnhanceOpenAIBaseURLVariants(t *testing.T) {
	cases := []struct {
		name     string
		suffix   string
		wantPath string
	}{
		{"plain base", "", "/v1/chat/completions"},
		{"trailing slash base", "/", "/v1/chat/completions"},
		{"v1 base", "/v1", "/v1/chat/completions"},
		{"v1 trailing slash base", "/v1/", "/v1/chat/completions"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
			offline := testOfflineReport(t, b, testLogEvidence())
			stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
				if req.Path != testCase.wantPath {
					t.Errorf("path = %q, want %q", req.Path, testCase.wantPath)
				}
				return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10, 10)}
			})
			cfg := openAIConfig(stub.URL() + testCase.suffix)
			if _, err := Enhance(b, offline, cfg); err != nil {
				t.Fatalf("Enhance: %v", err)
			}
			if stub.Calls() != 1 {
				t.Fatalf("provider calls = %d, want 1", stub.Calls())
			}
		})
	}
}

func TestEnhanceDropsResponseFormatOnceOnUnsupported400(t *testing.T) {
	b := testBundle(t, map[string]string{"logs/app.log": testLogFile})
	offline := testOfflineReport(t, b, testLogEvidence())

	stub := newStubProvider(t, func(call int, req stubRequest) stubResponse {
		payload := requestPayload(t, req.Body)
		if call == 1 {
			if _, ok := payload["response_format"]; !ok {
				t.Errorf("first attempt should send response_format")
			}
			return stubResponse{Status: http.StatusBadRequest, Body: `{"error":{"message":"response_format is not supported by this model"}}`}
		}
		if _, ok := payload["response_format"]; ok {
			t.Errorf("retry without response_format must not send it")
		}
		return stubResponse{Body: openAIChatCompletion(validD5(t, "ev-001"), 10, 10)}
	})

	enhanced, err := Enhance(b, offline, openAIConfig(stub.URL()))
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if enhanced.Mode != "model" {
		t.Fatalf("mode = %q, want model", enhanced.Mode)
	}
	if stub.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (fallback retry once)", stub.Calls())
	}
}
