package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldLoadValidate(t *testing.T) {
	dir := t.TempDir()
	if err := Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
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
}

func TestValidateRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, IncidentFile), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("expected ValidationError, got %T", err)
	}
	joined := strings.Join(ve.Problems, "\n")
	for _, want := range []string{"alertname", "severity", "cluster", "observed_at"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing problem %q in %q", want, joined)
		}
	}
}

func TestValidateRejectsSensitiveContent(t *testing.T) {
	dir := t.TempDir()
	if err := Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "leak.log"), []byte("password=supersecret123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Validate()
	if err == nil || !strings.Contains(err.Error(), "possible credential") {
		t.Fatalf("expected sensitive content error, got %v", err)
	}
	if strings.Contains(err.Error(), "supersecret123") {
		t.Fatal("error must not echo the secret value")
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
