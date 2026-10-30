package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

// dq is a double quote; keeps JSON assertions readable without escapes.
const dq = string(rune(34))

func testIncident() bundle.Incident {
	return bundle.Incident{
		ID:          "oom-01",
		Alertname:   "KubePodOOMKilled",
		Severity:    "critical",
		Cluster:     "prod-shanghai",
		Namespace:   "checkout",
		Workload:    "checkout-api",
		ObservedAt:  "2026-10-30T13:50:00Z",
		CollectedAt: "2026-10-30T13:50:05Z",
		Source:      "alertmanager",
	}
}

func TestNewDefaults(t *testing.T) {
	rep := New(testIncident(), "dev")
	if rep.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q", rep.SchemaVersion)
	}
	if rep.Mode != "offline" {
		t.Fatalf("mode = %q", rep.Mode)
	}
	if rep.Evidence == nil || rep.Hypotheses == nil || rep.MissingData == nil ||
		rep.RecommendedChecks == nil || rep.Limitations == nil || rep.Versions == nil {
		t.Fatalf("New must initialize empty slices and versions: %+v", rep)
	}
	if rep.Versions["cli"] != "dev" || rep.Versions["model"] != "none" {
		t.Fatalf("versions = %+v", rep.Versions)
	}
	if rep.Versions["rules"] != "rule-v0.2" {
		t.Fatalf("default rules version = %q, want rule-v0.2", rep.Versions["rules"])
	}
}

func TestHypothesisEmptyContradictionsMarshalAsArray(t *testing.T) {
	rep := New(testIncident(), "dev")
	rep.Hypotheses = []Hypothesis{{
		Rank:           1,
		Statement:      "容器因内存不足被 OOMKilled",
		Confidence:     "high",
		EvidenceIDs:    []string{"ev-001"},
		Contradictions: nil,
	}}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := rep.WriteJSON(path); err != nil {
		t.Fatalf("write json: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	emptyContradictions := dq + "contradictions" + dq + ": []"
	if !strings.Contains(string(data), emptyContradictions) {
		t.Fatalf("empty contradictions must serialize as []: %s", string(data))
	}
	if strings.Contains(string(data), dq+"contradictions"+dq+": null") {
		t.Fatal("contradictions must never be null")
	}
	if err := schema.ValidateReport(data); err != nil {
		t.Fatalf("report.json failed schema validation: %v", err)
	}
}

func TestReportMarshalNormalizesNilSlices(t *testing.T) {
	data, err := json.Marshal(Report{Incident: testIncident()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		dq + "evidence" + dq + ":[]",
		dq + "hypotheses" + dq + ":[]",
		dq + "missing_data" + dq + ":[]",
		dq + "recommended_checks" + dq + ":[]",
		dq + "limitations" + dq + ":[]",
		dq + "versions" + dq + ":{}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshaled report missing %q: %s", want, text)
		}
	}
}

func TestWriteJSONPassesSchema(t *testing.T) {
	rep := New(testIncident(), "dev")
	rep.GeneratedAt = "2026-10-30T14:00:00Z"
	rep.Summary = "容器因内存不足被 OOMKilled"
	rep.Evidence = []Evidence{{
		ID:          "ev-001",
		Source:      "k8s.events",
		ObservedAt:  "2026-10-30T13:49:59Z",
		CollectedAt: "2026-10-30T13:50:05Z",
		Summary:     "Warning OOMKilled: container terminated (exitCode=137)",
		ContentRef:  "k8s/events.json#items/0",
		Query:       "kubectl get events -n checkout -o json",
		Redaction:   "strict",
		SHA256:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Stale:       false,
	}}
	rep.Hypotheses = []Hypothesis{{
		Rank:       1,
		Statement:  "容器因内存不足被 OOMKilled",
		Confidence: "high",
		EvidenceIDs: []string{
			"ev-001",
		},
		Contradictions: []Contradiction{{EvidenceID: "ev-002", Note: "存在探针失败证据"}},
	}}
	rep.RecommendedChecks = []string{"核对 memory limit"}
	rep.SuggestedFix = "只读建议：评估提高 memory limit。"
	rep.Limitations = []string{"规则版本 rule-v0.2"}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := rep.WriteJSON(path); err != nil {
		t.Fatalf("write json: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.ValidateReport(data); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}
}

func TestMarkdownTimelineStaleAndProvenance(t *testing.T) {
	rep := New(testIncident(), "dev")
	rep.GeneratedAt = "2026-10-30T14:00:00Z"
	rep.Summary = "容器因内存不足被 OOMKilled"
	rep.Evidence = []Evidence{
		{
			ID:          "ev-002",
			Source:      "logs",
			ObservedAt:  "2026-10-30T13:49:58Z",
			CollectedAt: "2026-10-30T13:50:05Z",
			Summary:     "ERROR container terminated: OOMKilled (exitCode=137)",
			ContentRef:  "logs/app.log#L1",
			Query:       "loki query_range",
			Redaction:   "strict",
			SHA256:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		{
			ID:          "ev-003",
			Source:      "prometheus.metrics",
			ObservedAt:  "2026-10-30T13:20:00Z",
			CollectedAt: "2026-10-30T13:50:05Z",
			Summary:     "Prometheus 指标快照 metrics/memory.json",
			ContentRef:  "metrics/memory.json",
			SHA256:      "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Stale:       true,
			SkewSeconds: 1805,
		},
		{
			ID:          "ev-001",
			Source:      "k8s.events",
			ObservedAt:  "2026-10-30T13:49:55Z",
			CollectedAt: "2026-10-30T13:50:05Z",
			Summary:     "Warning OOMKilled: container terminated (exitCode=137)",
			ContentRef:  "k8s/events.json#items/0",
			SHA256:      "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
	}
	rep.Hypotheses = []Hypothesis{{
		Rank:           1,
		Statement:      "容器因内存不足被 OOMKilled",
		Confidence:     "high",
		EvidenceIDs:    []string{"ev-001", "ev-002"},
		Contradictions: []Contradiction{{EvidenceID: "ev-003", Note: "同期指标采样稀疏"}},
	}}
	rep.MissingData = []MissingData{{Source: "changes", Reason: "缺少发布记录", NextStep: "补充 changes/rollout.json"}}
	rep.RecommendedChecks = []string{"核对 memory limit"}
	rep.SuggestedFix = "只读建议：评估提高 memory limit。"
	rep.Limitations = []string{"规则版本 rule-v0.2", "证据超过上限 200 条，已确定性裁剪 1 条"}
	path := filepath.Join(t.TempDir(), "report.md")
	if err := rep.WriteMarkdown(path); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)
	// Timeline must be ordered by observed_at, not by the JSON order.
	first := strings.Index(md, "ev-003")
	second := strings.Index(md, "ev-001")
	third := strings.Index(md, "ev-002")
	if first < 0 || second < 0 || third < 0 || !(first < second && second < third) {
		t.Fatalf("evidence timeline is not ordered by observed_at: %d %d %d", first, second, third)
	}
	for _, want := range []string{
		"⚠",
		"k8s/events.json#items/0",
		"logs/app.log#L1",
		"时钟偏差：1805s",
		"sha256:",
		"反证：",
		"同期指标采样稀疏",
		"## 缺失证据",
		"## 限制",
		"已确定性裁剪",
		"## 下一步检查",
		"## 修复建议（只读 · 不自动执行）",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q: %s", want, md)
		}
	}
}

func TestLoadRoundTrip(t *testing.T) {
	rep := New(testIncident(), "dev")
	rep.GeneratedAt = "2026-10-30T14:00:00Z"
	rep.Summary = "summary"
	rep.Evidence = []Evidence{{
		ID:         "ev-001",
		Source:     "k8s.events",
		ObservedAt: "2026-10-30T13:49:55Z",
		ContentRef: "k8s/events.json#items/0",
		SHA256:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	rep.Hypotheses = []Hypothesis{{Rank: 1, Statement: "s", Confidence: "mid", EvidenceIDs: []string{"ev-001"}}}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := rep.WriteJSON(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Summary != "summary" || len(loaded.Evidence) != 1 || len(loaded.Hypotheses) != 1 {
		t.Fatalf("unexpected loaded report: %+v", loaded)
	}
	if loaded.Hypotheses[0].Contradictions == nil {
		t.Fatal("loaded contradictions must be an empty array, not nil")
	}
	if loaded.Evidence[0].ContentRef != "k8s/events.json#items/0" || loaded.Evidence[0].Stale {
		t.Fatalf("unexpected loaded evidence: %+v", loaded.Evidence[0])
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing report")
	}
}
