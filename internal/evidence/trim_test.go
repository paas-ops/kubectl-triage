package evidence

import (
	"fmt"
	"testing"
)

func TestTrimKeepsSourcePriorityThenTimeline(t *testing.T) {
	items := []Item{
		{ID: "ev-001", Source: "logs", ObservedAt: "2026-10-30T13:00:00Z"},
		{ID: "ev-002", Source: "k8s.pods", ObservedAt: "2026-10-30T13:00:00Z"},
		{ID: "ev-003", Source: "k8s.events", ObservedAt: "2026-10-30T13:05:00Z"},
		{ID: "ev-004", Source: "changes", ObservedAt: "2026-10-30T12:00:00Z"},
		{ID: "ev-005", Source: "k8s.events", ObservedAt: "2026-10-30T13:01:00Z"},
	}
	kept, trimmed := Trim(items, 3)
	if len(kept) != 3 || len(trimmed) != 2 {
		t.Fatalf("kept=%d trimmed=%d, want 3/2", len(kept), len(trimmed))
	}
	// Kept in timeline order: ev-002 (13:00), ev-005 (13:01), ev-003 (13:05).
	wantKept := []string{"ev-002", "ev-005", "ev-003"}
	for i, id := range wantKept {
		if kept[i].ID != id {
			t.Fatalf("kept[%d] = %s, want %s (%+v)", i, kept[i].ID, id, kept)
		}
	}
	// Dropped: logs (lowest priority), then changes (higher priority than logs? no:
	// changes priority 5 > logs 3, so logs dropped last) -> trimmed is changes+logs.
	wantTrimmed := map[string]bool{"ev-001": true, "ev-004": true}
	for _, it := range trimmed {
		if !wantTrimmed[it.ID] {
			t.Fatalf("unexpected trimmed item %s", it.ID)
		}
	}
}

func TestTrimAppliesDefaultCap(t *testing.T) {
	items := make([]Item, 0, DefaultMaxEvidence+5)
	for i := 1; i <= DefaultMaxEvidence+5; i++ {
		items = append(items, Item{
			ID:         NewID(i),
			Source:     "k8s.events",
			ObservedAt: fmt.Sprintf("2026-10-30T13:%02d:00Z", i%60),
		})
	}
	kept, trimmed := Trim(items, 0)
	if len(kept) != DefaultMaxEvidence {
		t.Fatalf("kept = %d, want default cap %d", len(kept), DefaultMaxEvidence)
	}
	if len(trimmed) != 5 {
		t.Fatalf("trimmed = %d, want 5", len(trimmed))
	}
}

func TestTrimDoesNotMutateInput(t *testing.T) {
	items := []Item{
		{ID: "ev-002", Source: "logs", ObservedAt: "2026-10-30T13:02:00Z"},
		{ID: "ev-001", Source: "k8s.events", ObservedAt: "2026-10-30T13:01:00Z"},
	}
	original := append([]Item(nil), items...)
	kept, _ := Trim(items, 1)
	if len(kept) != 1 || kept[0].ID != "ev-001" {
		t.Fatalf("kept = %+v", kept)
	}
	for i := range items {
		if items[i] != original[i] {
			t.Fatalf("Trim mutated input at %d: %+v != %+v", i, items[i], original[i])
		}
	}
}

func TestTrimUnderCapOrdersTimeline(t *testing.T) {
	items := []Item{
		{ID: "ev-002", Source: "logs", ObservedAt: "2026-10-30T13:05:00Z"},
		{ID: "ev-001", Source: "k8s.events", ObservedAt: "2026-10-30T13:01:00Z"},
		{ID: "ev-003", Source: "k8s.events", ObservedAt: "2026-10-30T13:01:00Z"},
	}
	kept, trimmed := Trim(items, 10)
	if len(trimmed) != 0 {
		t.Fatalf("trimmed = %+v", trimmed)
	}
	want := []string{"ev-001", "ev-003", "ev-002"}
	for i, id := range want {
		if kept[i].ID != id {
			t.Fatalf("kept[%d] = %s, want %s", i, kept[i].ID, id)
		}
	}
}

func TestSourcePriority(t *testing.T) {
	order := []string{"k8s.events", "k8s.pods", "prometheus.metrics", "logs", "alertmanager", "changes"}
	for i := 1; i < len(order); i++ {
		if SourcePriority(order[i-1]) >= SourcePriority(order[i]) {
			t.Fatalf("source priority not strictly increasing: %s >= %s", order[i-1], order[i])
		}
	}
	if SourcePriority("unknown.source") <= SourcePriority("changes") {
		t.Fatal("unknown sources must sort after known sources")
	}
}
