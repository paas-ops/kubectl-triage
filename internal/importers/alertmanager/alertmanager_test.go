package alertmanager

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

// update rewrites the golden files when the normalized layout changes.
var update = flag.Bool("update", false, "rewrite testdata golden files")

func TestImportGolden(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		golden string
	}{
		{"webhook payload", "webhook.in.json", "alert.golden.json"},
		{"api alert list", "list.in.json", "alert.golden.json"},
		{"single alert", "single.in.json", "single.golden.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := importFile(t, tc.input)
			golden := filepath.Join("testdata", tc.golden)
			if *update {
				if err := os.WriteFile(golden, out.Files[0].Data, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden %s: %v", golden, err)
			}
			if !bytes.Equal(out.Files[0].Data, want) {
				t.Fatalf("normalized alert.json differs from golden\n got:\n%s\nwant:\n%s", out.Files[0].Data, want)
			}
		})
	}
}

func TestImportDeterministic(t *testing.T) {
	first := importFile(t, "webhook.in.json")
	second := importFile(t, "webhook.in.json")
	if !bytes.Equal(first.Files[0].Data, second.Files[0].Data) {
		t.Fatalf("import is not deterministic:\n%s\n%s", first.Files[0].Data, second.Files[0].Data)
	}
}

func TestImportErrors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"empty", "", "empty input"},
		{"not json", "alert: yes", "not JSON"},
		{"invalid json", "{", "parse input"},
		{"unsupported object", `{"foo": 1}`, "unsupported JSON object"},
		{"bad status", `{"alerts":[{"status":"pending"}]}`, "unsupported status"},
		{"bad timestamp", `{"alerts":[{"status":"firing","startsAt":"yesterday"}]}`, "startsAt is not RFC3339"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Import(importers.Input{Data: []byte(tc.input)})
			if err == nil {
				t.Fatalf("Import(%q) succeeded, want error", tc.input)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err, tc.wantSub)
			}
		})
	}
}

func importFile(t *testing.T, name string) *importers.Output {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	out, err := Import(importers.Input{Data: data})
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	if len(out.Files) != 1 || out.Files[0].Path != "alert.json" {
		t.Fatalf("import %s: unexpected output files %v", name, out.Paths())
	}
	return out
}
