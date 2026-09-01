package core

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-1972079944 (Aaron's ruling option A): SeedClockForRestore is the
// narrow, restore-only API that seeds a never-yet-ticked Engine's clock,
// closing the gap Composition.Load leaves (a state-exact snapshot stuck at
// tick 0). These tests prove (1) the seed actually lands, (2) a negative
// seed is rejected, and (3) the sealed-clock invariant is preserved: once
// an Engine has run its first AdvanceTicks, SeedClockForRestore can never
// move its clock again -- the ONLY way to advance a live engine remains
// AdvanceTicks.

// TestSeedClockForRestore_SeedsTickBeforeSeal proves a fresh, never-ticked
// Engine's clock actually moves to the requested tick.
func TestSeedClockForRestore_SeedsTickBeforeSeal(t *testing.T) {
	e := NewEngine()
	const wantTick int64 = 91 // 3 months + 1 day at DailyTicksPerMonth=30

	if err := e.SeedClockForRestore("seed-cid", wantTick); err != nil {
		t.Fatalf("SeedClockForRestore: %v", err)
	}
	clk, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if clk.Tick() != wantTick {
		t.Fatalf("Tick() = %d, want %d", clk.Tick(), wantTick)
	}
	// Derived fields must agree -- proves the seed landed on the real
	// clock.tick field, not some side channel Tick()/Month() don't share.
	if wantMonth := wantTick / DailyTicksPerMonth; clk.Month() != wantMonth {
		t.Fatalf("Month() = %d, want %d", clk.Month(), wantMonth)
	}
}

// TestSeedClockForRestore_ProveCanFail is the mutation-style prove-can-fail
// companion to the test above: seeding an OFF-BY-ONE tick must produce a
// clock that does NOT match a naive "did it move at all" check, i.e. exact
// equality is genuinely enforced, not just "tick != 0".
func TestSeedClockForRestore_ProveCanFail(t *testing.T) {
	e := NewEngine()
	if err := e.SeedClockForRestore("seed-cid", 50); err != nil {
		t.Fatalf("SeedClockForRestore: %v", err)
	}
	clk, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if clk.Tick() == 51 {
		t.Fatal("prove-can-fail sentinel: seeded 50 somehow read back as 51")
	}
	if clk.Tick() != 50 {
		t.Fatalf("Tick() = %d, want exactly 50 (off-by-one would slip through a looser check)", clk.Tick())
	}
}

// TestSeedClockForRestore_RejectsNegativeTick proves a negative seed is
// rejected with ErrInvalidClockSeed and leaves the clock untouched (still
// genesis tick 0) -- Clock.Tick's own contract is "elapsed count since
// genesis", which can never be negative.
func TestSeedClockForRestore_RejectsNegativeTick(t *testing.T) {
	e := NewEngine()
	err := e.SeedClockForRestore("seed-cid", -1)
	if !errors.Is(err, &errs.E{Code: ErrInvalidClockSeed}) {
		t.Fatalf("SeedClockForRestore(-1): err = %v, want ErrInvalidClockSeed", err)
	}
	clk, cerr := e.Clock()
	if cerr != nil {
		t.Fatalf("Clock: %v", cerr)
	}
	if clk.Tick() != 0 {
		t.Fatalf("clock mutated despite rejected negative seed: Tick() = %d, want 0", clk.Tick())
	}
}

// TestSeedClockForRestore_RejectedAfterSeal_Deterministic is the payoff
// test: it proves the SEALED-CLOCK INVARIANT survives this new API. Drive
// exactly one AdvanceTicks call to completion (seal() runs synchronously
// at its top and has unconditionally returned before AdvanceTicks itself
// returns -- see TestSEC003_RegisterPhaseHook_RejectedAfterSeal_Deterministic
// for why this is deterministic, not scheduling-dependent), then assert a
// subsequent SeedClockForRestore call is rejected with ErrEngineSealed AND
// leaves the clock exactly where AdvanceTicks left it -- proving there is
// still NO way to move a live/already-ticked engine's clock except
// AdvanceTicks.
func TestSeedClockForRestore_RejectedAfterSeal_Deterministic(t *testing.T) {
	e := NewEngine(WithPoolSize(1))
	if err := e.AdvanceTicks("advance-cid", 5); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	clkBefore, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock (before): %v", err)
	}
	if clkBefore.Tick() != 5 {
		t.Fatalf("precondition: expected tick=5 after AdvanceTicks(5), got %d", clkBefore.Tick())
	}

	seedErr := e.SeedClockForRestore("seed-cid", 999)
	if !errors.Is(seedErr, &errs.E{Code: ErrEngineSealed}) {
		t.Fatalf("SeedClockForRestore after AdvanceTicks: err = %v, want ErrEngineSealed", seedErr)
	}

	clkAfter, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock (after): %v", err)
	}
	if clkAfter.Tick() != 5 {
		t.Fatalf("sealed-clock invariant broken: clock moved to %d despite rejected reseed (want unchanged at 5)", clkAfter.Tick())
	}
}

// TestSeedClockForRestore_RejectsOnCopiedEngine proves SeedClockForRestore
// shares the same struct-copy defence (SEC-014/SEC-016) as every other
// mu-acquiring Engine method -- a copied Engine's mu can be captured
// mid-lock and must be rejected before ever being touched. Uses e2Copy
// (sec014_poc_test.go) rather than a literal `*e` struct copy -- same
// bytes, same runtime semantics, but keeps `go vet ./...`'s copylocks
// analyzer clean (see e2Copy's own doc comment for why this package
// cannot contain that literal line).
func TestSeedClockForRestore_RejectsOnCopiedEngine(t *testing.T) {
	e := NewEngine()
	copied := e2Copy(e)
	err := copied.SeedClockForRestore("seed-cid", 10)
	if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("SeedClockForRestore on copied Engine: err = %v, want ErrEngineCopied", err)
	}
}
