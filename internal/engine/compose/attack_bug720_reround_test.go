package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug720_reround_test.go — INDEPENDENT re-round of BUG-720's two
// post-verdict fixes (F1 batch truncation, F4 `>` month comparison).
// Written by the attacker, never the author; helpers here are deliberately
// this file's own so a mutation of another file's helper cannot mask a
// finding.

const rrSeed = uint64(72099555)
const rrSynthBase = uint64(800_000_000)

// rrCons re-derives AC-14 from a fresh Snapshot (this file's own copy).
func rrCons(t *testing.T, ds *deathservices.DeathServicesAPI, cid, where string) deathservices.Conservation {
	t.Helper()
	c, err := ds.Snapshot(cid)
	if err != nil {
		t.Fatalf("%s: Snapshot: %v", where, err)
	}
	if c.BodiesAwaitingHandling+c.BodiesEnRoute+c.BodiesBuried+c.BodiesCremated+c.BodiesHandledByDispensation != c.BodiesReleased {
		t.Fatalf("%s: conservation violated %+v", where, c)
	}
	return c
}

// rrWire builds a composition with no real mortality fixture (an empty
// citizens pipeline) so the backlog is EXACTLY the synthetic Intake.
func rrWire(t *testing.T, seed uint64, pool int, crematoria []string, cemeteries []DeathServiceCemeterySpec, backlog int) (*core.Engine, *Composition) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
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
	if backlog > 0 {
		if _, err := comp.DeathServices().Intake(syntheticDeaths(backlog, rrSynthBase, false), cid); err != nil && !deathservices.IsDuplicateDeath(err) {
			t.Fatalf("Intake: %v", err)
		}
	}
	return e, comp
}

// ---------------------------------------------------------------------
// RE-ROUND 1 — the truncation must not STARVE capacity.
// ---------------------------------------------------------------------

// TestReroundBUG720_TruncationDoesNotStarveCapacity: two crematoria plus a
// cemetery, a backlog far larger than 30 days of total capacity. Every
// body the registered capacity COULD have handled must actually be handled
// — the expected totals are derived from the module's own data-sourced
// accessors (GR#15), never hardcoded: 30 days x 2 crematoria x
// DailyThroughput cremated, and exactly one HearseMonthlyBudget buried.
// A truncation that under-submits (e.g. off-by-one, or a per-crematorium
// batch that collides with the sorted-prefix admission rank) shows up here
// as a shortfall.
func TestReroundBUG720_TruncationDoesNotStarveCapacity(t *testing.T) {
	cid := errs.NewCorrelationID()
	const backlog = 3000 // < backlogCapacityCeiling, so dispensation never activates
	cems := []DeathServiceCemeterySpec{{ID: "cem-1"}}
	e, comp := rrWire(t, rrSeed, 1, []string{"crem-a", "crem-b"}, cems, backlog)
	ds := comp.DeathServices()

	throughput, err := ds.DailyThroughput(cid)
	if err != nil {
		t.Fatalf("DailyThroughput: %v", err)
	}
	hearseBudget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}

	days := int64(core.DailyTicksPerMonth) // exactly one month == 30 days
	advanceInChunks(t, e, days)
	c := rrCons(t, ds, cid, "after 30 days")

	wantCremated := days * 2 * throughput
	if c.BodiesCremated != wantCremated {
		t.Fatalf("STARVATION: cremated=%d, but 2 crematoria x %d/day x %d days = %d were possible (%+v)",
			c.BodiesCremated, throughput, days, wantCremated, c)
	}
	if c.BodiesBuried != hearseBudget {
		t.Fatalf("STARVATION/OVERRUN: buried=%d, want exactly one monthly hearse budget %d (%+v)",
			c.BodiesBuried, hearseBudget, c)
	}
	if c.BodiesAwaitingHandling != int64(backlog)-wantCremated-hearseBudget {
		t.Fatalf("awaiting mismatch: %+v", c)
	}
	if c.BodiesHandledByDispensation != 0 {
		t.Fatalf("dispensation should not have run below the ceiling: %+v", c)
	}
	t.Logf("RE-ROUND: 30 days, 2 crematoria + 1 cemetery, backlog %d -> cremated=%d (full 2x%d/day) buried=%d (full budget %d) awaiting=%d",
		backlog, c.BodiesCremated, throughput, c.BodiesBuried, hearseBudget, c.BodiesAwaitingHandling)

	// Continue a second month: the hearse budget must roll (a further full
	// budget buried), and cremation must continue at full rate — proving
	// the truncation is not a one-month artefact.
	advanceInChunks(t, e, days)
	c2 := rrCons(t, ds, cid, "after 60 days")
	if c2.BodiesCremated != 2*wantCremated {
		t.Fatalf("month 2 cremation shortfall: %d want %d", c2.BodiesCremated, 2*wantCremated)
	}
	if c2.BodiesBuried != 2*hearseBudget {
		t.Fatalf("month 2 burial shortfall: %d want %d", c2.BodiesBuried, 2*hearseBudget)
	}
}

// ---------------------------------------------------------------------
// RE-ROUND 2 — the two Remaining* reads are PURE, and the zero-remaining
// path is a skip, not an error.
// ---------------------------------------------------------------------

func TestReroundBUG720_RemainingReadsArePureAndZeroPathIsSafe(t *testing.T) {
	cid := errs.NewCorrelationID()
	_, comp := rrWire(t, rrSeed+1, 1, []string{"crem-a"}, []DeathServiceCemeterySpec{{ID: "cem-1"}}, 200)
	ds := comp.DeathServices()
	throughput, err := ds.DailyThroughput(cid)
	if err != nil {
		t.Fatalf("DailyThroughput: %v", err)
	}

	// Purity: hammering the read 50x on a day nothing has been cremated
	// must never consume the day's allowance.
	for i := 0; i < 50; i++ {
		got, err := ds.RemainingDailyThroughput("crem-a", 500, cid)
		if err != nil {
			t.Fatalf("RemainingDailyThroughput: %v", err)
		}
		if got != throughput {
			t.Fatalf("read %d MUTATED state: remaining=%d want %d", i, got, throughput)
		}
	}
	// Now exhaust day 500 for real.
	awaiting, err := ds.AwaitingSorted(cid)
	if err != nil {
		t.Fatalf("AwaitingSorted: %v", err)
	}
	if _, _, err := ds.Cremate(awaiting[:throughput], "crem-a", 500, cid); err != nil {
		t.Fatalf("Cremate: %v", err)
	}
	rem, err := ds.RemainingDailyThroughput("crem-a", 500, cid)
	if err != nil {
		t.Fatalf("RemainingDailyThroughput: %v", err)
	}
	if rem != 0 {
		t.Fatalf("remaining after a full day should be 0, got %d", rem)
	}
	// The zero path in compose is a `continue` (no call at all). Prove the
	// alternative would ALSO be safe: an empty batch is a clean no-op.
	got, cost, err := ds.Cremate(nil, "crem-a", 500, cid)
	if err != nil {
		t.Fatalf("empty-batch Cremate errored: %v", err)
	}
	if len(got) != 0 || cost != 0 {
		t.Fatalf("empty-batch Cremate was not a no-op: got=%v cost=%d", got, cost)
	}
	// A new day restores the full allowance (the read never froze it).
	rem2, err := ds.RemainingDailyThroughput("crem-a", 501, cid)
	if err != nil {
		t.Fatalf("RemainingDailyThroughput day+1: %v", err)
	}
	if rem2 != throughput {
		t.Fatalf("new day did not restore throughput: %d want %d", rem2, throughput)
	}

	// Hearse read purity + zero path.
	budget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := ds.RemainingHearseBudget(7, cid)
		if err != nil {
			t.Fatalf("RemainingHearseBudget: %v", err)
		}
		if got != budget {
			t.Fatalf("hearse read %d MUTATED state: %d want %d", i, got, budget)
		}
	}
	// Unknown crematorium must be a registry error, not a silent 0 (which
	// compose would read as "skip" and silently never cremate).
	if _, err := ds.RemainingDailyThroughput("nope", 1, cid); err == nil {
		t.Fatal("RemainingDailyThroughput on an unknown crematorium returned no error")
	}
}

// ---------------------------------------------------------------------
// RE-ROUND 3 — F4: the save-scum sequence, constructed independently.
// ---------------------------------------------------------------------

// rrSpendHearseBudget advances to mid-month `month` with a big backlog so
// the hearse budget for that month is fully spent, then returns the
// conservation snapshot.
func TestReroundBUG720_SaveScumCannotRefundHearseBudget(t *testing.T) {
	cid := errs.NewCorrelationID()
	cems := []DeathServiceCemeterySpec{{ID: "cem-1", Capacity: 100000}}
	e, comp := rrWire(t, rrSeed+2, 1, nil, cems, 4000)
	ds := comp.DeathServices()
	budget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	// Months 0..2 fully, then 10 days into month 3 (mid-month, off any
	// boundary) — month 3's budget is exhausted well inside 10 days.
	advanceInChunks(t, e, 3*int64(core.DailyTicksPerMonth)+10)
	before := rrCons(t, ds, cid, "pre-save")
	if before.BodiesBuried != 4*budget {
		t.Fatalf("fixture: want %d buried by mid-month-3, got %d", 4*budget, before.BodiesBuried)
	}

	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eR, compR := rrWire(t, rrSeed+2, 1, nil, cems, 0)
	if err := compR.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	dsR := compR.DeathServices()

	// ONE day after a plain Load: zero extra burials.
	advanceInChunks(t, eR, 1)
	day1 := rrCons(t, dsR, cid, "post-load day 1")
	if day1.BodiesBuried != before.BodiesBuried {
		t.Fatalf("F4 REGRESSED: save-scum refunded %d burials on the first post-load day",
			day1.BodiesBuried-before.BodiesBuried)
	}
	// Through the restarted months 0..3 (all <= the persisted watermark 3):
	// still zero.
	advanceInChunks(t, eR, 4*int64(core.DailyTicksPerMonth)-1)
	through := rrCons(t, dsR, cid, "post-load through restarted month 3")
	if through.BodiesBuried != before.BodiesBuried {
		t.Fatalf("F4 REGRESSED: save-scum refunded %d burials across restarted months 0-3",
			through.BodiesBuried-before.BodiesBuried)
	}
	// Month 4 > watermark 3: a fresh budget resumes, exactly one budget.
	advanceInChunks(t, eR, int64(core.DailyTicksPerMonth))
	resumed := rrCons(t, dsR, cid, "post-load month 4")
	if gained := resumed.BodiesBuried - through.BodiesBuried; gained != budget {
		t.Fatalf("budget did not resume correctly past the watermark: gained %d want %d", gained, budget)
	}
	t.Logf("RE-ROUND F4: plain Load refunded 0 across restarted months 0-3 (stayed %d), resumed +%d at month 4",
		before.BodiesBuried, budget)
}

// ---------------------------------------------------------------------
// RE-ROUND 4 — the DEAD-BUDGET question: a save taken at month 12 loaded
// into an engine that then runs months 0..13. Reports, per restarted
// month, how many burials happened. If the answer is "zero for twelve
// months", that is a real gameplay consequence of the `>` fix and is
// reported as such, not hidden.
// ---------------------------------------------------------------------

func TestReroundBUG720_DeadBudgetWindowAfterLateSave(t *testing.T) {
	cid := errs.NewCorrelationID()
	cems := []DeathServiceCemeterySpec{{ID: "cem-1", Capacity: 100000}}
	// 4500 < the 5000 ceiling so dispensation never activates and confuses
	// the burial count; 13 months x 300 = 3900 of it can be transported.
	e, comp := rrWire(t, rrSeed+3, 1, nil, cems, 4500)
	ds := comp.DeathServices()
	budget, err := ds.HearseMonthlyBudget(cid)
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	// Reach mid-month 12 with month 12's budget spent.
	advanceInChunks(t, e, 12*int64(core.DailyTicksPerMonth)+10)
	before := rrCons(t, ds, cid, "pre-save month 12")
	if before.BodiesBuried != 13*budget {
		t.Fatalf("fixture: want %d buried by mid-month-12, got %d", 13*budget, before.BodiesBuried)
	}

	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eR, compR := rrWire(t, rrSeed+3, 1, nil, cems, 0)
	if err := compR.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	dsR := compR.DeathServices()

	prev := before.BodiesBuried
	firstLiveMonth := -1
	for m := 0; m <= 13; m++ {
		advanceInChunks(t, eR, int64(core.DailyTicksPerMonth))
		c := rrCons(t, dsR, cid, fmt.Sprintf("restarted month %d", m))
		gained := c.BodiesBuried - prev
		t.Logf("DEAD-BUDGET PROBE: restarted month %2d -> +%d burials (total %d)", m, gained, c.BodiesBuried)
		if gained > 0 && firstLiveMonth < 0 {
			firstLiveMonth = m
		}
		prev = c.BodiesBuried
	}
	if firstLiveMonth < 0 {
		t.Fatalf("DEAD BUDGET: the hearse fleet never transported again across 14 restarted months")
	}
	t.Logf("DEAD-BUDGET ANSWER: after a save at month 12, hearse transport resumes at restarted month %d "+
		"(frozen for months 0..%d — %d simulated months of no burials)", firstLiveMonth, firstLiveMonth-1, firstLiveMonth)
}

// ---------------------------------------------------------------------
// RE-ROUND 5 — determinism across pools with the truncation live.
// ---------------------------------------------------------------------

func TestReroundBUG720_DeterministicAcrossPoolsWithTruncation(t *testing.T) {
	cid := errs.NewCorrelationID()
	var ref deathservices.Conservation
	var refIDs []uint64
	for i, pool := range []int{1, 4, 20} {
		e, comp := rrWire(t, rrSeed+4, pool, []string{"crem-a", "crem-b"},
			[]DeathServiceCemeterySpec{{ID: "cem-1"}}, 3000)
		ds := comp.DeathServices()
		advanceInChunks(t, e, 3*int64(core.DailyTicksPerMonth))
		c := rrCons(t, ds, cid, fmt.Sprintf("pool %d", pool))
		ids, err := ds.AwaitingSorted(cid)
		if err != nil {
			t.Fatalf("AwaitingSorted: %v", err)
		}
		if i == 0 {
			ref, refIDs = c, ids
			continue
		}
		if c != ref {
			t.Fatalf("pool %d conservation diverged: %+v vs %+v", pool, c, ref)
		}
		if len(ids) != len(refIDs) {
			t.Fatalf("pool %d backlog size diverged: %d vs %d", pool, len(ids), len(refIDs))
		}
		for j := range ids {
			if ids[j] != refIDs[j] {
				t.Fatalf("pool %d backlog identity diverged at %d: %d vs %d", pool, j, ids[j], refIDs[j])
			}
		}
	}
	t.Logf("RE-ROUND: pools 1/4/20 identical after 3 months: %+v", ref)
}
