package model_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze"
	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

const wiringIncident = `id: wiring-01
alertname: KubePodOOMKilled
severity: critical
cluster: prod-shanghai
namespace: checkout
workload: checkout-api
observed_at: 2026-10-30T13:50:00Z
collected_at: 2026-10-30T13:50:05Z
source: unit
`

const wiringLogs = `2026-10-30T13:49:57Z ERROR container terminated: OOMKilled (exitCode=137)
2026-10-30T13:49:58Z WARN memory usage 512MiB / 512MiB
`

func wiringFixedNow() time.Time {
	return time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC)
}

func wiringBundle(t *testing.T) *bundle.Bundle {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"incident.yaml": wiringIncident,
		"logs/app.log":  wiringLogs,
	}
	for rel, body := range files {
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

// assertReportSchema marshals and validates a report against the frozen schema.
func assertReportSchema(t *testing.T, rep *report.Report) {
	t.Helper()
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := schema.ValidateReport(data); err != nil {
		t.Fatalf("report fails schema validation: %v", err)
	}
}

func TestAnalyzeDegradesWhenModelOutputIsInvalid(t *testing.T) {
	b := wiringBundle(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"not a JSON object"}}],"usage":{}}`))
	}))
	t.Cleanup(server.Close)

	rep, err := analyze.Analyze(b, "test", analyze.Options{
		Now: wiringFixedNow(),
		Model: model.Config{
			Provider:  "openai-compatible",
			URL:       server.URL,
			APIKey:    "sk-test-key",
			Timeout:   2 * time.Second,
			MaxRounds: 1,
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.Degraded {
		t.Fatal("expected degraded=true after model failure")
	}
	if rep.Mode != "offline" {
		t.Fatalf("mode = %q, want offline", rep.Mode)
	}
	if got := rep.Versions["model"]; got != "none" {
		t.Fatalf("versions.model = %q, want none", got)
	}
	foundReason := false
	for _, limitation := range rep.Limitations {
		if strings.Contains(limitation, "模型不可用") {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("degradation reason missing from limitations: %v", rep.Limitations)
	}
	if len(rep.Evidence) == 0 {
		t.Fatal("offline evidence must be preserved after degradation")
	}
	assertReportSchema(t, rep)
}

func TestAnalyzeUsesModelReportOnSuccess(t *testing.T) {
	b := wiringBundle(t)
	fixed := wiringFixedNow()

	offline, err := analyze.Analyze(b, "test", analyze.Options{Now: fixed})
	if err != nil {
		t.Fatalf("offline Analyze: %v", err)
	}
	if len(offline.Evidence) == 0 {
		t.Fatalf("offline report has no evidence: %+v", offline)
	}
	evidenceID := offline.Evidence[0].ID

	content, err := json.Marshal(map[string]any{
		"summary": "模型摘要：容器因 OOMKilled 退出，疑似内存 limit 过小。",
		"hypotheses": []any{map[string]any{
			"statement":      "容器内存 limit 过小导致 OOMKilled",
			"confidence":     "high",
			"evidence_ids":   []string{evidenceID},
			"contradictions": []any{},
		}},
		"recommended_checks": []string{"核对 Pod 内存 limit 与实际峰值"},
		"suggested_fix":      "调高容器内存 limit 并观察重启次数",
	})
	if err != nil {
		t.Fatalf("marshal model content: %v", err)
	}
	envelope, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": string(content)},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(envelope)
	}))
	t.Cleanup(server.Close)

	rep, err := analyze.Analyze(b, "test", analyze.Options{
		Now: fixed,
		Model: model.Config{
			Provider:  "openai-compatible",
			URL:       server.URL,
			APIKey:    "sk-test-key",
			Timeout:   2 * time.Second,
			MaxRounds: 3,
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Degraded {
		t.Fatalf("report unexpectedly degraded: %v", rep.Limitations)
	}
	if rep.Mode != "model" {
		t.Fatalf("mode = %q, want model", rep.Mode)
	}
	if got := rep.Versions["model"]; got != "openai-compatible/gpt-4o-mini" {
		t.Fatalf("versions.model = %q", got)
	}
	if len(rep.Evidence) != len(offline.Evidence) {
		t.Fatalf("evidence count changed: %d != %d", len(rep.Evidence), len(offline.Evidence))
	}
	if len(rep.Hypotheses) != 1 || rep.Hypotheses[0].EvidenceIDs[0] != evidenceID {
		t.Fatalf("model hypothesis not merged: %+v", rep.Hypotheses)
	}
	if rep.Summary != "模型摘要：容器因 OOMKilled 退出，疑似内存 limit 过小。" {
		t.Fatalf("summary = %q", rep.Summary)
	}
	assertReportSchema(t, rep)
}
