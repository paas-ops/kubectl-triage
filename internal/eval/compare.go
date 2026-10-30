package eval

// Baseline regression comparison (v1.0 quality gate): compare a current Result
// with a previous eval --json Result by case name. The output is a separate
// stable JSON contract from Result and ValidateSet.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Compare contract identifiers. eval-compare follows the same versioning policy
// as the other eval layers (ADR-007 D1).
const (
	// CompareKind is the stable kind of the eval-compare JSON contract.
	CompareKind = "eval-compare"
	// CompareContractVersion is the version of the eval-compare JSON contract.
	CompareContractVersion = "1.0"
)

// Compare change statuses (frozen vocabulary).
const (
	StatusRegressed = "regressed"
	StatusImproved  = "improved"
	StatusSame      = "same"
	StatusAdded     = "added"
	StatusMissing   = "missing"
)

// Case fields reported when a status change is detected. They mirror the field
// names of the eval Result case JSON.
const (
	fieldPass          = "pass"
	fieldTop1          = "top1"
	fieldTop3          = "top3"
	fieldInjectionSafe = "injection_safe"
	fieldDegraded      = "degraded"
	fieldError         = "error"
)

// CompareChange is one per-case comparison result. Fields is never null and is
// sorted; it lists the case fields that caused the status (empty for same,
// added and missing).
type CompareChange struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Fields []string `json:"fields"`
}

// CompareSummary counts case statuses. Regressions counts status=regressed
// only; a missing baseline case is reported separately but still fails the gate.
type CompareSummary struct {
	Regressions  int `json:"regressions"`
	Improvements int `json:"improvements"`
	Same         int `json:"same"`
	Added        int `json:"added"`
	Missing      int `json:"missing"`
}

// CompareResult is the stable machine-readable output of Compare.
type CompareResult struct {
	Kind            string          `json:"kind"`
	Version         string          `json:"version"`
	BaselineSummary Summary         `json:"baseline_summary"`
	CurrentSummary  Summary         `json:"current_summary"`
	Changes         []CompareChange `json:"changes"`
	Summary         CompareSummary  `json:"summary"`
}

// HasRegressions reports whether the regression gate must fail: a case
// regressed or a baseline case disappeared from the current set.
func (r *CompareResult) HasRegressions() bool {
	return r != nil && (r.Summary.Regressions > 0 || r.Summary.Missing > 0)
}

// LoadBaseline reads a previous eval --json Result used as the regression
// baseline. Only kind=eval/version=1.0 files with at least one case are
// accepted so a wrong or truncated file fails fast instead of silently
// comparing against nothing.
func LoadBaseline(path string) (*Result, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("基线文件路径不能为空")
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, fmt.Errorf("读取基线文件失败: %w", err)
	}
	var baseline Result
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, fmt.Errorf("基线文件不是合法的 eval JSON: %w", err)
	}
	if baseline.Kind != Kind {
		return nil, fmt.Errorf("基线文件 kind=%q，需要 %q（应由 eval --json 生成）", baseline.Kind, Kind)
	}
	if baseline.Version != ContractVersion {
		return nil, fmt.Errorf("基线文件 version=%q，需要 %q", baseline.Version, ContractVersion)
	}
	if len(baseline.Cases) == 0 {
		return nil, fmt.Errorf("基线文件不含任何用例（cases 为空）")
	}
	return &baseline, nil
}

// Compare aligns baseline and current results by case name and classifies each
// case as regressed, improved, same, added or missing.
//
// Regression (any of): pass/top1/top3/injection_safe true->false, degraded
// false->true, error empty->non-empty. Improvement: pass/top1/top3
// false->true and no regression field. A baseline case absent from the current
// set is missing and fails the gate; a current-only case is added and never
// counts as a regression. Changes are sorted by name and each Fields list is
// sorted, so Marshal output is byte-identical across runs.
func Compare(baseline, current *Result) *CompareResult {
	if baseline == nil {
		baseline = &Result{}
	}
	if current == nil {
		current = &Result{}
	}
	baselineCases := indexCaseResults(baseline.Cases)
	currentCases := indexCaseResults(current.Cases)
	names := make([]string, 0, len(baselineCases)+len(currentCases))
	seen := make(map[string]bool, len(baselineCases)+len(currentCases))
	for name := range baselineCases {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for name := range currentCases {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)

	result := &CompareResult{
		Kind:            CompareKind,
		Version:         CompareContractVersion,
		BaselineSummary: baseline.Summary,
		CurrentSummary:  current.Summary,
		Changes:         make([]CompareChange, 0, len(names)),
	}
	for _, name := range names {
		base, baseOK := baselineCases[name]
		cur, curOK := currentCases[name]
		var change CompareChange
		switch {
		case baseOK && !curOK:
			change = CompareChange{Name: name, Status: StatusMissing, Fields: []string{}}
		case !baseOK && curOK:
			change = CompareChange{Name: name, Status: StatusAdded, Fields: []string{}}
		default:
			change = compareAligned(name, base, cur)
		}
		result.Changes = append(result.Changes, change)
		switch change.Status {
		case StatusRegressed:
			result.Summary.Regressions++
		case StatusImproved:
			result.Summary.Improvements++
		case StatusSame:
			result.Summary.Same++
		case StatusAdded:
			result.Summary.Added++
		case StatusMissing:
			result.Summary.Missing++
		}
	}
	return result
}

// indexCaseResults maps case names to results, keeping the first occurrence of
// a name so malformed duplicates cannot make the comparison non-deterministic.
func indexCaseResults(cases []CaseResult) map[string]CaseResult {
	index := make(map[string]CaseResult, len(cases))
	for _, c := range cases {
		if _, exists := index[c.Name]; !exists {
			index[c.Name] = c
		}
	}
	return index
}

// compareAligned classifies one case present in both results.
func compareAligned(name string, baseline, current CaseResult) CompareChange {
	if fields := regressionFields(baseline, current); len(fields) > 0 {
		return CompareChange{Name: name, Status: StatusRegressed, Fields: fields}
	}
	if fields := improvementFields(baseline, current); len(fields) > 0 {
		return CompareChange{Name: name, Status: StatusImproved, Fields: fields}
	}
	return CompareChange{Name: name, Status: StatusSame, Fields: []string{}}
}

// regressionFields returns the sorted fields that make the case a regression.
func regressionFields(baseline, current CaseResult) []string {
	fields := make([]string, 0, 6)
	if baseline.Pass && !current.Pass {
		fields = append(fields, fieldPass)
	}
	if baseline.Top1 && !current.Top1 {
		fields = append(fields, fieldTop1)
	}
	if baseline.Top3 && !current.Top3 {
		fields = append(fields, fieldTop3)
	}
	if baseline.InjectionSafe && !current.InjectionSafe {
		fields = append(fields, fieldInjectionSafe)
	}
	if !baseline.Degraded && current.Degraded {
		fields = append(fields, fieldDegraded)
	}
	if baseline.Error == "" && current.Error != "" {
		fields = append(fields, fieldError)
	}
	sort.Strings(fields)
	return fields
}

// improvementFields returns the sorted pass/top1/top3 fields that improved.
func improvementFields(baseline, current CaseResult) []string {
	fields := make([]string, 0, 3)
	if !baseline.Pass && current.Pass {
		fields = append(fields, fieldPass)
	}
	if !baseline.Top1 && current.Top1 {
		fields = append(fields, fieldTop1)
	}
	if !baseline.Top3 && current.Top3 {
		fields = append(fields, fieldTop3)
	}
	sort.Strings(fields)
	return fields
}
