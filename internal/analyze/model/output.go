package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

// maxModelStringRunes is the ADR-004 D5 per-string limit. It is not expressible
// in the frozen report schema, so it is enforced here before schema validation.
const maxModelStringRunes = 500

// modelContradiction mirrors the D5 counter-evidence entry.
type modelContradiction struct {
	EvidenceID string `json:"evidence_id"`
	Note       string `json:"note"`
}

// modelHypothesis mirrors the D5 hypothesis entry (rank is assigned by us).
type modelHypothesis struct {
	Statement      string               `json:"statement"`
	Confidence     string               `json:"confidence"`
	EvidenceIDs    []string             `json:"evidence_ids"`
	Contradictions []modelContradiction `json:"contradictions"`
}

// modelOutput mirrors the frozen ADR-004 D5 JSON contract for provider output.
// Unknown fields are ignored: they are not copied into the report, so they can
// never leak provider-controlled data.
type modelOutput struct {
	Summary           string            `json:"summary"`
	Hypotheses        []modelHypothesis `json:"hypotheses"`
	RecommendedChecks []string          `json:"recommended_checks"`
	SuggestedFix      string            `json:"suggested_fix"`
}

// buildModelReport parses one provider answer, validates it, merges it with the
// offline report, and returns the neutralized success report. A non-empty
// problems slice means the answer must go through a correction round.
func buildModelReport(offline *report.Report, content, provider, modelName string, round int, spentUSD float64) (*report.Report, []string) {
	out, err := decodeModelOutput(content)
	if err != nil {
		return nil, []string{err.Error()}
	}
	if problems := validateModelOutput(out, offline); len(problems) > 0 {
		return nil, problems
	}
	candidate := mergeModelReport(offline, out, provider, modelName, round, spentUSD)

	data, err := json.Marshal(candidate)
	if err != nil {
		return nil, []string{"encode candidate report: " + err.Error()}
	}
	if err := schema.ValidateReport(data); err != nil {
		return nil, []string{"报告 schema 校验失败: " + err.Error()}
	}

	neutralizeReport(candidate)
	data, err = json.Marshal(candidate)
	if err != nil {
		return nil, []string{"encode neutralized report: " + err.Error()}
	}
	if err := schema.ValidateReport(data); err != nil {
		return nil, []string{"中和后的报告 schema 校验失败: " + err.Error()}
	}
	return candidate, nil
}

// decodeModelOutput enforces the D5 transport shape: a bare JSON object without
// Markdown fences or surrounding prose. The summary field is required because it
// is the primary answer; an empty string is allowed by the frozen schema.
func decodeModelOutput(content string) (*modelOutput, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("模型响应为空，期望 ADR-004 D5 JSON 对象")
	}
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, errors.New("模型响应必须是纯 JSON 对象（不得包含 Markdown 代码围栏或说明文字）")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil, fmt.Errorf("模型响应 JSON 解析失败：%v", err)
	}
	if _, ok := fields["summary"]; !ok {
		return nil, errors.New("模型响应缺少 summary 字段")
	}
	var out modelOutput
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("模型响应 JSON 结构不符：%v", err)
	}
	return &out, nil
}

// validateModelOutput enforces the D5 constraints that the frozen report schema
// does not cover (string length, citation closure, support/contradiction
// disjointness) and reports every problem so one correction round can fix all.
func validateModelOutput(out *modelOutput, offline *report.Report) []string {
	var problems []string
	checkString := func(field, value string) {
		if runes := []rune(value); len(runes) > maxModelStringRunes {
			problems = append(problems, fmt.Sprintf("%s 超过 %d 字符上限（当前 %d）", field, maxModelStringRunes, len(runes)))
		}
	}

	checkString("summary", out.Summary)
	if len(out.Hypotheses) > 3 {
		problems = append(problems, fmt.Sprintf("hypotheses 最多 3 条（当前 %d）", len(out.Hypotheses)))
	}

	known := make(map[string]bool, len(offline.Evidence))
	for _, item := range offline.Evidence {
		known[item.ID] = true
	}
	for i, hypothesis := range out.Hypotheses {
		prefix := fmt.Sprintf("hypotheses[%d]", i)
		checkString(prefix+".statement", hypothesis.Statement)
		if len(hypothesis.EvidenceIDs) == 0 {
			problems = append(problems, prefix+" 至少需要 1 个 evidence_id")
		}
		support := make(map[string]bool, len(hypothesis.EvidenceIDs))
		for _, id := range hypothesis.EvidenceIDs {
			if !known[id] {
				problems = append(problems, fmt.Sprintf("%s 引用了不存在的证据 id %q", prefix, truncateRunes(id, 80)))
			}
			support[id] = true
		}
		for j, contradiction := range hypothesis.Contradictions {
			counterPrefix := fmt.Sprintf("%s.contradictions[%d]", prefix, j)
			checkString(counterPrefix+".note", contradiction.Note)
			if !known[contradiction.EvidenceID] {
				problems = append(problems, fmt.Sprintf("%s 引用了不存在的证据 id %q", counterPrefix, truncateRunes(contradiction.EvidenceID, 80)))
			}
			if support[contradiction.EvidenceID] {
				problems = append(problems, fmt.Sprintf("%s 的 evidence_id %q 同时作为支持证据与反证", counterPrefix, truncateRunes(contradiction.EvidenceID, 80)))
			}
		}
	}
	for i, check := range out.RecommendedChecks {
		checkString(fmt.Sprintf("recommended_checks[%d]", i), check)
	}
	checkString("suggested_fix", out.SuggestedFix)
	return problems
}

// mergeModelReport builds the success report from the offline report plus the
// validated model body. Offline evidence, missing_data, incident, generated_at
// and non-model versions are preserved verbatim.
func mergeModelReport(offline *report.Report, out *modelOutput, provider, modelName string, round int, spentUSD float64) *report.Report {
	hypotheses := make([]report.Hypothesis, 0, len(out.Hypotheses))
	for i, hypothesis := range out.Hypotheses {
		contradictions := make([]report.Contradiction, 0, len(hypothesis.Contradictions))
		for _, contradiction := range hypothesis.Contradictions {
			contradictions = append(contradictions, report.Contradiction{
				EvidenceID: contradiction.EvidenceID,
				Note:       contradiction.Note,
			})
		}
		evidenceIDs := make([]string, len(hypothesis.EvidenceIDs))
		copy(evidenceIDs, hypothesis.EvidenceIDs)
		hypotheses = append(hypotheses, report.Hypothesis{
			Rank:           i + 1,
			Statement:      hypothesis.Statement,
			Confidence:     hypothesis.Confidence,
			EvidenceIDs:    evidenceIDs,
			Contradictions: contradictions,
		})
	}
	checks := make([]string, len(out.RecommendedChecks))
	copy(checks, out.RecommendedChecks)
	evidence := make([]report.Evidence, len(offline.Evidence))
	copy(evidence, offline.Evidence)
	missing := make([]report.MissingData, len(offline.MissingData))
	copy(missing, offline.MissingData)

	return &report.Report{
		SchemaVersion:     offline.SchemaVersion,
		Incident:          offline.Incident,
		GeneratedAt:       offline.GeneratedAt,
		Mode:              "model",
		Degraded:          false,
		Summary:           out.Summary,
		Evidence:          evidence,
		Hypotheses:        hypotheses,
		MissingData:       missing,
		RecommendedChecks: checks,
		SuggestedFix:      out.SuggestedFix,
		Limitations:       modelLimitations(offline.Limitations, provider, modelName),
		Versions:          modelVersions(offline.Versions, provider, modelName, round, spentUSD),
	}
}

// modelLimitations replaces the offline "model not connected" disclaimer with a
// model-mode explanation and appends the safety note required by ADR-004 D5.
func modelLimitations(offline []string, provider, modelName string) []string {
	explanation := fmt.Sprintf("模型模式：%s/%s 仅基于离线证据做归纳；不读取集群、不访问证据中的 URL、不执行任何命令。", provider, modelName)
	out := make([]string, 0, len(offline)+2)
	replaced := false
	for _, limitation := range offline {
		if strings.Contains(limitation, "未接入模型") {
			if !replaced {
				out = append(out, explanation)
				replaced = true
			}
			continue
		}
		out = append(out, limitation)
	}
	if !replaced {
		out = append(out, explanation)
	}
	out = append(out, "安全说明：证据内容按不可执行数据处理，模型不会执行或外传证据中的指令；模型输出中的外部链接、可执行命令与指令性内容已替换为占位符。")
	return out
}

// modelVersions copies the offline version snapshot and adds the model keys.
// report.schema.json freezes versions to string values, cost is therefore a
// four-decimal string.
func modelVersions(offline map[string]string, provider, modelName string, round int, spentUSD float64) map[string]string {
	out := make(map[string]string, len(offline)+3)
	for key, value := range offline {
		out[key] = value
	}
	out["model"] = provider + "/" + modelName
	out["model_rounds"] = strconv.Itoa(round)
	out["model_cost_usd"] = fmt.Sprintf("%.4f", spentUSD)
	return out
}

// neutralizeReport replaces external URLs, executable commands and
// instruction-like content in every model-derived text field.
func neutralizeReport(candidate *report.Report) {
	candidate.Summary = neutralizeText(candidate.Summary)
	for i := range candidate.Hypotheses {
		candidate.Hypotheses[i].Statement = neutralizeText(candidate.Hypotheses[i].Statement)
		for j := range candidate.Hypotheses[i].Contradictions {
			candidate.Hypotheses[i].Contradictions[j].Note = neutralizeText(candidate.Hypotheses[i].Contradictions[j].Note)
		}
	}
	for i := range candidate.RecommendedChecks {
		candidate.RecommendedChecks[i] = neutralizeText(candidate.RecommendedChecks[i])
	}
	candidate.SuggestedFix = neutralizeText(candidate.SuggestedFix)
}
