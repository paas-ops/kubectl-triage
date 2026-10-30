package redact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	credentialSample = "password=supersecret123"
	jwtSample        = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	awsSample        = "AKIAIOSFODNN7EXAMPLE"
	githubSample     = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	privateKeySample = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\n-----END RSA PRIVATE KEY-----"
)

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

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"strict":   ModeStrict,
		"standard": ModeStandard,
		"off":      ModeOff,
		" Strict ": ModeStrict,
	}
	for value, want := range cases {
		got, err := ParseMode(value)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("ParseMode(%q) = %q, want %q", value, got, want)
		}
	}
	for _, value := range []string{"", "banana", "strictest"} {
		if _, err := ParseMode(value); err == nil {
			t.Fatalf("ParseMode(%q) should fail", value)
		}
	}
}

func TestModeSemantics(t *testing.T) {
	if !ModeStrict.Scans() || !ModeStrict.Scans() || ModeOff.Scans() {
		t.Fatal("unexpected Scans semantics")
	}
	if !ModeStandard.Scrubs() || ModeStrict.Scrubs() || ModeOff.Scrubs() {
		t.Fatal("unexpected Scrubs semantics")
	}
	if ModeStandard.String() != "standard" {
		t.Fatalf("String = %q", ModeStandard.String())
	}
}

func TestScanFindingsNeverContainSecret(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "logs/app.log", "starting up\n"+credentialSample+"\n")
	writeFile(t, dir, "k8s/events.json", "{\"reason\": \"OOMKilled\"}\n"+jwtSample+"\n")

	findings, err := Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d (%v), want 2", len(findings), findings)
	}
	if findings[0].File != "k8s/events.json" || findings[0].Line != 2 || findings[0].Kind != "JSON Web Token" {
		t.Fatalf("unexpected first finding: %+v", findings[0])
	}
	if findings[1].File != "logs/app.log" || findings[1].Line != 2 || findings[1].Kind != "credential assignment" {
		t.Fatalf("unexpected second finding: %+v", findings[1])
	}
	for _, finding := range findings {
		text := finding.String()
		for _, secret := range []string{"supersecret123", jwtSample} {
			if strings.Contains(text, secret) {
				t.Fatalf("finding %q leaks secret content", text)
			}
		}
	}
}

func TestScanDetectsAllPatternKinds(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"api_key: abcdefghijklmnop",
		"token=" + jwtSample,
		"aws " + awsSample,
		"github " + githubSample,
		privateKeySample,
		"",
	}, "\n")
	writeFile(t, dir, "notes.txt", content)

	findings, err := Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	got := map[string]int{}
	for _, finding := range findings {
		got[finding.Kind]++
	}
	for _, kind := range []string{"credential assignment", "JSON Web Token", "cloud access key", "GitHub token", "private key"} {
		if got[kind] == 0 {
			t.Fatalf("missing finding kind %q in %v", kind, findings)
		}
	}
}

func TestScanSkipsGeneratedReportsAndDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "report.md", credentialSample+"\n")
	writeFile(t, dir, "report.json", "{\"x\": \""+awsSample+"\"}\n")
	writeFile(t, dir, ".git/config", credentialSample+"\n")
	writeFile(t, dir, "node_modules/pkg/secret.txt", credentialSample+"\n")
	writeFile(t, dir, "evidence/leak.yaml", credentialSample+"\n")

	findings, err := Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want only evidence/leak.yaml", findings)
	}
	if findings[0].File != "evidence/leak.yaml" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}

func TestScrubRemovesSecrets(t *testing.T) {
	input := "password=supersecret123\n" +
		"jwt=" + jwtSample + "\n" +
		"aws=" + awsSample + "\n" +
		"github=" + githubSample + "\n" +
		privateKeySample + "\n"
	output := string(Scrub([]byte(input)))
	for _, secret := range []string{"supersecret123", jwtSample, awsSample, githubSample, "MIIEowIBAAKCAQEA1234"} {
		if strings.Contains(output, secret) {
			t.Fatalf("scrubbed output still contains %q: %q", secret, output)
		}
	}
	if !strings.Contains(output, "password="+redactedMarker) {
		t.Fatalf("credential key should be preserved: %q", output)
	}
	if strings.Count(output, redactedMarker) < 5 {
		t.Fatalf("expected at least 5 redactions, got %q", output)
	}
}

func TestScrubUnterminatedPrivateKey(t *testing.T) {
	input := "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\nBBBB"
	output := string(Scrub([]byte(input)))
	if strings.Contains(output, "AAAA") || strings.Contains(output, "BBBB") {
		t.Fatalf("unterminated private key not redacted: %q", output)
	}
	if !strings.HasPrefix(output, "before\n") {
		t.Fatalf("text before the key must be preserved: %q", output)
	}
}

func TestScrubLeavesCleanContent(t *testing.T) {
	clean := []byte("namespace: checkout\nOOMKilled exitCode=137\n")
	got := Scrub(clean)
	if string(got) != string(clean) {
		t.Fatalf("clean content changed: %q", got)
	}
	if Scrub(nil) != nil {
		t.Fatal("Scrub(nil) should stay nil")
	}
}

func TestHasFinding(t *testing.T) {
	if !HasFinding([]byte(credentialSample)) {
		t.Fatal("expected credential finding")
	}
	if HasFinding([]byte("namespace: checkout")) {
		t.Fatal("clean content should not be flagged")
	}
}
