package prometheus

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
		{"matrix", "query-range.in.json", "matrix.golden.json"},
		{"vector", "vector.in.json", "vector.golden.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := importFile(t, tc.input)
			if out.Files[0].Path != "metrics/query.json" {
				t.Fatalf("default output path = %q, want metrics/query.json", out.Files[0].Path)
			}
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
				t.Fatalf("normalized output differs from golden %s\n got:\n%s\nwant:\n%s", golden, out.Files[0].Data, want)
			}
		})
	}
}

func TestImportName(t *testing.T) {
	data := readTestdata(t, "query-range.in.json")
	cases := []struct {
		name string
		want string
	}{
		{"", "metrics/query.json"},
		{"memory", "metrics/memory.json"},
		{"memory.json", "metrics/memory.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Import(importers.Input{Name: tc.name, Data: data})
			if err != nil {
				t.Fatalf("Import(name=%q): %v", tc.name, err)
			}
			if got := out.Files[0].Path; got != tc.want {
				t.Fatalf("Import(name=%q) path = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
	for _, bad := range []string{"../escape", "a/b", ".hidden", "with space"} {
		t.Run("invalid "+bad, func(t *testing.T) {
			if _, err := Import(importers.Input{Name: bad, Data: data}); err == nil {
				t.Fatalf("Import(name=%q) succeeded, want error", bad)
			}
		})
	}
}

func TestImportDeterministic(t *testing.T) {
	data := readTestdata(t, "query-range.in.json")
	first, err := Import(importers.Input{Data: data})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second, err := Import(importers.Input{Data: data})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
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
		{"invalid json", "{", "parse query_range response"},
		{"error status", `{"status":"error","error":"bad query"}`, "query status"},
		{"missing data", `{"status":"success","data":{}}`, "no data.result"},
		{"unsupported result", `{"status":"success","data":{"resultType":"foo","result":[]}}`, "unsupported resultType"},
		{"bad sample", `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1]]}]}}`, "parse matrix result"},
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
	out, err := Import(importers.Input{Data: readTestdata(t, name)})
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	if len(out.Files) != 1 {
		t.Fatalf("import %s: got %d files, want 1", name, len(out.Files))
	}
	return out
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}
