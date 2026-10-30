package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/internal/telemetry"
)

// isolateTelemetryEnv points the telemetry config at a fresh directory and
// clears every telemetry environment variable.
func isolateTelemetryEnv(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kubectl-triage-telemetry")
	t.Setenv(telemetry.ConfigDirEnv, dir)
	t.Setenv(telemetry.EndpointEnv, "")
	t.Setenv(telemetry.TelemetryEnv, "")
	return dir
}

func telemetryConfigPath(dir string) string {
	return filepath.Join(dir, "config.yaml")
}

type splitRunResult struct {
	code   int
	stdout string
	stderr string
	err    error
}

// runSplit executes the CLI while keeping stdout and stderr separate so tests
// can assert that telemetry never writes to stdout and never pollutes stderr.
func runSplit(t *testing.T, args ...string) splitRunResult {
	t.Helper()
	root := NewRoot("test", "test-commit", "test-date")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
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
	return splitRunResult{code: code, stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func loadCLITelemetryConfig(t *testing.T) telemetry.Config {
	t.Helper()
	cfg, exists, err := telemetry.Load()
	if err != nil {
		t.Fatalf("load telemetry config: %v", err)
	}
	if !exists {
		t.Fatal("expected a telemetry config file")
	}
	return cfg
}

func requireTelemetryUUIDv4(t *testing.T, id string) {
	t.Helper()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || id[14] != '4' {
		t.Fatalf("install_id %q does not look like a UUID v4", id)
	}
}

func newTelemetryCountingServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func newTelemetryRecordingServer(t *testing.T, status int) (*httptest.Server, <-chan []byte) {
	t.Helper()
	bodies := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		bodies <- buf.Bytes()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, bodies
}

// telemetrySensitiveIncident carries distinctive identifiers that must never
// appear in a telemetry payload.
const telemetrySensitiveIncident = "id: telemetry-sensitive-incident\n" +
	"alertname: SensitiveAlertName\n" +
	"severity: warning\n" +
	"cluster: prod-sensitive-cluster\n" +
	"namespace: secret-namespace\n" +
	"workload: checkout-api-sensitive\n" +
	"observed_at: 2026-10-12T09:14:00Z\n" +
	"collected_at: 2026-10-12T09:20:00Z\n" +
	"source: manual\n"

var telemetryEventFields = []string{
	"install_id", "event", "version", "os", "arch",
	"mode", "fault_class", "duration_ms", "evidence_count", "outcome",
}

func TestTelemetryStatusDefaultsToDisabled(t *testing.T) {
	dir := isolateTelemetryEnv(t)
	res := run(t, "telemetry", "status")
	requireCode(t, res, 0)
	for _, want := range []string{
		"匿名遥测：已关闭",
		"端点：未配置（事件将被丢弃）",
		"配置文件：" + telemetryConfigPath(dir),
		"安装 ID：未生成",
	} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("status output missing %q: %q", want, res.out)
		}
	}
	if _, err := os.Stat(telemetryConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("status must not create a config file: %v", err)
	}
}

func TestTelemetryEnableStatusDisable(t *testing.T) {
	dir := isolateTelemetryEnv(t)

	res := run(t, "telemetry", "enable")
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "已启用匿名遥测") {
		t.Fatalf("enable output = %q", res.out)
	}
	cfg := loadCLITelemetryConfig(t)
	if !cfg.Enabled {
		t.Fatal("enable must persist enabled=true")
	}
	requireTelemetryUUIDv4(t, cfg.InstallID)
	id := cfg.InstallID
	if !strings.Contains(res.out, id[:8]) {
		t.Fatalf("enable output should show the install ID prefix %q: %q", id[:8], res.out)
	}
	if strings.Contains(res.out, id) {
		t.Fatalf("enable output must not reveal the full install ID: %q", res.out)
	}
	if _, err := os.Stat(telemetryConfigPath(dir)); err != nil {
		t.Fatalf("enable must write the config file: %v", err)
	}

	res = run(t, "telemetry", "status")
	requireCode(t, res, 0)
	for _, want := range []string{"匿名遥测：已开启", "安装 ID：" + id[:8], telemetryConfigPath(dir)} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("status output missing %q: %q", want, res.out)
		}
	}
	if strings.Contains(res.out, id) {
		t.Fatalf("status output must not reveal the full install ID: %q", res.out)
	}

	res = run(t, "telemetry", "disable")
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "已关闭匿名遥测") {
		t.Fatalf("disable output = %q", res.out)
	}
	cfg = loadCLITelemetryConfig(t)
	if cfg.Enabled {
		t.Fatal("disable must persist enabled=false")
	}
	if cfg.InstallID != id {
		t.Fatalf("disable must keep install_id %q, got %q", id, cfg.InstallID)
	}

	requireCode(t, run(t, "telemetry", "enable"), 0)
	if again := loadCLITelemetryConfig(t); again.InstallID != id {
		t.Fatalf("install_id changed on re-enable: %q -> %q", id, again.InstallID)
	}
}

func TestTelemetryCommandErrors(t *testing.T) {
	t.Run("corrupt config", func(t *testing.T) {
		dir := isolateTelemetryEnv(t)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(telemetryConfigPath(dir), []byte("enabled: [1, 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"telemetry", "status"},
			{"telemetry", "enable"},
			{"telemetry", "disable"},
		} {
			res := run(t, args...)
			requireCode(t, res, 1)
			if res.err == nil || !strings.Contains(res.err.Error(), "遥测配置") {
				t.Fatalf("%v: error should mention the telemetry config: %v", args, res.err)
			}
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		isolateTelemetryEnv(t)
		requireCode(t, run(t, "telemetry", "banana"), 1)
	})

	t.Run("unexpected arguments", func(t *testing.T) {
		isolateTelemetryEnv(t)
		requireCode(t, run(t, "telemetry", "status", "extra"), 1)
	})

	t.Run("unresolvable config directory", func(t *testing.T) {
		t.Setenv(telemetry.ConfigDirEnv, "")
		t.Setenv(telemetry.XDGConfigHomeEnv, "")
		// os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows; clear
		// both so the config directory is truly unresolvable on every CI target.
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		requireCode(t, run(t, "telemetry", "status"), 1)
	})
}

func TestTelemetryParentCommandShowsHelp(t *testing.T) {
	isolateTelemetryEnv(t)
	res := run(t, "telemetry")
	requireCode(t, res, 0)
	for _, want := range []string{"status", "enable", "disable"} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("telemetry help missing %q: %q", want, res.out)
		}
	}
}

func TestAnalyzeTelemetryDefaultOffSendsNothing(t *testing.T) {
	dir := isolateTelemetryEnv(t)
	srv, count := newTelemetryCountingServer(t, http.StatusNoContent)
	t.Setenv(telemetry.EndpointEnv, srv.URL)

	bundleDir := scaffoldDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v)", res.code, res.err)
	}
	if count.Load() != 0 {
		t.Fatalf("telemetry is disabled but %d request(s) were sent", count.Load())
	}
	if !strings.Contains(res.stdout, "report:") {
		t.Fatalf("stdout should still mention the report: %q", res.stdout)
	}
	if strings.Contains(res.stdout, telemetry.FirstRunHint) {
		t.Fatalf("first-run hint must go to stderr, not stdout: %q", res.stdout)
	}
	if !strings.Contains(res.stderr, telemetry.FirstRunHint) {
		t.Fatalf("stderr should carry the one-line opt-in hint: %q", res.stderr)
	}
	if _, err := os.Stat(telemetryConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("analyze must not write a telemetry config: %v", err)
	}
}

func TestAnalyzeTelemetryEnabledPostsOneWhitelistedEvent(t *testing.T) {
	isolateTelemetryEnv(t)
	requireCode(t, run(t, "telemetry", "enable"), 0)
	cfg := loadCLITelemetryConfig(t)
	requireTelemetryUUIDv4(t, cfg.InstallID)

	srv, bodies := newTelemetryRecordingServer(t, http.StatusNoContent)
	t.Setenv(telemetry.EndpointEnv, srv.URL)

	bundleDir := t.TempDir()
	writeFile(t, bundleDir, "incident.yaml", telemetrySensitiveIncident)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v, stderr=%q)", res.code, res.err, res.stderr)
	}
	if strings.Contains(res.stderr, telemetry.FirstRunHint) {
		t.Fatalf("configured telemetry must not print the first-run hint: %q", res.stderr)
	}

	var body []byte
	select {
	case body = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("enabled telemetry sent no event")
	}
	select {
	case extra := <-bodies:
		t.Fatalf("expected exactly one event, got another: %s", extra)
	default:
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("event is not JSON: %v (body=%s)", err, body)
	}
	if len(payload) != len(telemetryEventFields) {
		t.Fatalf("payload has %d fields, want %d: %s", len(payload), len(telemetryEventFields), body)
	}
	for _, name := range telemetryEventFields {
		if _, ok := payload[name]; !ok {
			t.Fatalf("payload missing field %q: %s", name, body)
		}
	}

	var got struct {
		InstallID     string `json:"install_id"`
		Event         string `json:"event"`
		Version       string `json:"version"`
		OS            string `json:"os"`
		Arch          string `json:"arch"`
		Mode          string `json:"mode"`
		FaultClass    string `json:"fault_class"`
		DurationMS    int64  `json:"duration_ms"`
		EvidenceCount int    `json:"evidence_count"`
		Outcome       string `json:"outcome"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	rep, err := report.Load(filepath.Join(bundleDir, "report.json"))
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	wantOutcome := "ok"
	if len(rep.Hypotheses) == 0 {
		wantOutcome = "missing"
	}
	if got.InstallID != cfg.InstallID {
		t.Fatalf("install_id = %q, want %q", got.InstallID, cfg.InstallID)
	}
	if got.Event != "analyze" {
		t.Fatalf("event = %q, want analyze", got.Event)
	}
	if got.Version != "test" {
		t.Fatalf("version = %q, want test", got.Version)
	}
	if got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Fatalf("os/arch = %q/%q, want %q/%q", got.OS, got.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if got.Mode != rep.Mode {
		t.Fatalf("mode = %q, want %q", got.Mode, rep.Mode)
	}
	if got.FaultClass != "" {
		t.Fatalf("fault_class = %q, want empty", got.FaultClass)
	}
	if got.DurationMS < 0 {
		t.Fatalf("duration_ms = %d, want >= 0", got.DurationMS)
	}
	if got.EvidenceCount != len(rep.Evidence) {
		t.Fatalf("evidence_count = %d, want %d", got.EvidenceCount, len(rep.Evidence))
	}
	if got.Outcome != wantOutcome {
		t.Fatalf("outcome = %q, want %q", got.Outcome, wantOutcome)
	}

	// The sensitive incident identifiers are never collected.
	for _, forbidden := range []string{
		"prod-sensitive-cluster", "secret-namespace",
		"SensitiveAlertName", "checkout-api-sensitive",
	} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("payload must not contain incident identifier %q: %s", forbidden, body)
		}
	}
}

func TestAnalyzeTelemetryEnvironmentOffOverridesEnabled(t *testing.T) {
	isolateTelemetryEnv(t)
	requireCode(t, run(t, "telemetry", "enable"), 0)
	srv, count := newTelemetryCountingServer(t, http.StatusNoContent)
	t.Setenv(telemetry.EndpointEnv, srv.URL)
	t.Setenv(telemetry.TelemetryEnv, "off")

	bundleDir := scaffoldDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v)", res.code, res.err)
	}
	if count.Load() != 0 {
		t.Fatalf("KUBECTL_TRIAGE_TELEMETRY=off still sent %d request(s)", count.Load())
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "report.json")); err != nil {
		t.Fatalf("report must still be written: %v", err)
	}
}

func TestAnalyzeTelemetryServerFailureIsSilentAndKeepsExitCode(t *testing.T) {
	isolateTelemetryEnv(t)
	requireCode(t, run(t, "telemetry", "enable"), 0)
	srv, count := newTelemetryCountingServer(t, http.StatusInternalServerError)
	t.Setenv(telemetry.EndpointEnv, srv.URL)

	// --fail-on-missing on an empty bundle exits 2; telemetry must not change it.
	bundleDir := minimalBundleDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline", "--fail-on-missing")
	if res.code != 2 {
		t.Fatalf("analyze exit code = %d, want 2 (err=%v)", res.code, res.err)
	}
	if count.Load() != 1 {
		t.Fatalf("request count = %d, want 1", count.Load())
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "report.json")); err != nil {
		t.Fatalf("report must still be written: %v", err)
	}
	if res.stderr != "" {
		t.Fatalf("a failing telemetry endpoint must be silent, stderr = %q", res.stderr)
	}
}

func TestAnalyzeTelemetryCorruptConfigDoesNotAffectAnalyze(t *testing.T) {
	dir := isolateTelemetryEnv(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(telemetryConfigPath(dir), []byte("enabled: [1, 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundleDir := scaffoldDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v)", res.code, res.err)
	}
	if !strings.Contains(res.stdout, "report:") {
		t.Fatalf("stdout should mention the report: %q", res.stdout)
	}
	if res.stderr != "" {
		t.Fatalf("corrupt telemetry config must be silent in analyze, stderr = %q", res.stderr)
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "report.json")); err != nil {
		t.Fatalf("report must still be written: %v", err)
	}
}

func TestAnalyzeTelemetryWithoutEndpointKeepsConfigUntouched(t *testing.T) {
	dir := isolateTelemetryEnv(t)
	requireCode(t, run(t, "telemetry", "enable"), 0)
	path := telemetryConfigPath(dir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	bundleDir := scaffoldDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v)", res.code, res.err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after analyze: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("analyze must not rewrite the telemetry config\nbefore: %s\nafter: %s", before, after)
	}
	if res.stderr != "" {
		t.Fatalf("configured telemetry must stay silent on stderr, got %q", res.stderr)
	}
}

func TestAnalyzeTelemetryHintSuppressedAfterDisable(t *testing.T) {
	isolateTelemetryEnv(t)
	requireCode(t, run(t, "telemetry", "disable"), 0)
	bundleDir := scaffoldDir(t)
	res := runSplit(t, "analyze", bundleDir, "--offline")
	if res.code != 0 {
		t.Fatalf("analyze exit code = %d, want 0 (err=%v)", res.code, res.err)
	}
	if strings.Contains(res.stderr, telemetry.FirstRunHint) {
		t.Fatalf("explicit opt-out must suppress the first-run hint: %q", res.stderr)
	}
}
