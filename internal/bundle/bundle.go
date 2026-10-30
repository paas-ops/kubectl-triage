// Package bundle loads and validates a local incident bundle.
package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
	"gopkg.in/yaml.v3"
)

const IncidentFile = "incident.yaml"

// Incident is the required metadata at the root of an incident bundle.
type Incident struct {
	ID          string `yaml:"id" json:"id"`
	Alertname   string `yaml:"alertname" json:"alertname"`
	Severity    string `yaml:"severity" json:"severity"`
	Cluster     string `yaml:"cluster" json:"cluster"`
	Namespace   string `yaml:"namespace" json:"namespace"`
	Workload    string `yaml:"workload" json:"workload"`
	ObservedAt  string `yaml:"observed_at" json:"observed_at"`
	CollectedAt string `yaml:"collected_at" json:"collected_at"`
	Source      string `yaml:"source" json:"source"`
}

// Bundle is a loaded incident directory.
type Bundle struct {
	Dir        string
	Incident   Incident
	RedactMode redact.Mode

	findings []redact.Finding
}

var severityValues = map[string]bool{"critical": true, "warning": true, "info": true}

// Load reads incident.yaml with strict field checking, converts it to JSON and
// validates it against the public incident schema. The bundle starts in strict
// redaction mode; callers may lower b.RedactMode before Validate/ReadFile.
func Load(dir string) (*Bundle, error) {
	path := filepath.Join(dir, IncidentFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var incident Incident
	if err := dec.Decode(&incident); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s is empty", path)
		}
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	data, err := json.Marshal(incident)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	if err := schema.ValidateIncident(data); err != nil {
		return nil, &ValidationError{Problems: []string{IncidentFile + ": " + err.Error()}}
	}
	return &Bundle{Dir: dir, Incident: incident, RedactMode: redact.ModeStrict}, nil
}

// effectiveMode normalizes the zero value to strict: a Bundle built directly by
// a test or library caller must never silently skip security checks.
func (b *Bundle) effectiveMode() redact.Mode {
	if b.RedactMode == "" {
		return redact.ModeStrict
	}
	return b.RedactMode
}

// Validate checks semantic constraints (timestamps) and scans for sensitive
// content according to the bundle's redaction mode.
//
// It returns *SecurityError (CLI exit code 4) when strict mode finds possible
// credentials, and *ValidationError (CLI exit code 1) for input problems.
// Error messages never echo the matched secret value.
func (b *Bundle) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(b.Incident.ID) == "" {
		add("incident.id is required")
	}
	if strings.TrimSpace(b.Incident.Alertname) == "" {
		add("incident.alertname is required")
	}
	if !severityValues[b.Incident.Severity] {
		add("incident.severity must be one of critical|warning|info")
	}
	if strings.TrimSpace(b.Incident.Cluster) == "" {
		add("incident.cluster is required")
	}
	if strings.TrimSpace(b.Incident.Namespace) == "" {
		add("incident.namespace is required")
	}
	if strings.TrimSpace(b.Incident.Workload) == "" {
		add("incident.workload is required")
	}
	observed, observedErr := parseTime("incident.observed_at", b.Incident.ObservedAt, add)
	collected, collectedErr := parseTime("incident.collected_at", b.Incident.CollectedAt, add)
	if observedErr == nil && collectedErr == nil && collected.Before(observed) {
		add("incident.collected_at must not be before observed_at")
	}
	if strings.TrimSpace(b.Incident.Source) == "" {
		add("incident.source is required")
	}

	mode := b.effectiveMode()
	findings, scanErr := b.Scan()
	if scanErr != nil {
		add("sensitive scan failed: %v", scanErr)
	}
	b.findings = findings
	if mode == redact.ModeStrict && len(findings) > 0 {
		return &SecurityError{Findings: findings}
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// Scan checks the bundle for sensitive content according to the redaction
// mode. ModeOff skips scanning entirely.
func (b *Bundle) Scan() ([]redact.Finding, error) {
	mode := b.effectiveMode()
	if !mode.Scans() {
		return nil, nil
	}
	return redact.Scan(b.Dir)
}

// Findings returns the findings cached by the most recent Validate call.
func (b *Bundle) Findings() []redact.Finding {
	out := make([]redact.Finding, len(b.findings))
	copy(out, b.findings)
	return out
}

func parseTime(field, value string, add func(string, ...any)) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		add("%s is required", field)
		return time.Time{}, errors.New("missing")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		add("%s must be RFC3339 (got %q)", field, value)
		return time.Time{}, err
	}
	return t, nil
}

// ValidationError carries all human-readable input problems at once.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "incident bundle is invalid:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// SecurityError reports possible credentials or secrets found in the bundle.
// It intentionally exposes only file/line/kind, never the matched value.
type SecurityError struct {
	Findings []redact.Finding
}

func (e *SecurityError) Error() string {
	lines := make([]string, 0, len(e.Findings))
	for _, finding := range e.Findings {
		lines = append(lines, "  - "+finding.String())
	}
	return fmt.Sprintf(
		"incident bundle may contain credentials or secrets (%d finding(s)); redact the raw values before analysis:\n%s",
		len(e.Findings), strings.Join(lines, "\n"),
	)
}

// textExtensions is the set of bundle files considered text evidence.
var textExtensions = map[string]bool{
	".yaml": true, ".yml": true, ".json": true, ".log": true, ".txt": true, ".md": true,
}

// resolve turns a bundle-relative path into an absolute path, rejecting
// absolute paths and ".." escapes so evidence references cannot read outside
// the bundle directory.
func (b *Bundle) resolve(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("bundle file path is empty")
	}
	slash := filepath.ToSlash(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("bundle file path %q must be relative", rel)
	}
	clean := path.Clean(slash)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("bundle file path %q escapes the bundle directory", rel)
	}
	full := filepath.Join(b.Dir, filepath.FromSlash(clean))
	within, err := filepath.Rel(b.Dir, full)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bundle file path %q escapes the bundle directory", rel)
	}
	return full, nil
}

// HasFile reports whether a relative file exists in the bundle.
func (b *Bundle) HasFile(rel string) bool {
	full, err := b.resolve(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(full)
	return err == nil
}

// ReadFile reads a bundle file with a size cap. In standard redaction mode the
// returned bytes are scrubbed so analysis and reports never echo credentials.
func (b *Bundle) ReadFile(rel string, maxBytes int64) ([]byte, error) {
	full, err := b.resolve(rel)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = 256 << 10
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, err
	}
	if b.effectiveMode().Scrubs() {
		data = redact.Scrub(data)
	}
	return data, nil
}

// WalkTextFiles returns relative text files, excluding incident metadata and
// previously generated reports. Deterministic order is required for reports.
func (b *Bundle) WalkTextFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(b.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, err := filepath.Rel(b.Dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == IncidentFile || strings.HasPrefix(filepath.Base(rel), "report.") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// ScanSensitive is kept for backward compatibility with earlier callers.
//
// Deprecated: use redact.Scan or Bundle.Scan.
func ScanSensitive(dir string) ([]string, error) {
	findings, err := redact.Scan(dir)
	if err != nil {
		return nil, err
	}
	problems := make([]string, 0, len(findings))
	for _, finding := range findings {
		problems = append(problems, finding.String()+": possible credential/secret")
	}
	return problems, nil
}

// SumLines is a small helper used by the initial rule analyzer.
func SumLines(data []byte) int {
	if len(bytes.TrimSpace(data)) == 0 {
		return 0
	}
	return bytes.Count(data, []byte("\n")) + 1
}
