package fiscal

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
)

// newTestFiscal builds a fully-wired FiscalAPI backed by the real
// data/fiscal.json, a fresh *finance.FinanceAPI and the real
// data/tax_instruments.json (so every figure a test reads is data-sourced,
// GR#15). It fails the test on any construction error.
func newTestFiscal(t *testing.T) (*FiscalAPI, *finance.FinanceAPI, *tax.TaxAPI) {
	t.Helper()
	f, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("fiscal.LoadDefault: %v", err)
	}
	fin := finance.NewFinanceAPI("test")
	taxAPI, err := tax.LoadDefault("test")
	if err != nil {
		t.Fatalf("tax.LoadDefault: %v", err)
	}
	if err := f.SetFinance(fin); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := f.SetTax(taxAPI); err != nil {
		t.Fatalf("SetTax: %v", err)
	}
	return f, fin, taxAPI
}

// seedTreasury credits the city treasury from the external world so later
// treasury debits (imports, debt service, benefits) don't hit the overdraft
// guard. It is a test-only injection of an external money inflow.
func seedTreasury(t *testing.T, fin *finance.FinanceAPI, amount finance.Money) {
	t.Helper()
	if _, err := fin.Post(finance.Transaction{
		Description: "seed treasury",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: "seed"},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: "seed"},
		},
	}); err != nil {
		t.Fatalf("seedTreasury: %v", err)
	}
}

// incomeInstrumentID returns the engine.tax income-category instrument's data
// ID (the PAYE instrument), looked up by data category rather than a
// hardcoded name (GR#15).
func incomeInstrumentID(t *testing.T, taxAPI *tax.TaxAPI) string {
	t.Helper()
	for _, info := range taxAPI.Instruments() {
		if info.Category == dataCatIncome {
			return info.ID
		}
	}
	t.Fatal("no income-category tax instrument loaded from data")
	return ""
}

// nodeAmountOf returns the amount of a node category in a topology, or fails
// the test if the node is absent.
func nodeAmountOf(t *testing.T, topo Topology, cat NodeCategory) finance.Money {
	t.Helper()
	for _, n := range topo.Nodes {
		if n.ID == cat {
			return n.Amount
		}
	}
	t.Fatalf("node %q not present in topology", cat)
	return 0
}
