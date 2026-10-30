package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/eval"
)

// evalGoldenDir is the checked-in 12-case baseline, resolved from internal/cli.
const evalGoldenDir = "../../testdata/golden"

type evalCommandResult struct {
	code int
	out  string
	err  error
}

// runEvalCommand executes the CLI with the same error-to-exit-code mapping as
// cmd/kubectl-triage, without spawning a process.
func runEvalCommand(t *testing.T, args ...string) evalCommandResult {
	t.Helper()
	root := NewRoot("test", "test-commit", "test-date")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs(args)
	err := root.Execute()
	code := 0
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			code = exit.Code
		} else {
			code = 1
		}
	}
	return evalCommandResult{code: code, out: stdout.String(), err: err}
}

func requireEvalExit(t *testing.T, res evalCommandResult, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit code = %d, want %d (err=%v, out=%q)", res.code, want, res.err, res.out)
	}
}

func parseEvalCommandResult(t *testing.T, out string) eval.Result {
	t.Helper()
	var result eval.Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("parse eval JSON: %v (out=%q)", err, out)
	}
	return result
}

func writeEvalCLIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestEvalCommandGoldenPass(t *testing.T) {
	res := runEvalCommand(t, "eval", evalGoldenDir, "--now", "2026-10-30T14:00:00Z")
	requireEvalExit(t, res, 0)
	for _, want := range []string{"通过 12/12", "摘要", "injection-01", "missing-01"} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("eval output missing %q: %q", want, res.out)
		}
	}
}

func TestEvalCommandJSONStable(t *testing.T) {
	first := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z")
	requireEvalExit(t, first, 0)
	trimmed := strings.TrimSpace(first.out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("--json output must be a JSON object: %q", first.out)
	}
	result := parseEvalCommandResult(t, trimmed)
	if result.Kind != eval.Kind || result.Version != eval.ContractVersion {
		t.Fatalf("kind/version = %q/%q, want %q/%q", result.Kind, result.Version, eval.Kind, eval.ContractVersion)
	}
	if len(result.Cases) != 12 {
		t.Fatalf("cases = %d, want 12", len(result.Cases))
	}
	if result.Summary.Total != 12 || result.Summary.Pass != 12 || result.Summary.Errors != 0 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	second := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z")
	requireEvalExit(t, second, 0)
	if trimmed != strings.TrimSpace(second.out) {
		t.Fatal("--json output must be deterministic across runs")
	}
	if strings.Contains(trimmed, "2026-") {
		t.Fatal("--json output must not contain timestamps")
	}
}

func TestEvalCommandExitTwo(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "broken-01")
	writeEvalCLIFile(t, filepath.Join(caseDir, "incident.yaml"), "id: broken")
	writeEvalCLIFile(t, filepath.Join(caseDir, "expected", "report.json"), "{}")

	res := runEvalCommand(t, "eval", root, "--json")
	requireEvalExit(t, res, 2)
	result := parseEvalCommandResult(t, res.out)
	if result.Summary.Total != 1 || result.Summary.Errors != 1 || result.Summary.Pass != 0 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if result.Cases[0].Error == "" {
		t.Fatalf("case error must be recorded: %+v", result.Cases[0])
	}

	text := runEvalCommand(t, "eval", root)
	requireEvalExit(t, text, 2)
	if !strings.Contains(text.out, "失败") {
		t.Fatalf("text output must report the failure: %q", text.out)
	}
}

func TestEvalCommandExitOne(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing cases dir", []string{"eval", filepath.Join(t.TempDir(), "missing")}},
		{"empty cases dir", []string{"eval", t.TempDir()}},
		{"invalid now", []string{"eval", evalGoldenDir, "--now", "2026-10-30"}},
		{"unknown model provider", []string{"eval", evalGoldenDir, "--model", "bananas"}},
		{"missing argument", []string{"eval"}},
		{"too many arguments", []string{"eval", "a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runEvalCommand(t, tc.args...)
			requireEvalExit(t, res, 1)
		})
	}
}

func TestEvalCommandFilterAndMaxCases(t *testing.T) {
	filtered := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--filter", "missing")
	requireEvalExit(t, filtered, 0)
	result := parseEvalCommandResult(t, filtered.out)
	if result.Summary.Total != 1 || result.Cases[0].Name != "missing-01" {
		t.Fatalf("filtered cases = %+v", result.Cases)
	}

	capped := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--max-cases", "1")
	requireEvalExit(t, capped, 0)
	result = parseEvalCommandResult(t, capped.out)
	if result.Summary.Total != 1 || result.Cases[0].Name != "crashloop-01" {
		t.Fatalf("capped cases = %+v", result.Cases)
	}

	unmatched := runEvalCommand(t, "eval", evalGoldenDir, "--filter", "zzz-no-such-case")
	requireEvalExit(t, unmatched, 1)
}

func TestEvalCommandModelFlagDefaults(t *testing.T) {
	root := NewRoot("test", "none", "unknown")
	cmd, _, err := root.Find([]string{"eval"})
	if err != nil || cmd == nil || cmd.Name() != "eval" {
		t.Fatalf("eval command is not registered (err=%v)", err)
	}
	wants := []struct {
		flag string
		want string
	}{
		{"model", "none"},
		{"model-url", ""},
		{"model-name", ""},
		{"model-timeout", "1m0s"},
		{"model-budget-usd", "1"},
		{"model-max-rounds", "3"},
		{"json", "false"},
		{"filter", ""},
		{"max-cases", "0"},
		{"validate", "false"},
		{"min-cases", "0"},
		{"compare", ""},
		{"now", ""},
	}
	for _, tc := range wants {
		flag := cmd.Flags().Lookup(tc.flag)
		if flag == nil {
			t.Fatalf("eval --%s is not registered", tc.flag)
		}
		if flag.DefValue != tc.want {
			t.Fatalf("eval --%s default = %q, want %q", tc.flag, flag.DefValue, tc.want)
		}
	}
}

func TestEvalCommandModelConfigPassthrough(t *testing.T) {
	original := evalRunner
	defer func() { evalRunner = original }()
	var captured eval.Config
	evalRunner = func(cfg eval.Config) (*eval.Result, error) {
		captured = cfg
		return &eval.Result{
			Kind:    eval.Kind,
			Version: eval.ContractVersion,
			Cases:   []eval.CaseResult{{Name: "canned", Pass: true, InjectionSafe: true}},
			Summary: eval.Summary{Total: 1, Pass: 1, Top1: 1, Top3: 1, MissingOK: 1, InjectionSafe: 1},
		}, nil
	}
	t.Setenv(evalModelKeyEnv, "primary-key")
	t.Setenv(evalOpenAIKeyEnv, "openai-key")

	res := runEvalCommand(t, "eval", "some-cases", "--json",
		"--model", "none",
		"--model-url", "http://127.0.0.1:11434",
		"--model-name", "llama3",
		"--model-timeout", "30s",
		"--model-budget-usd", "0.5",
		"--model-max-rounds", "2",
		"--filter", "oom",
		"--max-cases", "3",
		"--now", "2026-10-30T14:00:00Z")
	requireEvalExit(t, res, 0)

	wantNow, err := time.Parse(time.RFC3339, "2026-10-30T14:00:00Z")
	if err != nil {
		t.Fatalf("parse want now: %v", err)
	}
	if captured.CasesDir != "some-cases" || captured.MaxCases != 3 || captured.Filter != "oom" {
		t.Fatalf("captured run config = %+v", captured)
	}
	if !captured.Now.Equal(wantNow) {
		t.Fatalf("captured now = %v, want %v", captured.Now, wantNow)
	}
	if captured.Model.Provider != "" {
		t.Fatalf("model:none must disable the provider, got %q", captured.Model.Provider)
	}
	if captured.Model.URL != "http://127.0.0.1:11434" || captured.Model.ModelName != "llama3" {
		t.Fatalf("captured model url/name = %q/%q", captured.Model.URL, captured.Model.ModelName)
	}
	if captured.Model.Timeout != 30*time.Second || captured.Model.BudgetUSD != 0.5 || captured.Model.MaxRounds != 2 {
		t.Fatalf("captured model limits = %+v", captured.Model)
	}
	if captured.Model.APIKey != "" {
		t.Fatal("model:none must not read provider keys")
	}
	result := parseEvalCommandResult(t, res.out)
	if result.Summary.Pass != 1 {
		t.Fatalf("canned result summary = %+v", result.Summary)
	}
}

func TestEvalModelConfigKeys(t *testing.T) {
	t.Run("primary key wins", func(t *testing.T) {
		t.Setenv(evalModelKeyEnv, "primary-key")
		t.Setenv(evalOpenAIKeyEnv, "openai-key")
		cfg := evalModelConfig(" OpenAI-Compatible ", " http://127.0.0.1:8000 ", " gpt-4o-mini ", time.Second, 1, 3)
		if cfg.Provider != "openai-compatible" || cfg.URL != "http://127.0.0.1:8000" || cfg.ModelName != "gpt-4o-mini" {
			t.Fatalf("normalized config = %+v", cfg)
		}
		if cfg.APIKey != "primary-key" {
			t.Fatalf("api key = %q, want primary-key", cfg.APIKey)
		}
	})
	t.Run("openai key fallback", func(t *testing.T) {
		t.Setenv(evalModelKeyEnv, "")
		t.Setenv(evalOpenAIKeyEnv, "openai-key")
		cfg := evalModelConfig("openai-compatible", "http://127.0.0.1:8000", "gpt-4o-mini", time.Second, 1, 3)
		if cfg.APIKey != "openai-key" {
			t.Fatalf("api key = %q, want openai-key", cfg.APIKey)
		}
	})
	t.Run("ollama defaults url and shared key fallback", func(t *testing.T) {
		t.Setenv(evalModelKeyEnv, "")
		t.Setenv(evalOpenAIKeyEnv, "openai-key")
		cfg := evalModelConfig("ollama", "", "llama3", time.Second, 1, 3)
		if cfg.URL != evalOllamaDefaultURL {
			t.Fatalf("ollama url = %q, want %q", cfg.URL, evalOllamaDefaultURL)
		}
		if cfg.APIKey != "openai-key" {
			t.Fatalf("ollama api key = %q, want the OPENAI_API_KEY fallback", cfg.APIKey)
		}
	})
	t.Run("openai default model name", func(t *testing.T) {
		t.Setenv(evalModelKeyEnv, "primary-key")
		cfg := evalModelConfig("openai-compatible", "http://127.0.0.1:8000", "", time.Second, 1, 3)
		if cfg.ModelName != evalOpenAIDefaultModel {
			t.Fatalf("openai model name = %q, want %q", cfg.ModelName, evalOpenAIDefaultModel)
		}
	})
	t.Run("none disables", func(t *testing.T) {
		if cfg := evalModelConfig("none", "http://x", "m", time.Second, 1, 3); cfg.Provider != "" {
			t.Fatalf("none provider = %q, want empty", cfg.Provider)
		}
		if cfg := evalModelConfig("", "", "", time.Second, 1, 3); cfg.Provider != "" {
			t.Fatalf("empty provider = %q, want empty", cfg.Provider)
		}
	})

	if err := validateEvalModelConfig(model.Config{}); err != nil {
		t.Fatalf("disabled model config must validate: %v", err)
	}
	if err := validateEvalModelConfig(evalModelConfig("bananas", "http://x", "m", time.Second, 1, 3)); err == nil {
		t.Fatal("unknown provider must be a configuration error")
	}
}

func TestEvalParseNow(t *testing.T) {
	if got, err := parseEvalNow(""); err != nil || !got.IsZero() {
		t.Fatalf("empty --now = %v, %v; want zero time", got, err)
	}
	parsed, err := parseEvalNow("2026-10-30T22:00:00+08:00")
	if err != nil {
		t.Fatalf("parse offset now: %v", err)
	}
	if want := time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC); !parsed.Equal(want) {
		t.Fatalf("parsed now = %v, want %v", parsed, want)
	}
	if _, err := parseEvalNow("2026-10-30"); err == nil {
		t.Fatal("date-only --now must be rejected")
	}
}

func TestEvalCommandValidateGoldenSet(t *testing.T) {
	res := runEvalCommand(t, "eval", evalGoldenDir, "--validate")
	requireEvalExit(t, res, 0)
	for _, want := range []string{"评测集校验", "[有效] oom-01", "有效 12", "无效 0"} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("validate output missing %q: %q", want, res.out)
		}
	}
}

func TestEvalCommandValidateJSONContract(t *testing.T) {
	first := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json")
	requireEvalExit(t, first, 0)
	trimmed := strings.TrimSpace(first.out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("--validate --json output must be a JSON object: %q", first.out)
	}
	var validation eval.SetValidation
	if err := json.Unmarshal([]byte(trimmed), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Kind != eval.SetKind || validation.Version != eval.SetContractVersion {
		t.Fatalf("kind/version = %q/%q, want %q/%q", validation.Kind, validation.Version, eval.SetKind, eval.SetContractVersion)
	}
	if validation.Summary.Total != 12 || validation.Summary.Valid != 12 || validation.Summary.Invalid != 0 || validation.Summary.MinCasesRequired != 0 {
		t.Fatalf("summary = %+v", validation.Summary)
	}
	if len(validation.Cases) != 12 {
		t.Fatalf("cases = %d, want 12", len(validation.Cases))
	}
	second := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json")
	requireEvalExit(t, second, 0)
	if trimmed != strings.TrimSpace(second.out) {
		t.Fatal("--validate --json output must be deterministic across runs")
	}
	if strings.Contains(trimmed, "2026-") {
		t.Fatal("eval-set JSON must not contain timestamps")
	}
}

func TestEvalCommandValidateMinCases(t *testing.T) {
	enough := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json", "--min-cases", "12")
	requireEvalExit(t, enough, 0)
	var validation eval.SetValidation
	if err := json.Unmarshal([]byte(strings.TrimSpace(enough.out)), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Summary.MinCasesRequired != 12 {
		t.Fatalf("min_cases_required = %d, want 12", validation.Summary.MinCasesRequired)
	}

	insufficient := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json", "--min-cases", "100")
	requireEvalExit(t, insufficient, 2)
	validation = eval.SetValidation{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(insufficient.out)), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Summary.MinCasesRequired != 100 || validation.Summary.Total != 12 || validation.Summary.Invalid != 0 {
		t.Fatalf("summary = %+v", validation.Summary)
	}

	text := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--min-cases", "100")
	requireEvalExit(t, text, 2)
	if !strings.Contains(text.out, "数量不足") {
		t.Fatalf("text output must report the shortfall: %q", text.out)
	}
}

func TestEvalCommandValidateInvalidSet(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "broken-01")
	writeEvalCLIFile(t, filepath.Join(caseDir, "incident.yaml"), "id: broken")

	res := runEvalCommand(t, "eval", root, "--validate", "--json")
	requireEvalExit(t, res, 2)
	var validation eval.SetValidation
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.out)), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Summary.Total != 1 || validation.Summary.Valid != 0 || validation.Summary.Invalid != 1 {
		t.Fatalf("summary = %+v", validation.Summary)
	}
	if validation.Cases[0].Valid || len(validation.Cases[0].Errors) == 0 {
		t.Fatalf("broken case must be invalid with errors: %+v", validation.Cases[0])
	}

	text := runEvalCommand(t, "eval", root, "--validate")
	requireEvalExit(t, text, 2)
	for _, want := range []string{"[无效] broken-01", "无效 1"} {
		if !strings.Contains(text.out, want) {
			t.Fatalf("text output missing %q: %q", want, text.out)
		}
	}
}

func TestEvalCommandValidateExitOne(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing cases dir", []string{"eval", filepath.Join(t.TempDir(), "missing"), "--validate"}},
		{"empty cases dir", []string{"eval", t.TempDir(), "--validate"}},
		{"filter without match", []string{"eval", evalGoldenDir, "--validate", "--filter", "zzz-no-such-case"}},
		{"negative min cases", []string{"eval", evalGoldenDir, "--validate", "--min-cases", "-1"}},
		{"min cases without validate", []string{"eval", evalGoldenDir, "--min-cases", "3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runEvalCommand(t, tc.args...)
			requireEvalExit(t, res, 1)
		})
	}
}

func TestEvalCommandValidateFilterAndMaxCases(t *testing.T) {
	filtered := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json", "--filter", "missing")
	requireEvalExit(t, filtered, 0)
	var validation eval.SetValidation
	if err := json.Unmarshal([]byte(strings.TrimSpace(filtered.out)), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Summary.Total != 1 || validation.Cases[0].Name != "missing-01" {
		t.Fatalf("filtered cases = %+v", validation.Cases)
	}

	capped := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--json", "--max-cases", "1")
	requireEvalExit(t, capped, 0)
	validation = eval.SetValidation{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(capped.out)), &validation); err != nil {
		t.Fatalf("parse eval-set JSON: %v", err)
	}
	if validation.Summary.Total != 1 || validation.Cases[0].Name != "crashloop-01" {
		t.Fatalf("capped cases = %+v", validation.Cases)
	}
}

func TestEvalCommandValidateIgnoresAnalysisFlags(t *testing.T) {
	// Validate mode never analyzes and never touches the network, so --now and
	// model flags have no effect; this keeps CI command lines composable.
	res := runEvalCommand(t, "eval", evalGoldenDir, "--validate", "--now", "not-a-timestamp", "--model", "bananas")
	requireEvalExit(t, res, 0)
}

// evalGoldenBaselineFile saves the current golden eval Result as a baseline file
// and returns its path plus the parsed baseline.
func evalGoldenBaselineFile(t *testing.T) (string, eval.Result) {
	t.Helper()
	res := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z")
	requireEvalExit(t, res, 0)
	var baseline eval.Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.out)), &baseline); err != nil {
		t.Fatalf("parse golden baseline: %v", err)
	}
	return writeEvalCompareBaselineFile(t, baseline), baseline
}

func writeEvalCompareBaselineFile(t *testing.T, baseline eval.Result) string {
	t.Helper()
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	path := filepath.Join(t.TempDir(), "baseline.json")
	writeEvalCLIFile(t, path, string(data))
	return path
}

func parseEvalCompareResult(t *testing.T, out string) eval.CompareResult {
	t.Helper()
	var comparison eval.CompareResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &comparison); err != nil {
		t.Fatalf("parse eval-compare JSON: %v (out=%q)", err, out)
	}
	return comparison
}

func findEvalCompareChange(t *testing.T, comparison eval.CompareResult, name string) eval.CompareChange {
	t.Helper()
	for _, change := range comparison.Changes {
		if change.Name == name {
			return change
		}
	}
	t.Fatalf("change %q not found in %+v", name, comparison.Changes)
	return eval.CompareChange{}
}

func TestEvalCommandCompareSameSet(t *testing.T) {
	baselinePath, _ := evalGoldenBaselineFile(t)
	text := runEvalCommand(t, "eval", evalGoldenDir, "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, text, 0)
	for _, want := range []string{"基线回归对比", "回归 0", "无回归"} {
		if !strings.Contains(text.out, want) {
			t.Fatalf("compare output missing %q: %q", want, text.out)
		}
	}

	jsonRes := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, jsonRes, 0)
	comparison := parseEvalCompareResult(t, jsonRes.out)
	if comparison.Kind != eval.CompareKind || comparison.Version != eval.CompareContractVersion {
		t.Fatalf("kind/version = %q/%q", comparison.Kind, comparison.Version)
	}
	if comparison.Summary != (eval.CompareSummary{Same: 12}) {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
	if len(comparison.Changes) != 12 {
		t.Fatalf("changes = %d, want 12", len(comparison.Changes))
	}
	for _, change := range comparison.Changes {
		if change.Status != eval.StatusSame || len(change.Fields) != 0 {
			t.Fatalf("change = %+v", change)
		}
	}
	if comparison.HasRegressions() {
		t.Fatal("same set must not regress")
	}

	again := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, again, 0)
	if strings.TrimSpace(jsonRes.out) != strings.TrimSpace(again.out) {
		t.Fatal("eval-compare --json output must be deterministic across runs")
	}
}

func TestEvalCommandCompareRegression(t *testing.T) {
	baselinePath, baseline := evalGoldenBaselineFile(t)
	for i := range baseline.Cases {
		if baseline.Cases[i].Name == "missing-01" {
			baseline.Cases[i].Top1 = true
			baseline.Cases[i].Top3 = true
		}
	}
	baselinePath = writeEvalCompareBaselineFile(t, baseline)

	text := runEvalCommand(t, "eval", evalGoldenDir, "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, text, 2)
	for _, want := range []string{"[回归] missing-01", "top1, top3", "回归警告"} {
		if !strings.Contains(text.out, want) {
			t.Fatalf("compare output missing %q: %q", want, text.out)
		}
	}

	jsonRes := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, jsonRes, 2)
	comparison := parseEvalCompareResult(t, jsonRes.out)
	if comparison.Summary.Regressions != 1 || comparison.Summary.Same != 11 || comparison.Summary.Missing != 0 {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
	change := findEvalCompareChange(t, comparison, "missing-01")
	if change.Status != eval.StatusRegressed {
		t.Fatalf("missing-01 status = %q", change.Status)
	}
	if strings.Join(change.Fields, ",") != "top1,top3" {
		t.Fatalf("missing-01 fields = %v", change.Fields)
	}
}

func TestEvalCommandCompareImprovement(t *testing.T) {
	baselinePath, baseline := evalGoldenBaselineFile(t)
	for i := range baseline.Cases {
		if baseline.Cases[i].Name == "oom-01" {
			baseline.Cases[i].Top1 = false
			baseline.Cases[i].Top3 = false
		}
	}
	baselinePath = writeEvalCompareBaselineFile(t, baseline)

	jsonRes := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z", "--compare", baselinePath)
	requireEvalExit(t, jsonRes, 0)
	comparison := parseEvalCompareResult(t, jsonRes.out)
	if comparison.Summary.Improvements != 1 || comparison.Summary.Regressions != 0 {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
	change := findEvalCompareChange(t, comparison, "oom-01")
	if change.Status != eval.StatusImproved {
		t.Fatalf("oom-01 status = %q", change.Status)
	}
	if strings.Join(change.Fields, ",") != "top1,top3" {
		t.Fatalf("oom-01 fields = %v", change.Fields)
	}
}

func TestEvalCommandCompareInputErrors(t *testing.T) {
	baselinePath, _ := evalGoldenBaselineFile(t)
	invalidJSON := filepath.Join(t.TempDir(), "invalid.json")
	writeEvalCLIFile(t, invalidJSON, "{not json")
	wrongKind := filepath.Join(t.TempDir(), "wrong-kind.json")
	writeEvalCLIFile(t, wrongKind, `{"kind":"eval-set","version":"1.0","cases":[],"summary":{}}`)
	wrongVersion := filepath.Join(t.TempDir(), "wrong-version.json")
	writeEvalCLIFile(t, wrongVersion, `{"kind":"eval","version":"2.0","cases":[],"summary":{}}`)
	emptyCases := filepath.Join(t.TempDir(), "empty-cases.json")
	writeEvalCLIFile(t, emptyCases, `{"kind":"eval","version":"1.0","cases":[],"summary":{}}`)

	tests := []struct {
		name string
		args []string
	}{
		{"missing baseline", []string{"eval", evalGoldenDir, "--compare", filepath.Join(t.TempDir(), "missing.json")}},
		{"invalid baseline", []string{"eval", evalGoldenDir, "--compare", invalidJSON}},
		{"wrong kind", []string{"eval", evalGoldenDir, "--compare", wrongKind}},
		{"wrong version", []string{"eval", evalGoldenDir, "--compare", wrongVersion}},
		{"empty baseline cases", []string{"eval", evalGoldenDir, "--compare", emptyCases}},
		{"compare with validate", []string{"eval", evalGoldenDir, "--compare", baselinePath, "--validate"}},
		{"missing cases dir", []string{"eval", filepath.Join(t.TempDir(), "missing-set"), "--compare", baselinePath}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runEvalCommand(t, tc.args...)
			requireEvalExit(t, res, 1)
		})
	}
}

// TestEvalCommandCompareFiltersCurrentSet freezes the interaction rule:
// --filter/--max-cases only shape the current run, so baseline cases outside
// the filtered selection are reported missing and fail the gate.
func TestEvalCommandCompareFiltersCurrentSet(t *testing.T) {
	baselinePath, _ := evalGoldenBaselineFile(t)
	jsonRes := runEvalCommand(t, "eval", evalGoldenDir, "--json", "--now", "2026-10-30T14:00:00Z",
		"--compare", baselinePath, "--filter", "missing")
	requireEvalExit(t, jsonRes, 2)
	comparison := parseEvalCompareResult(t, jsonRes.out)
	if comparison.Summary.Same != 1 || comparison.Summary.Missing != 11 {
		t.Fatalf("summary = %+v", comparison.Summary)
	}
	if !comparison.HasRegressions() {
		t.Fatal("missing baseline cases must fail the gate")
	}
}

// TestEvalCommandCompareStableFailuresStayGreen proves the gate compares
// against the baseline instead of the absolute pass rate: a known stable
// failure that also failed in the baseline is not a regression.
func TestEvalCommandCompareStableFailuresStayGreen(t *testing.T) {
	original := evalRunner
	defer func() { evalRunner = original }()
	canned := eval.Result{
		Kind:    eval.Kind,
		Version: eval.ContractVersion,
		Cases: []eval.CaseResult{{
			Name:          "known-fail",
			Pass:          false,
			Top1:          false,
			Top3:          false,
			MissingOK:     true,
			InjectionSafe: true,
		}},
		Summary: eval.Summary{Total: 1, MissingOK: 1, InjectionSafe: 1},
	}
	evalRunner = func(cfg eval.Config) (*eval.Result, error) {
		return &canned, nil
	}
	baselinePath := writeEvalCompareBaselineFile(t, canned)
	res := runEvalCommand(t, "eval", "some-cases", "--json", "--compare", baselinePath)
	requireEvalExit(t, res, 0)
	comparison := parseEvalCompareResult(t, res.out)
	if comparison.Summary.Same != 1 || comparison.HasRegressions() {
		t.Fatalf("stable failure must stay green: %+v", comparison.Summary)
	}
}
