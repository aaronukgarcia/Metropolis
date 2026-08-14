package citizens

import (
	"reflect"
	"testing"
)

// TestPageStoreEvictsAndReloads (AC-19): the disk-backed LRU paging seam —
// the scaled-down analogue proving the cold-shard paging code path runs —
// evicts the least-recently-used resident shard to disk once the resident
// ceiling is exceeded, and reloads it intact on demand.
func TestPageStoreEvictsAndReloads(t *testing.T) {
	ps := NewPageStore(t.TempDir(), 2) // max 2 resident shards

	s0 := newColdShard(0)
	s0.append(mkRecord(1, 0))
	s1 := newColdShard(0)
	s1.append(mkRecord(2, 0))
	s2 := newColdShard(0)
	s2.append(mkRecord(3, 0))

	if err := ps.Store(0, s0); err != nil {
		t.Fatalf("Store 0: %v", err)
	}
	if err := ps.Store(1, s1); err != nil {
		t.Fatalf("Store 1: %v", err)
	}
	if err := ps.Store(2, s2); err != nil { // exceeds 2 → evicts LRU shard 0
		t.Fatalf("Store 2: %v", err)
	}
	if got := ps.ResidentCount(); got != 2 {
		t.Fatalf("resident count = %d, want 2 after eviction", got)
	}

	// Shard 0 was evicted; reload it from disk and verify round-trip integrity.
	loaded, ok := ps.Load(0)
	if !ok {
		t.Fatal("evicted shard 0 was not reloaded from disk")
	}
	if loaded.count() != 1 || loaded.ids[0] != 1 {
		t.Fatalf("reloaded shard corrupt: count=%d ids=%v", loaded.count(), loaded.ids)
	}
	if !reflect.DeepEqual(loaded.recordAt(0), s0.recordAt(0)) {
		t.Fatalf("reloaded record mismatch: %+v vs %+v", loaded.recordAt(0), s0.recordAt(0))
	}

	// A never-stored shard is simply absent (ok-idiom, never a panic).
	if _, ok := ps.Load(99); ok {
		t.Fatal("Load(99) should report absent")
	}
}
