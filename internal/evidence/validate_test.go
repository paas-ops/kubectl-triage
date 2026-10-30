package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validItem(t *testing.T, dir, rel string) Item {
	t.Helper()
	data := []byte("evidence body\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return Item{
		ID:          "ev-001",
		Source:      "logs",
		ObservedAt:  "2026-10-30T13:50:00Z",
		CollectedAt: "2026-10-30T13:50:05Z",
		Summary:     "boom",
		ContentRef:  rel,
		SHA256:      Hash(data),
	}
}

func TestValidateAcceptsItems(t *testing.T) {
	dir := t.TempDir()
	item := validItem(t, dir, "logs/app.log")
	item.ContentRef = "logs/app.log#L1"
	if err := Validate([]Item{item}, dir); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := Validate(nil, dir); err != nil {
		t.Fatalf("empty evidence must validate: %v", err)
	}
}

func TestValidateRejectsBadItems(t *testing.T) {
	dir := t.TempDir()
	base := validItem(t, dir, "logs/app.log")

	cases := []struct {
		name string
		mut  func(*Item)
		want string
	}{
		{"bad id", func(it *Item) { it.ID = "evidence-1" }, "id must look like"},
		{"duplicate id", func(it *Item) {}, "duplicate evidence id"},
		{"empty source", func(it *Item) { it.Source = "" }, "source is required"},
		{"bad observed", func(it *Item) { it.ObservedAt = "yesterday" }, "observed_at"},
		{"bad collected", func(it *Item) { it.CollectedAt = "" }, "collected_at"},
		{"bad hash", func(it *Item) { it.SHA256 = "sha256:xyz" }, "sha256"},
		{"bad ref", func(it *Item) { it.ContentRef = "logs/../secrets.txt" }, "not normalized"},
		{"escaping ref", func(it *Item) { it.ContentRef = "../outside.log" }, "escapes the bundle"},
		{"missing ref file", func(it *Item) { it.ContentRef = "logs/missing.log" }, "missing file"},
		{"bad anchor", func(it *Item) { it.ContentRef = "logs/app.log#foo/1" }, "unsupported anchor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := []Item{base, base}
			c.mut(&items[1])
			err := Validate(items, dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestValidateSyntaxOnlyWithoutBundleDir(t *testing.T) {
	item := Item{
		ID:          "ev-007",
		Source:      "changes",
		ObservedAt:  "2026-10-30T13:50:00Z",
		CollectedAt: "2026-10-30T13:50:01Z",
		ContentRef:  "changes/rollout.json",
		SHA256:      Hash([]byte("x")),
	}
	if err := Validate([]Item{item}, ""); err != nil {
		t.Fatalf("syntax-only validate: %v", err)
	}
}

func TestParseContentRef(t *testing.T) {
	cases := []struct {
		ref   string
		file  string
		items int
		line  int
	}{
		{"k8s/events.json", "k8s/events.json", -1, 0},
		{"k8s/events.json#items/3", "k8s/events.json", 3, 0},
		{"logs/app.log#L1", "logs/app.log", -1, 1},
		{"logs/app.log#L42", "logs/app.log", -1, 42},
	}
	for _, c := range cases {
		got, err := ParseContentRef(c.ref)
		if err != nil {
			t.Fatalf("ParseContentRef(%q): %v", c.ref, err)
		}
		if got.File != c.file || got.Items != c.items || got.Line != c.line {
			t.Fatalf("ParseContentRef(%q) = %+v", c.ref, got)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../b", "a#items/1#L2", "a#items/-1", "a#L0", "a#x"} {
		if _, err := ParseContentRef(bad); err == nil {
			t.Fatalf("ParseContentRef(%q) succeeded, want error", bad)
		}
	}
}
