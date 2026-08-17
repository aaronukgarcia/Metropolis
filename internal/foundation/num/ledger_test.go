package num

import "testing"

// TestBoundedLedger_CapsAtDefaultCapacity (SEC-203): the bounded ledger must
// never grow past its capacity no matter how many events are appended —
// appending DefaultLedgerCapacity+1 events retains exactly
// DefaultLedgerCapacity, with the oldest (0) evicted and the retained window
// 1..DefaultLedgerCapacity.
func TestBoundedLedger_CapsAtDefaultCapacity(t *testing.T) {
	b := NewBoundedLedger[int]()
	for i := 0; i < DefaultLedgerCapacity+1; i++ {
		b.Append(i)
	}
	if b.Len() != DefaultLedgerCapacity {
		t.Fatalf("Len() = %d, want %d (capped)", b.Len(), DefaultLedgerCapacity)
	}
	snap := b.Snapshot()
	if len(snap) != DefaultLedgerCapacity {
		t.Fatalf("Snapshot() len = %d, want %d", len(snap), DefaultLedgerCapacity)
	}
	if snap[0] != 1 || snap[len(snap)-1] != DefaultLedgerCapacity {
		t.Fatalf("retained window = %d..%d, want 1..%d (oldest evicted first)",
			snap[0], snap[len(snap)-1], DefaultLedgerCapacity)
	}
}

// TestBoundedLedger_DeterministicOldestEviction (SEC-203 / GR#21): eviction
// is deterministic — the oldest event is always the one dropped, and two
// identical append sequences produce identical snapshots. Exercised at a
// small capacity so the ring wrap-around is visible rather than only implied.
func TestBoundedLedger_DeterministicOldestEviction(t *testing.T) {
	seq := []int{10, 20, 30, 40, 50}
	run := func() []int {
		b := newBoundedLedger[int](3)
		for _, e := range seq {
			b.Append(e)
		}
		return b.Snapshot()
	}

	got := run()
	want := []int{30, 40, 50} // 10 and 20 evicted, in insertion order
	if len(got) != len(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot[%d] = %d, want %d (deterministic oldest-eviction)", i, got[i], want[i])
		}
	}

	// Determinism: the identical sequence must reproduce the identical window.
	again := run()
	for i := range got {
		if again[i] != got[i] {
			t.Fatalf("non-deterministic snapshot: %v vs %v", again, got)
		}
	}
}

// TestBoundedLedger_SnapshotIsACopy (SEC-062): Snapshot returns a fresh slice;
// mutating the returned value must not affect the ledger's retained history.
func TestBoundedLedger_SnapshotIsACopy(t *testing.T) {
	b := newBoundedLedger[int](2)
	b.Append(1)
	b.Append(2)
	s := b.Snapshot()
	s[0] = 999
	again := b.Snapshot()
	if again[0] != 1 {
		t.Fatalf("mutating Snapshot() must not affect the ledger: got %d, want 1", again[0])
	}
}

// TestBoundedLedger_ZeroCapacityRaisedToDefault: a sub-1 capacity must be
// raised to DefaultLedgerCapacity (never a divide-by-zero ring).
func TestBoundedLedger_ZeroCapacityRaisedToDefault(t *testing.T) {
	b := newBoundedLedger[int](0)
	if b.max != DefaultLedgerCapacity {
		t.Fatalf("max = %d, want %d (sub-1 raised to default)", b.max, DefaultLedgerCapacity)
	}
	if b.Len() != 0 {
		t.Fatalf("fresh ledger Len() = %d, want 0", b.Len())
	}
}
