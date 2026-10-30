package kubectl

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
		{"events json", "events.in.json", "events.golden.json"},
		{"events yaml", "events.in.yaml", "events.golden.json"},
		{"pods yaml", "pods.in.yaml", "pods.golden.yaml"},
		{"pods generic list", "pods-list.in.yaml", "pods.golden.yaml"},
		{"single pod", "pod-single.in.json", "pod-single.golden.yaml"},
		{"deployments yaml", "deployments.in.yaml", "deployments.golden.yaml"},
		{"single deployment", "deployment-single.in.yaml", "deployment-single.golden.yaml"},
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
				t.Fatalf("normalized output differs from golden %s\n got:\n%s\nwant:\n%s", golden, out.Files[0].Data, want)
			}
		})
	}
}

func TestImportDeterministic(t *testing.T) {
	first := importFile(t, "pods.in.yaml")
	second := importFile(t, "pods.in.yaml")
	if !bytes.Equal(first.Files[0].Data, second.Files[0].Data) {
		t.Fatalf("import is not deterministic:\n%s\n%s", first.Files[0].Data, second.Files[0].Data)
	}
}

func TestImportRoutesByKind(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"events.in.json", "k8s/events.json"},
		{"pods.in.yaml", "k8s/pods.yaml"},
		{"deployments.in.yaml", "k8s/deployments.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			out := importFile(t, tc.input)
			if got := out.Files[0].Path; got != tc.want {
				t.Fatalf("import %s routed to %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestImportErrors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"empty", "", "empty input"},
		{"no kind", `{"items": []}`, "has no kind"},
		{"unknown kind", "kind: Service\napiVersion: v1\n", "unsupported kind"},
		{"unterminated", "kind: PodList\nitems: [", "parse input"},
		{"mixed list", "kind: List\nitems:\n- kind: Pod\n- kind: Event\n", "mixes kinds"},
		{"empty list", "kind: List\nitems: []\n", "no items"},
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
	if len(out.Files) != 1 {
		t.Fatalf("import %s: got %d files, want 1", name, len(out.Files))
	}
	return out
}
