package fiscal

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-2326609835 (lead amendments A1-A3, docs/planning/acceptance/
// wire-engine.fiscal-verdict.md): the LEDGER-SOURCED query surface. These
// methods need engine.finance ONLY (no engine.tax): every figure is read from
// the finance ledger's own aggregates and tax.* categories, so on a composed
// city (where engine.tax is unwired and revenue posts through
// finance.CollectTax) the Sankey is non-zero and ledger-true. The older
// TaxAPI-sourced methods (SankeyTopology, MoneyIn, BudgetBalance,
// TaxBreakdown, DrillThrough) are unchanged and remain the engine.tax path.
//
// GR#3: fiscal holds no money total; the budget balance DELEGATES to
// finance.BudgetBalance (A2) - fiscal's own tax-minus-imports-minus-interest
// formula diverges from finance's on the first construction posting.

// ledgerTaxCategories is the ordered set of finance tax categories the
// ledger-sourced tax node and drill-through span: finance's whole
// taxCategories set INCLUDING tax.council (A3). A slice, never a map (GR#21).
var ledgerTaxCategories = []finance.Category{
	finance.CatTaxIncome, finance.CatTaxSales, finance.CatTaxCorp, finance.CatTaxCouncil,
}

// categoryTreasuryCredits sums the treasury-credit legs of one category over
// the current tick's ledger lines (the same shape finance.TaxRevenue sums).
func categoryTreasuryCredits(fin *finance.FinanceAPI, cat finance.Category) finance.Money {
	var total finance.Money
	for _, e := range fin.LinesByCategory(cat) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideCredit {
			total, _ = satAdd(total, e.Amount)
		}
	}
	return total
}

// LedgerTaxBreakdown returns the per-type tax inflow read from the finance
// ledger's tax.* categories (A1). Rates carries tax.council (rates & council
// tax); Duties has no ledger category and stays zero (Provisional). Its
// Total equals finance.TaxRevenue().
func (f *FiscalAPI) LedgerTaxBreakdown() (TaxBreakdown, error) {
	if err := f.checkNotCopied("LedgerTaxBreakdown"); err != nil {
		return TaxBreakdown{}, err
	}
	fin, err := f.requireFinance("LedgerTaxBreakdown")
	if err != nil {
		return TaxBreakdown{}, err
	}
	return TaxBreakdown{
		Income:      categoryTreasuryCredits(fin, finance.CatTaxIncome),
		Sales:       categoryTreasuryCredits(fin, finance.CatTaxSales),
		Corporation: categoryTreasuryCredits(fin, finance.CatTaxCorp),
		Rates:       categoryTreasuryCredits(fin, finance.CatTaxCouncil),
	}, nil
}

// LedgerMoneyIn is the budget inflow: finance.TaxRevenue() (the external
// producers are Provisional zero).
func (f *FiscalAPI) LedgerMoneyIn() (finance.Money, error) {
	if err := f.checkNotCopied("LedgerMoneyIn"); err != nil {
		return 0, err
	}
	fin, err := f.requireFinance("LedgerMoneyIn")
	if err != nil {
		return 0, err
	}
	return fin.TaxRevenue(), nil
}

// LedgerMoneyOut is the Sankey sink total: finance.ImportsTotal() plus
// finance.DebtServiceTotal() (leakage is Provisional zero). It is NOT the
// whole budget outflow - finance.BudgetBalance also subtracts maintenance,
// wages, materials, services opex, capex and construction; see
// LedgerOtherOutflows.
func (f *FiscalAPI) LedgerMoneyOut() (finance.Money, error) {
	if err := f.checkNotCopied("LedgerMoneyOut"); err != nil {
		return 0, err
	}
	fin, err := f.requireFinance("LedgerMoneyOut")
	if err != nil {
		return 0, err
	}
	out, _ := satAdd(fin.ImportsTotal(), fin.DebtServiceTotal())
	return out, nil
}

// LedgerBudgetBalance delegates to finance.BudgetBalance (A2).
func (f *FiscalAPI) LedgerBudgetBalance() (finance.Money, error) {
	if err := f.checkNotCopied("LedgerBudgetBalance"); err != nil {
		return 0, err
	}
	fin, err := f.requireFinance("LedgerBudgetBalance")
	if err != nil {
		return 0, err
	}
	return fin.BudgetBalance(), nil
}

// LedgerOtherOutflows is the budget outflow NOT shown as a Sankey sink:
// moneyIn - moneyOut - balance (saturating), i.e. the opex/capex/
// construction legs finance.BudgetBalance subtracts. It makes the published
// figures add up (moneyIn = moneyOut + otherOutflows + balance) without
// fabricating anything: it is a pure difference of ledger aggregates.
func (f *FiscalAPI) LedgerOtherOutflows() (finance.Money, error) {
	in, err := f.LedgerMoneyIn()
	if err != nil {
		return 0, err
	}
	out, err := f.LedgerMoneyOut()
	if err != nil {
		return 0, err
	}
	bal, err := f.LedgerBudgetBalance()
	if err != nil {
		return 0, err
	}
	return satSub(satSub(in, out), bal), nil
}

// LedgerTopology returns the whole-economy Sankey graph sourced entirely from
// the finance ledger (A1/A2): same fixed node/edge set and order as
// SankeyTopology, tax node = finance.TaxRevenue(), budget node =
// finance.BudgetBalance(). Provisional nodes carry amount 0.
func (f *FiscalAPI) LedgerTopology() (Topology, error) {
	if err := f.checkNotCopied("LedgerTopology"); err != nil {
		return Topology{}, err
	}
	fin, err := f.requireFinance("LedgerTopology")
	if err != nil {
		return Topology{}, err
	}
	moneyIn := fin.TaxRevenue()
	imports := fin.ImportsTotal()
	interest := fin.DebtServiceTotal()

	topo := Topology{
		Nodes: make([]SankeyNode, 0, 1+len(sourceNodeOrder)+len(sinkNodeOrder)),
		Edges: make([]SankeyEdge, 0, len(sourceNodeOrder)+len(sinkNodeOrder)),
	}
	topo.Nodes = append(topo.Nodes, SankeyNode{
		ID: NodeBudget, Label: "City budget", Kind: KindBudget, Amount: fin.BudgetBalance(),
	})
	for _, cat := range sourceNodeOrder {
		var amount finance.Money
		if cat == NodeTaxRevenue {
			amount = moneyIn
		}
		topo.Nodes = append(topo.Nodes, SankeyNode{
			ID: cat, Label: nodeLabel(cat), Kind: KindSource,
			Amount: amount, Provisional: isProvisional(cat),
		})
		topo.Edges = append(topo.Edges, SankeyEdge{From: cat, To: NodeBudget, Amount: amount})
	}
	for _, cat := range sinkNodeOrder {
		var amount finance.Money
		switch cat {
		case NodeImports:
			amount = imports
		case NodeInterest:
			amount = interest
		}
		topo.Nodes = append(topo.Nodes, SankeyNode{
			ID: cat, Label: nodeLabel(cat), Kind: KindSink,
			Amount: amount, Provisional: isProvisional(cat),
		})
		topo.Edges = append(topo.Edges, SankeyEdge{From: NodeBudget, To: cat, Amount: amount})
	}
	return topo, nil
}

// LedgerDrillThrough resolves a node to its finance ledger lines, with the
// tax node spanning ALL FOUR tax categories including tax.council (A3). Other
// nodes behave exactly as DrillThrough (Provisional -> empty).
func (f *FiscalAPI) LedgerDrillThrough(cat NodeCategory) ([]finance.Entry, error) {
	if err := f.checkNotCopied("LedgerDrillThrough"); err != nil {
		return nil, err
	}
	if cat != NodeTaxRevenue {
		return f.DrillThrough(cat)
	}
	fin, err := f.requireFinance("LedgerDrillThrough")
	if err != nil {
		return nil, err
	}
	out := []finance.Entry{}
	for _, c := range ledgerTaxCategories {
		out = append(out, fin.LinesByCategory(c)...)
	}
	return out, nil
}

// LedgerCivilServiceClawbackBp is the civil-service income-tax clawback at an
// explicit income-tax rate in basis points (the composition's own rate, the
// same fixed-point scale finance.CollectTax applies), for a composition
// where engine.tax is unwired. gross x bp / 10000.
func (f *FiscalAPI) LedgerCivilServiceClawbackBp(bp int64) (finance.Money, error) {
	if err := f.checkNotCopied("LedgerCivilServiceClawbackBp"); err != nil {
		return 0, err
	}
	if bp < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "incomeRateBp", "value": bp})
	}
	f.mu.RLock()
	gross := f.civilServiceWageBill
	f.mu.RUnlock()
	p, overflow := num.SafeMul(int64(gross), bp)
	if overflow {
		return 0, errs.New(ErrFiscalOverflow, f.correlationID, map[string]any{"field": "wageBill * rate", "amount": int64(gross)})
	}
	return finance.Money(p / basisPointScale), nil
}

// UnemploymentSupportPerPerson is the data-sourced monthly unemployment
// support per unemployed person (data/fiscal.json benefits, placeholder).
func (f *FiscalAPI) UnemploymentSupportPerPerson() finance.Money {
	if err := f.checkNotCopied("UnemploymentSupportPerPerson"); err != nil {
		return 0
	}
	return finance.Money(f.cfg.Benefits.UnemploymentSupportPerMonthMicroPounds)
}

// PostUnemploymentSupportFor posts one month's unemployment support for
// unemployed people: data amount x count, overflow-checked, through
// PostUnemploymentSupport (a balanced treasury -> households transfer under
// benefits.unemployment). count < 0 is refused; 0 posts nothing.
func (f *FiscalAPI) PostUnemploymentSupportFor(unemployed int64) (finance.TxID, error) {
	if err := f.checkNotCopied("PostUnemploymentSupportFor"); err != nil {
		return 0, err
	}
	if unemployed < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "unemployed", "value": unemployed})
	}
	total, overflow := num.SafeMul(f.cfg.Benefits.UnemploymentSupportPerMonthMicroPounds, unemployed)
	if overflow {
		return 0, errs.New(ErrFiscalOverflow, f.correlationID, map[string]any{"field": "unemploymentSupport * unemployed", "count": unemployed})
	}
	return f.PostUnemploymentSupport(finance.Money(total))
}

// State is the non-ledger-derivable fiscal input state (A5): the planning
// funding level, childcare places and the civil-service wage bill. Everything
// else the package reports is recomputed from the ledger.
type State struct {
	PlanningFunding      float64 `json:"planningFunding"`
	ChildcarePlaces      int64   `json:"childcarePlaces"`
	CivilServiceWageBill int64   `json:"civilServiceWageBill"`
}

// ExportState snapshots the non-derivable inputs for a save participant.
func (f *FiscalAPI) ExportState() State {
	if err := f.checkNotCopied("ExportState"); err != nil {
		return State{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return State{PlanningFunding: f.planningFunding, ChildcarePlaces: f.childcarePlaces, CivilServiceWageBill: int64(f.civilServiceWageBill)}
}

// ImportState restores the inputs, validating each by the same rules as the
// setters (fail closed: on any invalid field NOTHING is applied).
func (f *FiscalAPI) ImportState(s State) error {
	if err := f.checkNotCopied("ImportState"); err != nil {
		return err
	}
	if !num.IsFinite(s.PlanningFunding) || s.PlanningFunding < 0 || s.PlanningFunding > 1 {
		return errs.New(ErrInvalidFundingLevel, f.correlationID, map[string]any{"level": s.PlanningFunding})
	}
	if s.ChildcarePlaces < 0 || s.CivilServiceWageBill < 0 {
		return errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "state", "places": s.ChildcarePlaces, "bill": s.CivilServiceWageBill})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planningFunding = s.PlanningFunding
	f.childcarePlaces = s.ChildcarePlaces
	f.civilServiceWageBill = finance.Money(s.CivilServiceWageBill)
	return nil
}
