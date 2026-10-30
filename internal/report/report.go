// Package report defines the stable JSON report contract and Markdown renderer.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bavocado/kubectl-triage/internal/bundle"
)

const SchemaVersion = "1.0"

type Evidence struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	ObservedAt  string `json:"observed_at"`
	CollectedAt string `json:"collected_at"`
	Summary     string `json:"summary"`
	Ref         string `json:"ref"`
	SHA256      string `json:"sha256"`
}

type Hypothesis struct {
	Rank           int             `json:"rank"`
	Statement      string          `json:"statement"`
	Confidence     string          `json:"confidence"`
	EvidenceIDs    []string        `json:"evidence_ids"`
	Contradictions []Contradiction `json:"contradictions"`
}

type Contradiction struct {
	EvidenceID string `json:"evidence_id"`
	Note       string `json:"note"`
}

type MissingData struct {
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	NextStep string `json:"next_step"`
}

type Report struct {
	SchemaVersion     string            `json:"schema_version"`
	Incident          bundle.Incident   `json:"incident"`
	GeneratedAt       string            `json:"generated_at"`
	Mode              string            `json:"mode"`
	Degraded          bool              `json:"degraded"`
	Summary           string            `json:"summary"`
	Evidence          []Evidence        `json:"evidence"`
	Hypotheses        []Hypothesis      `json:"hypotheses"`
	MissingData       []MissingData     `json:"missing_data"`
	RecommendedChecks []string          `json:"recommended_checks"`
	SuggestedFix      string            `json:"suggested_fix"`
	Limitations       []string          `json:"limitations"`
	Versions          map[string]string `json:"versions"`
}

func (r *Report) Write(dir string) error {
	if err := r.WriteJSON(filepath.Join(dir, "report.json")); err != nil {
		return err
	}
	return r.WriteMarkdown(filepath.Join(dir, "report.md"))
}

func (r *Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (r *Report) WriteMarkdown(path string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 事故诊断：%s\n\n", r.Incident.Alertname)
	fmt.Fprintf(&b, "- 事故：`%s`\n", r.Incident.ID)
	fmt.Fprintf(&b, "- 集群 / 命名空间：`%s` / `%s`\n", r.Incident.Cluster, r.Incident.Namespace)
	fmt.Fprintf(&b, "- 工作负载：`%s`\n", r.Incident.Workload)
	fmt.Fprintf(&b, "- 生成时间：%s\n", r.GeneratedAt)
	fmt.Fprintf(&b, "- 模式：%s\n\n", r.Mode)
	fmt.Fprintf(&b, "## 摘要\n\n%s\n\n", r.Summary)

	fmt.Fprintf(&b, "## 证据时间线\n\n| ID | 来源 | 观察时间 | 摘要 |\n| --- | --- | --- | --- |\n")
	for _, e := range r.Evidence {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", e.ID, e.Source, e.ObservedAt, escapeCell(e.Summary))
	}

	fmt.Fprintf(&b, "\n## 候选根因\n\n")
	if len(r.Hypotheses) == 0 {
		fmt.Fprintf(&b, "证据不足，未生成根因假设。\n")
	}
	for _, h := range r.Hypotheses {
		fmt.Fprintf(&b, "### %d. %s\n\n", h.Rank, h.Statement)
		fmt.Fprintf(&b, "- 置信：%s\n", h.Confidence)
		fmt.Fprintf(&b, "- 证据：%s\n", strings.Join(h.EvidenceIDs, ", "))
		for _, c := range h.Contradictions {
			fmt.Fprintf(&b, "- 反证：%s — %s\n", c.EvidenceID, c.Note)
		}
		b.WriteString("\n")
	}

	if len(r.MissingData) > 0 {
		fmt.Fprintf(&b, "## 缺失证据\n\n")
		for _, m := range r.MissingData {
			fmt.Fprintf(&b, "- **%s**：%s；下一步：%s\n", m.Source, m.Reason, m.NextStep)
		}
		b.WriteString("\n")
	}
	if len(r.RecommendedChecks) > 0 {
		fmt.Fprintf(&b, "## 下一步检查\n\n")
		for _, c := range r.RecommendedChecks {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}
	if r.SuggestedFix != "" {
		fmt.Fprintf(&b, "## 修复建议（只读 · 不自动执行）\n\n%s\n\n", r.SuggestedFix)
	}
	if len(r.Limitations) > 0 {
		fmt.Fprintf(&b, "## 限制\n\n")
		for _, l := range r.Limitations {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

// New returns an empty report with stable defaults.
func New(inc bundle.Incident, cliVersion string) *Report {
	return &Report{
		SchemaVersion: SchemaVersion,
		Incident:      inc,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Mode:          "offline",
		Evidence:      []Evidence{},
		Hypotheses:    []Hypothesis{},
		MissingData:   []MissingData{},
		Versions:      map[string]string{"cli": cliVersion, "rules": "rule-v0.1", "model": "none"},
	}
}
