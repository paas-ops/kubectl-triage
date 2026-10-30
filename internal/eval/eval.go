// Package eval implements the offline local evaluation harness for golden
// incident bundles (ADR-004 D6).
//
// A case directory contains an incident bundle (incident.yaml plus evidence
// files) and expected/report.json. Run analyzes every discovered bundle with a
// fixed clock, scores the generated report against the expected one, and
// returns a deterministic Result without timestamps so the output can be
// committed or diffed. The harness never writes into case directories and only
// reaches the network when the caller explicitly enables model mode through
// Config.Model.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze"
	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

// Result contract identifiers. They are part of the stable --json output that
// the enterprise edition reuses; bump ContractVersion whenever the shape
// changes so consumers can detect the break.
const (
	Kind            = "eval"
	ContractVersion = "1.0"
)

// ExpectedReportPath is the required fixture path, relative to a case
// directory, holding the expected report used for scoring.
const ExpectedReportPath = "expected/report.json"

// AnalyzerVersion is recorded as the CLI version on generated reports. Eval
// runs must never masquerade as a released CLI build in versions.cli.
const AnalyzerVersion = "eval"

// maxEvidence mirrors the analyze default from ADR-002 D4 so eval scores the
// same report the CLI would write by default.
const maxEvidence = 200

// Config controls one local evaluation run.
//
// CasesDir is the directory walked for cases (the directory itself and every
// nested directory containing both incident.yaml and expected/report.json).
// MaxCases <= 0 means no cap. Filter keeps case directories whose base name
// contains the substring. A zero Now means "wall clock" as passed through to
// analyze.Options; golden reproducibility uses an injected fixed clock.
type Config struct {
	CasesDir string
	MaxCases int
	Filter   string
	Now      time.Time
	Model    model.Config
}

// CaseResult is the frozen per-case JSON shape. Do not add timestamps or
// non-deterministic values: the contract is consumed by CI and the enterprise
// edition.
type CaseResult struct {
	Name          string `json:"name"`
	Pass          bool   `json:"pass"`
	Top1          bool   `json:"top1"`
	Top3          bool   `json:"top3"`
	MissingOK     bool   `json:"missing_ok"`
	InjectionSafe bool   `json:"injection_safe"`
	Degraded      bool   `json:"degraded"`
	Error         string `json:"error"`
}

// Summary aggregates the per-case flags. Field order is part of the stable
// JSON contract (encoding/json preserves struct order).
type Summary struct {
	Total         int `json:"total"`
	Top1          int `json:"top1"`
	Top3          int `json:"top3"`
	MissingOK     int `json:"missing_ok"`
	InjectionSafe int `json:"injection_safe"`
	Degraded      int `json:"degraded"`
	Errors        int `json:"errors"`
	Pass          int `json:"pass"`
}

// Result is the stable machine-readable output of one eval run. The top-level
// fields are frozen by ADR-004 D6/D7.
type Result struct {
	Kind    string       `json:"kind"`
	Version string       `json:"version"`
	Cases   []CaseResult `json:"cases"`
	Summary Summary      `json:"summary"`
}

// caseRef is one discovered golden case.
type caseRef struct {
	name string
	dir  string
}

// Run discovers, analyzes and scores golden cases under cfg.CasesDir.
//
// Per-case failures (unreadable bundle, invalid expected report, analysis error)
// are recorded in CaseResult.Error and make the case fail; they do not abort the
// run. Only discovery problems (missing directory, no cases, empty CaseDir)
// return an error so the CLI can map them to exit code 1.
func Run(cfg Config) (*Result, error) {
	cases, err := discoverCases(cfg.CasesDir, cfg.Filter, cfg.MaxCases)
	if err != nil {
		return nil, err
	}
	result := &Result{
		Kind:    Kind,
		Version: ContractVersion,
		Cases:   make([]CaseResult, 0, len(cases)),
	}
	for _, c := range cases {
		result.Cases = append(result.Cases, runCase(c, cfg))
	}
	result.Summary = summarize(result.Cases)
	return result, nil
}

// discoverCases walks casesDir for directories holding incident.yaml and
// expected/report.json, filters by directory name, sorts by case name and
// applies the MaxCases cap.
func discoverCases(casesDir, filter string, maxCases int) ([]caseRef, error) {
	trimmed := strings.TrimSpace(casesDir)
	if trimmed == "" {
		return nil, fmt.Errorf("用例目录不能为空")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("解析用例目录 %q 失败: %w", casesDir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("用例目录不存在: %s", abs)
		}
		return nil, fmt.Errorf("读取用例目录 %s 失败: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("用例目录不是目录: %s", abs)
	}

	var found []caseRef
	walkErr := filepath.WalkDir(abs, func(dir string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || !isCaseDir(dir) {
			return nil
		}
		rel, relErr := filepath.Rel(abs, dir)
		if relErr != nil {
			return relErr
		}
		name := filepath.ToSlash(rel)
		if name == "." {
			name = filepath.Base(abs)
		}
		if filter != "" && !strings.Contains(path.Base(name), filter) {
			return nil
		}
		found = append(found, caseRef{name: name, dir: dir})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("遍历用例目录 %s 失败: %w", abs, walkErr)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	if len(found) == 0 {
		if filter != "" {
			return nil, fmt.Errorf("用例目录 %s 中没有匹配 %q 的用例", abs, filter)
		}
		return nil, fmt.Errorf("用例目录 %s 中没有可用例（每个用例需包含 incident.yaml 与 %s）", abs, ExpectedReportPath)
	}
	if maxCases > 0 && len(found) > maxCases {
		found = found[:maxCases]
	}
	return found, nil
}

// isCaseDir reports whether dir looks like a golden case directory.
func isCaseDir(dir string) bool {
	for _, rel := range []string{bundle.IncidentFile, ExpectedReportPath} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// runCase analyzes one case and scores it against the expected report. Every
// error is captured in the returned CaseResult so one broken fixture cannot
// hide the remaining cases.
func runCase(c caseRef, cfg Config) CaseResult {
	var out CaseResult
	loaded, err := bundle.Load(c.dir)
	if err != nil {
		out.Name = c.name
		out.Error = "加载事故包失败: " + err.Error()
		return out
	}
	if err := loaded.Validate(); err != nil {
		out.Name = c.name
		out.Error = "事故包校验失败: " + err.Error()
		return out
	}
	expected, err := loadExpected(filepath.Join(c.dir, filepath.FromSlash(ExpectedReportPath)))
	if err != nil {
		out.Name = c.name
		out.Error = "读取期望报告失败: " + err.Error()
		return out
	}
	generated, err := analyze.Analyze(loaded, AnalyzerVersion, analyze.Options{
		MaxEvidence: maxEvidence,
		Now:         cfg.Now,
		Model:       cfg.Model,
	})
	if err != nil {
		out.Name = c.name
		out.Error = "分析失败: " + err.Error()
		return out
	}
	generatedJSON, err := json.Marshal(generated)
	if err != nil {
		out.Name = c.name
		out.Error = "编码生成报告失败: " + err.Error()
		return out
	}
	markdown, err := renderMarkdown(generated)
	if err != nil {
		out.Name = c.name
		out.Error = "渲染生成报告 Markdown 失败: " + err.Error()
		return out
	}

	expectedHasHypotheses := len(expected.Hypotheses) > 0
	top1, top3, missingOK, injectionSafe := score(expected, generated, generatedJSON, markdown)
	out.Name = c.name
	out.Top1 = top1
	out.Top3 = top3
	out.MissingOK = missingOK
	out.InjectionSafe = injectionSafe
	out.Degraded = generated.Degraded
	out.Pass = casePass(expectedHasHypotheses, top1, missingOK, injectionSafe, out.Degraded)
	return out
}

// loadExpected reads and schema-validates expected/report.json. A broken
// fixture is reported as a case error instead of silently scoring wrong data.
func loadExpected(file string) (*report.Report, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if err := schema.ValidateReport(data); err != nil {
		return nil, fmt.Errorf("%s 不符合 report schema: %w", file, err)
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", file, err)
	}
	return &rep, nil
}

// summarize aggregates the per-case flags into the summary block.
func summarize(cases []CaseResult) Summary {
	out := Summary{Total: len(cases)}
	for _, c := range cases {
		if c.Pass {
			out.Pass++
		}
		if c.Top1 {
			out.Top1++
		}
		if c.Top3 {
			out.Top3++
		}
		if c.MissingOK {
			out.MissingOK++
		}
		if c.InjectionSafe {
			out.InjectionSafe++
		}
		if c.Degraded {
			out.Degraded++
		}
		if c.Error != "" {
			out.Errors++
		}
	}
	return out
}
