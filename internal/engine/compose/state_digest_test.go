package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestStateDigest_StableAcrossRepeatCalls proves StateDigest is a pure read
// of already-settled state: calling it twice on the same composition, with
// no ticks in between, yields byte-identical output. If this ever fails,
// StateDigest is ranging over a Go map (iteration order varies per range)
// or reading a live-mutating field without settling — either of which makes
// the determinism gate itself nondeterministic (GR#21). This is the
// "twice-run byte-identical" half of BUG-375 r3's digest-determinism proof.
func TestStateDigest_StableAcrossRepeatCalls(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(20260809), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	first := comp.StateDigest()
	for i := 0; i < 5; i++ {
		if got := comp.StateDigest(); got != first {
			t.Fatalf("StateDigest call %d = %x, want %x (digest construction is not deterministic)", i+2, got, first)
		}
	}
}

// TestStateDigest_EqualForTwoSameSeedRuns proves two independent composed
// runs at the same seed and month count produce the identical digest — the
// green-gate precondition. A failure here (with no injected bug) is itself a
// real determinism finding (GR#21 auto-P0), not a test defect: it would mean
// one of the observables StateDigest hashes is genuinely nondeterministic in
// baseline one today.
func TestStateDigest_EqualForTwoSameSeedRuns(t *testing.T) {
	const seed = uint64(20260809)

	run := func() [32]byte {
		e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
		return comp.StateDigest()
	}

	a := run()
	b := run()
	if a != b {
		t.Fatalf("two same-seed composed runs disagreed: %x != %x", a, b)
	}
}

// TestStateDigest_DiffersFromBareCitizenHash is a coverage guard: the broad
// digest must not collapse to (or be trivially equal to) the citizen
// PopulationHash it embeds — if it did, BUG-375 r3 would have shipped a
// digest that observes nothing beyond the population, re-opening the r2 hole.
// StateDigest folds a domain tag plus the finance/crime/refuse/ledger
// observables around PopulationHash, so the two 32-byte values must differ.
func TestStateDigest_DiffersFromBareCitizenHash(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(20260809), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if comp.StateDigest() == comp.PopulationHash() {
		t.Fatal("StateDigest equals the bare PopulationHash — the broad digest is observing nothing beyond population (BUG-375 r2 regression)")
	}
}
