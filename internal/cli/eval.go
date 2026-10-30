package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/eval"
	"github.com/spf13/cobra"
)

// evalModelKeyEnv and evalOpenAIKeyEnv are the only accepted sources for
// provider credentials (ADR-004 D2): keys never come from flags, reports or
// logs, and they are never echoed back to the user.
const (
	evalModelKeyEnv  = "KUBECTL_TRIAGE_MODEL_KEY"
	evalOpenAIKeyEnv = "OPENAI_API_KEY"

	// Provider defaults from ADR-004 D2, mirrored from the analyze command so
	// both commands resolve the same flags identically.
	evalOllamaDefaultURL   = "http://127.0.0.1:11434"
	evalOpenAIDefaultModel = "gpt-4o-mini"
)

// evalRunner is indirected through a package variable so tests can capture the
// resolved eval.Config without running the analyzer.
var evalRunner = eval.Run

// newEvalCmd wires the local evaluation harness over golden incident bundles
// (ADR-004 D6). It prints a Chinese table by default or the stable Result JSON
// with --json, and maps outcomes to the documented exit codes: 0 all cases
// pass, 1 input/configuration error, 2 at least one case failed.
func newEvalCmd() *cobra.Command {
	var (
		jsonOutput   bool
		validateSet  bool
		filter       string
		maxCases     int
		minCases     int
		comparePath  string
		nowText      string
		provider     string
		modelURL     string
		modelName    string
		modelTimeout time.Duration
		modelBudget  float64
		modelRounds  int
	)
	cmd := &cobra.Command{
		Use:   "eval <cases-dir>",
		Short: "Evaluate analysis quality against golden incident bundles",
		Long: `eval discovers incident bundles under <cases-dir> (each case needs incident.yaml and
expected/report.json), runs the deterministic analyzer with a fixed clock and scores
Top1/Top3, missing-evidence handling, injection safety and degradation.
Exit codes: 0 all cases pass, 1 input/configuration error, 2 at least one case failed.
--json prints the stable machine-readable Result contract; --now reproduces golden
expectations and model flags mirror the analyze command. Provider keys are read only
from KUBECTL_TRIAGE_MODEL_KEY or OPENAI_API_KEY, never from flags.
--validate switches to set validation (ADR-007 D4): it checks discovery, incident and
expected-report schemas, unique case names, credential patterns and --min-cases without
analyzing anything. --filter/--max-cases still apply; --now and model flags are ignored
in validate mode.
--compare loads a baseline saved from an earlier "eval ... --json" run, re-runs the
current evaluation and reports per-case regressions/improvements. The gate exits 2 when
any case regresses or a baseline case disappears; case additions never fail the gate.
--json prints the stable eval-compare contract instead of the text summary.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if minCases < 0 {
				return &ExitError{Code: 1, Err: fmt.Errorf("--min-cases 不能为负数")}
			}
			compareFile := strings.TrimSpace(comparePath)
			if validateSet && compareFile != "" {
				return &ExitError{Code: 1, Err: fmt.Errorf("--compare 与 --validate 不能同时使用")}
			}
			if validateSet {
				return runEvalSetValidation(cmd, args[0], jsonOutput, filter, maxCases, minCases)
			}
			if minCases != 0 {
				return &ExitError{Code: 1, Err: fmt.Errorf("--min-cases 只能与 --validate 一起使用")}
			}
			now, err := parseEvalNow(nowText)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			modelCfg := evalModelConfig(provider, modelURL, modelName, modelTimeout, modelBudget, modelRounds)
			if err := validateEvalModelConfig(modelCfg); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			// Load the baseline before running the analyzer so a bad baseline
			// fails fast (exit 1) instead of wasting a full eval run.
			var baseline *eval.Result
			if compareFile != "" {
				loaded, err := eval.LoadBaseline(compareFile)
				if err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				baseline = loaded
			}
			result, err := evalRunner(eval.Config{
				CasesDir: args[0],
				MaxCases: maxCases,
				Filter:   filter,
				Now:      now,
				Model:    modelCfg,
			})
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if baseline != nil {
				comparison := eval.Compare(baseline, result)
				if jsonOutput {
					data, err := json.MarshalIndent(comparison, "", "  ")
					if err != nil {
						return &ExitError{Code: 1, Err: fmt.Errorf("编码回归对比结果失败: %w", err)}
					}
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
				} else {
					writeEvalCompareText(cmd.OutOrStdout(), compareFile, comparison)
				}
				// The regression gate is independent of the current pass rate:
				// only regressions against the baseline (or baseline cases that
				// disappeared) fail the run, so a stable known-failing set stays
				// green while any metric rollback is caught.
				if comparison.HasRegressions() {
					return &ExitError{Code: 2, Err: fmt.Errorf("eval: 相对基线发现 %d 个回归用例、%d 个缺失用例",
						comparison.Summary.Regressions, comparison.Summary.Missing)}
				}
				return nil
			}
			if jsonOutput {
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("编码 eval 结果失败: %w", err)}
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				writeEvalText(cmd.OutOrStdout(), result)
			}
			if result.Summary.Pass < result.Summary.Total {
				return &ExitError{Code: 2, Err: fmt.Errorf(
					"eval: %d/%d 个用例未通过（通过 %d，错误 %d，降级 %d）",
					result.Summary.Total-result.Summary.Pass, result.Summary.Total,
					result.Summary.Pass, result.Summary.Errors, result.Summary.Degraded)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the stable JSON result instead of the text table")
	cmd.Flags().StringVar(&filter, "filter", "", "only evaluate case directories whose name contains this substring")
	cmd.Flags().IntVar(&maxCases, "max-cases", 0, "maximum number of cases to evaluate (0 = no cap)")
	cmd.Flags().BoolVar(&validateSet, "validate", false, "only validate the evaluation set (schemas, unique names, credentials, --min-cases); does not analyze")
	cmd.Flags().IntVar(&minCases, "min-cases", 0, "with --validate: require at least N cases (0 = no minimum)")
	cmd.Flags().StringVar(&comparePath, "compare", "", "compare the current eval against a baseline eval JSON (from eval --json); exit 2 on regressions")
	cmd.Flags().StringVar(&nowText, "now", "", "fixed clock for reproducible golden runs (RFC3339)")
	cmd.Flags().StringVar(&provider, "model", "none", "model provider: none|ollama|openai-compatible")
	cmd.Flags().StringVar(&modelURL, "model-url", "", "provider base URL (ollama default http://127.0.0.1:11434)")
	cmd.Flags().StringVar(&modelName, "model-name", "", "provider model identifier")
	cmd.Flags().DurationVar(&modelTimeout, "model-timeout", 60*time.Second, "per-request model timeout")
	cmd.Flags().Float64Var(&modelBudget, "model-budget-usd", 1.0, "per-run model budget in USD")
	cmd.Flags().IntVar(&modelRounds, "model-max-rounds", 3, "maximum model rounds including correction rounds")
	return cmd
}

// evalNewline is the ASCII line feed kept as a named value so the formatting
// code below stays free of escape sequences.
var evalNewline = string([]byte{10})

// parseEvalNow parses the optional RFC3339 --now clock. An empty value means
// "use the wall clock", matching the analyzer default.
func parseEvalNow(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("无效的 --now %q（需要 RFC3339，例如 2026-10-30T14:00:00Z）: %w", value, err)
	}
	return parsed.UTC(), nil
}

// evalModelConfig turns the eval model flags plus environment variables into
// the frozen model.Config. "none"/empty disables model mode. Credentials come
// only from KUBECTL_TRIAGE_MODEL_KEY (priority) or OPENAI_API_KEY (only needed
// by openai-compatible).
func evalModelConfig(provider, rawURL, name string, timeout time.Duration, budgetUSD float64, maxRounds int) model.Config {
	cfg := model.Config{
		Provider:  strings.ToLower(strings.TrimSpace(provider)),
		URL:       strings.TrimSpace(rawURL),
		ModelName: strings.TrimSpace(name),
		Timeout:   timeout,
		BudgetUSD: budgetUSD,
		MaxRounds: maxRounds,
	}
	if cfg.Provider == "" || cfg.Provider == "none" {
		cfg.Provider = ""
		return cfg
	}
	switch cfg.Provider {
	case "ollama":
		if cfg.URL == "" {
			cfg.URL = evalOllamaDefaultURL
		}
	case "openai-compatible":
		if cfg.ModelName == "" {
			cfg.ModelName = evalOpenAIDefaultModel
		}
	}
	cfg.APIKey = strings.TrimSpace(os.Getenv(evalModelKeyEnv))
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv(evalOpenAIKeyEnv))
	}
	return cfg
}

// validateEvalModelConfig fails fast on configuration mistakes (exit code 1)
// before any case runs; runtime model failures are recorded as degraded cases.
func validateEvalModelConfig(cfg model.Config) error {
	if cfg.Provider == "" {
		return nil
	}
	err := model.ValidateConfig(cfg)
	if err == nil {
		return nil
	}
	var configErr *model.ConfigError
	if errors.As(err, &configErr) {
		return fmt.Errorf("模型配置错误: %w", err)
	}
	// An unexpected validation failure is a provider bug, not user input: let
	// the cases run degrade to offline rules instead of reporting exit code 1.
	return nil
}

// runEvalSetValidation implements --validate: it only validates the evaluation
// set (schemas, unique case names, credential patterns, --min-cases), never runs
// the analyzer and never reaches the network. --now and the model flags are
// accepted but ignored in this mode. Exit codes: 0 valid and above the minimum,
// 1 input/configuration error, 2 invalid cases or too few cases.
func runEvalSetValidation(cmd *cobra.Command, casesDir string, jsonOutput bool, filter string, maxCases, minCases int) error {
	validation, err := eval.ValidateSet(eval.Config{
		CasesDir: casesDir,
		MaxCases: maxCases,
		Filter:   filter,
		MinCases: minCases,
	})
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if jsonOutput {
		data, err := json.MarshalIndent(validation, "", "  ")
		if err != nil {
			return &ExitError{Code: 1, Err: fmt.Errorf("编码评测集校验结果失败: %w", err)}
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	} else {
		writeEvalSetText(cmd.OutOrStdout(), casesDir, validation)
	}
	if validation.Summary.Invalid > 0 {
		return &ExitError{Code: 2, Err: fmt.Errorf("eval: %d/%d 个用例未通过集合校验",
			validation.Summary.Invalid, validation.Summary.Total)}
	}
	if validation.Summary.MinCasesRequired > 0 && validation.Summary.Total < validation.Summary.MinCasesRequired {
		return &ExitError{Code: 2, Err: fmt.Errorf("eval: 用例数 %d 少于 --min-cases 要求的 %d",
			validation.Summary.Total, validation.Summary.MinCasesRequired)}
	}
	return nil
}

// writeEvalSetText renders the human-readable Chinese set-validation summary.
func writeEvalSetText(w io.Writer, casesDir string, validation *eval.SetValidation) {
	fmt.Fprintf(w, "评测集校验：%s", casesDir)
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	for _, c := range validation.Cases {
		if c.Valid {
			fmt.Fprintf(w, "[有效] %s", c.Name)
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "[无效] %s", c.Name)
		fmt.Fprintln(w)
		for _, problem := range c.Errors {
			fmt.Fprintf(w, "  - %s", strings.ReplaceAll(problem, evalNewline, "；"))
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "摘要：共 %d 个用例；有效 %d，无效 %d；最低数量要求 %d",
		validation.Summary.Total, validation.Summary.Valid,
		validation.Summary.Invalid, validation.Summary.MinCasesRequired)
	if validation.Summary.MinCasesRequired > 0 {
		if validation.Summary.Total >= validation.Summary.MinCasesRequired {
			fmt.Fprint(w, "（数量达标）")
		} else {
			fmt.Fprint(w, "（数量不足）")
		}
	}
	fmt.Fprintln(w)
}

// writeEvalCompareText renders the human-readable Chinese regression summary.
func writeEvalCompareText(w io.Writer, baselinePath string, comparison *eval.CompareResult) {
	fmt.Fprintf(w, "基线回归对比：%s", baselinePath)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "基线：通过 %d/%d；当前：通过 %d/%d",
		comparison.BaselineSummary.Pass, comparison.BaselineSummary.Total,
		comparison.CurrentSummary.Pass, comparison.CurrentSummary.Total)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "回归 %d，改进 %d，持平 %d，新增 %d，缺失 %d",
		comparison.Summary.Regressions, comparison.Summary.Improvements,
		comparison.Summary.Same, comparison.Summary.Added, comparison.Summary.Missing)
	fmt.Fprintln(w)
	for _, change := range comparison.Changes {
		switch change.Status {
		case eval.StatusRegressed:
			fmt.Fprintf(w, "[回归] %s：%s", change.Name, strings.Join(change.Fields, ", "))
			fmt.Fprintln(w)
		case eval.StatusImproved:
			fmt.Fprintf(w, "[改进] %s：%s", change.Name, strings.Join(change.Fields, ", "))
			fmt.Fprintln(w)
		case eval.StatusMissing:
			fmt.Fprintf(w, "[缺失] %s：基线存在但当前集合没有该用例", change.Name)
			fmt.Fprintln(w)
		}
	}
	if comparison.HasRegressions() {
		fmt.Fprintln(w, "回归警告：当前结果低于基线（pass/Top1/Top3/注入安全/降级/错误状态存在回退），质量门禁不通过；请修复回归，或在人工复核后更新基线。")
	} else {
		fmt.Fprintln(w, "无回归：当前结果不低于基线。")
	}
}

// writeEvalText renders the human-readable Chinese table and summary.
func writeEvalText(w io.Writer, result *eval.Result) {
	format := "%-20s  %-6s  %-5s  %-5s  %-8s  %-8s  %-6s"
	line := func(args ...any) {
		fmt.Fprintf(w, format, args...)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "本地评测（确定性离线规则）")
	fmt.Fprintln(w)
	line("用例", "结果", "Top1", "Top3", "缺失处理", "注入安全", "降级")
	for _, c := range result.Cases {
		line(c.Name, passLabel(c.Pass), yesNo(c.Top1), yesNo(c.Top3), yesNo(c.MissingOK), yesNo(c.InjectionSafe), yesNo(c.Degraded))
	}
	fmt.Fprintln(w)
	for _, c := range result.Cases {
		if c.Pass {
			continue
		}
		fmt.Fprintf(w, "失败 %s：%s", c.Name, evalFailureReason(c))
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "摘要：通过 %d/%d；Top1 %d，Top3 %d，缺失处理 %d，注入安全 %d，降级 %d，错误 %d",
		result.Summary.Pass, result.Summary.Total,
		result.Summary.Top1, result.Summary.Top3,
		result.Summary.MissingOK, result.Summary.InjectionSafe,
		result.Summary.Degraded, result.Summary.Errors)
	fmt.Fprintln(w)
}

// evalFailureReason explains the failed checks of one case in Chinese.
func evalFailureReason(c eval.CaseResult) string {
	if c.Error != "" {
		return strings.ReplaceAll(c.Error, evalNewline, "；")
	}
	reasons := make([]string, 0, 4)
	if !c.InjectionSafe {
		reasons = append(reasons, "报告包含外传链接或可执行命令")
	}
	if c.Degraded {
		reasons = append(reasons, "报告已降级")
	}
	if !c.MissingOK {
		reasons = append(reasons, "缺失证据处理不符合预期")
	} else if !c.Top1 {
		reasons = append(reasons, "Top1 未命中")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "用例未通过")
	}
	return strings.Join(reasons, "；")
}

func passLabel(pass bool) string {
	if pass {
		return "通过"
	}
	return "失败"
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}
