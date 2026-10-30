package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustValidateSet runs ValidateSet and fails on discovery errors.
func mustValidateSet(t *testing.T, root string) *SetValidation {
	t.Helper()
	return mustValidateSetConfig(t, Config{CasesDir: root})
}

func mustValidateSetConfig(t *testing.T, cfg Config) *SetValidation {
	t.Helper()
	validation, err := ValidateSet(cfg)
	if err != nil {
		t.Fatalf("ValidateSet(%+v): %v", cfg, err)
	}
	return validation
}

func findSetCase(t *testing.T, validation *SetValidation, name string) SetCaseResult {
	t.Helper()
	for _, c := range validation.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("case %q not found in %+v", name, validation.Cases)
	return SetCaseResult{}
}

func requireSetErrors(t *testing.T, c SetCaseResult, substrings ...string) {
	t.Helper()
	joined := strings.Join(c.Errors, string([]byte{10}))
	for _, want := range substrings {
		if !strings.Contains(joined, want) {
			t.Fatalf("case %q errors %v missing %q", c.Name, c.Errors, want)
		}
	}
}

func validationJSON(t *testing.T, validation *SetValidation) string {
	t.Helper()
	data, err := json.Marshal(validation)
	if err != nil {
		t.Fatalf("marshal validation: %v", err)
	}
	return string(data)
}

// TestValidateSetGoldenBaseline validates the checked-in 12-case baseline.
func TestValidateSetGoldenBaseline(t *testing.T) {
	validation := mustValidateSet(t, goldenDir)
	if validation.Kind != SetKind || validation.Version != SetContractVersion {
		t.Fatalf("kind/version = %q/%q, want %q/%q", validation.Kind, validation.Version, SetKind, SetContractVersion)
	}
	if len(validation.Cases) != 12 {
		t.Fatalf("cases = %d, want 12", len(validation.Cases))
	}
	summary := validation.Summary
	if summary.Total != 12 || summary.Valid != 12 || summary.Invalid != 0 || summary.MinCasesRequired != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	for i := 1; i < len(validation.Cases); i++ {
		if validation.Cases[i-1].Name >= validation.Cases[i].Name {
			t.Fatalf("cases are not sorted by name: %q before %q", validation.Cases[i-1].Name, validation.Cases[i].Name)
		}
	}
	for _, c := range validation.Cases {
		if !c.Valid {
			t.Fatalf("%s: valid=false, errors=%v", c.Name, c.Errors)
		}
		if len(c.Errors) != 0 {
			t.Fatalf("%s: valid case has errors %v", c.Name, c.Errors)
		}
	}
}

// TestValidateSetJSONContract freezes the eval-set JSON shape and determinism.
func TestValidateSetJSONContract(t *testing.T) {
	first := mustValidateSet(t, goldenDir)
	second := mustValidateSet(t, goldenDir)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("eval-set JSON must be deterministic across runs")
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(firstJSON, &top); err != nil {
		t.Fatalf("unmarshal validation: %v", err)
	}
	assertEvalKeys(t, "eval-set", top, []string{"kind", "version", "cases", "summary"})

	var cases []map[string]json.RawMessage
	if err := json.Unmarshal(top["cases"], &cases); err != nil {
		t.Fatalf("unmarshal cases: %v", err)
	}
	assertEvalKeys(t, "eval-set case", cases[0], []string{"name", "valid", "errors"})
	if string(cases[0]["errors"]) == "null" {
		t.Fatal("errors must be an array, never null")
	}

	var summary map[string]json.RawMessage
	if err := json.Unmarshal(top["summary"], &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	assertEvalKeys(t, "eval-set summary", summary, []string{"total", "valid", "invalid", "min_cases_required"})

	text := string(firstJSON)
	for _, forbidden := range []string{"generated_at", "timestamp", "2026-"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("eval-set JSON must not contain %q", forbidden)
		}
	}
}

func TestValidateSetMinCases(t *testing.T) {
	required := mustValidateSetConfig(t, Config{CasesDir: goldenDir, MinCases: 100})
	if required.Summary.MinCasesRequired != 100 {
		t.Fatalf("min_cases_required = %d, want 100", required.Summary.MinCasesRequired)
	}
	if required.Summary.Total != 12 || required.Summary.Valid != 12 || required.Summary.Invalid != 0 {
		t.Fatalf("summary = %+v", required.Summary)
	}

	defaulted := mustValidateSet(t, goldenDir)
	if defaulted.Summary.MinCasesRequired != 0 {
		t.Fatalf("min_cases_required = %d, want 0", defaulted.Summary.MinCasesRequired)
	}
}

func TestValidateSetFilterAndMaxCases(t *testing.T) {
	filtered := mustValidateSetConfig(t, Config{CasesDir: goldenDir, Filter: "oom"})
	if filtered.Summary.Total != 2 {
		t.Fatalf("filtered total = %d, want 2", filtered.Summary.Total)
	}
	if filtered.Cases[0].Name != "oom-01" || filtered.Cases[1].Name != "oom-02" {
		t.Fatalf("filtered cases = %+v", filtered.Cases)
	}

	capped := mustValidateSetConfig(t, Config{CasesDir: goldenDir, MaxCases: 3})
	if capped.Summary.Total != 3 {
		t.Fatalf("capped total = %d, want 3", capped.Summary.Total)
	}
	for i, want := range []string{"crashloop-01", "crashloop-02", "imagepull-01"} {
		if capped.Cases[i].Name != want {
			t.Fatalf("capped case %d = %q, want %q", i, capped.Cases[i].Name, want)
		}
	}
}

func TestValidateSetMissingCaseFiles(t *testing.T) {
	t.Run("missing expected report", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		if err := os.RemoveAll(filepath.Join(caseDir, "expected")); err != nil {
			t.Fatalf("remove expected: %v", err)
		}
		validation := mustValidateSet(t, root)
		if validation.Summary.Invalid != 1 {
			t.Fatalf("summary = %+v", validation.Summary)
		}
		requireSetErrors(t, validation.Cases[0], "缺少 expected/report.json")
	})

	t.Run("missing incident", func(t *testing.T) {
		root := t.TempDir()
		caseDir := filepath.Join(root, "no-incident")
		expected, err := os.ReadFile(filepath.Join(goldenDir, "oom-01", "expected", "report.json"))
		if err != nil {
			t.Fatalf("read expected fixture: %v", err)
		}
		writeEvalFile(t, filepath.Join(caseDir, "expected", "report.json"), string(expected))
		validation := mustValidateSet(t, root)
		if validation.Summary.Total != 1 || validation.Summary.Invalid != 1 {
			t.Fatalf("summary = %+v", validation.Summary)
		}
		requireSetErrors(t, validation.Cases[0], "缺少 incident.yaml")
	})
}

func TestValidateSetBrokenFixtures(t *testing.T) {
	t.Run("incident schema", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		writeEvalFile(t, filepath.Join(caseDir, "incident.yaml"), "id: broken")
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "incident.yaml 无效")
	})

	t.Run("expected report is not JSON", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		writeEvalFile(t, filepath.Join(caseDir, "expected", "report.json"), "{not json")
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "expected/report.json 无效")
	})

	t.Run("expected report fails schema", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		writeEvalFile(t, filepath.Join(caseDir, "expected", "report.json"), `{"schema_version":"9.9"}`)
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "expected/report.json 无效")
	})
}

func TestValidateSetDuplicateNames(t *testing.T) {
	root := t.TempDir()
	copyEvalTree(t, filepath.Join(goldenDir, "oom-01"), filepath.Join(root, "a", "dup-01"))
	copyEvalTree(t, filepath.Join(goldenDir, "oom-01"), filepath.Join(root, "b", "dup-01"))
	validation := mustValidateSet(t, root)
	if validation.Summary.Total != 2 || validation.Summary.Valid != 0 || validation.Summary.Invalid != 2 {
		t.Fatalf("summary = %+v", validation.Summary)
	}
	for _, c := range validation.Cases {
		if c.Name != "dup-01" || c.Valid {
			t.Fatalf("duplicate case = %+v", c)
		}
		requireSetErrors(t, c, "用例名重复: dup-01")
	}
}

func TestValidateSetCredentialPatterns(t *testing.T) {
	const secret = "password=supersecret123"

	t.Run("evidence file", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		writeEvalFile(t, filepath.Join(caseDir, "logs", "leak.log"), secret)
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "包含疑似凭据模式: logs/leak.log")
		if strings.Contains(validationJSON(t, validation), "supersecret123") {
			t.Fatal("validation JSON must never echo the matched value")
		}
	})

	t.Run("incident yaml", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		incident := strings.Join([]string{
			"id: cred-incident",
			"alertname: KubePodCrashLooping",
			"severity: warning",
			"cluster: local",
			"namespace: checkout",
			"workload: password=supersecret123",
			"observed_at: 2026-10-30T13:50:00Z",
			"collected_at: 2026-10-30T13:50:05Z",
			"source: manual",
		}, string([]byte{10}))
		writeEvalFile(t, filepath.Join(caseDir, "incident.yaml"), incident)
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "包含疑似凭据模式: incident.yaml")
	})

	t.Run("expected report", func(t *testing.T) {
		root := t.TempDir()
		caseDir := copyEvalGolden(t, root, "oom-01")
		expectedPath := filepath.Join(caseDir, "expected", "report.json")
		data, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("read expected report: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal expected report: %v", err)
		}
		doc["summary"] = secret
		modified, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal expected report: %v", err)
		}
		if err := os.WriteFile(expectedPath, modified, 0o644); err != nil {
			t.Fatalf("write expected report: %v", err)
		}
		validation := mustValidateSet(t, root)
		requireSetErrors(t, validation.Cases[0], "包含疑似凭据模式: expected/report.json")
	})
}

// TestValidateSetDiscoveryErrors maps discovery problems to errors so the CLI
// can return exit code 1.
func TestValidateSetDiscoveryErrors(t *testing.T) {
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
			validation, err := ValidateSet(tc.cfg)
			if err == nil {
				t.Fatalf("ValidateSet succeeded with %d cases, want error", len(validation.Cases))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
