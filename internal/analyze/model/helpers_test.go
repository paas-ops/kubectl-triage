package model

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

const testIncidentYAML = `id: model-unit-01
alertname: KubePodOOMKilled
severity: critical
cluster: prod-shanghai
namespace: checkout
workload: checkout-api
observed_at: 2026-10-30T13:50:00Z
collected_at: 2026-10-30T13:50:05Z
source: unit
`

const testGeneratedAt = "2026-10-30T14:00:00Z"

const testLogFile = `2026-10-30T13:49:57Z ERROR container terminated: OOMKilled (exitCode=137)
2026-10-30T13:49:58Z WARN memory usage 512MiB / 512MiB
`

// testBundle writes a deterministic incident bundle and returns it validated.
// Redaction scanning is disabled so tests can hold arbitrary text.
func testBundle(t *testing.T, files map[string]string) *bundle.Bundle {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{"incident.yaml": testIncidentYAML}
	for rel, body := range files {
		all[rel] = body
	}
	for rel, body := range all {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	loaded, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	loaded.RedactMode = redact.ModeOff
	if err := loaded.Validate(); err != nil {
		t.Fatalf("validate bundle: %v", err)
	}
	return loaded
}

// testLogEvidence is one schema-valid evidence entry pointing at logs/app.log.
func testLogEvidence() report.Evidence {
	return report.Evidence{
		ID:          "ev-001",
		Source:      "logs",
		ObservedAt:  "2026-10-30T13:49:57Z",
		CollectedAt: "2026-10-30T13:50:05Z",
		Summary:     "日志摘录 logs/app.log：ERROR container terminated: OOMKilled (exitCode=137)",
		ContentRef:  "logs/app.log#L1",
		Query:       "loki query_range",
		Redaction:   "strict",
		SHA256:      "0123456789abcdef",
		Stale:       false,
	}
}

// testOfflineReport builds a deterministic, schema-valid offline fallback report.
func testOfflineReport(t *testing.T, b *bundle.Bundle, evidence ...report.Evidence) *report.Report {
	t.Helper()
	rep := report.New(b.Incident, "test")
	rep.GeneratedAt = testGeneratedAt
	rep.Mode = "offline"
	rep.Degraded = false
	rep.Summary = "离线规则摘要：容器疑似 OOMKilled。"
	rep.Evidence = append([]report.Evidence{}, evidence...)
	if len(evidence) > 0 {
		rep.Hypotheses = []report.Hypothesis{{
			Rank:           1,
			Statement:      "容器内存超限被 OOMKilled",
			Confidence:     "mid",
			EvidenceIDs:    []string{evidence[0].ID},
			Contradictions: []report.Contradiction{},
		}}
	} else {
		rep.Hypotheses = []report.Hypothesis{}
	}
	rep.MissingData = []report.MissingData{{
		Source:   "changes",
		Reason:   "事故包缺少 changes/ 发布变更记录",
		NextStep: "补充 changes/rollout.json",
	}}
	rep.RecommendedChecks = []string{"核对容器退出码"}
	rep.SuggestedFix = "提高容器内存 limit"
	rep.Limitations = []string{
		"离线规则版本 rule-test；未接入模型，不读取集群、不访问网络。",
		"所有建议均为只读，不自动执行任何修复。",
	}
	rep.Versions["cli"] = "test"
	rep.Versions["rules"] = "rule-test"
	rep.Versions["model"] = "none"
	assertReportSchemaValid(t, rep)
	return rep
}

// assertReportSchemaValid fails when the report does not satisfy the frozen
// report.schema.json contract.
func assertReportSchemaValid(t *testing.T, rep *report.Report) {
	t.Helper()
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := schema.ValidateReport(data); err != nil {
		t.Fatalf("report fails schema validation: %v", err)
	}
}

// markdownFor renders the report and returns report.md content.
func markdownFor(t *testing.T, rep *report.Report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.md")
	if err := rep.WriteMarkdown(path); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	return string(data)
}

// stubRequest captures one provider request.
type stubRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// stubResponse is what the stub provider answers for one request.
type stubResponse struct {
	Status int
	Body   string
	Header http.Header
}

// stubProvider is a deterministic httptest-based OpenAI/Ollama stub.
type stubProvider struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	calls  []stubRequest
	handle func(call int, req stubRequest) stubResponse
}

func newStubProvider(t *testing.T, handle func(call int, req stubRequest) stubResponse) *stubProvider {
	t.Helper()
	stub := &stubProvider{t: t, handle: handle}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read stub request body: %v", err)
		}
		req := stubRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		stub.mu.Lock()
		stub.calls = append(stub.calls, req)
		call := len(stub.calls)
		stub.mu.Unlock()

		response := stub.handle(call, req)
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		status := response.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response.Body)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// URL is the provider base URL passed to Config.
func (s *stubProvider) URL() string { return s.server.URL }

// Calls returns how many requests the stub received.
func (s *stubProvider) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// Requests returns a copy of all captured requests.
func (s *stubProvider) Requests() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubRequest, len(s.calls))
	copy(out, s.calls)
	return out
}

// openAIChatCompletion renders a standard chat.completion envelope around the
// ADR-004 D5 JSON string.
func openAIChatCompletion(content string, promptTokens, completionTokens int) string {
	payload := map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ollamaChatCompletion renders a standard Ollama /api/chat envelope.
func ollamaChatCompletion(content string, promptEvalCount, evalCount int) string {
	payload := map[string]any{
		"model":             "llama3.1",
		"created_at":        "2026-10-30T13:50:00Z",
		"message":           map[string]any{"role": "assistant", "content": content},
		"done":              true,
		"prompt_eval_count": promptEvalCount,
		"eval_count":        evalCount,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// d5Document renders a D5 JSON body with defaults overridden by fields.
func d5Document(t *testing.T, fields map[string]any) string {
	t.Helper()
	document := map[string]any{
		"summary":            "容器因 OOMKilled 退出，需核对内存 limit 与工作负载峰值。",
		"hypotheses":         []any{},
		"recommended_checks": []string{"核对 Pod 内存 limit 与实际峰值"},
		"suggested_fix":      "调高容器内存 limit 并观察重启次数",
	}
	for key, value := range fields {
		document[key] = value
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal D5 document: %v", err)
	}
	return string(data)
}

// d5Hypothesis is one valid D5 hypothesis entry.
func d5Hypothesis(statement, confidence string, evidenceIDs []string) map[string]any {
	return map[string]any{
		"statement":      statement,
		"confidence":     confidence,
		"evidence_ids":   evidenceIDs,
		"contradictions": []any{},
	}
}

// validD5 renders a valid D5 document citing the given evidence ids.
func validD5(t *testing.T, evidenceIDs ...string) string {
	t.Helper()
	hypotheses := []any{}
	if len(evidenceIDs) > 0 {
		hypotheses = append(hypotheses, d5Hypothesis(
			"OOMKilled 由容器内存 limit 过小导致", "high", evidenceIDs))
	}
	return d5Document(t, map[string]any{"hypotheses": hypotheses})
}

// requestPayload decodes a captured provider request body.
func requestPayload(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode provider request body: %v (body=%s)", err, string(body))
	}
	return payload
}

// chatMessages returns the decoded messages array of a provider request.
func chatMessages(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	payload := requestPayload(t, body)
	raw, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("provider request has no messages array: %s", string(body))
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("provider request message is not an object: %v", item)
		}
		out = append(out, message)
	}
	return out
}

// userPromptOf returns the user-role message content of a provider request.
func userPromptOf(t *testing.T, body []byte) string {
	t.Helper()
	for _, message := range chatMessages(t, body) {
		if message["role"] == "user" {
			content, _ := message["content"].(string)
			return content
		}
	}
	t.Fatalf("provider request has no user message: %s", string(body))
	return ""
}

// systemPromptOf returns the system-role message content of a provider request.
func systemPromptOf(t *testing.T, body []byte) string {
	t.Helper()
	for _, message := range chatMessages(t, body) {
		if message["role"] == "system" {
			content, _ := message["content"].(string)
			return content
		}
	}
	t.Fatalf("provider request has no system message: %s", string(body))
	return ""
}

// fastRetries shrinks the production retry backoff to 1ms for the test.
func fastRetries(t *testing.T) {
	t.Helper()
	previous := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryBackoff = previous })
}

// openAIConfig returns a valid openai-compatible config against a stub URL.
func openAIConfig(url string) Config {
	return Config{
		Provider:  "openai-compatible",
		URL:       url,
		APIKey:    "sk-test-key",
		Timeout:   5 * time.Second,
		BudgetUSD: 1.0,
		MaxRounds: 3,
	}
}

// assertErrorContains fails unless err is non-nil and contains every substring.
func assertErrorContains(t *testing.T, err error, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", substrings)
	}
	for _, substring := range substrings {
		if !strings.Contains(err.Error(), substring) {
			t.Fatalf("error %q does not contain %q", err.Error(), substring)
		}
	}
}
