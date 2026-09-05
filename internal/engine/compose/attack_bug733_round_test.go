package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug733_round_test.go — INDEPENDENT destructive round against
// BUG-733 (attacker opus-round-bug733, NOT the author). These probe the
// axes the author's own bug733_cremation_shortfall_test.go does not:
// the ENGINE's own conservation invariant (RecomputeMoneyStock /
// FindConservationViolations) rather than a hand-rolled balance identity,
// multi-crematorium repayment ordering (GR#21), Deps-order independence,
// convergence-vs-treadmill under sustained income, and int64 saturation.

const attackBUG733Seed = uint64(733900)

// wireBUG733Multi builds a composition with the given crematorium ids in
// the given Deps order (deliberately allowing an UNSORTED Deps slice so
// the round can prove Wire's own sort.Strings makes the caller's ordering
// irrelevant — GR#21).
func wireBUG733Multi(t *testing.T, seed uint64, crematoria []string) (*core.Engine, *Composition, *deathservices.DeathServicesAPI, string) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: crematoria})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp, comp.DeathServices(), cid
}

// requireMoneyStockIntact is the ENGINE's own conservation check (AC-10 /
// AC-10b), not a hand-rolled arithmetic identity: the running money-stock
// total must equal a from-scratch walk of the ledger, and no transaction
// in the current tick may be unbalanced.
func requireMoneyStockIntact(t *testing.T, f *finance.FinanceAPI, label string) finance.Money {
	t.Helper()
	running := f.TotalMoneyInCirculation()
	scratch := f.RecomputeMoneyStock()
	if running != scratch {
		t.Fatalf("%s: money stock drift — TotalMoneyInCirculation()=%d but RecomputeMoneyStock()=%d", label, running, scratch)
	}
	if v := f.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("%s: FindConservationViolations() reported %d unbalanced transaction(s): %+v", label, len(v), v)
	}
	return running
}

func attackDrainTreasury(t *testing.T, f *finance.FinanceAPI) {
	t.Helper()
	drainTreasuryToZero(t, f)
}

// TestAttackBUG733_EngineConservationInvariantHoldsAcrossAccrueRepayArc is
// the round's headline conservation probe. The author's conservation test
// asserts a hand-computed "funded minus billed" identity; this one uses
// engine.finance's OWN invariant surfaces at EVERY step of a
// broke -> accrue -> accrue -> funded -> repay arc. Accruing a shortfall
// moves no money, so the stock must be IDENTICAL across the broke days;
// repaying moves exactly the debt out of the treasury, so the stock must
// fall by exactly that much and never by more (double payment) or less
// (a debt written off without a matching outflow).
func TestAttackBUG733_EngineConservationInvariantHoldsAcrossAccrueRepayArc(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, attackBUG733Seed)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	attackDrainTreasury(t, f)
	stock0 := requireMoneyStockIntact(t, f, "after drain")

	// Two broke days: money must not move at all, only the debt grows.
	if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
		t.Fatalf("Intake day1: %v", err)
	}
	advanceInChunks(t, e, 1)
	stock1 := requireMoneyStockIntact(t, f, "after broke day1")
	if stock1 != stock0 {
		t.Fatalf("BUG-733: an ACCRUED (unpaid) cremation shortfall moved money — stock %d -> %d; the debt must be tracked outside the ledger with zero money movement", stock0, stock1)
	}

	if _, err := ds.Intake(syntheticDeaths(1, 100, false), cid); err != nil {
		t.Fatalf("Intake day2: %v", err)
	}
	advanceInChunks(t, e, 1)
	stock2 := requireMoneyStockIntact(t, f, "after broke day2")
	if stock2 != stock1 {
		t.Fatalf("BUG-733: second accrual moved money — stock %d -> %d", stock1, stock2)
	}

	owed := f.CremationShortfallOwed()
	if owed != finance.Money(3*costPerBody) {
		t.Fatalf("fixture: owed=%d want %d", owed, 3*costPerBody)
	}

	// Funded day: fund exactly owed + one new body's cost, cremate one
	// more body. Every micropound funded must leave again as opex.
	if _, err := ds.Intake(syntheticDeaths(1, 200, false), cid); err != nil {
		t.Fatalf("Intake day3: %v", err)
	}
	funded := owed + finance.Money(costPerBody)
	fundTreasury(t, f, funded)
	stockFunded := requireMoneyStockIntact(t, f, "after funding")
	if stockFunded != stock2+funded {
		t.Fatalf("fixture: funding did not raise the stock by exactly %d (%d -> %d)", funded, stock2, stockFunded)
	}

	advanceInChunks(t, e, 1)
	stock3 := requireMoneyStockIntact(t, f, "after funded day3")

	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: debt not cleared on a fully funded day, owed=%d", got)
	}
	// The debt (3 bodies) plus day3's own body = 4 bodies of opex left
	// the treasury; the stock must have fallen by exactly that.
	wantStock := stockFunded - finance.Money(4*costPerBody)
	if stock3 != wantStock {
		t.Fatalf("BUG-733 conservation: stock after repay+opex = %d, want %d (funded stock %d - 4 bodies x %d) — money created or destroyed by the debt bookkeeping", stock3, wantStock, stockFunded, costPerBody)
	}
	// And the arc must be a closed loop: back to the pre-funding stock.
	if stock3 != stock2 {
		t.Fatalf("BUG-733 conservation: the full arc did not close — stock %d before funding, %d after repaying, want identical (everything funded was billed away)", stock2, stock3)
	}
}

// TestAttackBUG733_MultiCrematoriumRepayOrderIsDeterministicAndDepsOrderFree
// attacks the "all-or-nothing repay per crematorium per day" ordering
// question (GR#21): with TWO crematoria both cremating on a broke day and
// a later treasury that can cover only ONE repayment attempt, does the
// crematorium ORDER decide the outcome, and is that order stable across
// (a) repeated identical runs and (b) the order the ids were handed to
// Wire? compose sorts its roster (sort.Strings), so both must be exact.
func TestAttackBUG733_MultiCrematoriumRepayOrderIsDeterministicAndDepsOrderFree(t *testing.T) {
	// Read the real per-crematorium daily cap so the fixture cremates at
	// BOTH crematoria in one day (GR#15 — never a hardcoded 12).
	_, _, probeDS, probeCID := wireBUG733Multi(t, attackBUG733Seed+1, []string{"crem-a"})
	cfg, err := probeDS.Config(probeCID)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	throughput := cfg.CremationDailyThroughputPerBody()
	costPerBody := cfg.CremationCostPerBodyMicropounds()
	if throughput <= 0 {
		t.Fatalf("fixture: non-positive throughput %d", throughput)
	}
	// One full cap plus one body, so crematorium #2 must also run.
	bodies := int(throughput) + 1

	run := func(depsOrder []string) (owedAfterBroke, owedAfterPartial, bal finance.Money) {
		e, comp, ds, cid := wireBUG733Multi(t, attackBUG733Seed+1, depsOrder)
		f := comp.state.finance
		attackDrainTreasury(t, f)

		if _, err := ds.Intake(syntheticDeaths(bodies, 1, false), cid); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		advanceInChunks(t, e, 1)
		owedAfterBroke = f.CremationShortfallOwed()

		// Fund with strictly LESS than the whole debt but more than one
		// body: an all-or-nothing repayment must therefore fail at every
		// crematorium in the loop, leaving the debt exactly intact.
		fundTreasury(t, f, owedAfterBroke-finance.Money(costPerBody))
		advanceInChunks(t, e, 1)
		owedAfterPartial = f.CremationShortfallOwed()
		bal, _ = f.AccountBalance(finance.AcctTreasury)
		requireMoneyStockIntact(t, f, "multi-crematorium run")
		return
	}

	a1, a2, a3 := run([]string{"crem-a", "crem-b"})
	b1, b2, b3 := run([]string{"crem-a", "crem-b"})
	if a1 != b1 || a2 != b2 || a3 != b3 {
		t.Fatalf("BUG-733 GR#21: two identical runs diverged — (%d,%d,%d) vs (%d,%d,%d)", a1, a2, a3, b1, b2, b3)
	}
	// Same ids, reversed Deps order — Wire sorts the roster, so this must
	// be byte-identical too.
	c1, c2, c3 := run([]string{"crem-b", "crem-a"})
	if a1 != c1 || a2 != c2 || a3 != c3 {
		t.Fatalf("BUG-733 GR#21: the ORDER crematorium ids were handed to Wire changed the outcome — sorted (%d,%d,%d) vs reversed (%d,%d,%d)", a1, a2, a3, c1, c2, c3)
	}

	// NON-VACUITY: bodies == throughput+1, which a SINGLE crematorium
	// cannot clear in one day (its own daily cap is throughput). Seeing a
	// debt of exactly bodies*cost after ONE broke day therefore proves the
	// second crematorium's finance block really did run — without this the
	// ordering assertions above would pass trivially on a one-crematorium
	// path.
	wantMultiOwed := finance.Money(int64(bodies) * costPerBody)
	if a1 != wantMultiOwed {
		t.Fatalf("VACUITY GUARD: after one broke day owed=%d, want %d (%d bodies x %d) — both crematoria must have cremated in the same day for this ordering test to mean anything (throughput/crematorium=%d)", a1, wantMultiOwed, bodies, costPerBody, throughput)
	}
	if a2 != a1 {
		t.Fatalf("BUG-733: a partially-funded day must leave the all-or-nothing debt EXACTLY intact — owed %d -> %d", a1, a2)
	}
}

// TestAttackBUG733_DebtConvergesOnSustainedIncomeAndIsNotATreadmill probes
// the "repay-then-post starves today's opex so the debt merely rotates"
// treadmill hypothesis. With a per-day income strictly greater than the
// per-day cremation bill, the debt must reach zero and STAY zero (it is
// convergent, not a rotating balance that is repaid and immediately
// re-accrued at the same size forever).
func TestAttackBUG733_DebtConvergesOnSustainedIncomeAndIsNotATreadmill(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, attackBUG733Seed+2)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	attackDrainTreasury(t, f)

	// Three broke days, one body each: a real starting debt.
	for d := 0; d < 3; d++ {
		if _, err := ds.Intake(syntheticDeaths(1, uint64(1+d*100), false), cid); err != nil {
			t.Fatalf("Intake broke day %d: %v", d, err)
		}
		advanceInChunks(t, e, 1)
	}
	startOwed := f.CremationShortfallOwed()
	if startOwed != finance.Money(3*costPerBody) {
		t.Fatalf("fixture: startOwed=%d want %d", startOwed, 3*costPerBody)
	}

	// Now a sustained modest income: 2 bodies' worth of cash per day
	// against a 1-body-per-day bill. The surplus is only ONE body per
	// day, deliberately far below the 3-body debt, so an all-or-nothing
	// repayment cannot fire until the treasury has organically saved up
	// the whole balance — the exact shape a "treadmill" would stall on.
	const days = 12
	prev := startOwed
	cleared := -1
	for d := 0; d < days; d++ {
		fundTreasury(t, f, finance.Money(2*costPerBody))
		if _, err := ds.Intake(syntheticDeaths(1, uint64(10_000+d*100), false), cid); err != nil {
			t.Fatalf("Intake funded day %d: %v", d, err)
		}
		advanceInChunks(t, e, 1)
		owed := f.CremationShortfallOwed()
		if owed > prev {
			t.Fatalf("BUG-733 treadmill: on funded day %d the debt GREW (%d -> %d) even though the day's income (%d) exceeded its own bill (%d) — repay-then-post is starving today's opex and re-accruing it", d, prev, owed, 2*costPerBody, costPerBody)
		}
		if owed == 0 && cleared < 0 {
			cleared = d
		}
		prev = owed
		requireMoneyStockIntact(t, f, "treadmill day")
	}
	if cleared < 0 {
		t.Fatalf("BUG-733 treadmill: after %d funded days at 2x the daily bill the debt NEVER cleared (still %d owed of an original %d) — the all-or-nothing repayment is not convergent", days, prev, startOwed)
	}
	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: debt cleared on day %d but came back (%d owed at the end) — it rotates rather than settles", cleared, got)
	}
}

// TestAttackBUG733_StatusSurfaceIsZeroWithoutFinance guards the documented
// nil-finance default on the new DeathServicesRunStatus field.
func TestAttackBUG733_StatusSurfaceIsZeroWithoutFinance(t *testing.T) {
	_, comp, _, _ := wireBUG733(t, attackBUG733Seed+3)
	saved := comp.state.finance
	comp.state.finance = nil
	got := comp.DeathServicesRunStatus().CremationShortfallOwed
	comp.state.finance = saved
	if got != 0 {
		t.Fatalf("BUG-733: DeathServicesRunStatus().CremationShortfallOwed = %d with finance unwired, want 0", got)
	}
}
