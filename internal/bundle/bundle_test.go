package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/redact"
)

func scaffoldBundle(t *testing.T) *Bundle {
	t.Helper()
	dir := t.TempDir()
	if err := Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return b
}

func writeBundleFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validIncident = `id: example-oom
alertname: KubePodCrashLooping
severity: critical
cluster: local
namespace: checkout
workload: checkout-api
observed_at: 2026-10-12T09:14:00Z
collected_at: 2026-10-12T09:20:00Z
source: alertmanager
`

func TestScaffoldLoadValidate(t *testing.T) {
	b := scaffoldBundle(t)
	if err := b.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if b.RedactMode != redact.ModeStrict {
		t.Fatalf("default redact mode = %q, want strict", b.RedactMode)
	}
	if b.Incident.ID != "example-oom" || b.Incident.Namespace != "checkout" {
		t.Fatalf("unexpected incident: %+v", b.Incident)
	}
	files, err := b.WalkTextFiles()
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(files) < 3 {
		t.Fatalf("expected evidence files, got %v", files)
	}
	for _, file := range files {
		if file == IncidentFile || strings.HasPrefix(filepath.Base(file), "report.") {
			t.Fatalf("WalkTextFiles must exclude metadata/report files, got %q", file)
		}
	}
}

func TestLoadRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	writeBundleFile(t, dir, IncidentFile, "id: x\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected load error for incomplete incident")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "alertname") {
		t.Fatalf("error should mention alertname: %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeBundleFile(t, dir, IncidentFile, validIncident+"owner: alice\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected load error for unknown field")
	}
}

func TestLoadRejectsBadTimestamp(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(validIncident, "observed_at: 2026-10-12T09:14:00Z", "observed_at: yesterday", 1)
	writeBundleFile(t, dir, IncidentFile, bad)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected load error for bad timestamp")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	for _, want := range []string{"observed_at", "RFC3339"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err, want)
		}
	}
}

func TestLoadMissingIncidentFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("expected load error for missing incident.yaml")
	}
}

func TestLoadEmptyIncidentFile(t *testing.T) {
	dir := t.TempDir()
	writeBundleFile(t, dir, IncidentFile, "")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected load error for empty incident.yaml")
	}
}

func TestValidateRejectsTimestampOrder(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(validIncident, "collected_at: 2026-10-12T09:20:00Z", "collected_at: 2026-10-12T09:10:00Z", 1)
	writeBundleFile(t, dir, IncidentFile, bad)
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = b.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "must not be before") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsSensitiveContentStrict(t *testing.T) {
	b := scaffoldBundle(t)
	const secret = "password=supersecret123"
	writeBundleFile(t, b.Dir, "logs/leak.log", secret+"\n")

	err := b.Validate()
	if err == nil {
		t.Fatal("expected security error")
	}
	var se *SecurityError
	if !errors.As(err, &se) {
		t.Fatalf("expected *SecurityError, got %T: %v", err, err)
	}
	if len(se.Findings) != 1 {
		t.Fatalf("findings = %v, want 1", se.Findings)
	}
	if se.Findings[0].File != "logs/leak.log" || se.Findings[0].Line != 1 {
		t.Fatalf("unexpected finding: %+v", se.Findings[0])
	}
	if strings.Contains(err.Error(), "supersecret123") {
		t.Fatal("error must not echo the secret value")
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Fatalf("error should describe the finding kind: %v", err)
	}
	if len(b.Findings()) != 1 {
		t.Fatalf("cached findings = %v", b.Findings())
	}
}

func TestValidateStandardModeScrubsReads(t *testing.T) {
	b := scaffoldBundle(t)
	const secret = "password=supersecret123"
	writeBundleFile(t, b.Dir, "logs/leak.log", secret+"\n")
	b.RedactMode = redact.ModeStandard

	if err := b.Validate(); err != nil {
		t.Fatalf("standard mode should accept the bundle: %v", err)
	}
	if len(b.Findings()) != 1 {
		t.Fatalf("expected findings to be cached, got %v", b.Findings())
	}
	data, err := b.ReadFile("logs/leak.log", 4096)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "supersecret123") {
		t.Fatalf("standard mode read leaked secret: %q", data)
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("standard mode read should be scrubbed: %q", data)
	}
}

func TestValidateOffModeSkipsScan(t *testing.T) {
	b := scaffoldBundle(t)
	const secret = "password=supersecret123"
	writeBundleFile(t, b.Dir, "logs/leak.log", secret+"\n")
	b.RedactMode = redact.ModeOff

	if err := b.Validate(); err != nil {
		t.Fatalf("off mode should not scan: %v", err)
	}
	if len(b.Findings()) != 0 {
		t.Fatalf("off mode findings = %v, want none", b.Findings())
	}
	data, err := b.ReadFile("logs/leak.log", 4096)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "supersecret123") {
		t.Fatalf("off mode read should not be scrubbed: %q", data)
	}
}

func TestReadFileRejectsPathTraversal(t *testing.T) {
	b := scaffoldBundle(t)
	outside := filepath.Join(filepath.Dir(b.Dir), "bundle-outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	for _, rel := range []string{"../bundle-outside.txt", "/etc/passwd", "k8s/../../bundle-outside.txt", ""} {
		if _, err := b.ReadFile(rel, 4096); err == nil {
			t.Fatalf("ReadFile(%q) should be rejected", rel)
		}
		if b.HasFile(rel) {
			t.Fatalf("HasFile(%q) should be false", rel)
		}
	}
	if !b.HasFile("k8s/events.json") {
		t.Fatal("HasFile should accept bundle-relative files")
	}
	if _, err := b.ReadFile("incident.yaml", 4096); err != nil {
		t.Fatalf("ReadFile within bundle failed: %v", err)
	}
}

func TestReadFileSizeCap(t *testing.T) {
	b := scaffoldBundle(t)
	data, err := b.ReadFile("logs/app.log", 5)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) > 5 {
		t.Fatalf("read %d bytes, want at most 5", len(data))
	}
	if _, err := b.ReadFile("logs/missing.log", 0); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestScaffoldRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	if err := Scaffold(dir); err == nil {
		t.Fatal("expected second scaffold to fail")
	}
}

func TestScanSensitiveWrapper(t *testing.T) {
	dir := t.TempDir()
	writeBundleFile(t, dir, "logs/leak.log", "password=supersecret123\n")
	problems, err := ScanSensitive(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "possible credential/secret") {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if strings.Contains(strings.Join(problems, "\n"), "supersecret123") {
		t.Fatal("problems must not echo the secret value")
	}
}
