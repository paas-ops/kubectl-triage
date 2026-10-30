package evidence

import "testing"

func TestIndexAddLookupAll(t *testing.T) {
	ix := NewIndex()
	a := Item{ID: "ev-001", Source: "k8s.events"}
	b := Item{ID: "ev-002", Source: "logs"}
	if err := ix.Add(a, b); err != nil {
		t.Fatalf("add: %v", err)
	}
	if ix.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ix.Len())
	}
	got, ok := ix.ByID("ev-002")
	if !ok || got.Source != "logs" {
		t.Fatalf("ByID(ev-002) = %+v, %v", got, ok)
	}
	if _, ok := ix.ByID("ev-999"); ok {
		t.Fatal("unexpected lookup hit")
	}
	all := ix.All()
	if len(all) != 2 || all[0].ID != "ev-001" || all[1].ID != "ev-002" {
		t.Fatalf("All() = %+v", all)
	}
	// All must return a copy: mutating it must not affect the index.
	all[0].ID = "mutated"
	if got, _ := ix.ByID("ev-001"); got.ID != "ev-001" {
		t.Fatal("All() did not return a copy")
	}
}

func TestIndexRejectsDuplicatesAndEmptyIDs(t *testing.T) {
	ix := NewIndex()
	if err := ix.Add(Item{ID: "ev-001"}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Add(Item{ID: "ev-001"}); err == nil {
		t.Fatal("expected duplicate id error")
	}
	if err := ix.Add(Item{ID: ""}); err == nil {
		t.Fatal("expected empty id error")
	}
}
