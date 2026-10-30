package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
)

const (
	testAlertmanagerPayload = `{
  "version": "4",
  "status": "firing",
  "receiver": "platform-webhook",
  "groupKey": "{}:{alertname=\"KubePodCrashLooping\"}",
  "commonLabels": {"cluster": "local", "namespace": "checkout"},
  "externalURL": "http://alertmanager.example.com",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "KubePodCrashLooping",
        "cluster": "local",
        "namespace": "checkout",
        "pod": "checkout-api-7d9f",
        "severity": "critical"
      },
      "annotations": {
        "summary": "checkout-api is crash looping",
        "description": "container checkout-api restarted 6 times in 10 minutes"
      },
      "startsAt": "2026-10-12T09:04:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://prometheus.example.com/graph",
      "fingerprint": "f2a91c4e"
    }
  ]
}
`

	testPrometheusPayload = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {"metric": {"pod": "checkout-api-7d9f"}, "values": [[1793000000, "498"], [1793000060, "512"]]}
    ]
  }
}
`

	testKubectlEventsPayload = `{
  "apiVersion": "v1",
  "kind": "EventList",
  "items": [
    {
      "type": "Warning",
      "reason": "OOMKilled",
      "message": "Container checkout-api was OOMKilled (exitCode=137)",
      "involvedObject": {"kind": "Pod", "namespace": "checkout", "name": "checkout-api-7d9f"},
      "count": 1,
      "firstTimestamp": "2026-10-12T09:13:50Z",
      "lastTimestamp": "2026-10-12T09:13:50Z"
    }
  ]
}
`

	testLokiPayload = `{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {"stream": {"app": "checkout-api"}, "values": [["1793000010000000000", "INFO starting checkout-api"]]}
    ]
  }
}
`
)

func TestImportAlertmanagerFromFile(t *testing.T) {
	dir := scaffoldImportBundle(t)
	input := filepath.Join(t.TempDir(), "webhook.json")
	if err := os.WriteFile(input, []byte(testAlertmanagerPayload), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	out, err := runImportCommand(t, "", "alertmanager", dir, "--from", input)
	if err != nil {
		t.Fatalf("import alertmanager: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"imported alertmanager", "wrote alert.json", "next: kubectl-triage validate"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout %q does not contain %q", out, want)
		}
	}
	alert := readImportBundleFile(t, dir, "alert.json")
	for _, want := range []string{`"alerts"`, `"KubePodCrashLooping"`, `"firing"`} {
		if !strings.Contains(alert, want) {
			t.Fatalf("alert.json missing %q:\n%s", want, alert)
		}
	}
	requireImportBundleValidates(t, dir)

	// A second import must not silently overwrite the existing file.
	out, err = runImportCommand(t, "", "alertmanager", dir, "--from", input)
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("re-import exit code = %d, want 1 (out=%q)", got, out)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error %q should mention --force", err)
	}
	if got := readImportBundleFile(t, dir, "alert.json"); got != alert {
		t.Fatal("failed import must not modify the existing file")
	}

	// --force overwrites the existing file.
	if _, err := runImportCommand(t, "", "alertmanager", dir, "--from", input, "--force"); err != nil {
		t.Fatalf("import --force: %v", err)
	}
}

func TestImportPrometheusFromStdinAndName(t *testing.T) {
	dir := scaffoldImportBundle(t)

	out, err := runImportCommand(t, testPrometheusPayload, "prometheus", dir)
	if err != nil {
		t.Fatalf("import prometheus (stdin): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "metrics/query.json") {
		t.Fatalf("stdout should list metrics/query.json: %q", out)
	}
	metrics := readImportBundleFile(t, dir, "metrics/query.json")
	for _, want := range []string{`"resultType": "matrix"`, `"values": [[1793000000, "498"]`} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics/query.json missing %q:\n%s", want, metrics)
		}
	}

	// --name selects the output file name. bundle.Scaffold already ships
	// metrics/memory.json, so the first attempt is a conflict and --force wins.
	_, err = runImportCommand(t, testPrometheusPayload, "prometheus", dir, "--name", "memory")
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("conflicting --name exit code = %d, want 1", got)
	}
	if _, err := runImportCommand(t, testPrometheusPayload, "prometheus", dir, "--name", "memory", "--force"); err != nil {
		t.Fatalf("import --name memory --force: %v", err)
	}
	if got := readImportBundleFile(t, dir, "metrics/memory.json"); !strings.Contains(got, `"resultType": "matrix"`) {
		t.Fatalf("metrics/memory.json looks wrong:\n%s", got)
	}

	// Path traversal in --name is an input error, not a write outside the bundle.
	_, err = runImportCommand(t, testPrometheusPayload, "prometheus", dir, "--name", "../escape")
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("invalid --name exit code = %d, want 1", got)
	}
	requireImportBundleValidates(t, dir)
}

func TestImportKubectlEvents(t *testing.T) {
	dir := scaffoldImportBundle(t)
	// bundle.Scaffold ships a placeholder k8s/events.json, so importing events
	// requires --force; without it the command must refuse and exit 1.
	if _, err := runImportCommand(t, testKubectlEventsPayload, "kubectl", dir); importErrorCode(t, err) != 1 {
		t.Fatalf("import kubectl without --force should exit 1, got %v", err)
	}
	if _, err := runImportCommand(t, testKubectlEventsPayload, "kubectl", dir, "--force"); err != nil {
		t.Fatalf("import kubectl --force: %v", err)
	}
	events := readImportBundleFile(t, dir, "k8s/events.json")
	for _, want := range []string{`"kind": "EventList"`, `"reason": "OOMKilled"`, `"involvedObject"`} {
		if !strings.Contains(events, want) {
			t.Fatalf("k8s/events.json missing %q:\n%s", want, events)
		}
	}
	requireImportBundleValidates(t, dir)
}

func TestImportLokiFromStdin(t *testing.T) {
	dir := scaffoldImportBundle(t)
	if _, err := runImportCommand(t, testLokiPayload, "loki", dir); err != nil {
		t.Fatalf("import loki: %v", err)
	}
	logs := readImportBundleFile(t, dir, "logs/loki.log")
	if !strings.Contains(logs, `{app="checkout-api"} INFO starting checkout-api`) {
		t.Fatalf("logs/loki.log looks wrong:\n%s", logs)
	}
	requireImportBundleValidates(t, dir)
}

func TestImportUnknownSource(t *testing.T) {
	dir := scaffoldImportBundle(t)
	_, err := runImportCommand(t, "", "cloudwatch", dir)
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("unknown source exit code = %d, want 1", got)
	}
	for _, want := range []string{"unknown import source", "alertmanager", "prometheus"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err, want)
		}
	}
}

func TestImportRequiresIncidentYAML(t *testing.T) {
	dir := t.TempDir()
	_, err := runImportCommand(t, testAlertmanagerPayload, "alertmanager", dir)
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("missing incident.yaml exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "incident.yaml") {
		t.Fatalf("error %q should mention incident.yaml", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "alert.json")); statErr == nil {
		t.Fatal("import into a non-bundle directory must not write files")
	}
}

func TestImportMissingFromFile(t *testing.T) {
	dir := scaffoldImportBundle(t)
	_, err := runImportCommand(t, "", "alertmanager", dir, "--from", filepath.Join(t.TempDir(), "missing.json"))
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("missing --from file exit code = %d, want 1", got)
	}
}

func TestImportInvalidPayload(t *testing.T) {
	dir := scaffoldImportBundle(t)
	_, err := runImportCommand(t, "not json", "alertmanager", dir)
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("invalid payload exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "alertmanager") {
		t.Fatalf("error %q should name the source", err)
	}
}

func TestImportEmptyStdin(t *testing.T) {
	dir := scaffoldImportBundle(t)
	_, err := runImportCommand(t, "", "prometheus", dir)
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("empty stdin exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("error %q should mention stdin", err)
	}
}

func TestImportNameNotSupported(t *testing.T) {
	dir := scaffoldImportBundle(t)
	_, err := runImportCommand(t, testAlertmanagerPayload, "alertmanager", dir, "--name", "alerts")
	if got := importErrorCode(t, err); got != 1 {
		t.Fatalf("--name on alertmanager exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Fatalf("error %q should mention --name", err)
	}
}

const (
	// Synthetic credential-like values used only to prove the import-time scan
	// rejects them; they are not real secrets.
	testSensitivePassword = "password=supersecret123"
	testSensitiveJWT      = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
)

// alertmanagerInput builds a webhook payload whose annotation carries value.
func alertmanagerInput(value string) string {
	return `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"KubePodCrashLooping","namespace":"checkout"},"annotations":{"description":"` + value + `"},"startsAt":"2026-10-12T09:04:00Z"}]}`
}

// kubectlEventsInput builds an EventList payload whose message carries value.
func kubectlEventsInput(value string) string {
	return `{"apiVersion":"v1","kind":"EventList","items":[{"type":"Warning","reason":"BackOff","message":"` + value + `","involvedObject":{"kind":"Pod","namespace":"checkout","name":"checkout-api-7d9f"}}]}`
}

// TestImportRejectsSensitiveInput covers both input paths and both pattern
// families: credential assignments and JWTs, across alertmanager and kubectl
// payloads. A rejected import must leave the bundle byte-for-byte untouched.
func TestImportRejectsSensitiveInput(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		payload  string
		secret   string
		fromFile bool
	}{
		{"stdin alertmanager password", "alertmanager", alertmanagerInput(testSensitivePassword), testSensitivePassword, false},
		{"file alertmanager jwt", "alertmanager", alertmanagerInput(testSensitiveJWT), testSensitiveJWT, true},
		{"stdin kubectl password", "kubectl", kubectlEventsInput(testSensitivePassword), testSensitivePassword, false},
		{"file kubectl jwt", "kubectl", kubectlEventsInput(testSensitiveJWT), testSensitiveJWT, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := scaffoldImportBundle(t)
			// Snapshot every file before the attempt so the assertion covers both
			// "no new file" and "no existing file overwritten" (kubectl cases pass
			// --force, which would otherwise allow the overwrite).
			before := snapshotImportFiles(t, dir)
			args := []string{tc.source, dir, "--force"}
			stdin := ""
			if tc.fromFile {
				input := filepath.Join(t.TempDir(), "export.json")
				if err := os.WriteFile(input, []byte(tc.payload), 0o644); err != nil {
					t.Fatalf("write input: %v", err)
				}
				args = append(args, "--from", input)
			} else {
				stdin = tc.payload
			}
			out, err := runImportCommand(t, stdin, args...)
			if got := importErrorCode(t, err); got != 4 {
				t.Fatalf("sensitive import exit code = %d, want 4 (err=%v, out=%q)", got, err, out)
			}
			for _, needle := range []string{"credentials", "secrets", "redact"} {
				if !strings.Contains(err.Error(), needle) {
					t.Fatalf("error %q should mention %q", err, needle)
				}
			}
			if strings.Contains(err.Error(), tc.secret) || strings.Contains(out, tc.secret) {
				t.Fatal("security error must never echo the matched value")
			}
			after := snapshotImportFiles(t, dir)
			if !sameImportFiles(before, after) {
				t.Fatalf("sensitive import must not write or overwrite files:\nbefore=%v\nafter=%v", importFileNames(before), importFileNames(after))
			}
			for rel := range after {
				if strings.HasPrefix(filepath.Base(rel), ".import-") {
					t.Fatalf("temporary import file left behind: %s", rel)
				}
			}
		})
	}
}

// TestImportAllowsBenignSecretMention guards against over-blocking: the scanner
// only rejects credential-like values, not the word "secret" in normal notes.
func TestImportAllowsBenignSecretMention(t *testing.T) {
	dir := scaffoldImportBundle(t)
	payload := alertmanagerInput("the secret rotation job failed after the deploy")
	if _, err := runImportCommand(t, payload, "alertmanager", dir); err != nil {
		t.Fatalf("benign mention of secret should import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "alert.json")); err != nil {
		t.Fatalf("expected alert.json after import: %v", err)
	}
}

// snapshotImportFiles records every bundle file content for no-write checks.
func snapshotImportFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return files
}

func sameImportFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, content := range a {
		if b[name] != content {
			return false
		}
	}
	return true
}

func importFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scaffoldImportBundle creates a minimal valid bundle with bundle.Scaffold.
func scaffoldImportBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return dir
}

// runImportCommand executes only the import subcommand with an injected stdin.
func runImportCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newImportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func importErrorCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *ExitError, got %T: %v", err, err)
	}
	return exit.Code
}

func readImportBundleFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// requireImportBundleValidates is the integration acceptance for import: the
// written bundle must pass the same validate scan analyze uses.
func requireImportBundleValidates(t *testing.T, dir string) {
	t.Helper()
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("reload imported bundle: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("imported bundle must validate: %v", err)
	}
}
