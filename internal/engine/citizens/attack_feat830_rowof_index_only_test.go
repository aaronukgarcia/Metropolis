package citizens

import "testing"

// FEAT-2326609830 round r1 (Destructive-F830-scale-r1) -- INDEX-ONLY PIN.
//
// The rowOf probe counter is self-reported: rowOf notes one probe and
// then does whatever it does, so a restored linear scan (on hit OR on
// miss) still reports probes/lookup == 1.000 and every counter gate stays
// green (measured in round r1: full uncounted scan => all gates PASS,
// perTick 377ms -> 663ms at 1M, invisible to pass/fail). The BUG-666
// oracle suite cannot see it either, because a scan returns CORRECT rows.
//
// This pin detects a scan STRUCTURALLY instead of by counting: it
// desynchronises the ids column from the index on purpose, so the index
// and a scan give DIFFERENT answers, and asserts rowOf gives the index's
// answer. No clock, no ratio, no fixture size.
func TestAttackFEAT830RowOfIsIndexOnly(t *testing.T) {
	s := newColdShard(0)
	const n = 1000
	for i := 1; i <= n; i++ {
		s.append(mkRecord(uint64(i), uint16(i%10)))
	}
	if s.rowOf(500) != 499 {
		t.Fatalf("precondition: rowOf(500) = %d, want 499", s.rowOf(500))
	}

	// (1) Hit path must not consult the ids column: poison every id so a
	// scan finds nothing, leave the index intact.
	saved := append([]uint64(nil), s.ids...)
	for i := range s.ids {
		s.ids[i] = 1<<63 | uint64(i)
	}
	for _, id := range []uint64{1, 250, 500, n} {
		if got, want := s.rowOf(id), int(id)-1; got != want {
			t.Errorf("hit path: rowOf(%d) = %d with the ids column poisoned, want %d from the index -- rowOf scans s.ids instead of probing the index", id, got, want)
		}
	}
	copy(s.ids, saved)

	// (2) Miss path must not fall back to a scan: drop one id from the
	// index only (the row still holds it), the index says absent.
	delete(s.index, 777)
	if got := s.rowOf(777); got != -1 {
		t.Errorf("miss path: rowOf(777) = %d with 777 absent from the index, want -1 -- rowOf falls back to a scan on a miss (dead ids are the worst case)", got)
	}
	s.index[777] = 776
}
