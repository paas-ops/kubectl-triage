package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/push"
	"github.com/spf13/cobra"
)

// newPushCmd wires the optional enterprise push command (ADR-006). It is the
// only command besides the model provider that may make outbound requests, and
// it never runs without an explicit endpoint and environment token.
func newPushCmd() *cobra.Command {
	var (
		endpoint        string
		includeEvidence bool
		maxBundleBytes  int64
		timeout         time.Duration
		dryRun          bool
		out             string
	)
	cmd := &cobra.Command{
		Use:   "push <dir>",
		Short: "Push a redacted report to an enterprise control plane (explicit opt-in)",
		Long: "push uploads <dir>/report.json and hash-only evidence references to the enterprise control plane.\n" +
			"It is the only OSS command that uploads incident data and only runs when explicitly configured:\n" +
			"the endpoint comes from --endpoint or KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT; the token only from\n" +
			"KUBECTL_TRIAGE_ENTERPRISE_TOKEN (there is no token flag and it is never printed). https is required\n" +
			"except for loopback http (127.0.0.1 / localhost / ::1). --include-evidence additionally uploads the\n" +
			"redacted evidence bundle as deterministic tar.gz. Exit codes: 0 accepted, 1 config or 4xx rejection,\n" +
			"3 network/timeout/5xx after retries, 4 sensitive content blocked before sending.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			resolvedEndpoint := strings.TrimSpace(endpoint)
			if resolvedEndpoint == "" {
				resolvedEndpoint = strings.TrimSpace(os.Getenv(push.EndpointEnv))
			}
			cfg := push.Config{
				Endpoint:        resolvedEndpoint,
				Token:           strings.TrimSpace(os.Getenv(push.TokenEnv)),
				Timeout:         timeout,
				IncludeEvidence: includeEvidence,
				MaxBundleBytes:  maxBundleBytes,
			}
			if err := cfg.Validate(); err != nil {
				return pushExitError(err)
			}

			if dryRun {
				payload, summary, err := push.Build(dir, cfg)
				if err != nil {
					return pushExitError(err)
				}
				if out != "" {
					if err := writePayloadFile(out, payload); err != nil {
						return &ExitError{Code: 1, Err: err}
					}
				}
				printPushSummary(cmd, "推送摘要（dry-run，未联网）", cfg, summary)
				if out != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "- payload 文件：%s\n", out)
				}
				return nil
			}

			// --out with a real push records the exact payload that is sent,
			// including the push_id used for Idempotency-Key.
			if out != "" {
				payload, _, err := push.Build(dir, cfg)
				if err != nil {
					return pushExitError(err)
				}
				if err := writePayloadFile(out, payload); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				result, err := push.Send(ctx, cfg, payload)
				if err != nil {
					return pushExitError(err)
				}
				printPushResult(cmd, result)
				return nil
			}

			result, err := push.Run(cmd.Context(), dir, cfg)
			if err != nil {
				return pushExitError(err)
			}
			printPushResult(cmd, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "enterprise endpoint URL (or KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT); https unless loopback")
	cmd.Flags().BoolVar(&includeEvidence, "include-evidence", false, "also upload the redacted evidence bundle as tar.gz (default: report + evidence hashes only)")
	cmd.Flags().Int64Var(&maxBundleBytes, "max-bundle-bytes", push.DefaultMaxBundleBytes, "maximum gzipped evidence bundle size in bytes")
	cmd.Flags().DurationVar(&timeout, "timeout", push.DefaultTimeout, "overall push timeout including retries")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "build and summarize the payload without any network request")
	cmd.Flags().StringVar(&out, "out", "", "write the payload JSON to PATH for audit (never contains the token)")
	return cmd
}

func writePayloadFile(path string, payload *push.Payload) error {
	data, err := payload.JSON()
	if err != nil {
		return fmt.Errorf("生成 payload JSON 失败：%w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入 --out 文件失败：%w", err)
	}
	return nil
}

func printPushSummary(cmd *cobra.Command, title string, cfg push.Config, result *push.Result) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, title+"：")
	fmt.Fprintf(out, "- 端点：%s\n", cfg.Endpoint)
	fmt.Fprintf(out, "- 协议版本：%s\n", result.ProtocolVersion)
	fmt.Fprintf(out, "- push_id：%s\n", result.PushID)
	fmt.Fprintf(out, "- created_at：%s\n", result.CreatedAt)
	fmt.Fprintf(out, "- 报告 sha256：%s\n", result.ReportSHA256)
	fmt.Fprintf(out, "- 证据条数：%d\n", result.EvidenceCount)
	if result.BundleIncluded {
		fmt.Fprintf(out, "- 证据包：包含（%d 字节，application/gzip）\n", result.BundleSize)
	} else {
		fmt.Fprintln(out, "- 证据包：未包含（默认仅发送报告与证据哈希）")
	}
}

func printPushResult(cmd *cobra.Command, result *push.Result) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "推送成功：")
	fmt.Fprintf(out, "- incident_id：%s\n", result.IncidentID)
	fmt.Fprintf(out, "- protocol_version：%s\n", result.ProtocolVersion)
	fmt.Fprintf(out, "- push_id：%s\n", result.PushID)
	fmt.Fprintf(out, "- 报告 sha256：%s\n", result.ReportSHA256)
	fmt.Fprintf(out, "- 证据条数：%d\n", result.EvidenceCount)
	if result.BundleIncluded {
		fmt.Fprintf(out, "- 证据包：已上传（%d 字节）\n", result.BundleSize)
	}
}

// pushExitError maps push errors to the frozen exit code table: security 4,
// non-retryable rejection or configuration 1, network/timeout/5xx 3.
func pushExitError(err error) error {
	var configErr *push.ConfigError
	if errors.As(err, &configErr) {
		return &ExitError{Code: 1, Err: configErr}
	}
	var securityErr *push.SecurityError
	if errors.As(err, &securityErr) {
		return &ExitError{Code: 4, Err: securityErr}
	}
	var remoteErr *push.RemoteError
	if errors.As(err, &remoteErr) {
		return &ExitError{Code: 1, Err: remoteErr}
	}
	var networkErr *push.NetworkError
	if errors.As(err, &networkErr) {
		return &ExitError{Code: 3, Err: networkErr}
	}
	return &ExitError{Code: 1, Err: err}
}
