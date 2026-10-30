package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldenDir is the checked-in M1/M2 baseline, resolved from internal/eval.
const goldenDir = "../../testdata/golden"

// evalFixedNow is the clock the golden expected reports were generated with.
func evalFixedNow(t *testing.T) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, "2026-10-30T14:00:00Z")
	if err != nil {
		t.Fatalf("parse fixed now: %v", err)
	}
	return parsed
}

// copyEvalTree copies a directory tree (including expected/) into dst.
func copyEvalTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", src, dst, err)
	}
}

// copyEvalGolden copies one golden case into root and returns the case dir.
func copyEvalGolden(t *testing.T, root, name string) string {
	t.Helper()
	dst := filepath.Join(root, name)
	copyEvalTree(t, filepath.Join(goldenDir, name), dst)
	return dst
}

func writeEvalFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findEvalCase(t *testing.T, result *Result, name string) CaseResult {
	t.Helper()
	for _, c := range result.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("case %q not found in result", name)
	return CaseResult{}
}

// TestRunGoldenBaselineReplays is the acceptance test: all 12 checked-in golden
// cases must reproduce their expected reports with the fixed golden clock.
func TestRunGoldenBaselineReplays(t *testing.T) {
	result, err := Run(Config{CasesDir: goldenDir, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != Kind || result.Version != ContractVersion {
		t.Fatalf("result header = %q/%q, want %q/%q", result.Kind, result.Version, Kind, ContractVersion)
	}
	if result.Summary.Total != 12 {
		t.Fatalf("total = %d, want the 12-case baseline", result.Summary.Total)
	}
	if result.Summary.Pass != 12 {
		t.Fatalf("pass = %d, want 12 (summary %+v)", result.Summary.Pass, result.Summary)
	}
	if result.Summary.Errors != 0 || result.Summary.Degraded != 0 {
		t.Fatalf("unexpected errors/degraded: %+v", result.Summary)
	}
	if result.Summary.InjectionSafe != 12 {
		t.Fatalf("injection_safe = %d, want 12", result.Summary.InjectionSafe)
	}
	for i := 1; i < len(result.Cases); i++ {
		if result.Cases[i-1].Name >= result.Cases[i].Name {
			t.Fatalf("cases are not sorted by name: %q before %q", result.Cases[i-1].Name, result.Cases[i].Name)
		}
	}
	for _, c := range result.Cases {
		if c.Error != "" {
			t.Fatalf("%s: unexpected error: %s", c.Name, c.Error)
		}
		if !c.Pass || c.Degraded || !c.InjectionSafe {
			t.Fatalf("%s: pass=%v degraded=%v injection_safe=%v", c.Name, c.Pass, c.Degraded, c.InjectionSafe)
		}
		expected, err := loadExpected(filepath.Join(goldenDir, c.Name, filepath.FromSlash(ExpectedReportPath)))
		if err != nil {
			t.Fatalf("%s: load expected: %v", c.Name, err)
		}
		if len(expected.Hypotheses) > 0 {
			if !c.Top1 || !c.Top3 || !c.MissingOK {
				t.Fatalf("%s: top1=%v top3=%v missing_ok=%v, want all true", c.Name, c.Top1, c.Top3, c.MissingOK)
			}
		} else {
			if c.Top1 || c.Top3 {
				t.Fatalf("%s: expected report has no hypotheses but top1=%v top3=%v", c.Name, c.Top1, c.Top3)
			}
			if !c.MissingOK {
				t.Fatalf("%s: expected report has no hypotheses but missing_ok=false", c.Name)
			}
		}
	}
	if result.Summary.Top1 != 11 || result.Summary.Top3 != 11 {
		t.Fatalf("top1/top3 = %d/%d, want 11/11 (missing-01 has no hypotheses)", result.Summary.Top1, result.Summary.Top3)
	}
	missing := findEvalCase(t, result, "missing-01")
	if !missing.MissingOK || missing.Top1 || !missing.Pass {
		t.Fatalf("missing-01: missing_ok=%v top1=%v pass=%v", missing.MissingOK, missing.Top1, missing.Pass)
	}
	injection := findEvalCase(t, result, "injection-01")
	if !injection.InjectionSafe || !injection.Pass || !injection.Top1 {
		t.Fatalf("injection-01: injection_safe=%v pass=%v top1=%v", injection.InjectionSafe, injection.Pass, injection.Top1)
	}
}

// TestRunDeterministicJSON guards the stable --json contract: no timestamps and
// byte-identical output across runs.
func TestRunDeterministicJSON(t *testing.T) {
	cfg := Config{CasesDir: goldenDir, Now: evalFixedNow(t)}
	first, err := Run(cfg)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first result: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second result: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("eval JSON output is not deterministic")
	}
	text := string(firstJSON)
	for _, forbidden := range []string{"generated_at", "timestamp", "2026-"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("eval JSON must not contain %q: %s", forbidden, forbidden)
		}
	}
}

// TestRunTempCases covers filter, case ordering and the MaxCases cap using
// copied golden fixtures in a temp dir.
func TestRunTempCases(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"oom-01", "crashloop-01", "missing-01"} {
		copyEvalGolden(t, root, name)
	}
	now := evalFixedNow(t)

	filtered, err := Run(Config{CasesDir: root, Filter: "oom", Now: now})
	if err != nil {
		t.Fatalf("filtered Run: %v", err)
	}
	if filtered.Summary.Total != 1 || filtered.Cases[0].Name != "oom-01" {
		t.Fatalf("filter mismatch: %+v", filtered.Cases)
	}
	if filtered.Summary.Pass != 1 {
		t.Fatalf("filtered pass = %d, want 1", filtered.Summary.Pass)
	}

	capped, err := Run(Config{CasesDir: root, MaxCases: 2, Now: now})
	if err != nil {
		t.Fatalf("capped Run: %v", err)
	}
	if capped.Summary.Total != 2 {
		t.Fatalf("capped total = %d, want 2", capped.Summary.Total)
	}
	if capped.Cases[0].Name != "crashloop-01" || capped.Cases[1].Name != "missing-01" {
		t.Fatalf("capped case order = %q, %q", capped.Cases[0].Name, capped.Cases[1].Name)
	}
	if capped.Summary.Pass != 2 {
		t.Fatalf("capped pass = %d, want 2", capped.Summary.Pass)
	}
}

// TestRunDiscoveryErrors maps every discovery-level problem to an error so the
// CLI can return exit code 1.
func TestRunDiscoveryErrors(t *testing.T) {
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	filterRoot := t.TempDir()
	copyEvalGolden(t, filterRoot, "oom-01")

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty cases dir", Config{}, "不能为空"},
		{"missing cases dir", Config{CasesDir: filepath.Join(t.TempDir(), "missing")}, "不存在"},
		{"empty directory", Config{CasesDir: t.TempDir()}, "没有可用例"},
		{"file instead of dir", Config{CasesDir: fileAsDir}, "不是目录"},
		{"filter without match", Config{CasesDir: filterRoot, Filter: "zzz-no-such-case"}, "没有匹配"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Run(tc.cfg)
			if err == nil {
				t.Fatalf("Run succeeded with %d cases, want error", len(result.Cases))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// TestRunBrokenCaseRecorded checks that a broken fixture is a per-case error,
// not a run-level failure.
func TestRunBrokenCaseRecorded(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "broken-01")
	writeEvalFile(t, filepath.Join(caseDir, "incident.yaml"), "id: broken")
	writeEvalFile(t, filepath.Join(caseDir, filepath.FromSlash(ExpectedReportPath)), "{}")

	result, err := Run(Config{CasesDir: root, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Total != 1 || result.Summary.Errors != 1 || result.Summary.Pass != 0 {
		t.Fatalf("summary = %+v, want 1 total / 1 error / 0 pass", result.Summary)
	}
	c := result.Cases[0]
	if c.Pass || c.Error == "" || !strings.Contains(c.Error, "加载事故包失败") {
		t.Fatalf("case = %+v, want recorded bundle error", c)
	}
}

// TestRunInvalidExpectedReportRecorded checks that malformed or schema-invalid
// expected reports are recorded as case errors.
func TestRunInvalidExpectedReportRecorded(t *testing.T) {
	root := t.TempDir()
	caseDir := copyEvalGolden(t, root, "oom-01")
	expectedPath := filepath.Join(caseDir, filepath.FromSlash(ExpectedReportPath))
	if err := os.WriteFile(expectedPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write broken expected: %v", err)
	}
	result, err := Run(Config{CasesDir: root, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run broken JSON: %v", err)
	}
	if result.Summary.Errors != 1 || !strings.Contains(result.Cases[0].Error, "读取期望报告失败") {
		t.Fatalf("broken expected report case = %+v", result.Cases[0])
	}

	if err := os.WriteFile(expectedPath, []byte(`{"schema_version":"9.9"}`), 0o644); err != nil {
		t.Fatalf("write schema-invalid expected: %v", err)
	}
	result, err = Run(Config{CasesDir: root, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run schema-invalid: %v", err)
	}
	if result.Summary.Errors != 1 || !strings.Contains(result.Cases[0].Error, "不符合 report schema") {
		t.Fatalf("schema-invalid expected report case = %+v", result.Cases[0])
	}
}

// TestRunMismatchedExpectedFails checks that a wrong expected report makes the
// case fail without a run-level error.
func TestRunMismatchedExpectedFails(t *testing.T) {
	root := t.TempDir()
	caseDir := copyEvalGolden(t, root, "oom-01")
	source := filepath.Join(goldenDir, "missing-01", filepath.FromSlash(ExpectedReportPath))
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read source expected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, filepath.FromSlash(ExpectedReportPath)), data, 0o644); err != nil {
		t.Fatalf("write mismatched expected: %v", err)
	}

	result, err := Run(Config{CasesDir: root, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := result.Cases[0]
	if c.Pass || c.Top1 || c.MissingOK {
		t.Fatalf("mismatched case = %+v, want pass=false top1=false missing_ok=false", c)
	}
	if result.Summary.Errors != 0 {
		t.Fatalf("mismatch must not count as an error: %+v", result.Summary)
	}
}

// TestRunJSONContractShape freezes the Result JSON keys documented in
// docs/eval.md.
func TestRunJSONContractShape(t *testing.T) {
	result, err := Run(Config{CasesDir: goldenDir, Now: evalFixedNow(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	assertEvalKeys(t, "result", top, []string{"kind", "version", "cases", "summary"})

	var cases []map[string]json.RawMessage
	if err := json.Unmarshal(top["cases"], &cases); err != nil {
		t.Fatalf("unmarshal cases: %v", err)
	}
	if len(cases) != 12 {
		t.Fatalf("cases = %d, want 12", len(cases))
	}
	assertEvalKeys(t, "case", cases[0], []string{"name", "pass", "top1", "top3", "missing_ok", "injection_safe", "degraded", "error"})

	var summary map[string]json.RawMessage
	if err := json.Unmarshal(top["summary"], &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	assertEvalKeys(t, "summary", summary, []string{"total", "top1", "top3", "missing_ok", "injection_safe", "degraded", "errors", "pass"})
}

func assertEvalKeys(t *testing.T, label string, object map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("%s keys = %v, want %v", label, evalKeyNames(object), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Fatalf("%s missing key %q: %v", label, key, evalKeyNames(object))
		}
	}
}

func evalKeyNames(object map[string]json.RawMessage) []string {
	names := make([]string, 0, len(object))
	for key := range object {
		names = append(names, key)
	}
	return names
}
