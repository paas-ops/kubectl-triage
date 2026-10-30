// Package cli wires commands to the bundle/analyzer/report packages.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze"
	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
	"github.com/spf13/cobra"
)

// ExitError lets commands choose a stable process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// exitCodeFor maps bundle validation failures to the documented exit codes:
// 4 for security failures, 1 for every other input/configuration error.
func exitCodeFor(err error) int {
	var security *bundle.SecurityError
	if errors.As(err, &security) {
		return 4
	}
	return 1
}

func NewRoot(version, commit, date string) *cobra.Command {
	root := &cobra.Command{
		Use:           "kubectl-triage",
		Short:         "Lightweight offline Kubernetes incident triage",
		Long:          "kubectl-triage analyzes a local, redacted incident bundle and produces an evidence-linked report.\nIt never writes to a cluster and does not upload evidence.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(version, commit, date),
		newInitCmd(),
		newImportCmd(),
		newValidateCmd(),
		newAnalyzeCmd(version),
		newReportCmd(),
		newEvalCmd(),
		newTelemetryCmd(),
	)
	return root
}

func Execute(version, commit, date string) int {
	if err := NewRoot(version, commit, date).Execute(); err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, "error:", exit.Err)
			return exit.Code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "kubectl-triage %s (commit %s, built %s)\n", version, commit, date)
		},
	}
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <dir>",
		Short: "Create a minimal incident bundle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := bundle.Scaffold(dir); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			abs, _ := filepath.Abs(dir)
			fmt.Fprintf(cmd.OutOrStdout(), "created incident bundle: %s\n", abs)
			fmt.Fprintln(cmd.OutOrStdout(), "next: edit incident.yaml, add redacted evidence, then run `kubectl-triage validate .`")
			return nil
		},
	}
}

func newValidateCmd() *cobra.Command {
	var redactMode string
	cmd := &cobra.Command{
		Use:   "validate <dir>",
		Short: "Validate an incident bundle (schema, timestamps, sensitive content)",
		Long: "validate checks incident.yaml against the public schema, verifies timestamps and scans evidence files\n" +
			"for credential-like content. --redact strict (default) rejects the bundle with exit code 4 when anything\n" +
			"is found; --redact standard accepts it and scrubs matches before analysis; --redact off disables checking.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := redact.ParseMode(redactMode)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			b, err := bundle.Load(args[0])
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			b.RedactMode = mode
			if err := b.Validate(); err != nil {
				return &ExitError{Code: exitCodeFor(err), Err: err}
			}
			files, _ := b.WalkTextFiles()
			fmt.Fprintf(cmd.OutOrStdout(), "OK: %s (%s/%s, %d evidence files)\n",
				b.Incident.ID, b.Incident.Cluster, b.Incident.Namespace, len(files))
			if findings := b.Findings(); len(findings) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: %d sensitive-content finding(s) will be scrubbed before analysis (--redact=%s)\n",
					len(findings), mode)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&redactMode, "redact", "strict", "strict|standard|off: sensitive-content handling")
	return cmd
}

// modelFlags is the raw CLI input for the optional model provider (ADR-004).
type modelFlags struct {
	provider  string
	url       string
	name      string
	timeout   time.Duration
	budgetUSD float64
	maxRounds int
}

func newAnalyzeCmd(version string) *cobra.Command {
	var (
		offline       bool
		modelInput    modelFlags
		format        string
		out           string
		maxEvidence   int
		redactMode    string
		failOnMissing bool
	)
	cmd := &cobra.Command{
		Use:   "analyze <dir>",
		Short: "Analyze an incident bundle and write report.md/report.json",
		Long: "analyze runs the deterministic offline rules over a validated incident bundle and writes the report.\n" +
			"By default it never touches the network. Model mode is opt-in: --model openai-compatible requires\n" +
			"--model-url and KUBECTL_TRIAGE_MODEL_KEY (or OPENAI_API_KEY); --model ollama defaults to\n" +
			"http://127.0.0.1:11434. Model runtime failures keep the offline report and exit 3; configuration\n" +
			"errors exit 1. --offline is a hard no-network guarantee and conflicts with a non-none --model.\n" +
			"--out accepts either a directory (report.json/report.md are written inside it) or a single file\n" +
			"path ending in .json/.md (only when --format selects a single format); it defaults to the bundle directory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			startedAt := time.Now()
			dir := args[0]
			mode, err := redact.ParseMode(redactMode)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if err := validateFormat(format); err != nil {
				return &ExitError{Code: 1, Err: err}
			}

			// A hard offline run must win over any model configuration, so the
			// mutual-exclusion check comes before provider validation.
			modelRequested := isModelRequested(modelInput.provider)
			if offline && modelRequested {
				return &ExitError{Code: 1, Err: fmt.Errorf("--offline and --model %q are mutually exclusive; use one of them", strings.TrimSpace(modelInput.provider))}
			}

			var modelCfg model.Config
			if modelRequested {
				cfg, cfgErr := resolveModelConfig(modelInput)
				if cfgErr != nil {
					return &ExitError{Code: 1, Err: cfgErr}
				}
				modelCfg = cfg
			}

			b, err := bundle.Load(dir)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			b.RedactMode = mode
			if err := b.Validate(); err != nil {
				return &ExitError{Code: exitCodeFor(err), Err: err}
			}

			rep, err := analyze.Analyze(b, version, analyze.Options{
				MaxEvidence: maxEvidence,
				Model:       modelCfg,
			})
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			normalizeReport(rep)

			data, err := json.Marshal(rep)
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("encode report: %w", err)}
			}
			if err := schema.ValidateReport(data); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("generated report failed schema validation: %w", err)}
			}

			written, err := writeReport(rep, dir, format, out)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "report: %s\n", strings.Join(written, ", "))
			if summary := strings.TrimSpace(rep.Summary); summary != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "summary: %s\n", summary)
			}

			// Opt-in telemetry is strictly best-effort: it never changes the exit
			// code, stdout, or the written report (ADR-005 D6).
			recordAnalyzeTelemetry(cmd, version, startedAt, rep)

			// An explicit CI gate wins over the model-fallback signal when both
			// apply, so --fail-on-missing always reports 2 (ADR-004 D3/D8).
			if failOnMissing && (len(rep.MissingData) > 0 || len(rep.Hypotheses) == 0) {
				return &ExitError{Code: 2, Err: fmt.Errorf("evidence is missing (%d missing item(s), %d hypothesis(es)); see report.json missing_data",
					len(rep.MissingData), len(rep.Hypotheses))}
			}
			if modelRequested && rep.Degraded {
				return &ExitError{Code: 3, Err: fmt.Errorf("model mode degraded to offline rules; report written with degraded=true and versions.model=none (%s)",
					degradationReason(rep.Limitations))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "hard no-network guarantee (mutually exclusive with --model)")
	cmd.Flags().StringVar(&modelInput.provider, "model", "none", "model provider: none|ollama|openai-compatible")
	cmd.Flags().StringVar(&modelInput.url, "model-url", "", "provider base URL (default http://127.0.0.1:11434 for ollama; required for openai-compatible)")
	cmd.Flags().StringVar(&modelInput.name, "model-name", "", "model name (default gpt-4o-mini for openai-compatible; required for ollama)")
	cmd.Flags().DurationVar(&modelInput.timeout, "model-timeout", 60*time.Second, "per-run model timeout")
	cmd.Flags().Float64Var(&modelInput.budgetUSD, "model-budget-usd", 1.0, "per-run model budget in USD")
	cmd.Flags().IntVar(&modelInput.maxRounds, "model-max-rounds", 3, "maximum model rounds including correction rounds")
	cmd.Flags().StringVar(&format, "format", "both", "md|json|both")
	cmd.Flags().StringVar(&out, "out", "", "output directory, or a single .json/.md file with --format json|md")
	cmd.Flags().IntVar(&maxEvidence, "max-evidence", 200, "maximum evidence items passed to the analyzer")
	cmd.Flags().StringVar(&redactMode, "redact", "strict", "strict|standard|off: sensitive-content handling")
	cmd.Flags().BoolVar(&failOnMissing, "fail-on-missing", false, "exit 2 when evidence is missing or no hypothesis is produced")
	return cmd
}

// isModelRequested reports whether --model asks for a model provider.
func isModelRequested(provider string) bool {
	provider = strings.TrimSpace(provider)
	return provider != "" && !strings.EqualFold(provider, "none")
}

// resolveModelConfig fills provider defaults and loads the API key from the
// environment. Configuration errors are returned as *model.ConfigError so the
// CLI can map them to exit code 1; it never touches the network.
func resolveModelConfig(in modelFlags) (model.Config, error) {
	// Provider names are lower-cased so that --model OLLAMA gets the same
	// defaults as --model ollama; model.ValidateConfig applies the same rule.
	provider := strings.ToLower(strings.TrimSpace(in.provider))
	cfg := model.Config{
		Provider:  provider,
		URL:       strings.TrimSpace(in.url),
		ModelName: strings.TrimSpace(in.name),
		Timeout:   in.timeout,
		BudgetUSD: in.budgetUSD,
		MaxRounds: in.maxRounds,
	}
	switch provider {
	case "ollama":
		if cfg.URL == "" {
			cfg.URL = "http://127.0.0.1:11434"
		}
	case "openai-compatible":
		if cfg.ModelName == "" {
			cfg.ModelName = "gpt-4o-mini"
		}
	}
	// API keys come from the environment only and are never echoed by the CLI,
	// reports, or error messages (ADR-004 D2).
	cfg.APIKey = strings.TrimSpace(os.Getenv("KUBECTL_TRIAGE_MODEL_KEY"))
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}

	if err := model.ValidateConfig(cfg); err != nil {
		var configErr *model.ConfigError
		if errors.As(err, &configErr) {
			return model.Config{}, fmt.Errorf("invalid model configuration: %w", err)
		}
		// Unexpected validation failure (for example a provider implementation
		// bug): keep the offline report and let Analyze degrade with exit code 3
		// instead of presenting it as a user configuration error.
		return cfg, nil
	}
	return cfg, nil
}

// degradationReason returns the most relevant limitation added by the model
// fallback so the exit-3 error stays actionable.
func degradationReason(limitations []string) string {
	for i := len(limitations) - 1; i >= 0; i-- {
		if strings.Contains(limitations[i], "模型不可用，已回退离线规则") {
			return limitations[i]
		}
	}
	return "model provider unavailable"
}

func validateFormat(format string) error {
	switch format {
	case "md", "json", "both":
		return nil
	default:
		return fmt.Errorf("invalid --format %q (want md|json|both)", format)
	}
}
