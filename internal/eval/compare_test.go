package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compareTestResult builds an eval Result with a consistent summary for the
// given cases (callers may override the summary when they need a mismatch).
func compareTestResult(cases ...CaseResult) *Result {
	summary := Summary{Total: len(cases), MissingOK: len(cases), InjectionSafe: len(cases)}
	for _, c := range cases {
		if c.Pass {
			summary.Pass++
		}
		if c.Top1 {
			summary.Top1++
		}
		if c.Top3 {
			summary.Top3++
		}
		if !c.InjectionSafe {
			summary.InjectionSafe--
		}
		if c.Degraded {
			summary.Degraded++
		}
		if c.Error != "" {
			summary.Errors++
		}
	}
	return &Result{Kind: Kind, Version: ContractVersion, Cases: cases, Summary: summary}
}

// compareTestCase builds an all-good case result that individual tests mutate.
func compareTestCase(name string) CaseResult {
	return CaseResult{
		Name:          name,
		Pass:          true,
		Top1:          true,
		Top3:          true,
		MissingOK:     true,
		InjectionSafe: true,
		Degraded:      false,
		Error:         "",
	}
}

// writeCompareBaselineFile stores a Result as a JSON baseline file.
func writeCompareBaselineFile(t *testing.T, result *Result) string {
	t.Helper()
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return path
}

func findCompareChange(t *testing.T, comparison *CompareResult, name string) CompareChange {
	t.Helper()
	for _, change := range comparison.Changes {
		if change.Name == name {
			return change
		}
	}
	t.Fatalf("change %q not found in %+v", name, comparison.Changes)
	return CompareChange{}
}

func TestLoadBaselineRoundTrip(t *testing.T) {
	baseline := compareTestResult(compareTestCase("oom-01"), compareTestCase("missing-01"))
	path := writeCompareBaselineFile(t, baseline)
	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if loaded.Kind != Kind || loaded.Version != ContractVersion {
		t.Fatalf("kind/version = %q/%q", loaded.Kind, loaded.Version)
	}
	if len(loaded.Cases) != 2 || loaded.Cases[0].Name != "oom-01" {
		t.Fatalf("cases = %+v", loaded.Cases)
	}
	if loaded.Summary.Total != 2 {
		t.Fatalf("summary = %+v", loaded.Summary)
	}
}

func TestLoadBaselineErrors(t *testing.T) {
	invalidJSON := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidJSON, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write invalid baseline: %v", err)
	}
	wrongKind := compareTestResult(compareTestCase("oom-01"))
	wrongKind.Kind = "eval-set"
	wrongVersion := compareTestResult(compareTestCase("oom-01"))
	wrongVersion.Version = "2.0"
	emptyCases := &Result{Kind: Kind, Version: ContractVersion}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"empty path", "   ", "不能为空"},
		{"missing file", filepath.Join(t.TempDir(), "missing.json"), "读取基线文件失败"},
		{"invalid JSON", invalidJSON, "不是合法的 eval JSON"},
		{"wrong kind", writeCompareBaselineFile(t, wrongKind), "kind="},
		{"wrong version", writeCompareBaselineFile(t, wrongVersion), "version="},
		{"no cases", writeCompareBaselineFile(t, emptyCases), "不含任何用例"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			baseline, err := LoadBaseline(tc.path)
			if err == nil {
				t.Fatalf("LoadBaseline succeeded with %+v, want error", baseline)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestCompareSame(t *testing.T) {
	baseline := compareTestResult(compareTestCase("oom-01"), compareTestCase("missing-01"), compareTestCase("injection-01"))
	current := compareTestResult(compareTestCase("oom-01"), compareTestCase("missing-01"), compareTestCase("injection-01"))
	comparison := Compare(baseline, current)
	if comparison.Kind != CompareKind || comparison.Version != CompareContractVersion {
		t.Fatalf("kind/version = %q/%q", comparison.Kind, comparison.Version)
	}
	if comparison.Summary != (CompareSummary{Same: 3}) {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
	if comparison.HasRegressions() {
		t.Fatal("identical results must not regress")
	}
	for _, change := range comparison.Changes {
		if change.Status != StatusSame || len(change.Fields) != 0 {
			t.Fatalf("change = %+v", change)
		}
		if change.Fields == nil {
			t.Fatal("fields must be an empty array, not null")
		}
	}
	if comparison.BaselineSummary.Total != 3 || comparison.CurrentSummary.Total != 3 {
		t.Fatalf("summaries = %+v / %+v", comparison.BaselineSummary, comparison.CurrentSummary)
	}
}

func TestCompareRegressionFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CaseResult)
		fields []string
	}{
		{"pass", func(c *CaseResult) { c.Pass = false }, []string{"pass"}},
		{"top1", func(c *CaseResult) { c.Top1 = false }, []string{"top1"}},
		{"top3", func(c *CaseResult) { c.Top3 = false }, []string{"top3"}},
		{"injection_safe", func(c *CaseResult) { c.InjectionSafe = false }, []string{"injection_safe"}},
		{"degraded", func(c *CaseResult) { c.Degraded = true }, []string{"degraded"}},
		{"error", func(c *CaseResult) { c.Error = "analysis failed" }, []string{"error"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			baseCase := compareTestCase("case-1")
			curCase := compareTestCase("case-1")
			tc.mutate(&curCase)
			comparison := Compare(compareTestResult(baseCase), compareTestResult(curCase))
			change := findCompareChange(t, comparison, "case-1")
			if change.Status != StatusRegressed {
				t.Fatalf("status = %q, want %q", change.Status, StatusRegressed)
			}
			if strings.Join(change.Fields, ",") != strings.Join(tc.fields, ",") {
				t.Fatalf("fields = %v, want %v", change.Fields, tc.fields)
			}
			if !comparison.HasRegressions() || comparison.Summary.Regressions != 1 {
				t.Fatalf("summary = %+v", comparison.Summary)
			}
		})
	}

	t.Run("multiple fields sorted", func(t *testing.T) {
		curCase := compareTestCase("case-1")
		curCase.Pass = false
		curCase.Top1 = false
		curCase.Top3 = false
		comparison := Compare(compareTestResult(compareTestCase("case-1")), compareTestResult(curCase))
		change := findCompareChange(t, comparison, "case-1")
		if want := "pass,top1,top3"; strings.Join(change.Fields, ",") != want {
			t.Fatalf("fields = %v, want %s", change.Fields, want)
		}
	})
}

func TestCompareImprovement(t *testing.T) {
	baseCase := compareTestCase("case-1")
	baseCase.Pass = false
	baseCase.Top1 = false
	baseCase.Top3 = false
	curCase := compareTestCase("case-1")
	comparison := Compare(compareTestResult(baseCase), compareTestResult(curCase))
	change := findCompareChange(t, comparison, "case-1")
	if change.Status != StatusImproved {
		t.Fatalf("status = %q, want %q", change.Status, StatusImproved)
	}
	if want := "pass,top1,top3"; strings.Join(change.Fields, ",") != want {
		t.Fatalf("fields = %v, want %s", change.Fields, want)
	}
	if comparison.HasRegressions() || comparison.Summary.Improvements != 1 {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
}

func TestCompareRegressionBeatsImprovement(t *testing.T) {
	baseCase := compareTestCase("case-1")
	baseCase.Top1 = false
	curCase := compareTestCase("case-1")
	curCase.InjectionSafe = false
	comparison := Compare(compareTestResult(baseCase), compareTestResult(curCase))
	change := findCompareChange(t, comparison, "case-1")
	if change.Status != StatusRegressed {
		t.Fatalf("status = %q, want %q", change.Status, StatusRegressed)
	}
	if want := "injection_safe"; strings.Join(change.Fields, ",") != want {
		t.Fatalf("fields = %v, want %s", change.Fields, want)
	}
	if comparison.Summary.Improvements != 0 {
		t.Fatalf("improvement must not be counted when a regression exists: %+v", comparison.Summary)
	}
}

func TestCompareAddedAndMissing(t *testing.T) {
	baseline := compareTestResult(compareTestCase("a-same"), compareTestCase("gone-01"))
	current := compareTestResult(compareTestCase("a-same"), compareTestCase("new-01"))
	comparison := Compare(baseline, current)
	want := CompareSummary{Same: 1, Added: 1, Missing: 1}
	if comparison.Summary != want {
		t.Fatalf("summary = %+v, want %+v", comparison.Summary, want)
	}
	if !comparison.HasRegressions() {
		t.Fatal("a missing baseline case must fail the gate")
	}
	if change := findCompareChange(t, comparison, "gone-01"); change.Status != StatusMissing {
		t.Fatalf("gone-01 change = %+v", change)
	}
	if change := findCompareChange(t, comparison, "new-01"); change.Status != StatusAdded {
		t.Fatalf("new-01 change = %+v", change)
	}
	for i := 1; i < len(comparison.Changes); i++ {
		if comparison.Changes[i-1].Name >= comparison.Changes[i].Name {
			t.Fatalf("changes not sorted: %+v", comparison.Changes)
		}
	}

	addedOnly := Compare(compareTestResult(compareTestCase("a-same")),
		compareTestResult(compareTestCase("a-same"), compareTestCase("new-01")))
	if addedOnly.HasRegressions() {
		t.Fatal("case additions must not fail the gate")
	}
	if addedOnly.Summary.Added != 1 || addedOnly.Summary.Missing != 0 {
		t.Fatalf("summary = %+v", addedOnly.Summary)
	}
}

func TestCompareJSONContract(t *testing.T) {
	baseline := compareTestResult(compareTestCase("a-regressed"), compareTestCase("b-same"), compareTestCase("c-improved"))
	current := compareTestResult(compareTestCase("a-regressed"), compareTestCase("b-same"), compareTestCase("c-improved"))
	// a-regressed loses Top1; c-improved gains Top1.
	current.Cases[0].Top1 = false
	baseline.Cases[2].Top1 = false
	comparison := Compare(baseline, current)
	if comparison.Summary != (CompareSummary{Regressions: 1, Improvements: 1, Same: 1}) {
		t.Fatalf("summary = %+v", comparison.Summary)
	}

	first, err := json.Marshal(comparison)
	if err != nil {
		t.Fatalf("marshal comparison: %v", err)
	}
	second, err := json.Marshal(Compare(baseline, current))
	if err != nil {
		t.Fatalf("marshal second comparison: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("eval-compare JSON must be deterministic")
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(first, &top); err != nil {
		t.Fatalf("unmarshal comparison: %v", err)
	}
	assertEvalKeys(t, "eval-compare", top, []string{"kind", "version", "baseline_summary", "current_summary", "changes", "summary"})

	var changes []map[string]json.RawMessage
	if err := json.Unmarshal(top["changes"], &changes); err != nil {
		t.Fatalf("unmarshal changes: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	assertEvalKeys(t, "eval-compare change", changes[0], []string{"name", "status", "fields"})
	if string(changes[0]["fields"]) == "null" {
		t.Fatal("fields must be an array, never null")
	}

	var comparisonSummary map[string]json.RawMessage
	if err := json.Unmarshal(top["summary"], &comparisonSummary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	assertEvalKeys(t, "eval-compare summary", comparisonSummary, []string{"regressions", "improvements", "same", "added", "missing"})

	var baselineSummary map[string]json.RawMessage
	if err := json.Unmarshal(top["baseline_summary"], &baselineSummary); err != nil {
		t.Fatalf("unmarshal baseline summary: %v", err)
	}
	assertEvalKeys(t, "eval-compare baseline_summary", baselineSummary, []string{"total", "top1", "top3", "missing_ok", "injection_safe", "degraded", "errors", "pass"})

	text := string(first)
	for _, forbidden := range []string{"generated_at", "timestamp", "2026-"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("eval-compare JSON must not contain %q", forbidden)
		}
	}
}

// TestCompareGoldenRunRoundTrip is the package-level acceptance test: re-running
// the checked-in golden set against its own saved Result has no regressions.
func TestCompareGoldenRunRoundTrip(t *testing.T) {
	current, err := Run(Config{CasesDir: goldenDir, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	path := writeCompareBaselineFile(t, current)
	baseline, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	comparison := Compare(baseline, current)
	if comparison.HasRegressions() {
		t.Fatalf("golden round trip regressed: %+v", comparison.Summary)
	}
	if comparison.Summary.Same != 12 || comparison.Summary.Added != 0 || comparison.Summary.Missing != 0 {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
}
