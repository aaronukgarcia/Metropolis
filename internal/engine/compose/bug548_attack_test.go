package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-548 independent destructive round (attacker "opus-round-bug548",
// 2026-09-05) — composition half. The fix splits the wage bill into a
// firms-paid PRIVATE leg and a treasury-paid PUBLIC leg, replaces a fake
// 100% income-tax clawback with the real 28% rate, reorders consumption
// ahead of wages, and grants AcctFirms a fixed credit line. These tests
// attack all four, deriving every expectation from the LEDGER (GR#15).

// allLedgerAccounts is the complete account set NewFinanceAPI opens.
// Summing every one of them (money, external AND liability) must be
// identically zero under double entry — this is the conservation law the
// tracked-stock invariant (treasury+households only, TrackedDelta
// measured from the same ledger it checks) structurally cannot catch.
var allLedgerAccounts = []finance.AccountID{
	finance.AcctTreasury,
	finance.AcctHouseholds,
	finance.AcctFirms,
	finance.AcctReserves,
	finance.AcctDebt,
	finance.AcctExternal,
}

func ledgerSumAll(f *finance.FinanceAPI) int64 {
	var sum int64
	for _, id := range allLedgerAccounts {
		sum += ledgerBalance(f, id)
	}
	return sum
}

// wageLegs reads THIS month's wage postings straight off the ledger:
// what households were credited, and which account paid each share.
func wageLegs(f *finance.FinanceAPI) (creditedToHouseholds, paidByFirms, paidByTreasury int64) {
	for _, e := range f.LinesByCategory(finance.CatWages) {
		switch {
		case e.Account == finance.AcctHouseholds && e.Side == finance.SideCredit:
			creditedToHouseholds += int64(e.Amount)
		case e.Account == finance.AcctFirms && e.Side == finance.SideDebit:
			paidByFirms += int64(e.Amount)
		case e.Account == finance.AcctTreasury && e.Side == finance.SideDebit:
			paidByTreasury += int64(e.Amount)
		}
	}
	return
}

func incomeTaxCollected(f *finance.FinanceAPI) int64 {
	var got int64
	for _, e := range f.LinesByCategory(finance.CatTaxIncome) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideCredit {
			got += int64(e.Amount)
		}
	}
	return got
}

// TestBUG548Attack_ConservationHoldsEveryMonth drives a real composed
// 24-month run and asserts, at EVERY month boundary, that the ledger's
// double-entry law holds across every account (including the untracked
// AcctFirms the new wage leg draws on) and that no transaction that
// month broke conservation.
func TestBUG548Attack_ConservationHoldsEveryMonth(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	f := comp.state.finance
	if sum := ledgerSumAll(f); sum != 0 {
		t.Fatalf("opening ledger sum = %d, want 0", sum)
	}
	for month := 1; month <= 24; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
		if sum := ledgerSumAll(f); sum != 0 {
			t.Fatalf("month %d: sum of every ledger account = %d, want 0 — money created or destroyed by the firms->households wage leg", month, sum)
		}
		if v := f.FindConservationViolations(); len(v) != 0 {
			t.Fatalf("month %d: FindConservationViolations = %+v", month, v)
		}
	}
}

// TestBUG548Attack_WagePayersAndTaxAreExact is the core money-path
// attack: for every month of a real run, (a) what households received
// equals exactly what firms plus treasury paid, (b) the income tax
// landing in treasury is exactly 28% of the posted bill — never the
// retired 100% clawback, never zero, never double-counted — and (c) the
// posted bill never falls below the documented monthlyWagesFloor.
func TestBUG548Attack_WagePayersAndTaxAreExact(t *testing.T) {
	e, comp := newTestEngine(t, 7)
	f := comp.state.finance
	sawFirmsLeg := false
	for month := 1; month <= 18; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
		credited, byFirms, byTreasury := wageLegs(f)
		if credited == 0 {
			t.Fatalf("month %d: no wages credited to households at all", month)
		}
		if credited != byFirms+byTreasury {
			t.Fatalf("month %d: households credited %d but payers debited %d (firms %d + treasury %d) — an unbalanced wage leg", month, credited, byFirms+byTreasury, byFirms, byTreasury)
		}
		if byFirms > 0 {
			sawFirmsLeg = true
		}
		if credited < monthlyWagesFloor {
			t.Fatalf("month %d: posted wage bill %d is below the documented monthlyWagesFloor %d — the floor was lost in the public/private split", month, credited, monthlyWagesFloor)
		}
		wantTax := credited * incomeNITaxRateBp / 10_000
		gotTax := incomeTaxCollected(f)
		if gotTax != wantTax {
			t.Fatalf("month %d: income tax credited to treasury = %d, want exactly %d (%d bp of the %d posted) — the 28%% deduction distributeWagesToResidents takes from every citizen must land in the treasury, not vanish", month, gotTax, wantTax, incomeNITaxRateBp, credited)
		}
		// The retired clawback: a 100% income "tax" would equal the whole
		// bill. Prove it is GONE, not merely reduced.
		if gotTax == credited {
			t.Fatalf("month %d: income tax (%d) equals the whole wage bill — the fake 100%% clawback is still live", month, gotTax)
		}
	}
	if !sawFirmsLeg {
		t.Fatal("firms never paid a single private-sector wage across 18 months — PostWagesFromFirms is not wired (built-but-not-wired)")
	}
}

// TestBUG548Attack_TreasuryNoLongerPaysEveryWage proves the actual
// regression this bug is about: over a real run the treasury's share of
// the wage bill must be a MINORITY (public sector only), and firms must
// carry the bulk. A revert to "treasury pays 100%" reds this.
func TestBUG548Attack_TreasuryNoLongerPaysEveryWage(t *testing.T) {
	e, comp := newTestEngine(t, 99)
	f := comp.state.finance
	var totalFirms, totalTreasury int64
	for month := 1; month <= 12; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
		_, byFirms, byTreasury := wageLegs(f)
		totalFirms += byFirms
		totalTreasury += byTreasury
	}
	if totalFirms <= totalTreasury {
		t.Fatalf("over 12 months firms paid %d of the wage bill and the treasury paid %d — the treasury is still the dominant payer (BUG-548's whole subject)", totalFirms, totalTreasury)
	}
}

// TestBUG548Attack_SectorSplitIsLoadBearing proves employedPublic is
// derived from each resident's ACTUAL current sector, not a constant: a
// city whose employed residents are all forced to SectorPublic must bill
// the whole payroll to the TREASURY, and one forced to a private sector
// must bill it to FIRMS. This is the hostile-employment-mix attack the
// production code's own doc comment claims ("employedPublic is not
// always zero") but which an organic run barely exercises.
func TestBUG548Attack_SectorSplitIsLoadBearing(t *testing.T) {
	treasuryShare := map[string]int64{}
	for _, tc := range []struct {
		name   string
		sector citizens.Sector
	}{
		{"all-public", citizens.SectorPublic},
		{"all-private", citizens.SectorTertiary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, comp := newTestEngine(t, 5)
			st := comp.state
			// Settle employment for one month first, then force every
			// employed resident into the sector under test.
			if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
				t.Fatalf("AdvanceTicks(settle): %v", err)
			}
			forced := 0
			for _, id := range st.liveResidentIDs() {
				cit, ok := st.citizens.CitizenAt(id, st.cid)
				if !ok || cit.Employment.State != citizens.EmploymentEmployed {
					continue
				}
				if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
					CorrelationID: st.cid,
					Kind:          citizens.LifeEventEmployment,
					CitizenID:     id,
					Employment:    citizens.EmploymentEmployed,
					Sector:        tc.sector,
				}); err != nil {
					t.Fatalf("force sector on %d: %v", id, err)
				}
				forced++
			}
			if forced == 0 {
				t.Fatal("no employed residents to force — fixture is vacuous")
			}
			if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
				t.Fatalf("AdvanceTicks(month 2): %v", err)
			}
			credited, byFirms, byTreasury := wageLegs(comp.state.finance)
			if credited != byFirms+byTreasury {
				t.Fatalf("unbalanced wage leg: credited %d, paid %d", credited, byFirms+byTreasury)
			}
			t.Logf("%s: forced=%d credited=%d firms=%d treasury=%d", tc.name, forced, credited, byFirms, byTreasury)
			treasuryShare[tc.name] = byTreasury
			// The treasury's share must be a whole number of public
			// headcounts at the real gross wage — never a floor top-up,
			// never a fudge (the monthlyWagesFloor shortfall is
			// attributed entirely to the PRIVATE bucket by design).
			if byTreasury%monthlyWageGrossPerEmployedMicropounds != 0 {
				t.Fatalf("%s: treasury wage share %d is not a whole multiple of the gross monthly wage %d — the public leg is not headcount-derived", tc.name, byTreasury, int64(monthlyWageGrossPerEmployedMicropounds))
			}
		})
	}
	// The load-bearing proof: forcing every worker public must move a
	// materially larger share of the SAME payroll onto the treasury than
	// forcing them private. If Employment.Sector were still economically
	// dead (BUG-548's headline), these two would be identical.
	pub, priv := treasuryShare["all-public"], treasuryShare["all-private"]
	if pub <= priv {
		t.Fatalf("treasury wage share: all-public city = %d, all-private city = %d — forcing every worker into SectorPublic did not shift the bill onto the treasury, so Employment.Sector is still economically dead", pub, priv)
	}
}

// TestBUG548Attack_CreditLineExhaustion_FailureModeIsBoundedButSilent is
// the credit-line-cap attack. AcctFirms is drained to within a hair of
// firmsWageCreditLineMicropounds (exactly the state a large employed
// population reaches organically — the cap is FIXED, sized by
// firmsWageCreditLinePopulationCeiling/-RunwayMonths, while payroll grows
// with the city), then one more month is run.
//
// REWRITTEN post-fix (BUG-548, 2026-09-05, round re-verification): the
// round's original two findings here were (1) the posted wage bill
// silently collapsed below monthlyWagesFloor, and (2) distributeWagesToResidents
// kept crediting every employed citizen's per-citizen Wealth regardless of
// what the ledger actually posted. Fix #1 (the floor is now UNCONDITIONAL
// — financeHook.ApplyEffect falls back to the treasury) and fix #4 (the
// two accountings are now COUPLED via distributeWagesToResidents'
// creditPrivateSector gate) directly retire both findings; this test now
// asserts the FIXED state as a permanent regression guard instead of
// documenting the bug.
func TestBUG548Attack_CreditLineExhaustion_FailureModeIsBoundedButSilent(t *testing.T) {
	e, comp := newTestEngine(t, 11)
	st := comp.state
	f := st.finance

	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)*2); err != nil {
		t.Fatalf("AdvanceTicks(warmup): %v", err)
	}
	baseCredited, baseFirms, _ := wageLegs(f)
	if baseCredited == 0 || baseFirms == 0 {
		t.Fatalf("warmup produced no firms-paid wages (credited=%d firms=%d) — fixture is vacuous", baseCredited, baseFirms)
	}

	// Drain AcctFirms to leave a residue far smaller than one month's
	// payroll. Any real city reaches this state eventually: the cap is
	// fixed, the payroll is not.
	firmsBal := ledgerBalance(f, finance.AcctFirms)
	const residue = 1_000
	drain := firmsBal + firmsWageCreditLineMicropounds - residue
	if drain <= 0 {
		t.Fatalf("nothing to drain (firms balance %d)", firmsBal)
	}
	if _, err := f.Post(finance.Transaction{
		Description: "attack: exhaust the firms working-capital line",
		Entries: []finance.Entry{
			{Account: finance.AcctFirms, Side: finance.SideDebit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
		},
	}); err != nil {
		t.Fatalf("drain post: %v", err)
	}

	// Per-citizen Wealth of the employed cohort BEFORE the starved month,
	// split by sector — fix #4's gate only withholds credit from
	// NON-public employed citizens when the firms leg fails, so the two
	// cohorts have different expectations after the starved month.
	privateEmployedIDs := make([]uint64, 0, 128)
	publicEmployedIDs := make([]uint64, 0, 8)
	wealthBefore := map[uint64]int64{}
	for _, id := range st.liveResidentIDs() {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok || cit.Employment.State != citizens.EmploymentEmployed {
			continue
		}
		wealthBefore[id] = cit.Wealth
		if cit.Employment.Sector == citizens.SectorPublic {
			publicEmployedIDs = append(publicEmployedIDs, id)
		} else {
			privateEmployedIDs = append(privateEmployedIDs, id)
		}
	}
	if len(privateEmployedIDs)+len(publicEmployedIDs) == 0 {
		t.Fatal("no employed residents — fixture is vacuous")
	}

	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks(starved month): %v", err)
	}

	credited, byFirms, byTreasury := wageLegs(f)
	firmsAfter := ledgerBalance(f, finance.AcctFirms)
	t.Logf("starved month: credited=%d firms=%d treasury=%d firmsBalance=%d creditLine=%d", credited, byFirms, byTreasury, firmsAfter, int64(firmsWageCreditLineMicropounds))

	// BOUNDED: the cap holds absolutely.
	if firmsAfter < -int64(firmsWageCreditLineMicropounds) {
		t.Fatalf("AcctFirms = %d, past its credit line -%d — the cap is not a cap", firmsAfter, int64(firmsWageCreditLineMicropounds))
	}
	// Conservation still holds through the rejection.
	if sum := ledgerSumAll(f); sum != 0 {
		t.Fatalf("ledger sum = %d after a rejected wage post, want 0", sum)
	}
	// The private wage leg is GONE this month (that is the failure) —
	// firms itself never pays a penny once its line is exhausted.
	if byFirms >= baseFirms {
		t.Fatalf("firms still paid %d (baseline %d) with an exhausted credit line — the drain did not reproduce the cap", byFirms, baseFirms)
	}
	if byFirms != 0 {
		t.Fatalf("firms paid %d with an exhausted credit line — expected a total rejection", byFirms)
	}

	// FIX #1 VERIFICATION: the monthlyWagesFloor safety net is now
	// UNCONDITIONAL — it must never be breached just because firms
	// rejected the post, regardless of whether the resulting posted bill
	// happens to be at, above, or (pre-fix) below the pre-exhaustion
	// baseline.
	if credited < monthlyWagesFloor {
		t.Fatalf("posted wage bill %d fell BELOW the monthlyWagesFloor safety net %d after the firms credit line was exhausted — the floor is supposed to be unconditional (BUG-548 fix #1)", credited, int64(monthlyWagesFloor))
	}
	t.Logf("post-fix: with the firms working-capital line exhausted, the posted wage bill is %d (baseline %d, floor %d) — firms paid 0 but the floor held via a treasury backstop", credited, baseCredited, int64(monthlyWagesFloor))

	// FIX #4 VERIFICATION: the two accountings are now COUPLED. A
	// non-public employed citizen must NOT have been credited Wealth this
	// month (the ledger did not pay them — firms rejected outright).
	// Sector is re-read AFTER the starved month (not the pre-tick
	// snapshot): citizens.ColdShard's independent matchJob can legitimately
	// reassign a citizen's sector to SectorPublic mid-tick, before
	// distributeWagesToResidents runs — that citizen is correctly paid via
	// the treasury this same month, which is not the finding under test.
	privateStillPaid := 0
	for _, id := range privateEmployedIDs {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok || cit.Employment.State != citizens.EmploymentEmployed {
			continue
		}
		if cit.Employment.Sector == citizens.SectorPublic {
			continue // reassigned to public mid-tick — legitimately paid via treasury
		}
		if cit.Wealth > wealthBefore[id] {
			privateStillPaid++
		}
	}
	if privateStillPaid != 0 {
		t.Fatalf("%d of %d private-sector employed citizens had their Wealth credited despite firms posting ZERO wages to the ledger — the two accountings are still uncoupled (BUG-548 fix #4 regression)", privateStillPaid, len(privateEmployedIDs))
	}
	// A legitimate public-sector employed citizen (paid via the treasury,
	// a leg this attack never starves) is UNAFFECTED by the gate and may
	// still be credited normally. Sector is re-read AFTER the tick for the
	// same reason as the private loop above — matchJob can also reassign
	// a citizen OUT of SectorPublic mid-tick, which is not this fix's
	// concern.
	stillPublic := 0
	publicPaid := 0
	for _, id := range publicEmployedIDs {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok || cit.Employment.State != citizens.EmploymentEmployed || cit.Employment.Sector != citizens.SectorPublic {
			continue
		}
		stillPublic++
		if cit.Wealth > wealthBefore[id] {
			publicPaid++
		}
	}
	if stillPublic > 0 && publicPaid != stillPublic {
		t.Fatalf("%d of %d still-public employed citizens were NOT credited even though the treasury wage leg is unaffected by this attack", stillPublic-publicPaid, stillPublic)
	}
	t.Logf("BUG-548 fix verified: firms posted ZERO wages, 0 of %d private-sector employed citizens were credited (coupled), the monthlyWagesFloor safety net held (%d >= %d), and %d/%d still-public-sector employed citizens were unaffected", len(privateEmployedIDs), credited, int64(monthlyWagesFloor), publicPaid, stillPublic)
}

// TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable follows the
// credit-line cap to its conclusion: firms' only modelled revenue is the
// ~£55/household/month consumption leg (45% of which is immediately
// taxed away), which is orders of magnitude below payroll, so once the
// working-capital line is exhausted firms can NEVER refill it (private
// wages stay at 0/12 in the fixed state too — fix #2 changed how the
// cap is SIZED, not whether a sufficiently large city can still exceed
// it, which the round explicitly flagged as an accepted, documented
// residual risk). This test measures how many of the next 12 months post
// a private wage and whether the monthlyWagesFloor safety net holds, so
// the severity of the cap is a number rather than an opinion.
//
// REWRITTEN post-fix (BUG-548, 2026-09-05, round re-verification): the
// round's original finding was the floor breached on every one of the 12
// post-exhaustion months. Fix #1 (financeHook.ApplyEffect's unconditional
// treasury backstop) retires that finding; this test now asserts the
// floor holds on EVERY month as a permanent regression guard, and also
// checks population is never destroyed by the BUG-452 rent-burden
// emigration collapse the original (unfixed) floor breach would have
// reproduced.
func TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable(t *testing.T) {
	e, comp := newTestEngine(t, 11)
	st := comp.state
	f := st.finance

	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)*2); err != nil {
		t.Fatalf("AdvanceTicks(warmup): %v", err)
	}
	popBefore := comp.Population()
	firmsBal := ledgerBalance(f, finance.AcctFirms)
	drain := firmsBal + firmsWageCreditLineMicropounds - 1_000
	if _, err := f.Post(finance.Transaction{
		Description: "attack: exhaust the firms working-capital line",
		Entries: []finance.Entry{
			{Account: finance.AcctFirms, Side: finance.SideDebit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
		},
	}); err != nil {
		t.Fatalf("drain post: %v", err)
	}

	monthsWithPrivateWages := 0
	monthsBelowFloor := 0
	for month := 1; month <= 12; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
		credited, byFirms, _ := wageLegs(f)
		if byFirms > 0 {
			monthsWithPrivateWages++
		}
		if credited < monthlyWagesFloor {
			monthsBelowFloor++
		}
		if sum := ledgerSumAll(f); sum != 0 {
			t.Fatalf("month %d: ledger sum = %d, want 0", month, sum)
		}
	}
	popAfter := comp.Population()
	t.Logf("post-fix: 12 months after the firms credit line was exhausted — months that paid ANY private wage: %d/12; months whose posted bill was BELOW the monthlyWagesFloor safety net: %d/12; population %d -> %d",
		monthsWithPrivateWages, monthsBelowFloor, popBefore, popAfter)
	if monthsWithPrivateWages > 0 {
		t.Logf("firms recovered on %d month(s) — the deadlock is not absolute at this scale", monthsWithPrivateWages)
	}
	// FIX #1 VERIFICATION: the monthlyWagesFloor safety net must now hold
	// UNCONDITIONALLY, every single month, for as long as the firms leg
	// stays exhausted — never just "most months".
	if monthsBelowFloor != 0 {
		t.Fatalf("the monthlyWagesFloor safety net was breached on %d/12 months after the firms credit line was exhausted — the floor is supposed to be unconditional (BUG-548 fix #1 regression)", monthsBelowFloor)
	}
	// The BUG-452 rent-burden emigration collapse the original (unfixed)
	// floor breach reproduced must not recur now that the floor holds:
	// population should not be gutted by a starved wage bill.
	if popAfter < popBefore/2 {
		t.Fatalf("population collapsed from %d to %d (more than halved) across 12 starved months — the BUG-452 rent-burden emigration collapse has resurfaced despite the floor holding", popBefore, popAfter)
	}
}

// TestBUG548Attack_DeterministicLedger — same seed, same ledger, entry
// for entry, month for month (GR#21). Catches any map-range creeping
// into the public/private split.
func TestBUG548Attack_DeterministicLedger(t *testing.T) {
	run := func(seed uint64) []string {
		e, comp := newTestEngine(t, seed)
		f := comp.state.finance
		var out []string
		for month := 1; month <= 12; month++ {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
				t.Fatalf("AdvanceTicks: %v", err)
			}
			for _, id := range allLedgerAccounts {
				out = append(out, fmt.Sprintf("m%d %s=%d", month, id, ledgerBalance(f, id)))
				for i, entry := range f.Lines(id) {
					out = append(out, fmt.Sprintf("m%d %s#%d %s %v %d", month, id, i, entry.Category, entry.Side, entry.Amount))
				}
			}
		}
		return out
	}
	a, b := run(2026), run(2026)
	if len(a) != len(b) {
		t.Fatalf("ledger stream lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("ledger diverged at line %d: %q vs %q", i, a[i], b[i])
		}
	}
	if len(a) == 0 {
		t.Fatal("empty ledger stream — vacuous determinism check")
	}
}

// TestBUG548Attack_ConsumptionBeforeWages_NoHouseholdOverdraft attacks
// the ORDERING change. Consumption spend + council tax now debit
// households BEFORE the month's wages credit them. If households were
// ever short, PostHouseholdSpend would reject and the consumption leg
// would silently vanish — a regression the reorder could introduce.
func TestBUG548Attack_ConsumptionBeforeWages_NoHouseholdOverdraft(t *testing.T) {
	e, comp := newTestEngine(t, 3)
	f := comp.state.finance
	for month := 1; month <= 12; month++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", month, err)
		}
		var spend int64
		for _, entry := range f.LinesByCategory(finance.CatSpend) {
			if entry.Account == finance.AcctHouseholds && entry.Side == finance.SideDebit {
				spend += int64(entry.Amount)
			}
		}
		var council int64
		for _, entry := range f.LinesByCategory(finance.CatTaxCouncil) {
			if entry.Account == finance.AcctHouseholds && entry.Side == finance.SideDebit {
				council += int64(entry.Amount)
			}
		}
		if spend <= 0 {
			t.Fatalf("month %d: consumption spend leg posted %d — households could not pay BEFORE receiving wages (the reorder starved the consumption leg)", month, spend)
		}
		if council <= 0 {
			t.Fatalf("month %d: council tax leg posted %d", month, council)
		}
		if got := ledgerBalance(f, finance.AcctHouseholds); got < 0 {
			t.Fatalf("month %d: households balance %d went negative", month, got)
		}
	}
}
