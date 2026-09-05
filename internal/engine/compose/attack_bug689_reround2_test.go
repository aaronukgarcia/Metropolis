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

// TestAttackBUG689_RR2_OverLengthCursorSelfCorrectsNoDroppedDeaths is
// attack angle C's real find, this round's headline FINDING -- CLOSED by
// BUG-720/BUG-725.
//
// F6 clamps a NEGATIVE decoded handoffCursor to 0 and logs MET-G5452. It
// does NOT bound the cursor from ABOVE at decode time. A cursor greater
// than the restored citizens handoff stream's length is exactly as
// impossible-a-state as a negative one (nothing in this codebase ever
// writes either), arrives by exactly the same route (a hand-edited /
// corrupt / format-skewed bundle), and WOULD BE strictly worse in
// consequence if left unguarded:
//
//   - negative: DeathHandoffSince clamps to 0, one safe re-delivery the
//     duplicate-death guard absorbs, then self-corrects within one month.
//   - over-length (unguarded): DeathHandoffSince returns EMPTY (being
//     "caught up" is not an error), so IntakeFromHandoff is never called,
//     so the cursor NEVER ADVANCES — the module would be permanently
//     wedged, silently dropping every death from that point on: no body
//     record, no backlog, no error, no registry code, forever. The AC-14
//     conservation identity between citizens' realised deaths and
//     deathservices' BodiesReleased would desync from its own body map
//     with no signal.
//
// FIXED (BUG-720 P2 follow-up, detail added BUG-725): decode itself stays
// unchanged (F6's clamp is negative-only -- decode never holds a citizens
// reference, GR#20, so it cannot know the real stream length to bound
// against). The correction instead lives at the composition root, the one
// place with both APIs: compose's intakeDeathServices detects a decoded
// cursor beyond the live handoff stream's length on its first call and
// resets it via [deathservices.DeathServicesAPI.ResetHandoffCursor], then
// re-reads the full stream from 0. This test now ASSERTS the fix rather
// than pinning the defect: it drives a hostile arm alongside a control arm
// over the SAME three months and requires the hostile arm to end up
// bit-for-bit caught up with the control (cursor, BodiesReleased) -- i.e.
// PROVES zero deaths are dropped, counted against the control's own
// exactly-once figures, for both the over-length and math.MaxInt64 shapes.
// If this test ever starts failing, the fix has regressed and the gap is
// open again -- re-read this comment before touching either direction of
// ErrCorruptHandoffCursor.
func TestAttackBUG689_RR2_OverLengthCursorSelfCorrectsNoDroppedDeaths(t *testing.T) {
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

			// FIX (BUG-720 P2 follow-up): decode itself is UNCHANGED — the
			// raw wire value is still installed verbatim by
			// applyLoadRecord (F6's negative-only clamp is the one
			// decode-time guard, and an over-length value is only
			// detectable relative to citizens' live stream length, which
			// deathservices' decode step never has access to, GR#20 — see
			// compose's intakeDeathServices doc comment for the full
			// argument). The correction instead lives at the composition
			// root, the one place with both APIs: the FIRST driven
			// month's intake finds DeathHandoffSince(cursor) empty,
			// discovers cursor exceeds the real stream length, resets the
			// persisted cursor via [deathservices.DeathServicesAPI.
			// ResetHandoffCursor], and re-reads the full stream from 0 —
			// so this hostile arm ends up bit-for-bit CAUGHT UP with the
			// control arm after the same three driven months, not merely
			// "no longer wedged forever".
			if decoded != tc.cursor {
				t.Fatalf("decode-time behaviour changed: an over-length handoffCursor of %d decoded to %d — "+
					"applyLoadRecord is documented to install a decoded cursor verbatim (only F6's negative guard fires "+
					"at decode time); if this now differs, update this assertion to match the new decode contract.",
					tc.cursor, decoded)
			}
			if after != ctrlCursor {
				t.Fatalf("self-correction should converge on the SAME cursor the control arm reached: hostile=%d control=%d", after, ctrlCursor)
			}
			if released != ctrlReleased {
				t.Fatalf("self-correction should intake the FULL stream (replaying already-consumed entries as safely-absorbed "+
					"duplicates, H4 policy (b)), catching up to the control arm exactly: hostile released=%d control=%d (bundle's own restored %d)",
					released, ctrlReleased, savedReleased)
			}
			t.Logf("BUG-720 P2 follow-up CLOSED: handoffCursor=%d self-corrected to %d (== control's %d) over three driven "+
				"months via compose's intakeDeathServices detecting cursor > len(citizens' live stream) and calling "+
				"ResetHandoffCursor — released %d bodies (== control's %d), backlog %d (BUG-720's own run loop now "+
				"processes dispensation even with no cemetery/crematorium registered), zero silently dropped, MET-G5452 raised once.",
				tc.cursor, after, ctrlCursor, released, ctrlReleased, backlog)
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
