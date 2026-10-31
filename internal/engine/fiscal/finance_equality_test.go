package fiscal

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// AC-1 overlap-verdict equality tests (docs/planning/acceptance/
// wire-engine.fiscal-verdict.md). Each test pins a fiscal export that the
// verdict classes "already covered by finance" (or documents the one place
// the two disagree) against the finance function returning the same quantity,
// on a fixed ledger. Test-only: no production code is touched.

// fixedLedger posts a fixed, hand-computable month into fin: imports 700,
// debt-service interest 300 (principal 100), and a construction spend of 450.
func fixedLedger(t *testing.T, fin *finance.FinanceAPI) {
	t.Helper()
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 10_000_000)
	for _, acct := range []finance.AccountID{finance.AcctHouseholds, finance.AcctFirms} {
		if _, err := fin.Post(finance.Transaction{
			Description: "seed payer",
			Entries: []finance.Entry{
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: 100_000_000, Category: "seed"},
				{Account: acct, Side: finance.SideCredit, Amount: 100_000_000, Category: "seed"},
			},
		}); err != nil {
			t.Fatalf("seed %v: %v", acct, err)
		}
	}
	if _, err := fin.SettleImports(700); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	if err := fin.ServiceDebt(300, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
}

// TestEquality_ImportsAndInterestNodesEqualFinanceTotals: the Imports and
// Interest Sankey nodes ARE finance.ImportsTotal / finance.DebtServiceTotal
// (verdict rows Node(imports), Node(interest) -> covered).
func TestEquality_ImportsAndInterestNodesEqualFinanceTotals(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	fixedLedger(t, fin)

	topo, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology: %v", err)
	}
	if got, want := nodeAmountOf(t, topo, NodeImports), fin.ImportsTotal(); got != want || want != 700 {
		t.Errorf("imports node = %d, finance.ImportsTotal = %d, want 700", int64(got), int64(want))
	}
	if got, want := nodeAmountOf(t, topo, NodeInterest), fin.DebtServiceTotal(); got != want || want != 300 {
		t.Errorf("interest node = %d, finance.DebtServiceTotal = %d, want 300", int64(got), int64(want))
	}
	// Node() agrees with the topology node.
	for _, c := range []struct {
		cat  NodeCategory
		want finance.Money
	}{{NodeImports, fin.ImportsTotal()}, {NodeInterest, fin.DebtServiceTotal()}} {
		n, err := f.Node(c.cat)
		if err != nil || n.Amount != c.want {
			t.Errorf("Node(%q) = %d,%v want %d", c.cat, int64(n.Amount), err, int64(c.want))
		}
	}
}

// TestEquality_MoneyOutEqualsFinanceOutflows: MoneyOut = imports + interest
// = finance.ImportsTotal() + finance.OpexBreakdown().DebtService (the
// finance function for the interest leg).
func TestEquality_MoneyOutEqualsFinanceOutflows(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	fixedLedger(t, fin)

	got, err := f.MoneyOut()
	if err != nil {
		t.Fatalf("MoneyOut: %v", err)
	}
	want := fin.ImportsTotal() + fin.OpexBreakdown().DebtService
	if got != want || want != 1000 {
		t.Errorf("MoneyOut = %d, finance imports+debt service = %d, want 1000", int64(got), int64(want))
	}
}

// TestEquality_DrillThroughEqualsFinanceLines: drill-through for imports,
// interest and budget is exactly finance.LinesByCategory / finance.Lines;
// tax drill-through is the concatenation of finance's tax.income/sales/corp
// lines (and, documented divergence, EXCLUDES tax.council).
func TestEquality_DrillThroughEqualsFinanceLines(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	fixedLedger(t, fin)
	if _, err := fin.PostCouncilTax(55); err != nil {
		t.Fatalf("PostCouncilTax: %v", err)
	}

	pairs := []struct {
		cat  NodeCategory
		want []finance.Entry
	}{
		{NodeImports, fin.LinesByCategory(finance.CatImports)},
		{NodeInterest, fin.LinesByCategory(finance.CatDebtService)},
		{NodeBudget, fin.Lines(finance.AcctTreasury)},
	}
	for _, p := range pairs {
		got, err := f.DrillThrough(p.cat)
		if err != nil {
			t.Fatalf("DrillThrough(%q): %v", p.cat, err)
		}
		if len(p.want) == 0 {
			t.Fatalf("fixed ledger produced no finance lines for %q", p.cat)
		}
		if !reflect.DeepEqual(got, p.want) {
			t.Errorf("DrillThrough(%q) = %+v, want finance lines %+v", p.cat, got, p.want)
		}
	}

	// Tax drill-through omits council tax: finance counts it in TaxRevenue,
	// fiscal's drill does not. The verdict flags this as a merge-time fix.
	taxLines, err := f.DrillThrough(NodeTaxRevenue)
	if err != nil {
		t.Fatalf("DrillThrough(tax): %v", err)
	}
	if len(taxLines) != 0 {
		t.Errorf("no income/sales/corp tax posted, want 0 tax lines, got %d", len(taxLines))
	}
	if fin.TaxRevenue() != 55 {
		t.Errorf("finance.TaxRevenue = %d, want 55 (council tax counted by finance)", int64(fin.TaxRevenue()))
	}
}

// TestEquality_BudgetBalanceVsFinance: on a ledger with ONLY tax, imports and
// interest posted, fiscal.BudgetBalance equals finance.BudgetBalance; the
// moment any other treasury outflow exists (construction, services opex)
// they DISAGREE: fiscal's balance omits it. Finance is the canonical figure
// (the f2 balance sheet reads it); the verdict makes the Sankey balance
// delegate to it.
func TestEquality_BudgetBalanceVsFinance(t *testing.T) {
	f, fin, taxAPI := newTestFiscal(t)
	if err := taxAPI.SetFinance(fin); err != nil {
		t.Fatalf("tax.SetFinance: %v", err)
	}
	fixedLedger(t, fin)

	// Collect tax through the real posting path so the ledger and TaxAPI agree.
	for _, info := range taxAPI.Instruments() {
		if err := taxAPI.SetBase(info.ID, 1_000_000); err != nil {
			t.Fatalf("SetBase: %v", err)
		}
	}
	if _, err := taxAPI.CollectAll(); err != nil {
		t.Fatalf("CollectAll: %v", err)
	}

	got, err := f.BudgetBalance()
	if err != nil {
		t.Fatalf("BudgetBalance: %v", err)
	}
	// Equal when the ledger carries only the legs fiscal knows about, up to
	// the tax lines fiscal reads from TaxAPI versus finance.TaxRevenue.
	in, _ := f.MoneyIn()
	if in != fin.TaxRevenue() {
		t.Logf("fiscal.MoneyIn=%d finance.TaxRevenue=%d (tax-source divergence)", int64(in), int64(fin.TaxRevenue()))
	}
	if want := fin.BudgetBalance(); got != want {
		t.Errorf("BudgetBalance fiscal=%d finance=%d: must be equal with only tax/imports/interest posted", int64(got), int64(want))
	}

	// Now post a construction outflow: finance drops, fiscal does not.
	if _, err := fin.SettleConstruction(450); err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}
	got2, _ := f.BudgetBalance()
	if got2 != got {
		t.Errorf("fiscal.BudgetBalance moved on a construction posting (%d -> %d); documented behaviour is that it ignores it", int64(got), int64(got2))
	}
	if fin.BudgetBalance() != got-450 {
		t.Errorf("finance.BudgetBalance = %d, want %d (construction counted)", int64(fin.BudgetBalance()), int64(got-450))
	}
	if got2 == fin.BudgetBalance() {
		t.Error("expected fiscal and finance budget balance to diverge once construction posts")
	}
}

// TestEquality_SocialHousingBuildEqualsSettleConstruction: PostSocialHousingBuild
// is a pass-through; two identical ledgers, one via fiscal and one via
// finance.SettleConstruction, end identical (verdict: covered, drop).
func TestEquality_SocialHousingBuildEqualsSettleConstruction(t *testing.T) {
	f, finA, _ := newTestFiscal(t)
	finB := finance.NewFinanceAPI("test-b")
	for _, fin := range []*finance.FinanceAPI{finA, finB} {
		if err := fin.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		seedTreasury(t, fin, 10_000_000)
	}
	a, err := f.PostSocialHousingBuild(1234)
	if err != nil {
		t.Fatalf("PostSocialHousingBuild: %v", err)
	}
	b, err := finB.SettleConstruction(1234)
	if err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}
	if a != b {
		t.Errorf("returned cost fiscal=%d finance=%d", int64(a), int64(b))
	}
	if finA.ConstructionTotal() != finB.ConstructionTotal() || finA.ConstructionTotal() != 1234 {
		t.Errorf("ConstructionTotal fiscal-path=%d finance-path=%d want 1234", int64(finA.ConstructionTotal()), int64(finB.ConstructionTotal()))
	}
	if !reflect.DeepEqual(finA.Lines(finance.AcctTreasury), finB.Lines(finance.AcctTreasury)) {
		t.Error("treasury lines differ between the fiscal path and direct SettleConstruction")
	}
	if finA.TotalMoneyInCirculation() != finB.TotalMoneyInCirculation() {
		t.Error("money stock differs between the two paths")
	}
}

// TestEquality_CivilServiceClawbackEqualsFinanceIncomeTax: the clawback is
// the income tax finance.CollectTax would levy on the same wage bill at the
// same rate (verdict: same quantity, but fiscal needs the TaxAPI income rate
// as input, so it wires as a hook that supplies wage bill + rate).
func TestEquality_CivilServiceClawbackEqualsFinanceIncomeTax(t *testing.T) {
	f, _, taxAPI := newTestFiscal(t)
	const wageBill finance.Money = 8_000_000
	if err := f.SetCivilServiceWageBill(wageBill); err != nil {
		t.Fatalf("SetCivilServiceWageBill: %v", err)
	}
	clawback, err := f.CivilServiceClawback()
	if err != nil {
		t.Fatalf("CivilServiceClawback: %v", err)
	}

	var ratePct float64
	for _, info := range taxAPI.Instruments() {
		if info.Category == dataCatIncome {
			ratePct = info.Rate
		}
	}
	bp := finance.BasisPoints(int64(ratePct*100 + 0.5))
	ref := finance.NewFinanceAPI("ref")
	if err := ref.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, ref, 10_000_000)
	// Households must hold the wages to pay income tax.
	if _, err := ref.PostWages(wageBill); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	rc, err := ref.CollectTax(finance.TaxRates{IncomeRate: bp}, wageBill, 0, 0)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if clawback != rc.Income || rc.Income == 0 {
		t.Errorf("CivilServiceClawback = %d, finance.CollectTax income = %d (rate %v%% = %d bp)", int64(clawback), int64(rc.Income), ratePct, int64(bp))
	}
	net, _ := f.CivilServiceNet()
	if net != wageBill-rc.Income {
		t.Errorf("CivilServiceNet = %d, want gross-clawback %d", int64(net), int64(wageBill-rc.Income))
	}
}

// TestEquality_BenefitPostingEqualsRawFinancePost: the two benefit posters
// are thin wrappers over finance.Post; their ledger effect equals a hand-built
// treasury->households transfer under the benefit category (verdict: wires as
// a module hook, the category names are fiscal's only addition).
func TestEquality_BenefitPostingEqualsRawFinancePost(t *testing.T) {
	f, finA, _ := newTestFiscal(t)
	finB := finance.NewFinanceAPI("raw")
	for _, fin := range []*finance.FinanceAPI{finA, finB} {
		if err := fin.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		seedTreasury(t, fin, 10_000_000)
	}
	if _, err := f.PostUnemploymentSupport(321); err != nil {
		t.Fatalf("PostUnemploymentSupport: %v", err)
	}
	if _, err := finB.Post(finance.Transaction{
		Description: "unemployment support",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: 321, Category: "benefits.unemployment"},
			{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: 321, Category: "benefits.unemployment"},
		},
	}); err != nil {
		t.Fatalf("raw Post: %v", err)
	}
	if !reflect.DeepEqual(finA.LinesByCategory("benefits.unemployment"), finB.LinesByCategory("benefits.unemployment")) {
		t.Error("benefit lines differ from the raw finance.Post transfer")
	}
	if finA.TotalMoneyInCirculation() != finB.TotalMoneyInCirculation() {
		t.Error("money stock differs")
	}
}
