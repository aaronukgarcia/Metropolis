package compose

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug720_round_test.go — INDEPENDENT destructive round against
// BUG-720 (crematoriums/cemeteries/hearses actually RUN). Written by an
// attacker who is NOT the author (GR#23 independence amendment). Every
// test drives the REAL composed engine through Wire -> AdvanceTicks.
//
// abSeed is this file's own world seed, distinct from bug720Seed /
// bug689Seed / attackSeed so an edit to another file cannot silently
// change this file's assumptions.
const abSeed = uint64(7209901)

// abSynthBase is the citizen-id base this file's synthetic Intake batches
// use. Deliberately far above any id the real citizens fixture mints in a
// handful of simulated months, so a synthetic body can never collide with
// (and be dedup-skipped against) a genuine death.
const abSynthBase = uint64(900_000_000)

// abConservation re-derives AC-14's identity from a fresh Snapshot and
// fails on any violation. Distinct from the author's requireConservation
// so a mutation of the author's helper cannot mask this file's checks.
func abConservation(t *testing.T, ds *deathservices.DeathServicesAPI, cid, where string) deathservices.Conservation {
	t.Helper()
	c, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("%s: Snapshot: %v", where, err)
	}
	sum := c.BodiesAwaitingHandling + c.BodiesEnRoute + c.BodiesBuried + c.BodiesCremated + c.BodiesHandledByDispensation
	if sum != c.BodiesReleased {
		t.Fatalf("%s: AC-14 conservation violated: released=%d sum=%d (%+v)", where, c.BodiesReleased, sum, c)
	}
	return c
}

// abTerminalWatch tracks every body's terminal classification across
// months and fails if one ever changes (a body buried this month and
// cremated next month, or re-classified at all) or carries BOTH a
// cemeteryID and a crematoriumID.
type abTerminalWatch struct {
	seen map[uint64]deathservices.Body
}

func newAbTerminalWatch() *abTerminalWatch {
	return &abTerminalWatch{seen: make(map[uint64]deathservices.Body)}
}

func (w *abTerminalWatch) check(t *testing.T, ds *deathservices.DeathServicesAPI, cid string, ids []uint64, where string) {
	t.Helper()
	for _, id := range ids {
		b, err := ds.Body(id, cid)
		if err != nil {
			continue // not intaken (yet) — not this watch's concern
		}
		if b.CemeteryID != "" && b.CrematoriumID != "" {
			t.Fatalf("%s: body %d is BOTH buried at %q and cremated at %q (%+v)", where, id, b.CemeteryID, b.CrematoriumID, b)
		}
		switch b.State {
		case deathservices.BodyBuried:
			if b.CrematoriumID != "" {
				t.Fatalf("%s: buried body %d carries crematoriumID %q", where, id, b.CrematoriumID)
			}
		case deathservices.BodyCremated:
			if b.CemeteryID != "" {
				t.Fatalf("%s: cremated body %d carries cemeteryID %q", where, id, b.CemeteryID)
			}
		}
		prev, ok := w.seen[id]
		if ok && isTerminalState(prev.State) && prev != b {
			t.Fatalf("%s: body %d changed after reaching terminal state: was %+v now %+v", where, id, prev, b)
		}
		w.seen[id] = b
	}
}

func isTerminalState(s deathservices.BodyState) bool {
	return s == deathservices.BodyBuried || s == deathservices.BodyCremated || s == deathservices.BodyDispensed
}

// abWire builds a composed engine with the given disposal registrations
// and an isolated real-mortality citizens fixture.
func abWire(t *testing.T, seed uint64, pool int, crematoria []string, cemeteries []DeathServiceCemeterySpec, realDeaths bool) (*core.Engine, *Composition) {
	t.Helper()
	var api *citizens.CitizensAPI
	if realDeaths {
		api = buildGuaranteedDeathCitizensAPI(t, seed)
	} else {
		var err error
		api, err = citizens.NewCitizensAPI(seed, errs.NewCorrelationID())
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(pool))
	comp, err := Wire(e, &Deps{
		Citizens:               api,
		DeathServiceCrematoria: crematoria,
		DeathServiceCemeteries: cemeteries,
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

// ---------------------------------------------------------------------
// ATTACK 1 — conservation under EVERY disposal-path composition.
// ---------------------------------------------------------------------

// TestAttackBUG720_ConservationEveryPath drives four arms (none / cemetery
// only / crematorium only / both) through 6 real months each, with a
// synthetic backlog seeded on top of genuine deaths, and re-checks AC-14
// EVERY month plus terminal-state exclusivity for every body.
func TestAttackBUG720_ConservationEveryPath(t *testing.T) {
	arms := []struct {
		name       string
		crematoria []string
		cemeteries []DeathServiceCemeterySpec
	}{
		{"none", nil, nil},
		{"cemetery_only", nil, []DeathServiceCemeterySpec{{ID: "cem-1"}}},
		{"crematorium_only", []string{"crem-1"}, nil},
		{"both", []string{"crem-1"}, []DeathServiceCemeterySpec{{ID: "cem-1"}}},
	}
	type row struct {
		name string
		cons deathservices.Conservation
	}
	var table []row
	for i, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			cid := errs.NewCorrelationID()
			e, comp := abWire(t, abSeed+uint64(i), 1, arm.crematoria, arm.cemeteries, true)
			ds := comp.DeathServices()
			// A synthetic backlog on top of the real deaths, so every arm
			// has genuine pressure to drain regardless of the mortality
			// fixture's own rate.
			synth := syntheticDeaths(400, abSynthBase, false)
			if _, err := ds.Intake(synth, cid); err != nil && !deathservices.IsDuplicateDeath(err) {
				t.Fatalf("Intake: %v", err)
			}
			watch := newAbTerminalWatch()
			ids := make([]uint64, 0, len(synth))
			for _, d := range synth {
				ids = append(ids, d.CitizenID)
			}
			var cons deathservices.Conservation
			for m := 0; m < 6; m++ {
				advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
				cons = abConservation(t, ds, cid, fmt.Sprintf("%s month %d", arm.name, m))
				watch.check(t, ds, cid, ids, fmt.Sprintf("%s month %d", arm.name, m))
			}
			if cons.BodiesEnRoute != 0 {
				t.Errorf("%s: %d bodies stranded EnRoute (inc1 buries in the same call — a non-zero EnRoute means a body is stuck in transit forever)", arm.name, cons.BodiesEnRoute)
			}
			table = append(table, row{arm.name, cons})
			t.Logf("ARM %-16s released=%d awaiting=%d enroute=%d buried=%d cremated=%d dispensed=%d",
				arm.name, cons.BodiesReleased, cons.BodiesAwaitingHandling, cons.BodiesEnRoute,
				cons.BodiesBuried, cons.BodiesCremated, cons.BodiesHandledByDispensation)
		})
	}
}

// ---------------------------------------------------------------------
// ATTACK 2 — capacity honesty.
// ---------------------------------------------------------------------

// TestAttackBUG720_CrematoriumNeverExceedsDailyThroughput advances ONE
// TICK AT A TIME with a backlog far larger than the daily cap and proves
// the per-DAY cremation delta never exceeds DailyThroughput, and equals it
// exactly while the backlog can supply it.
func TestAttackBUG720_CrematoriumNeverExceedsDailyThroughput(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+10, 1, []string{"crem-1"}, nil, false)
	ds := comp.DeathServices()
	cap, err := ds.DailyThroughput(cid)
	if err != nil {
		t.Fatalf("DailyThroughput: %v", err)
	}
	if _, err := ds.Intake(syntheticDeaths(1000, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	prev := int64(0)
	for day := 0; day < 40; day++ {
		advanceInChunks(t, e, 1)
		c := abConservation(t, ds, cid, fmt.Sprintf("day %d", day))
		delta := c.BodiesCremated - prev
		if delta > cap {
			t.Fatalf("day %d: cremated %d bodies in ONE day, daily throughput cap is %d", day, delta, cap)
		}
		if delta < 0 {
			t.Fatalf("day %d: cremated count went BACKWARDS by %d", day, -delta)
		}
		if c.BodiesAwaitingHandling >= cap && delta != cap {
			t.Fatalf("day %d: backlog %d >= cap %d but only %d cremated — daily capacity silently under-used", day, c.BodiesAwaitingHandling, cap, delta)
		}
		prev = c.BodiesCremated
	}
	t.Logf("40 days x cap %d: cremated=%d (exact cap every day)", cap, prev)
	if prev != cap*40 {
		t.Fatalf("expected exactly %d cremations over 40 days, got %d", cap*40, prev)
	}
}

// TestAttackBUG720_CemeterySaturationAcceptsNothing registers a tiny
// cemetery, floods it, and proves burial stops dead at capacity and stays
// there (the reuse horizon is 240 months — nothing recycles), with the
// remainder left Awaiting, never lost.
func TestAttackBUG720_CemeterySaturationAcceptsNothing(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+11, 1, nil, []DeathServiceCemeterySpec{{ID: "cem-small", Capacity: 5}}, false)
	ds := comp.DeathServices()
	const n = 100
	if _, err := ds.Intake(syntheticDeaths(n, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	for m := 0; m < 4; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		c := abConservation(t, ds, cid, fmt.Sprintf("month %d", m))
		if c.BodiesBuried > 5 {
			t.Fatalf("month %d: %d bodies buried in a 5-plot cemetery", m, c.BodiesBuried)
		}
		if c.BodiesBuried+c.BodiesAwaitingHandling != n {
			t.Fatalf("month %d: bodies lost — buried=%d awaiting=%d want total %d (%+v)", m, c.BodiesBuried, c.BodiesAwaitingHandling, n, c)
		}
	}
	occ, capacity, err := ds.CemeteryOccupancy("cem-small", cid)
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occ != 5 || capacity != 5 {
		t.Fatalf("cemetery not saturated: occupied=%d capacity=%d", occ, capacity)
	}
	// PlotEligibleForReuse must be deterministically false for every
	// occupant (horizon 240 months, only 4 elapsed).
	for id := abSynthBase; id < abSynthBase+n; id++ {
		ok, err := ds.PlotEligibleForReuse("cem-small", id, 4, cid)
		if err != nil {
			t.Fatalf("PlotEligibleForReuse: %v", err)
		}
		if ok {
			t.Fatalf("plot for body %d reported reuse-eligible only 4 months after burial (horizon is 240)", id)
		}
	}
	t.Logf("cemetery saturation: occupied=%d/%d, remainder stays Awaiting", occ, capacity)
}

// TestAttackBUG720_HearseMonthlyBudgetBoundsBurial proves the shared
// monthly hearse budget — not the cemetery's plot count — is what bounds
// burials inside one month, and that the budget resets per month.
func TestAttackBUG720_HearseMonthlyBudgetBoundsBurial(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+12, 1, nil, []DeathServiceCemeterySpec{{ID: "cem-big"}}, false)
	ds := comp.DeathServices()
	budget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	if _, err := ds.Intake(syntheticDeaths(5000, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	prev := int64(0)
	for m := 0; m < 3; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		c := abConservation(t, ds, cid, fmt.Sprintf("month %d", m))
		delta := c.BodiesBuried - prev
		if delta > budget {
			t.Fatalf("month %d: %d bodies buried, monthly hearse budget is %d", m, delta, budget)
		}
		if delta == 0 {
			t.Fatalf("month %d: hearse budget did not reset — zero burials with a 5000-body backlog", m)
		}
		prev = c.BodiesBuried
		t.Logf("month %d: buried this month=%d (budget %d) cumulative=%d", m, delta, budget, c.BodiesBuried)
	}
}

// ---------------------------------------------------------------------
// ATTACK 3 — money.
// ---------------------------------------------------------------------

// TestAttackBUG720_EmptyTreasuryCremationIsFree measures the author's
// self-filed P2 in the DEFAULT composition: the opening treasury cannot
// cover the cremation bill, SettleOpex is rejected, the error is swallowed
// and the bodies burn anyway. This test asserts only the load-bearing
// safety property — NO MONEY IS CREATED and the ledger stays balanced —
// and LOGS the free-disposal measurement as evidence.
func TestAttackBUG720_EmptyTreasuryCremationIsFree(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+20, 1, []string{"crem-1"}, nil, true)
	ds := comp.DeathServices()
	perBody, err := ds.PerBodyCostMicropounds(cid)
	if err != nil {
		t.Fatalf("PerBodyCostMicropounds: %v", err)
	}
	treasuryBefore := ledgerBalance(comp.state.finance, finance.AcctTreasury)
	extBefore := ledgerBalance(comp.state.finance, finance.AcctExternal)
	for m := 0; m < 5; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	c := abConservation(t, ds, cid, "empty treasury")
	treasuryAfter := ledgerBalance(comp.state.finance, finance.AcctTreasury)
	extAfter := ledgerBalance(comp.state.finance, finance.AcctExternal)
	if c.BodiesCremated == 0 {
		t.Skip("fixture produced no cremations — nothing to measure")
	}
	billed := c.BodiesCremated * perBody
	t.Logf("EMPTY-TREASURY MEASUREMENT: cremated=%d bodies, notional bill=%d micropounds (£%d); "+
		"treasury %d -> %d (delta %d), external %d -> %d (delta %d)",
		c.BodiesCremated, billed, billed/1_000_000,
		treasuryBefore, treasuryAfter, treasuryAfter-treasuryBefore,
		extBefore, extAfter, extAfter-extBefore)
	// The reject-and-continue path must never MINT money: treasury may
	// only ever move by real, posted transactions. A treasury that GREW
	// by the cremation bill would be money created out of a failed post.
	if treasuryAfter-treasuryBefore >= billed && billed > 0 {
		t.Fatalf("treasury grew by at least the cremation bill (%d) across a run that only ever SPENDS on cremation — money created", billed)
	}
}

// TestAttackBUG720_FundedTreasuryCostExactlyOncePerBody independently
// re-derives the once-per-body property from a per-DAY window (the
// author's test uses a month-length window), so an off-by-one in the
// author's BeginMonth reasoning cannot mask a double-post.
func TestAttackBUG720_FundedTreasuryCostExactlyOncePerBody(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+21, 1, []string{"crem-1"}, nil, false)
	ds := comp.DeathServices()
	if _, err := comp.state.finance.Post(finance.Transaction{
		Description: "attack funding",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(500_000_000_000), Category: "test.topup"},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(500_000_000_000), Category: "test.topup"},
		},
	}); err != nil {
		t.Fatalf("funding: %v", err)
	}
	comp.state.syncMoneyFromLedger()
	if _, err := ds.Intake(syntheticDeaths(600, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	perBody, err := ds.PerBodyCostMicropounds(cid)
	if err != nil {
		t.Fatalf("PerBodyCostMicropounds: %v", err)
	}
	// Land on a month boundary first (BeginMonth resets the tick log), then
	// step ONE day at a time and compare that single day's opex delta
	// against that single day's cremation delta.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	prev := abConservation(t, ds, cid, "warm")
	prevOpex := comp.state.finance.OpexTotal()
	checked := 0
	for day := 1; day < int(core.DailyTicksPerMonth); day++ {
		advanceInChunks(t, e, 1)
		c := abConservation(t, ds, cid, fmt.Sprintf("day %d", day))
		opex := comp.state.finance.OpexTotal()
		gotDelta := int64(opex - prevOpex)
		wantDelta := (c.BodiesCremated - prev.BodiesCremated) * perBody
		if gotDelta != wantDelta {
			t.Fatalf("day %d: cremated %d bodies this day (perBody=%d) => want opex delta %d, got %d",
				day, c.BodiesCremated-prev.BodiesCremated, perBody, wantDelta, gotDelta)
		}
		if wantDelta > 0 {
			checked++
		}
		prev, prevOpex = c, opex
	}
	if checked == 0 {
		t.Fatal("no day in the window posted any cremation cost — the assertion above is vacuous")
	}
	t.Logf("cost posted exactly once per body on %d separate days", checked)
}

// ---------------------------------------------------------------------
// ATTACK 4 — determinism and the save/restore boundary.
// ---------------------------------------------------------------------

// TestAttackBUG720_DeterminismPoolsAndCursor compares the FULL observable
// state (conservation, backlog, handoff cursor, treasury, run status)
// across pool sizes 1/4/20 — the author's own test compares conservation
// only.
func TestAttackBUG720_DeterminismPoolsAndCursor(t *testing.T) {
	cid := errs.NewCorrelationID()
	type snap struct {
		cons     deathservices.Conservation
		cursor   int64
		treasury int64
		status   DeathServicesRunStatus
	}
	var got []snap
	for _, pool := range []int{1, 4, 20} {
		e, comp := abWire(t, abSeed+30, pool, []string{"crem-a", "crem-b"},
			[]DeathServiceCemeterySpec{{ID: "cem-b"}, {ID: "cem-a", Capacity: 40}}, true)
		ds := comp.DeathServices()
		if _, err := ds.Intake(syntheticDeaths(700, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
			t.Fatalf("Intake: %v", err)
		}
		for m := 0; m < 5; m++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
			abConservation(t, ds, cid, fmt.Sprintf("pool %d month %d", pool, m))
		}
		cursor, err := ds.HandoffCursor(cid)
		if err != nil {
			t.Fatalf("HandoffCursor: %v", err)
		}
		got = append(got, snap{
			cons:     abConservation(t, ds, cid, "final"),
			cursor:   cursor,
			treasury: ledgerBalance(comp.state.finance, finance.AcctTreasury),
			status:   comp.DeathServicesRunStatus(),
		})
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Fatalf("determinism violated at pool index %d: %+v != %+v", i, got[i], got[0])
		}
	}
	if got[0].cons.BodiesCremated == 0 && got[0].cons.BodiesBuried == 0 {
		t.Fatal("vacuous: no disposal happened in any arm")
	}
	t.Logf("pools 1/4/20 identical: %+v", got[0])
}

// TestAttackBUG720_SaveRestoreMidBatchContinuation is the real teeth the
// author's round-trip test lacks: a control arm runs 6 months straight; a
// hostile arm runs 3 months, saves MID-BACKLOG, restores into a fresh
// composition, and runs 3 more. The two must land on identical state.
func TestAttackBUG720_SaveRestoreMidBatchContinuation(t *testing.T) {
	cid := errs.NewCorrelationID()
	const seed = abSeed + 40
	crem := []string{"crem-1"}
	cems := []DeathServiceCemeterySpec{{ID: "cem-1", Capacity: 50}}

	// Control: 6 uninterrupted months.
	eC, compC := abWire(t, seed, 1, crem, cems, true)
	if _, err := compC.DeathServices().Intake(syntheticDeaths(500, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake (control): %v", err)
	}
	for m := 0; m < 6; m++ {
		advanceInChunks(t, eC, int64(core.DailyTicksPerMonth))
	}
	ctrl := abConservation(t, compC.DeathServices(), cid, "control")
	ctrlCursor, _ := compC.DeathServices().HandoffCursor(cid)

	// Hostile: 3 months, save, restore, 3 more.
	eH, compH := abWire(t, seed, 1, crem, cems, true)
	if _, err := compH.DeathServices().Intake(syntheticDeaths(500, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake (hostile): %v", err)
	}
	for m := 0; m < 3; m++ {
		advanceInChunks(t, eH, int64(core.DailyTicksPerMonth))
	}
	mid := abConservation(t, compH.DeathServices(), cid, "mid")
	if mid.BodiesAwaitingHandling == 0 {
		t.Fatal("vacuous: the save boundary is not mid-backlog (nothing left Awaiting)")
	}
	root := t.TempDir()
	if err := compH.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eR, compR := abWire(t, seed, 1, crem, cems, true)
	if err := compR.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for m := 0; m < 3; m++ {
		advanceInChunks(t, eR, int64(core.DailyTicksPerMonth))
	}
	restored := abConservation(t, compR.DeathServices(), cid, "restored")
	restoredCursor, _ := compR.DeathServices().HandoffCursor(cid)

	if restored != ctrl {
		t.Fatalf("save/restore continuation diverged from the control run:\n control  = %+v\n restored = %+v", ctrl, restored)
	}
	if restoredCursor != ctrlCursor {
		t.Fatalf("handoff cursor diverged across the save boundary: control=%d restored=%d", ctrlCursor, restoredCursor)
	}
	t.Logf("mid-batch save/restore continuation identical to control: %+v cursor=%d", ctrl, ctrlCursor)
}

// ---------------------------------------------------------------------
// ATTACK 5 — cursor abuse (folded BUG-689 P2).
// ---------------------------------------------------------------------

// TestAttackBUG720_ResetHandoffCursorCannotReplayDeaths hits the new
// escape hatch directly: a mid-run reset to 0 (the exact thing the clamp
// path does) must NOT re-release any death — releasedTotal must not move,
// no body may be double-counted, and the cursor must converge back on the
// stream length. Includes a negative-then-positive sequence.
func TestAttackBUG720_ResetHandoffCursorCannotReplayDeaths(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+50, 1, []string{"crem-1"}, nil, true)
	ds := comp.DeathServices()
	for m := 0; m < 4; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	before := abConservation(t, ds, cid, "pre-reset")
	beforeCursor, err := ds.HandoffCursor(cid)
	if err != nil {
		t.Fatalf("HandoffCursor: %v", err)
	}
	if before.BodiesReleased == 0 || beforeCursor == 0 {
		t.Fatal("vacuous: no deaths paged before the reset")
	}

	// Abuse 1: a bare reset mid-run.
	if err := ds.ResetHandoffCursor(cid); err != nil {
		t.Fatalf("ResetHandoffCursor: %v", err)
	}
	if c, _ := ds.HandoffCursor(cid); c != 0 {
		t.Fatalf("ResetHandoffCursor left cursor at %d, want 0", c)
	}
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	afterReset := abConservation(t, ds, cid, "post-reset")
	afterCursor, _ := ds.HandoffCursor(cid)
	if afterCursor < beforeCursor {
		t.Fatalf("cursor did not recover past its pre-reset value: before=%d after=%d", beforeCursor, afterCursor)
	}

	// Abuse 2: reset again, twice in a row, then drive. Still no replay.
	if err := ds.ResetHandoffCursor(cid); err != nil {
		t.Fatalf("ResetHandoffCursor (2): %v", err)
	}
	if err := ds.ResetHandoffCursor(cid); err != nil {
		t.Fatalf("ResetHandoffCursor (3): %v", err)
	}
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	final := abConservation(t, ds, cid, "post-double-reset")
	finalCursor, _ := ds.HandoffCursor(cid)

	// Control: the same fixture, never reset, driven the same total months.
	eC, compC := abWire(t, abSeed+50, 1, []string{"crem-1"}, nil, true)
	for m := 0; m < 6; m++ {
		advanceInChunks(t, eC, int64(core.DailyTicksPerMonth))
	}
	ctrl := abConservation(t, compC.DeathServices(), cid, "control")
	ctrlCursor, _ := compC.DeathServices().HandoffCursor(cid)

	if final.BodiesReleased != ctrl.BodiesReleased {
		t.Fatalf("cursor reset REPLAYED or DROPPED deaths: hostile released=%d control released=%d", final.BodiesReleased, ctrl.BodiesReleased)
	}
	if final != ctrl {
		t.Fatalf("cursor reset changed the disposal outcome:\n hostile = %+v\n control = %+v", final, ctrl)
	}
	if finalCursor != ctrlCursor {
		t.Fatalf("cursor did not converge on the control's value: hostile=%d control=%d", finalCursor, ctrlCursor)
	}
	t.Logf("three ResetHandoffCursor abuses absorbed: released=%d (== control) cursor=%d (== control), afterReset=%+v",
		final.BodiesReleased, finalCursor, afterReset)
}

// ---------------------------------------------------------------------
// ATTACK 6 — hook ordering / same-day realisation.
// ---------------------------------------------------------------------

// TestAttackBUG720_HookOrderStableAcrossSaveBoundary drives the SAME
// number of ticks in two shapes — one straight run, and one that saves and
// restores on EVERY month boundary — and proves the daily intake-vs-run
// order produces identical counts either way. An order dependence that
// survives a save/restore boundary would show as a divergence here.
//
// Uses LoadAt, not Load: save_wire.go documents plain Load as a SNAPSHOT
// that leaves the engine at tick 0, so a plain-Load continuation is not
// claimed to be tick-equivalent. LoadAt is the sanctioned tick-continuous
// resume, and it is the one this bug's day/month-scoped budgets
// (crematorium lastDay/cremToday, hearse+dispensation lastMonth/
// usedThisMonth) must survive.
func TestAttackBUG720_HookOrderStableAcrossSaveBoundary(t *testing.T) {
	cid := errs.NewCorrelationID()
	const seed = abSeed + 60
	crem := []string{"crem-1"}

	eC, compC := abWire(t, seed, 1, crem, nil, true)
	for m := 0; m < 5; m++ {
		advanceInChunks(t, eC, int64(core.DailyTicksPerMonth))
	}
	ctrl := abConservation(t, compC.DeathServices(), cid, "straight")

	e, comp := abWire(t, seed, 1, crem, nil, true)
	for m := 0; m < 5; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		clock, err := e.Clock()
		if err != nil {
			t.Fatalf("Clock: %v", err)
		}
		tick := clock.Tick()
		root := t.TempDir()
		if err := comp.Save(root); err != nil {
			t.Fatalf("Save (month %d): %v", m, err)
		}
		var next *Composition
		e, next = abWire(t, seed, 1, crem, nil, true)
		if err := next.LoadAt(root, tick); err != nil {
			t.Fatalf("LoadAt (month %d): %v", m, err)
		}
		comp = next
	}
	chopped := abConservation(t, comp.DeathServices(), cid, "chopped")
	if chopped != ctrl {
		t.Fatalf("save/restore on every month boundary changed disposal counts:\n straight = %+v\n chopped  = %+v", ctrl, chopped)
	}
	if ctrl.BodiesReleased == 0 {
		t.Fatal("vacuous: no deaths")
	}
	t.Logf("hook order stable across 5 save/restore boundaries: %+v", ctrl)
}

// ---------------------------------------------------------------------
// ATTACK 7 — concurrent status readers (-race).
// ---------------------------------------------------------------------

// TestAttackBUG720_ConcurrentRunStatusReaders hammers
// Composition.DeathServicesRunStatus from 8 goroutines while the daily run
// hook mutates deathservices under the engine's own locks. Run under
// -race, this is the lock-graph probe: compose -> deathservices ->
// {services, logistics} plus compose -> finance.SettleOpex.
func TestAttackBUG720_ConcurrentRunStatusReaders(t *testing.T) {
	cid := errs.NewCorrelationID()
	e, comp := abWire(t, abSeed+70, 4, []string{"crem-1"},
		[]DeathServiceCemeterySpec{{ID: "cem-1", Capacity: 100}}, true)
	ds := comp.DeathServices()
	if _, err := ds.Intake(syntheticDeaths(800, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var reads int64
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := int64(0)
			for {
				select {
				case <-stop:
					mu.Lock()
					reads += local
					mu.Unlock()
					return
				default:
				}
				st := comp.DeathServicesRunStatus()
				if st.AwaitingBacklog < 0 {
					t.Errorf("negative backlog observed: %+v", st)
					mu.Lock()
					reads += local
					mu.Unlock()
					return
				}
				if _, err := ds.HandoffCursor(cid); err != nil {
					t.Errorf("HandoffCursor under hammer: %v", err)
				}
				local++
			}
		}()
	}
	for m := 0; m < 12; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	close(stop)
	wg.Wait()
	if reads == 0 {
		t.Fatal("vacuous: readers performed zero reads")
	}
	abConservation(t, ds, cid, "post-hammer")
	t.Logf("12 months with 8 concurrent status readers: %d reads, final %+v", reads, comp.DeathServicesRunStatus())
}

// ---------------------------------------------------------------------
// ATTACK 8 — cost of the daily sweep (perf shape, evidence only).
// ---------------------------------------------------------------------

// TestAttackBUG720_DailySweepCostGrowsSuperlinearly measures the wall time
// of ONE simulated month at three backlog sizes. It asserts NOTHING about
// wall clock (banned in CI, per this project's verification standards) —
// it exists purely to produce the measurement for the round's report:
// compose hands the ENTIRE AwaitingSorted backlog to Cremate every day,
// and Cremate calls awaitingAheadCountLocked (O(len(bodies))) once per
// submitted id, making the daily sweep O(backlog x totalBodies).
func TestAttackBUG720_DailySweepCostGrowsSuperlinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("perf shape probe")
	}
	for _, n := range []int{500, 2000, 5000} {
		cid := errs.NewCorrelationID()
		e, comp := abWire(t, abSeed+80, 1, []string{"crem-1"}, nil, false)
		ds := comp.DeathServices()
		if _, err := ds.Intake(syntheticDeaths(n, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
			t.Fatalf("Intake: %v", err)
		}
		start := time.Now()
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		elapsed := time.Since(start)
		c := abConservation(t, ds, cid, "perf")
		t.Logf("PERF backlog=%-5d one month = %-12s cremated=%d awaiting=%d",
			n, elapsed, c.BodiesCremated, c.BodiesAwaitingHandling)
	}
}

// ---------------------------------------------------------------------
// ATTACK 9 — FINDING: zero infrastructure out-disposes real infrastructure.
// ---------------------------------------------------------------------

// TestAttackBUG720_FINDING_ZeroInfrastructureOutDisposesRealInfrastructure
// pins the incentive inversion this round found. A city that has built
// NOTHING — no cemetery, no crematorium — disposes of its dead FASTER,
// and entirely free of charge, than a city that built a crematorium,
// provided a SINGLE emergency-flagged death has ever arrived (which
// permanently raises dispensation until the backlog hits exactly zero).
//
// Mechanism: intakeLocked raises dispensation.active on ANY
// EmergencyFlag=true death (inherited, inc1). BUG-720's new
// runDeathServices then LOOPS Dispense until the whole monthly
// dispensation budget (hearseMonthlyTransportBudget x
// dispensationThroughputMultiplier = 300 x 4 = 1200/month) is spent —
// four times a crematorium's 12/day x 30 = 360/month, with no plot
// consumed, no cost posted anywhere, and no building required. The
// backlogCapacityCeiling threshold is NOT needed for this: the emergency
// flag alone opens the channel.
//
// This test asserts the measured relationship, so a future fix (gating
// dispensation on real capacity, charging for it, or capping it below the
// built-infrastructure rate) turns it red and forces a re-read.
func TestAttackBUG720_FINDING_ZeroInfrastructureOutDisposesRealInfrastructure(t *testing.T) {
	run := func(name string, crematoria []string, emergencySeed bool) deathservices.Conservation {
		cid := errs.NewCorrelationID()
		e, comp := abWire(t, abSeed+90, 1, crematoria, nil, false)
		ds := comp.DeathServices()
		// One single emergency-flagged death is the entire trigger.
		if emergencySeed {
			if _, err := ds.Intake(syntheticDeaths(1, abSynthBase-1, true), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
				t.Fatalf("%s: emergency Intake: %v", name, err)
			}
		}
		if _, err := ds.Intake(syntheticDeaths(2000, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
			t.Fatalf("%s: Intake: %v", name, err)
		}
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		c := abConservation(t, ds, cid, name)
		t.Logf("%-34s buried=%d cremated=%d dispensed=%d awaiting=%d treasury=%d",
			name, c.BodiesBuried, c.BodiesCremated, c.BodiesHandledByDispensation,
			c.BodiesAwaitingHandling, ledgerBalance(comp.state.finance, finance.AcctTreasury))
		return c
	}
	nothing := run("NOTHING BUILT (1 emergency death)", nil, true)
	crem := run("CREMATORIUM BUILT (no emergency)", []string{"crem-1"}, false)

	if nothing.BodiesHandledByDispensation == 0 {
		t.Fatal("vacuous: dispensation never opened")
	}
	disposedByNothing := nothing.BodiesHandledByDispensation + nothing.BodiesBuried + nothing.BodiesCremated
	disposedByCrem := crem.BodiesHandledByDispensation + crem.BodiesBuried + crem.BodiesCremated
	if disposedByNothing <= disposedByCrem {
		t.Fatalf("FINDING APPEARS FIXED — re-read this test: zero-infrastructure disposed %d, crematorium disposed %d",
			disposedByNothing, disposedByCrem)
	}
	t.Logf("FINDING: zero infrastructure disposed %d bodies in one month vs %d for a built crematorium (%.1fx), "+
		"free of charge and consuming no plots — one emergency-flagged death is the only prerequisite",
		disposedByNothing, disposedByCrem, float64(disposedByNothing)/float64(disposedByCrem))
}

// TestAttackBUG720_PlainLoadCannotRefundMonthlyDisposalBudgets_FIXED is the
// FIXED form of the round's second finding (F4). deathservices' month-scoped
// budgets used to be compared with a plain `!=` against the current month
// (hearseState/dispensationState.resetMonthLocked), so a plain
// Composition.Load — which restarts the ENGINE's own clock at tick/month 0
// by design (save_wire.go's documented snapshot-not-tick-continuous
// contract) while restoring deathservices' own lastMonth/usedThisMonth
// watermarks unchanged — saw month(0) != the restored (higher) lastMonth on
// the very first post-Load day, and resetMonthLocked handed the WHOLE
// monthly budget back for free (measured: 300 extra burials on that first
// day). Fixed by comparing with `>` instead (hearse.go/dispensation.go,
// BUG-720 round F4): a month number that goes BACKWARDS relative to the
// persisted watermark is no longer treated as "a new month started" at all,
// so the persisted usedThisMonth stays in force. This test proves BOTH
// halves of that fix: (a) immediately after a plain Load, the exhausted
// budget stays exhausted (zero refund, repeatably, across several more
// days) — not merely "same or fewer", genuinely zero — and (b) the budget
// is NOT permanently dead either: once the engine's own restarted month
// counter climbs back PAST the persisted watermark, a fresh month's budget
// resumes exactly as it would have without any save/load at all. LoadAt
// (the tick-continuous sibling) is unaffected — proven separately by
// TestAttackBUG720_HookOrderStableAcrossSaveBoundary.
func TestAttackBUG720_PlainLoadCannotRefundMonthlyDisposalBudgets_FIXED(t *testing.T) {
	cid := errs.NewCorrelationID()
	cems := []DeathServiceCemeterySpec{{ID: "cem-big"}}
	e, comp := abWire(t, abSeed+100, 1, nil, cems, false)
	ds := comp.DeathServices()
	budget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	if _, err := ds.Intake(syntheticDeaths(5000, abSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
		t.Fatalf("Intake: %v", err)
	}
	// Reach month 1 (so the persisted lastMonth is NOT the month 0 a plain
	// Load restarts at) and spend that month's whole hearse budget
	// mid-month, off any boundary.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth)+int64(core.DailyTicksPerMonth)/2)
	spent := abConservation(t, ds, cid, "mid-month")
	if spent.BodiesBuried != 2*budget {
		t.Fatalf("fixture: expected two full monthly budgets (%d) spent by mid-month-1, got %d buried", 2*budget, spent.BodiesBuried)
	}
	// Confirm it really is exhausted: more days in the SAME month bury nothing.
	advanceInChunks(t, e, 3)
	stillSpent := abConservation(t, ds, cid, "still mid-month")
	if stillSpent.BodiesBuried != spent.BodiesBuried {
		t.Fatalf("fixture: budget not actually exhausted (%d -> %d)", spent.BodiesBuried, stillSpent.BodiesBuried)
	}

	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eR, compR := abWire(t, abSeed+100, 1, nil, cems, false)
	if err := compR.Load(root); err != nil { // plain Load — the snapshot path
		t.Fatalf("Load: %v", err)
	}
	dsR := compR.DeathServices()

	// (a) The FIRST post-load day must refund NOTHING.
	advanceInChunks(t, eR, 1)
	afterOneDay := abConservation(t, dsR, cid, "post-plain-load day 1")
	if afterOneDay.BodiesBuried != stillSpent.BodiesBuried {
		t.Fatalf("BUG-720 F4 REGRESSED: plain Load refunded the monthly hearse budget on day 1 (buried %d -> %d)",
			stillSpent.BodiesBuried, afterOneDay.BodiesBuried)
	}
	// Repeatably: drive through the engine's own restarted month 0 AND
	// month 1 (both <= the persisted lastMonth of 1) — still zero refund
	// across the whole span, not just the first tick.
	advanceInChunks(t, eR, 2*int64(core.DailyTicksPerMonth)-1)
	stillZero := abConservation(t, dsR, cid, "post-plain-load through restarted month 1")
	if stillZero.BodiesBuried != stillSpent.BodiesBuried {
		t.Fatalf("BUG-720 F4 REGRESSED: plain Load refunded the monthly hearse budget somewhere across restarted months 0-1 (buried %d -> %d)",
			stillSpent.BodiesBuried, stillZero.BodiesBuried)
	}

	// (b) Not permanently dead: once the engine's restarted month counter
	// climbs PAST the persisted watermark (month 2 > persisted lastMonth
	// 1), a fresh month's budget resumes exactly as it would have with no
	// save/load at all.
	advanceInChunks(t, eR, int64(core.DailyTicksPerMonth))
	resumed := abConservation(t, dsR, cid, "post-plain-load month 2 (past the watermark)")
	gained := resumed.BodiesBuried - stillZero.BodiesBuried
	if gained != budget {
		t.Fatalf("fresh month's budget did not resume correctly once the engine's month passed the persisted watermark: want +%d, got +%d (buried %d -> %d)",
			budget, gained, stillZero.BodiesBuried, resumed.BodiesBuried)
	}
	t.Logf("BUG-720 F4 FIXED: plain Load refunded ZERO burials across restarted months 0-1 (stayed at %d, budget %d already spent), "+
		"then correctly resumed a fresh %d-body budget once the engine's month passed the persisted watermark (buried -> %d)",
		stillSpent.BodiesBuried, budget, budget, resumed.BodiesBuried)
}
