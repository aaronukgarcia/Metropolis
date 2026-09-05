package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug720_deathservices_run_test.go — BUG-720: crematoriums/cemeteries/
// hearses never RUN (compose drove Intake only, BUG-689). These tests
// exercise deathServicesRunHook's daily RunHearseTransport/Cremate/
// Dispense sweep end to end through the real Wire -> AdvanceTicks loop
// (never a direct package-internal call standing in for the tick), the
// way this package's existing BUG-689 tests do.
//
// bug720Seed is this file's own dedicated world seed (distinct from
// attackSeed/rr2Seed/bug689Seed) so an edit to another attack file cannot
// silently change this file's death-count assumptions.
const bug720Seed = uint64(720202)

// syntheticDeaths builds n distinct RealisedDeath records, all landed in
// month 0, all non-emergency unless flagged — used to drive deathservices'
// backlog directly via Intake (bypassing the real per-citizen mortality
// pipeline, which would need hundreds of simulated months to reach a
// 5000+ body backlog). Intake is deathservices' own public,
// directly-callable surface for exactly this (api.go's doc: "a caller
// with no data directory handy in a test"), and never touches the
// handoffCursor BUG-689's IntakeFromHandoff owns, so this cannot desync
// compose's own monthly intake hook.
func syntheticDeaths(n int, startID uint64, emergency bool) []citizens.RealisedDeath {
	out := make([]citizens.RealisedDeath, n)
	for i := 0; i < n; i++ {
		out[i] = citizens.RealisedDeath{CitizenID: startID + uint64(i), DeathMonth: 0, EmergencyFlag: emergency}
	}
	return out
}

// requireConservation re-checks AC-14's six-term identity directly against
// a fresh Snapshot (never trusting a stale/previously-read value) —
// BodiesReleased must equal the sum of every disposal-state bucket, every
// month this bug's disposal channels run, not just when the backlog is
// fully drained.
func requireConservation(t *testing.T, ds *deathservices.DeathServicesAPI, cid string) deathservices.Conservation {
	t.Helper()
	cons, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	sum := cons.BodiesAwaitingHandling + cons.BodiesEnRoute + cons.BodiesBuried + cons.BodiesCremated + cons.BodiesHandledByDispensation
	if sum != cons.BodiesReleased {
		t.Fatalf("AC-14 conservation violated: released=%d but awaiting+enroute+buried+cremated+dispensed=%d (%+v)",
			cons.BodiesReleased, sum, cons)
	}
	return cons
}

// TestBUG720_CrematoriumDrainsBacklogAndConserves is the primary "it now
// RUNS" proof: with one crematorium registered and a real, ancient-seeded
// mortality pipeline driving genuine deaths, the Awaiting backlog drains
// toward zero and every cremation is conserved against releases every
// month, not just at the end.
func TestBUG720_CrematoriumDrainsBacklogAndConserves(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, bug720Seed)
	e := core.NewEngine(core.WithWorldSeed(bug720Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: []string{"crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()

	var lastCons deathservices.Conservation
	for month := 0; month < 8; month++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		lastCons = requireConservation(t, ds, cid)
	}

	if lastCons.BodiesReleased == 0 {
		t.Fatal("fixture produced no deaths")
	}
	if lastCons.BodiesCremated == 0 {
		t.Fatalf("BUG-720 regression: zero bodies cremated after 8 months with a crematorium registered (%+v)", lastCons)
	}
	backlog, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	// "drains to ~0": the crematorium's daily throughput (12/d, spec seed)
	// dwarfs this fixture's death rate, so the backlog should be a small
	// remainder (in-flight this month), never anywhere near the full
	// released count BUG-689's own pinned FINDING measured (backlog ==
	// released, i.e. 100% stuck).
	if int64(backlog)*2 >= lastCons.BodiesReleased {
		t.Fatalf("backlog did not drain toward zero: backlog=%d released=%d (%+v)", backlog, lastCons.BodiesReleased, lastCons)
	}
	t.Logf("BUG-720: after 8 months, released=%d cremated=%d backlog=%d (%+v)",
		lastCons.BodiesReleased, lastCons.BodiesCremated, backlog, lastCons)
}

// TestBUG720_NoRegistration_BacklogGrowsThenDispensationActivatesAtThreshold
// proves the "with none registered, backlog grows and dispensation
// activates at the threshold" AC: with zero cemeteries/crematoria
// registered, a backlog Intake'd directly (see syntheticDeaths) sits
// entirely Awaiting until it reaches data/deathservices.json's
// backlogCapacityCeiling (5000, placeholder), at which point
// deathServicesRunHook's daily sweep raises dispensation — then, because
// dispensation's own multi-body van channel IS one of the disposal
// channels this bug wires, the backlog drains and dispensation reverts to
// inactive once it reaches zero (this composition's own documented
// "backlog==0" reversion rule — see runDeathServices' doc comment for why
// that is the safe condition, not "back under the ceiling").
func TestBUG720_NoRegistration_BacklogGrowsThenDispensationActivatesAtThreshold(t *testing.T) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(bug720Seed+1, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(bug720Seed+1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.DeathServices()

	cfg, err := ds.Config(cid)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	ceiling := cfg.BacklogCapacityCeiling()

	// One tick BEFORE the backlog would cross the ceiling: dispensation
	// must still be inactive (the raise is threshold-triggered, not
	// eager).
	if _, err := ds.Intake(syntheticDeaths(int(ceiling)-1, 1, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake (below ceiling): %v", err)
	}
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	backlog, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if int64(backlog) >= ceiling {
		t.Fatalf("fixture error: backlog=%d already at/above ceiling=%d before the crossing Intake", backlog, ceiling)
	}
	if active, aerr := ds.DispensationActive(cid); aerr != nil || active {
		t.Fatalf("dispensation active BELOW the ceiling (backlog=%d, ceiling=%d, activeErr=%v)", backlog, ceiling, aerr)
	}

	// Cross the ceiling.
	if _, err := ds.Intake(syntheticDeaths(2, uint64(ceiling)+1000, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake (crossing ceiling): %v", err)
	}
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	backlogAfterCross, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	// No slack assertion on backlogAfterCross itself: once dispensation
	// activates, this composition's run loop drains it via REPEATED
	// Dispense calls in the SAME day until the full monthly budget is
	// exhausted (the "24x7 emergency operation" reading — see
	// runDeathServices' own doc comment) rather than one van's worth per
	// day, so a full month can legitimately drain most or all of the
	// crossing backlog already. What this test actually asserts is
	// activation itself, and eventual full drain + reversion below.
	activeAfterCross, err := ds.DispensationActive(cid)
	if err != nil {
		t.Fatalf("DispensationActive: %v", err)
	}
	if !activeAfterCross {
		t.Fatalf("BUG-720 regression: dispensation did not activate at/above the ceiling (backlog=%d, ceiling=%d)", backlogAfterCross, ceiling)
	}

	// Drive enough months for the multi-body dispensation channel to
	// fully drain the backlog (DispensationMonthlyBudget = hearse budget x
	// multiplier = 300*4 = 1200/month, spec-seed placeholders — the
	// backlog here is a small multiple of the ceiling, so a handful of
	// months suffices; generous bound to stay robust to a future balance
	// pass changing those placeholders).
	for i := 0; i < 12; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		b, err := ds.AwaitingBacklog(cid)
		if err != nil {
			t.Fatalf("AwaitingBacklog: %v", err)
		}
		if b == 0 {
			break
		}
	}
	finalBacklog, err := ds.AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if finalBacklog != 0 {
		t.Fatalf("backlog never fully drained after 12 more months: %d remaining", finalBacklog)
	}
	finalActive, err := ds.DispensationActive(cid)
	if err != nil {
		t.Fatalf("DispensationActive: %v", err)
	}
	if finalActive {
		t.Fatalf("BUG-720 regression: dispensation still active once backlog fully drained")
	}
	requireConservation(t, ds, cid)
}

// TestBUG720_SaveRestoreMidBacklogExact proves a save/load boundary taken
// mid-backlog (some bodies still Awaiting, some already disposed of)
// round-trips every disposal-state count exactly — the same "restores
// exactly" bar BUG-689's own save-wiring tests hold Intake/handoffCursor
// to, now extended to the disposal counters this bug adds no NEW
// participant fields for (Bury/Cremate/Dispense all mutate state that
// participant.go's existing schema already serializes: bodies map state,
// hearse/dispensation counters).
func TestBUG720_SaveRestoreMidBacklogExact(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, bug720Seed+2)
	e := core.NewEngine(core.WithWorldSeed(bug720Seed+2), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: []string{"crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for i := 0; i < 3; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	before := requireConservation(t, comp.DeathServices(), cid)
	beforeBacklog, err := comp.DeathServices().AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if before.BodiesReleased == 0 {
		t.Fatal("fixture produced no deaths")
	}

	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	api2 := buildGuaranteedDeathCitizensAPI(t, bug720Seed+2)
	e2 := core.NewEngine(core.WithWorldSeed(bug720Seed+2), core.WithPoolSize(1))
	comp2, err := Wire(e2, &Deps{
		Citizens:               api2,
		DeathServiceCrematoria: []string{"crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire (restore): %v", err)
	}
	if err := comp2.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	after := requireConservation(t, comp2.DeathServices(), cid)
	afterBacklog, err := comp2.DeathServices().AwaitingBacklog(cid)
	if err != nil {
		t.Fatalf("AwaitingBacklog (restored): %v", err)
	}

	if before != after {
		t.Fatalf("save/restore did not round-trip disposal state exactly: before=%+v after=%+v", before, after)
	}
	if beforeBacklog != afterBacklog {
		t.Fatalf("save/restore backlog mismatch: before=%d after=%d", beforeBacklog, afterBacklog)
	}
}

// TestBUG720_DeterminismAcrossPoolSizes proves the daily run loop (GR#21)
// produces byte-identical disposal outcomes regardless of the engine's
// shard pool size — the SingleShardHook contract deathServicesRunHook
// declares (SingleShard() == true) is exactly what this pins.
func TestBUG720_DeterminismAcrossPoolSizes(t *testing.T) {
	cid := errs.NewCorrelationID()
	var results []deathservices.Conservation
	for _, pool := range []int{1, 4, 20} {
		api := buildGuaranteedDeathCitizensAPI(t, bug720Seed+3)
		e := core.NewEngine(core.WithWorldSeed(bug720Seed+3), core.WithPoolSize(pool))
		comp, err := Wire(e, &Deps{
			Citizens:               api,
			DeathServiceCrematoria: []string{"crem-1"},
			DeathServiceCemeteries: []DeathServiceCemeterySpec{{ID: "cem-1"}},
		})
		if err != nil {
			t.Fatalf("Wire (pool=%d): %v", pool, err)
		}
		for i := 0; i < 5; i++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		}
		cons := requireConservation(t, comp.DeathServices(), cid)
		results = append(results, cons)
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("determinism violated: pool result %d = %+v, want %+v", i, results[i], results[0])
		}
	}
	if results[0].BodiesReleased == 0 {
		t.Fatal("fixture produced no deaths")
	}
	t.Logf("BUG-720 determinism across pool sizes 1/4/20: %+v", results[0])
}

// TestBUG720_CremationCostPostedExactlyOncePerBody proves Cremate's
// returned cost is posted to engine.finance's SettleOpex EXACTLY once per
// cremated body — the sanctioned "service operating expenditure" path
// (finance/stages.go's SettleOpex, CatOpex).
//
// [finance.FinanceAPI.OpexTotal] is documented as "the TICK's posted
// opex" — [finance.FinanceAPI.BeginMonth] (called once per month, from
// the LAST monthly phase, PhaseFinance) resets the underlying per-tick
// transaction log every month, so it is NOT a lifetime cumulative figure,
// and it is called AFTER that same month's 30 daily ticks (this bug's own
// crematorium postings included) have already landed in it — reading
// OpexTotal() right after a FULL month boundary sees only whatever
// financeHook itself posted AFTER that reset, not the crematorium's own
// same-month postings (BeginMonth already wiped them). This test instead
// reads OpexTotal() one tick BEFORE the next month boundary (day
// DailyTicksPerMonth-1 of the following month) — the one window where
// this bug's daily crematorium postings have landed but the NEXT
// BeginMonth has not yet reset them — and compares it against the exact
// same window's cremation count (a Snapshot delta), so a double-count (or
// under-count) is directly visible as a mismatch.
func TestBUG720_CremationCostPostedExactlyOncePerBody(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, bug720Seed+4)
	e := core.NewEngine(core.WithWorldSeed(bug720Seed+4), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: []string{"crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	// Baseline-one's opening treasury (compose.go's initialTreasury) is
	// far too small to cover even one crematorium's daily 12-body cap at
	// the £150/body placeholder cost (£1800/day > the whole opening
	// balance) — SettleOpex's overdraft protection would then reject
	// EVERY posting outright, which is a real, separate fixture-funding
	// concern (data/deathservices.json's own cost figure is explicitly a
	// disclosed placeholder pending a balance pass), not a defect in this
	// test's SUBJECT (whether a successful post is counted exactly once).
	// Top up treasury generously so the postings this test measures
	// actually succeed — mirrors compose.go's own seedOpeningBalances
	// transaction shape (external -> treasury).
	if _, err := comp.state.finance.Post(finance.Transaction{
		Description: "test funding top-up (BUG-720 cost-posting proof)",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(50_000_000_000), Category: "test.topup"},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(50_000_000_000), Category: "test.topup"},
		},
	}); err != nil {
		t.Fatalf("test funding top-up: %v", err)
	}
	comp.state.syncMoneyFromLedger()
	ds := comp.DeathServices()
	// Warm up over full months (each AdvanceTicks call lands exactly on a
	// month boundary, so BeginMonth has just reset the tick log when this
	// loop exits).
	for i := 0; i < 4; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	before := requireConservation(t, ds, cid)

	// One tick SHORT of the next month boundary: every crematorium
	// posting this window made has landed in the CURRENT tick log, and
	// the next BeginMonth (which would wipe it) has not fired yet.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth)-1)

	after := requireConservation(t, ds, cid)
	crematedThisWindow := after.BodiesCremated - before.BodiesCremated
	if crematedThisWindow <= 0 {
		t.Fatalf("fixture produced no cremations this window: before=%+v after=%+v", before, after)
	}
	perBody, err := ds.PerBodyCostMicropounds(cid)
	if err != nil {
		t.Fatalf("PerBodyCostMicropounds: %v", err)
	}
	wantOpex := finance.Money(crematedThisWindow * perBody)
	gotOpex := comp.state.finance.OpexTotal()
	if gotOpex != wantOpex {
		t.Fatalf("cremation cost not posted exactly once per body: crematedThisWindow=%d perBody=%d want opex=%d got opex=%d",
			crematedThisWindow, perBody, wantOpex, gotOpex)
	}
	t.Logf("BUG-720: %d bodies cremated this window, opex posted = %d micropounds (== %d x %d exactly)",
		crematedThisWindow, gotOpex, crematedThisWindow, perBody)
}

// TestBUG720_DailySweepBoundedByThroughputNotBacklog is the round's F1 perf
// fix's regression proof — count-based, never wall-clock (this project's
// verification standards ban wall-clock CI assertions; the attacker's own
// TestAttackBUG720_DailySweepCostGrowsSuperlinearly is evidence-only for
// exactly that reason). It uses the deathServicesCremateBatchObserved test
// seam to capture the EXACT batch size runDeathServices hands
// [deathservices.DeathServicesAPI.Cremate] each day, across backlog sizes
// 500/2000/5000, and asserts every single one of those batch sizes is
// bounded by DailyThroughput — never anywhere close to the backlog size —
// which is precisely the O(throughput) vs O(backlog) property the round
// measured costing 109ms/2.65s/17.08s per month before this fix.
func TestBUG720_DailySweepBoundedByThroughputNotBacklog(t *testing.T) {
	cid := errs.NewCorrelationID()
	for _, backlog := range []int{500, 2000, 5000} {
		e, comp := abGuardedWire(t, bug720Seed+50, backlog)
		ds := comp.DeathServices()
		cap, err := ds.DailyThroughput(cid)
		if err != nil {
			t.Fatalf("backlog=%d: DailyThroughput: %v", backlog, err)
		}

		var batches []int
		deathServicesCremateBatchObserved = func(_ string, submitted int) {
			batches = append(batches, submitted)
		}
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		deathServicesCremateBatchObserved = nil

		if len(batches) == 0 {
			t.Fatalf("backlog=%d: the observer was never called — the batch was never submitted to Cremate at all", backlog)
		}
		maxBatch := 0
		for _, b := range batches {
			if b > maxBatch {
				maxBatch = b
			}
			if int64(b) > cap {
				t.Fatalf("backlog=%d: a batch of %d ids was submitted to Cremate — DailyThroughput is %d; "+
					"the daily batch must be truncated to the remaining throughput BEFORE calling Cremate", backlog, b, cap)
			}
		}
		t.Logf("backlog=%-5d days observed=%d max batch submitted to Cremate=%d (DailyThroughput=%d, backlog itself=%d)",
			backlog, len(batches), maxBatch, cap, backlog)
	}
}

// abGuardedWire builds a composed engine with one crematorium and a
// synthetic Awaiting backlog of exactly n bodies, no real citizens fixture
// (a fresh, empty mortality pipeline — the backlog is entirely the
// synthetic Intake, so its size is exact and test-controlled).
func abGuardedWire(t *testing.T, seed uint64, n int) (*core.Engine, *Composition) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: []string{"crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if _, err := comp.DeathServices().Intake(syntheticDeaths(n, uint64(1_000_000+n), false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	return e, comp
}
