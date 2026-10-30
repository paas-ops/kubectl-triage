package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
	"github.com/spf13/cobra"
)

// newReportCmd re-renders an existing report.json without re-running analysis.
func newReportCmd() *cobra.Command {
	var (
		format string
		out    string
	)
	cmd := &cobra.Command{
		Use:   "report <dir>",
		Short: "Re-render report.md/report.json from an existing report (no analysis)",
		Long: "report reads <dir>/report.json, validates it against the public report schema and re-renders the\n" +
			"requested formats without re-running analysis. --out accepts either a directory (report.json/report.md\n" +
			"are written inside it) or a single file path ending in .json/.md (only when --format selects a single\n" +
			"format); it defaults to the bundle directory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := validateFormat(format); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			jsonPath := filepath.Join(dir, "report.json")
			data, err := os.ReadFile(jsonPath)
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("read %s: %w", jsonPath, err)}
			}
			if err := schema.ValidateReport(data); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("%s does not match the report schema: %w", jsonPath, err)}
			}
			rep, err := report.Load(jsonPath)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			written, err := writeReport(rep, dir, format, out)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "report: %s\n", strings.Join(written, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "both", "md|json|both")
	cmd.Flags().StringVar(&out, "out", "", "output directory, or a single .json/.md file with --format json|md")
	return cmd
}

// normalizeReport makes nil slices encode as empty JSON arrays. The report
// schema requires arrays for evidence, hypotheses, contradictions, evidence_ids
// and the other list fields, so encoding them as null would be a contract break.
func normalizeReport(rep *report.Report) {
	if rep.Evidence == nil {
		rep.Evidence = []report.Evidence{}
	}
	if rep.Hypotheses == nil {
		rep.Hypotheses = []report.Hypothesis{}
	}
	if rep.MissingData == nil {
		rep.MissingData = []report.MissingData{}
	}
	if rep.RecommendedChecks == nil {
		rep.RecommendedChecks = []string{}
	}
	if rep.Limitations == nil {
		rep.Limitations = []string{}
	}
	if rep.Versions == nil {
		rep.Versions = map[string]string{}
	}
	for i := range rep.Hypotheses {
		if rep.Hypotheses[i].EvidenceIDs == nil {
			rep.Hypotheses[i].EvidenceIDs = []string{}
		}
		if rep.Hypotheses[i].Contradictions == nil {
			rep.Hypotheses[i].Contradictions = []report.Contradiction{}
		}
	}
}

// writeReport renders rep according to format and returns the written paths.
func writeReport(rep *report.Report, bundleDir, format, out string) ([]string, error) {
	jsonPath, mdPath, err := resolveReportPaths(bundleDir, format, out)
	if err != nil {
		return nil, err
	}
	var written []string
	if jsonPath != "" {
		if err := rep.WriteJSON(jsonPath); err != nil {
			return written, fmt.Errorf("write %s: %w", jsonPath, err)
		}
		written = append(written, jsonPath)
	}
	if mdPath != "" {
		if err := rep.WriteMarkdown(mdPath); err != nil {
			return written, fmt.Errorf("write %s: %w", mdPath, err)
		}
		written = append(written, mdPath)
	}
	return written, nil
}

// resolveReportPaths implements the documented --out semantics:
//   - empty: write into the bundle directory;
//   - an existing directory (or a path without a .json/.md extension): write
//     report.json/report.md inside it;
//   - a .json/.md file path: only valid with a matching single --format.
func resolveReportPaths(bundleDir, format, out string) (jsonPath, mdPath string, err error) {
	needsJSON := format == "json" || format == "both"
	needsMD := format == "md" || format == "both"

	target := strings.TrimSpace(out)
	if target == "" {
		if needsJSON {
			jsonPath = filepath.Join(bundleDir, "report.json")
		}
		if needsMD {
			mdPath = filepath.Join(bundleDir, "report.md")
		}
		return jsonPath, mdPath, nil
	}

	ext := strings.ToLower(filepath.Ext(target))
	isFile := ext == ".json" || ext == ".md"
	if info, statErr := os.Stat(target); statErr == nil {
		if info.IsDir() {
			isFile = false
		} else if !isFile {
			return "", "", fmt.Errorf("--out %q is neither a directory nor a .json/.md file", out)
		}
	}

	if !isFile {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", "", fmt.Errorf("create output directory %s: %w", target, err)
		}
		if needsJSON {
			jsonPath = filepath.Join(target, "report.json")
		}
		if needsMD {
			mdPath = filepath.Join(target, "report.md")
		}
		return jsonPath, mdPath, nil
	}

	if format == "both" {
		return "", "", fmt.Errorf("--out %q names a file but --format=both needs a directory", out)
	}
	if format == "md" && ext != ".md" {
		return "", "", fmt.Errorf("--out %q must end in .md for --format=md", out)
	}
	if format == "json" && ext != ".json" {
		return "", "", fmt.Errorf("--out %q must end in .json for --format=json", out)
	}
	if parent := filepath.Dir(target); parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", "", fmt.Errorf("create output directory %s: %w", parent, err)
		}
	}
	if needsJSON {
		jsonPath = target
	} else {
		mdPath = target
	}
	return jsonPath, mdPath, nil
}
