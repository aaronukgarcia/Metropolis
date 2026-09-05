package compose

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug689_reround2_test.go — INDEPENDENT DESTRUCTIVE RE-ROUND 2
// against BUG-689's engine.deathservices compose wiring, targeting the
// round-1 follow-ups the author claims closed (G1 non-vacuity, G2's
// WireDrainCapacity SSOT + its NEW q.mu -> d.mu lock edge, F1's
// shard-less-load reset ordering, F6's decode-time cursor clamp).
//
// GR#23: the attacker is not the author. Every test here was written to
// BREAK the landed wiring. Tests whose doc comment is marked FINDING pin a
// CURRENT GAP rather than a current guarantee — closing the gap makes them
// fail loudly so the pin is re-read, never silently rots.

// rr2Seed is this file's own dedicated world seed, distinct from
// bug689Seed and attackSeed, so a future edit to either of those files
// cannot silently change this file's death-count assumptions.
const rr2Seed = uint64(689202)

// rr2MismatchSeed is a DIFFERENT world seed used only to provoke
// save.ErrSaveSeedMismatch against an rr2Seed bundle.
const rr2MismatchSeed = uint64(689203)

// rr2Composition builds a driven composition on seed and returns the
// engine, composition and its deathservices instance after months months
// of real, ancient-cohort deaths.
func rr2Composition(t *testing.T, seed uint64, months int) (*core.Engine, *Composition, *deathservices.DeathServicesAPI) {
	t.Helper()
	api := buildGuaranteedDeathCitizensAPI(t, seed)
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire(seed=%d): %v", seed, err)
	}
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	return e, comp, comp.DeathServices()
}

// rr2State reads the (cursor, bodiesReleased, awaitingBacklog) triple that
// every reset/restore assertion in this file compares.
func rr2State(t *testing.T, ds *deathservices.DeathServicesAPI, cid string) (int64, int64, int) {
	t.Helper()
	cursor, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	backlog, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	return cursor, cons.BodiesReleased, backlog
}

// TestAttackBUG689_RR2_SeedMismatchLeavesDeathServicesUntouched is attack
// angle B, case 1. F1 made deathservices' Handler reset EAGERLY at Handler
// construction (participant.go) — a strictly more destructive stance than
// citizens' lazy first-record reset. That raises a specific hazard the
// BUG-479 pristine-digest proof does NOT cover, because
// Composition.StateDigest has no deathservices term at all: if
// save.Manager.Load ever constructed participant Handlers BEFORE its seed
// check, a REFUSED load would silently WIPE deathservices' bodies and
// handoff cursor while leaving every other module untouched — a refused
// load must be a no-op for every participant.
//
// (The landed ordering is correct: load.go calls p.Handler() inside the
// per-shard loop, after the ErrSaveSeedMismatch check. This test pins that
// ordering against the deathservices state directly, which no existing
// test does.)
func TestAttackBUG689_RR2_SeedMismatchLeavesDeathServicesUntouched(t *testing.T) {
	cid := errs.NewCorrelationID()

	// A bundle under rr2Seed with real intaken deaths.
	_, compA, _ := rr2Composition(t, rr2Seed, 2)
	root := t.TempDir()
	if err := compA.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A DIFFERENT-seed composition, itself driven far enough to hold real
	// deathservices state that a spurious reset would destroy.
	_, compB, dsB := rr2Composition(t, rr2MismatchSeed, 3)
	cursorBefore, releasedBefore, backlogBefore := rr2State(t, dsB, cid)
	if cursorBefore == 0 || releasedBefore == 0 {
		t.Fatalf("fixture: seed-%d composition produced no deathservices state (cursor=%d released=%d)", rr2MismatchSeed, cursorBefore, releasedBefore)
	}

	if err := compB.Load(root); err == nil {
		t.Fatal("Load of a seed-mismatched bundle succeeded, want save.ErrSaveSeedMismatch")
	}

	cursorAfter, releasedAfter, backlogAfter := rr2State(t, dsB, cid)
	if cursorAfter != cursorBefore || releasedAfter != releasedBefore || backlogAfter != backlogBefore {
		t.Fatalf("a REFUSED (seed-mismatched) load mutated deathservices state: "+
			"cursor %d->%d, bodiesReleased %d->%d, awaitingBacklog %d->%d — "+
			"a refused load must leave every participant untouched (F1's eager Handler reset must never run before the seed check)",
			cursorBefore, cursorAfter, releasedBefore, releasedAfter, backlogBefore, backlogAfter)
	}
}

// TestAttackBUG689_RR2_ShardPresentLoadRestoresExactlyNoDoubleReset is
// attack angle B, case 3 — the case F1's fix could most plausibly have
// broken. compose's Load now calls ResetForLoad when the validated header
// lacks a deathservices shard. If that condition were ever inverted, or
// the reset run unconditionally after the shard stream, a load of a bundle
// that DOES carry a deathservices shard would restore the state and then
// immediately throw it away — a silent, total loss of every restored body
// and the cursor, presenting as "the restored city has no dead".
func TestAttackBUG689_RR2_ShardPresentLoadRestoresExactlyNoDoubleReset(t *testing.T) {
	cid := errs.NewCorrelationID()

	_, compA, dsA := rr2Composition(t, rr2Seed, 3)
	cursorA, releasedA, backlogA := rr2State(t, dsA, cid)
	if cursorA == 0 || releasedA == 0 || backlogA == 0 {
		t.Fatalf("fixture: nothing to restore (cursor=%d released=%d backlog=%d)", cursorA, releasedA, backlogA)
	}
	root := t.TempDir()
	if err := compA.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A pristine, never-driven target: everything it ends up holding must
	// have come from the bundle.
	apiB := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
	eB := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{Citizens: apiB})
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	if err := compB.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	dsB := compB.DeathServices()
	cursorB, releasedB, backlogB := rr2State(t, dsB, cid)
	if cursorB != cursorA || releasedB != releasedA || backlogB != backlogA {
		t.Fatalf("shard-PRESENT load did not restore exactly: cursor %d want %d, bodiesReleased %d want %d, awaitingBacklog %d want %d — "+
			"a zeroed result here means the F1 reset ran even though the shard WAS present (double reset)",
			cursorB, cursorA, releasedB, releasedA, backlogB, backlogA)
	}
	if cursorB == 0 {
		t.Fatal("shard-present load left the cursor at 0 — the F1 shard-less reset fired on a shard-PRESENT bundle")
	}

	// And the restored module must be caught up, not lagging: a further
	// month must neither re-deliver the restored page nor drop the new one.
	since, err := compB.state.citizens.DeathHandoffSince(int(cursorB), cid)
	if err != nil {
		t.Fatalf("DeathHandoffSince: %v", err)
	}
	if len(since) != 0 {
		t.Fatalf("restored cursor %d would re-deliver %d already-applied records", cursorB, len(since))
	}
	advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	cursorAfter, _, _ := rr2State(t, dsB, cid)
	handoff, err := compB.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(cursorAfter) != len(handoff) {
		t.Fatalf("post-restore month left cursor=%d against a %d-entry handoff stream", cursorAfter, len(handoff))
	}
}

// TestAttackBUG689_RR2_FINDING_OverLengthCursorPermanentlyDropsDeaths is
// attack angle C's real find, and this round's headline FINDING.
//
// F6 clamps a NEGATIVE decoded handoffCursor to 0 and logs MET-G5452. It
// does NOT bound the cursor from ABOVE. A cursor greater than the restored
// citizens handoff stream's length is exactly as impossible-a-state as a
// negative one (nothing in this codebase ever writes either), arrives by
// exactly the same route (a hand-edited / corrupt / format-skewed bundle),
// and is STRICTLY WORSE in consequence:
//
//   - negative: DeathHandoffSince clamps to 0, one safe re-delivery the
//     duplicate-death guard absorbs, then self-corrects within one month.
//   - over-length: DeathHandoffSince returns EMPTY (being "caught up" is
//     not an error), so IntakeFromHandoff is never called, so the cursor
//     NEVER ADVANCES — the module is permanently wedged. Every death the
//     city ever suffers from that point on is silently dropped: no body
//     record, no backlog, no error, no registry code, forever. The AC-14
//     conservation identity between citizens' realised deaths and
//     deathservices' BodiesReleased is permanently broken with no signal.
//
// This test pins the CURRENT (defective) behaviour so that closing the gap
// — clamping/logging an over-length cursor the way F6 clamps a negative
// one — fails this test and forces the pin to be re-read.
func TestAttackBUG689_RR2_FINDING_OverLengthCursorPermanentlyDropsDeaths(t *testing.T) {
	cid := errs.NewCorrelationID()

	// A shared bundle both the control and the hostile arms load from.
	_, compA, dsA := rr2Composition(t, rr2Seed, 2)
	// savedReleased is the body count the bundle itself carries — every
	// arm restores exactly these, so anything ABOVE this figure after the
	// following months is a NEW intake and anything equal to it is none.
	_, savedReleased, _ := rr2State(t, dsA, cid)
	root := t.TempDir()
	if err := compA.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// --- CONTROL: load the bundle untouched, drive 3 more months. The
	// cursor must track the handoff stream exactly (exactly-once).
	apiC := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
	eC := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(1))
	compC, err := Wire(eC, &Deps{Citizens: apiC})
	if err != nil {
		t.Fatalf("Wire control: %v", err)
	}
	if err := compC.Load(root); err != nil {
		t.Fatalf("Load control: %v", err)
	}
	for i := 0; i < 3; i++ {
		advanceInChunks(t, eC, int64(core.DailyTicksPerMonth))
	}
	ctrlCursor, ctrlReleased, _ := rr2State(t, compC.DeathServices(), cid)
	ctrlHandoff, err := compC.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff control: %v", err)
	}
	if int(ctrlCursor) != len(ctrlHandoff) || ctrlReleased != int64(len(ctrlHandoff)) {
		t.Fatalf("control arm is not exactly-once: cursor=%d released=%d handoff=%d", ctrlCursor, ctrlReleased, len(ctrlHandoff))
	}
	if len(ctrlHandoff) == 0 {
		t.Fatal("fixture: control arm produced no deaths at all")
	}

	for _, tc := range []struct {
		name   string
		cursor int64
	}{
		{"over_length", int64(len(ctrlHandoff)) * 1000},
		{"max_int64", math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostileRoot := t.TempDir()
			if err := compA.Save(hostileRoot); err != nil {
				t.Fatalf("Save hostile: %v", err)
			}
			rewriteDeathServicesCursor(t, hostileRoot, tc.cursor)

			api := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
			e := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(1))
			comp, err := Wire(e, &Deps{Citizens: api})
			if err != nil {
				t.Fatalf("Wire: %v", err)
			}
			// It must at minimum DECODE without a panic or a hard failure
			// (F6's own stance for the negative twin).
			if err := comp.Load(hostileRoot); err != nil {
				t.Fatalf("Load of a bundle with handoffCursor=%d failed: %v — an impossible-state cursor must decode, not hard-fail", tc.cursor, err)
			}
			ds := comp.DeathServices()
			decoded, _, _ := rr2State(t, ds, cid)

			// Drive the SAME three months the control arm drove; nothing
			// may panic (the int64->int narrowing in compose's
			// intakeDeathServices and the slice bound in
			// DeathHandoffSince are the two panic candidates).
			for i := 0; i < 3; i++ {
				advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
			}
			after, released, backlog := rr2State(t, ds, cid)
			handoff, err := comp.state.citizens.DeathHandoff(cid)
			if err != nil {
				t.Fatalf("DeathHandoff: %v", err)
			}
			if len(handoff) != len(ctrlHandoff) {
				t.Fatalf("fixture divergence: hostile arm handoff=%d, control=%d — the arms must be otherwise identical", len(handoff), len(ctrlHandoff))
			}

			// PIN: the cursor is installed verbatim (NOT clamped the way a
			// negative one is), never advances, and every death in those
			// three months is silently lost.
			if decoded != tc.cursor {
				t.Fatalf("FINDING CLOSED (re-read this pin): an over-length handoffCursor of %d decoded to %d — "+
					"the decode path now bounds the cursor from above. Update this test to assert the new, correct behaviour "+
					"(clamp + a registry code, mirroring F6/MET-G5452's negative-cursor treatment).", tc.cursor, decoded)
			}
			if after != tc.cursor {
				t.Fatalf("FINDING CLOSED (re-read this pin): the over-length cursor moved from %d to %d over three driven months — "+
					"self-correction now exists. Update this pin.", tc.cursor, after)
			}
			// released is measured against the bundle's OWN restored body
			// count: this arm must have intaken exactly ZERO NEW bodies
			// over the three months, while the control intaken every death
			// in the same stream.
			if released != savedReleased {
				t.Fatalf("FINDING CLOSED (re-read this pin): the over-length cursor arm intaken %d NEW bodies "+
					"(released %d vs the bundle's own %d, backlog %d) — deaths now reach deathservices despite the "+
					"poisoned cursor. Update this pin.", released-savedReleased, released, savedReleased, backlog)
			}
			if released >= ctrlReleased {
				t.Fatalf("FINDING CLOSED (re-read this pin): the poisoned arm kept pace with the control (%d vs %d). Update this pin.", released, ctrlReleased)
			}
			// The gap itself, stated as data: the control intaken every
			// death, this arm intaken NO NEW ones, and no error surfaced.
			t.Logf("FINDING (P2, open): handoffCursor=%d survives decode verbatim (F6 clamps only NEGATIVE). "+
				"Over three driven months the control arm reached %d bodies against a %d-record handoff stream; "+
				"this arm stayed at the bundle's own %d (ZERO new intakes, %d deaths silently dropped), "+
				"cursor still %d, no error and no registry code raised. deathservices is permanently wedged: "+
				"DeathHandoffSince returns empty for an at-or-past-length cursor, so IntakeFromHandoff is never called, "+
				"so the cursor never advances. Strictly worse than the negative case F6 DID guard, which self-corrects in one month.",
				tc.cursor, ctrlReleased, len(ctrlHandoff), savedReleased, ctrlReleased-released, after)
		})
	}
}

// TestAttackBUG689_RR2_DrainCapacityChangesBetweenSaveAndRestore is attack
// angle D: exactly-once must not depend on the drain capacity being
// constant. A crematorium registered on the RESTORE-side instance (before
// the load, so it is wiped by the restore, and after the load, so it is
// live for the following months) changes MonthlyDrainCapacity — the figure
// G2 now feeds into citizens.DeathQueue.RealiseDrained — while the handoff
// cursor arithmetic must stay exactly-once regardless.
func TestAttackBUG689_RR2_DrainCapacityChangesBetweenSaveAndRestore(t *testing.T) {
	cid := errs.NewCorrelationID()

	_, compA, _ := rr2Composition(t, rr2Seed, 2)
	root := t.TempDir()
	if err := compA.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	api := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
	e := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()

	// Registered BEFORE the load: the restore must wipe it (crematoria are
	// serialized state), so capacity must fall back to the bundle's.
	if err := ds.RegisterCrematorium("crem-pre", cid); err != nil {
		t.Fatalf("RegisterCrematorium pre: %v", err)
	}
	capBefore := ds.MonthlyDrainCapacity(0)
	if err := comp.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	capAfterLoad := ds.MonthlyDrainCapacity(0)
	if capAfterLoad >= capBefore {
		t.Fatalf("a crematorium registered before the load survived it: capacity %d -> %d, want a DROP (the restore must replace the crematoria map)", capBefore, capAfterLoad)
	}

	// Registered AFTER the load: live for the following months, so the
	// wired drain the death queue consults genuinely varies across the
	// boundary.
	if err := ds.RegisterCrematorium("crem-post", cid); err != nil {
		t.Fatalf("RegisterCrematorium post: %v", err)
	}
	if got := ds.MonthlyDrainCapacity(0); got <= capAfterLoad {
		t.Fatalf("post-load crematorium did not raise capacity: %d -> %d", capAfterLoad, got)
	}

	for i := 0; i < 3; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	cursor, released, _ := rr2State(t, ds, cid)
	handoff, err := comp.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(cursor) != len(handoff) {
		t.Fatalf("exactly-once broken with a VARYING drain capacity: cursor=%d handoff=%d", cursor, len(handoff))
	}
	if released != int64(len(handoff)) {
		t.Fatalf("bodiesReleased=%d != handoff length %d with a varying drain capacity", released, len(handoff))
	}
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cons.Sum() != cons.BodiesReleased {
		t.Fatalf("AC-14 conservation broken across a varying-capacity restore: Sum()=%d BodiesReleased=%d (%+v)", cons.Sum(), cons.BodiesReleased, cons)
	}
}

// TestAttackBUG689_RR2_DeterminismAcrossPoolSizesWithWiredDrain is attack
// angle E. G2 put a NEW read of shared, lock-protected module state
// (deathservices.MonthlyDrainCapacity) on the death-realisation path,
// which every worker-pool size must observe identically. Pool sizes 1, 4
// and 20 must produce byte-identical PopulationHash, an identical cursor,
// and identical awaiting/conservation figures.
func TestAttackBUG689_RR2_DeterminismAcrossPoolSizesWithWiredDrain(t *testing.T) {
	cid := errs.NewCorrelationID()
	type result struct {
		hash     [32]byte
		cursor   int64
		released int64
		backlog  int
	}
	var ref *result
	for _, pool := range []int{1, 4, 20} {
		api := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
		e := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(pool))
		comp, err := Wire(e, &Deps{Citizens: api})
		if err != nil {
			t.Fatalf("Wire(pool=%d): %v", pool, err)
		}
		// A crematorium so the wired drain is a NON-TRIVIAL, live figure
		// during the run rather than the default-data constant.
		if err := comp.DeathServices().RegisterCrematorium("crem-det", cid); err != nil {
			t.Fatalf("RegisterCrematorium(pool=%d): %v", pool, err)
		}
		for i := 0; i < 4; i++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		}
		cursor, released, backlog := rr2State(t, comp.DeathServices(), cid)
		got := result{hash: comp.PopulationHash(), cursor: cursor, released: released, backlog: backlog}
		if released == 0 {
			t.Fatalf("fixture(pool=%d): no deaths intaken", pool)
		}
		if ref == nil {
			ref = &got
			continue
		}
		if got != *ref {
			t.Fatalf("pool size %d diverged from pool size 1 with the wired drain capacity live: "+
				"hash %x vs %x, cursor %d vs %d, released %d vs %d, backlog %d vs %d (GR#21)",
				pool, got.hash, ref.hash, got.cursor, ref.cursor, got.released, ref.released, got.backlog, ref.backlog)
		}
	}
}

// TestAttackBUG689_RR2_LockInversionHammer is attack angle A's runtime
// half. G2 introduced a genuinely new lock-nesting edge — citizens.
// DeathQueue.RealiseDrained holds q.mu and calls deathservices.
// MonthlyDrainCapacity, which takes d.mu — where the old hand-rolled
// closure called the lock-free HearseMonthlyBudget. The author's audit
// claims the edge is one-directional.
//
// STATIC HALF (recorded here, checked by grep at review time rather than
// at runtime): internal/engine/deathservices holds NO reference to
// citizens.CitizensAPI or citizens.DeathQueue at all — its only injected
// dependencies are engine.services and engine.logistics, and NEITHER of
// those packages imports engine/citizens or engine/deathservices, so no
// d.mu -> q.mu path can exist through them either. The lock graph
// q.mu -> d.mu -> {services.mu, logistics.mu} is a DAG.
//
// RUNTIME HALF (this test): 8 concurrent readers hammering the exported
// deathservices surface — including MonthlyDrainCapacity, the method now
// reached UNDER q.mu — while the engine ticks 12 months and a save and a
// full Load happen mid-run. Under -race this is the inversion/data-race
// probe. A genuine inversion deadlocks (the test harness's own timeout
// reports it); a genuine race is reported by -race.
func TestAttackBUG689_RR2_LockInversionHammer(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, rr2Seed)
	e := core.NewEngine(core.WithWorldSeed(rr2Seed), core.WithPoolSize(4))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()
	if err := ds.RegisterCrematorium("crem-hammer", cid); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	var stop atomic.Bool
	var reads atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for !stop.Load() {
				// The q.mu -> d.mu edge's own destination, hammered
				// directly and concurrently with the tick that reaches it
				// through RealiseDrained.
				_ = ds.MonthlyDrainCapacity(int64(g))
				if _, err := ds.HandoffCursor(cid); err != nil {
					t.Errorf("HandoffCursor under hammer: %v", err)
					return
				}
				if _, err := ds.Snapshot(cid); err != nil {
					t.Errorf("Snapshot under hammer: %v", err)
					return
				}
				if _, err := ds.AwaitingBacklog(cid); err != nil {
					t.Errorf("AwaitingBacklog under hammer: %v", err)
					return
				}
				if _, err := ds.AwaitingSorted(cid); err != nil {
					t.Errorf("AwaitingSorted under hammer: %v", err)
					return
				}
				if _, err := ds.DispensationActive(cid); err != nil {
					t.Errorf("DispensationActive under hammer: %v", err)
					return
				}
				reads.Add(1)
			}
		}(g)
	}

	root := t.TempDir()
	const months = 12
	perMonth := int64(core.DailyTicksPerMonth)
	for i := 0; i < months; i++ {
		// Save/Load land MID-MONTH (a third of the way in), so the readers
		// are hammering across a participant snapshot AND a participant
		// restore, not only across ordinary ticks.
		advanceInChunks(t, e, perMonth/3)
		if i == 3 {
			if err := comp.Save(root); err != nil {
				t.Fatalf("Save mid-run: %v", err)
			}
		}
		if i == 7 {
			if err := comp.Load(root); err != nil {
				t.Fatalf("Load mid-run: %v", err)
			}
		}
		advanceInChunks(t, e, perMonth-perMonth/3)
	}
	stop.Store(true)
	wg.Wait()

	if reads.Load() == 0 {
		t.Fatal("the hammer never completed a single read pass — the concurrency probe is vacuous")
	}
	// The run must still be internally consistent after all that.
	cursor, released, _ := rr2State(t, ds, cid)
	handoff, err := comp.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(cursor) != len(handoff) {
		t.Fatalf("after %d months with a mid-run save+load and %d concurrent read passes, cursor=%d against a %d-entry handoff stream",
			months, reads.Load(), cursor, len(handoff))
	}
	if released != int64(len(handoff)) {
		t.Fatalf("bodiesReleased=%d != handoff length %d after the hammer", released, len(handoff))
	}
	t.Logf("hammer completed %d concurrent read passes across %d months, one mid-month Save and one mid-month Load", reads.Load(), months)
}
