package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

type runResult struct {
	code int
	out  string
	err  error
}

func run(t *testing.T, args ...string) runResult {
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
	return runResult{code: code, out: stdout.String(), err: err}
}

func requireCode(t *testing.T, res runResult, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit code = %d, want %d (err=%v, out=%q)", res.code, want, res.err, res.out)
	}
}

func scaffoldDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const minimalIncident = `id: minimal-incident
alertname: KubePodCrashLooping
severity: warning
cluster: local
namespace: checkout
workload: checkout-api
observed_at: 2026-10-12T09:14:00Z
collected_at: 2026-10-12T09:20:00Z
source: manual
`

func minimalBundleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "incident.yaml", minimalIncident)
	return dir
}

func requireSchemaValidReport(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := schema.ValidateReport(data); err != nil {
		t.Fatalf("%s failed report schema validation: %v", path, err)
	}
}

func TestContractFlagDefaults(t *testing.T) {
	root := NewRoot("test", "none", "unknown")
	cases := []struct {
		command string
		flag    string
		want    string
	}{
		{"analyze", "offline", "false"},
		{"analyze", "model", "none"},
		{"analyze", "format", "both"},
		{"analyze", "max-evidence", "200"},
		{"analyze", "redact", "strict"},
		{"analyze", "model-timeout", "1m0s"},
		{"analyze", "model-budget-usd", "1"},
		{"analyze", "model-max-rounds", "3"},
		{"validate", "redact", "strict"},
		{"report", "format", "both"},
	}
	for _, tc := range cases {
		cmd, _, err := root.Find([]string{tc.command})
		if err != nil || cmd == nil || cmd.Name() != tc.command {
			t.Fatalf("command %q not registered (err=%v)", tc.command, err)
		}
		flag := cmd.Flags().Lookup(tc.flag)
		if flag == nil {
			t.Fatalf("%s --%s is not registered", tc.command, tc.flag)
		}
		if flag.DefValue != tc.want {
			t.Fatalf("%s --%s default = %q, want %q", tc.command, tc.flag, flag.DefValue, tc.want)
		}
	}
}

func TestAnalyzeOfflineSuccess(t *testing.T) {
	dir := scaffoldDir(t)
	res := run(t, "analyze", dir, "--offline")
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "report:") {
		t.Fatalf("stdout should mention written report: %q", res.out)
	}
	jsonPath := filepath.Join(dir, "report.json")
	mdPath := filepath.Join(dir, "report.md")
	for _, path := range []string{jsonPath, mdPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	requireSchemaValidReport(t, jsonPath)
	rep, err := report.Load(jsonPath)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if rep.Mode != "offline" {
		t.Fatalf("mode = %q, want offline", rep.Mode)
	}
	if rep.Degraded {
		t.Fatal("default analyze --offline must not be degraded")
	}
}

func TestAnalyzeOfflineModelConflict(t *testing.T) {
	dir := scaffoldDir(t)
	res := run(t, "analyze", dir, "--offline", "--model", "ollama")
	requireCode(t, res, 1)
	if res.err == nil || !strings.Contains(res.err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("configuration errors must not write a report")
	}

	// Per ADR-002/ADR-004: --model none and --offline agree semantically.
	res = run(t, "analyze", dir, "--offline", "--model", "none")
	requireCode(t, res, 0)
	res = run(t, "analyze", dir, "--model", "none")
	requireCode(t, res, 0)
}

func TestAnalyzeModelRuntimeFailureExitsThree(t *testing.T) {
	dir := scaffoldDir(t)
	// Valid configuration with an unreachable endpoint: this is a runtime
	// failure, so the offline report is kept and the process exits 3 (ADR-004 D3).
	res := run(t, "analyze", dir,
		"--model", "ollama",
		"--model-url", "http://127.0.0.1:1",
		"--model-name", "llama3",
		"--model-timeout", "500ms")
	requireCode(t, res, 3)

	jsonPath := filepath.Join(dir, "report.json")
	requireSchemaValidReport(t, jsonPath)
	rep, err := report.Load(jsonPath)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if !rep.Degraded {
		t.Fatal("model runtime failure report must be degraded")
	}
	if rep.Mode != "offline" {
		t.Fatalf("fallback mode = %q, want offline", rep.Mode)
	}
	if rep.Versions["model"] != "none" {
		t.Fatalf("fallback versions.model = %q, want none", rep.Versions["model"])
	}
	joined := strings.Join(rep.Limitations, "\n")
	if !strings.Contains(joined, "模型不可用，已回退离线规则") {
		t.Fatalf("limitations missing model fallback note: %v", rep.Limitations)
	}
}

func TestAnalyzeFailOnMissingExitsTwo(t *testing.T) {
	dir := minimalBundleDir(t)
	res := run(t, "analyze", dir, "--offline", "--fail-on-missing")
	requireCode(t, res, 2)

	jsonPath := filepath.Join(dir, "report.json")
	requireSchemaValidReport(t, jsonPath)
	rep, err := report.Load(jsonPath)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if len(rep.Hypotheses) != 0 || len(rep.MissingData) == 0 {
		t.Fatalf("expected empty hypotheses and missing data, got %d/%d", len(rep.Hypotheses), len(rep.MissingData))
	}

	// Without the flag the same bundle is a successful, best-effort report.
	res = run(t, "analyze", dir, "--offline")
	requireCode(t, res, 0)
}

func TestAnalyzeInvalidFormat(t *testing.T) {
	dir := scaffoldDir(t)
	res := run(t, "analyze", dir, "--offline", "--format", "xml")
	requireCode(t, res, 1)
}

func TestAnalyzeFormatAndOut(t *testing.T) {
	// md only: no report.json.
	dir := scaffoldDir(t)
	requireCode(t, run(t, "analyze", dir, "--offline", "--format", "md"), 0)
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
		t.Fatalf("expected report.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("--format md must not write report.json")
	}

	// json only: no report.md.
	dir = scaffoldDir(t)
	requireCode(t, run(t, "analyze", dir, "--offline", "--format", "json"), 0)
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Fatalf("expected report.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err == nil {
		t.Fatal("--format json must not write report.md")
	}

	// Directory --out: both files are written inside it.
	dir = scaffoldDir(t)
	outDir := filepath.Join(t.TempDir(), "nested", "out")
	requireCode(t, run(t, "analyze", dir, "--offline", "--out", outDir), 0)
	for _, name := range []string{"report.json", "report.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected %s in --out directory: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("--out directory must not also write into the bundle")
	}
	requireSchemaValidReport(t, filepath.Join(outDir, "report.json"))

	// File --out with a single format.
	dir = scaffoldDir(t)
	jsonOut := filepath.Join(t.TempDir(), "custom.json")
	requireCode(t, run(t, "analyze", dir, "--offline", "--format", "json", "--out", jsonOut), 0)
	requireSchemaValidReport(t, jsonOut)

	// A file --out is ambiguous with both.
	dir = scaffoldDir(t)
	res := run(t, "analyze", dir, "--offline", "--out", filepath.Join(t.TempDir(), "x.json"))
	requireCode(t, res, 1)

	// Extension must match the selected format.
	dir = scaffoldDir(t)
	res = run(t, "analyze", dir, "--offline", "--format", "json", "--out", filepath.Join(t.TempDir(), "x.md"))
	requireCode(t, res, 1)
}

func TestValidateExitCodes(t *testing.T) {
	dir := scaffoldDir(t)
	res := run(t, "validate", dir)
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "OK:") {
		t.Fatalf("validate output should contain OK: %q", res.out)
	}

	// Sensitive content in strict mode -> exit 4, no secret echoed.
	const secret = "password=supersecret123"
	writeFile(t, dir, "logs/leak.log", secret+"\n")
	res = run(t, "validate", dir)
	requireCode(t, res, 4)
	if res.err == nil || !strings.Contains(res.err.Error(), "credential") {
		t.Fatalf("expected security error, got %v", res.err)
	}
	if strings.Contains(res.err.Error(), "supersecret123") {
		t.Fatal("security error must not echo the secret")
	}

	// Standard mode accepts and will scrub before analysis.
	res = run(t, "validate", dir, "--redact", "standard")
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "warning:") {
		t.Fatalf("standard mode should warn about scrubbing: %q", res.out)
	}

	// Off mode skips the scan.
	requireCode(t, run(t, "validate", dir, "--redact", "off"), 0)

	// Invalid mode is a configuration error.
	requireCode(t, run(t, "validate", dir, "--redact", "banana"), 1)
}

func TestValidateInputErrors(t *testing.T) {
	requireCode(t, run(t, "validate", filepath.Join(t.TempDir(), "missing")), 1)

	dir := t.TempDir()
	writeFile(t, dir, "incident.yaml", "id: broken\n")
	requireCode(t, run(t, "validate", dir), 1)
}

func TestAnalyzeSensitiveStrictExitsFour(t *testing.T) {
	dir := scaffoldDir(t)
	const secret = "password=supersecret123"
	writeFile(t, dir, "logs/leak.log", secret+"\n")
	res := run(t, "analyze", dir, "--offline")
	requireCode(t, res, 4)
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		t.Fatal("security failure must not write a report")
	}
	if strings.Contains(res.out, "supersecret123") {
		t.Fatal("output must not echo the secret")
	}
}

// validReportJSON builds a schema-valid report document independently of the
// report package, so report-command tests exercise the frozen JSON contract.
func validReportJSON(t *testing.T) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version": "1.0",
		"incident": map[string]any{
			"id": "example-oom", "alertname": "KubePodCrashLooping", "severity": "critical",
			"cluster": "local", "namespace": "checkout", "workload": "checkout-api",
			"observed_at": "2026-10-12T09:14:00Z", "collected_at": "2026-10-12T09:20:00Z",
			"source": "alertmanager",
		},
		"generated_at": "2026-10-12T09:25:00Z",
		"mode":         "offline",
		"degraded":     false,
		"summary":      "容器因内存不足被 OOMKilled",
		"evidence": []any{map[string]any{
			"id": "ev-001", "source": "k8s/events.json",
			"observed_at": "2026-10-12T09:14:00Z", "collected_at": "2026-10-12T09:20:00Z",
			"summary": "Pod OOMKilled, exitCode=137", "content_ref": "k8s/events.json#items/0",
			"sha256": "sha256:abc", "stale": false,
		}},
		"hypotheses": []any{map[string]any{
			"rank": 1, "statement": "容器内存 limit 过低导致 OOMKilled", "confidence": "high",
			"evidence_ids": []any{"ev-001"}, "contradictions": []any{},
		}},
		"missing_data":       []any{},
		"recommended_checks": []any{"检查 memory limit"},
		"suggested_fix":      "只读建议：评估 memory limit。",
		"limitations":        []any{},
		"versions":           map[string]any{"cli": "test", "rules": "rule-v0.1", "model": "none"},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal report fixture: %v", err)
	}
	return data
}

func TestReportCommandRerenders(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(jsonPath, validReportJSON(t), 0o644); err != nil {
		t.Fatal(err)
	}
	mdPath := filepath.Join(dir, "report.md")
	if err := os.Remove(mdPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	res := run(t, "report", dir)
	requireCode(t, res, 0)
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("report command should render report.md: %v", err)
	}

	// Format-specific rendering into an explicit file.
	out := filepath.Join(t.TempDir(), "from-json.md")
	requireCode(t, run(t, "report", dir, "--format", "md", "--out", out), 0)
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected --out file: %v", err)
	}
}

func TestReportCommandErrors(t *testing.T) {
	// Missing report.json.
	requireCode(t, run(t, "report", t.TempDir()), 1)

	// Invalid report.json content.
	dir := t.TempDir()
	writeFile(t, dir, "report.json", "{not json")
	requireCode(t, run(t, "report", dir), 1)

	// Schema-invalid report: schema version constant mismatch.
	writeFile(t, dir, "report.json", strings.Replace(string(validReportJSON(t)), "\"1.0\"", "\"9.9\"", 1))
	requireCode(t, run(t, "report", dir), 1)

	// Unknown format.
	dir = t.TempDir()
	writeFile(t, dir, "report.json", string(validReportJSON(t)))
	requireCode(t, run(t, "report", dir, "--format", "yaml"), 1)
}

func TestAnalyzeStandardModeScrubsReport(t *testing.T) {
	dir := scaffoldDir(t)
	const secret = "password=supersecret123"
	writeFile(t, dir, "logs/leak.log", secret+"\n")

	res := run(t, "analyze", dir, "--offline", "--redact", "standard")
	requireCode(t, res, 0)
	jsonPath := filepath.Join(dir, "report.json")
	requireSchemaValidReport(t, jsonPath)
	for _, path := range []string{jsonPath, filepath.Join(dir, "report.md")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "supersecret123") {
			t.Fatalf("%s must not echo scrubbed secrets", path)
		}
	}
}
