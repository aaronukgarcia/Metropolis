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
