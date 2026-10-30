// Package rule implements the deterministic offline rules for the five M1
// failure classes (OOMKilled, CrashLoopBackOff, ImagePullBackOff, Pending and
// release regression).
//
// Rules only consume normalized evidence; they never touch the cluster, the
// network or the raw bundle. Each rule cites evidence ids, may surface
// counter-evidence and is scored with a small confidence ladder so that the
// analyzer can produce at most three cited hypotheses.
package rule

import (
	"sort"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/evidence"
)

// Version is the ruleset version recorded in every report.
const Version = "rule-v0.2"

// Contradiction is counter-evidence that weakens a candidate hypothesis.
type Contradiction struct {
	EvidenceID string
	Note       string
}

// Input is the deterministic rule evaluation input.
type Input struct {
	Items []evidence.Item
	// Text maps evidence id to a lower-cased searchable representation of the
	// entry (content plus summary). Rules never echo it into reports verbatim.
	Text       map[string]string
	ObservedAt time.Time
}

// Rule is one failure-class signature.
type Rule struct {
	ID           string
	Priority     int
	Statement    string
	Checks       []string
	SuggestedFix string
	// Markers are lower-cased substrings that match supporting evidence text.
	Markers []string
	// RequireSource, when set, requires at least one marker-matching entry from
	// that evidence source (for example release regression only fires on
	// changes/ evidence).
	RequireSource string
	// ExcludeFromSupport removes marker-matching entries of that source from the
	// cited support. It is used when the source only provides context or
	// counter-evidence (release change records must never support themselves).
	ExcludeFromSupport string
	// Counter derives counter-evidence from the full evidence set.
	Counter func(in Input, support []evidence.Item) []Contradiction
}

// Candidate is a matched rule with cited evidence and a confidence level.
type Candidate struct {
	RuleID         string
	Priority       int
	Statement      string
	Checks         []string
	SuggestedFix   string
	EvidenceIDs    []string
	Sources        []string
	SourceCount    int
	Fresh          bool
	Confidence     string
	Contradictions []Contradiction
}

// Registry holds the ordered, deterministic rule set.
type Registry struct {
	rules []Rule
}

// NewRegistry returns a registry over the given rules (evaluation order).
func NewRegistry(rules ...Rule) *Registry {
	return &Registry{rules: append([]Rule(nil), rules...)}
}

// Add appends a rule to the registry.
func (reg *Registry) Add(r Rule) {
	reg.rules = append(reg.rules, r)
}

// Rules returns a copy of the registered rules in evaluation order.
func (reg *Registry) Rules() []Rule {
	return append([]Rule(nil), reg.rules...)
}

// Evaluate runs every rule over the input and returns matches in registry order.
func (reg *Registry) Evaluate(in Input) []Candidate {
	candidates := make([]Candidate, 0, len(reg.rules))
	for _, r := range reg.rules {
		matched := SelectSupport(in, r.Markers)
		if len(matched) == 0 {
			continue
		}
		if r.RequireSource != "" && !hasSource(matched, r.RequireSource) {
			continue
		}
		support := matched
		if r.ExcludeFromSupport != "" {
			support = withoutSource(matched, r.ExcludeFromSupport)
		}
		if len(support) == 0 {
			continue
		}
		sources := distinctSources(support)
		ids := make([]string, 0, len(support))
		fresh := false
		for _, it := range support {
			ids = append(ids, it.ID)
			if !it.Stale {
				fresh = true
			}
		}
		sort.Strings(ids)

		var contradictions []Contradiction
		if r.Counter != nil {
			contradictions = filterContradictions(normalizeContradictions(r.Counter(in, support)), support)
		}
		candidates = append(candidates, Candidate{
			RuleID:         r.ID,
			Priority:       r.Priority,
			Statement:      r.Statement,
			Checks:         append([]string(nil), r.Checks...),
			SuggestedFix:   r.SuggestedFix,
			EvidenceIDs:    ids,
			Sources:        sources,
			SourceCount:    len(sources),
			Fresh:          fresh,
			Confidence:     Confidence(len(sources), fresh, len(contradictions) > 0),
			Contradictions: contradictions,
		})
	}
	return candidates
}

// SelectSupport returns evidence entries whose searchable text contains any of
// the (lower-case) markers, in input order.
func SelectSupport(in Input, markers []string) []evidence.Item {
	if len(markers) == 0 {
		return nil
	}
	var out []evidence.Item
	for _, it := range in.Items {
		text, ok := in.Text[it.ID]
		if !ok {
			text = strings.ToLower(it.Summary)
		}
		if containsAny(text, markers) {
			out = append(out, it)
		}
	}
	return out
}

// Confidence implements the confidence ladder:
//
//	>=2 independent sources and fresh evidence -> high
//	one source and fresh evidence               -> mid
//	only stale evidence                         -> low
//
// Any contradiction downgrades the result by one level.
func Confidence(sourceCount int, fresh, contradicted bool) string {
	level := "low"
	switch {
	case sourceCount >= 2 && fresh:
		level = "high"
	case sourceCount == 1 && fresh:
		level = "mid"
	}
	if contradicted {
		level = downgrade(level)
	}
	return level
}

func downgrade(level string) string {
	switch level {
	case "high":
		return "mid"
	case "mid":
		return "low"
	default:
		return "low"
	}
}

// ConfidenceRank orders confidence levels for deterministic sorting (high first).
func ConfidenceRank(level string) int {
	switch level {
	case "high":
		return 0
	case "mid":
		return 1
	default:
		return 2
	}
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// withoutSource returns items whose Source differs from source.
func withoutSource(items []evidence.Item, source string) []evidence.Item {
	out := make([]evidence.Item, 0, len(items))
	for _, it := range items {
		if it.Source == source {
			continue
		}
		out = append(out, it)
	}
	return out
}

// filterContradictions guarantees that cited support and counter-evidence are
// disjoint: an entry cannot both support and contradict the same hypothesis.
func filterContradictions(contradictions []Contradiction, support []evidence.Item) []Contradiction {
	if len(contradictions) == 0 {
		return nil
	}
	supported := make(map[string]bool, len(support))
	for _, it := range support {
		supported[it.ID] = true
	}
	out := make([]Contradiction, 0, len(contradictions))
	for _, c := range contradictions {
		if supported[c.EvidenceID] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasSource(items []evidence.Item, source string) bool {
	for _, it := range items {
		if it.Source == source {
			return true
		}
	}
	return false
}

func distinctSources(items []evidence.Item) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if seen[it.Source] {
			continue
		}
		seen[it.Source] = true
		out = append(out, it.Source)
	}
	sort.Strings(out)
	return out
}

func normalizeContradictions(in []Contradiction) []Contradiction {
	seen := map[string]bool{}
	out := make([]Contradiction, 0, len(in))
	for _, c := range in {
		if c.EvidenceID == "" || seen[c.EvidenceID] {
			continue
		}
		seen[c.EvidenceID] = true
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
	return out
}

// releaseWindow is how recent a rollout must be to keep full explanatory power.
const releaseWindow = 30 * time.Minute

var imagePulledMarkers = []string{
	"successfully pulled",
	"already present on machine",
	"image is up to date",
}

var probeFailureMarkers = []string{
	"liveness probe failed",
	"readiness probe failed",
	"startup probe failed",
}

var releaseCompletionKeys = []string{"completed_at", "finished_at", "timestamp"}

// ImagePulledCounter weakens image-pull hypotheses when the image was pulled
// successfully before, which points at a moved tag instead of an unreachable one.
func ImagePulledCounter(in Input) []Contradiction {
	var out []Contradiction
	for _, it := range in.Items {
		if containsAny(textFor(in, it), imagePulledMarkers) {
			out = append(out, Contradiction{
				EvidenceID: it.ID,
				Note:       "存在镜像曾被成功拉取或已在本机的证据，更像是 tag 被覆盖、仓库元数据或凭证轮换，而非镜像名完全不可达。",
			})
		}
	}
	return out
}

// ProbeCounter weakens crash-loop hypotheses when probe failures are present.
func ProbeCounter(in Input) []Contradiction {
	var out []Contradiction
	for _, it := range in.Items {
		if containsAny(textFor(in, it), probeFailureMarkers) {
			out = append(out, Contradiction{
				EvidenceID: it.ID,
				Note:       "存在探针失败证据，启动失败可能由 liveness/readiness 探针配置（initialDelay/period/timeout）引起，而非应用自身崩溃。",
			})
		}
	}
	return out
}

// ReleaseTimingCounter weakens release-regression hypotheses when the rollout
// completed well before the incident window. It scans all changes/ evidence
// because those entries are excluded from cited support by the release rule.
func ReleaseTimingCounter(in Input, _ []evidence.Item) []Contradiction {
	if in.ObservedAt.IsZero() {
		return nil
	}
	var out []Contradiction
	for _, it := range in.Items {
		if it.Source != "changes" {
			continue
		}
		completed, ok := changeCompletedAt(textFor(in, it))
		if !ok {
			continue
		}
		if in.ObservedAt.Sub(completed) > releaseWindow {
			out = append(out, Contradiction{
				EvidenceID: it.ID,
				Note:       "变更记录显示发布在故障窗口之前已完成，发布回归的解释力被削弱。",
			})
		}
	}
	return out
}

// changeCompletedAt extracts an explicit completion timestamp from a change
// record without assuming a full JSON schema; the analyzer stores the raw JSON
// text (lower-cased) in Input.Text.
func changeCompletedAt(text string) (time.Time, bool) {
	for _, key := range releaseCompletionKeys {
		value, ok := jsonStringValue(text, key)
		if !ok {
			continue
		}
		// Input.Text is lower-cased by the analyzer; restore RFC3339 casing.
		t, err := evidence.ParseTime(strings.ToUpper(value))
		if err != nil {
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

// jsonStringValue finds key in a JSON-like document and returns its string
// value. It is deliberately forgiving: malformed input simply returns false.
func jsonStringValue(text, key string) (string, bool) {
	idx := strings.Index(text, key)
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len(key):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return "", false
	}
	rest = rest[colon+1:]
	open := strings.IndexByte(rest, '"')
	if open < 0 {
		return "", false
	}
	rest = rest[open+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func textFor(in Input, it evidence.Item) string {
	if text, ok := in.Text[it.ID]; ok {
		return text
	}
	return strings.ToLower(it.Summary)
}
