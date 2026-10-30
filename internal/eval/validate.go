package eval

// Set validation (ADR-007 D4): validate an evaluation-set without running any
// analysis. The result is a separate stable JSON contract from Result so the
// two can evolve independently.

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

// Set contract identifiers. kind=eval-set is independent of the analysis
// Result contract and follows the same versioning policy (ADR-007 D1/D4).
const (
	SetKind            = "eval-set"
	SetContractVersion = "1.0"
)

// maxCredentialScanBytes caps per-file credential scanning, mirroring the
// internal/redact line-scanner limit.
const maxCredentialScanBytes = 1 << 20

// setTextExtensions mirrors the text files that bundle/redact treat as bundle
// content, so the credential scan covers incident.yaml, evidence and the
// expected report.
var setTextExtensions = map[string]bool{
	".yaml": true, ".yml": true, ".json": true, ".log": true, ".txt": true, ".md": true,
}

// SetCaseResult is one validated case. Errors is never null (empty slice when
// valid) and is sorted so repeated runs are byte-identical.
type SetCaseResult struct {
	Name   string   `json:"name"`
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// SetSummary aggregates the set validation outcome. Field order is part of the
// stable JSON contract.
type SetSummary struct {
	Total            int `json:"total"`
	Valid            int `json:"valid"`
	Invalid          int `json:"invalid"`
	MinCasesRequired int `json:"min_cases_required"`
}

// SetValidation is the stable machine-readable output of ValidateSet.
type SetValidation struct {
	Kind    string          `json:"kind"`
	Version string          `json:"version"`
	Cases   []SetCaseResult `json:"cases"`
	Summary SetSummary      `json:"summary"`
}

// setCaseCandidate is one directory that may be a case. A candidate has at
// least one of incident.yaml / expected/report.json so a directory missing one
// half is reported as invalid instead of being silently skipped.
type setCaseCandidate struct {
	name string
	rel  string
	dir  string
}

// ValidateSet validates an evaluation-set without analyzing it (ADR-007 D4).
//
// Discovery mirrors Run but also treats directories that contain only one of
// incident.yaml / expected/report.json as invalid cases. Filter and MaxCases
// follow the same contract as Run (basename substring and cap). Now and Model
// are intentionally unused: validation never runs the analyzer and never
// reaches the network. MinCases is echoed in the summary; the caller maps
// invalid > 0 or total < MinCases to exit code 2.
//
// Errors are returned only for discovery/input problems (missing directory, no
// candidates). Broken cases are reported as invalid entries, not run errors.
func ValidateSet(cfg Config) (*SetValidation, error) {
	root, err := resolveCasesDir(cfg.CasesDir)
	if err != nil {
		return nil, err
	}
	candidates, err := walkSetCases(root, cfg.Filter)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		if cfg.Filter != "" {
			return nil, fmt.Errorf("用例目录 %s 中没有匹配 %q 的用例", root, cfg.Filter)
		}
		return nil, fmt.Errorf("用例目录 %s 中没有可用例（每个用例需包含 incident.yaml 与 %s）", root, ExpectedReportPath)
	}
	if cfg.MaxCases > 0 && len(candidates) > cfg.MaxCases {
		candidates = candidates[:cfg.MaxCases]
	}

	validation := &SetValidation{
		Kind:    SetKind,
		Version: SetContractVersion,
		Cases:   make([]SetCaseResult, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		validation.Cases = append(validation.Cases, validateSetCase(candidate))
	}
	markDuplicateSetCaseNames(validation.Cases)
	validation.Summary = summarizeSet(validation.Cases, cfg.MinCases)
	return validation, nil
}

// walkSetCases walks root for case candidates, filters by directory basename,
// sorts by name (relative path as a deterministic tie-break) and keeps the
// order stable across runs.
func walkSetCases(root, filter string) ([]setCaseCandidate, error) {
	var candidates []setCaseCandidate
	walkErr := filepath.WalkDir(root, func(dir string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || !isSetCaseCandidate(dir) {
			return nil
		}
		name := filepath.Base(dir)
		if filter != "" && !strings.Contains(name, filter) {
			return nil
		}
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			return relErr
		}
		candidates = append(candidates, setCaseCandidate{name: name, rel: filepath.ToSlash(rel), dir: dir})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("遍历用例目录 %s 失败: %w", root, walkErr)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].name != candidates[j].name {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].rel < candidates[j].rel
	})
	return candidates, nil
}

// isSetCaseCandidate reports whether dir holds at least one required case file.
func isSetCaseCandidate(dir string) bool {
	return isRegularFile(filepath.Join(dir, bundle.IncidentFile)) ||
		isRegularFile(filepath.Join(dir, filepath.FromSlash(ExpectedReportPath)))
}

func isRegularFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && !info.IsDir()
}

// validateSetCase runs every set check for one candidate and aggregates all
// problems so a fixture is never reported one problem at a time.
func validateSetCase(candidate setCaseCandidate) SetCaseResult {
	result := SetCaseResult{Name: candidate.name, Errors: []string{}}
	incidentPath := filepath.Join(candidate.dir, bundle.IncidentFile)
	expectedPath := filepath.Join(candidate.dir, filepath.FromSlash(ExpectedReportPath))

	incidentExists := isRegularFile(incidentPath)
	expectedExists := isRegularFile(expectedPath)
	if !incidentExists {
		result.Errors = append(result.Errors, "缺少 incident.yaml")
	}
	if !expectedExists {
		result.Errors = append(result.Errors, "缺少 expected/report.json")
	}
	if incidentExists {
		if err := validateIncidentFile(candidate.dir); err != nil {
			result.Errors = appendSetProblem(result.Errors, "incident.yaml 无效", err)
		}
	}
	if expectedExists {
		if err := validateExpectedReport(expectedPath); err != nil {
			result.Errors = appendSetProblem(result.Errors, "expected/report.json 无效", err)
		}
	}
	problems, err := credentialProblems(candidate.dir)
	if err != nil {
		result.Errors = appendSetProblem(result.Errors, "凭据扫描失败", err)
	}
	result.Errors = append(result.Errors, problems...)
	sort.Strings(result.Errors)
	result.Valid = len(result.Errors) == 0
	return result
}

// appendSetProblem appends a single-line Chinese-prefixed problem. Schema and
// YAML errors keep their original (English) detail, but embedded newlines are
// flattened so one problem is one line in JSON and terminal output.
func appendSetProblem(problems []string, prefix string, err error) []string {
	detail := strings.ReplaceAll(err.Error(), "\n", "；")
	return append(problems, prefix+": "+detail)
}

// validateIncidentFile checks incident.yaml against the incident schema
// (bundle.Load) and the semantic rules in bundle.Validate. The bundle scan is
// disabled because credential scanning is done separately over every text
// file, including expected/report.json, which bundle scanning skips.
func validateIncidentFile(caseDir string) error {
	loaded, err := bundle.Load(caseDir)
	if err != nil {
		return err
	}
	loaded.RedactMode = redact.ModeOff
	return loaded.Validate()
}

// validateExpectedReport checks expected/report.json parses and matches the
// frozen report schema.
func validateExpectedReport(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return schema.ValidateReport(data)
}

// credentialProblems scans every text file under the case directory (incident,
// evidence and expected report) with internal/redact.Scrub and reports files
// whose scrubbed form differs. The matched value is never returned.
func credentialProblems(caseDir string) ([]string, error) {
	var problems []string
	walkErr := filepath.WalkDir(caseDir, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !setTextExtensions[strings.ToLower(filepath.Ext(file))] {
			return nil
		}
		data, readErr := readCappedFile(file)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(redact.Scrub(data), data) {
			return nil
		}
		rel, relErr := filepath.Rel(caseDir, file)
		if relErr != nil {
			rel = file
		}
		problems = append(problems, "包含疑似凭据模式: "+filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return problems, nil
}

func readCappedFile(name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxCredentialScanBytes))
}

// markDuplicateSetCaseNames flags every case sharing a directory name. Names
// are unique in a formal evaluation set; nested duplicates are ambiguous for
// reporting, so both entries become invalid.
func markDuplicateSetCaseNames(cases []SetCaseResult) {
	counts := make(map[string]int, len(cases))
	for _, c := range cases {
		counts[c.Name]++
	}
	for i := range cases {
		if counts[cases[i].Name] < 2 {
			continue
		}
		cases[i].Errors = append(cases[i].Errors, "用例名重复: "+cases[i].Name)
		sort.Strings(cases[i].Errors)
		cases[i].Valid = false
	}
}

// summarizeSet aggregates the validation outcome and echoes the minimum case
// requirement so consumers can decide exit codes from the JSON alone.
func summarizeSet(cases []SetCaseResult, minCases int) SetSummary {
	summary := SetSummary{Total: len(cases), MinCasesRequired: minCases}
	for _, c := range cases {
		if c.Valid {
			summary.Valid++
		} else {
			summary.Invalid++
		}
	}
	return summary
}
