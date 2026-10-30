package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	idPattern     = regexp.MustCompile(`^ev-[0-9]{3,}$`)
	sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	itemsAnchor   = regexp.MustCompile(`^items/([0-9]+)$`)
	logLineAnchor = regexp.MustCompile(`^L([1-9][0-9]*)$`)
)

// ContentRef describes where an evidence entry came from:
//
//	<rel>              whole file
//	<rel>#items/<n>    zero-based JSON array item (kubectl event lists)
//	<rel>#L<line>      1-based log line anchor
type ContentRef struct {
	File  string
	Items int // -1 when the ref is not an item anchor
	Line  int // 0 when the ref is not a line anchor
}

// ParseContentRef validates and splits a content reference.
func ParseContentRef(ref string) (ContentRef, error) {
	if strings.TrimSpace(ref) == "" {
		return ContentRef{}, fmt.Errorf("content_ref is required")
	}
	if strings.Count(ref, "#") > 1 {
		return ContentRef{}, fmt.Errorf("content_ref %q has more than one anchor", ref)
	}
	file, anchor := ref, ""
	if i := strings.Index(ref, "#"); i >= 0 {
		file, anchor = ref[:i], ref[i+1:]
	}
	if err := validateRelPath(file); err != nil {
		return ContentRef{}, fmt.Errorf("content_ref %q: %w", ref, err)
	}
	out := ContentRef{File: file, Items: -1}
	if anchor == "" {
		return out, nil
	}
	if m := itemsAnchor.FindStringSubmatch(anchor); m != nil {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		out.Items = n
		return out, nil
	}
	if m := logLineAnchor.FindStringSubmatch(anchor); m != nil {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		out.Line = n
		return out, nil
	}
	return ContentRef{}, fmt.Errorf("content_ref %q: unsupported anchor %q (want items/<n> or L<line>)", ref, anchor)
}

func validateRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("empty file path")
	}
	if strings.Contains(rel, "\\") {
		return fmt.Errorf("file path must use forward slashes")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return fmt.Errorf("file path must be relative to the bundle")
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == "." || cleaned != rel {
		return fmt.Errorf("file path %q is not normalized", rel)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("file path %q escapes the bundle", rel)
	}
	return nil
}

// ValidationError carries every evidence problem found in one pass.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "evidence is invalid: " + strings.Join(e.Problems, "; ")
}

// Validate checks ids, sources, timestamps, hashes and content references.
//
// bundleDir may be empty to only check the reference syntax; when set, every
// referenced file must exist inside the bundle and must not escape it.
func Validate(items []Item, bundleDir string) error {
	var problems []string
	seen := map[string]bool{}
	for _, it := range items {
		label := it.ID
		if label == "" {
			label = "<empty id>"
		}
		if !idPattern.MatchString(it.ID) {
			problems = append(problems, fmt.Sprintf("%s: id must look like ev-001", label))
		}
		if it.ID != "" {
			if seen[it.ID] {
				problems = append(problems, fmt.Sprintf("%s: duplicate evidence id", it.ID))
			}
			seen[it.ID] = true
		}
		if strings.TrimSpace(it.Source) == "" {
			problems = append(problems, fmt.Sprintf("%s: source is required", label))
		}
		if _, err := ParseTime(it.ObservedAt); err != nil {
			problems = append(problems, fmt.Sprintf("%s: observed_at %q is not RFC3339", label, it.ObservedAt))
		}
		if _, err := ParseTime(it.CollectedAt); err != nil {
			problems = append(problems, fmt.Sprintf("%s: collected_at %q is not RFC3339", label, it.CollectedAt))
		}
		if !sha256Pattern.MatchString(it.SHA256) {
			problems = append(problems, fmt.Sprintf("%s: sha256 must look like sha256:<64 hex chars>", label))
		}
		if err := validateContentRef(it.ContentRef, bundleDir); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", label, err))
		}
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func validateContentRef(ref, bundleDir string) error {
	parsed, err := ParseContentRef(ref)
	if err != nil {
		return err
	}
	if bundleDir == "" {
		return nil
	}
	root := filepath.Clean(bundleDir)
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(parsed.File)))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("content_ref %q points outside bundle %q", ref, bundleDir)
	}
	if info, err := os.Stat(full); err != nil || info.IsDir() {
		return fmt.Errorf("content_ref %q points to a missing file", ref)
	}
	return nil
}
