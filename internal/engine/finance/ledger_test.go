package finance

import (
	"testing"
)

// TestDoubleEntryPostingBalances (AC-1) posts one transaction and then
// retrieves the posted entries themselves (not just a running total) and
// asserts the transaction has both a debit and a credit leg that sum to
// zero.
func TestDoubleEntryPostingBalances(t *testing.T) {
	f := NewFinanceAPI("ac1")
	seedTreasury(t, f, gbp(1000))
	if _, err := f.Post(Transaction{
		Description: "test transfer",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: gbp(100), Category: CatWages},
			{Account: AcctHouseholds, Side: SideCredit, Amount: gbp(100), Category: CatWages},
		},
	}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	// Retrieve the actual posted entries (not just a running total) and
	// assert the transaction carries both a debit and a credit leg that
	// sum to zero.
	entries := f.LinesByCategory(CatWages)
	if len(entries) != 2 {
		t.Fatalf("expected exactly two posted entries (one debit, one credit), got %d", len(entries))
	}

	var debits, credits Money
	var sawDebit, sawCredit bool
	for _, e := range entries {
		switch e.Side {
		case SideDebit:
			debits += e.Amount
			sawDebit = true
		case SideCredit:
			credits += e.Amount
			sawCredit = true
		}
	}
	if !sawDebit || !sawCredit {
		t.Fatalf("transaction must have both a debit and a credit leg (debit=%v credit=%v)", sawDebit, sawCredit)
	}
	if debits != credits {
		t.Errorf("debits %d != credits %d — not balanced", debits, credits)
	}
	if debits == 0 {
		t.Error("posting a zero-amount transfer is not a real double entry")
	}
}

// TestUnbalancedTransactionRejected (AC-12) proves an unbalanced posting
// is rejected with the registry error AND that no partial transaction or
// plug entry reaches the ledger.
func TestUnbalancedTransactionRejected(t *testing.T) {
	f := NewFinanceAPI("ac12")
	before := f.TotalMoneyInCirculation()

	_, err := f.Post(Transaction{
		Description: "unbalanced",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: gbp(100), Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: gbp(90), Category: CatOpex},
		},
	})
	if err == nil {
		t.Fatal("expected an unbalanced transaction to be rejected")
	}
	if !hasCode(err, ErrUnbalancedTransaction) {
		t.Fatalf("expected ErrUnbalancedTransaction (%s), got %v", ErrUnbalancedTransaction, err)
	}

	// No partial post, no plug entry: the ledger is unchanged.
	if len(f.tickTxns) != 0 {
		t.Fatalf("no transaction should have been written, tickTxns has %d", len(f.tickTxns))
	}
	if after := f.TotalMoneyInCirculation(); after != before {
		t.Fatalf("money stock changed from %d to %d on a rejected post", before, after)
	}
	if len(f.Lines(AcctTreasury)) != 0 || len(f.Lines(AcctExternal)) != 0 {
		t.Fatal("no entry should have been written for the rejected post")
	}
}

// TestOverdraftRejected (AC-13) proves a debit that would take a money
// account negative is rejected without credit, and allowed when a credit
// line covers it.
func TestOverdraftRejected(t *testing.T) {
	f := NewFinanceAPI("ac13")

	// Treasury starts empty; a wage payment would overdraw it.
	_, err := f.PostWages(gbp(50))
	if err == nil {
		t.Fatal("expected an overdraft to be rejected with no funds and no credit")
	}
	if !hasCode(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds (%s), got %v", ErrInsufficientFunds, err)
	}
	if bal, _ := f.AccountBalance(AcctTreasury); bal != 0 {
		t.Fatalf("treasury should remain at 0, got %d", bal)
	}

	// With a credit line covering the shortfall, the same debit is allowed.
	if err := f.SetCreditLine(AcctTreasury, gbp(100)); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}
	if _, err := f.PostWages(gbp(50)); err != nil {
		t.Fatalf("expected the credit-line-covered debit to post, got %v", err)
	}
	if bal, _ := f.AccountBalance(AcctTreasury); bal != -gbp(50) {
		t.Fatalf("treasury should be -50 (drawn on credit), got %d", bal)
	}
}

// TestTotalMoneyInCirculation (AC-10) proves the maintained running total
// matches a from-scratch recomputation over the ledger entries, for a
// synthetic ledger state with both internal and external flows.
func TestTotalMoneyInCirculation(t *testing.T) {
	f := NewFinanceAPI("ac10")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	seedTreasury(t, f, gbp(1000))

	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(500), TermMonths: 60}); err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	if _, err := f.PostWages(gbp(200)); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(100, gbp(1)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if _, err := f.SettleOpex(gbp(80)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}

	got := f.TotalMoneyInCirculation()
	want := f.RecomputeMoneyStock()
	if got != want {
		t.Fatalf("running total %d != from-scratch recompute %d", got, want)
	}

	// Also assert against an independent from-scratch sum over the ledger.
	var fromScratch Money
	for _, tx := range f.txns {
		fromScratch += tx.moneyDelta(f.role)
	}
	if got != fromScratch {
		t.Fatalf("running total %d != independent delta sum %d", got, fromScratch)
	}
}

// TestConservationViolationLocalisesUnbalancedTransaction (AC-10b) posts
// an unbalanced transaction directly against the low-level entry path
// (postRaw) and asserts the per-tick log identifies that specific
// transaction's ID and account — not merely that the running total moved.
func TestConservationViolationLocalisesUnbalancedTransaction(t *testing.T) {
	f := NewFinanceAPI("ac10b")
	seedTreasury(t, f, gbp(100))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	// A balanced post first (the control: not a violation).
	if _, err := f.PostWages(gbp(10)); err != nil {
		t.Fatalf("PostWages: %v", err)
	}

	// Now the bug: an unbalanced credit to the treasury, bypassing Post.
	rawID := f.postRaw(Transaction{
		Description: "money created from nothing",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideCredit, Amount: gbp(500), Category: CatLoan},
		},
	})

	violations := f.FindConservationViolations()
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 conservation violation, got %d: %+v", len(violations), violations)
	}
	v := violations[0]
	if v.TransactionID != rawID {
		t.Errorf("violation names transaction %d, want %d", v.TransactionID, rawID)
	}
	if len(v.AccountIDs) == 0 || v.AccountIDs[0] != AcctTreasury {
		t.Errorf("violation should name the treasury account, got %v", v.AccountIDs)
	}
	if !v.Unbalanced {
		t.Error("violation should be flagged unbalanced")
	}
}

// TestDrillThroughLinesSumToAggregate (AC-11) proves every aggregate the
// API exposes is the sum of retrievable ledger lines.
func TestDrillThroughLinesSumToAggregate(t *testing.T) {
	f := NewFinanceAPI("ac11")
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, f, gbp(1000))

	wages := gbp(400)
	spend := gbp(200)
	opex := gbp(50)
	if _, err := f.PostWages(wages); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(200, gbp(1)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if _, err := f.CollectTax(TaxRates{IncomeRate: 1000, SalesRate: 1000}, wages, spend, 0); err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if _, err := f.SettleOpex(opex); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}

	// Tax revenue == sum of treasury credit entries with a tax category.
	var taxLines Money
	for _, e := range f.Lines(AcctTreasury) {
		if e.Side == SideCredit && isTaxCategory(e.Category) {
			taxLines += e.Amount
		}
	}
	if got := f.TaxRevenue(); got != taxLines {
		t.Errorf("TaxRevenue() %d != sum of tax lines %d", got, taxLines)
	}

	// Opex == sum of treasury debit entries with CatOpex.
	var opexLines Money
	for _, e := range f.Lines(AcctTreasury) {
		if e.Side == SideDebit && e.Category == CatOpex {
			opexLines += e.Amount
		}
	}
	if got := f.OpexTotal(); got != opexLines {
		t.Errorf("OpexTotal() %d != sum of opex lines %d", got, opexLines)
	}
}
