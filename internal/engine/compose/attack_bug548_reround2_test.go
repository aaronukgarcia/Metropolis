package compose

import (
	"fmt"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-548 INDEPENDENT re-round 2 (attacker "opus-reround2-bug548",
// 2026-09-05). Round 1 REJECTED on four findings; the author claims all
// four fixed. These tests are written against the FIXED tree and are
// deliberately disjoint from (not a rewrite of) the author-held
// bug548_attack_test.go: they attack the TOP-UP BACKSTOP itself (mint vs
// transfer, double payment), the GR#17 status surface's clear semantics
// and save/restore behaviour, per-citizen credit exactly-once across a
// mid-exhaustion sector switch, and determinism THROUGH exhaustion.

// r2Accounts is every account NewFinanceAPI opens; their signed sum must
// be identically zero under double entry.
var r2Accounts = []finance.AccountID{
	finance.AcctTreasury,
	finance.AcctHouseholds,
	finance.AcctFirms,
	finance.AcctReserves,
	finance.AcctDebt,
	finance.AcctExternal,
}

func r2SumAll(f *finance.FinanceAPI) int64 {
	var sum int64
	for _, id := range r2Accounts {
		sum += ledgerBalance(f, id)
	}
	return sum
}

// r2WageLegs reads THIS tick's wage postings off the ledger.
func r2WageLegs(f *finance.FinanceAPI) (credited, byFirms, byTreasury int64, treasuryLegs int) {
	for _, e := range f.LinesByCategory(finance.CatWages) {
		switch {
		case e.Account == finance.AcctHouseholds && e.Side == finance.SideCredit:
			credited += int64(e.Amount)
		case e.Account == finance.AcctFirms && e.Side == finance.SideDebit:
			byFirms += int64(e.Amount)
		case e.Account == finance.AcctTreasury && e.Side == finance.SideDebit:
			byTreasury += int64(e.Amount)
			treasuryLegs++
		}
	}
	return
}

// r2ExhaustFirms drains AcctFirms down to within `residue` of its credit
// line, the state a large employed population reaches organically (the
// line is fixed, the payroll is not).
func r2ExhaustFirms(t *testing.T, f *finance.FinanceAPI, residue int64) {
	t.Helper()
	drain := ledgerBalance(f, finance.AcctFirms) + firmsWageCreditLineMicropounds - residue
	if drain <= 0 {
		t.Fatalf("nothing to drain: firms balance %d", ledgerBalance(f, finance.AcctFirms))
	}
	if _, err := f.Post(finance.Transaction{
		Description: "attack r2: exhaust the firms working-capital line",
		Entries: []finance.Entry{
			{Account: finance.AcctFirms, Side: finance.SideDebit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(drain), Category: finance.Category("attack.drain")},
		},
	}); err != nil {
		t.Fatalf("drain post: %v", err)
	}
}

// r2RefillFirms hands AcctFirms enough cash to pay any plausible payroll
// again, so a recovery month can be observed.
func r2RefillFirms(t *testing.T, f *finance.FinanceAPI, amount int64) {
	t.Helper()
	if _, err := f.Post(finance.Transaction{
		Description: "attack r2: refill the firms working-capital line",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(amount), Category: finance.Category("attack.refill")},
			{Account: finance.AcctFirms, Side: finance.SideCredit, Amount: finance.Money(amount), Category: finance.Category("attack.refill")},
		},
	}); err != nil {
		t.Fatalf("refill post: %v", err)
	}
}

func r2Month(t *testing.T, e *core.Engine) {
	t.Helper()
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
}

// r2ForceSector forces every currently-Employed resident into the given
// sector and returns how many were moved.
func r2ForceSector(t *testing.T, st *simState, sector citizens.Sector) int {
	t.Helper()
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
			Sector:        sector,
		}); err != nil {
			t.Fatalf("force sector on %d: %v", id, err)
		}
		forced++
	}
	return forced
}

// ---------------------------------------------------------------------
// ANGLE A — the top-up backstop must be a TRANSFER, never a mint.
// ---------------------------------------------------------------------

// TestBUG548R2_BackstopIsTransferNotMint drives 24 months with the firms
// working-capital line exhausted from month 4, so the unconditional
// monthlyWagesFloor treasury backstop (fix #1) fires on ~21 of them, and
// asserts on EVERY month that (a) every ledger account sums to zero,
// (b) the ledger reports no conservation violation, and (c) the reported
// money stock closes at opening+trackedDelta. A backstop that minted the
// top-up rather than debiting the treasury reds (a) and (b).
func TestBUG548R2_BackstopIsTransferNotMint(t *testing.T) {
	e, comp := newTestEngine(t, 4242)
	f := comp.state.finance
	backstopMonths := 0
	for month := 1; month <= 24; month++ {
		if month == 4 {
			r2ExhaustFirms(t, f, 1_000)
		}
		r2Month(t, e)
		if sum := r2SumAll(f); sum != 0 {
			t.Fatalf("month %d: signed sum of every ledger account = %d, want 0 — the floor top-up minted money", month, sum)
		}
		if v := f.FindConservationViolations(); len(v) != 0 {
			t.Fatalf("month %d: FindConservationViolations = %+v", month, v)
		}
		s := f.MoneyStock()
		if int64(s.Closing) != int64(s.Opening)+int64(s.TrackedDelta) {
			t.Fatalf("month %d: money stock closing %d != opening %d + trackedDelta %d", month, int64(s.Closing), int64(s.Opening), int64(s.TrackedDelta))
		}
		credited, byFirms, byTreasury, _ := r2WageLegs(f)
		if credited != byFirms+byTreasury {
			t.Fatalf("month %d: households credited %d but payers debited %d (firms %d + treasury %d) — a one-sided wage credit", month, credited, byFirms+byTreasury, byFirms, byTreasury)
		}
		if month > 4 && byFirms == 0 && byTreasury >= monthlyWagesFloor {
			backstopMonths++
		}
	}
	if backstopMonths == 0 {
		t.Fatal("the treasury floor backstop never fired across 20 exhausted months — the fixture is vacuous, so conservation across the backstop was never actually tested")
	}
	t.Logf("backstop fired on %d/20 post-exhaustion months with conservation intact every month", backstopMonths)
}

// ---------------------------------------------------------------------
// ANGLE B — no month may be paid twice.
// ---------------------------------------------------------------------

// TestBUG548R2_NeverPaysTheSameMonthTwice asserts the posted wage bill
// never exceeds max(headcount x gross wage, monthlyWagesFloor) — the
// exact double-payment shape the new backstop could introduce (firms
// post the bill, then the floor tops it up again on top). It runs both a
// healthy stretch and an exhausted stretch, and also asserts firms and a
// FULL floor backstop never both land in the same month.
func TestBUG548R2_NeverPaysTheSameMonthTwice(t *testing.T) {
	e, comp := newTestEngine(t, 777)
	st := comp.state
	f := st.finance
	sawFirmsPaid, sawBackstop := false, false
	for month := 1; month <= 18; month++ {
		if month == 10 {
			r2ExhaustFirms(t, f, 1_000)
		}
		r2Month(t, e)
		credited, byFirms, byTreasury, _ := r2WageLegs(f)
		employed := int64(st.employedResidentCount())
		ceiling := employed * monthlyWageGrossPerEmployedMicropounds
		if ceiling < monthlyWagesFloor {
			ceiling = monthlyWagesFloor
		}
		if credited > ceiling {
			t.Fatalf("month %d: posted wage bill %d EXCEEDS max(employed %d x gross %d, floor %d) = %d — a month was paid more than once (firms %d + treasury %d)",
				month, credited, employed, int64(monthlyWageGrossPerEmployedMicropounds), int64(monthlyWagesFloor), ceiling, byFirms, byTreasury)
		}
		if byFirms > 0 {
			sawFirmsPaid = true
			// A full floor backstop stacked on a successful firms post
			// is the double-payment signature: the treasury share must
			// stay the public-headcount share, never a whole floor.
			if byTreasury >= monthlyWagesFloor {
				t.Fatalf("month %d: firms paid %d AND the treasury paid %d (>= the whole floor %d) — the floor backstop stacked on top of a successful firms post", month, byFirms, byTreasury, int64(monthlyWagesFloor))
			}
		}
		if byFirms == 0 && byTreasury >= monthlyWagesFloor {
			sawBackstop = true
		}
	}
	if !sawFirmsPaid || !sawBackstop {
		t.Fatalf("vacuous: sawFirmsPaid=%v sawBackstop=%v — the run never exercised both the healthy and the backstopped path", sawFirmsPaid, sawBackstop)
	}
}

// ---------------------------------------------------------------------
// ANGLE C — GR#17 status surface hygiene.
// ---------------------------------------------------------------------

// TestBUG548R2_ShortfallSurfaceClearsExactlyOnTheNextCleanMonth walks the
// PayrollShortfall status surface through clean -> starved -> recovered
// and asserts it reads zero while healthy, non-zero exactly on the
// starved month, and zero again on the first recovered month — never
// earlier, never later.
func TestBUG548R2_ShortfallSurfaceClearsExactlyOnTheNextCleanMonth(t *testing.T) {
	e, comp := newTestEngine(t, 31337)
	f := comp.state.finance

	// Clean months: the surface must stay quiet.
	for month := 1; month <= 3; month++ {
		r2Month(t, e)
		if _, amount := f.PayrollShortfall(); amount != 0 {
			t.Fatalf("clean month %d: PayrollShortfall reported %d, want 0 — the surface fires without a shortfall", month, int64(amount))
		}
	}

	// Starved month: the surface must fire, for THIS month.
	r2ExhaustFirms(t, f, 1_000)
	r2Month(t, e)
	shortMonth, amount := f.PayrollShortfall()
	if amount <= 0 {
		t.Fatalf("starved month: PayrollShortfall reported %d, want a positive shortfall — the GR#17 surface is silent on a real payroll failure", int64(amount))
	}
	if _, byFirms, _, _ := r2WageLegs(f); byFirms != 0 {
		t.Fatalf("starved month: firms still paid %d — the drain did not reproduce the exhaustion", byFirms)
	}
	t.Logf("starved month %d: shortfall %d", shortMonth, int64(amount))

	// It must STAY set while the failure persists.
	r2Month(t, e)
	if _, amount := f.PayrollShortfall(); amount <= 0 {
		t.Fatalf("second starved month: PayrollShortfall cleared to %d while the failure persists — a monitor would report recovery that never happened", int64(amount))
	}

	// Recovery: hand firms plenty of cash; the very next month must clear.
	r2RefillFirms(t, f, 100*firmsWageCreditLineMicropounds)
	r2Month(t, e)
	_, byFirms, _, _ := r2WageLegs(f)
	if byFirms <= 0 {
		t.Fatalf("recovered month: firms paid %d despite a refilled balance — fixture did not reproduce recovery", byFirms)
	}
	if recMonth, amount := f.PayrollShortfall(); amount != 0 {
		t.Fatalf("recovered month: firms paid the full private bill (%d) but PayrollShortfall still reports %d for month %d — the surface never clears, so a monitor shows a stale failure forever (GR#17)", byFirms, int64(amount), recMonth)
	}
}

// TestBUG548R2_ShortfallSurfaceClearsOnAnAllPublicCleanMonth is the
// clear-path hole this attacker read out of financeHook.ApplyEffect: the
// only clear site is guarded by `privateWageBill > 0`, so a month with NO
// private wage bill at all (every employed resident in SectorPublic, the
// public bill alone already above monthlyWagesFloor) posts its entire
// payroll successfully — a genuinely clean month, nothing rejected — yet
// leaves the previous month's shortfall standing on the surface.
func TestBUG548R2_ShortfallSurfaceClearsOnAnAllPublicCleanMonth(t *testing.T) {
	e, comp := newTestEngine(t, 8181)
	st := comp.state
	f := st.finance

	// Grow the city until the employed headcount alone can carry a bill
	// above monthlyWagesFloor (floor / gross ~= 72 employed), so an
	// all-public month leaves privateWageBill at exactly zero.
	needed := int(monthlyWagesFloor/monthlyWageGrossPerEmployedMicropounds) + 1
	grown := 0
	for month := 1; month <= 60; month++ {
		r2Month(t, e)
		grown = st.employedResidentCount()
		if grown >= needed {
			break
		}
	}
	if grown < needed {
		t.Skipf("city only reached %d employed in 60 months, need >= %d for a zero-private-bill month — cannot reach this shape at baseline-one scale", grown, needed)
	}

	// Starve one month so the surface is set.
	r2ExhaustFirms(t, f, 1_000)
	r2Month(t, e)
	if _, amount := f.PayrollShortfall(); amount <= 0 {
		t.Fatalf("starved month did not set the shortfall surface (%d)", int64(amount))
	}

	// MOD-034 finding: refill the firms working-capital line BEFORE the
	// clean-month attempts below. r2ExhaustFirms above deliberately
	// starves AcctFirms down to a residue of 1_000 so the earlier "set
	// the surface" step above can force a rejection — but sector churn
	// (markEmploymentAndCount re-deciding a resident, and new
	// private-sector migrants; see this function's own comment two blocks
	// down) means a "forced all-public" month can still leak a SMALL
	// residual private bill this same tick. With AcctFirms still drained,
	// that residual bill REJECTS (insufficient funds) rather than posting
	// — which still satisfies this test's byFirms==0 && credited>floor
	// "clean" heuristic (the residual bill's rejection gets backstopped
	// straight to the treasury, so byFirms stays 0), even though a real
	// private posting was rejected this month. That is NOT the scenario
	// this test means to exercise (a genuinely clean, nothing-rejected
	// month) — it is exactly the "rejected posting still keeps/sets it"
	// case financeHook.ApplyEffect's clear guard correctly refuses to
	// clear. Refilling here means any such residual bill actually POSTS
	// (byFirms becomes >0 for it, never silently rejected), so byFirms==0
	// afterwards is unambiguous proof of zero private wage bill, not
	// "private bill existed but reads as clean because it was rejected."
	r2RefillFirms(t, f, 10_000_000_000)

	// Now drive an unambiguously CLEAN all-public month. The month must
	// satisfy BOTH: firms paid nothing (so no private bill existed) AND
	// the posted bill is STRICTLY ABOVE monthlyWagesFloor (so no floor
	// top-up was needed either — a bill sitting exactly ON the floor is
	// ambiguous: it could be a starved month backstopped up to the
	// floor). Sector churn (markEmploymentAndCount re-deciding a
	// resident, and new private-sector migrants) erodes an all-public
	// city, so re-force every month until such a month lands.
	var credited, byFirms, byTreasury int64
	clean := false
	for attempt := 1; attempt <= 8 && !clean; attempt++ {
		if forced := r2ForceSector(t, st, citizens.SectorPublic); forced < needed {
			t.Logf("attempt %d: only %d employed residents left to force public (need >= %d) — employment churn has shrunk the cohort below the reachability threshold", attempt, forced, needed)
			break
		}
		r2Month(t, e)
		credited, byFirms, byTreasury, _ = r2WageLegs(f)
		t.Logf("all-public attempt %d: credited=%d firms=%d treasury=%d (employed=%d, floor=%d)", attempt, credited, byFirms, byTreasury, st.employedResidentCount(), int64(monthlyWagesFloor))
		clean = byFirms == 0 && credited > monthlyWagesFloor
	}
	if !clean {
		// MEASURED RESULT, not a skip: the clear guard's hole is LATENT
		// at baseline-one scale. markEmploymentAndCount re-decides
		// employment every month and stamps employedSectorPlaceholder
		// (SectorTertiary) on every freshly-decided resident, and new
		// migrants arrive private, so an employedPrivate==0 month with a
		// public bill already above monthlyWagesFloor could not be
		// constructed even by force-flipping the entire employed cohort
		// public before every tick. The `privateWageBill > 0` guard on
		// the only clear site is therefore a real but currently
		// unreachable defect — it becomes reachable the moment a
		// dedicated public-sector assignment path (engine.staffing, which
		// moneycirc.go's own doc comment already anticipates) is wired
		// into compose. Recorded here so the follow-up has a repro.
		t.Logf("LATENT (not reachable at this scale): could not land an unambiguously clean zero-private-bill month in 8 attempts (last: firms paid %d, credited %d, floor %d). The clear site's `privateWageBill > 0` guard means such a month could never clear PayrollShortfall — file as a follow-up against engine.staffing wiring rather than a blocking finding.", byFirms, credited, int64(monthlyWagesFloor))
		return
	}
	if sfMonth, amount := f.PayrollShortfall(); amount != 0 {
		t.Fatalf("an entirely clean month (no private wage bill owed, the whole %d payroll paid by the treasury from a strictly-above-floor public headcount, nothing rejected) left PayrollShortfall reporting %d for month %d — the clear site in financeHook.ApplyEffect is guarded by `privateWageBill > 0`, so a clean month with no private bill can never clear it and the GR#17 monitor shows a permanent phantom failure", credited, int64(amount), sfMonth)
	}
}

// TestBUG548R2_ShortfallSurfaceSurvivesSaveRestore probes the
// participant_test.go allowlist reasoning: the surface is excluded from
// the save as "transient this-month observability". This test measures
// what a save/restore ACTUALLY does to a live shortfall, so the
// transient claim is a number rather than an opinion.
func TestBUG548R2_ShortfallSurfaceSurvivesSaveRestore(t *testing.T) {
	eA, compA := newTestEngine(t, 5150)
	fA := compA.state.finance
	r2Month(t, eA)
	r2ExhaustFirms(t, fA, 1_000)
	r2Month(t, eA)
	_, amountA := fA.PayrollShortfall()
	if amountA <= 0 {
		t.Fatalf("fixture: no shortfall to save (%d)", int64(amountA))
	}

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eB, compB := newTestEngine(t, 5150)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	monthB, amountB := compB.state.finance.PayrollShortfall()
	t.Logf("shortfall before save: %d; after restore: month=%d amount=%d", int64(amountA), monthB, int64(amountB))
	if amountB != amountA {
		t.Logf("KNOWN-GAP (documented in participant_test.go's excluded allowlist): a live payroll shortfall of %d is LOST across a save/restore — a player who saves during a payroll failure reloads into a city whose GR#17 monitor reports no failure at all, until financeHook next runs and re-detects it (one month later at most)", int64(amountA))
	}
	// The self-correcting claim: the very next month must re-detect it,
	// otherwise the exclusion is a silent-loss hole, not a transient.
	r2Month(t, eB)
	if _, amount := compB.state.finance.PayrollShortfall(); amount <= 0 {
		t.Fatalf("after a save/restore the shortfall was lost AND the next month failed to re-detect it (%d) — the participant_test exclusion's 'self-correcting gap' justification does not hold", int64(amount))
	}
}

// ---------------------------------------------------------------------
// ANGLE D — per-citizen credit is exactly once, per that citizen's sector.
// ---------------------------------------------------------------------

// TestBUG548R2_SectorSwitchMidExhaustion_CreditedExactlyOncePerSector
// flips half the employed cohort to SectorPublic immediately before a
// starved month and asserts every citizen's Wealth delta over that month
// is exactly the net wage (public, treasury-paid) or exactly zero
// (private, unpaid) — never twice, never a partial.
func TestBUG548R2_SectorSwitchMidExhaustion_CreditedExactlyOncePerSector(t *testing.T) {
	e, comp := newTestEngine(t, 606)
	st := comp.state
	f := st.finance
	r2Month(t, e)
	r2Month(t, e)
	r2ExhaustFirms(t, f, 1_000)

	// Flip every other employed resident to SectorPublic.
	ids := append([]uint64(nil), st.liveResidentIDs()...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	flipped := 0
	before := map[uint64]int64{}
	for i, id := range ids {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok || cit.Employment.State != citizens.EmploymentEmployed {
			continue
		}
		before[id] = cit.Wealth
		if i%2 == 0 {
			if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
				CorrelationID: st.cid,
				Kind:          citizens.LifeEventEmployment,
				CitizenID:     id,
				Employment:    citizens.EmploymentEmployed,
				Sector:        citizens.SectorPublic,
			}); err != nil {
				t.Fatalf("flip %d public: %v", id, err)
			}
			flipped++
		}
	}
	if flipped == 0 || len(before) == 0 {
		t.Fatal("vacuous fixture: no employed residents to flip")
	}

	r2Month(t, e)

	_, byFirms, _, _ := r2WageLegs(f)
	if byFirms != 0 {
		t.Fatalf("firms paid %d during the starved month — the drain did not hold", byFirms)
	}
	paidPublic, paidNothing, anomalies := 0, 0, 0
	for id, was := range before {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok || cit.Employment.State != citizens.EmploymentEmployed {
			continue
		}
		delta := cit.Wealth - was
		switch delta {
		case 0:
			paidNothing++
			if cit.Employment.Sector == citizens.SectorPublic {
				anomalies++
				t.Errorf("citizen %d is SectorPublic (treasury-paid, a leg this attack never starves) but received nothing", id)
			}
		case monthlyWageNetPerCitizenMicropounds:
			paidPublic++
			if cit.Employment.Sector != citizens.SectorPublic {
				anomalies++
				t.Errorf("citizen %d is private-sector (sector %v) but was credited a full net wage while firms posted ZERO to the ledger — the two accountings are uncoupled again", id, cit.Employment.Sector)
			}
		default:
			anomalies++
			t.Errorf("citizen %d Wealth moved by %d over one starved month — neither zero nor exactly one net wage (%d); a double credit is %d", id, delta, int64(monthlyWageNetPerCitizenMicropounds), 2*int64(monthlyWageNetPerCitizenMicropounds))
		}
	}
	if anomalies == 0 && paidPublic == 0 {
		t.Fatalf("vacuous: not one of the %d flipped public-sector citizens was credited, so 'exactly once per sector' was never actually observed (paidNothing=%d)", flipped, paidNothing)
	}
	t.Logf("starved month with a mid-exhaustion sector switch: %d citizens credited exactly one net wage (public), %d credited nothing (private), %d anomalies", paidPublic, paidNothing, anomalies)
}

// ---------------------------------------------------------------------
// ANGLE G — mutation-coverage gap found by this round.
// ---------------------------------------------------------------------

// TestBUG548R2_CreditLineIsPayrollDerivedNotAFlatMultiple closes a
// coverage hole this round measured by mutation: reverting fix #2 (the
// data-derived firms credit line) to the pre-fix flat
// "1000 x initialTreasury" was caught by NOT ONE test in either the
// author's suite or the rest of this file — a silent revert of the fix
// would have gone unnoticed. The assertion is semantic, not a restatement
// of the constant: the line must cover at least
// firmsWageCreditLineMicropoundsRunwayMonths months of payroll at
// firmsWageCreditLinePopulationCeiling employed residents, priced with
// the SAME real gross wage the wage bill itself uses (GR#15 — derived
// from the data in play, never a hardcoded expected figure). The retired
// flat multiple covers under 8 months at that scale and reds this.
func TestBUG548R2_CreditLineIsPayrollDerivedNotAFlatMultiple(t *testing.T) {
	payrollAtCeiling := int64(firmsWageCreditLinePopulationCeiling) * monthlyWageGrossPerEmployedMicropounds
	if payrollAtCeiling <= 0 {
		t.Fatalf("payroll at the documented ceiling is %d — the sizing inputs are degenerate", payrollAtCeiling)
	}
	wantRunway := int64(firmsWageCreditLineMicropoundsRunwayMonths)
	gotRunway := int64(firmsWageCreditLineMicropounds) / payrollAtCeiling
	if gotRunway < wantRunway {
		t.Fatalf("the firms working-capital line (%d) covers only %d months of payroll at the documented %d-employed ceiling (%d/month), but its own doc comment claims %d months of runway — the line is no longer payroll-derived (a flat multiple of a starting balance was the pre-fix BUG-548 defect)",
			int64(firmsWageCreditLineMicropounds), gotRunway, int64(firmsWageCreditLinePopulationCeiling), payrollAtCeiling, wantRunway)
	}
	// And it must actually be sized off the wage figure: changing the
	// gross wage must move the line. A constant unrelated to payroll
	// (the pre-fix shape) fails this the moment the wage is retuned.
	if int64(firmsWageCreditLineMicropounds)%monthlyWageGrossPerEmployedMicropounds != 0 {
		t.Fatalf("the firms working-capital line (%d) is not a whole multiple of the monthly gross wage (%d) — it is not derived from the payroll figure it is supposed to fund",
			int64(firmsWageCreditLineMicropounds), int64(monthlyWageGrossPerEmployedMicropounds))
	}
	// Non-vacuity: the line must be materially larger than one month of
	// baseline-one's OWN payroll, or the derivation is decorative.
	if int64(firmsWageCreditLineMicropounds) <= monthlyWagesFloor {
		t.Fatalf("the firms working-capital line (%d) does not even exceed one month's monthlyWagesFloor (%d)", int64(firmsWageCreditLineMicropounds), int64(monthlyWagesFloor))
	}
}

// ---------------------------------------------------------------------
// ANGLE E — determinism through exhaustion.
// ---------------------------------------------------------------------

// TestBUG548R2_DeterministicThroughExhaustion runs the SAME seed twice
// through the same exhaustion schedule and compares a stream covering
// both accountings — every ledger line AND every citizen's wealth and
// sector — month by month. A map-range or sector-set nondeterminism in
// the new split reds this (GR#21).
func TestBUG548R2_DeterministicThroughExhaustion(t *testing.T) {
	run := func() []string {
		e, comp := newTestEngine(t, 20260905)
		st := comp.state
		f := st.finance
		var out []string
		for month := 1; month <= 10; month++ {
			if month == 3 {
				r2ExhaustFirms(t, f, 1_000)
			}
			r2Month(t, e)
			for _, id := range r2Accounts {
				out = append(out, fmt.Sprintf("m%d %s=%d", month, id, ledgerBalance(f, id)))
				for i, entry := range f.Lines(id) {
					out = append(out, fmt.Sprintf("m%d %s#%d %s %v %d", month, id, i, entry.Category, entry.Side, entry.Amount))
				}
			}
			sfMonth, sfAmount := f.PayrollShortfall()
			out = append(out, fmt.Sprintf("shortfall m%d=%d", sfMonth, int64(sfAmount)))
			ids := append([]uint64(nil), st.liveResidentIDs()...)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids {
				cit, ok := st.citizens.CitizenAt(id, st.cid)
				if !ok {
					continue
				}
				out = append(out, fmt.Sprintf("c%d w=%d s=%v e=%v", id, cit.Wealth, cit.Employment.Sector, cit.Employment.State))
			}
		}
		return out
	}
	a, b := run(), run()
	if len(a) == 0 {
		t.Fatal("empty stream — vacuous determinism check")
	}
	if len(a) != len(b) {
		t.Fatalf("stream lengths differ through exhaustion: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("diverged at line %d through exhaustion: %q vs %q", i, a[i], b[i])
		}
	}
}
