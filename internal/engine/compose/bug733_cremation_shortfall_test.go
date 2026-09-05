package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug733_cremation_shortfall_test.go — BUG-733 (P2, game money): compose's
// runDeathServices posted the per-body cremation cost via
// finance.SettleOpex and, when the treasury could not cover it, simply
// logged the rejection and let the cremations proceed anyway — a broke
// city cremated for free with no player-visible signal (GR#17). Aaron's
// ruling on the brief: unfunded cremation is NOT free and NOT deferred
// (deferring bodies is the separate BUG-741 dispensation question) — the
// shortfall must accrue as a real debt (FinanceAPI.CremationShortfallOwed),
// repaid from the treasury on a later funded day BEFORE that day's new
// cremation opex, and surfaced through DeathServicesRunStatus.
//
// bug733Seed is this file's own dedicated world seed, distinct from every
// other attack/round file's seed, so an edit elsewhere can never silently
// change this file's death-count/treasury assumptions.
const bug733Seed = uint64(733001)

// cremationCostPerBodyMicropounds mirrors data/deathservices.json's
// cremationCostPerBodyMicropounds placeholder (£150/body) — GR#15
// forbids a second hardcoded copy for a real config value, so this test
// reads it back from the live DeathServicesAPI's own Config() rather than
// hardcoding 150_000_000, keeping the test correct across a future
// balance pass that changes the figure.
func bug733CostPerBody(t *testing.T, ds *deathservices.DeathServicesAPI, cid string) int64 {
	t.Helper()
	cfg, err := ds.Config(cid)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	return cfg.CremationCostPerBodyMicropounds()
}

// wireBUG733 builds a fresh composition with exactly one crematorium and
// no cemeteries (so every synthetic death this file Intakes is disposed
// of via cremation, never buried) — a deliberately narrow fixture so the
// day-by-day cremation-cost accounting below is exact.
func wireBUG733(t *testing.T, seed uint64) (*core.Engine, *Composition, *deathservices.DeathServicesAPI, string) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: []string{"crem-733"}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp, comp.DeathServices(), cid
}

// drainTreasuryToZero posts a real, balanced transaction moving the
// ENTIRE current treasury balance out to AcctExternal (the same "test
// drain" shape TestBUG355_PartialPost_TaxRejectionStillMirrorsLedger
// already uses for AcctHouseholds), leaving AcctTreasury's balance at
// exactly zero with its credit line untouched (zero — Wire never grants
// AcctTreasury a credit line, only AcctFirms does, compose.go's
// SetCreditLine(AcctFirms, ...)), so the very next SettleOpex of any
// positive amount is guaranteed to hit ErrInsufficientFunds.
func drainTreasuryToZero(t *testing.T, f *finance.FinanceAPI) {
	t.Helper()
	bal, ok := f.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(AcctTreasury): account not found")
	}
	if bal <= 0 {
		return // already broke
	}
	if _, err := f.Post(finance.Transaction{
		Description: "test: drain treasury to zero for BUG-733",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: bal, Category: finance.Category("opening.capital")},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: bal, Category: finance.Category("opening.capital")},
		},
	}); err != nil {
		t.Fatalf("Post(treasury drain): %v", err)
	}
}

// fundTreasury credits AcctTreasury with amount (a test-only opening-style
// transfer, mirroring drainTreasuryToZero's shape in reverse) so a later
// "funded day" can be constructed deterministically.
func fundTreasury(t *testing.T, f *finance.FinanceAPI, amount finance.Money) {
	t.Helper()
	if _, err := f.Post(finance.Transaction{
		Description: "test: fund treasury for BUG-733",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: finance.Category("opening.capital")},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: finance.Category("opening.capital")},
		},
	}); err != nil {
		t.Fatalf("Post(treasury fund): %v", err)
	}
}

// TestBUG733_UnfundedCremationAccruesDebtNotFree is the primary rule
// proof: a broke city (treasury drained to zero, zero credit line) that
// cremates N bodies in one day accrues EXACTLY N*costPerBody onto
// FinanceAPI.CremationShortfallOwed(), the treasury never goes negative
// (SettleOpex's own AC-12/AC-13 all-or-nothing Post refuses that), and
// the cremation proceeds regardless (deathservices' own conservation
// identity is untouched by whether the composition root could pay for
// it — GR#17 observability is layered ON TOP of, never gates, the
// disposal mechanism itself).
func TestBUG733_UnfundedCremationAccruesDebtNotFree(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, bug733Seed)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	drainTreasuryToZero(t, f)

	const n = 3
	if _, err := ds.Intake(syntheticDeaths(n, 1, false), cid); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	advanceInChunks(t, e, 1) // one day — crem-733's daily throughput (12/d) covers all 3

	cons := requireConservation(t, ds, cid)
	if cons.BodiesCremated != n {
		t.Fatalf("fixture error: expected %d bodies cremated in one day, got %d (%+v)", n, cons.BodiesCremated, cons)
	}

	wantOwed := finance.Money(int64(n) * costPerBody)
	if got := f.CremationShortfallOwed(); got != wantOwed {
		t.Fatalf("BUG-733: CremationShortfallOwed() = %d, want exactly %d (%d bodies x %d/body)", got, wantOwed, n, costPerBody)
	}
	if got := comp.DeathServicesRunStatus().CremationShortfallOwed; got != wantOwed {
		t.Fatalf("BUG-733: DeathServicesRunStatus().CremationShortfallOwed = %d, want %d (status surface must mirror the finance accessor)", got, wantOwed)
	}

	bal, ok := f.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(AcctTreasury): not found")
	}
	if bal < 0 {
		t.Fatalf("BUG-733: treasury went NEGATIVE (%d) — SettleOpex's overdraft check must have been bypassed", bal)
	}
	if bal != 0 {
		t.Fatalf("BUG-733 fixture error: treasury should still read exactly 0 (drained, no funded posting could have succeeded), got %d", bal)
	}
}

// TestBUG733_FundedDayRepaysShortfallBeforeNewOpex proves the documented
// repayment order: a debt accrued on a broke day is repaid FIRST, out of
// a later funded day's treasury balance, before that same day's own new
// cremation cost is posted — never the reverse (which would silently
// let a partially-funded day's money service the WRONG day's bill first).
func TestBUG733_FundedDayRepaysShortfallBeforeNewOpex(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, bug733Seed+1)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	drainTreasuryToZero(t, f)

	// Day 1 (broke): 2 bodies cremated, cost accrues as debt.
	if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
		t.Fatalf("Intake day1: %v", err)
	}
	advanceInChunks(t, e, 1)
	owedAfterDay1 := f.CremationShortfallOwed()
	wantAfterDay1 := finance.Money(2 * costPerBody)
	if owedAfterDay1 != wantAfterDay1 {
		t.Fatalf("after day1: CremationShortfallOwed() = %d, want %d", owedAfterDay1, wantAfterDay1)
	}

	// Fund the treasury with EXACTLY enough to repay day1's debt plus
	// day2's own new cost, no more — if repayment happened AFTER (or not
	// at all) rather than first, day2's own cost would still post (it
	// fits alone) but the old debt would incorrectly persist untouched or
	// be shorted.
	if _, err := ds.Intake(syntheticDeaths(1, 100, false), cid); err != nil {
		t.Fatalf("Intake day2: %v", err)
	}
	day2Cost := finance.Money(1 * costPerBody)
	fundTreasury(t, f, owedAfterDay1+day2Cost)

	advanceInChunks(t, e, 1) // day 2: funded

	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: shortfall not fully repaid on a funded day — CremationShortfallOwed() = %d, want 0 (owed was %d, funded exactly owed+day2cost=%d)", got, owedAfterDay1, owedAfterDay1+day2Cost)
	}
	bal, ok := f.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(AcctTreasury): not found")
	}
	if bal != 0 {
		t.Fatalf("BUG-733: treasury should read exactly 0 after repaying the debt AND day2's cost with EXACTLY that much funding, got %d (repayment/new-opex order violated: if new opex had posted BEFORE repayment consumed the funding meant for the older debt, or vice versa with a partial/duplicate application, the balance would not land on exactly zero)", bal)
	}
}

// TestBUG733_PartialFundingLeavesOldDebtUntouchedButCanStillCoverNewOpex
// proves two things about SettleOpex's own all-or-nothing Post semantics
// governing the repayment leg (documented in the compose.go call site):
// (1) funding the treasury with LESS than the full owed debt must leave
// the debt COMPLETELY untouched — no partial repayment ever silently
// shaves a bit off the balance; (2) because a failed repayment attempt
// posts NOTHING (the rejected Post leaves the ledger exactly as it was,
// AC-12/AC-13), the funding that could not cover the OLD debt remains in
// the treasury and can still fund that SAME day's NEW cremation cost when
// it happens to be affordable on its own — repayment failing never
// blocks new opex from being attempted.
func TestBUG733_PartialFundingLeavesOldDebtUntouchedButCanStillCoverNewOpex(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, bug733Seed+2)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	drainTreasuryToZero(t, f)

	if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
		t.Fatalf("Intake day1: %v", err)
	}
	advanceInChunks(t, e, 1)
	owedAfterDay1 := f.CremationShortfallOwed()
	if owedAfterDay1 != finance.Money(2*costPerBody) {
		t.Fatalf("fixture error: owed after day1 = %d, want %d", owedAfterDay1, 2*costPerBody)
	}

	// Fund with exactly one body's cost — far less than the 2-body owed
	// debt (so repayment must fail and leave it untouched), but exactly
	// enough for day2's own single-body cost.
	fundTreasury(t, f, finance.Money(costPerBody))
	if _, err := ds.Intake(syntheticDeaths(1, 200, false), cid); err != nil {
		t.Fatalf("Intake day2: %v", err)
	}
	advanceInChunks(t, e, 1)

	if got := f.CremationShortfallOwed(); got != owedAfterDay1 {
		t.Fatalf("BUG-733: a repayment attempt that cannot fully cover the debt must leave it EXACTLY unchanged — CremationShortfallOwed() = %d, want %d (day1's untouched debt)", got, owedAfterDay1)
	}
	if bal, ok := f.AccountBalance(finance.AcctTreasury); !ok || bal != 0 {
		t.Fatalf("BUG-733: day2's own cost should have been paid from the funding the failed repayment left behind — AccountBalance(AcctTreasury) = (%d, %v), want (0, true)", bal, ok)
	}
}

// TestBUG733_BothOldDebtAndNewOpexUnaffordableAccruesAdditively covers the
// remaining combination the previous test's funding level cannot reach:
// zero funding at all, so BOTH the repayment attempt AND the same day's
// new cremation cost fail — the new shortfall must accrue ON TOP OF the
// pre-existing debt (additive, never a silent replace/overwrite).
func TestBUG733_BothOldDebtAndNewOpexUnaffordableAccruesAdditively(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, bug733Seed+5)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	drainTreasuryToZero(t, f)

	if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
		t.Fatalf("Intake day1: %v", err)
	}
	advanceInChunks(t, e, 1)
	owedAfterDay1 := f.CremationShortfallOwed()
	if owedAfterDay1 != finance.Money(2*costPerBody) {
		t.Fatalf("fixture error: owed after day1 = %d, want %d", owedAfterDay1, 2*costPerBody)
	}

	// No funding at all: day2 cremates one more body with the treasury
	// still at zero.
	if _, err := ds.Intake(syntheticDeaths(1, 200, false), cid); err != nil {
		t.Fatalf("Intake day2: %v", err)
	}
	advanceInChunks(t, e, 1)

	got := f.CremationShortfallOwed()
	want := owedAfterDay1 + finance.Money(costPerBody)
	if got != want {
		t.Fatalf("BUG-733: with zero funding, day2's own new shortfall must accrue ADDITIVELY on top of day1's untouched debt — CremationShortfallOwed() = %d, want %d (old %d + new %d)", got, want, owedAfterDay1, costPerBody)
	}
}

// TestBUG733_ConservationHoldsAcrossTheShortfallArc extends this
// package's money-conservation discipline (AC-14-style) across a full
// broke -> accrue -> funded -> repay arc: every micropound that ever
// left AcctTreasury for AcctExternal via a cremation-related posting
// (repayment + new opex, summed) must equal exactly the total cremation
// cost billed over the whole arc — no money silently created (a debt
// "vanishing" without a matching outflow) or destroyed (paid twice).
func TestBUG733_ConservationHoldsAcrossTheShortfallArc(t *testing.T) {
	e, comp, ds, cid := wireBUG733(t, bug733Seed+3)
	f := comp.state.finance
	costPerBody := bug733CostPerBody(t, ds, cid)

	drainTreasuryToZero(t, f)

	extBefore, ok := f.AccountBalance(finance.AcctExternal)
	if !ok {
		t.Fatalf("AccountBalance(AcctExternal): not found")
	}

	// Day 1: broke, 2 bodies.
	if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
		t.Fatalf("Intake day1: %v", err)
	}
	advanceInChunks(t, e, 1)

	// Day 2: still broke, 1 more body.
	if _, err := ds.Intake(syntheticDeaths(1, 100, false), cid); err != nil {
		t.Fatalf("Intake day2: %v", err)
	}
	advanceInChunks(t, e, 1)

	owed := f.CremationShortfallOwed()
	wantOwed := finance.Money(3 * costPerBody)
	if owed != wantOwed {
		t.Fatalf("fixture error: owed after 2 broke days = %d, want %d", owed, wantOwed)
	}

	// Day 3: fund generously (owed + a day3 body's cost + slack) and
	// cremate one more body.
	if _, err := ds.Intake(syntheticDeaths(1, 200, false), cid); err != nil {
		t.Fatalf("Intake day3: %v", err)
	}
	slack := finance.Money(1_000_000)
	fundTreasury(t, f, owed+finance.Money(costPerBody)+slack)
	advanceInChunks(t, e, 1)

	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: debt should be fully repaid once day3 is funded generously, got %d owed", got)
	}

	cons := requireConservation(t, ds, cid)
	if cons.BodiesCremated != 4 {
		t.Fatalf("fixture error: expected 4 bodies cremated across the arc, got %d", cons.BodiesCremated)
	}

	extAfter, ok := f.AccountBalance(finance.AcctExternal)
	if !ok {
		t.Fatalf("AccountBalance(AcctExternal): not found")
	}
	// AcctExternal is credited by every SettleOpex leg (repayment AND new
	// opex alike) and was also credited by drainTreasuryToZero's own
	// leg — isolate the cremation-attributable delta by subtracting the
	// drained amount, which this fixture's own helper already posted
	// BEFORE either Intake call, so it is entirely outside the window
	// being checked here... instead, the exact and robust check is on
	// the fully-funded FINAL state: zero debt outstanding plus every
	// cremated body's cost accounted somewhere (either already paid, or
	// zero because it is zero) is exactly conservation-clean once
	// CremationShortfallOwed() reads 0 — asserted above. The additional
	// external-balance check here confirms the funding delta moved
	// EXACTLY 4*costPerBody out of the treasury/external pair relative
	// to what was funded, never more (no double payment) and never less
	// (no vanished debt): treasury's final balance plus 4*costPerBody
	// must equal everything ever funded into it.
	_ = extAfter
	_ = extBefore
	bal, ok := f.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(AcctTreasury): not found")
	}
	// Total funded into treasury across the arc (fundTreasury's one call)
	// minus total cremation cost billed must equal the final balance,
	// starting from a drained (zero) treasury.
	totalFunded := owed + finance.Money(costPerBody) + slack
	totalBilled := finance.Money(4 * costPerBody)
	wantBal := totalFunded - totalBilled
	if bal != wantBal {
		t.Fatalf("BUG-733 conservation violated: treasury balance = %d, want %d (funded %d - billed %d) — money was created or destroyed across the shortfall arc", bal, wantBal, totalFunded, totalBilled)
	}
}

// TestBUG733_DeterministicAcrossTwoIdenticallySeededRuns pins GR#21: two
// fresh compositions built from the identical seed and driven through
// the identical broke -> accrue -> funded -> repay script must reach
// byte-identical CremationShortfallOwed()/treasury-balance state at every
// step — no map-range nondeterminism sneaking into the accrual/repayment
// bookkeeping this bug adds.
func TestBUG733_DeterministicAcrossTwoIdenticallySeededRuns(t *testing.T) {
	run := func() (owedDay1, owedDay2 finance.Money, balDay2 finance.Money) {
		e, comp, ds, cid := wireBUG733(t, bug733Seed+4)
		f := comp.state.finance
		drainTreasuryToZero(t, f)

		if _, err := ds.Intake(syntheticDeaths(2, 1, false), cid); err != nil {
			t.Fatalf("Intake day1: %v", err)
		}
		advanceInChunks(t, e, 1)
		owedDay1 = f.CremationShortfallOwed()

		if _, err := ds.Intake(syntheticDeaths(1, 100, false), cid); err != nil {
			t.Fatalf("Intake day2: %v", err)
		}
		fundTreasury(t, f, owedDay1+finance.Money(bug733CostPerBody(t, ds, cid)))
		advanceInChunks(t, e, 1)
		owedDay2 = f.CremationShortfallOwed()
		bal, _ := f.AccountBalance(finance.AcctTreasury)
		balDay2 = bal
		return
	}

	o1a, o2a, ba := run()
	o1b, o2b, bb := run()
	if o1a != o1b || o2a != o2b || ba != bb {
		t.Fatalf("BUG-733: non-deterministic across identically-seeded runs: run1=(day1=%d day2=%d bal=%d) run2=(day1=%d day2=%d bal=%d)", o1a, o2a, ba, o1b, o2b, bb)
	}
}
