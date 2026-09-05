package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug759_recordmonth_test.go — BUG-759 (P1, built-but-not-wired, game
// money): FinanceAPI.RecordMonthResult (insolvency.go) had ZERO
// production callers — InsolvencyMonths/IsInsolvent (AC-7's 3-consecutive-
// months game-over signal) never advanced no matter how badly a city
// missed its obligations, because nothing ever called it. compose.go's
// financeHook.ApplyEffect now calls it once per month, last, with real
// PayrollShortfall()/CremationShortfallOwed()/AvailableCredit() derived
// inputs (see that call site's own doc comment) — these tests drive the
// REAL Composition tick loop (core.Engine.AdvanceTicks), never call
// RecordMonthResult directly.
//
// Round REJECT (opus-round-bug759, F1): AvailableCredit() used to return
// the GRANTED credit-line ceiling (compose.go's Wire-time
// SetCreditLine(AcctFirms, firmsWageCreditLineMicropounds)), never
// reduced by drawdown, so it read positive in every production city
// regardless of how exhausted the line actually was.
//
// Re-round REJECT (opus-reround-bug759, F1 follow-up): the first fix
// summed headroom over EVERY RoleMoney account, including AcctHouseholds
// — citizens' own private wealth — so a city with an empty treasury and
// a fully-drawn firms line still reported AvailableCredit()=750,000,000
// off households alone. insolvency.go's AvailableCredit() now sums only
// cityObligationAccounts (AcctTreasury, AcctFirms, AcctReserves — see
// that var's own doc comment for the full rationale), excluding
// AcctHouseholds. starveBUG759 never drains AcctHouseholds (production
// never does) and never calls SetCreditLine (the original round's
// explicit rejection of that test-only shortcut) — every account this
// file touches is driven to its true floor via real Post transactions.
//
// Lead ruling on the same re-round's F3 finding: the re-round also
// surfaced that moneycirc.go's postConsumptionAndTax always lands some
// revenue into the city's own accounts before that same month's wage
// debit is attempted, so a POST-HOC, independently-sampled
// AvailableCredit() reading goes positive even in a genuinely bankrupt
// city — masking 12 real consecutive payroll failures behind a reading
// of "credit was available". The fix is NOT to change what
// AvailableCredit measures (it stays the honest city-account headroom
// for a genuinely met month) but WHEN compose.go's financeHook.ApplyEffect
// consults it: creditAvailable is now forced false whenever
// obligationsMet is already false — a rejected wage post or unpaid
// cremation debt IS the proof that neither funds nor credit sufficed at
// the moment they were needed, and no later residual reading is allowed
// to override that. See compose.go's call site and insolvency.go's
// RecordMonthResult doc comment for the full reasoning.
//
// bug759Seed is this file's own dedicated seed (distinct from every
// other test file's, per this package's own convention).
const bug759Seed = uint64(759001)

// wireBUG759 builds a fresh, deliberately empty-population composition
// (no seeded citizens, so employment/tax revenue never replenishes the
// treasury or firms across the months these tests advance through) with
// one crematorium — the same minimal-fixture shape wireBUG733 uses, so
// the day-by-day/month-by-month accounting stays exact and this file's
// assumptions can never silently drift if that file's fixture changes
// (a distinct seed and helper name, never a shared var).
func wireBUG759(t *testing.T, seed uint64) (*core.Engine, *Composition, string) {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: []string{"crem-759"}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp, cid
}

// bug759StarveCategory is this file's own dedicated test-posting category
// (mirrors bug733's reuse of the "opening.capital" category for its own
// drain/fund test helpers) — never a real production category.
const bug759StarveCategory = finance.Category("opening.capital")

// postToTarget posts a REAL, balanced transaction moving account id to
// EXACTLY target, crediting/debiting AcctExternal for the difference —
// used by starveBUG759/fundBUG759 to drive every RoleMoney account to
// its true floor or a funded level via genuine ledger postings, never a
// test-only SetCreditLine twiddle (round REJECT F1's explicit demand).
// A no-op if the account is already at target.
func postToTarget(t *testing.T, f *finance.FinanceAPI, id finance.AccountID, target finance.Money) {
	t.Helper()
	bal, ok := f.AccountBalance(id)
	if !ok {
		t.Fatalf("AccountBalance(%s): not found", id)
	}
	if bal == target {
		return
	}
	if bal > target {
		amount := bal - target
		if _, err := f.Post(finance.Transaction{
			Description: "test: drive " + string(id) + " down to target for BUG-759",
			Entries: []finance.Entry{
				{Account: id, Side: finance.SideDebit, Amount: amount, Category: bug759StarveCategory},
				{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: amount, Category: bug759StarveCategory},
			},
		}); err != nil {
			t.Fatalf("Post(%s down to %d): %v", id, target, err)
		}
		return
	}
	amount := target - bal
	if _, err := f.Post(finance.Transaction{
		Description: "test: drive " + string(id) + " up to target for BUG-759",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: bug759StarveCategory},
			{Account: id, Side: finance.SideCredit, Amount: amount, Category: bug759StarveCategory},
		},
	}); err != nil {
		t.Fatalf("Post(%s up to %d): %v", id, target, err)
	}
}

// starveBUG759 drives the CITY's own accounts (cityObligationAccounts,
// insolvency.go — AcctTreasury, AcctFirms, AcctReserves) to their true
// floor via real Post transactions — the way a production city actually
// goes broke, never SetCreditLine(id, 0) (round REJECT F1): AcctTreasury
// (no credit line — BUG-733's drainTreasuryToZero doc comment) goes to
// exactly 0; AcctFirms is driven to exactly -firmsWageCreditLineMicropounds,
// its ENTIRE granted line fully drawn down (a further 1-unit debit would
// now reject, matching the round's own live reproduction of the bug this
// fixes).
//
// Round re-REJECT (opus-reround-bug759, F1 follow-up): AcctHouseholds is
// DELIBERATELY left INTACT here (never drained) — that is the production
// shape. Citizens' private wealth (seeded 750M at Wire, replenished every
// month by wage postings) is not money the city can spend on its own
// obligations, so it must never need draining to reach a genuine city
// bankruptcy; cityObligationAccounts already excludes it from
// AvailableCredit(), and TestBUG759_HouseholdsWealthCannotMaskBankruptcy
// below is the dedicated proof that this exclusion is load-bearing.
//
// With every CITY account sitting at its floor, every month's
// PostWagesFromFirms/PostWages legs reject (RecordPayrollShortfall
// fires — see financeHook.ApplyEffect), and the lead's F3 ruling
// (compose.go's call site, this file's own top-of-file doc comment)
// means the resulting obligationsMet==false forces creditAvailable
// false too, regardless of any same-month consumption/tax trickle that
// lands in these accounts afterward — RecordMonthResult's inputs land
// at their worst case every month without any further per-month
// intervention from this file.
func starveBUG759(t *testing.T, f *finance.FinanceAPI) {
	t.Helper()
	postToTarget(t, f, finance.AcctTreasury, 0)
	postToTarget(t, f, finance.AcctFirms, -finance.Money(firmsWageCreditLineMicropounds))
}

// fundBUG759 is starveBUG759's reverse: real Post transactions crediting
// AcctTreasury and AcctFirms with a large balance each (comfortably above
// any single month's monthlyWagesFloor-sized wage bill in this file's
// zero-population fixture) — a "recovered" month, through the same real-
// posting mechanism, never SetCreditLine.
func fundBUG759(t *testing.T, f *finance.FinanceAPI) {
	t.Helper()
	const funded = finance.Money(1_000_000_000)
	postToTarget(t, f, finance.AcctTreasury, funded)
	postToTarget(t, f, finance.AcctFirms, funded)
}

// TestBUG759_TwelveStarvedMonthsReachGameOverThroughRealTickLoop is the
// primary rule proof (AC-7, via the real Composition pipeline rather
// than calling RecordMonthResult directly): a city starved of both funds
// and credit reaches a downgraded insolvency state — InsolvencyMonths()
// >= 3 and IsInsolvent()==true — purely by advancing core.Engine ticks,
// for twelve consecutive months (mirroring the round's own live
// reproduction horizon). Before RecordMonthResult's wiring, F1's account-
// set fix, and the lead's F3 ruling (compose.go's call site forcing
// creditAvailable false whenever obligationsMet is already false), this
// pair stayed at their zero/false values forever regardless of how many
// starved months elapsed — proven at each stage of this round's own
// investigation.
func TestBUG759_TwelveStarvedMonthsReachGameOverThroughRealTickLoop(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug759Seed)
	f := comp.state.finance
	starveBUG759(t, f)

	if got := f.AvailableCredit(); got != 0 {
		t.Fatalf("BUG-759 fixture error: AvailableCredit() = %d after starving every city account to its floor, want exactly 0", got)
	}
	if f.IsInsolvent() {
		t.Fatal("fixture error: must not already be insolvent before any month elapses")
	}

	advanceInChunks(t, e, 12*core.DailyTicksPerMonth)

	if got := f.InsolvencyMonths(); got < 3 {
		t.Fatalf("BUG-759: InsolvencyMonths() = %d, want >= 3 after twelve consecutive starved months (RecordMonthResult never called, or F3's post-hoc credit reading masking a real failure, if this is < 3)", got)
	}
	if !f.IsInsolvent() {
		t.Fatal("BUG-759: IsInsolvent() = false after twelve consecutive starved months, want true (AC-7 game-over)")
	}
}

// TestBUG759_FreshCityNeverInsolventOverTwelveMonths is the necessary
// counterpart to the starved test above: an UNTOUCHED, freshly-Wired
// city (real opening treasury/household balances, real credit line,
// nothing drained) must run twelve real months without ever reporting
// insolvency — proving the wiring is not a blanket "always insolvent"
// regression and that a genuinely solvent city's own consumption/tax/
// wage cycle keeps meeting its obligations exactly as it did before this
// ticket's fix.
func TestBUG759_FreshCityNeverInsolventOverTwelveMonths(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug759Seed+10)
	f := comp.state.finance

	advanceInChunks(t, e, 12*core.DailyTicksPerMonth)

	if got := f.InsolvencyMonths(); got != 0 {
		t.Fatalf("BUG-759: InsolvencyMonths() = %d after twelve months on a fresh, untouched city, want 0", got)
	}
	if f.IsInsolvent() {
		t.Fatal("BUG-759: IsInsolvent() = true on a fresh, untouched city after twelve months — must never fire without a real, sustained failure")
	}
}

// TestBUG759_UnlimitedModeNeverInsolvent proves FEAT-143 AC-2's
// Unlimited-Money inertness holds through this ticket's new call site:
// a city starved exactly like TestBUG759_TwelveStarvedMonthsReachGameOverThroughRealTickLoop,
// but Wired with Deps.GameMode="unlimited", must never advance
// InsolvencyMonths or fire IsInsolvent — RecordMonthResult's own
// unlimitedLocked() gate (insolvency.go) forces obligationsMet=true
// regardless of the real, ungated inputs this call site passes.
func TestBUG759_UnlimitedModeNeverInsolvent(t *testing.T) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(bug759Seed+11, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(bug759Seed+11), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: []string{"crem-759c"}, GameMode: "unlimited"})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	f := comp.state.finance
	starveBUG759(t, f)

	advanceInChunks(t, e, 12*core.DailyTicksPerMonth)

	if got := f.InsolvencyMonths(); got != 0 {
		t.Fatalf("BUG-759: InsolvencyMonths() = %d after twelve starved months in Unlimited mode, want 0 (AC-2 inertness)", got)
	}
	if f.IsInsolvent() {
		t.Fatal("BUG-759: IsInsolvent() = true in Unlimited mode — AC-2 requires the insolvency trigger to be completely inert")
	}
}

// TestBUG759_RecoveryAfterFundedMonthResetsCounterBeforeGameOver proves
// the OTHER half of AC-7's literal reading through the same real tick
// loop: two starved months (counter reaches 2, no game over yet) followed
// by one funded month (real Post transactions, never SetCreditLine)
// resets the counter to zero rather than continuing to accumulate
// toward game over.
func TestBUG759_RecoveryAfterFundedMonthResetsCounterBeforeGameOver(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug759Seed+1)
	f := comp.state.finance
	starveBUG759(t, f)

	advanceInChunks(t, e, 2*core.DailyTicksPerMonth)
	if got := f.InsolvencyMonths(); got != 2 {
		t.Fatalf("BUG-759 fixture error: InsolvencyMonths() = %d after two starved months, want 2", got)
	}
	if f.IsInsolvent() {
		t.Fatal("must not be insolvent after only two consecutive starved months")
	}

	fundBUG759(t, f)
	advanceInChunks(t, e, core.DailyTicksPerMonth)

	if got := f.InsolvencyMonths(); got != 0 {
		t.Fatalf("BUG-759: InsolvencyMonths() = %d after a funded month, want 0 (a met-obligations-and-credit-available month resets the counter, never merely decrements it)", got)
	}
	if f.IsInsolvent() {
		t.Fatal("BUG-759: IsInsolvent() = true after recovery reached before the third consecutive failure — must never have game-overed")
	}
}

// TestBUG759_DeterministicAcrossTwoIdenticallySeededRuns (GR#21) mirrors
// bug733_cremation_shortfall_test.go's own determinism proof shape: two
// identically-seeded runs of the exact starve->twelve-months arc must
// produce byte-identical InsolvencyMonths/IsInsolvent/AvailableCredit
// results.
func TestBUG759_DeterministicAcrossTwoIdenticallySeededRuns(t *testing.T) {
	run := func() (insolvencyMonths int, isInsolvent bool, availableCredit finance.Money) {
		e, comp, _ := wireBUG759(t, bug759Seed+20)
		f := comp.state.finance
		starveBUG759(t, f)
		advanceInChunks(t, e, 12*core.DailyTicksPerMonth)
		return f.InsolvencyMonths(), f.IsInsolvent(), f.AvailableCredit()
	}

	m1, i1, a1 := run()
	m2, i2, a2 := run()
	if m1 != m2 || i1 != i2 || a1 != a2 {
		t.Fatalf("BUG-759: non-deterministic across identically-seeded runs: run1=(months=%d insolvent=%v avail=%d) run2=(months=%d insolvent=%v avail=%d)", m1, i1, a1, m2, i2, a2)
	}
}

// TestBUG759_CreditRatingDegradesWithUnrepaidCremationDebt proves the
// other half of this ticket's brief: BUG-733's CremationShortfallOwed
// (real, un-deferred city debt per that ticket's own ruling) now feeds
// CreditRatingNow's underlying debt figure (credit.go's creditScoreLocked)
// — a broke city that lets cremation debt run up rates strictly worse
// than an otherwise-identical city with none, through the real
// deathservices->finance path, never a direct creditScoreLocked/CreditRating
// unit call.
func TestBUG759_CreditRatingDegradesWithUnrepaidCremationDebt(t *testing.T) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(bug759Seed+2, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(bug759Seed+2), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: []string{"crem-759b"}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	f := comp.state.finance
	ds := comp.DeathServices()

	baselineScore := f.CreditRatingNow()

	starveBUG759(t, f)
	const nBodies = 20
	deaths := syntheticDeaths(nBodies, 1, false)
	if _, err := ds.Intake(deaths, cid); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	advanceInChunks(t, e, 1) // crem-759b's daily throughput covers all nBodies in one day

	if owed := f.CremationShortfallOwed(); owed <= 0 {
		t.Fatalf("BUG-759 fixture error: CremationShortfallOwed() = %d, want > 0 (a broke city cremating %d unfunded bodies)", owed, nBodies)
	}

	degradedScore := f.CreditRatingNow()
	if degradedScore >= baselineScore {
		t.Fatalf("BUG-759: CreditRatingNow() = %d after accruing unfunded cremation debt, want strictly below the pre-debt baseline %d (CremationShortfallOwed must feed the credit-rating debt figure)", degradedScore, baselineScore)
	}
}

// TestBUG759_AvailableCreditReflectsRealDrawdownNotGrantedLine is the
// round's own F1 live reproduction, pinned as a permanent regression
// test: driving AcctFirms to exactly -firmsWageCreditLineMicropounds via
// a real Post (never SetCreditLine) must read AvailableCredit()==0, and
// a genuinely untouched line must read the full granted amount — proving
// the fixed accessor tracks drawdown, not just the ceiling. AcctHouseholds
// is left at its default seeded balance throughout (never drained) —
// the F1 follow-up round's own point is that it must not need to be.
func TestBUG759_AvailableCreditReflectsRealDrawdownNotGrantedLine(t *testing.T) {
	_, comp, _ := wireBUG759(t, bug759Seed+3)
	f := comp.state.finance

	postToTarget(t, f, finance.AcctTreasury, 0)

	if got := f.AvailableCredit(); got != finance.Money(firmsWageCreditLineMicropounds) {
		t.Fatalf("BUG-759 fixture error: AvailableCredit() = %d with AcctFirms untouched (AcctHouseholds still holds its full seeded balance), want exactly the granted line %d", got, firmsWageCreditLineMicropounds)
	}

	postToTarget(t, f, finance.AcctFirms, -finance.Money(firmsWageCreditLineMicropounds))

	if got := f.AvailableCredit(); got != 0 {
		t.Fatalf("BUG-759: AvailableCredit() = %d after driving AcctFirms to exactly -firmsWageCreditLineMicropounds via a real Post, want exactly 0 (the round's F1: a granted-but-fully-drawn line must never still read as available, and AcctHouseholds' intact balance must never paper over it)", got)
	}
}

// TestBUG759_HouseholdsWealthCannotMaskBankruptcy is the round's F1
// follow-up (opus-reround-bug759) live reproduction, pinned as a
// permanent regression test and the RED-PROOF the round asked for: with
// AcctHouseholds left FULLY INTACT (its default seeded ~750,000,000
// balance — never drained, matching starveBUG759's production shape) and
// only the CITY's own accounts starved, both AvailableCredit()==0 AND
// (through the real tick loop) IsInsolvent()==true must hold. If
// AcctHouseholds were ever added back into cityObligationAccounts
// (insolvency.go), both assertions would RED exactly the way the round's
// own live reproduction did (750,000,000 read as "credit available"
// while the city itself sat broke for twelve consecutive months).
func TestBUG759_HouseholdsWealthCannotMaskBankruptcy(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug759Seed+4)
	f := comp.state.finance
	starveBUG759(t, f)

	householdsBal, ok := f.AccountBalance(finance.AcctHouseholds)
	if !ok {
		t.Fatalf("AccountBalance(AcctHouseholds): not found")
	}
	if householdsBal <= 0 {
		t.Fatalf("BUG-759 fixture error: AcctHouseholds balance = %d, want > 0 (this test's whole point is a FAT household balance that must not mask bankruptcy)", householdsBal)
	}
	if got := f.AvailableCredit(); got != 0 {
		t.Fatalf("BUG-759: AvailableCredit() = %d with the city's own accounts starved but AcctHouseholds holding %d, want exactly 0 (AcctHouseholds must be excluded from the sum)", got, householdsBal)
	}

	advanceInChunks(t, e, 12*core.DailyTicksPerMonth)

	householdsBal, _ = f.AccountBalance(finance.AcctHouseholds)
	if !f.IsInsolvent() {
		t.Fatalf("BUG-759: IsInsolvent() = false after twelve consecutive starved city-account months, despite AcctHouseholds holding %d — a fat citizens' savings balance must never mask real city bankruptcy (cityObligationAccounts must exclude AcctHouseholds)", householdsBal)
	}
}

// TestBUG759_PayrollObligationMetGatesOnMonth is the round's P3
// follow-up: a table-driven, deterministic proof of the EXACT production
// function financeHook.ApplyEffect calls (payrollObligationMet,
// compose.go) — not a re-derived duplicate. The "stale shortfall from an
// earlier month" case is the RED-PROOF: with the month gate
// (`payrollMonth != currentMonth`) removed from payrollObligationMet's
// body, leaving only `payrollShortfall <= 0`, that case would read false
// (obligations wrongly NOT met) instead of the correct true.
func TestBUG759_PayrollObligationMetGatesOnMonth(t *testing.T) {
	cases := []struct {
		name         string
		payrollMonth int64
		shortfall    finance.Money
		currentMonth int64
		want         bool
	}{
		{name: "no shortfall ever recorded", payrollMonth: 0, shortfall: 0, currentMonth: 5, want: true},
		{name: "zero shortfall recorded this month (a clean, explicitly-cleared month)", payrollMonth: 5, shortfall: 0, currentMonth: 5, want: true},
		{name: "real shortfall recorded THIS month", payrollMonth: 5, shortfall: 1_000, currentMonth: 5, want: false},
		{name: "STALE shortfall from an EARLIER month, unrelated to the current month", payrollMonth: 3, shortfall: 1_000, currentMonth: 5, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := payrollObligationMet(c.payrollMonth, c.shortfall, c.currentMonth); got != c.want {
				t.Fatalf("payrollObligationMet(month=%d, shortfall=%d, currentMonth=%d) = %v, want %v", c.payrollMonth, c.shortfall, c.currentMonth, got, c.want)
			}
		})
	}
}

// TestBUG759_CremationDebtAloneCountsAsUnmet is opus-reround3-bug759's
// requested pin for the cremation half of obligationsMet
// (compose.go's `&& st.finance.CremationShortfallOwed() <= 0` term),
// which nothing else in this file's suite actually exercises — every
// other test's cremation debt (TestBUG759_CreditRatingDegradesWithUnrepaidCremationDebt)
// or absence of one gets repaid/never-accrued through the real
// crematorium path (runDeathServices' "repay any PRE-EXISTING shortfall
// FIRST" leg, compose.go), so removing the cremation term from
// obligationsMet left the whole suite green.
//
// This fixture Wires WITHOUT any DeathServiceCrematoria (Deps left at
// its zero value for that field) — st.deathServiceCrematoriumIDs stays
// empty, so runDeathServices' cremation loop (`for _, crematoriumID :=
// range crematoriumIDs`) never executes a single iteration and the
// PRE-EXISTING-shortfall repay leg inside it is structurally
// unreachable. The shortfall is instead forced directly on the finance
// API (FinanceAPI.RecordCremationShortfall — a real, exported accessor,
// never a private field poke), exactly the debt BUG-733's own
// runDeathServices would have posted had a crematorium been reachable to
// walk it back down; with none wired, nothing ever will. AcctTreasury
// and AcctFirms are FUNDED (fundBUG759) so payroll succeeds cleanly every
// month — the only thing making this city fail its obligations is the
// unrepayable cremation debt.
//
// RED-PROOF (confirmed by hand this round, per the coordinator's
// instruction — not left in the tree): removing
// `&& st.finance.CremationShortfallOwed() <= 0` from compose.go's
// obligationsMet expression makes this test FAIL (InsolvencyMonths stays
// 0 through all three months, since payroll alone is clean and credit is
// available) while the rest of this file's suite stays green — proving
// this test, uniquely, pins that term.
func TestBUG759_CremationDebtAloneCountsAsUnmet(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug759Seed+12)
	f := comp.state.finance
	fundBUG759(t, f)

	const forcedShortfall = finance.Money(50_000_000)
	f.RecordCremationShortfall(0, forcedShortfall)
	if owed := f.CremationShortfallOwed(); owed != forcedShortfall {
		t.Fatalf("BUG-759 fixture error: CremationShortfallOwed() = %d after RecordCremationShortfall, want exactly %d", owed, forcedShortfall)
	}

	advanceInChunks(t, e, 3*core.DailyTicksPerMonth)

	if owed := f.CremationShortfallOwed(); owed != forcedShortfall {
		t.Fatalf("BUG-759 fixture error: CremationShortfallOwed() = %d after three months with no crematorium wired, want it UNCHANGED at %d (the repay path must be structurally unreachable in this fixture)", owed, forcedShortfall)
	}
	if _, payrollShortfall := f.PayrollShortfall(); payrollShortfall != 0 {
		t.Fatalf("BUG-759 fixture error: PayrollShortfall() amount = %d after funding treasury+firms, want 0 (payroll must be clean — cremation debt alone is this test's whole point)", payrollShortfall)
	}
	if got := f.InsolvencyMonths(); got != 3 {
		t.Fatalf("BUG-759: InsolvencyMonths() = %d after three months of a clean payroll but an unrepaid, unrepayable cremation debt, want exactly 3 (an unfunded cremation debt alone must count as an unmet obligation)", got)
	}
	if !f.IsInsolvent() {
		t.Fatal("BUG-759: IsInsolvent() = false after three consecutive months of unmet cremation debt with clean payroll, want true")
	}
}
