package finance

// Insolvency (§7 line 210, §12's "Death conditions: insolvency (…)",
// AC-7): the city is game-over after 3 consecutive months in which it
// could not meet its obligations AND no credit was available. A month
// where obligations were met, or where credit was available even if
// unused, resets the counter to zero (not decrements it) — the literal
// reading of "3 consecutive months".
const insolvencyMonthsForGameOver = 3

// MonthResult is RecordMonthResult's return: the updated consecutive-
// failed-months count and whether game over just fired (or had already).
type MonthResult struct {
	ConsecutiveFailedMonths int
	GameOver                bool
}

// RecordMonthResult records one month's solvency outcome (AC-7):
//
//   - obligationsMet: the city met every obligation due this month.
//   - creditAvailable: credit was available this month (even if unused).
//
// If either is true the consecutive-failure counter resets to 0; if both
// are false it increments, and at exactly insolvencyMonthsForGameOver the
// game-over signal fires. It returns the updated state.
//
// BUG-759 lead ruling (opus-reround-bug759's own re-bounce, F3): a caller
// must NEVER derive creditAvailable as an independent, always-sampled
// signal once obligationsMet is already false. A rejected wage post or
// an unpaid debt IS the proof that neither funds nor credit sufficed at
// the moment it mattered — a later, POST-HOC "is there any headroom
// anywhere right now" reading can trivially go positive from revenue
// that landed AFTER the failure was already real (e.g. next month's
// opening receipts, or — the concrete production case that surfaced
// this — the SAME month's own consumption/tax legs posting before the
// wage debit that then rejects), which would silently paper over a
// genuine, already-proven miss. The correct shape at any call site is
// `creditAvailable := obligationsMet && <real headroom> > 0` — i.e.
// compute a real headroom reading ONLY to see whether an
// already-met month also had spare capacity, never as an independent
// second chance for a month that already failed. See compose.go's
// financeHook.ApplyEffect call site for the concrete derivation and its
// own account-set rationale (AvailableCredit, this package).
func (f *FinanceAPI) RecordMonthResult(obligationsMet, creditAvailable bool) MonthResult {
	if err := f.checkNotCopied("RecordMonthResult"); err != nil {
		return MonthResult{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("RecordMonthResult"); err != nil {
		return MonthResult{}
	}

	// FEAT-143 AC-2: in Unlimited Money mode the insolvency/debt-rating
	// triggers are inert -- InsolvencyMonths never advances and game-over
	// never fires. Forcing obligationsMet=true routes through the exact
	// same "the city is fine this month" branch Real mode would take on
	// an actually-solvent month (US-4: one finance code, mode as a gate,
	// never a second divergent implementation), rather than a bypass that
	// returns early and skips the counter reset semantics entirely.
	if f.unlimitedLocked() {
		obligationsMet = true
	}

	if obligationsMet || creditAvailable {
		f.insolvencyMonths = 0
	} else {
		f.insolvencyMonths++
		if f.insolvencyMonths >= insolvencyMonthsForGameOver {
			f.gameOver = true
		}
	}
	return MonthResult{ConsecutiveFailedMonths: f.insolvencyMonths, GameOver: f.gameOver}
}

// IsInsolvent reports whether the game-over signal has fired (AC-7).
func (f *FinanceAPI) IsInsolvent() bool {
	if err := f.checkNotCopied("IsInsolvent"); err != nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.gameOver
}

// InsolvencyMonths returns the current consecutive failed-months count.
func (f *FinanceAPI) InsolvencyMonths() int {
	if err := f.checkNotCopied("InsolvencyMonths"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.insolvencyMonths
}

// InsolvencyStatus (BUG-769 round finding F1/F4, opus-round-bug769)
// returns InsolvencyMonths() and IsInsolvent() under ONE RLock
// acquisition — mirrors PayrollShortfallStatus's identical torn-read
// rationale immediately above (that accessor's own doc comment): a
// caller reading months and gameOver via two SEPARATE calls could
// observe a torn snapshot if RecordMonthResult's write lock lands exactly
// between them (e.g. a concurrent publish reading months=3-pre-reset
// alongside gameOver=false-post-reset, or any other interleaving). Every
// production caller of both fields together (finance_publish.go's
// buildFinanceBalanceSheetPatch) uses this instead of the two individual
// accessors.
func (f *FinanceAPI) InsolvencyStatus() (months int, insolvent bool) {
	if err := f.checkNotCopied("InsolvencyStatus"); err != nil {
		return 0, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.insolvencyMonths, f.gameOver
}

// cityObligationAccounts is the FIXED, deterministic set of accounts
// AvailableCredit sums over (BUG-759 round re-REJECT, opus-reround-
// bug759): the accounts the CITY itself can actually draw on to settle
// its own obligations — AcctTreasury (the city's cash), AcctFirms (the
// working-capital pot PostWagesFromFirms pays private wages from, and
// the only account compose.go ever grants a credit line to), and
// AcctReserves (baseline one's unused-today reserve account, included
// for completeness since creditScoreLocked already reads it as part of
// the SAME "can the city cover its own bills" question). AcctHouseholds
// is DELIBERATELY EXCLUDED even though it is a RoleMoney account: it is
// citizens' own private wealth (seeded 750M at Wire, replenished every
// month by wage postings — finance_publish.go's own balance-sheet view
// excludes it from the city's Assets for the identical reason, see that
// file's doc comment), not money the CITY can spend to meet ITS
// obligations. Proven live by the round: with this fix absent, a city
// with an empty treasury and a fully-drawn firms line still reported
// AvailableCredit()=750,000,000 off Households alone, so InsolvencyMonths
// stayed at 0 through twelve consecutive failed payrolls — a fat
// citizens' savings balance was masking real city bankruptcy. A literal
// slice, never derived from f.role's map (GR#21/AC-14) — order here does
// not even affect the sum, but is kept ascending by AccountID for
// consistency with sortedMoneyAccounts' convention.
var cityObligationAccounts = [...]AccountID{AcctReserves, AcctTreasury, AcctFirms}

// AvailableCredit returns the city's total REAL, currently-unused
// headroom across the accounts the city itself can draw on to meet its
// OWN obligations (cityObligationAccounts, above — NOT every RoleMoney
// account, and specifically NOT AcctHouseholds): sum of (balance + that
// account's granted credit line), each floored at 0.
//
// BUG-759 round REJECT (opus-round-bug759, F1): this used to return
// f.totalCreditLine — the GRANTED overdraft ceiling set once at Wire
// time (compose.go's SetCreditLine(AcctFirms, ...)) and never reduced by
// drawdown — mislabelled "unused" in this very doc comment. Proven live:
// AcctFirms driven to exactly -totalCreditLine (a further debit already
// rejects, i.e. genuinely zero headroom left) still reported the full
// granted line as "available".
//
// BUG-759 re-REJECT (opus-reround-bug759, same round's F1 follow-up):
// the FIRST fix summed over EVERY RoleMoney account, which silently
// re-introduced the identical false-positive failure mode through
// AcctHouseholds — citizens' private wealth, which the city cannot
// spend, was still counted as if it were city headroom. See
// cityObligationAccounts' own doc comment for the fix and rationale.
//
// A positive result now means the CITY genuinely has real spare capacity
// somewhere in ITS OWN accounts right now — the literal AC-7 reading
// ("credit was available this month even if unused"), never a facility
// that merely EXISTS regardless of drawdown, and never citizens' own
// money masking the city's inability to pay its own bills.
// balance+line is never allowed to go negative per account by
// construction (Post's own overdraft check refuses a debit that would
// push balance below -creditLines[account]), so the per-account max(0,…)
// floor only guards a would-be future invariant break from ever
// wrapping this sum negative (GR#16), it is not expected to fire today.
func (f *FinanceAPI) AvailableCredit() Money {
	if err := f.checkNotCopied("AvailableCredit"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var total Money
	for _, id := range cityObligationAccounts {
		headroom, _ := satAddMoney(f.accountBalanceLocked(id), f.creditLines[id])
		if headroom < 0 {
			headroom = 0
		}
		total, _ = satAddMoney(total, headroom)
	}
	return total
}

// RecordPayrollShortfall (BUG-548, GR#17) sets the USER-VISIBLE payroll-
// shortfall surface for the given month: the composition root calls this
// when PostWagesFromFirms rejected the private-sector wage bill and the
// monthlyWagesFloor safety net had to be topped up from the treasury
// instead. shortfall is the amount that failed to post from firms.
// Passing a zero shortfall for the current month clears the surface (the
// month posted its full private bill with no gap) — see PayrollShortfall.
//
// BUG-723 round finding F7: payrollShortfallMonths counts consecutive
// MONTHS, not consecutive CALLS — a second call for the SAME month
// (financeHook is only expected to call this once per month, but nothing
// upstream enforces that, and a defensive re-post/retry path calling it
// twice for one month must not double-count the streak) only advances
// the counter the FIRST time that month is seen; a repeat call for a
// month already reflected in the streak is a no-op on the counter
// (though lastPayrollShortfall/lastPayrollShortfallMonth still take the
// latest amount, matching the pre-existing "most recent wins" contract).
func (f *FinanceAPI) RecordPayrollShortfall(month int64, shortfall Money) {
	if err := f.checkNotCopied("RecordPayrollShortfall"); err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if shortfall > 0 {
		newMonth := f.payrollShortfallMonths == 0 || month != f.lastPayrollShortfallMonth
		if newMonth {
			f.payrollShortfallMonths++
		}
	} else {
		f.payrollShortfallMonths = 0
	}
	f.lastPayrollShortfall = shortfall
	f.lastPayrollShortfallMonth = month
}

// PayrollShortfall returns the most recently recorded private-sector
// payroll shortfall and the month it was recorded for (BUG-548, GR#17) —
// the monitorable status surface a news feed or status line polls instead
// of grepping the MET-G217 log line. A zero amount means the most recent
// month posted its full private wage bill.
//
// BUG-723 round finding F5: delegates to PayrollShortfallStatus so the
// month+amount pair this returns is read under the SAME RLock acquisition
// as PayrollShortfallMonths would read the streak — see that method's
// doc comment for why a caller needing more than one of these three
// values together (finance_publish.go does) must call
// PayrollShortfallStatus directly rather than composing this with
// PayrollShortfallMonths().
func (f *FinanceAPI) PayrollShortfall() (month int64, shortfall Money) {
	if err := f.checkNotCopied("PayrollShortfall"); err != nil {
		return 0, 0
	}
	month, shortfall, _ = f.PayrollShortfallStatus()
	return month, shortfall
}

// PayrollShortfallMonths returns the current consecutive-months-in-
// shortfall streak (BUG-723, GR#17): 0 means the most recent recorded
// month cleared (or nothing has ever shortfallen); N>0 means the last N
// consecutive DISTINCT months each carried a positive shortfall (see
// RecordPayrollShortfall's F7 doc comment on why this is months, not
// calls). Reset to 0 the instant a month clears, exactly like
// PayrollShortfall's own amount.
func (f *FinanceAPI) PayrollShortfallMonths() int {
	if err := f.checkNotCopied("PayrollShortfallMonths"); err != nil {
		return 0
	}
	_, _, months := f.PayrollShortfallStatus()
	return months
}

// PayrollShortfallStatus (BUG-723 round finding F5) returns month, amount
// AND the consecutive-months streak read under ONE RLock acquisition —
// the atomic combined read finance_publish.go's publish path must use.
// Before this existed, buildFinanceBalanceSheetPatch called
// PayrollShortfall() and PayrollShortfallMonths() as two SEPARATE lock
// acquisitions; the publish pump runs concurrently with tick-phase writes
// (RecordPayrollShortfall taking the write lock in between), so a
// shortfall clearing (RecordPayrollShortfall(month, 0)) exactly between
// those two calls could publish a torn snapshot — a non-zero
// lastPayrollShortfall amount from just before the clear paired with the
// ALREADY-zeroed payrollShortfallMonths from just after it (or the
// reverse on a fresh starve), a self-contradictory reading no real
// FinanceAPI state ever holds (concurrent-attacker measurement: 459k/500k
// paired reads torn under sustained concurrent RecordPayrollShortfall +
// PayrollShortfallStatus traffic before this fix existed as a single
// accessor). PayrollShortfall()/PayrollShortfallMonths() remain as
// single-value convenience wrappers for callers (existing tests) that
// only need one field and can tolerate the tiny window between two
// separate calls.
func (f *FinanceAPI) PayrollShortfallStatus() (month int64, shortfall Money, months int) {
	if err := f.checkNotCopied("PayrollShortfallStatus"); err != nil {
		return 0, 0, 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.lastPayrollShortfallMonth, f.lastPayrollShortfall, f.payrollShortfallMonths
}

// RecordCremationShortfall (BUG-733, GR#17) ACCRUES amount onto the
// running, unpaid cremation-cost debt for month: the composition root
// calls this when SettleOpex rejected a day's cremation cost because the
// treasury (plus credit line) could not cover it. Unlike
// RecordPayrollShortfall, which OVERWRITES a transient "this month's
// shortfall" value, this ADDS — a broke city's cremation debt keeps
// growing day over day until a funded day repays it (RepayCremationShortfall),
// exactly the "unfunded cremation is not free, not deferred" ruling this
// bug's brief records. amount must be non-negative (the composition root
// only ever calls this with a real posting shortfall); a negative amount
// is ignored (GR#15: never silently substitute/clamp a caller error,
// but also never let a bad caller drive the debt negative — the "ignore"
// is because this method returns no error, mirroring RecordPayrollShortfall's
// shape, so validation happens once, at the SettleOpex call site that
// derives amount from a real ledger rejection).
func (f *FinanceAPI) RecordCremationShortfall(month int64, amount Money) {
	if err := f.checkNotCopied("RecordCremationShortfall"); err != nil {
		return
	}
	if amount < 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cremationShortfall, _ = satAddMoney(f.cremationShortfall, amount)
	f.lastCremationShortfallMonth = month
}

// CremationShortfallOwed returns the total currently-outstanding, unpaid
// cremation cost (BUG-733, GR#17) — the accruing debt surface a news
// feed/status line (or a future insolvency/debt-rating trigger, wired the
// same way PayrollShortfall would be) polls. Zero means every cremation
// ever billed has since been paid in full.
func (f *FinanceAPI) CremationShortfallOwed() Money {
	if err := f.checkNotCopied("CremationShortfallOwed"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cremationShortfall
}

// CremationShortfall returns the month a cremation shortfall was most
// recently ACCRUED and the current total outstanding debt (BUG-733,
// GR#17) — mirrors PayrollShortfall's (month, amount) reporting shape,
// except the amount here is the running balance, not a single month's
// delta (see cremationShortfall's field doc for why this one persists
// and accrues rather than resetting each month).
func (f *FinanceAPI) CremationShortfall() (month int64, owed Money) {
	if err := f.checkNotCopied("CremationShortfall"); err != nil {
		return 0, 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.lastCremationShortfallMonth, f.cremationShortfall
}

// RepayCremationShortfall (BUG-733) reduces the outstanding cremation debt
// by amount, floored at zero. It does NOT post to the ledger itself — the
// caller (compose.go's runDeathServices) posts the actual SettleOpex
// repayment transaction first and calls this only once that posting
// succeeds, mirroring PostMaintenance's backlog-adjustment-after-the-fact
// pattern (opex.go): the debt-tracking balance is kept in lock-step with
// a real, separately-posted ledger transaction, never a phantom deduction
// with no matching money movement.
func (f *FinanceAPI) RepayCremationShortfall(amount Money) {
	if err := f.checkNotCopied("RepayCremationShortfall"); err != nil {
		return
	}
	if amount <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Floored at zero (mirrors PostMaintenance's backlog-recovery clamp,
	// opex.go): a caller repaying more than is actually owed must never
	// drive this into negative territory, which would nonsensically read
	// as the treasury being OWED money rather than owing it.
	if amount > f.cremationShortfall {
		amount = f.cremationShortfall
	}
	f.cremationShortfall = satSubMoney(f.cremationShortfall, amount)
}
