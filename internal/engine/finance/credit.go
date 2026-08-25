package finance

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CreditScore is a 0–1000 integer rating: 1000 is a clean, low-debt
// borrower; 0 is the worst. All credit math is int64 fixed-point — no
// float rating, no float interest rate (AC-2).
type CreditScore int64

const (
	creditScoreMax CreditScore = 1000
	creditScoreMin CreditScore = 0
)

// baseInterestRateBp is the placeholder base interest rate a perfect
// borrower pays. PLACEHOLDER pending Aaron's balance pass — directional
// tests only (AC-5/AC-6).
const baseInterestRateBp BasisPoints = 500 // 5.00%

// CreditRating computes a credit score from the city's financial posture
// (AC-5): debt/revenue ratio, payment history, and reserve months.
//
//   - debt/revenue: a higher ratio lowers the score (up to −600 points at
//     ~3× revenue).
//   - missedPayments: each missed payment lowers the score by 150 points.
//   - reserveMonths: each month of reserves (up to 3) adds 60 points.
//
// All inputs are int64 (money as micro-pounds, reserveMonths as a
// dimensionless months count); the result is clamped to [0, 1000]. The
// specific weights are placeholder balance data — the acceptance
// criteria check direction (worse inputs ⇒ worse rating), not a pinned
// formula (GR#15).
func CreditRating(debt, revenue int64, missedPayments int, reserveMonths int64) CreditScore {
	if revenue <= 0 {
		revenue = 1
	}
	if debt < 0 {
		debt = 0
	}
	// Clamp the miss count like every other input: a negative missedPayments
	// would otherwise wrap the num.SafeMul product and IMPROVE the score (GR#16).
	if missedPayments < 0 {
		missedPayments = 0
	}

	score := int64(creditScoreMax)

	// Debt/revenue: ratio is per-mille (1000 = 100%). Computed with mulDiv
	// (full-width, saturating) so a huge debt cannot overflow the multiply
	// and wrap into a good-looking ratio — a hugely-indebted city must
	// never rate better than a low-debt one (GR#16). Deduct 200 points per
	// 100% of debt/revenue, capped at 600.
	ratio := mulDiv(debt, 1000, revenue)
	score = num.SatSub(score, minI64(mulDiv(ratio, 200, 1000), 600))

	// Payment history: 150 points per missed payment (num.SafeMul so an absurd
	// miss count saturates the deduction instead of wrapping).
	missDeduction, missOverflow := num.SafeMul(int64(missedPayments), 150)
	if missOverflow {
		missDeduction = math.MaxInt64
	}
	score = num.SatSub(score, missDeduction)

	// Reserve months: +60 per month, up to three months.
	if reserveMonths < 0 {
		reserveMonths = 0
	}
	score = num.SatAdd(score, minI64(reserveMonths, 3)*60)

	return CreditScore(clampScore(score))
}

// InterestRate maps a credit score to an annual interest rate in basis
// points (AC-5): base plus a spread that grows as the score falls. A
// lower score ⇒ a higher rate. PLACEHOLDER magnitudes — directional only.
// The score is clamped before the spread multiply so an out-of-range
// input cannot wrap the (1000 − score) × 10 term.
func InterestRate(score CreditScore) BasisPoints {
	s := clampScore(int64(score))
	spread := BasisPoints((int64(creditScoreMax) - s) * 10)
	return baseInterestRateBp + spread
}

// LoanID identifies a loan issued by a FinanceAPI.
type LoanID uint64

// Loan is one issued loan facility (AC-5). Its rate is fixed at
// origination from the credit score at the time; refinancing means
// borrowing again at the (possibly worse) current rate.
type Loan struct {
	ID            LoanID
	Principal     Money
	Outstanding   Money
	TermMonths    int
	MilestoneTier int
	RateBp        BasisPoints
}

// MonthlyPayment returns the loan's placeholder monthly debt-service
// obligation: interest (outstanding × annual rate ÷ 12) plus straight-
// line principal amortisation (outstanding ÷ term). Placeholder shape —
// directional tests only.
func (l Loan) MonthlyPayment() Money {
	if l.TermMonths <= 0 {
		return 0
	}
	interest := Money(mulDiv(int64(l.Outstanding), int64(l.RateBp), basisPointScale*12))
	principal := l.Outstanding / Money(l.TermMonths)
	total, _ := satAddMoney(interest, principal)
	return total
}

// LoanRequest is Borrow's input.
type LoanRequest struct {
	Tier       int
	Principal  Money
	TermMonths int
}

// Borrow issues a loan facility for the requested milestone tier (AC-5):
// the injected MilestoneGate must report the tier reached, and the rate
// is fixed from the current credit score. On success it posts the
// disbursement (treasury credited, debt liability debited — an external
// money inflow) and returns the loan. Rejects non-positive principal or
// term, a negative tier (ErrInvalidLoanTerms), or an unreached milestone
// (ErrLoanUnavailable).
func (f *FinanceAPI) Borrow(req LoanRequest) (Loan, error) {
	if err := f.checkNotCopied("Borrow"); err != nil {
		return Loan{}, err
	}
	if req.Principal <= 0 {
		return Loan{}, errs.New(ErrInvalidLoanTerms, f.correlationID, map[string]any{
			"field": "principal", "value": int64(req.Principal), "rule": "must be positive",
		})
	}
	if req.TermMonths <= 0 {
		return Loan{}, errs.New(ErrInvalidLoanTerms, f.correlationID, map[string]any{
			"field": "termMonths", "value": req.TermMonths, "rule": "must be positive",
		})
	}
	if req.Tier < 0 {
		return Loan{}, errs.New(ErrInvalidLoanTerms, f.correlationID, map[string]any{
			"field": "tier", "value": req.Tier, "rule": "must be non-negative",
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("Borrow"); err != nil {
		return Loan{}, err
	}
	if f.gate == nil || !f.gate.MilestoneReached(req.Tier) {
		return Loan{}, errs.New(ErrLoanUnavailable, f.correlationID, map[string]any{"tier": req.Tier})
	}

	rate := InterestRate(f.creditScoreLocked())
	loan := &Loan{
		ID:            f.nextLoanID,
		Principal:     req.Principal,
		Outstanding:   req.Principal,
		TermMonths:    req.TermMonths,
		MilestoneTier: req.Tier,
		RateBp:        rate,
	}
	f.nextLoanID++
	f.loans[loan.ID] = loan
	f.totalDebt, _ = satAddMoney(f.totalDebt, req.Principal)

	f.post(Transaction{
		Description: "loan disbursement",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideCredit, Amount: req.Principal, Category: CatLoan},
			{Account: AcctDebt, Side: SideDebit, Amount: req.Principal, Category: CatLoan},
		},
	}, true)

	return *loan, nil
}

// RepayLoan applies a principal repayment: it reduces the loan's
// outstanding balance and posts the treasury→debt principal payment. The
// caller posts the interest leg separately via ServiceDebt.
func (f *FinanceAPI) RepayLoan(id LoanID, principal Money) error {
	if err := f.checkNotCopied("RepayLoan"); err != nil {
		return err
	}
	if principal < 0 {
		return errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "principal", "amount": int64(principal)})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("RepayLoan"); err != nil {
		return err
	}
	loan, ok := f.loans[id]
	if !ok {
		return errs.New(ErrUnknownLoan, f.correlationID, map[string]any{"loan": uint64(id)})
	}
	if principal > loan.Outstanding {
		principal = loan.Outstanding
	}
	loan.Outstanding = satSubMoney(loan.Outstanding, principal)
	f.totalDebt = satSubMoney(f.totalDebt, principal)

	f.post(Transaction{
		Description: "loan principal repayment",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: principal, Category: CatDebtPrincipal},
			{Account: AcctDebt, Side: SideCredit, Amount: principal, Category: CatDebtPrincipal},
		},
	}, true)
	return nil
}

// MissPayment records a missed debt payment (AC-6): it increments the
// payment-history miss counter, which degrades the credit score and so
// raises the rate on the next Borrow — the refinancing spiral. The
// caller remains responsible for any insolvency bookkeeping via
// RecordMonthResult.
func (f *FinanceAPI) MissPayment(id LoanID) error {
	if err := f.checkNotCopied("MissPayment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("MissPayment"); err != nil {
		return err
	}
	if _, ok := f.loans[id]; !ok {
		return errs.New(ErrUnknownLoan, f.correlationID, map[string]any{"loan": uint64(id)})
	}
	f.missedPayments++
	return nil
}

// CreditRatingNow returns the FinanceAPI's current credit score, derived
// from its live state: outstanding debt, this tick's tax revenue,
// reserve months (reserves ÷ monthly revenue), and the missed-payment
// counter.
func (f *FinanceAPI) CreditRatingNow() CreditScore {
	if err := f.checkNotCopied("CreditRatingNow"); err != nil {
		return creditScoreMin
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.creditScoreLocked()
}

// CurrentInterestRate returns the annual rate the city would pay if it
// borrowed now, from its current credit score.
func (f *FinanceAPI) CurrentInterestRate() BasisPoints {
	if err := f.checkNotCopied("CurrentInterestRate"); err != nil {
		return 0
	}
	return InterestRate(f.CreditRatingNow())
}

// OutstandingDebt returns the total outstanding loan principal.
func (f *FinanceAPI) OutstandingDebt() Money {
	if err := f.checkNotCopied("OutstandingDebt"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.totalDebtLocked()
}

// Loans returns a snapshot of every open loan facility in ascending
// LoanID order (GR#21 — sorted IDs, never map-iteration order), copying
// each value so the caller owns the slice and can never alias the
// internal book (weakness pattern #1). FEAT-233's loans-summary view
// consumes this; OutstandingDebt remains the maintained running TOTAL and
// stays authoritative for aggregates (AC-14 discipline).
func (f *FinanceAPI) Loans() []Loan {
	if err := f.checkNotCopied("Loans"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	ids := make([]LoanID, 0, len(f.loans))
	for id := range f.loans {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Loan, 0, len(ids))
	for _, id := range ids {
		out = append(out, *f.loans[id])
	}
	return out
}

// totalDebtLocked returns the maintained outstanding-principal running
// total (f.mu held). It never iterates the loans map — the total is
// updated incrementally on Borrow/RepayLoan, so no monetary sum depends
// on map-iteration order (AC-14).
func (f *FinanceAPI) totalDebtLocked() Money {
	if err := f.checkNotCopied("totalDebtLocked"); err != nil {
		return 0
	}
	return f.totalDebt
}

// reduceLoanBookLocked retires principal against the outstanding loan
// book, oldest-first (sorted loan IDs for determinism, GR#21), updating
// each loan's Outstanding and the running totalDebt (f.mu held). It is
// what keeps ServiceDebt and RepayLoan consistent: both paths retire
// principal from the book AND from the ledger, so OutstandingDebt() and
// CreditRatingNow() never keep charging interest on already-repaid
// principal (the ServiceDebt/RepayLoan divergence fix).
func (f *FinanceAPI) reduceLoanBookLocked(principal Money) {
	if err := f.checkNotCopied("reduceLoanBookLocked"); err != nil {
		return
	}
	if principal <= 0 {
		return
	}
	ids := make([]LoanID, 0, len(f.loans))
	for id := range f.loans {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	remaining := principal
	for _, id := range ids {
		if remaining <= 0 {
			break
		}
		l := f.loans[id]
		if l.Outstanding <= remaining {
			f.totalDebt = satSubMoney(f.totalDebt, l.Outstanding)
			remaining = satSubMoney(remaining, l.Outstanding)
			l.Outstanding = 0
		} else {
			l.Outstanding = satSubMoney(l.Outstanding, remaining)
			f.totalDebt = satSubMoney(f.totalDebt, remaining)
			remaining = 0
		}
	}
}

// creditScoreLocked derives the credit score from live state (f.mu held).
func (f *FinanceAPI) creditScoreLocked() CreditScore {
	if err := f.checkNotCopied("creditScoreLocked"); err != nil {
		return creditScoreMin
	}
	debt := f.totalDebtLocked()
	revenue := f.taxRevenueLocked()
	reserves := f.accountBalanceLocked(AcctReserves)

	var reserveMonths int64
	if revenue > 0 {
		reserveMonths = int64(reserves) / int64(revenue)
	}
	return CreditRating(int64(debt), int64(revenue), f.missedPayments, reserveMonths)
}

// taxRevenueLocked sums the treasury's tax-category credits for the
// current tick (f.mu held). Determinism: taxCategories is a slice, not a
// map, so summation order is fixed (GR#21, AC-14).
func (f *FinanceAPI) taxRevenueLocked() Money {
	if err := f.checkNotCopied("taxRevenueLocked"); err != nil {
		return 0
	}
	var total Money
	for _, tx := range f.tickTxns {
		for _, e := range tx.Entries {
			if e.Account == AcctTreasury && e.Side == SideCredit && isTaxCategory(e.Category) {
				total, _ = satAddMoney(total, e.Amount)
			}
		}
	}
	return total
}

// accountBalanceLocked returns an account's balance (f.mu held).
func (f *FinanceAPI) accountBalanceLocked(id AccountID) Money {
	if err := f.checkNotCopied("accountBalanceLocked"); err != nil {
		return 0
	}
	if acct, ok := f.accounts[id]; ok {
		return acct.Balance
	}
	return 0
}
