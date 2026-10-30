package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bavocado/kubectl-triage/internal/bundle"
)

func TestAnalyzeExampleOOM(t *testing.T) {
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	rep, err := Analyze(b, "test")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(rep.Hypotheses) == 0 {
		t.Fatal("expected an OOM hypothesis")
	}
	if rep.Hypotheses[0].Statement == "" || len(rep.Hypotheses[0].EvidenceIDs) == 0 {
		t.Fatalf("hypothesis lacks statement/evidence: %+v", rep.Hypotheses[0])
	}
	if len(rep.Evidence) == 0 {
		t.Fatal("expected evidence")
	}
	if err := rep.Write(dir); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("report.json invalid: %v", err)
	}
	if decoded["schema_version"] != "1.0" {
		t.Fatalf("unexpected schema_version: %v", decoded["schema_version"])
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
		t.Fatalf("report.md missing: %v", err)
	}
}
