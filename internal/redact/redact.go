// Package redact detects and scrubs credential-like content in incident bundle
// files. It is the single source of truth for sensitive patterns used by
// validation and by analysis-time reads, so reports can never echo secrets.
package redact

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Mode selects how sensitive content is handled by the CLI.
type Mode string

const (
	// ModeStrict rejects bundles that contain sensitive content.
	ModeStrict Mode = "strict"
	// ModeStandard allows the bundle but scrubs matches before analysis.
	ModeStandard Mode = "standard"
	// ModeOff disables scanning and scrubbing. It is not recommended and must
	// only be used for already-sanitized bundles.
	ModeOff Mode = "off"
)

// ParseMode validates a --redact flag value.
func ParseMode(value string) (Mode, error) {
	switch mode := Mode(strings.ToLower(strings.TrimSpace(value))); mode {
	case ModeStrict, ModeStandard, ModeOff:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid redact mode %q (want strict|standard|off)", value)
	}
}

// String returns the CLI spelling of the mode.
func (m Mode) String() string { return string(m) }

// Scans reports whether the mode rejects or reports sensitive content.
func (m Mode) Scans() bool { return m == ModeStrict || m == ModeStandard }

// Scrubs reports whether bundle reads must be sanitized before analysis.
func (m Mode) Scrubs() bool { return m == ModeStandard }

// Finding is one sensitive-content match. It deliberately does not retain the
// matched value: callers may print findings, and reports must never echo
// credentials.
type Finding struct {
	// File is the bundle-relative slash path.
	File string
	// Line is the 1-based line number of the match.
	Line int
	// Kind names the detected pattern, for example "credential assignment".
	Kind string
}

// String implements fmt.Stringer without exposing the matched content.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Kind)
}

const (
	// maxScanBytes caps how much of a single file is inspected.
	maxScanBytes = 1 << 20
	// redactedMarker replaces sensitive values during scrubbing.
	redactedMarker = "[REDACTED]"
)

// textExtensions is the set of bundle files scanned for sensitive content.
var textExtensions = map[string]bool{
	".yaml": true, ".yml": true, ".json": true, ".log": true, ".txt": true, ".md": true,
}

type rule struct {
	kind        string
	scan        *regexp.Regexp
	scrub       *regexp.Regexp
	replacement string
}

// rules mirrors the conservative pattern set that used to live in
// internal/bundle: credentials in key=value form, JWTs, private keys, cloud
// access keys and GitHub tokens. The word "secret" alone is not flagged
// because it is common in runbooks.
var rules = []rule{
	{
		kind:        "credential assignment",
		scan:        regexp.MustCompile(`(?i)(password|passwd|api[_-]?key|access[_-]?token|auth[_-]?token|secret[_-]?key)\s*[:=]\s*["']?[A-Za-z0-9_./+=-]{8,}`),
		scrub:       regexp.MustCompile(`(?i)((?:password|passwd|api[_-]?key|access[_-]?token|auth[_-]?token|secret[_-]?key)\s*[:=]\s*["']?)[A-Za-z0-9_./+=-]{8,}`),
		replacement: "${1}" + redactedMarker,
	},
	{
		kind:        "JSON Web Token",
		scan:        regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`),
		scrub:       regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`),
		replacement: redactedMarker,
	},
	{
		kind:        "private key",
		scan:        regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		scrub:       regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
		replacement: redactedMarker,
	},
	{
		kind:        "cloud access key",
		scan:        regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		scrub:       regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		replacement: redactedMarker,
	},
	{
		kind:        "GitHub token",
		scan:        regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
		scrub:       regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
		replacement: redactedMarker,
	},
}

// Scrub replaces every sensitive match in data with [REDACTED]. The original
// value is never returned to the caller, and non-sensitive text is preserved.
func Scrub(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	out := data
	for _, r := range rules {
		if r.scrub == nil {
			continue
		}
		out = r.scrub.ReplaceAll(out, []byte(r.replacement))
	}
	return out
}

// Scan walks root and returns every sensitive-content finding in text files.
// Findings never contain the matched value, so callers can safely print them.
// Results are ordered by file path and line number.
func Scan(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		// Generated reports are outputs, not evidence; scanning them would make
		// validate fail on its own artifacts.
		if strings.HasPrefix(filepath.Base(path), "report.") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return scanFile(path, filepath.ToSlash(rel), &findings)
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

func scanFile(path, rel string, findings *[]Finding) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("scan %s: %w", rel, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(io.LimitReader(f, maxScanBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), 2*maxScanBytes)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Bytes()
		for _, r := range rules {
			if r.scan.Match(text) {
				*findings = append(*findings, Finding{File: rel, Line: line, Kind: r.kind})
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", rel, err)
	}
	return nil
}

// HasFinding reports whether data contains sensitive content. It is used by
// callers that only need a yes/no answer.
func HasFinding(data []byte) bool {
	for _, r := range rules {
		if r.scan.Match(data) {
			return true
		}
	}
	return false
}
