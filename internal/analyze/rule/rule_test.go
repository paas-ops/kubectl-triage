package rule

import (
	"strings"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/evidence"
)

func testTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// quote returns s wrapped in double quotes without relying on escape sequences.
func quote(s string) string {
	return string(rune(34)) + s + string(rune(34))
}

func testItem(id, source string, stale bool) evidence.Item {
	return evidence.Item{
		ID:          id,
		Source:      source,
		ObservedAt:  "2026-10-30T13:50:00Z",
		CollectedAt: "2026-10-30T13:50:05Z",
		Summary:     "summary for " + id,
		ContentRef:  "logs/" + id + ".log",
		SHA256:      evidence.Hash([]byte(id)),
		Stale:       stale,
	}
}

func testInput(items []evidence.Item, texts map[string]string, observed string) Input {
	if texts == nil {
		texts = map[string]string{}
	}
	return Input{Items: items, Text: texts, ObservedAt: testTime(observed)}
}

func TestSelectSupport(t *testing.T) {
	items := []evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "logs", false),
	}
	in := testInput(items, map[string]string{
		"ev-001": "warning oomkilled container checkout-api",
		"ev-002": "info starting checkout-api",
	}, "2026-10-30T13:50:00Z")

	support := SelectSupport(in, []string{"oomkilled", "exit code 137"})
	if len(support) != 1 || support[0].ID != "ev-001" {
		t.Fatalf("support = %+v", support)
	}
	if got := SelectSupport(in, nil); got != nil {
		t.Fatalf("no markers must select nothing, got %+v", got)
	}
}

func TestConfidenceLadder(t *testing.T) {
	cases := []struct {
		sources      int
		fresh        bool
		contradicted bool
		want         string
	}{
		{2, true, false, "high"},
		{3, true, false, "high"},
		{2, true, true, "mid"},
		{1, true, false, "mid"},
		{1, true, true, "low"},
		{1, false, false, "low"},
		{2, false, false, "low"},
		{2, false, true, "low"},
	}
	for _, c := range cases {
		if got := Confidence(c.sources, c.fresh, c.contradicted); got != c.want {
			t.Fatalf("Confidence(%d,%v,%v) = %q, want %q", c.sources, c.fresh, c.contradicted, got, c.want)
		}
	}
	if ConfidenceRank("high") >= ConfidenceRank("mid") || ConfidenceRank("mid") >= ConfidenceRank("low") {
		t.Fatal("confidence ranking is not high < mid < low")
	}
}

func TestEvaluateOOM(t *testing.T) {
	items := []evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "logs", false),
		testItem("ev-003", "prometheus.metrics", true),
	}
	in := testInput(items, map[string]string{
		"ev-001": "warning oomkilled: container terminated (exitcode=137)",
		"ev-002": "error container terminated: oomkilled (exitcode=137)",
		"ev-003": "kube_pod_container_status_last_terminated_reason reason=oomkilled",
	}, "2026-10-30T13:50:00Z")

	candidates := DefaultRegistry().Evaluate(in)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want only oom", candidates)
	}
	c := candidates[0]
	if c.RuleID != "oom-killed" || c.Confidence != "high" || c.SourceCount != 3 || !c.Fresh {
		t.Fatalf("unexpected candidate: %+v", c)
	}
	wantIDs := []string{"ev-001", "ev-002", "ev-003"}
	if strings.Join(c.EvidenceIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("evidence ids = %v, want %v", c.EvidenceIDs, wantIDs)
	}
	if len(c.Contradictions) != 0 {
		t.Fatalf("unexpected contradictions: %+v", c.Contradictions)
	}
}

func TestEvaluateSingleSourceAndStale(t *testing.T) {
	freshIn := testInput(
		[]evidence.Item{testItem("ev-001", "k8s.events", false)},
		map[string]string{"ev-001": "warning imagepullbackoff: failed to pull image"},
		"2026-10-30T13:50:00Z",
	)
	candidates := DefaultRegistry().Evaluate(freshIn)
	if len(candidates) != 1 || candidates[0].Confidence != "mid" {
		t.Fatalf("single fresh source must be mid: %+v", candidates)
	}

	staleIn := testInput(
		[]evidence.Item{testItem("ev-001", "k8s.events", true)},
		map[string]string{"ev-001": "warning imagepullbackoff: failed to pull image"},
		"2026-10-30T13:50:00Z",
	)
	candidates = DefaultRegistry().Evaluate(staleIn)
	if len(candidates) != 1 || candidates[0].Confidence != "low" {
		t.Fatalf("stale-only evidence must be low: %+v", candidates)
	}
}

func TestContradictionsNeverOverlapSupport(t *testing.T) {
	cases := []Input{}
	// Image pulled before: counter-evidence must not also be a support entry.
	imageIn := testInput([]evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "k8s.pods", false),
		testItem("ev-003", "k8s.pods", false),
	}, map[string]string{
		"ev-001": "warning errimagepull: manifest unknown",
		"ev-002": "warning imagepullbackoff: back-off pulling image",
		"ev-003": "normal successfully pulled image checkout-api:v1.9.0",
	}, "2026-10-30T13:50:00Z")
	cases = append(cases, imageIn)
	// Probe failure while the pod is crash-looping.
	probeIn := testInput([]evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "logs", false),
		testItem("ev-003", "logs", false),
	}, map[string]string{
		"ev-001": "warning back-off restarting failed container",
		"ev-002": "error panic: failed to start",
		"ev-003": "warning liveness probe failed: connection refused",
	}, "2026-10-30T13:50:00Z")
	cases = append(cases, probeIn)
	// Old rollout record countering the release hypothesis.
	_, _, releaseIn := releaseTestItems("2026-10-30t11:30:00z")
	cases = append(cases, releaseIn)

	for _, in := range cases {
		for _, candidate := range DefaultRegistry().Evaluate(in) {
			assertDisjoint(t, &candidate)
		}
	}
}

func TestImagePulledContradiction(t *testing.T) {
	items := []evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "k8s.pods", false),
		testItem("ev-003", "k8s.pods", false),
	}
	in := testInput(items, map[string]string{
		"ev-001": "warning errimagepull: manifest unknown",
		"ev-002": "warning imagepullbackoff: back-off pulling image",
		"ev-003": "normal successfully pulled image checkout-api:v1.9.0",
	}, "2026-10-30T13:50:00Z")

	candidates := DefaultRegistry().Evaluate(in)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if c.RuleID != "image-pull" || len(c.Contradictions) != 1 || c.Contradictions[0].EvidenceID != "ev-003" {
		t.Fatalf("unexpected image pull candidate: %+v", c)
	}
	if c.Confidence != "mid" {
		t.Fatalf("contradiction must downgrade high to mid, got %q", c.Confidence)
	}
}

func TestProbeContradiction(t *testing.T) {
	items := []evidence.Item{
		testItem("ev-001", "k8s.events", false),
		testItem("ev-002", "logs", false),
		testItem("ev-003", "logs", false),
	}
	in := testInput(items, map[string]string{
		"ev-001": "warning back-off restarting failed container",
		"ev-002": "error panic: failed to start",
		"ev-003": "warning liveness probe failed: connection refused",
	}, "2026-10-30T13:50:00Z")

	candidates := DefaultRegistry().Evaluate(in)
	if len(candidates) != 1 || candidates[0].RuleID != "crashloop-backoff" {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if len(c.Contradictions) != 1 || c.Confidence != "mid" {
		t.Fatalf("probe contradiction must downgrade high to mid: %+v", c)
	}
}

func TestReleaseRuleRequiresChangesSource(t *testing.T) {
	items := []evidence.Item{testItem("ev-001", "k8s.events", false)}
	in := testInput(items, map[string]string{
		"ev-001": "normal scaled up replica set checkout-api-7c8d9 to 3",
	}, "2026-10-30T13:50:00Z")

	for _, c := range DefaultRegistry().Evaluate(in) {
		if c.RuleID == "release-regression" {
			t.Fatalf("release rule fired without changes evidence: %+v", c)
		}
	}
}

// changeText builds a lower-cased change record fragment as the analyzer sees it.
func changeText(completed string) string {
	return "status=completed image checkout-api:v1.9.0 -> v1.9.1 " +
		quote("completed_at") + ":" + quote(completed)
}

// releaseTestItems builds a release scenario: one changes record plus two
// independent non-changes sources (events, pods) that cite the rollout.
func releaseTestItems(completed string) ([]evidence.Item, map[string]string, Input) {
	items := []evidence.Item{
		testItem("ev-001", "changes", false),
		testItem("ev-002", "k8s.events", false),
		testItem("ev-003", "k8s.pods", false),
	}
	texts := map[string]string{
		"ev-001": changeText(completed),
		"ev-002": "normal scaled up replica set checkout-api-7c8d9 to 3",
		"ev-003": "pod image registry.prod/checkout-api:v1.9.1 running",
	}
	return items, texts, testInput(items, texts, "2026-10-30T13:50:00Z")
}

func releaseCandidate(t *testing.T, in Input) *Candidate {
	t.Helper()
	candidates := DefaultRegistry().Evaluate(in)
	for i := range candidates {
		if candidates[i].RuleID == "release-regression" {
			return &candidates[i]
		}
	}
	t.Fatalf("release candidate missing: %+v", candidates)
	return nil
}

func assertDisjoint(t *testing.T, c *Candidate) {
	t.Helper()
	supported := map[string]bool{}
	for _, id := range c.EvidenceIDs {
		supported[id] = true
	}
	for _, contradiction := range c.Contradictions {
		if supported[contradiction.EvidenceID] {
			t.Fatalf("evidence %q is both support and contradiction: %+v", contradiction.EvidenceID, c)
		}
	}
}

func TestReleaseSupportExcludesChanges(t *testing.T) {
	_, _, in := releaseTestItems("2026-10-30t11:30:00z")
	release := releaseCandidate(t, in)
	if len(release.EvidenceIDs) != 2 {
		t.Fatalf("release support = %v, want events+pods only", release.EvidenceIDs)
	}
	for _, id := range release.EvidenceIDs {
		if id == "ev-001" {
			t.Fatalf("changes evidence must not support the release hypothesis: %v", release.EvidenceIDs)
		}
	}
	if len(release.Contradictions) != 1 || release.Contradictions[0].EvidenceID != "ev-001" {
		t.Fatalf("changes record should be counter-evidence: %+v", release.Contradictions)
	}
	assertDisjoint(t, release)
}

func TestReleaseTimingContradiction(t *testing.T) {
	_, _, in := releaseTestItems("2026-10-30t11:30:00z")
	release := releaseCandidate(t, in)
	if release.Confidence != "mid" {
		t.Fatalf("release timing contradiction must downgrade high to mid, got %q", release.Confidence)
	}
	if release.SourceCount != 2 {
		t.Fatalf("release source count = %d, want 2", release.SourceCount)
	}

	_, _, recent := releaseTestItems("2026-10-30t13:49:55z")
	release = releaseCandidate(t, recent)
	if len(release.Contradictions) != 0 {
		t.Fatalf("recent rollout must not be contradicted: %+v", release.Contradictions)
	}
	if release.Confidence != "high" {
		t.Fatalf("recent rollout with two sources must be high, got %q", release.Confidence)
	}
	assertDisjoint(t, release)
}

func TestChangeCompletedAt(t *testing.T) {
	cases := []struct {
		text string
		want string
		ok   bool
	}{
		{"completed_at=2026-10-30t13:49:55z", "", false},
		{quote("completed_at") + ": " + quote("2026-10-30t13:49:55z"), "2026-10-30T13:49:55Z", true},
		{quote("finished_at") + ":" + quote("2026-10-30t11:30:00z"), "2026-10-30T11:30:00Z", true},
		{quote("timestamp") + ":" + quote("2026-10-30t13:49:55z"), "2026-10-30T13:49:55Z", true},
		{quote("completed_at") + ":" + quote("not-a-time"), "", false},
		{"no timestamps here", "", false},
	}
	for _, c := range cases {
		got, ok := changeCompletedAt(c.text)
		if ok != c.ok {
			t.Fatalf("changeCompletedAt(%q) ok = %v, want %v", c.text, ok, c.ok)
		}
		if ok && !got.Equal(testTime(c.want)) {
			t.Fatalf("changeCompletedAt(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	items := []evidence.Item{
		testItem("ev-002", "logs", false),
		testItem("ev-001", "k8s.events", false),
	}
	in := testInput(items, map[string]string{
		"ev-001": "warning oomkilled exitcode=137",
		"ev-002": "error oomkilled exitcode=137",
	}, "2026-10-30T13:50:00Z")
	first := DefaultRegistry().Evaluate(in)
	second := DefaultRegistry().Evaluate(in)
	if len(first) != len(second) {
		t.Fatalf("length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.RuleID != b.RuleID || a.Confidence != b.Confidence ||
			strings.Join(a.EvidenceIDs, ",") != strings.Join(b.EvidenceIDs, ",") ||
			strings.Join(a.Sources, ",") != strings.Join(b.Sources, ",") {
			t.Fatalf("candidate %d differs: %+v vs %+v", i, a, b)
		}
	}
}

func TestDefaultRulesAreOrdered(t *testing.T) {
	rules := DefaultRegistry().Rules()
	if len(rules) != 5 {
		t.Fatalf("rules = %d, want 5", len(rules))
	}
	seen := map[string]bool{}
	for i, r := range rules {
		if r.ID == "" || r.Statement == "" || len(r.Markers) == 0 || len(r.Checks) == 0 || r.SuggestedFix == "" {
			t.Fatalf("incomplete rule: %+v", r)
		}
		if seen[r.ID] {
			t.Fatalf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if i > 0 && rules[i-1].Priority >= r.Priority {
			t.Fatalf("priorities not strictly increasing at %d", i)
		}
	}
	if Version == "" {
		t.Fatal("rule version must be recorded")
	}
}
