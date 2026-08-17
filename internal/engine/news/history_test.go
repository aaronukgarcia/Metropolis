package news

import "testing"

// TestSnapshotWhere_BoundedAllocation is SEC-150: SnapshotWhere must not
// pre-allocate a backing array sized to the whole log. A window query that
// matches one record of many must return a copy whose capacity is bounded by
// the window, not by O(total history) — every monthly bulletin / annual
// review filters a whole log down to a sliver and must not allocate the full
// log's backing array each time.
func TestSnapshotWhere_BoundedAllocation(t *testing.T) {
	const total = 10000
	h := NewHistory()
	for i := 0; i < total; i++ {
		h.append(Event{ID: "e", Tick: int64(i), Category: CategoryRecord, Magnitude: 1, Text: "story"}, "")
	}

	target := int64(total - 1)
	out := h.SnapshotWhere(func(r record) bool { return r.ev.Tick == target })

	if len(out) != 1 {
		t.Fatalf("matched %d records, want 1", len(out))
	}
	// The core SEC-150 claim: the returned slice must not carry a backing
	// array sized to the whole log. Pre-fix, cap == len(h.records) == total.
	if cap(out) >= total {
		t.Errorf("SnapshotWhere pre-allocated O(total): cap=%d for a 1-record window over %d records (SEC-150)", cap(out), total)
	}
	// The stronger window-bounded property: one matching record should need
	// only a handful of slots, not thousands.
	if cap(out) > 8 {
		t.Errorf("SnapshotWhere window cap=%d, want a small window-bounded capacity", cap(out))
	}
}

// TestSnapshot_ReturnsFullCopy guards the flip side of SEC-150's fix: the
// full-log Snapshot still returns every record in ingest order (the nil-slice
// growth must not drop or reorder anything when keep is always true).
func TestSnapshot_ReturnsFullCopy(t *testing.T) {
	h := NewHistory()
	for i := 0; i < 5; i++ {
		h.append(Event{ID: "e", Tick: int64(i), Category: CategoryRecord, Magnitude: 1, Text: "story"}, "")
	}
	out := h.Snapshot()
	if len(out) != 5 {
		t.Fatalf("Snapshot len = %d, want 5", len(out))
	}
	for i, r := range out {
		if r.ev.Tick != int64(i) {
			t.Errorf("Snapshot[%d].Tick = %d, want %d (ingest order)", i, r.ev.Tick, i)
		}
	}
}
