// Package bundle loads and validates a local incident bundle.
package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	Dir      string
	Incident Incident
}

var severityValues = map[string]bool{"critical": true, "warning": true, "info": true}

// Load reads incident.yaml with strict field checking.
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
	return &Bundle{Dir: dir, Incident: incident}, nil
}

// Validate checks required fields, timestamps and sensitive content.
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
	observed, err := parseTime("incident.observed_at", b.Incident.ObservedAt, add)
	if err != nil {
		// parseTime already reported the problem
	}
	collected, err := parseTime("incident.collected_at", b.Incident.CollectedAt, add)
	if err == nil && observed != nil && collected.Before(*observed) {
		add("incident.collected_at must not be before observed_at")
	}
	if strings.TrimSpace(b.Incident.Source) == "" {
		add("incident.source is required")
	}

	sensitive, err := ScanSensitive(b.Dir)
	if err != nil {
		add("sensitive scan failed: %v", err)
	}
	problems = append(problems, sensitive...)

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func parseTime(field, value string, add func(string, ...any)) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		add("%s is required", field)
		return nil, errors.New("missing")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		add("%s must be RFC3339 (got %q)", field, value)
		return nil, err
	}
	return &t, nil
}

// ValidationError carries all human-readable problems at once.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "incident bundle is invalid:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Sensitive patterns are intentionally conservative: credentials in key=value or
// key: value form, JWT/private keys and cloud access keys. We do not flag the word
// "secret" alone, which is common in runbooks.
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|api[_-]?key|access[_-]?token|auth[_-]?token|secret[_-]?key)\s*[:=]\s*["']?[A-Za-z0-9_./+=-]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
}

var textExtensions = map[string]bool{
	".yaml": true, ".yml": true, ".json": true, ".log": true, ".txt": true, ".md": true,
}

// ScanSensitive walks text files in the bundle and returns problems without
// including the matched secret value in the message.
func ScanSensitive(dir string) ([]string, error) {
	var problems []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), "report.") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 1<<20))
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for _, re := range sensitivePatterns {
			if loc := re.FindIndex(data); loc != nil {
				problems = append(problems, fmt.Sprintf("%s: possible credential/secret (%s); redact before analysis", rel, re.String()))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return problems, nil
}

// HasFile reports whether a relative file exists in the bundle.
func (b *Bundle) HasFile(rel string) bool {
	_, err := os.Stat(filepath.Join(b.Dir, filepath.FromSlash(rel)))
	return err == nil
}

// ReadFile reads a bundle file with a size cap.
func (b *Bundle) ReadFile(rel string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 256 << 10
	}
	f, err := os.Open(filepath.Join(b.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// WalkTextFiles returns relative text files, excluding incident metadata and
// previously generated reports. Deterministic order is required for reports.
func (b *Bundle) WalkTextFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(b.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, _ := filepath.Rel(b.Dir, path)
		rel = filepath.ToSlash(rel)
		if rel == IncidentFile || strings.HasPrefix(filepath.Base(rel), "report.") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// SumLines is a small helper used by the initial rule analyzer.
func SumLines(data []byte) int {
	if len(bytes.TrimSpace(data)) == 0 {
		return 0
	}
	return bytes.Count(data, []byte("\n")) + 1
}
