package finance

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// TestOverdraftBypassRejected (Destructive round #1, AC-13/GR#16) proves
// two debit entries to the same account are validated against the
// accumulated effect, not the untouched pre-transaction balance: £100 in
// the treasury cannot cover two £60 debits.
func TestOverdraftBypassRejected(t *testing.T) {
	f := NewFinanceAPI("reg-overdraft")
	seedTreasury(t, f, gbp(100))

	_, err := f.Post(Transaction{
		Description: "two debits against one account",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: gbp(60), Category: CatOpex},
			{Account: AcctTreasury, Side: SideDebit, Amount: gbp(60), Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: gbp(60), Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: gbp(60), Category: CatOpex},
		},
	})
	if !hasCode(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}
	if bal, _ := f.AccountBalance(AcctTreasury); bal != gbp(100) {
		t.Fatalf("treasury balance should be unchanged at £100, got %d", bal)
	}
	if got := f.TotalMoneyInCirculation(); got != gbp(100) {
		t.Fatalf("money stock must stay positive (no money destroyed): got %d", got)
	}
}

// TestServiceDebtReducesLoanBook (Destructive round #2) proves the
// ServiceDebt principal path retires the same amount from the outstanding
// loan book, so OutstandingDebt (and the credit rating) stay consistent
// with the ledger instead of double-counting repaid principal.
func TestServiceDebtReducesLoanBook(t *testing.T) {
	f := NewFinanceAPI("reg-debt")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	seedTreasury(t, f, gbp(1000))
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(500), TermMonths: 60}); err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	if err := f.ServiceDebt(gbp(10), gbp(100)); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	if got := f.OutstandingDebt(); got != gbp(400) {
		t.Fatalf("OutstandingDebt = %d after repaying £100 of £500, want £400", got)
	}
}

// TestDebitCreditSumOverflowRejected (Destructive round #3, GR#16) proves
// a transaction whose debit (or credit) sum overflows int64 is rejected
// as unbalanced rather than wrapping into a false "balanced" comparison.
func TestDebitCreditSumOverflowRejected(t *testing.T) {
	f := NewFinanceAPI("reg-overflow")
	huge := Money(1 << 62) // two of these sum to 1<<63, which wraps int64

	_, err := f.Post(Transaction{
		Description: "overflowing debit/credit sums",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: huge, Category: CatOpex},
			{Account: AcctTreasury, Side: SideDebit, Amount: huge, Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: huge, Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: huge, Category: CatOpex},
		},
	})
	if !hasCode(err, ErrUnbalancedTransaction) {
		t.Fatalf("expected ErrUnbalancedTransaction for an overflowing sum, got %v", err)
	}
}

// TestCreditRatingRatioOverflow (Destructive round #4, GR#16) proves a
// hugely indebted city never rates better than a low-debt city — the
// debt*1000 multiply must not overflow and wrap.
func TestCreditRatingRatioOverflow(t *testing.T) {
	huge := int64(1) << 62
	lowDebt := CreditRating(1, 1, 0, 0)
	hugeDebt := CreditRating(huge, 1, 0, 0)
	if hugeDebt >= lowDebt {
		t.Fatalf("hugely-indebted rating %d must be below low-debt rating %d", hugeDebt, lowDebt)
	}
}

// TestLandFactorsDoNotWrapBelowBaseline (Destructive round #5, GR#16)
// proves extreme access/amenity inputs clamp instead of wrapping the
// factor below the 1.0× (1000) baseline.
func TestLandFactorsDoNotWrapBelowBaseline(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	if got := AccessFactor(false, maxInt); got < factorScale {
		t.Fatalf("AccessFactor(roads=MaxInt) = %d wrapped below baseline %d", got, factorScale)
	}
	if got := AccessFactor(true, maxInt); got < factorScale {
		t.Fatalf("AccessFactor(junction, roads=MaxInt) = %d wrapped below baseline", got)
	}
	if got := AmenityFactor(maxInt, false, 0); got < factorScale {
		t.Fatalf("AmenityFactor(services=MaxInt) = %d wrapped below baseline %d", got, factorScale)
	}
	if got := AmenityFactor(maxInt, true, 0); got < factorScale {
		t.Fatalf("AmenityFactor(services=MaxInt, coast) = %d wrapped below baseline", got)
	}
}

// TestRunningMoneySumsDoNotWrap (round-2 defect #1, GR#16) proves two
// huge Borrows saturate the running money/debt totals instead of wrapping
// to a negative money stock, treasury balance, and outstanding debt.
func TestRunningMoneySumsDoNotWrap(t *testing.T) {
	f := NewFinanceAPI("reg-wrap")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	huge := Money(1 << 62)
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: huge, TermMonths: 12}); err != nil {
		t.Fatalf("first Borrow: %v", err)
	}
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: huge, TermMonths: 12}); err != nil {
		t.Fatalf("second Borrow: %v", err)
	}

	if got := f.TotalMoneyInCirculation(); got < 0 {
		t.Fatalf("TotalMoneyInCirculation wrapped negative: %d", got)
	}
	if got, _ := f.AccountBalance(AcctTreasury); got < 0 {
		t.Fatalf("treasury balance wrapped negative: %d", got)
	}
	if got := f.OutstandingDebt(); got < 0 {
		t.Fatalf("OutstandingDebt wrapped negative: %d", got)
	}
}

// TestPaybackCurveDoesNotWrap (round-2 defect #2, GR#16) proves the
// payback-curve cumulative-return product saturates instead of producing
// a non-monotonic wrapped curve.
func TestPaybackCurveDoesNotWrap(t *testing.T) {
	f := NewFinanceAPI("reg-payback")
	seedTreasury(t, f, Money(1<<60))

	prog, err := f.StartInvestment("huge", gbp(1000), Money(1<<62), 4)
	if err != nil {
		t.Fatalf("StartInvestment: %v", err)
	}
	prev := Money(0)
	for _, pt := range prog.PaybackCurve() {
		if pt.CumulativeReturn < prev {
			t.Fatalf("payback curve wrapped at month %d: %d < previous %d",
				pt.MonthOffset, pt.CumulativeReturn, prev)
		}
		prev = pt.CumulativeReturn
	}
}

// TestMonthlyPaymentDoesNotWrap (round-2 defect #3, GR#16) proves the
// interest+principal debt-service sum saturates instead of wrapping
// negative when the interest leg saturates.
func TestMonthlyPaymentDoesNotWrap(t *testing.T) {
	loan := Loan{Outstanding: Money(1 << 62), RateBp: BasisPoints(10000), TermMonths: 1}
	if got := loan.MonthlyPayment(); got < 0 {
		t.Fatalf("MonthlyPayment wrapped negative: %d", got)
	}
}

// TestSafeMulMixedSignOverflow (round-3 defect #1, GR#16) proves the
// mixed-sign branch of num.SafeMul flags and saturates, and does not
// mis-flag small mixed-sign products.
func TestSafeMulMixedSignOverflow(t *testing.T) {
	if v, ok := num.SafeMul(math.MaxInt64, -2); !ok || v != math.MinInt64 {
		t.Fatalf("num.SafeMul(MaxInt64, -2) = (%d, %v), want (MinInt64, true)", v, ok)
	}
	if v, ok := num.SafeMul(math.MinInt64, 2); !ok || v != math.MinInt64 {
		t.Fatalf("num.SafeMul(MinInt64, 2) = (%d, %v), want (MinInt64, true)", v, ok)
	}
	if v, ok := num.SafeMul(-5, 3); ok || v != -15 {
		t.Fatalf("num.SafeMul(-5, 3) = (%d, %v), want (-15, false)", v, ok)
	}
	if v, ok := num.SafeMul(5, -3); ok || v != -15 {
		t.Fatalf("num.SafeMul(5, -3) = (%d, %v), want (-15, false)", v, ok)
	}
	// 2 × -(2^62) = -2^63 = MinInt64 exactly: the representable negative
	// boundary must NOT be flagged as overflow.
	if v, ok := num.SafeMul(2, -(1 << 62)); ok || v != math.MinInt64 {
		t.Fatalf("num.SafeMul(2, -(1<<62)) = (%d, %v), want (MinInt64, false)", v, ok)
	}
}

// TestPaybackCurveNegativeReturnDoesNotJumpPositive (round-3 defect #2,
// GR#16) proves a negative MonthlyReturn keeps the cumulative-return
// curve monotonic (never jumps from negative to positive via a wrap).
func TestPaybackCurveNegativeReturnDoesNotJumpPositive(t *testing.T) {
	prog := InvestmentProgramme{MonthlyReturn: Money(-(1 << 62)), PaybackMonths: 4}
	prev := Money(0)
	for _, pt := range prog.PaybackCurve() {
		if pt.CumulativeReturn > prev {
			t.Fatalf("negative-return curve jumped upward at month %d: %d > %d",
				pt.MonthOffset, pt.CumulativeReturn, prev)
		}
		prev = pt.CumulativeReturn
	}
}

// TestCreditRatingNeverImprovesFromNegativeMisses (round-3 defect #3,
// GR#16) proves a negative missedPayments input is clamped and never
// improves the score.
func TestCreditRatingNeverImprovesFromNegativeMisses(t *testing.T) {
	clean := CreditRating(1, 1, 0, 0)
	negative := CreditRating(1, 1, -6148914691236517206, 0)
	if negative > clean {
		t.Fatalf("negative missedPayments must not improve the score: %d > %d", negative, clean)
	}
}

// TestNoMoneyTotalsWrapUnderExtremeInputs is the broad sweep invariant:
// after a barrage of extreme operations, every non-negative money total
// (money stock, from-scratch recompute, debt, treasury) stays non-negative.
func TestNoMoneyTotalsWrapUnderExtremeInputs(t *testing.T) {
	f := NewFinanceAPI("reg-broad")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	huge := Money(1 << 62)

	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: huge, TermMonths: 12}); err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: huge, TermMonths: 12}); err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	// Drive the raw path too: an unbalanced extreme credit must still not
	// wrap the running totals.
	f.postRaw(Transaction{Entries: []Entry{
		{Account: AcctTreasury, Side: SideCredit, Amount: huge, Category: CatLoan},
	}})

	if got := f.TotalMoneyInCirculation(); got < 0 {
		t.Fatalf("TotalMoneyInCirculation wrapped negative: %d", got)
	}
	if got := f.RecomputeMoneyStock(); got < 0 {
		t.Fatalf("RecomputeMoneyStock wrapped negative: %d", got)
	}
	if got := f.OutstandingDebt(); got < 0 {
		t.Fatalf("OutstandingDebt wrapped negative: %d", got)
	}
	if got, _ := f.AccountBalance(AcctTreasury); got < 0 {
		t.Fatalf("treasury balance wrapped negative: %d", got)
	}
}
