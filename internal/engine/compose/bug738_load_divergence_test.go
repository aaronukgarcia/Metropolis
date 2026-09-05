package compose

import (
	"bytes"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-738 investigation + regression suite.
//
// VERDICT (this lane, 2026-09-05): NOT a missing-participant bug. Every
// registered save.Participant's own record stream is already byte-identical
// immediately after a real restore, and stays byte-identical through 1..12
// further ticked months at pool sizes 1/4/20, PROVIDED the restore goes
// through Composition.LoadAt -- the API that is actually reachable from
// gameplay (internal/engine/compose/snapshot.go's GetSnapshot -> LoadAt
// restore path; see that file's package doc comment). The wellbeing lane's
// probe used plain Composition.Load, which deliberately does NOT seed the
// *core.Engine* clock (save_wire.go's Load never calls
// SeedClockForRestore) -- continuing to tick afterwards re-plays months
// 0, 1, 2... through the engine-clock-driven compose.go hooks
// (attractEffect/buildEffect/consumptionEffect/deathServicesEffect all read
// clock.Month(), not a module's own restored month field) while
// citizens/attract's OWN restored state believes it is already at the true
// saved month. That mismatch is DOCUMENTED, INTENTIONAL, and independently
// proved elsewhere (save_loadat_test.go's
// TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking) -- it is
// exactly the gap LoadAt exists to close, not a new defect. No production
// code was changed for this item: TestBUG738_LoadAt_TickContinuity_LightFixture
// below is the surviving positive regression (36 cases: 3 pool sizes x 12
// month-counts, all green) and
// TestBUG738_ProveCanFail_MissingParticipantIsDetected is its mutation
// control, proving the same comparison DOES catch a truly missing
// participant.
//
// Original reported shape (wellbeing wiring lane, 2026-09-05): drive N months, Save,
// then EITHER (a) keep ticking the same live composition with no reload at
// all (the "direct" reference) or (b) Load the save into a fresh
// composition and tick the same number of extra months. PopulationHash
// matches immediately after Load but is reported to diverge from the direct
// run after one more ticked month, despite an identical population COUNT.
//
// Two collapsed variables the original report did not separate:
//  1. plain Load() vs LoadAt() -- Load leaves the *core.Engine* clock at
//     tick 0 (Composition.Load, save_wire.go, never calls
//     SeedClockForRestore), while every module's OWN month-shaped state
//     (citizens.month, attract.lastAdvancedMonth, etc.) is restored to the
//     true saved month. Every per-tick effect in compose.go that reads
//     "the current month" for a compose-level hook (attractEffect,
//     buildEffect, consumptionEffect, deathServicesEffect -- see
//     compose.go's clock.Month() call sites) reads it from the ENGINE
///    clock, not from a module's own restored counter. So continuing to
//     tick after a plain Load necessarily reintroduces month 0, 1, 2... while
//     citizens/attract believe they are already at month N -- a DOCUMENTED,
//     proved-on-purpose divergence (see
//     TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking in
//     save_loadat_test.go). LoadAt exists specifically to close this gap.
//  2. Whether that same divergence ALSO survives LoadAt (which does seed
//     the clock) -- THAT would be a genuine missing-participant bug, since
//     LoadAt's whole contract (FEAT-1972079944/947) is exact tick
//     continuity across a month boundary, and is independently proved for
//     the driveMultiDomain fixture in TestLoadAt_TickContinuity_AcrossMonthBoundary.
//
// The tests below reproduce the wellbeing lane's LIGHTER fixture
// (single/four/twenty-worker pool, no driveMultiDomain command traffic, just
// ticking) rather than the heavier driveMultiDomain fixture the existing
// LoadAt suite uses, confirming the same clean result holds on a different
// fixture shape too.

// runMonths advances e in whole-month (DailyTicksPerMonth) chunks.
func runMonths(t *testing.T, e *core.Engine, months int) {
	t.Helper()
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
}

// bugComposition is buildComposition but with a caller-chosen pool size, so
// the bisection can be run at 1/4/20 exactly like the regression suite.
func bugComposition(t *testing.T, poolSize int) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(poolSize))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire (pool %d): %v", poolSize, err)
	}
	return e, comp
}

// diffParticipants returns the Kind of every participant whose byte-exact
// record stream differs between a and b -- the actual bisection step: dump
// every registered save participant's own state digest and diff it, rather
// than guessing from the compose-level PopulationHash alone.
func diffParticipants(t *testing.T, a, b *Composition) []string {
	t.Helper()
	sa := participantStreams(t, a)
	sb := participantStreams(t, b)
	var diffs []string
	for kind, da := range sa {
		db, ok := sb[kind]
		if !ok {
			diffs = append(diffs, kind+" (missing in b)")
			continue
		}
		if !bytes.Equal(da, db) {
			diffs = append(diffs, kind)
		}
	}
	for kind := range sb {
		if _, ok := sa[kind]; !ok {
			diffs = append(diffs, kind+" (missing in a)")
		}
	}
	return diffs
}

// TestBUG738_PlainLoad_DivergesOnContinuedTicking reproduces the wellbeing
// lane's exact report using plain Load: direct-continued reference vs a
// Load()'d composition ticked the same number of extra months. This is
// EXPECTED to diverge (per TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking's
// documented contract) -- it exists here to confirm the report's fixture
// reproduces the same known shape before checking whether LoadAt (the
// actually tick-continuous API) also diverges.
func TestBUG738_PlainLoad_DivergesOnContinuedTicking(t *testing.T) {
	const monthsBeforeSave = 2
	const monthsAfterSave = 1

	eRef, compRef := bugComposition(t, 1)
	runMonths(t, eRef, monthsBeforeSave)
	dir := t.TempDir()
	if err := compRef.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	popPreSave := compRef.Population()
	hashPreSave := compRef.PopulationHash()
	runMonths(t, eRef, monthsAfterSave)
	refHash := compRef.PopulationHash()

	eB, compB := bugComposition(t, 1)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := compB.Population(); got != popPreSave {
		t.Fatalf("Population() immediately after Load = %d, want %d", got, popPreSave)
	}
	if got := compB.PopulationHash(); got != hashPreSave {
		t.Fatalf("PopulationHash() immediately after Load = %x, want %x", got, hashPreSave)
	}
	runMonths(t, eB, monthsAfterSave)
	loadedHash := compB.PopulationHash()

	t.Logf("plain Load: refHash=%x loadedHash=%x refPop=%d loadedPop=%d", refHash, loadedHash, compRef.Population(), compB.Population())
	if loadedHash != refHash {
		t.Logf("plain Load diverges after continued ticking (EXPECTED -- clock is not seeded by Load; see save_loadat_test.go's TestLoadAt_ProveCanFail_PlainLoadDivergesOnContinuedTicking). Differing participants: %v", diffParticipants(t, compRef, compB))
	}
}

// TestBUG738_LoadAt_TickContinuity_LightFixture is the real bisection: same
// drive/save/restore/advance shape as the plain-Load test above, but through
// LoadAt (the tick-continuous contract) instead of Load, at pool sizes
// 1/4/20 and for monthsAfterSave in 1..12 (the regression's exact matrix).
// If this passes at every N and pool size, the wellbeing lane's report was
// the known plain-Load clock-seed gap, not a missing participant. If it
// fails, diffParticipants pinpoints the exact culprit shard.
func TestBUG738_LoadAt_TickContinuity_LightFixture(t *testing.T) {
	for _, poolSize := range []int{1, 4, 20} {
		for n := 1; n <= 12; n++ {
			poolSize, n := poolSize, n
			t.Run("", func(t *testing.T) {
				const monthsBeforeSave = 2

				eRef, compRef := bugComposition(t, poolSize)
				runMonths(t, eRef, monthsBeforeSave)
				dir := t.TempDir()
				if err := compRef.Save(dir); err != nil {
					t.Fatalf("Save: %v", err)
				}
				clockRef, err := eRef.Clock()
				if err != nil {
					t.Fatalf("Clock (ref): %v", err)
				}
				saveTick := clockRef.Tick()
				runMonths(t, eRef, n)
				refHash := compRef.PopulationHash()
				refPop := compRef.Population()

				eB, compB := bugComposition(t, poolSize)
				if err := compB.LoadAt(dir, saveTick); err != nil {
					t.Fatalf("LoadAt: %v", err)
				}
				runMonths(t, eB, n)
				loadedHash := compB.PopulationHash()
				loadedPop := compB.Population()

				if loadedPop != refPop {
					t.Fatalf("pool=%d n=%d: Population mismatch: ref=%d loaded=%d", poolSize, n, refPop, loadedPop)
				}
				if loadedHash != refHash {
					t.Fatalf("pool=%d n=%d: PopulationHash diverges %d month(s) after LoadAt despite matching population (ref=%x loaded=%x). Differing participants: %v", poolSize, n, refPop, refHash, loadedHash, diffParticipants(t, compRef, compB))
				}
			})
		}
	}
}

// TestBUG738_LoadAt_ParticipantDigestsIdenticalImmediatelyAfterLoad is the
// "before the boundary" half of the bisection: every registered participant's
// raw record stream must already be byte-identical the INSTANT LoadAt
// returns, before a single extra tick runs. If a later test shows the
// digests diverge only after ticking, but this one is clean, the culprit is
// a field that round-trips but is not consulted/consumed by the tick path
// the same way pre- and post-restore (e.g. an engine-level, non-participant
// counter) rather than a participant with a genuinely missing field.
func TestBUG738_LoadAt_ParticipantDigestsIdenticalImmediatelyAfterLoad(t *testing.T) {
	const monthsBeforeSave = 2

	eRef, compRef := bugComposition(t, 1)
	runMonths(t, eRef, monthsBeforeSave)
	dir := t.TempDir()
	if err := compRef.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockRef, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}

	_, compB := bugComposition(t, 1)
	if err := compB.LoadAt(dir, clockRef.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	if diffs := diffParticipants(t, compRef, compB); len(diffs) != 0 {
		t.Fatalf("participant streams differ immediately after LoadAt (before any further ticking): %v", diffs)
	}
}

// TestBUG738_ProveCanFail_MissingParticipantIsDetected is the mutation
// control for TestBUG738_LoadAt_TickContinuity_LightFixture: it reruns the
// exact same light-fixture comparison but saves/restores through
// participantsWithoutAttract (save_loadat_test.go's existing negative-control
// helper -- withholding engine.attract's shard exactly as it would be if a
// module's participant genuinely dropped a field) instead of the full
// Participants() list, proving the comparison methodology has teeth: a truly
// missing piece of tick-affecting state IS caught by this exact digest/hash
// check, so LightFixture's clean pass above is not vacuous.
func TestBUG738_ProveCanFail_MissingParticipantIsDetected(t *testing.T) {
	const monthsBeforeSave = 2
	const monthsAfterSave = 2 // long enough to cross a month boundary, where attract's momentum/migrant-id state actually gets consulted again

	eRef, compRef := bugComposition(t, 1)
	runMonths(t, eRef, monthsBeforeSave)
	dir := t.TempDir()
	if err := saveWithoutAttractParticipant(compRef, dir); err != nil {
		t.Fatalf("saveWithoutAttractParticipant: %v", err)
	}
	clockRef, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (ref): %v", err)
	}
	saveTick := clockRef.Tick()
	runMonths(t, eRef, monthsAfterSave)
	refHash := compRef.PopulationHash()

	eB, compB := bugComposition(t, 1)
	if err := loadAtWithoutAttractParticipant(compB, dir, saveTick); err != nil {
		t.Fatalf("loadAtWithoutAttractParticipant: %v", err)
	}
	runMonths(t, eB, monthsAfterSave)
	loadedHash := compB.PopulationHash()

	if loadedHash == refHash {
		t.Fatal("prove-can-fail: withholding the attract participant's shard still produced a matching PopulationHash -- the LightFixture comparison has no teeth for a genuinely missing participant")
	}
}
