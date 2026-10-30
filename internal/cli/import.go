package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/importers"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	// Importers register themselves; blank imports keep the supported sources
	// explicit in one place.
	_ "github.com/paas-ops/kubectl-triage/internal/importers/alertmanager"
	_ "github.com/paas-ops/kubectl-triage/internal/importers/kubectl"
	_ "github.com/paas-ops/kubectl-triage/internal/importers/loki"
	_ "github.com/paas-ops/kubectl-triage/internal/importers/prometheus"
	"github.com/spf13/cobra"
)

// maxImportBytes caps one import payload so a runaway export cannot exhaust
// memory. Larger exports should be trimmed to the incident window first.
const maxImportBytes = 64 << 20

func newImportCmd() *cobra.Command {
	var (
		from  string
		name  string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "import <source> <dir>",
		Short: "Import exported monitoring data into an incident bundle",
		Long: "import normalizes one exported monitoring payload into the standard incident bundle layout.\n" +
			"Sources: alertmanager, kubectl, prometheus, loki. The payload is read from --from (\"-\" means stdin)\n" +
			"and written below <dir>, which must already contain incident.yaml. Existing files are only\n" +
			"overwritten with --force, and imports never touch the network. Inputs are scanned for\n" +
			"credential-like content and rejected with exit code 4 before any file is written.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, dir := args[0], args[1]
			imp, ok := importers.Lookup(source)
			if !ok {
				return &ExitError{Code: 1, Err: fmt.Errorf("unknown import source %q (want %s)", source, strings.Join(importers.Names(), "|"))}
			}
			if name != "" && imp.DefaultName() == "" {
				return &ExitError{Code: 1, Err: fmt.Errorf("--name is not supported for source %q", source)}
			}
			// Validate the bundle before reading the payload: importing into an
			// arbitrary directory would create a tree that validate/analyze reject.
			if _, err := bundle.Load(dir); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			payload, err := readImportPayload(cmd, from)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			// Requirement §8: reject credential-like exports before anything is
			// written. redact.Scrub is the single source of truth for the patterns;
			// comparing the scrubbed bytes detects a match without ever exposing or
			// echoing the matched value. This runs for both stdin and --from inputs.
			if scrubbed := redact.Scrub(payload); !bytes.Equal(scrubbed, payload) {
				return &ExitError{Code: 4, Err: errors.New("input may contain credentials or secrets; redact the raw values before importing")}
			}
			out, err := imp.Import(importers.Input{Name: name, Data: payload})
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if len(out.Files) == 0 {
				return &ExitError{Code: 1, Err: fmt.Errorf("import %s: importer produced no files", source)}
			}
			if err := writeImportOutput(dir, out, force); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "imported %s into %s\n", source, dir)
			for _, file := range out.Files {
				fmt.Fprintf(cmd.OutOrStdout(), "  wrote %s (%d bytes)\n", file.Path, len(file.Data))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "next: kubectl-triage validate %s\n", dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "-", "input file to read, - for stdin")
	cmd.Flags().StringVar(&name, "name", "", "output name for prometheus/loki imports (e.g. memory)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return cmd
}

// readImportPayload reads the raw export from --from or stdin with a size cap.
func readImportPayload(cmd *cobra.Command, from string) ([]byte, error) {
	if from == "" || from == "-" {
		data, err := readImportLimited(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		if len(data) == 0 {
			return nil, errors.New("no input on stdin; pipe an export or use --from FILE")
		}
		return data, nil
	}
	file, err := os.Open(from)
	if err != nil {
		return nil, fmt.Errorf("read --from %s: %w", from, err)
	}
	defer file.Close()
	data, err := readImportLimited(file)
	if err != nil {
		return nil, fmt.Errorf("read --from %s: %w", from, err)
	}
	return data, nil
}

func readImportLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxImportBytes {
		return nil, fmt.Errorf("input exceeds %d MiB", maxImportBytes>>20)
	}
	return data, nil
}

// writeImportOutput pre-checks every target before writing anything so a
// conflicting file never leaves a half-imported bundle, then writes atomically.
func writeImportOutput(dir string, out *importers.Output, force bool) error {
	var conflicts []string
	for _, file := range out.Files {
		target := filepath.Join(dir, filepath.FromSlash(file.Path))
		if _, err := os.Stat(target); err == nil {
			conflicts = append(conflicts, file.Path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check %s: %w", file.Path, err)
		}
	}
	if len(conflicts) > 0 && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite", strings.Join(conflicts, ", "))
	}
	for _, file := range out.Files {
		target := filepath.Join(dir, filepath.FromSlash(file.Path))
		if err := writeFileAtomic(target, file.Data); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".import-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
