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

// AvailableCredit returns the total unused credit line across accounts
// (the running total, never a map-iteration sum — AC-14). A positive
// value means credit was available this month even if unused, which
// resets the insolvency counter (AC-7).
func (f *FinanceAPI) AvailableCredit() Money {
	if err := f.checkNotCopied("AvailableCredit"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.totalCreditLine
}

// RecordPayrollShortfall (BUG-548, GR#17) sets the USER-VISIBLE payroll-
// shortfall surface for the given month: the composition root calls this
// when PostWagesFromFirms rejected the private-sector wage bill and the
// monthlyWagesFloor safety net had to be topped up from the treasury
// instead. shortfall is the amount that failed to post from firms.
// Passing a zero shortfall for the current month clears the surface (the
// month posted its full private bill with no gap) — see PayrollShortfall.
func (f *FinanceAPI) RecordPayrollShortfall(month int64, shortfall Money) {
	if err := f.checkNotCopied("RecordPayrollShortfall"); err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastPayrollShortfall = shortfall
	f.lastPayrollShortfallMonth = month
}

// PayrollShortfall returns the most recently recorded private-sector
// payroll shortfall and the month it was recorded for (BUG-548, GR#17) —
// the monitorable status surface a news feed or status line polls instead
// of grepping the MET-G217 log line. A zero amount means the most recent
// month posted its full private wage bill.
func (f *FinanceAPI) PayrollShortfall() (month int64, shortfall Money) {
	if err := f.checkNotCopied("PayrollShortfall"); err != nil {
		return 0, 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.lastPayrollShortfallMonth, f.lastPayrollShortfall
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
