package finance

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Investments (§7, AC-8): surplus is either parked in interest-bearing
// reserves or committed to capex investment programmes carrying a
// multi-year payback curve, exposed for engine.projections to render
// (the Slow-Fuse Principle's data source for investment decisions).

// InvestmentID identifies an investment programme.
type InvestmentID uint64

// InvestmentProgramme is a committed capex programme (AC-8). Capex is
// parked into productivity up front; the programme carries a documented
// multi-year payback curve (a per-month cumulative-return schedule), not
// a single-month lump.
//
// All magnitudes are PLACEHOLDER pending Aaron's balance pass — the
// acceptance criteria check that the payback curve spans multiple years,
// not a specific return figure.
type InvestmentProgramme struct {
	ID            InvestmentID
	Name          string
	Capex         Money
	MonthlyReturn Money // placeholder constant per-month return
	PaybackMonths int   // horizon, in months (>= 1)
	StartMonth    int64
}

// PaybackPoint is one point on an investment's payback curve.
type PaybackPoint struct {
	MonthOffset      int   // months since the programme started
	CumulativeReturn Money // MonthlyReturn × MonthOffset
}

// PaybackCurve returns the programme's multi-year payback curve: one
// point per month from 0 through PaybackMonths, each carrying the
// cumulative return to that month. Exposed for engine.projections (AC-8).
func (p *InvestmentProgramme) PaybackCurve() []PaybackPoint {
	out := make([]PaybackPoint, 0, p.PaybackMonths+1)
	for m := 0; m <= p.PaybackMonths; m++ {
		// safeMul already saturates to the correct extreme on overflow, so
		// a negative MonthlyReturn underflows toward MinInt64 (never jumps
		// to a positive value) and a positive one saturates toward
		// MaxInt64 — the curve stays monotonic either way.
		cum, _ := safeMul(int64(p.MonthlyReturn), int64(m))
		out = append(out, PaybackPoint{
			MonthOffset:      m,
			CumulativeReturn: Money(cum),
		})
	}
	return out
}

// BreakEvenMonth returns the first month offset (≥ 0) at which the
// cumulative return meets or exceeds the capex, or -1 if the programme
// never breaks even within its horizon.
func (p *InvestmentProgramme) BreakEvenMonth() int {
	if p.MonthlyReturn <= 0 {
		return -1
	}
	for _, pt := range p.PaybackCurve() {
		if pt.CumulativeReturn >= p.Capex {
			return pt.MonthOffset
		}
	}
	return -1
}

// StartInvestment commits a capex programme: it validates the request,
// posts the capex outflow from the treasury, and registers the
// programme. Rejects non-positive capex or payback horizon, or a
// negative monthly return (ErrInvalidInvestment).
func (f *FinanceAPI) StartInvestment(name string, capex, monthlyReturn Money, paybackMonths int) (InvestmentProgramme, error) {
	if err := f.checkNotCopied("StartInvestment"); err != nil {
		return InvestmentProgramme{}, err
	}
	if capex <= 0 {
		return InvestmentProgramme{}, errs.New(ErrInvalidInvestment, f.correlationID, map[string]any{
			"field": "capex", "value": int64(capex), "rule": "must be positive",
		})
	}
	if paybackMonths <= 0 {
		return InvestmentProgramme{}, errs.New(ErrInvalidInvestment, f.correlationID, map[string]any{
			"field": "paybackMonths", "value": paybackMonths, "rule": "must be positive",
		})
	}
	if monthlyReturn < 0 {
		return InvestmentProgramme{}, errs.New(ErrInvalidInvestment, f.correlationID, map[string]any{
			"field": "monthlyReturn", "value": int64(monthlyReturn), "rule": "must be non-negative",
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("StartInvestment"); err != nil {
		return InvestmentProgramme{}, err
	}

	prog := &InvestmentProgramme{
		ID:            f.nextInvestID,
		Name:          name,
		Capex:         capex,
		MonthlyReturn: monthlyReturn,
		PaybackMonths: paybackMonths,
		StartMonth:    f.month,
	}
	f.nextInvestID++
	f.investments = append(f.investments, prog)

	f.post(Transaction{
		Description: "investment programme capex",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: capex, Category: CatInvestment},
			{Account: AcctExternal, Side: SideCredit, Amount: capex, Category: CatInvestment},
		},
	}, true)

	return *prog, nil
}

// AllocateToReserves parks surplus in the interest-bearing reserve
// account (an internal transfer — money stock unchanged). Returns the
// allocated amount.
func (f *FinanceAPI) AllocateToReserves(amount Money) (Money, error) {
	if amount < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "reserveAllocation", "amount": int64(amount)})
	}
	if _, err := f.Post(Transaction{
		Description: "surplus allocated to reserves",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: amount, Category: CatReserveDeposit},
			{Account: AcctReserves, Side: SideCredit, Amount: amount, Category: CatReserveDeposit},
		},
	}); err != nil {
		return 0, err
	}
	return amount, nil
}

// AccrueReserveInterest posts a monthly reserve-account interest accrual:
// interest is created into reserves (an external inflow, tracked). The
// rate is a placeholder monthly rate in basis points (documented — the
// real figure is Aaron's balance pass). Returns the accrued amount.
func (f *FinanceAPI) AccrueReserveInterest(rateBp BasisPoints) (Money, error) {
	if err := f.checkNotCopied("AccrueReserveInterest"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("AccrueReserveInterest"); err != nil {
		return 0, err
	}

	reserves := f.accountBalanceLocked(AcctReserves)
	interest := Money(mulDiv(int64(reserves), int64(rateBp), basisPointScale))
	if interest <= 0 {
		return 0, nil
	}
	f.post(Transaction{
		Description: "reserve interest accrual",
		Entries: []Entry{
			{Account: AcctReserves, Side: SideCredit, Amount: interest, Category: CatReserveInterest},
			{Account: AcctExternal, Side: SideDebit, Amount: interest, Category: CatReserveInterest},
		},
	}, true)
	return interest, nil
}
