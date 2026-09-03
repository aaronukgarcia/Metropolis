package citizens

import (
	"math"
	"testing"
)

// attack_bug484_emergency_bypass_test.go pins BUG-484's fix (Aaron ruling,
// 2026-09-03): EMERGENCY BYPASSES DRAIN. A declared weather emergency
// (AC-6) must realise its whole selected cohort (up to the emergency
// budget, 0 meaning unbounded -- ASM-580/EmergencyRealise's existing
// sentinel) in the SAME tick regardless of the injected FEAT-088 drain
// capacity; the ordinary (non-emergency) path stays exactly
// min(ordinary budget, drain, queued), per ASM-580's literal wording.
//
// Every test here targets [DeathQueue.RealiseDrained] directly -- the
// same surface registry.go's AdvanceDayTick calls -- so a fix confined to
// a different call site would not satisfy them.

// --- (1) AC-6 major event: a tiny hearse fleet must not flatten it. ---
//
// 500 citizens selected for one declared emergency month, drain capacity
// pinned at 1/month (the smallest possible non-zero fleet). Before the
// BUG-484 fix, RealiseDrained clamped effective := min(budget, drain,
// queued) unconditionally, so this released exactly 1 of the 500 --
// literally "a small hearse fleet flattens the AC-6 major death event
// into a trickle", the defect BUG-484 exists to close. After the fix, the
// emergency path never consults drain at all: all 500 must release in
// this one call, each carrying EmergencyFlag=true.
func TestAttackBUG484_EmergencyBypassesTinyDrainFleet(t *testing.T) {
	const queued = 500
	q := NewDeathQueue()
	// emergencyBudget=0 is the documented "unbounded" sentinel (budgetFor,
	// EmergencyRealise's doc) -- release the queue's entire content.
	cfg := mkFixedBudgetCfg(t, 5, 0)
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return 1 }), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= queued; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	released := q.RealiseDrained(cfg, true, 200, "corr")
	if len(released) != queued {
		t.Fatalf("emergency release with drain=1 realised %d of %d queued -- the emergency must bypass the drain entirely (BUG-484)", len(released), queued)
	}
	for i, rd := range released {
		if !rd.EmergencyFlag {
			t.Fatalf("released[%d]=%+v: EmergencyFlag must be true for an emergency release", i, rd)
		}
		if rd.DeathMonth != 200 {
			t.Fatalf("released[%d]=%+v: DeathMonth must be the realisation month 200", i, rd)
		}
	}
	if pending := q.Len("corr"); pending != 0 {
		t.Fatalf("pending=%d after the bypassed emergency release, want 0 -- the whole cohort must clear in one tick", pending)
	}
	// Conservation: everything queued is now in the handoff, nothing lost.
	if n := len(q.RealisedDeaths("corr")); n != queued {
		t.Fatalf("handoff has %d records, want %d", n, queued)
	}
}

// A finite (non-zero-sentinel) emergency budget still binds the emergency
// release -- BUG-484 removes the DRAIN clamp on the emergency path, not
// the emergency BUDGET itself. Proves the fix did not overshoot into
// "emergency is always fully unbounded".
func TestAttackBUG484_EmergencyStillBoundByItsOwnBudgetNotDrain(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 50) // finite emergency budget
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return 1 }), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 200; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	released := q.RealiseDrained(cfg, true, 200, "corr")
	if len(released) != 50 {
		t.Fatalf("emergency release with emergencyBudget=50, drain=1, queued=200: released %d, want 50 (the emergency BUDGET must still bind, drain must NOT)", len(released))
	}
	if pending := q.Len("corr"); pending != 150 {
		t.Fatalf("pending=%d, want 150 (200-50) -- the remainder stays queued, not dropped", pending)
	}
}

// --- (2) Normal (non-emergency) path is UNCHANGED: min(budget, drain, queued). ---
func TestAttackBUG484_NonEmergencyPathStillClampsToDrain(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 100) // budget=5
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return 3 }), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 10; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	released := q.RealiseDrained(cfg, false, 200, "corr")
	if len(released) != 3 {
		t.Fatalf("non-emergency release with budget=5 drain=3 queued=10: released %d, want 3 (min(5,3,10)) -- BUG-484's fix must not touch the ordinary path", len(released))
	}
	for _, rd := range released {
		if rd.EmergencyFlag {
			t.Fatalf("released %+v: EmergencyFlag must be false on a non-emergency release", rd)
		}
	}
	if pending := q.Len("corr"); pending != 7 {
		t.Fatalf("pending=%d, want 7 (10-3)", pending)
	}
}

// --- (4) The 96-case differential (nil drain) must still hold. ---
//
// TestAttackInc3_NilDrainIsDifferentiallyIdenticalToEmergencyRealise
// (attack_feat087_inc3_handoff_test.go) already exercises the exhaustive
// (ordinary, emergencyBudget, emergency, queued) grid with NO drain wired
// at all -- 3*4*2*4 = 96 cases. This is a lightweight re-assertion, scoped
// to BUG-484's change, that a NIL drain still makes RealiseDrained and
// EmergencyRealise release identically for both emergency and
// non-emergency months -- the fix must be additive (guard the drain
// consultation on !emergency) rather than a rewrite that could have
// disturbed the nil-drain no-op path.
func TestAttackBUG484_NilDrainStaysDifferentiallyIdenticalBothPaths(t *testing.T) {
	for _, emergency := range []bool{false, true} {
		for _, queued := range []int{0, 1, 7, 50} {
			cfg := mkFixedBudgetCfg(t, 4, 6)
			ref := NewDeathQueue()
			got := NewDeathQueue()
			for id := uint64(1); id <= uint64(queued); id++ {
				mustEnqueue(t, ref, id, 100, "corr")
				mustEnqueue(t, got, id, 100, "corr")
			}
			wantIDs := EmergencyRealise(ref, cfg, emergency, 200, "corr")
			gotRecords := got.RealiseDrained(cfg, emergency, 200, "corr")
			if len(gotRecords) != len(wantIDs) {
				t.Fatalf("emergency=%v queued=%d: RealiseDrained released %d, EmergencyRealise released %d",
					emergency, queued, len(gotRecords), len(wantIDs))
			}
			for i := range wantIDs {
				if gotRecords[i].CitizenID != wantIDs[i] {
					t.Fatalf("emergency=%v queued=%d: order diverges at %d", emergency, queued, i)
				}
			}
		}
	}
}

// --- (3) Conservation over a mixed run with hostile drain capacities,
// spanning both emergency and non-emergency months. ---
//
// Extends the existing TestAttackInc3_ConservationUnderRandomisedDrainSchedule
// shape (0/negative/MaxInt drain values) with an explicit assertion that
// EVERY emergency month in the run cleared its drain-independent budget --
// i.e. the bypass is not just "doesn't crash" but actually fires on every
// emergency tick, even immediately after a hostile (negative/MaxInt) drain
// value was returned on a neighbouring non-emergency month.
func TestAttackBUG484_ConservationAcrossHostileDrainAndEmergencyMonths(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 9, 0) // emergencyBudget=0 -> unbounded per emergency month

	caps := []int{0, 3, math.MaxInt, 1, -5, 12, 0, 7, 2, math.MaxInt, 0, 4, math.MinInt}
	idx := 0
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int {
		v := caps[idx%len(caps)]
		idx++
		return v
	}), "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}

	enqueued := 0
	seen := make(map[uint64]int)
	var nextID uint64 = 1
	emergencyMonths := 0

	for month := int64(0); month < 400; month++ {
		n := int((month*61 + 17) % 13)
		for i := 0; i < n; i++ {
			mustEnqueue(t, q, nextID, month, "corr")
			nextID++
			enqueued++
		}

		emergency := month%11 == 0
		pendingBefore := q.Len("corr")
		released := q.RealiseDrained(cfg, emergency, month, "corr")

		for _, rd := range released {
			seen[rd.CitizenID]++
			if seen[rd.CitizenID] > 1 {
				t.Fatalf("month %d: citizen %d realised %d times -- a duplicate corpse", month, rd.CitizenID, seen[rd.CitizenID])
			}
			if rd.EmergencyFlag != emergency {
				t.Fatalf("month %d: record %+v carries EmergencyFlag=%v, want %v", month, rd, rd.EmergencyFlag, emergency)
			}
		}

		if emergency {
			emergencyMonths++
			// With emergencyBudget=0 (unbounded sentinel) the ENTIRE
			// pre-release pending queue must clear this month, no matter
			// what hostile value the drain function would have returned --
			// this is the drain-bypass itself, proven under the same
			// hostile-capacity schedule the pre-existing conservation test
			// uses.
			if len(released) != pendingBefore {
				t.Fatalf("month %d (emergency): released %d, want the full pre-release pending count %d -- drain must not bind on an emergency month", month, len(released), pendingBefore)
			}
			if pend := q.Len("corr"); pend != 0 {
				t.Fatalf("month %d (emergency): pending=%d after an unbounded emergency release, want 0", month, pend)
			}
		}

		// Conservation after EVERY call: in == out + pending.
		stream := q.RealisedDeaths("corr")
		if len(stream)+q.Len("corr") != enqueued {
			t.Fatalf("month %d: conservation broken -- handoff=%d pending=%d enqueued=%d",
				month, len(stream), q.Len("corr"), enqueued)
		}
	}

	if emergencyMonths == 0 || enqueued == 0 {
		t.Fatalf("test setup invalid: emergencyMonths=%d enqueued=%d", emergencyMonths, enqueued)
	}
}
