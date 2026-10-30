// Package report defines the stable JSON report contract and Markdown renderer.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/evidence"
)

// SchemaVersion is the frozen report.json schema version.
const SchemaVersion = "1.0"

// newline avoids escaped line breaks so the renderer stays easy to audit.
var newline = string([]byte{10})

// Evidence is one cited evidence entry (frozen report.schema.json shape).
type Evidence struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	ObservedAt  string `json:"observed_at"`
	CollectedAt string `json:"collected_at"`
	Summary     string `json:"summary"`
	ContentRef  string `json:"content_ref"`
	Query       string `json:"query,omitempty"`
	Redaction   string `json:"redaction,omitempty"`
	SHA256      string `json:"sha256"`
	Stale       bool   `json:"stale"`
	SkewSeconds int    `json:"skew_seconds,omitempty"`
}

// Hypothesis is one ranked root-cause candidate.
type Hypothesis struct {
	Rank           int             `json:"rank"`
	Statement      string          `json:"statement"`
	Confidence     string          `json:"confidence"`
	EvidenceIDs    []string        `json:"evidence_ids"`
	Contradictions []Contradiction `json:"contradictions"`
}

// MarshalJSON keeps empty contradictions/evidence_ids as arrays, never null:
// the frozen schema requires arrays and downstream consumers rely on them.
func (h Hypothesis) MarshalJSON() ([]byte, error) {
	type alias Hypothesis
	a := alias(h)
	if a.EvidenceIDs == nil {
		a.EvidenceIDs = []string{}
	}
	if a.Contradictions == nil {
		a.Contradictions = []Contradiction{}
	}
	return json.Marshal(a)
}

// Contradiction is counter-evidence attached to a hypothesis.
type Contradiction struct {
	EvidenceID string `json:"evidence_id"`
	Note       string `json:"note"`
}

// MissingData explains a missing evidence source and the next step to fill it.
type MissingData struct {
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	NextStep string `json:"next_step"`
}

// Report is the stable machine-readable diagnosis output.
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

// MarshalJSON normalizes nil slices to empty arrays so that even loaded or
// hand-built reports stay schema-valid.
func (r Report) MarshalJSON() ([]byte, error) {
	type alias Report
	a := alias(r)
	if a.Evidence == nil {
		a.Evidence = []Evidence{}
	}
	if a.Hypotheses == nil {
		a.Hypotheses = []Hypothesis{}
	}
	if a.MissingData == nil {
		a.MissingData = []MissingData{}
	}
	if a.RecommendedChecks == nil {
		a.RecommendedChecks = []string{}
	}
	if a.Limitations == nil {
		a.Limitations = []string{}
	}
	if a.Versions == nil {
		a.Versions = map[string]string{}
	}
	return json.Marshal(a)
}

// New returns an empty report with stable defaults.
func New(inc bundle.Incident, cliVersion string) *Report {
	return &Report{
		SchemaVersion:     SchemaVersion,
		Incident:          inc,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Mode:              "offline",
		Evidence:          []Evidence{},
		Hypotheses:        []Hypothesis{},
		MissingData:       []MissingData{},
		RecommendedChecks: []string{},
		Limitations:       []string{},
		// Keep the rules version in sync with analyze/rule.Version; the analyzer
		// overwrites it explicitly, this default keeps hand-built reports honest.
		Versions: map[string]string{"cli": cliVersion, "rules": "rule-v0.2", "model": "none"},
	}
}

// Write renders report.json and report.md into dir.
func (r *Report) Write(dir string) error {
	if err := r.WriteJSON(filepath.Join(dir, "report.json")); err != nil {
		return err
	}
	return r.WriteMarkdown(filepath.Join(dir, "report.md"))
}

// WriteJSON writes an indented, schema-shaped report.json.
func (r *Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, byte(10)), 0o644)
}

// WriteMarkdown renders the human-readable report derived from the JSON model.
func (r *Report) WriteMarkdown(path string) error {
	return os.WriteFile(path, []byte(r.markdown()), 0o644)
}

// Load decodes a previously generated report.json.
//
// Schema validation is the caller's responsibility; the CLI validates the raw
// bytes with pkg/schema before rendering so that tampered reports are rejected.
func Load(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &r, nil
}

func (r *Report) markdown() string {
	var lines []string
	add := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	add("# 事故诊断：%s", r.Incident.Alertname)
	add("")
	add("- 事故：`%s`", r.Incident.ID)
	add("- 集群 / 命名空间：`%s` / `%s`", r.Incident.Cluster, r.Incident.Namespace)
	add("- 工作负载：`%s`", r.Incident.Workload)
	add("- 观察时间：%s", r.Incident.ObservedAt)
	add("- 生成时间：%s", r.GeneratedAt)
	if r.Degraded {
		add("- 模式：%s（降级：部分能力不可用或证据被裁剪）", r.Mode)
	} else {
		add("- 模式：%s", r.Mode)
	}
	add("")

	add("## 摘要")
	add("")
	add("%s", r.Summary)
	add("")

	add("## 证据时间线")
	add("")
	if len(r.Evidence) == 0 {
		add("证据包为空，未提取到证据。")
		add("")
	} else {
		ordered := r.evidenceByObservedAt()
		add("| ID | 来源 | 观察时间 | 采集时间 | 内容引用 | 摘要 |")
		add("| --- | --- | --- | --- | --- | --- |")
		staleCount := 0
		for _, e := range ordered {
			idCell := e.ID
			if e.Stale {
				idCell += " ⚠"
				staleCount++
			}
			add("| %s | `%s` | %s | %s | `%s` | %s |",
				idCell, e.Source, e.ObservedAt, e.CollectedAt, e.ContentRef, escapeCell(e.Summary))
		}
		add("")
		if staleCount > 0 {
			add("> ⚠ 标记为过期证据（stale）：观察时间或采集时间距今超过 15 分钟，不作为高置信结论的唯一依据。")
			add("")
		}
		r.addEvidenceDetails(&lines, ordered)
	}

	add("## 候选根因")
	add("")
	if len(r.Hypotheses) == 0 {
		add("证据不足，未生成可引用的根因假设；请参考「缺失证据」补齐后重新分析。")
		add("")
	}
	for _, h := range r.Hypotheses {
		add("### %d. %s", h.Rank, h.Statement)
		add("")
		add("- 置信：%s", h.Confidence)
		add("- 证据：%s", inlineIDs(h.EvidenceIDs))
		for _, c := range h.Contradictions {
			add("- 反证：`%s` — %s", c.EvidenceID, c.Note)
		}
		add("")
	}

	if len(r.MissingData) > 0 {
		add("## 缺失证据")
		add("")
		for _, m := range r.MissingData {
			add("- **%s**：%s；下一步：%s", m.Source, m.Reason, m.NextStep)
		}
		add("")
	}
	if len(r.RecommendedChecks) > 0 {
		add("## 下一步检查")
		add("")
		for _, c := range r.RecommendedChecks {
			add("- %s", c)
		}
		add("")
	}
	if r.SuggestedFix != "" {
		add("## 修复建议（只读 · 不自动执行）")
		add("")
		add("%s", r.SuggestedFix)
		add("")
	}
	if len(r.Limitations) > 0 {
		add("## 限制")
		add("")
		for _, l := range r.Limitations {
			add("- %s", l)
		}
		add("")
	}
	if len(r.Versions) > 0 {
		add("## 版本")
		add("")
		for _, key := range sortedKeys(r.Versions) {
			add("- %s：%s", key, r.Versions[key])
		}
		add("")
	}
	add("> 本报告由离线规则生成，只读；不访问集群、不上传证据、不自动执行修复。")
	add("")
	return strings.Join(lines, newline)
}

// addEvidenceDetails surfaces query, clock skew and hashes so Markdown keeps the
// same provenance as the JSON contract.
func (r *Report) addEvidenceDetails(lines *[]string, ordered []Evidence) {
	details := make([]string, 0, len(ordered))
	for _, e := range ordered {
		parts := make([]string, 0, 4)
		if e.Query != "" {
			parts = append(parts, "查询："+e.Query)
		}
		if e.Redaction != "" {
			parts = append(parts, "脱敏级别："+e.Redaction)
		}
		if e.SkewSeconds != 0 {
			parts = append(parts, fmt.Sprintf("时钟偏差：%ds（阈值 9s）", e.SkewSeconds))
		}
		if e.SHA256 != "" {
			parts = append(parts, "sha256："+e.SHA256)
		}
		if len(parts) == 0 {
			continue
		}
		details = append(details, "- `"+e.ID+"`："+strings.Join(parts, "；"))
	}
	if len(details) == 0 {
		return
	}
	*lines = append(*lines, "### 证据引用细节")
	*lines = append(*lines, "")
	*lines = append(*lines, details...)
	*lines = append(*lines, "")
}

func (r *Report) evidenceByObservedAt() []Evidence {
	ordered := make([]Evidence, len(r.Evidence))
	copy(ordered, r.Evidence)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, errA := evidence.ParseTime(ordered[i].ObservedAt)
		b, errB := evidence.ParseTime(ordered[j].ObservedAt)
		if errA == nil && errB == nil && !a.Equal(b) {
			return a.Before(b)
		}
		if errA == nil && errB == nil {
			return ordered[i].ID < ordered[j].ID
		}
		if ordered[i].ObservedAt != ordered[j].ObservedAt {
			return ordered[i].ObservedAt < ordered[j].ObservedAt
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

func inlineIDs(ids []string) string {
	if len(ids) == 0 {
		return "（无）"
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "`" + id + "`"
	}
	return strings.Join(quoted, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), newline, " ")
}
