package loki

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
	data := readTestdata(t, "streams.in.json")
	out, err := Import(importers.Input{Data: data})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := out.Files[0].Path; got != "logs/loki.log" {
		t.Fatalf("default output path = %q, want logs/loki.log", got)
	}
	golden := filepath.Join("testdata", "streams.golden.log")
	if *update {
		if err := os.WriteFile(golden, out.Files[0].Data, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(out.Files[0].Data, want) {
		t.Fatalf("normalized log output differs from golden\n got:\n%s\nwant:\n%s", out.Files[0].Data, want)
	}
}

func TestImportSortsAndEscapes(t *testing.T) {
	data := readTestdata(t, "streams.in.json")
	out, err := Import(importers.Input{Data: data})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out.Files[0].Data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out.Files[0].Data)
	}
	for i := 1; i < len(lines); i++ {
		prev := strings.SplitN(lines[i-1], " ", 2)[0]
		cur := strings.SplitN(lines[i], " ", 2)[0]
		if cur < prev {
			t.Fatalf("line %d is out of order: %q before %q", i, prev, cur)
		}
	}
	// The two entries with timestamp 1793000020 share that timestamp, so they
	// must both come before the later 7d9f entry and be sorted by labels.
	wantLabel := `{app="checkout-api", namespace="checkout", pod="checkout-api-5c8b"}`
	for _, i := range []int{1, 2} {
		if !strings.Contains(lines[i], wantLabel) {
			t.Fatalf("same-timestamp entries are not sorted by labels: line %d = %q", i, lines[i])
		}
	}
	if !strings.Contains(string(out.Files[0].Data), `OOMKilled (exitCode=137)\n`) {
		t.Fatalf("embedded newline was not escaped:\n%s", out.Files[0].Data)
	}
}

func TestImportName(t *testing.T) {
	data := readTestdata(t, "streams.in.json")
	cases := []struct {
		name string
		want string
	}{
		{"", "logs/loki.log"},
		{"checkout", "logs/checkout.log"},
		{"checkout.log", "logs/checkout.log"},
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
}

func TestImportDeterministic(t *testing.T) {
	data := readTestdata(t, "streams.in.json")
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
		{"error status", `{"status":"error"}`, "query status"},
		{"metric query", `{"status":"success","data":{"resultType":"matrix","result":[]}}`, "unsupported resultType"},
		{"bad timestamp", `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["later","line"]]}]}}`, "invalid nanosecond timestamp"},
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

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}
