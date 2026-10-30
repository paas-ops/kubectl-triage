// Package analyze implements the deterministic offline analyzer for an incident
// bundle. It extracts normalized evidence from bundle files, runs the rule
// engine, and renders a report that cites evidence and explains gaps.
//
// Analysis never touches the cluster, the network, or the raw bundle path list
// in non-deterministic order; repeated runs over the same bundle produce the
// same report apart from generated_at (driven by Options.Now).
package analyze

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
	"github.com/paas-ops/kubectl-triage/internal/analyze/rule"
	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/evidence"
	"github.com/paas-ops/kubectl-triage/internal/report"
)

// Evidence source categories (frozen by the report contract).
const (
	sourceK8sEvents    = "k8s.events"
	sourceK8sPods      = "k8s.pods"
	sourceMetrics      = "prometheus.metrics"
	sourceLogs         = "logs"
	sourceAlertmanager = "alertmanager"
	sourceChanges      = "changes"
	readLimit          = 512 << 10
	maxEvidenceSummary = 200
	removedURL         = "[已移除外部链接]"
	removedCommand     = "[已移除命令]"
	removedInstruction = "[已移除可疑指令内容]"
)

// commandFragments are never echoed into a report summary.
var commandFragments = []string{
	"rm -rf", "curl ", "wget ", "bash -c", "sh -c",
	"nc -e", "chmod +x", "base64 -d",
}

// instructionMarkers identify prompt-injection style instructions in evidence
// content. They are treated as data, never executed, and never copied into a
// report.
var instructionMarkers = []string{
	"ignore previous instructions",
	"ignore all previous",
	"ignore the above",
	"disregard previous",
	"exfiltrate",
	"忽略之前指令",
	"忽略以上指令",
}

// newline is the line separator for log parsing; avoids escape sequences.
var newline = string([]byte{10})

type fileKind int

const (
	kindUnknown fileKind = iota
	kindEvents
	kindSnapshot
	kindLogs
	kindMetrics
	kindAlertmanager
	kindChanges
)

// analysis carries the deterministic state for one Analyze call.
type analysis struct {
	b                 *bundle.Bundle
	now               time.Time
	incidentObserved  time.Time
	incidentCollected time.Time
	items             []evidence.Item
	texts             map[string]string
}

// Analyze runs the deterministic rule set over a validated incident bundle.
//
// MaxEvidence <= 0 caps at evidence.DefaultMaxEvidence (200). A zero Options.Now
// means time.Now().UTC(); tests and golden runs inject a fixed time so that the
// whole report is reproducible.
func Analyze(b *bundle.Bundle, cliVersion string, opts Options) (*report.Report, error) {
	if b == nil {
		return nil, fmt.Errorf("analyze: nil bundle")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	maxEvidence := opts.MaxEvidence
	if maxEvidence <= 0 {
		maxEvidence = evidence.DefaultMaxEvidence
	}

	a := &analysis{b: b, now: now, texts: map[string]string{}}
	a.incidentObserved = firstParsedTime(b.Incident.ObservedAt, b.Incident.CollectedAt)
	if a.incidentObserved.IsZero() {
		a.incidentObserved = now
	}
	a.incidentCollected = firstParsedTime(b.Incident.CollectedAt, b.Incident.ObservedAt)
	if a.incidentCollected.IsZero() {
		a.incidentCollected = a.incidentObserved
	}

	files, err := b.WalkTextFiles()
	if err != nil {
		return nil, fmt.Errorf("walk bundle: %w", err)
	}
	sort.Strings(files)
	for _, rel := range files {
		if err := a.consumeFile(rel); err != nil {
			return nil, err
		}
	}

	kept, trimmed := evidence.Trim(a.items, maxEvidence)
	if err := evidence.Validate(kept, b.Dir); err != nil {
		return nil, fmt.Errorf("generated evidence is invalid: %w", err)
	}

	rep := report.New(b.Incident, cliVersion)
	rep.GeneratedAt = evidence.FormatTime(now)
	rep.Mode = "offline"
	rep.Versions["rules"] = rule.Version
	rep.Versions["model"] = "none"
	for _, it := range kept {
		rep.Evidence = append(rep.Evidence, report.Evidence{
			ID:          it.ID,
			Source:      it.Source,
			ObservedAt:  it.ObservedAt,
			CollectedAt: it.CollectedAt,
			Summary:     it.Summary,
			ContentRef:  it.ContentRef,
			Query:       it.Query,
			Redaction:   it.Redaction,
			SHA256:      it.SHA256,
			Stale:       it.Stale,
			SkewSeconds: it.SkewSeconds,
		})
	}

	in := rule.Input{Items: kept, Text: a.keptTexts(kept), ObservedAt: a.incidentObserved}
	candidates := rule.DefaultRegistry().Evaluate(in)
	sortCandidates(candidates)
	rep.Hypotheses = buildHypotheses(candidates)

	rep.MissingData = a.missingData(rep.Hypotheses)
	rep.Limitations = a.limitations(kept, trimmed, maxEvidence)
	if len(rep.Hypotheses) > 0 {
		top := candidates[0]
		rep.Summary = top.Statement
		rep.RecommendedChecks = append([]string{}, top.Checks...)
		rep.SuggestedFix = top.SuggestedFix
	} else {
		rep.Summary = "现有证据不足以形成可引用的根因假设。"
		rep.RecommendedChecks = []string{}
		rep.SuggestedFix = ""
	}

	// Optional model pass (ADR-004). The offline report above is always the
	// fallback, so any model/config/runtime failure degrades instead of failing
	// the whole run; the CLI maps degraded model runs to exit code 3.
	if cfg := opts.Model; cfg.Provider != "" && !strings.EqualFold(cfg.Provider, "none") {
		enhanced, modelErr := model.Enhance(b, rep, cfg)
		if modelErr != nil {
			rep.Mode = "offline"
			rep.Degraded = true
			rep.Versions["model"] = "none"
			rep.Limitations = append(rep.Limitations, "模型不可用，已回退离线规则："+modelErr.Error())
		} else if enhanced != nil {
			rep = enhanced
		}
	}
	return rep, nil
}

// consumeFile classifies and parses one bundle text file into evidence.
func (a *analysis) consumeFile(rel string) error {
	source, kind := classify(rel)
	if source == "" {
		return nil
	}
	data, err := a.b.ReadFile(rel, readLimit)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	switch kind {
	case kindEvents:
		a.addEvents(rel, data)
	case kindLogs:
		a.addLogs(rel, data)
	case kindMetrics:
		a.addMetrics(rel, data)
	case kindAlertmanager:
		a.addAlertmanager(rel, data)
	case kindChanges:
		a.addChanges(rel, data)
	default:
		a.addSnapshot(source, rel, data)
	}
	return nil
}

// classify maps a bundle-relative path to an evidence source and parser kind.
// Markdown notes/runbooks are context, not evidence, and are skipped.
func classify(rel string) (string, fileKind) {
	base := path.Base(rel)
	switch {
	case rel == "k8s/events.json" || strings.HasPrefix(rel, "k8s/events."):
		return sourceK8sEvents, kindEvents
	case strings.HasPrefix(rel, "k8s/"):
		return sourceK8sPods, kindSnapshot
	case strings.HasPrefix(rel, "metrics/"):
		return sourceMetrics, kindMetrics
	case strings.HasPrefix(rel, "logs/"):
		return sourceLogs, kindLogs
	case rel == "alert.json" || strings.HasPrefix(base, "alertmanager") || strings.HasPrefix(rel, "alerts/"):
		return sourceAlertmanager, kindAlertmanager
	case strings.HasPrefix(rel, "changes/"):
		return sourceChanges, kindChanges
	default:
		return "", kindUnknown
	}
}

// addItem normalizes one evidence entry, assigns the next stable id and keeps
// the lower-cased search text used by the rule engine.
func (a *analysis) addItem(source, contentRef string, observed time.Time, summary, query string, fileData []byte, searchText string) string {
	if observed.IsZero() {
		observed = a.incidentObserved
	}
	id := evidence.NewID(len(a.items) + 1)
	item := evidence.Item{
		ID:          id,
		Source:      source,
		ObservedAt:  evidence.FormatTime(observed),
		CollectedAt: evidence.FormatTime(a.incidentCollected),
		Summary:     truncateRunes(sanitizeText(summary), maxEvidenceSummary),
		ContentRef:  contentRef,
		Query:       query,
		Redaction:   "strict",
		SHA256:      evidence.Hash(fileData),
	}
	item.MarkTiming(a.now)
	a.items = append(a.items, item)
	text := strings.ToLower(searchText) + " " + strings.ToLower(item.Summary)
	a.texts[id] = text
	return id
}

type k8sEvent struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	FirstTimestamp string `json:"firstTimestamp"`
	LastTimestamp  string `json:"lastTimestamp"`
	EventTime      string `json:"eventTime"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Metadata struct {
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
}

type k8sEventList struct {
	Kind  string     `json:"kind"`
	Items []k8sEvent `json:"items"`
}

func (a *analysis) addEvents(rel string, data []byte) {
	var list k8sEventList
	if err := json.Unmarshal(data, &list); err != nil || len(list.Items) == 0 {
		a.addSnapshot(sourceK8sEvents, rel, data)
		return
	}
	query := fmt.Sprintf("kubectl get events -n %s -o json", a.namespace())
	for i, ev := range list.Items {
		observed := firstParsedTime(ev.LastTimestamp, ev.EventTime, ev.FirstTimestamp, ev.Metadata.CreationTimestamp)
		summary := strings.TrimSpace(ev.Type + " " + ev.Reason)
		if message := sanitizeText(ev.Message); message != "" {
			summary += ": " + message
		}
		subject := strings.TrimSpace(ev.InvolvedObject.Kind + " " + ev.InvolvedObject.Name)
		if subject != "" {
			summary += "（" + subject + "）"
		}
		search := strings.Join([]string{ev.Type, ev.Reason, ev.Message, ev.InvolvedObject.Kind, ev.InvolvedObject.Name}, " ")
		ref := fmt.Sprintf("%s#items/%d", rel, i)
		a.addItem(sourceK8sEvents, ref, observed, summary, query, data, search)
	}
}

func (a *analysis) addLogs(rel string, data []byte) {
	observed, firstLine := logFirstTimestamp(data)
	summary := "日志摘录 " + rel
	if firstLine != "" {
		summary += "：" + sanitizeText(firstLine)
	}
	query := "loki query_range"
	a.addItem(sourceLogs, rel+"#L1", observed, summary, query, data, string(data))
}

func (a *analysis) addMetrics(rel string, data []byte) {
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string   `json:"metric"`
				Values [][]json.RawMessage `json:"values"`
				Value  []json.RawMessage   `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	observed := time.Time{}
	series, samples := 0, 0
	resultType := "unknown"
	if err := json.Unmarshal(data, &payload); err == nil {
		resultType = payload.Data.ResultType
		if resultType == "" {
			resultType = "unknown"
		}
		series = len(payload.Data.Result)
		var latestSeconds int64
		haveLatest := false
		record := func(raw []json.RawMessage) {
			if len(raw) < 2 {
				return
			}
			seconds, ok := rawInt64(raw[0])
			if !ok {
				return
			}
			samples++
			if !haveLatest || seconds > latestSeconds {
				latestSeconds = seconds
				haveLatest = true
			}
		}
		for _, r := range payload.Data.Result {
			for _, pair := range r.Values {
				record(pair)
			}
			record(r.Value)
		}
		if haveLatest {
			observed = time.Unix(latestSeconds, 0).UTC()
		}
	}
	summary := fmt.Sprintf("Prometheus 指标快照 %s（resultType=%s，%d 个序列，%d 个采样点", rel, resultType, series, samples)
	if !observed.IsZero() {
		summary += "，最新采样 " + evidence.FormatTime(observed)
	}
	summary += "）"
	a.addItem(sourceMetrics, rel, observed, summary, "prometheus query_range", data, string(data))
}

func (a *analysis) addAlertmanager(rel string, data []byte) {
	var payload struct {
		Status string `json:"status"`
		Alerts []struct {
			Status      string            `json:"status"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    string            `json:"startsAt"`
			EndsAt      string            `json:"endsAt"`
		} `json:"alerts"`
	}
	observed := time.Time{}
	names := map[string]bool{}
	statuses := map[string]bool{}
	if err := json.Unmarshal(data, &payload); err == nil {
		for _, alert := range payload.Alerts {
			if observed.IsZero() {
				observed = firstParsedTime(alert.StartsAt, alert.EndsAt)
			}
			if name := strings.TrimSpace(alert.Labels["alertname"]); name != "" {
				names[name] = true
			}
			if status := strings.TrimSpace(alert.Status); status != "" {
				statuses[status] = true
			}
		}
	}
	summary := fmt.Sprintf("Alertmanager 告警快照 %s：%d 条告警", rel, len(payload.Alerts))
	if len(names) > 0 {
		summary += "（alertname=" + strings.Join(sortedSet(names), "，") + "）"
	}
	if len(statuses) > 0 {
		summary += "（status=" + strings.Join(sortedSet(statuses), "，") + "）"
	}
	a.addItem(sourceAlertmanager, rel, observed, summary, "alertmanager api v2 alerts", data, string(data))
}

func (a *analysis) addChanges(rel string, data []byte) {
	var doc map[string]any
	observed := time.Time{}
	summary := "发布变更记录 " + rel
	if err := json.Unmarshal(data, &doc); err == nil {
		observed = firstParsedTime(
			valueString(doc["completed_at"]),
			valueString(doc["finished_at"]),
			valueString(doc["started_at"]),
			valueString(doc["timestamp"]),
			valueString(doc["created_at"]),
		)
		parts := make([]string, 0, 4)
		if workload := firstValueString(doc, "workload", "name"); workload != "" {
			parts = append(parts, "workload="+workload)
		}
		if revision := firstValueString(doc, "revision", "generation"); revision != "" {
			parts = append(parts, "revision="+revision)
		}
		previous := firstValueString(doc, "image_previous", "previous_image", "from_image")
		current := firstValueString(doc, "image_current", "image", "new_image", "to_image")
		switch {
		case previous != "" && current != "":
			parts = append(parts, "镜像 "+previous+" → "+current)
		case current != "":
			parts = append(parts, "镜像 "+current)
		case previous != "":
			parts = append(parts, "镜像 "+previous)
		}
		if status := firstValueString(doc, "status", "result", "state"); status != "" {
			parts = append(parts, "status="+status)
		}
		if len(parts) > 0 {
			summary += "：" + strings.Join(parts, "，")
		}
	}
	a.addItem(sourceChanges, rel, observed, summary, "ci/cd rollout history", data, string(data))
}

func (a *analysis) addSnapshot(source, rel string, data []byte) {
	summary := "证据快照 " + rel
	query := "bundle file"
	if source == sourceK8sPods {
		summary, query = a.kubernetesSnapshotSummary(rel, data)
	}
	if source == sourceK8sEvents {
		summary = "事件快照 " + rel
		query = fmt.Sprintf("kubectl get events -n %s -o json", a.namespace())
	}
	a.addItem(source, rel, time.Time{}, summary, query, data, string(data))
}

func (a *analysis) kubernetesSnapshotSummary(rel string, data []byte) (string, string) {
	query := "kubectl get pods -n " + a.namespace() + " -o yaml"
	switch {
	case strings.Contains(rel, "deploy"):
		query = "kubectl get deployments -n " + a.namespace() + " -o yaml"
	case strings.Contains(rel, "replicaset"):
		query = "kubectl get replicasets -n " + a.namespace() + " -o yaml"
	}
	summary := "Kubernetes 资源快照 " + rel
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return summary + "（YAML 解析失败，按整文件证据处理）", query
	}
	if rawItems, ok := doc["items"].([]any); ok {
		counts := map[string]int{}
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind := strings.TrimSpace(valueString(item["kind"]))
			if kind == "" {
				kind = "Unknown"
			}
			counts[kind]++
		}
		summary = fmt.Sprintf("Kubernetes 资源快照 %s（%d 个对象", rel, len(rawItems))
		if len(counts) > 0 {
			summary += "：" + strings.Join(countKinds(counts), "，")
		}
		summary += "）"
		return summary, query
	}
	if kind := strings.TrimSpace(valueString(doc["kind"])); kind != "" {
		summary += "（kind=" + kind + "）"
	}
	return summary, query
}

// missingData lists absent evidence sources with an actionable next step. Order
// is fixed so reports are reproducible.
func (a *analysis) missingData(hyps []report.Hypothesis) []report.MissingData {
	seen := map[string]bool{}
	for _, it := range a.items {
		seen[it.Source] = true
	}
	out := make([]report.MissingData, 0, 5)
	if !seen[sourceMetrics] {
		out = append(out, report.MissingData{
			Source:   sourceMetrics,
			Reason:   "事故包缺少 metrics/ 指标数据",
			NextStep: "导出故障前后 30 分钟的 Prometheus query_range 结果到 metrics/（脱敏后）",
		})
	}
	if !seen[sourceK8sEvents] {
		out = append(out, report.MissingData{
			Source:   sourceK8sEvents,
			Reason:   "事故包缺少 k8s/events.json 事件列表",
			NextStep: "运行 kubectl get events -n <namespace> -o json，脱敏后保存为 k8s/events.json",
		})
	}
	if !seen[sourceLogs] {
		out = append(out, report.MissingData{
			Source:   sourceLogs,
			Reason:   "事故包缺少 logs/ 日志摘录",
			NextStep: "导出故障时间窗内相关 Pod 的日志到 logs/（脱敏后）",
		})
	}
	if !seen[sourceChanges] {
		out = append(out, report.MissingData{
			Source:   sourceChanges,
			Reason:   "事故包缺少 changes/ 发布变更记录",
			NextStep: "补充发布/镜像/commit 变更记录到 changes/rollout.json",
		})
	}
	if len(hyps) == 0 {
		out = append(out, report.MissingData{
			Source:   "incident.evidence",
			Reason:   "未找到五类故障的明确签名或证据不足",
			NextStep: "按上述缺失项补齐证据后重新运行 analyze；优先补充 k8s/events.json 与 logs/",
		})
	}
	return out
}

// limitations records rule version, trimming and staleness so a reader can judge
// the report without guessing.
func (a *analysis) limitations(kept, trimmed []evidence.Item, maxEvidence int) []string {
	out := []string{
		fmt.Sprintf("离线规则版本 %s；未接入模型，不读取集群、不访问网络。", rule.Version),
		"所有建议均为只读，不自动执行任何修复。",
		"报告仅基于事故包内证据，未核验集群实时状态。",
		"证据内容按不可执行数据处理：日志与事件中的指令性文本不会被执行或外传。",
	}
	stale := 0
	for _, it := range kept {
		if it.Stale {
			stale++
		}
	}
	if stale > 0 {
		out = append(out, fmt.Sprintf("有 %d 条证据已过期（stale，观察或采集时间距今超过 15 分钟）。", stale))
	}
	if len(trimmed) > 0 {
		out = append(out, fmt.Sprintf("证据超过上限 %d 条，已确定性裁剪 %d 条（保留优先级：k8s 事件/工作负载/指标/日志/告警/变更）；裁剪可能造成结论不完整。", maxEvidence, len(trimmed)))
	}
	return out
}

func (a *analysis) keptTexts(kept []evidence.Item) map[string]string {
	out := make(map[string]string, len(kept))
	for _, it := range kept {
		if text, ok := a.texts[it.ID]; ok {
			out[it.ID] = text
			continue
		}
		out[it.ID] = strings.ToLower(it.Summary)
	}
	return out
}

func (a *analysis) namespace() string {
	ns := strings.TrimSpace(a.b.Incident.Namespace)
	if ns == "" {
		return "default"
	}
	return ns
}

// sortCandidates orders hypotheses by confidence (high first), then rule
// priority, then rule id so that ranks are deterministic.
func sortCandidates(candidates []rule.Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		ci := rule.ConfidenceRank(candidates[i].Confidence)
		cj := rule.ConfidenceRank(candidates[j].Confidence)
		if ci != cj {
			return ci < cj
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].RuleID < candidates[j].RuleID
	})
}

// buildHypotheses converts the top candidates into at most three report entries.
func buildHypotheses(candidates []rule.Candidate) []report.Hypothesis {
	limit := len(candidates)
	if limit > 3 {
		limit = 3
	}
	out := make([]report.Hypothesis, 0, limit)
	seen := map[string]bool{}
	for _, c := range candidates {
		if len(out) == limit {
			break
		}
		if seen[c.RuleID] || len(c.EvidenceIDs) == 0 {
			continue
		}
		seen[c.RuleID] = true
		hypothesis := report.Hypothesis{
			Rank:           len(out) + 1,
			Statement:      c.Statement,
			Confidence:     c.Confidence,
			EvidenceIDs:    append([]string{}, c.EvidenceIDs...),
			Contradictions: make([]report.Contradiction, 0, len(c.Contradictions)),
		}
		for _, contradiction := range c.Contradictions {
			hypothesis.Contradictions = append(hypothesis.Contradictions, report.Contradiction{
				EvidenceID: contradiction.EvidenceID,
				Note:       contradiction.Note,
			})
		}
		out = append(out, hypothesis)
	}
	return out
}

// sanitizeText neutralizes instruction-like content in anything that reaches a
// report. Rules still see the raw (lower-cased) text for marker matching, but
// reports must never echo exfiltration URLs or commands.
func sanitizeText(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	cleaned := stripURLs(trimmed)
	lower := strings.ToLower(cleaned)
	cut := -1
	instruction := false
	for _, fragment := range commandFragments {
		if at := strings.Index(lower, fragment); at >= 0 && (cut < 0 || at < cut) {
			cut = at
			instruction = false
		}
	}
	for _, marker := range instructionMarkers {
		if at := strings.Index(lower, marker); at >= 0 && (cut < 0 || at < cut) {
			cut = at
			instruction = true
		}
	}
	if cut >= 0 {
		replacement := removedCommand
		if instruction {
			replacement = removedInstruction
		}
		cleaned = strings.TrimSpace(cleaned[:cut]) + " " + replacement
	}
	return strings.TrimSpace(cleaned)
}

// stripURLs replaces http(s) links with a neutral marker. URLs are never needed
// in a report summary and could carry exfiltration instructions.
func stripURLs(s string) string {
	var builder strings.Builder
	lower := strings.ToLower(s)
	i := 0
	for i < len(s) {
		found := -1
		for _, prefix := range []string{"http://", "https://"} {
			if at := strings.Index(lower[i:], prefix); at >= 0 && (found < 0 || at < found) {
				found = at
			}
		}
		if found < 0 {
			builder.WriteString(s[i:])
			break
		}
		start := i + found
		builder.WriteString(s[i:start])
		builder.WriteString(removedURL)
		j := start
		for j < len(s) && !isURLDelimiter(s[j]) {
			j++
		}
		i = j
	}
	return builder.String()
}

func isURLDelimiter(c byte) bool {
	switch c {
	case ' ', byte(9), byte(10), byte(13), '"', '<', '>', ')', ']', '}', ',', ';':
		return true
	default:
		return false
	}
}

// logFirstTimestamp returns the first parseable log line timestamp and that line.
func logFirstTimestamp(data []byte) (time.Time, string) {
	text := string(data)
	firstLine := ""
	for _, line := range strings.Split(text, newline) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if firstLine == "" {
			firstLine = trimmed
		}
		if observed, ok := parseLogTimestamp(trimmed); ok {
			return observed, trimmed
		}
	}
	return time.Time{}, firstLine
}

// parseLogTimestamp parses a token-prefixed RFC3339 timestamp such as
// "2026-10-30T13:49:58Z", "[2026-10-30T13:49:58Z]" or "2026-10-30T13:49:58+08:00".
func parseLogTimestamp(line string) (time.Time, bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "[")
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == ']' || s[i] == byte(9) {
			end = i
			break
		}
	}
	token := s[:end]
	// Support "2026-10-30 13:49:58" (space instead of T) as well.
	if len(token) == 10 && len(s) >= 19 && s[10] == ' ' {
		token = s[:19]
	}
	if len(token) < 19 {
		return time.Time{}, false
	}
	normalized := []byte(token)
	if normalized[10] == ' ' {
		normalized[10] = 'T'
	}
	if len(normalized) == 19 {
		normalized = append(normalized, 'Z')
	}
	parsed, err := evidence.ParseTime(string(normalized))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

func firstParsedTime(values ...string) time.Time {
	for _, value := range values {
		if parsed, err := evidence.ParseTime(value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func rawInt64(raw json.RawMessage) (int64, bool) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return int64(number), true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err == nil {
			return int64(number), true
		}
	}
	return 0, false
}

func valueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func firstValueString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := valueString(doc[key]); value != "" {
			return value
		}
	}
	return ""
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func countKinds(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for kind, count := range counts {
		out = append(out, fmt.Sprintf("%s×%d", kind, count))
	}
	sort.Strings(out)
	return out
}
