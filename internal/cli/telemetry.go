package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/internal/telemetry"
	"github.com/spf13/cobra"
)

// newTelemetryCmd wires the opt-in anonymous telemetry commands (ADR-005 D6).
// Telemetry is disabled by default: nothing is sent and no file is written
// until the operator explicitly enables it.
func newTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry <status|enable|disable>",
		Short: "Manage opt-in anonymous telemetry (disabled by default)",
		Args:  cobra.ArbitraryArgs,
		Long: "Telemetry is disabled by default. When enabled, one anonymous JSON event is\n" +
			"POSTed after each successful analyze; its payload is exactly:\n" +
			"install_id, event, version, os, arch, mode, fault_class, duration_ms,\n" +
			"evidence_count, outcome. Incident content, cluster/namespace/alert names,\n" +
			"logs, evidence bodies, file paths, and model keys are never collected.\n" +
			"The endpoint comes from KUBECTL_TRIAGE_TELEMETRY_URL; without it events are\n" +
			"dropped. KUBECTL_TRIAGE_TELEMETRY=off force-disables sending.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &ExitError{Code: 1, Err: fmt.Errorf("未知 telemetry 子命令 %q（可用：status|enable|disable）", args[0])}
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newTelemetryStatusCmd(), newTelemetryEnableCmd(), newTelemetryDisableCmd())
	return cmd
}

func newTelemetryStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether telemetry is enabled and where it would send",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := telemetry.Status()
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "匿名遥测：%s\n", telemetryStateLine(state))
			fmt.Fprintf(out, "端点：%s\n", telemetryEndpointLine(state))
			fmt.Fprintf(out, "配置文件：%s\n", state.Path)
			if state.Config.InstallID != "" {
				fmt.Fprintf(out, "安装 ID：%s…（仅显示前 8 位）\n", installIDPrefix(state.Config.InstallID))
			} else {
				fmt.Fprintln(out, "安装 ID：未生成（运行 kubectl-triage telemetry enable 生成）")
			}
			return nil
		},
	}
}

func newTelemetryEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable anonymous telemetry and generate an install ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := telemetry.Enable()
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "已启用匿名遥测。")
			fmt.Fprintf(out, "安装 ID：%s…（随机生成，仅显示前 8 位）\n", installIDPrefix(state.Config.InstallID))
			fmt.Fprintf(out, "配置文件：%s\n", state.Path)
			fmt.Fprintf(out, "端点：%s\n", telemetryEndpointLine(state))
			if state.Endpoint() == "" {
				fmt.Fprintln(out, "提示：未配置端点时事件会被丢弃；设置 KUBECTL_TRIAGE_TELEMETRY_URL 后开始发送。")
			}
			if state.EnvDisabled {
				fmt.Fprintln(out, "注意：当前环境变量 KUBECTL_TRIAGE_TELEMETRY=off，发送仍被强制关闭。")
			}
			fmt.Fprintln(out, "仅发送白名单字段（install_id, event, version, os, arch, mode, fault_class, duration_ms, evidence_count, outcome），不含事故内容。")
			return nil
		},
	}
}

func newTelemetryDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable anonymous telemetry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := telemetry.Disable()
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "已关闭匿名遥测，不再发送任何事件。")
			fmt.Fprintf(out, "配置文件：%s\n", state.Path)
			if state.Config.InstallID != "" {
				fmt.Fprintln(out, "安装 ID 已保留（再次开启时保持不变）；如需彻底删除，请手动删除配置文件。")
			}
			return nil
		},
	}
}

// telemetryStateLine renders whether telemetry is enabled, including the
// environment force-off override.
func telemetryStateLine(state telemetry.State) string {
	var line string
	switch {
	case state.Config.Enabled:
		line = "已开启"
	case !state.Exists:
		line = "已关闭（默认；从未开启）"
	default:
		line = "已关闭"
	}
	if state.EnvDisabled {
		line += "；环境变量 KUBECTL_TRIAGE_TELEMETRY=off 强制关闭，不会发送"
	}
	return line
}

// telemetryEndpointLine renders the effective endpoint or the drop warning.
func telemetryEndpointLine(state telemetry.State) string {
	endpoint := state.Endpoint()
	if endpoint == "" {
		return "未配置（事件将被丢弃）"
	}
	if strings.TrimSpace(os.Getenv(telemetry.EndpointEnv)) != "" {
		return endpoint + "（来自环境变量 " + telemetry.EndpointEnv + "）"
	}
	return endpoint
}

// installIDPrefix shows only the first 8 characters of the pseudonymous install
// ID so terminal output and screenshots cannot leak the whole identifier.
func installIDPrefix(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// recordAnalyzeTelemetry is the analyze instrumentation hook. It records one
// anonymous "analyze" event and, while telemetry has never been configured,
// prints the single non-interactive opt-in hint to stderr.
//
// Everything here is best-effort (ADR-005 D6): a corrupt config, missing
// endpoint, or failed send never changes the command's exit code, stdout, or
// the written report.
func recordAnalyzeTelemetry(cmd *cobra.Command, version string, startedAt time.Time, rep *report.Report) {
	if cmd == nil || rep == nil {
		return
	}
	cfg, exists, err := telemetry.Load()
	if err != nil {
		// A corrupt config must never block or fail an analysis.
		return
	}
	if !exists && !telemetry.DisabledByEnv() {
		fmt.Fprintln(cmd.ErrOrStderr(), telemetry.FirstRunHint)
	}
	outcome := "ok"
	if len(rep.Hypotheses) == 0 {
		outcome = "missing"
	}
	telemetry.Record(cfg, telemetry.Event{
		InstallID: cfg.InstallID,
		Event:     "analyze",
		Version:   version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Mode:      rep.Mode,
		// The frozen report contract has no stable fault_class field yet, so the
		// event keeps it empty rather than guessing from free-text statements.
		FaultClass:    "",
		DurationMS:    time.Since(startedAt).Milliseconds(),
		EvidenceCount: len(rep.Evidence),
		Outcome:       outcome,
	})
}
