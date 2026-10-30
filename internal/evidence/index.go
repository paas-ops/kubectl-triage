package evidence

import "fmt"

// Index is an insertion-ordered evidence collection with O(1) lookup by id.
// It is used by the analyzer to keep ids stable while assembling evidence.
type Index struct {
	order []string
	items map[string]Item
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{items: map[string]Item{}}
}

// Add appends items in order. It rejects empty ids and duplicates so that a bug
// in the analyzer cannot silently overwrite a cited evidence entry.
func (ix *Index) Add(items ...Item) error {
	if ix.items == nil {
		ix.items = map[string]Item{}
	}
	for _, it := range items {
		if it.ID == "" {
			return fmt.Errorf("evidence: empty id for source %q", it.Source)
		}
		if _, ok := ix.items[it.ID]; ok {
			return fmt.Errorf("evidence: duplicate id %q", it.ID)
		}
		ix.items[it.ID] = it
		ix.order = append(ix.order, it.ID)
	}
	return nil
}

// ByID returns the item with the given id.
func (ix *Index) ByID(id string) (Item, bool) {
	it, ok := ix.items[id]
	return it, ok
}

// All returns a copy of the items in insertion order.
func (ix *Index) All() []Item {
	if ix == nil {
		return nil
	}
	out := make([]Item, 0, len(ix.order))
	for _, id := range ix.order {
		out = append(out, ix.items[id])
	}
	return out
}

// Len returns the number of items in the index.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.order)
}
