// Package cli wires commands to the bundle/analyzer/report packages.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bavocado/kubectl-triage/internal/analyze"
	"github.com/bavocado/kubectl-triage/internal/bundle"
	"github.com/spf13/cobra"
)

// ExitError lets commands choose a stable process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

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
		newValidateCmd(),
		newAnalyzeCmd(version),
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
	return &cobra.Command{
		Use:   "validate <dir>",
		Short: "Validate an incident bundle (schema, timestamps, sensitive content)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			b, err := bundle.Load(dir)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if err := b.Validate(); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			files, _ := b.WalkTextFiles()
			fmt.Fprintf(cmd.OutOrStdout(), "OK: %s (%s/%s, %d evidence files)\n",
				b.Incident.ID, b.Incident.Cluster, b.Incident.Namespace, len(files))
			return nil
		},
	}
}

func newAnalyzeCmd(version string) *cobra.Command {
	var (
		offline       bool
		model         string
		format        string
		failOnMissing bool
	)
	cmd := &cobra.Command{
		Use:   "analyze <dir>",
		Short: "Analyze an incident bundle and write report.md/report.json",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if model != "" {
				return &ExitError{Code: 3, Err: errors.New("model mode is not implemented in v0.0.1; use --offline")}
			}
			switch format {
			case "md", "json", "both":
			default:
				return &ExitError{Code: 1, Err: fmt.Errorf("invalid --format %q (want md|json|both)", format)}
			}
			b, err := bundle.Load(args[0])
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if err := b.Validate(); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			rep, err := analyze.Analyze(b, version)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			rep.Mode = "offline"
			if !offline {
				rep.Degraded = true
				rep.Limitations = append(rep.Limitations, "请求了非离线模式，但 v0.0.1 仅实现规则分析，报告按降级处理。")
			}
			switch format {
			case "md":
				err = rep.WriteMarkdown(filepath.Join(args[0], "report.md"))
			case "json":
				err = rep.WriteJSON(filepath.Join(args[0], "report.json"))
			default:
				err = rep.Write(args[0])
			}
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "report: %s\nsummary: %s\n", filepath.Join(args[0], "report.md"), strings.TrimSpace(rep.Summary))
			if failOnMissing && len(rep.Hypotheses) == 0 {
				return &ExitError{Code: 2, Err: errors.New("no cited hypothesis; see report.json missing_data")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", true, "disable all network access")
	cmd.Flags().StringVar(&model, "model", "", "model provider (not implemented in v0.0.1)")
	cmd.Flags().StringVar(&format, "format", "both", "md|json|both")
	cmd.Flags().BoolVar(&failOnMissing, "fail-on-missing", false, "exit 2 when no hypothesis is produced")
	return cmd
}
