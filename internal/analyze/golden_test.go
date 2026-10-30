package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

const (
	goldenRootDir     = "../../testdata/golden"
	goldenGeneratedAt = "2000-01-01T00:00:00Z"
	goldenCLIVersion  = "dev"
	goldenFixedNow    = "2026-10-30T14:00:00Z"
)

// goldenScenarios is the frozen M1 baseline: five failure classes x2 plus one
// missing-evidence bundle and one prompt-injection bundle.
var goldenScenarios = []string{
	"oom-01",
	"oom-02",
	"crashloop-01",
	"crashloop-02",
	"imagepull-01",
	"imagepull-02",
	"pending-01",
	"pending-02",
	"release-01",
	"release-02",
	"missing-01",
	"injection-01",
}

// injectionForbidden must never appear in a report (JSON or Markdown).
var injectionForbidden = []string{
	"evil.example.com",
	"attacker.invalid",
	"http://",
	"https://",
	"rm -rf",
	"curl",
	"wget",
	"ignore previous instructions",
	"忽略之前指令",
}

func goldenNow(t *testing.T) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, goldenFixedNow)
	if err != nil {
		t.Fatalf("parse golden now: %v", err)
	}
	return parsed
}

// copyGoldenBundle copies a fixture bundle into a temp dir so analysis never
// writes next to the checked-in fixtures.
func copyGoldenBundle(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read golden dir %s: %v", src, err)
	}
	for _, entry := range entries {
		if entry.Name() == "expected" {
			continue
		}
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dstPath, err)
			}
			copyGoldenBundle(t, srcPath, dstPath)
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read %s: %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", dstPath, err)
		}
	}
}

// analyzeGoldenOnce copies one scenario into t.TempDir, validates and analyzes
// it with a fixed Now and the default evidence cap.
func analyzeGoldenOnce(t *testing.T, scenario string) *report.Report {
	t.Helper()
	dir := filepath.Join(t.TempDir(), scenario)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	copyGoldenBundle(t, filepath.Join(goldenRootDir, scenario), dir)

	loaded, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("load %s: %v", scenario, err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("validate %s: %v", scenario, err)
	}
	rep, err := Analyze(loaded, goldenCLIVersion, Options{MaxEvidence: 200, Now: goldenNow(t)})
	if err != nil {
		t.Fatalf("analyze %s: %v", scenario, err)
	}
	return rep
}

func normalizeGolden(rep *report.Report) {
	rep.GeneratedAt = goldenGeneratedAt
	if rep.Versions == nil {
		rep.Versions = map[string]string{}
	}
	rep.Versions["cli"] = goldenCLIVersion
}

func marshalGolden(t *testing.T, rep *report.Report) []byte {
	t.Helper()
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return append(data, byte(10))
}

// firstDifference returns a short human-readable diff of the first differing
// line so golden failures are actionable.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, string([]byte{10}))
	gotLines := strings.Split(got, string([]byte{10}))
	limit := len(wantLines)
	if len(gotLines) < limit {
		limit = len(gotLines)
	}
	for i := 0; i < limit; i++ {
		if wantLines[i] != gotLines[i] {
			return "line " + itoa(i+1) + ": expected " + truncateLine(wantLines[i]) + " | actual " + truncateLine(gotLines[i])
		}
	}
	return "line count: expected " + itoa(len(wantLines)) + " actual " + itoa(len(gotLines))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func truncateLine(line string) string {
	if len(line) > 160 {
		return line[:160] + "..."
	}
	return line
}

func TestGoldenReports(t *testing.T) {
	for _, scenario := range goldenScenarios {
		scenario := scenario
		t.Run(scenario, func(t *testing.T) {
			rep := analyzeGoldenOnce(t, scenario)
			second := analyzeGoldenOnce(t, scenario)
			normalizeGolden(rep)
			normalizeGolden(second)
			first := marshalGolden(t, rep)
			if repeated := marshalGolden(t, second); string(repeated) != string(first) {
				t.Fatalf("analysis is not deterministic for %s (only generated_at may differ)", scenario)
			}

			expectedPath := filepath.Join(goldenRootDir, scenario, "expected", "report.json")
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected report: %v", err)
			}
			if err := schema.ValidateReport(expectedRaw); err != nil {
				t.Fatalf("expected report for %s fails schema validation: %v", scenario, err)
			}
			expectedReport, err := report.Load(expectedPath)
			if err != nil {
				t.Fatalf("load expected report: %v", err)
			}
			normalizeGolden(expectedReport)
			expected := marshalGolden(t, expectedReport)
			if string(expected) != string(first) {
				t.Fatalf("golden mismatch for %s: %s", scenario, firstDifference(string(expected), string(first)))
			}

			if err := schema.ValidateReport(first); err != nil {
				t.Fatalf("actual report for %s fails schema validation: %v", scenario, err)
			}
			assertEvidenceDisjoint(t, scenario, rep.Hypotheses)

			switch scenario {
			case "missing-01":
				if len(rep.Hypotheses) != 0 {
					t.Fatalf("missing-evidence bundle must have no hypotheses, got %+v", rep.Hypotheses)
				}
				if len(rep.MissingData) == 0 {
					t.Fatal("missing-evidence bundle must report missing_data")
				}
				if len(rep.Evidence) != 0 {
					t.Fatalf("missing-evidence bundle must have no evidence, got %d", len(rep.Evidence))
				}
			case "injection-01":
				assertNoInjection(t, scenario, rep, first)
				if len(rep.Hypotheses) == 0 {
					t.Fatal("injection bundle still contains real OOM evidence and must yield a hypothesis")
				}
			}
		})
	}
}

// assertEvidenceDisjoint enforces that counter-evidence never doubles as cited
// support: an entry cannot both strengthen and weaken the same hypothesis.
func assertEvidenceDisjoint(t *testing.T, scenario string, hypotheses []report.Hypothesis) {
	t.Helper()
	for _, hypothesis := range hypotheses {
		supported := make(map[string]bool, len(hypothesis.EvidenceIDs))
		for _, id := range hypothesis.EvidenceIDs {
			supported[id] = true
		}
		for _, contradiction := range hypothesis.Contradictions {
			if supported[contradiction.EvidenceID] {
				t.Fatalf("%s: hypothesis rank %d cites %q as both support and contradiction",
					scenario, hypothesis.Rank, contradiction.EvidenceID)
			}
		}
	}
}

func assertNoInjection(t *testing.T, scenario string, rep *report.Report, reportJSON []byte) {
	t.Helper()
	mdPath := filepath.Join(t.TempDir(), "report.md")
	if err := rep.WriteMarkdown(mdPath); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	for _, token := range injectionForbidden {
		if strings.Contains(strings.ToLower(string(reportJSON)), token) {
			t.Fatalf("%s: report.json leaks forbidden token %q", scenario, token)
		}
		if strings.Contains(strings.ToLower(string(md)), token) {
			t.Fatalf("%s: report.md leaks forbidden token %q", scenario, token)
		}
	}
}

// TestGoldenMaxEvidenceTrims exercises the evidence cap: only two entries are
// kept, the trimming is disclosed in limitations and every cited id exists.
func TestGoldenMaxEvidenceTrims(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "oom-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	copyGoldenBundle(t, filepath.Join(goldenRootDir, "oom-01"), dir)

	loaded, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := Analyze(loaded, goldenCLIVersion, Options{MaxEvidence: 2, Now: goldenNow(t)})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(rep.Evidence) != 2 {
		t.Fatalf("evidence = %d, want 2 after trim", len(rep.Evidence))
	}
	trimDisclosed := false
	for _, limitation := range rep.Limitations {
		if strings.Contains(limitation, "裁剪") {
			trimDisclosed = true
		}
	}
	if !trimDisclosed {
		t.Fatalf("trim must be disclosed in limitations: %v", rep.Limitations)
	}
	kept := map[string]bool{}
	for _, item := range rep.Evidence {
		kept[item.ID] = true
	}
	for _, hypothesis := range rep.Hypotheses {
		for _, id := range hypothesis.EvidenceIDs {
			if !kept[id] {
				t.Fatalf("hypothesis cites trimmed evidence %q; kept=%v", id, kept)
			}
		}
	}
}

// TestGoldenBaselineCoverage guards the frozen M1 baseline shape: exactly 12
// bundles, at least two scenarios with counter-evidence, all three confidence
// levels exercised, and at most three ranked hypotheses per report.
func TestGoldenBaselineCoverage(t *testing.T) {
	if len(goldenScenarios) != 12 {
		t.Fatalf("golden baseline = %d scenarios, want 12", len(goldenScenarios))
	}
	contradictionScenarios := 0
	confidences := map[string]bool{}
	for _, scenario := range goldenScenarios {
		base := filepath.Join(goldenRootDir, scenario)
		if _, err := os.Stat(filepath.Join(base, "incident.yaml")); err != nil {
			t.Fatalf("%s: incident.yaml missing: %v", scenario, err)
		}
		expectedPath := filepath.Join(base, "expected", "report.json")
		raw, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("%s: expected report missing: %v", scenario, err)
		}
		if err := schema.ValidateReport(raw); err != nil {
			t.Fatalf("%s: expected report invalid: %v", scenario, err)
		}
		rep, err := report.Load(expectedPath)
		if err != nil {
			t.Fatalf("%s: load expected report: %v", scenario, err)
		}
		if len(rep.Hypotheses) > 3 {
			t.Fatalf("%s: %d hypotheses, want <= 3", scenario, len(rep.Hypotheses))
		}
		statements := map[string]bool{}
		for i, hypothesis := range rep.Hypotheses {
			if hypothesis.Rank != i+1 {
				t.Fatalf("%s: rank = %d at position %d", scenario, hypothesis.Rank, i)
			}
			if len(hypothesis.EvidenceIDs) == 0 {
				t.Fatalf("%s: hypothesis without evidence ids", scenario)
			}
			if hypothesis.Contradictions == nil {
				t.Fatalf("%s: contradictions must be an array", scenario)
			}
			if statements[hypothesis.Statement] {
				t.Fatalf("%s: duplicate hypothesis statement", scenario)
			}
			statements[hypothesis.Statement] = true
			confidences[hypothesis.Confidence] = true
			if len(hypothesis.Contradictions) > 0 {
				contradictionScenarios++
			}
		}
		assertEvidenceDisjoint(t, scenario, rep.Hypotheses)
	}
	if contradictionScenarios < 2 {
		t.Fatalf("golden baseline needs counter-evidence in at least 2 scenarios, got %d", contradictionScenarios)
	}
	for _, level := range []string{"high", "mid", "low"} {
		if !confidences[level] {
			t.Fatalf("golden baseline does not exercise confidence level %q", level)
		}
	}
}

// TestGoldenUpdate regenerates expected/report.json. It is skipped unless
// UPDATE_GOLDEN is set so that normal test runs are strict comparisons.
func TestGoldenUpdate(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") == "" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate testdata/golden/*/expected/report.json")
	}
	for _, scenario := range goldenScenarios {
		rep := analyzeGoldenOnce(t, scenario)
		normalizeGolden(rep)
		dir := filepath.Join(goldenRootDir, scenario, "expected")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := rep.WriteJSON(filepath.Join(dir, "report.json")); err != nil {
			t.Fatalf("write expected report for %s: %v", scenario, err)
		}
		t.Logf("updated %s", scenario)
	}
}
