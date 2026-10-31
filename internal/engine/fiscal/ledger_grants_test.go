package fiscal

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// FEAT-2326609836: the ledger Sankey's NodeGrants reads finance.CatGrants.
// Provisional-when-empty AND real-when-lines-exist, with an INDEPENDENT
// recompute (the test sums the raw ledger lines itself, not via the helper
// under test).

func ledgerGrantsNode(t *testing.T, f *FiscalAPI) SankeyNode {
	t.Helper()
	topo, err := f.LedgerTopology()
	if err != nil {
		t.Fatalf("LedgerTopology: %v", err)
	}
	for _, n := range topo.Nodes {
		if n.ID == NodeGrants {
			return n
		}
	}
	t.Fatal("grants node missing from ledger topology")
	return SankeyNode{}
}

func postGrantLine(t *testing.T, fin *finance.FinanceAPI, amount finance.Money) {
	t.Helper()
	if _, err := fin.Post(finance.Transaction{
		Description: "grant",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: finance.CatGrants},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: finance.CatGrants},
		},
	}); err != nil {
		t.Fatalf("post grant: %v", err)
	}
}

func TestLedgerGrants_ProvisionalZeroWhenNoLines(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	n := ledgerGrantsNode(t, f)
	if !n.Provisional || n.Amount != 0 {
		t.Fatalf("grants node with no lines = %+v, want Provisional zero", n)
	}
	in, err := f.LedgerMoneyIn()
	if err != nil || in != 0 {
		t.Fatalf("LedgerMoneyIn = %d,%v want 0", int64(in), err)
	}
}

func TestLedgerGrants_RealWhenLinesExistAndMoneyInEqualsIndependentRecompute(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	fixedLedger(t, fin)
	if _, err := fin.PostCouncilTax(55); err != nil {
		t.Fatalf("PostCouncilTax: %v", err)
	}
	postGrantLine(t, fin, 500)
	postGrantLine(t, fin, 70)

	// Independent recompute straight off the raw ledger lines.
	var want finance.Money
	for _, l := range fin.LinesByCategory(finance.CatGrants) {
		if l.Account == finance.AcctTreasury && l.Side == finance.SideCredit {
			want += l.Amount
		}
	}
	if want != 570 {
		t.Fatalf("independent recompute = %d, want 570", int64(want))
	}
	n := ledgerGrantsNode(t, f)
	if n.Provisional || n.Amount != want {
		t.Fatalf("grants node = %+v, want real %d", n, int64(want))
	}
	in, err := f.LedgerMoneyIn()
	if err != nil {
		t.Fatalf("LedgerMoneyIn: %v", err)
	}
	if wantIn := fin.TaxRevenue() + want; in != wantIn || wantIn != 625 {
		t.Fatalf("LedgerMoneyIn = %d, want tax(%d)+grants(%d)=625", int64(in), int64(fin.TaxRevenue()), int64(want))
	}
	// BudgetBalance stays the canonical finance figure.
	if bal, _ := f.LedgerBudgetBalance(); bal != fin.BudgetBalance() {
		t.Fatalf("LedgerBudgetBalance = %d, want finance %d", int64(bal), int64(fin.BudgetBalance()))
	}
	// Drill-through is exactly the grants lines (both legs).
	lines, err := f.LedgerDrillThrough(NodeGrants)
	if err != nil || len(lines) != 4 {
		t.Fatalf("LedgerDrillThrough(grants) = %d lines, %v want 4", len(lines), err)
	}
	// The identity in = out + other + balance holds by construction.
	out, _ := f.LedgerMoneyOut()
	other, _ := f.LedgerOtherOutflows()
	bal, _ := f.LedgerBudgetBalance()
	if in != out+other+bal {
		t.Fatalf("in %d != out %d + other %d + bal %d", int64(in), int64(out), int64(other), int64(bal))
	}
}
