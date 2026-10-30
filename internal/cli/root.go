// Package cli wires commands to the bundle/analyzer/report packages.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/analyze"
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

func newAnalyzeCmd(version string) *cobra.Command {
	var (
		offline       bool
		model         string
		modelURL      string
		modelName     string
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
			"--offline is a hard no-network guarantee and conflicts with --model. --model values other than none\n" +
			"request model mode; this build has no model provider, so it writes a degraded offline report and exits 3.\n" +
			"--out accepts either a directory (report.json/report.md are written inside it) or a single file\n" +
			"path ending in .json/.md (only when --format selects a single format); it defaults to the bundle directory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			mode, err := redact.ParseMode(redactMode)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if err := validateFormat(format); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			model = strings.TrimSpace(model)
			modelRequested := model != "" && model != "none"
			if offline && modelRequested {
				return &ExitError{Code: 1, Err: fmt.Errorf("--offline and --model %q are mutually exclusive; use one of them", model)}
			}
			// --model-url/--model-name are accepted for forward compatibility;
			// M1 has no model provider so they do not affect the report.
			_ = modelURL
			_ = modelName

			b, err := bundle.Load(dir)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			b.RedactMode = mode
			if err := b.Validate(); err != nil {
				return &ExitError{Code: exitCodeFor(err), Err: err}
			}

			rep, err := analyze.Analyze(b, version, analyze.Options{MaxEvidence: maxEvidence})
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			rep.Mode = "offline"
			if modelRequested {
				rep.Degraded = true
				rep.Limitations = append(rep.Limitations, "模型模式未实现，已回退离线规则")
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

			// An explicit CI gate wins over the model-fallback signal when both
			// apply, so --fail-on-missing always reports 2.
			if failOnMissing && (len(rep.MissingData) > 0 || len(rep.Hypotheses) == 0) {
				return &ExitError{Code: 2, Err: fmt.Errorf("evidence is missing (%d missing item(s), %d hypothesis(es)); see %s",
					len(rep.MissingData), len(rep.Hypotheses), "report.json missing_data")}
			}
			if modelRequested {
				return &ExitError{Code: 3, Err: fmt.Errorf("model mode is unavailable in this build; wrote an offline report with degraded=true")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "hard no-network guarantee (mutually exclusive with --model)")
	cmd.Flags().StringVar(&model, "model", "none", "model provider: none|ollama|openai-compatible (M1 has no provider)")
	cmd.Flags().StringVar(&modelURL, "model-url", "", "OpenAI-compatible base URL (reserved for M2)")
	cmd.Flags().StringVar(&modelName, "model-name", "", "model name (reserved for M2)")
	cmd.Flags().StringVar(&format, "format", "both", "md|json|both")
	cmd.Flags().StringVar(&out, "out", "", "output directory, or a single .json/.md file with --format json|md")
	cmd.Flags().IntVar(&maxEvidence, "max-evidence", 200, "maximum evidence items passed to the analyzer")
	cmd.Flags().StringVar(&redactMode, "redact", "strict", "strict|standard|off: sensitive-content handling")
	cmd.Flags().BoolVar(&failOnMissing, "fail-on-missing", false, "exit 2 when evidence is missing or no hypothesis is produced")
	return cmd
}

func validateFormat(format string) error {
	switch format {
	case "md", "json", "both":
		return nil
	default:
		return fmt.Errorf("invalid --format %q (want md|json|both)", format)
	}
}
