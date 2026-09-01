package compose

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/unlocks"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// participantsWithoutAttract reproduces the exact pre-FEAT-1972079947
// Participants() shape (save_wire.go, minus the attract entry it now
// includes) — the negative control's fixture. Used by BOTH the save and
// the load side of the prove-can-fail test below, so the reproduced
// scenario is faithful to history: attract never had a shard written for
// it at all (not a shard present-but-unmatched, which save.Load correctly
// fails closed on — see MET-E812 in save/errors.go — a stronger property
// than the historical bug this control reproduces).
func participantsWithoutAttract(st *simState) []save.Participant {
	return []save.Participant{
		world.NewSaveParticipant(st.world),
		citizens.NewSaveParticipant(st.citizens),
		finance.NewSaveParticipant(st.finance),
		build.NewSaveParticipant(st.buildAPI),
		refuse.NewSaveParticipant(st.refuse),
		traffic.NewSaveParticipant(st.traffic),
		unlocks.NewSaveParticipant(st.unlocks),
		crime.NewSaveParticipant(st.crime),
		market.NewSaveParticipant(st.market),
		consumption.NewSaveParticipant(st.consumption),
		// attract.NewSaveParticipant(st.attract) deliberately OMITTED —
		// reproducing the pre-FEAT-1972079947 shape.
		newComposeLedgerParticipant(st),
	}
}

// saveWithoutAttractParticipant mirrors Composition.Save but writes the
// bundle through participantsWithoutAttract's reduced list, so — unlike a
// full Save followed by a reduced Load — no "attract" shard is written at
// all (the actual historical shape: attract never implemented
// save.Participant, so Save never emitted anything for it).
func saveWithoutAttractParticipant(c *Composition, root string) error {
	st := c.state
	clock, err := st.e.Clock()
	if err != nil {
		return err
	}
	ctx := save.Context{
		WorldSeed:     int64(st.seed),
		CreatedAtTick: clock.Tick(),
		GameMonth:     clock.Month(),
		AppVersion:    compositionSaveAppVersion,
	}
	mgr := save.NewManager(root, participantsWithoutAttract(st), st.cid)
	return mgr.SaveManual(ctx, compositionSaveName)
}

// loadAtWithoutAttractParticipant is
// TestLoadAt_ProveCanFail_AttractParticipantMissingBreaksMonthBoundaryContinuity's
// negative control: it duplicates Composition.LoadAt's logic (Load +
// SeedClockForRestore + BUG-288 tracker re-establishment) but loads
// through participantsWithoutAttract's reduced list — matched against a
// bundle written by saveWithoutAttractParticipant, so this is a like-for-
// like reproduction of the pre-FEAT-1972079947 world: attract's state was
// simply never part of the save/load contract, left at whatever
// [c.state.attract] already held (a freshly-Wired composition's default,
// in this test).
func loadAtWithoutAttractParticipant(c *Composition, root string, tick int64) error {
	st := c.state
	summaries, _, err := save.List(root)
	if err != nil {
		return err
	}
	dir := ""
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			dir = s.Path
			break
		}
	}
	if dir == "" {
		return errors.New("loadAtWithoutAttractParticipant: no composition save found")
	}
	mgr := save.NewManager(root, participantsWithoutAttract(st), st.cid)
	if _, _, err := mgr.Load(dir); err != nil {
		return err
	}
	st.syncMoneyFromLedger()

	if err := st.e.SeedClockForRestore(st.cid, tick); err != nil {
		return err
	}
	st.lastClosedTick = tick
	st.previousClosingPop = int64(st.citizens.TotalPopulation(st.cid))
	st.previousClosingMoney = num.SatAdd(st.treasury, st.citizenWealth)
	return nil
}

// FEAT-1972079944 (Aaron's ruling option A): LoadAt is Load's
// tick-continuous sibling -- it restores module state exactly like Load,
// then seeds the clock via core.Engine's new restore-only
// SeedClockForRestore. These tests prove the two things Load alone could
// not: (1) the loaded engine's clock actually reads the saved tick with
// state still byte-exact, and (2) continuing to tick a LoadAt'd
// composition is genuinely equivalent to continuing the original --
// something a plain Load demonstrably is NOT (see
// TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking below).

// TestLoadAt_SeedsClockAndPreservesStateDigest proves LoadAt yields an
// engine whose Clock().Tick() equals the tick passed in AND whose
// StateDigest still matches the saved state exactly, byte-for-byte, same
// as Load's own headline guarantee.
func TestLoadAt_SeedsClockAndPreservesStateDigest(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)

	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	if clockA.Tick() == 0 {
		t.Fatalf("precondition: driven composition A should be at tick>0, got 0")
	}
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	clockB, err := eB.Clock()
	if err != nil {
		t.Fatalf("Clock (B): %v", err)
	}
	if clockB.Tick() != clockA.Tick() {
		t.Fatalf("LoadAt did not seed the clock: got tick=%d, want %d", clockB.Tick(), clockA.Tick())
	}
	if compB.StateDigest() != digestA {
		t.Errorf("LoadAt: StateDigest did NOT round-trip: A=%x B=%x", digestA, compB.StateDigest())
	}
}

// TestLoadAt_ProveCanFail_OffByOneSeedDetected proves the tick-equality
// check above has teeth: seeding one tick off the saved value is
// detectably wrong, not silently accepted.
func TestLoadAt_ProveCanFail_OffByOneSeedDetected(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildComposition(t)
	// Deliberately seed one tick off the true saved value.
	if err := compB.LoadAt(dir, clockA.Tick()+1); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	got := compB.state.e
	gotClock, err := got.Clock()
	if err != nil {
		t.Fatalf("Clock (B): %v", err)
	}
	if gotClock.Tick() == clockA.Tick() {
		t.Fatal("prove-can-fail: off-by-one seed was not observable in Clock().Tick()")
	}
}

// TestLoadAt_TickContinuity is the payoff test (the whole point of
// FEAT-1972079944): save at tick T, LoadAt(root, T), then AdvanceTicks(n)
// on both the loaded engine and a reference engine that ran straight
// through with NO save/load at all. Both must land on identical
// StateDigest and identical clock -- proving the loaded composition is
// genuinely tick-continuous with the original, which a plain Load (frozen
// at tick 0) cannot be.
//
// extraTicks is deliberately kept well inside the SAME calendar month the
// save was taken in (< DailyTicksPerMonth ticks remain before the next
// month boundary — driveMultiDomain leaves the driven engine at an exact
// month boundary, tick 90): clock continuity (core.Engine's new
// SeedClockForRestore) plus the BUG-288 ledger-closing-tracker continuity
// LoadAt re-establishes (see its doc comment). Continuity across a NEW
// calendar month boundary — which additionally requires engine.attract's
// own internal momentum state (reputation/lastAdvancedMonth/nextMigrantID)
// to have round-tripped — is proved separately by
// TestLoadAt_TickContinuity_AcrossMonthBoundary below (FEAT-1972079947
// closed that gap by giving engine.attract a save.Participant; it was
// previously a documented known limitation).
func TestLoadAt_TickContinuity(t *testing.T) {
	const extraTicks = int64(10)

	// Reference: one engine driven straight through with no save/load at
	// all -- the ground truth for "what continuing the original would
	// have produced".
	eRef, compRef := buildComposition(t)
	driveMultiDomain(t, eRef, compRef)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (reference): %v", err)
	}
	refDigest := compRef.StateDigest()
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (reference): %v", err)
	}

	// Save/LoadAt path: same driven history saved at tick T, restored via
	// LoadAt(T), then advanced the SAME extraTicks.
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (loaded): %v", err)
	}
	loadedDigest := compB.StateDigest()
	loadedClock, err := eB.Clock()
	if err != nil {
		t.Fatalf("Clock (loaded): %v", err)
	}

	if loadedClock.Tick() != refClock.Tick() {
		t.Fatalf("tick continuity broken: loaded clock=%d, reference clock=%d", loadedClock.Tick(), refClock.Tick())
	}
	if loadedDigest != refDigest {
		t.Errorf("tick continuity broken: StateDigest after continued ticking does not match the reference that never stopped: loaded=%x ref=%x", loadedDigest, refDigest)
	}
}

// TestLoadAt_TickContinuity_AcrossMonthBoundary is FEAT-1972079947's payoff
// test: it REPLACES the former
// TestLoadAt_KnownLimitation_AttractStateNotRestoredAcrossMonthBoundary,
// which documented that LoadAt's tick continuity broke the instant
// continued ticking crossed a NEW calendar month boundary, because
// engine.attract's own internal momentum state (reputation,
// lastAdvancedMonth, nextMigrantID -- internal/engine/attract/api.go) had
// no save.Participant at all and was therefore not restored by
// Load/LoadAt. Now that engine.attract implements save.Participant
// (attract/participant.go, wired into save_wire.go's Participants()),
// this test proves the gap is closed: it drives TWO extra full months past
// the save point (the same history the old known-limitation test used, and
// the same extraTicks that used to prove a MUST-diverge) and asserts the
// digests now MATCH -- the loaded composition is genuinely tick-continuous
// with a reference engine that never stopped, across a month boundary and
// not just within one (which TestLoadAt_TickContinuity above already
// covered).
func TestLoadAt_TickContinuity_AcrossMonthBoundary(t *testing.T) {
	extraTicks := int64(2 * core.DailyTicksPerMonth) // two full extra months, crossing at least one calendar month boundary

	eRef, compRef := buildComposition(t)
	driveMultiDomain(t, eRef, compRef)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (reference): %v", err)
	}
	refDigest := compRef.StateDigest()
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (reference): %v", err)
	}

	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (loaded): %v", err)
	}
	loadedDigest := compB.StateDigest()
	loadedClock, err := eB.Clock()
	if err != nil {
		t.Fatalf("Clock (loaded): %v", err)
	}

	if loadedClock.Tick() != refClock.Tick() {
		t.Fatalf("tick continuity broken: loaded clock=%d, reference clock=%d", loadedClock.Tick(), refClock.Tick())
	}
	if loadedDigest != refDigest {
		t.Fatalf("tick continuity broken across a month boundary: StateDigest after continued ticking does not match the reference that never stopped: loaded=%x ref=%x -- if this reverts, engine.attract's participant (attract/participant.go) or its wiring (save_wire.go) most likely regressed", loadedDigest, refDigest)
	}
}

// TestLoadAt_ProveCanFail_AttractParticipantMissingBreaksMonthBoundaryContinuity
// proves TestLoadAt_TickContinuity_AcrossMonthBoundary's comparison has
// teeth: it repeats the exact same drive/save/LoadAt/advance sequence but
// saves AND loads through participantsWithoutAttract's reduced list (a
// like-for-like reproduction of the pre-FEAT-1972079947 shape: attract
// never had a shard written for it at all) — the loaded state must then
// DIVERGE from the reference across the month boundary, the same failure
// the removed known-limitation test used to document as expected. This is
// the prove-can-fail control for the positive test above.
func TestLoadAt_ProveCanFail_AttractParticipantMissingBreaksMonthBoundaryContinuity(t *testing.T) {
	extraTicks := int64(2 * core.DailyTicksPerMonth)

	eRef, compRef := buildComposition(t)
	driveMultiDomain(t, eRef, compRef)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (reference): %v", err)
	}
	refDigest := compRef.StateDigest()

	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	dir := t.TempDir()
	if err := saveWithoutAttractParticipant(compA, dir); err != nil {
		t.Fatalf("saveWithoutAttractParticipant: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := loadAtWithoutAttractParticipant(compB, dir, clockA.Tick()); err != nil {
		t.Fatalf("loadAtWithoutAttractParticipant: %v", err)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (loaded, attract participant withheld): %v", err)
	}
	loadedDigest := compB.StateDigest()

	if loadedDigest == refDigest {
		t.Fatal("prove-can-fail: loading WITHOUT the attract participant still matched the reference across a month boundary -- either the comparison has no teeth, or driveMultiDomain's history no longer exercises attract's momentum/migrant-id path")
	}
}

// TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking is the
// negative-control half of the payoff: it proves the SAME
// continue-ticking comparison genuinely detects a real difference by
// running it against plain Load (which leaves the clock at 0) instead of
// LoadAt. Continuing to tick a plain-Load'd composition re-advances from
// tick 0, so it MUST diverge from the reference that continued from tick
// T -- this is exactly the desync FEAT-1972079944 exists to close, and
// this test proves the comparison above is not vacuously true (i.e. it
// would have caught a LoadAt that forgot to seed the clock).
func TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking(t *testing.T) {
	const extraTicks = int64(2 * core.DailyTicksPerMonth)

	eRef, compRef := buildComposition(t)
	driveMultiDomain(t, eRef, compRef)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (reference): %v", err)
	}
	refDigest := compRef.StateDigest()

	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil { // plain Load: clock stays 0
		t.Fatalf("Load: %v", err)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (plain-Load): %v", err)
	}
	plainLoadDigest := compB.StateDigest()

	if plainLoadDigest == refDigest {
		t.Fatal("prove-can-fail: plain Load + continued ticking produced the SAME digest as the reference -- the continuity comparison has no teeth (or driveMultiDomain's tick history is degenerate)")
	}
}

// TestLoadAt_RejectsInvalidSeed proves LoadAt propagates
// core.ErrInvalidClockSeed for a negative tick, and does not silently
// half-restore -- module state is loaded (Load's own step) but the clock
// seed step fails loudly.
func TestLoadAt_RejectsInvalidSeed(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildComposition(t)
	err := compB.LoadAt(dir, -1)
	if err == nil {
		t.Fatal("LoadAt(-1): want error, got nil")
	}
	if !errors.Is(err, &errs.E{Code: core.ErrInvalidClockSeed}) {
		t.Fatalf("LoadAt(-1): err = %v, want to wrap core.ErrInvalidClockSeed", err)
	}
}

// TestLoadAt_DevModeContinuity_NoConservationViolationOnFirstResumedTick
// gives LoadAt's BUG-288 tracker re-establishment (lastClosedTick /
// previousClosingPop / previousClosingMoney, save_wire.go's LoadAt, the
// lines immediately after SeedClockForRestore) REAL failing-capable
// coverage. An independent destructive round found the other LoadAt tests
// above are BLIND to this: they build every composition via
// buildComposition -> Wire(e, nil), i.e. with ZERO invariant.HookOptions,
// so a conservation mismatch only LOGS (errs.New always fires) -- it never
// touches StateDigest, never errors AdvanceTicks, and self-heals one tick
// later when the NEXT closeLedgerForTick call overwrites the transiently
// wrong opening baseline again. Confirmed live: scratch-deleting the three
// tracker-reestablishment lines left every one of TestLoadAt_TickContinuity
// / TestLoadAt_KnownLimitation_.../ TestLoadAt_SeedsClockAndPreservesStateDigest
// green (BUG-230's vacuous-test class).
//
// This test instead wires compB's invariant hook with
// invariant.WithDevMode(true) (AC-8: a Detected Violation additionally
// hard-fails) plus invariant.WithPanicFunc (the documented test-only
// override that captures the hard-fail message instead of a real
// process-killing panic) so a conservation violation is an observable,
// failing test assertion instead of a silent log line. Advancing exactly
// ONE tick past the load point is deliberate: that is precisely the tick
// where closeLedgerForTick's lastClosedTick-vs-tick gate fires for the
// FIRST time after restore -- the one tick where an unrestored (fresh-Wire
// zero) opening baseline has not yet been overwritten by anything else and
// therefore actually matters. A fresh compB's own Wire-time construction
// already seeds a legitimate peopleOpening/moneyOpening (matching its own
// zero-tick seed population) -- it is precisely lastClosedTick staying at
// its fresh-Wire 0 that makes the FIRST post-load closeLedgerForTick call
// (tick+1 > 0) stomp that legitimate baseline back down to
// previousClosingPop/Money's fresh-Wire zero values, corrupting the
// conservation check against the ACTUAL restored population/money even
// though every module's own state loaded byte-exact.
func TestLoadAt_DevModeContinuity_NoConservationViolationOnFirstResumedTick(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	if clockA.Tick() == 0 {
		t.Fatalf("precondition: driven composition A should be at tick>0, got 0")
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var violations []string
	eB := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{
		InvariantOpts: []invariant.HookOption{
			invariant.WithDevMode(true),
			invariant.WithPanicFunc(func(msg string) {
				violations = append(violations, msg)
			}),
		},
	})
	if err != nil {
		t.Fatalf("Wire (devMode): %v", err)
	}
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks (first resumed tick): %v", err)
	}

	if len(violations) != 0 {
		t.Fatalf("LoadAt left the conservation ledger's opening baseline wrong: %d MET-E300 dev-mode violation(s) captured on the very first tick resumed after LoadAt (proves the BUG-288 tracker re-establishment regressed): %v", len(violations), violations)
	}
}
