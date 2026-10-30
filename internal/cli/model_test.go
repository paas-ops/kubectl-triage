package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/report"
)

// modelOutputJSON is the ADR-004 D5 body returned by the stub provider. ev-001
// is the first evidence item produced by bundle.Scaffold (k8s/events.json).
const modelOutputJSON = `{"summary":"模型归纳：容器内存 limit 过低导致 OOMKilled","hypotheses":[{"statement":"容器内存 limit 过低导致 OOMKilled","confidence":"high","evidence_ids":["ev-001"],"contradictions":[]}],"recommended_checks":["核对 memory limit 是否在最近一次发布中降低"],"suggested_fix":"只读建议：确认峰值后评估提高 memory limit 或修复内存泄漏，不自动修改工作负载。"}`

// modelStub is a minimal OpenAI-compatible provider used for CLI end-to-end
// tests. It never leaves 127.0.0.1 and records the requests it receives.
type modelStub struct {
	URL  string
	hits int32

	mu          sync.Mutex
	authHeaders []string
}

func newModelStub(t *testing.T, responseBody string) *modelStub {
	t.Helper()
	stub := &modelStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&stub.hits, 1)
		stub.mu.Lock()
		stub.authHeaders = append(stub.authHeaders, r.Header.Get("Authorization"))
		stub.mu.Unlock()

		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("model stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []any{
				map[string]any{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": responseBody},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
		})
	}))
	stub.URL = server.URL
	t.Cleanup(server.Close)
	return stub
}

func (s *modelStub) hitCount() int { return int(atomic.LoadInt32(&s.hits)) }

func (s *modelStub) authorizationHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.authHeaders...)
}

func loadCLIReport(t *testing.T, dir string) *report.Report {
	t.Helper()
	jsonPath := filepath.Join(dir, "report.json")
	requireSchemaValidReport(t, jsonPath)
	rep, err := report.Load(jsonPath)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	return rep
}

// requireNoModelCall asserts a config error neither wrote a report nor reached
// the provider endpoint.
func requireNoModelCall(t *testing.T, stub *modelStub, dir string) {
	t.Helper()
	if hits := stub.hitCount(); hits != 0 {
		t.Fatalf("configuration errors must not call the provider, got %d request(s)", hits)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("configuration errors must not write a report")
	}
}

func TestAnalyzeOpenAICompatibleSuccess(t *testing.T) {
	dir := scaffoldDir(t)
	stub := newModelStub(t, modelOutputJSON)
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "cli-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	res := run(t, "analyze", dir,
		"--model", "openai-compatible",
		"--model-url", stub.URL)
	requireCode(t, res, 0)

	rep := loadCLIReport(t, dir)
	if rep.Mode != "model" {
		t.Fatalf("mode = %q, want model", rep.Mode)
	}
	if rep.Degraded {
		t.Fatal("successful model run must not be degraded")
	}
	modelVersion := rep.Versions["model"]
	for _, want := range []string{"openai-compatible", "gpt-4o-mini"} {
		if !strings.Contains(modelVersion, want) {
			t.Fatalf("versions.model = %q, want it to contain %q", modelVersion, want)
		}
	}
	if !strings.Contains(rep.Summary, "模型归纳") {
		t.Fatalf("summary should come from the model body, got %q", rep.Summary)
	}
	if len(rep.Hypotheses) == 0 {
		t.Fatal("expected at least one model hypothesis")
	}
	cited := false
	for _, id := range rep.Hypotheses[0].EvidenceIDs {
		if id == "ev-001" {
			cited = true
		}
	}
	if !cited {
		t.Fatalf("model hypothesis should cite offline evidence ev-001: %v", rep.Hypotheses[0].EvidenceIDs)
	}
	if stub.hitCount() == 0 {
		t.Fatal("expected the stub provider to be called")
	}
	// KUBECTL_TRIAGE_MODEL_KEY takes precedence over OPENAI_API_KEY.
	headers := stub.authorizationHeaders()
	if len(headers) == 0 || !strings.Contains(headers[0], "cli-key") {
		t.Fatalf("unexpected Authorization header(s): %v", headers)
	}
}

func TestAnalyzeModelInvalidOutputDegrades(t *testing.T) {
	dir := scaffoldDir(t)
	stub := newModelStub(t, "this is not a valid model JSON body")
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "cli-key")

	res := run(t, "analyze", dir,
		"--model", "openai-compatible",
		"--model-url", stub.URL,
		"--model-max-rounds", "1")
	requireCode(t, res, 3)

	rep := loadCLIReport(t, dir)
	if rep.Mode != "offline" {
		t.Fatalf("degraded mode = %q, want offline", rep.Mode)
	}
	if !rep.Degraded {
		t.Fatal("invalid model output must degrade the report")
	}
	if rep.Versions["model"] != "none" {
		t.Fatalf("degraded versions.model = %q, want none", rep.Versions["model"])
	}
	joined := strings.Join(rep.Limitations, "\n")
	if !strings.Contains(joined, "模型不可用，已回退离线规则") {
		t.Fatalf("degraded report is missing the fallback reason: %v", rep.Limitations)
	}
}

func TestAnalyzeUnknownProviderIsConfigError(t *testing.T) {
	dir := scaffoldDir(t)
	stub := newModelStub(t, modelOutputJSON)
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "cli-key")

	res := run(t, "analyze", dir, "--model", "banana", "--model-url", stub.URL)
	requireCode(t, res, 1)
	requireNoModelCall(t, stub, dir)
}

func TestAnalyzeOpenAIMissingKeyIsConfigError(t *testing.T) {
	dir := scaffoldDir(t)
	stub := newModelStub(t, modelOutputJSON)
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	res := run(t, "analyze", dir, "--model", "openai-compatible", "--model-url", stub.URL)
	requireCode(t, res, 1)
	requireNoModelCall(t, stub, dir)
}

func TestAnalyzeOpenAIMissingURLIsConfigError(t *testing.T) {
	dir := scaffoldDir(t)
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "cli-key")

	res := run(t, "analyze", dir, "--model", "openai-compatible")
	requireCode(t, res, 1)
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("configuration errors must not write a report")
	}
}

func TestAnalyzeOllamaMissingModelNameIsConfigError(t *testing.T) {
	dir := scaffoldDir(t)
	// Point at the stub so an accidental network attempt is observable and to
	// avoid touching a real local Ollama during tests.
	stub := newModelStub(t, modelOutputJSON)

	res := run(t, "analyze", dir, "--model", "ollama", "--model-url", stub.URL)
	requireCode(t, res, 1)
	requireNoModelCall(t, stub, dir)
}

func TestAnalyzeOllamaUnreachableDegrades(t *testing.T) {
	dir := scaffoldDir(t)
	res := run(t, "analyze", dir,
		"--model", "ollama",
		"--model-url", "http://127.0.0.1:1",
		"--model-name", "llama3",
		"--model-timeout", "500ms")
	requireCode(t, res, 3)

	rep := loadCLIReport(t, dir)
	if rep.Mode != "offline" || !rep.Degraded {
		t.Fatalf("unreachable provider must degrade: mode=%q degraded=%v", rep.Mode, rep.Degraded)
	}
	if rep.Versions["model"] != "none" {
		t.Fatalf("degraded versions.model = %q, want none", rep.Versions["model"])
	}
}

func TestAnalyzeOfflineNeverCallsProvider(t *testing.T) {
	dir := scaffoldDir(t)
	stub := newModelStub(t, modelOutputJSON)
	t.Setenv("KUBECTL_TRIAGE_MODEL_KEY", "cli-key")

	// --model-url without --model is ignored; --offline is a hard no-network
	// guarantee, so the provider must never be reached.
	res := run(t, "analyze", dir, "--offline", "--model-url", stub.URL)
	requireCode(t, res, 0)
	if hits := stub.hitCount(); hits != 0 {
		t.Fatalf("offline analyze made %d network call(s)", hits)
	}
	rep := loadCLIReport(t, dir)
	if rep.Mode != "offline" || rep.Degraded {
		t.Fatalf("offline report should not be degraded: mode=%q degraded=%v", rep.Mode, rep.Degraded)
	}
}

func TestAnalyzeFailOnMissingWinsOverModelDegradation(t *testing.T) {
	dir := scaffoldDir(t)
	// The scaffold bundle has evidence but lacks changes/ data, so missing_data
	// is non-empty. The unreachable provider degrades the report, and ADR-004 D3
	// gives the explicit --fail-on-missing gate priority (2 over 3).
	res := run(t, "analyze", dir,
		"--model", "ollama",
		"--model-url", "http://127.0.0.1:1",
		"--model-name", "llama3",
		"--model-timeout", "500ms",
		"--fail-on-missing")
	requireCode(t, res, 2)

	rep := loadCLIReport(t, dir)
	if !rep.Degraded {
		t.Fatalf("expected the model run to degrade before the missing-data gate, got mode=%q degraded=%v", rep.Mode, rep.Degraded)
	}
	if len(rep.MissingData) == 0 {
		t.Fatal("expected missing_data so the fail-on-missing gate applies")
	}
}
