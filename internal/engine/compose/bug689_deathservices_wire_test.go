package compose

import (
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-689 — engine.deathservices compose wiring: the P1 filed by the
// FEAT-087 AC-20 round ("built but not wired" -- no compose participant,
// Intake never called by the engine loop, no save/restore). This file
// proves the wiring end to end: monthly Intake driven from the live loop,
// exactly-once paging via the DeathHandoffSince cursor, save/restore
// round-trip, and determinism.

// bug689Seed is a fixed world seed dedicated to this file's tests, distinct
// from roundTripSeed (save_roundtrip_test.go) and the various single-digit
// seeds other compose_test.go tests use, so a future seed collision never
// makes an unrelated test's assumption about this file's citizen ids
// silently wrong.
const bug689Seed = uint64(689)

// buildGuaranteedDeathCitizensAPI builds a fresh CitizensAPI whose Gompertz-
// Makeham hazard is clamped to 1.0 for every citizen spawnCitizens mints at
// Wire time -- mirrors TestFEAT169_LiveDeaths_RealMortality's own preAge
// recipe exactly (compose_test.go): advancing ~200 sim years of EMPTY-store
// months before Wire ever seeds a citizen means every citizen born "now"
// (at Wire's mint month) is immediately, deterministically ancient relative
// to the hazard curve's clamp threshold. preAgeMonths=2403 is that same
// test's own verified value (April, a mild non-emergency month under
// data/mortality.json's weatherEmergency thresholds -- see that test's own
// doc comment for why 2403 and not the rounder 2400).
func buildGuaranteedDeathCitizensAPI(t *testing.T, seed uint64) *citizens.CitizensAPI {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const preAgeMonths = 2403
	for i := 0; i < preAgeMonths; i++ {
		if err := api.AdvanceMonth(cid); err != nil {
			t.Fatalf("pre-age AdvanceMonth: %v", err)
		}
	}
	return api
}

// TestBUG689_MonthlyIntakeExactlyOnce drives a real, ancient-seeded
// composition through the FULL live loop (Wire -> AdvanceTicks, no direct
// package-internal calls) and proves:
//
//  1. engine.deathservices actually receives real deaths (AC per the BUG-689
//     brief: "Intake never called by the engine loop" is fixed);
//  2. the exactly-once identity holds: deathservices' own persisted cursor
//     equals the citizens handoff stream's full length (nothing double-
//     paged, nothing left un-paged) after the run;
//  3. deathservices' AC-14 six-term conservation identity
//     (BodiesReleased == Sum() of the five terminal/non-terminal buckets)
//     holds, with BodiesReleased == the handoff length too (every released
//     death was intaken exactly once, none dropped, none duplicated).
func TestBUG689_MonthlyIntakeExactlyOnce(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, bug689Seed)

	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(bug689Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens: api,
		InvariantOpts: []invariant.HookOption{
			invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()
	if ds == nil {
		t.Fatal("Composition.DeathServices() = nil, want a wired instance (BUG-689: deathservices must be wired by default)")
	}

	// Drive 4 months (ceil(seedCitizenCount/monthlyDeathBudget)+1, mirroring
	// TestFEAT169_LiveDeaths_RealMortality's own drain-window sizing) so
	// the whole ancient seed cohort's hazard-selected deaths are fully
	// realised and handed off.
	const months = 4
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}

	if got := comp.VitalDeaths(); got <= 0 {
		t.Fatalf("VitalDeaths() = %d, want > 0 (this test's whole point is real deaths flowing through the live loop)", got)
	}

	cursor, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	fullHandoff, err := api.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if int(cursor) != len(fullHandoff) {
		t.Fatalf("exactly-once paging violated: HandoffCursor()=%d, citizens handoff stream length=%d — every released death must be paged exactly once", cursor, len(fullHandoff))
	}
	if cursor == 0 {
		t.Fatal("HandoffCursor() = 0 after a driven run with real deaths — the monthly Intake hook never ran (BUG-689 regression)")
	}

	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cons.BodiesReleased != int64(len(fullHandoff)) {
		t.Fatalf("BodiesReleased=%d, want %d (== the citizens handoff stream length -- every released death must reach deathservices' Intake exactly once)", cons.BodiesReleased, len(fullHandoff))
	}
	if got := cons.Sum(); got != cons.BodiesReleased {
		t.Fatalf("AC-14 conservation identity broken: Sum()=%d != BodiesReleased=%d (%+v)", got, cons.BodiesReleased, cons)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations while deathservices' monthly Intake was live, want 0", got)
	}
}

// TestBUG689_ExactlyOnceAcrossSaveRestore proves the AC-20 round's exact
// warning wrong for the LANDED wiring: "an unserialized module + a
// serialized handoff stream = double-delivery on restore". After Save then
// Load, the restored deathservices instance's own persisted cursor must
// already be caught up against the (also-restored) citizens handoff stream
// — DeathHandoffSince(restoredCursor) on the freshly loaded composition
// must return EMPTY, proving a subsequent monthly tick would neither
// re-intake anything already applied before the save nor drop anything.
func TestBUG689_ExactlyOnceAcrossSaveRestore(t *testing.T) {
	cid := errs.NewCorrelationID()

	// --- A: drive a real, ancient-seeded composition partway, then save.
	apiA := buildGuaranteedDeathCitizensAPI(t, bug689Seed)
	eA := core.NewEngine(core.WithWorldSeed(bug689Seed), core.WithPoolSize(1))
	compA, err := Wire(eA, &Deps{Citizens: apiA})
	if err != nil {
		t.Fatalf("Wire A: %v", err)
	}
	const monthsBeforeSave = 2
	for i := 0; i < monthsBeforeSave; i++ {
		advanceInChunks(t, eA, int64(core.DailyTicksPerMonth))
	}
	dsA := compA.DeathServices()
	cursorA, err := dsA.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor A: %v", err)
	}
	consA, err := dsA.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot A: %v", err)
	}
	if cursorA == 0 || consA.BodiesReleased == 0 {
		t.Fatalf("test fixture did not produce any pre-save deaths (cursorA=%d, BodiesReleased=%d) — the exactly-once boundary proof needs real state to carry across", cursorA, consA.BodiesReleased)
	}

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// --- B: a FRESH composition, then Load A's save into it.
	apiB := buildGuaranteedDeathCitizensAPI(t, bug689Seed)
	eB := core.NewEngine(core.WithWorldSeed(bug689Seed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{Citizens: apiB})
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	dsB := compB.DeathServices()

	// Round-trip fidelity: the restored cursor/conservation snapshot must
	// match A's at the save point exactly.
	cursorB, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor B: %v", err)
	}
	if cursorB != cursorA {
		t.Fatalf("HandoffCursor did not round-trip: A=%d, B(restored)=%d", cursorA, cursorB)
	}
	consB, err := dsB.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot B: %v", err)
	}
	if consB != consA {
		t.Fatalf("Conservation snapshot did not round-trip: A=%+v, B(restored)=%+v", consA, consB)
	}

	// THE exactly-once proof: paging the (also-restored) citizens handoff
	// stream from the restored cursor must be a no-op — every entry that
	// was already applied before the save is NOT handed back for
	// re-application. This is precisely the double-delivery hazard the
	// AC-20 round warned an unserialized module would hit.
	sinceB, err := compB.state.citizens.DeathHandoffSince(int(cursorB), cid)
	if err != nil {
		t.Fatalf("DeathHandoffSince (restored): %v", err)
	}
	if len(sinceB) != 0 {
		t.Fatalf("double-delivery hazard: DeathHandoffSince(restoredCursor) on the freshly loaded composition returned %d records, want 0 — a restore must never re-hand-off already-applied deaths", len(sinceB))
	}

	// --- Continue driving B post-restore: NEW deaths (against B's own
	// restarted clock — Composition.Load does not restore the clock, a
	// documented pre-existing limitation, FEAT-1972079944) must still
	// intake correctly and keep the conservation identity holding, proving
	// the mechanism is not merely inert after a restore.
	const monthsAfterLoad = 3
	for i := 0; i < monthsAfterLoad; i++ {
		advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	}
	cursorB2, err := dsB.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor B (post-continue): %v", err)
	}
	fullHandoffB, err := compB.state.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff B (post-continue): %v", err)
	}
	if int(cursorB2) != len(fullHandoffB) {
		t.Fatalf("exactly-once paging violated post-restore: HandoffCursor()=%d, citizens handoff length=%d", cursorB2, len(fullHandoffB))
	}
	consB2, err := dsB.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot B (post-continue): %v", err)
	}
	if consB2.BodiesReleased != int64(len(fullHandoffB)) {
		t.Fatalf("BodiesReleased=%d, want %d post-restore continuation", consB2.BodiesReleased, len(fullHandoffB))
	}
	if got := consB2.Sum(); got != consB2.BodiesReleased {
		t.Fatalf("AC-14 conservation identity broken post-restore: Sum()=%d != BodiesReleased=%d (%+v)", got, consB2.BodiesReleased, consB2)
	}
	if consB2.BodiesReleased <= consB.BodiesReleased {
		t.Fatalf("no NEW deaths were intaken after continuing past the restore point (BodiesReleased stayed at %d) — the post-restore continuation proof needs genuine new activity", consB2.BodiesReleased)
	}
}

// TestBUG689_NilWiringFailSafe proves item 4 of the BUG-689 brief directly:
// simState.intakeDeathServices (the monthly hook's body) is a documented
// no-op, never a panic, when either dependency is nil — the defensive path
// for a future lower-level harness that builds a *simState without going
// through the real Wire construction (Wire itself never leaves either nil
// today).
func TestBUG689_NilWiringFailSafe(t *testing.T) {
	st := &simState{cid: errs.NewCorrelationID()}
	if err := st.intakeDeathServices(1); err != nil {
		t.Fatalf("intakeDeathServices with nil citizens/deathServices = %v, want nil (documented no-op)", err)
	}
}

// TestBUG689_DrainCapacityNeverBindsForDefaultData pins the numeric
// safety-margin the BUG-689 wiring's backward-compatibility argument relies
// on: for the CURRENT data/deathservices.json + data/mortality.json
// figures, the injected hearse-throughput drain (ASM-580) must never bind
// tighter than the ordinary mortality smoothing budget, so wiring
// deathservices on by default reproduces every pre-BUG-689 population
// trajectory byte-for-byte (RealiseDrained's own doc: "min(ordinary
// budget, injected drain, queued)" — the drain is a no-op whenever it is
// >= the budget it is compared against). A future data-file edit that
// breaks this margin is a genuine, deliberate balance change — this test
// makes that change visible immediately rather than as a silent regression
// across the whole existing baseline-one test suite.
func TestBUG689_DrainCapacityNeverBindsForDefaultData(t *testing.T) {
	cid := errs.NewCorrelationID()
	ds, err := deathservices.LoadDefault(cid)
	if err != nil {
		t.Fatalf("deathservices.LoadDefault: %v", err)
	}
	hearseBudget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	mcfg, err := citizens.LoadDefaultMortalityConfig(cid)
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	deathBudget := mcfg.MonthlyDeathBudget()
	if int64(hearseBudget) < int64(deathBudget) {
		t.Fatalf("hearseMonthlyTransportBudget=%d < monthlyDeathBudget=%d: the injected drain now BINDS on the ordinary path for default data, which changes population dynamics for every existing baseline-one test — this is a deliberate balance change that needs its own review, not a silent side effect of BUG-689's wiring", hearseBudget, deathBudget)
	}
}

// TestBUG689_DeterministicAcrossPoolSizes proves the wired monthly Intake
// carries determinism through the composition (mirrors
// TestFEAT169_DeterministicAcrossPoolSizes) at pool sizes 1, 4, and 20:
// PopulationHash AND deathservices' own conservation/cursor state must be
// byte-identical across all three.
func TestBUG689_DeterministicAcrossPoolSizes(t *testing.T) {
	cid := errs.NewCorrelationID()
	const months = 3

	run := func(poolSize int) ([32]byte, deathservices.Conservation, int64) {
		api := buildGuaranteedDeathCitizensAPI(t, bug689Seed)
		e := core.NewEngine(core.WithWorldSeed(bug689Seed), core.WithPoolSize(poolSize))
		comp, err := Wire(e, &Deps{Citizens: api})
		if err != nil {
			t.Fatalf("Wire (pool %d): %v", poolSize, err)
		}
		for i := 0; i < months; i++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		}
		cons, err := comp.DeathServices().Snapshot(cid)
		if err != nil {
			t.Fatalf("Snapshot (pool %d): %v", poolSize, err)
		}
		cursor, err := comp.DeathServices().HandoffCursor(cid)
		if err != nil {
			t.Fatalf("HandoffCursor (pool %d): %v", poolSize, err)
		}
		return comp.PopulationHash(), cons, cursor
	}

	hash1, cons1, cursor1 := run(1)
	hash4, cons4, cursor4 := run(4)
	hash20, cons20, cursor20 := run(20)

	if hash1 != hash4 || hash1 != hash20 {
		t.Fatalf("PopulationHash differs across pool sizes: 1=%x 4=%x 20=%x", hash1, hash4, hash20)
	}
	if cons1 != cons4 || cons1 != cons20 {
		t.Fatalf("deathservices Conservation snapshot differs across pool sizes: 1=%+v 4=%+v 20=%+v", cons1, cons4, cons20)
	}
	if cursor1 != cursor4 || cursor1 != cursor20 {
		t.Fatalf("deathservices HandoffCursor differs across pool sizes: 1=%d 4=%d 20=%d", cursor1, cursor4, cursor20)
	}
	if cursor1 == 0 {
		t.Fatal("HandoffCursor() = 0 across all pool sizes — this determinism proof needs real deaths in play")
	}
}
