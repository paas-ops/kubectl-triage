package evidence

import "sort"

// sourcePriority defines the deterministic keep order for trimming: primary
// timeline evidence (events) is kept before derived or context sources.
var sourcePriority = map[string]int{
	"k8s.events":         0,
	"k8s.pods":           1,
	"prometheus.metrics": 2,
	"logs":               3,
	"alertmanager":       4,
	"changes":            5,
}

// SourcePriority returns the stable keep order for an evidence source; lower
// values are more important when a bundle exceeds the evidence cap.
func SourcePriority(source string) int {
	if p, ok := sourcePriority[source]; ok {
		return p
	}
	return len(sourcePriority)
}

// Trim enforces the evidence cap deterministically.
//
// Items are first ordered by source priority, then observed_at, then id; the
// first max entries are kept. The kept slice is then re-ordered as a timeline
// (observed_at, then id) so Markdown and JSON share one evidence order.
// trimmed contains the dropped items (also timeline ordered).
//
// max <= 0 falls back to DefaultMaxEvidence.
func Trim(items []Item, max int) (kept []Item, trimmed []Item) {
	if max <= 0 {
		max = DefaultMaxEvidence
	}
	ordered := make([]Item, len(items))
	copy(ordered, items)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, pj := SourcePriority(ordered[i].Source), SourcePriority(ordered[j].Source)
		if pi != pj {
			return pi < pj
		}
		if cmp := compareObserved(ordered[i].ObservedAt, ordered[j].ObservedAt); cmp != 0 {
			return cmp < 0
		}
		return ordered[i].ID < ordered[j].ID
	})

	if len(ordered) <= max {
		sortTimeline(ordered)
		return ordered, nil
	}
	kept = make([]Item, max)
	copy(kept, ordered[:max])
	trimmed = make([]Item, len(ordered)-max)
	copy(trimmed, ordered[max:])
	sortTimeline(kept)
	sortTimeline(trimmed)
	return kept, trimmed
}

// sortTimeline orders entries by observation time and then by id.
func sortTimeline(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if cmp := compareObserved(items[i].ObservedAt, items[j].ObservedAt); cmp != 0 {
			return cmp < 0
		}
		return items[i].ID < items[j].ID
	})
}

// compareObserved compares two RFC3339 timestamps; unparseable values sort by
// their raw string so that trimming stays deterministic for degraded input.
func compareObserved(a, b string) int {
	ta, errA := ParseTime(a)
	tb, errB := ParseTime(b)
	if errA == nil && errB == nil {
		return ta.Compare(tb)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
