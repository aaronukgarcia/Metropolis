package finance

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-548 independent destructive round (attacker "opus-round-bug548",
// 2026-09-05) — finance-primitive half. These tests attack
// PostWagesFromFirms directly: the new firms->households wage leg the
// fix introduces, its overdraft/credit-line behaviour, and its
// interaction with the aggregates that already sum CatWages.
//
// Every test here is written against the LEDGER, never against a
// hardcoded expectation of what the caller should have posted.

// seedForAttack opens the ledger with a treasury and household float so
// the interesting rejections are the ones under test, not incidental
// opening-balance overdrafts.
func seedForAttack(t *testing.T, f *FinanceAPI, treasury, households Money) {
	t.Helper()
	if treasury > 0 {
		if _, err := f.Post(Transaction{
			Description: "attack opening treasury",
			Entries: []Entry{
				{Account: AcctTreasury, Side: SideCredit, Amount: treasury, Category: Category("opening.capital")},
				{Account: AcctExternal, Side: SideDebit, Amount: treasury, Category: Category("opening.capital")},
			},
		}); err != nil {
			t.Fatalf("seed treasury: %v", err)
		}
	}
	if households > 0 {
		if _, err := f.Post(Transaction{
			Description: "attack opening households",
			Entries: []Entry{
				{Account: AcctHouseholds, Side: SideCredit, Amount: households, Category: Category("opening.capital")},
				{Account: AcctExternal, Side: SideDebit, Amount: households, Category: Category("opening.capital")},
			},
		}); err != nil {
			t.Fatalf("seed households: %v", err)
		}
	}
}

func balOf(t *testing.T, f *FinanceAPI, id AccountID) Money {
	t.Helper()
	b, ok := f.AccountBalance(id)
	if !ok {
		t.Fatalf("AccountBalance(%s): account missing", id)
	}
	return b
}

// TestBUG548Attack_PostWagesFromFirms_OverdraftIsLoudAndAtomic proves the
// credit-line boundary is a HARD, REGISTRY-SOURCED rejection (MET-G201,
// GR#7/GR#17 — never a silent partial post): with no credit line, firms
// cannot pay a penny of wages, the error carries the registry code, and
// BOTH sides of the would-be transaction are untouched.
func TestBUG548Attack_PostWagesFromFirms_OverdraftIsLoudAndAtomic(t *testing.T) {
	f := NewFinanceAPI("attack-overdraft")
	seedForAttack(t, f, 1_000_000, 500_000)

	beforeFirms := balOf(t, f, AcctFirms)
	beforeHH := balOf(t, f, AcctHouseholds)

	posted, err := f.PostWagesFromFirms(1)
	if err == nil {
		t.Fatalf("PostWagesFromFirms(1) with zero balance and zero credit line succeeded (posted=%d) — an overdraft must be rejected (AC-13)", posted)
	}
	if !errors.Is(err, &errs.E{Code: ErrInsufficientFunds}) {
		t.Fatalf("PostWagesFromFirms overdraft error = %v, want registry code %s (GR#7: the failure must be identifiable, not a bare error)", err, ErrInsufficientFunds)
	}
	if posted != 0 {
		t.Fatalf("PostWagesFromFirms returned posted=%d on a rejected post, want 0 — a caller adding this to its flow metric would book money that never moved", posted)
	}
	if got := balOf(t, f, AcctFirms); got != beforeFirms {
		t.Fatalf("AcctFirms = %d after a REJECTED post, want unchanged %d (partial post)", got, beforeFirms)
	}
	if got := balOf(t, f, AcctHouseholds); got != beforeHH {
		t.Fatalf("AcctHouseholds = %d after a REJECTED post, want unchanged %d (households credited without a payer = money from nowhere)", got, beforeHH)
	}
	if got := f.WagesPosted(); got != 0 {
		t.Fatalf("WagesPosted() = %d after a rejected firms wage post, want 0", got)
	}
}

// TestBUG548Attack_CreditLineIsAnExactBoundedCap proves the failure mode
// at the cap is BOUNDED: firms may go exactly as negative as the credit
// line and not one micropound further, and the rejection at line+1 is
// clean.
func TestBUG548Attack_CreditLineIsAnExactBoundedCap(t *testing.T) {
	const line Money = 1_000_000
	f := NewFinanceAPI("attack-cap")
	seedForAttack(t, f, 1_000_000, 500_000)
	if err := f.SetCreditLine(AcctFirms, line); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}

	if _, err := f.PostWagesFromFirms(line + 1); err == nil {
		t.Fatalf("PostWagesFromFirms(line+1) succeeded — the credit line is not a cap")
	}
	if got := balOf(t, f, AcctFirms); got != 0 {
		t.Fatalf("AcctFirms = %d after the over-line rejection, want 0", got)
	}

	if _, err := f.PostWagesFromFirms(line); err != nil {
		t.Fatalf("PostWagesFromFirms(line) rejected: %v — the line must be fully drawable", err)
	}
	if got := balOf(t, f, AcctFirms); got != -line {
		t.Fatalf("AcctFirms = %d after drawing the full line, want exactly %d", got, -line)
	}
	// Exhausted: not even 1 more micropound.
	if _, err := f.PostWagesFromFirms(1); err == nil {
		t.Fatal("PostWagesFromFirms(1) succeeded on an exhausted credit line — firms can overdraft past the cap")
	}
	if got := balOf(t, f, AcctFirms); got != -line {
		t.Fatalf("AcctFirms = %d after the post-exhaustion rejection, want %d (unbounded drift past the cap)", got, -line)
	}
}

// TestBUG548Attack_BothWageLegsCountIdentically proves WagesPosted (which
// attract's HousingAffordability reads) sums the firms leg and the
// treasury leg identically — a public/private split must not change the
// headline wage figure the rest of the sim consumes.
func TestBUG548Attack_BothWageLegsCountIdentically(t *testing.T) {
	const private Money = 700_000
	const public Money = 300_000

	whole := NewFinanceAPI("attack-whole")
	seedForAttack(t, whole, 10_000_000, 500_000)
	if _, err := whole.PostWages(private + public); err != nil {
		t.Fatalf("PostWages(whole): %v", err)
	}

	split := NewFinanceAPI("attack-split")
	seedForAttack(t, split, 10_000_000, 500_000)
	if err := split.SetCreditLine(AcctFirms, 10_000_000); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}
	if _, err := split.PostWagesFromFirms(private); err != nil {
		t.Fatalf("PostWagesFromFirms: %v", err)
	}
	if _, err := split.PostWages(public); err != nil {
		t.Fatalf("PostWages(public): %v", err)
	}

	if a, b := whole.WagesPosted(), split.WagesPosted(); a != b {
		t.Fatalf("WagesPosted: undivided = %d, split = %d — the split changed the figure attract reads", a, b)
	}
	if a, b := balOf(t, whole, AcctHouseholds), balOf(t, split, AcctHouseholds); a != b {
		t.Fatalf("households: undivided = %d, split = %d — the split changed what workers received", a, b)
	}
	// The whole point of the fix: the payer differs.
	if got := balOf(t, split, AcctFirms); got != -private {
		t.Fatalf("split AcctFirms = %d, want %d (firms must actually pay the private share)", got, -private)
	}
	if got, want := balOf(t, split, AcctTreasury), balOf(t, whole, AcctTreasury)+private; got != want {
		t.Fatalf("split treasury = %d, want %d (the treasury must be spared the PRIVATE share)", got, want)
	}
}

// TestBUG548Attack_PostWagesFromFirms_NegativeRejected — a negative wage
// bill (an int64 underflow upstream) must be rejected by registry code,
// never silently reversed into a firms CREDIT.
func TestBUG548Attack_PostWagesFromFirms_NegativeRejected(t *testing.T) {
	f := NewFinanceAPI("attack-neg")
	seedForAttack(t, f, 1_000_000, 500_000)
	if err := f.SetCreditLine(AcctFirms, 1_000_000); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}
	if _, err := f.PostWagesFromFirms(-1); err == nil {
		t.Fatal("PostWagesFromFirms(-1) succeeded — a negative wage bill would move money the wrong way")
	}
	if got := balOf(t, f, AcctFirms); got != 0 {
		t.Fatalf("AcctFirms = %d after a rejected negative post, want 0", got)
	}
	if _, err := f.PostWagesFromFirms(0); err != nil {
		t.Fatalf("PostWagesFromFirms(0): %v — a zero bill must be a clean no-op, not an error", err)
	}
}

// TestBUG548Attack_LedgerStaysBalancedAcrossTheNewLeg — the double-entry
// law over every account (money, external and liability alike): the sum
// of all balances is identically zero after the new leg, and the tick's
// conservation scan is clean.
func TestBUG548Attack_LedgerStaysBalancedAcrossTheNewLeg(t *testing.T) {
	f := NewFinanceAPI("attack-balance")
	seedForAttack(t, f, 10_000_000, 5_000_000)
	if err := f.SetCreditLine(AcctFirms, 100_000_000); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	if _, err := f.PostWagesFromFirms(3_000_000); err != nil {
		t.Fatalf("PostWagesFromFirms: %v", err)
	}
	if _, err := f.CollectTax(TaxRates{IncomeRate: 2800}, 3_000_000, 0, 0); err != nil {
		t.Fatalf("CollectTax: %v", err)
	}

	var sum Money
	for _, id := range []AccountID{AcctTreasury, AcctHouseholds, AcctFirms, AcctReserves, AcctDebt, AcctExternal} {
		sum += balOf(t, f, id)
	}
	if sum != 0 {
		t.Fatalf("sum of every account balance = %d, want 0 — double entry broken by the new wage leg", sum)
	}
	if v := f.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("FindConservationViolations = %+v, want none", v)
	}
	// The tax must be exactly 28% of the posted bill — never the retired
	// 100% clawback, never a rounding-away-to-nothing.
	wantTax := Money(3_000_000 * 2800 / 10_000)
	var gotTax Money
	for _, e := range f.LinesByCategory(CatTaxIncome) {
		if e.Account == AcctTreasury && e.Side == SideCredit {
			gotTax += e.Amount
		}
	}
	if gotTax != wantTax {
		t.Fatalf("income tax credited to treasury = %d, want %d", gotTax, wantTax)
	}
}
