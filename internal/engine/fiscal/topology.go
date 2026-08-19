package fiscal

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// escapeControl renders s with every non-printable (control, e.g. ANSI
// escape/terminal-control) rune replaced by its Go-quoted escape form
// (SEC-151): Node/DrillThrough accept any caller-controlled NodeCategory
// string and echo it into an ErrUnknownCategory's "category" context value;
// errs.renderTemplate interpolates that value with a plain fmt.Sprint and
// performs no escaping of its own (no sanitizer exists in foundation/errs to
// reuse, so this helper lives here in the owning package rather than there),
// so a category string carrying raw control bytes would otherwise flow
// verbatim into a TUI/log error tail. unicode.IsPrint follows the same
// "printable" definition Go's %q/strconv.Quote use, so the escaped form is
// exactly what %q would show for that rune.
func escapeControl(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			sb.WriteRune(r)
			continue
		}
		sb.WriteString(strconv.QuoteRune(r))
	}
	return sb.String()
}

// The §54 money-in/money-out node ordering. These ordered slices — never a
// Go map — are the only iteration order this package uses for a monetary sum
// (GR#21, AC-11).
var (
	// sourceNodeOrder is the fixed money-in node order: the five external
	// producers (Provisional this sprint) then the tax inflow.
	sourceNodeOrder = []NodeCategory{
		NodeExports, NodeTourism, NodeOutCommuterWages, NodeFDI, NodeGrants,
		NodeTaxRevenue,
	}

	// sinkNodeOrder is the fixed money-out node order.
	sinkNodeOrder = []NodeCategory{NodeImports, NodeLeakage, NodeInterest}
)

// nodeMeta returns the display label and kind for a fixed node category, and
// whether the category is one of the fixed whole-economy categories.
func nodeMeta(cat NodeCategory) (label string, kind NodeKind, ok bool) {
	switch cat {
	case NodeBudget:
		return "City budget", KindBudget, true
	case NodeExports:
		return "Exports", KindSource, true
	case NodeTourism:
		return "Tourism", KindSource, true
	case NodeOutCommuterWages:
		return "Out-commuter wages", KindSource, true
	case NodeFDI:
		return "FDI", KindSource, true
	case NodeGrants:
		return "Central grants", KindSource, true
	case NodeTaxRevenue:
		return "Tax revenue", KindSource, true
	case NodeImports:
		return "Imports", KindSink, true
	case NodeLeakage:
		return "Leakage", KindSink, true
	case NodeInterest:
		return "Debt interest", KindSink, true
	default:
		return "", 0, false
	}
}

// isProvisional reports whether cat is an unbuilt-producer node this sprint:
// the §54 money-in producers engine.export/engine.tourism/engine.fdi and
// engine.defence grants, plus the leakage sink, none of which have a producer
// module yet. A Provisional node has amount zero and never fabricates a
// non-zero figure standing in for unbuilt data (AC-2).
func isProvisional(cat NodeCategory) bool {
	switch cat {
	case NodeExports, NodeTourism, NodeOutCommuterWages, NodeFDI, NodeGrants, NodeLeakage:
		return true
	default:
		return false
	}
}

// SankeyTopology returns the whole-economy Sankey graph (AC-1/AC-2): the
// fixed node/edge set with every amount recomputed at query time from the
// wired engine.finance ledger and engine.tax revenue. The graph is emitted in
// fixed, deterministic order (budget, then sources, then sinks; edges in the
// same order) — never Go map-iteration order (GR#21). It holds no cache: a
// second call after a ledger change returns the changed amounts (AC-9).
func (f *FiscalAPI) SankeyTopology() (Topology, error) {
	if err := f.checkNotCopied("SankeyTopology"); err != nil {
		return Topology{}, err
	}
	fin, err := f.requireFinance("SankeyTopology")
	if err != nil {
		return Topology{}, err
	}
	t, err := f.requireTax("SankeyTopology")
	if err != nil {
		return Topology{}, err
	}

	tb := taxBreakdownFrom(t)
	imports := fin.ImportsTotal()
	interest := fin.DebtServiceTotal()

	// moneyIn = tax inflow + the (zero) provisional external sources;
	// moneyOut = imports + interest + (zero) provisional leakage.
	moneyIn := tb.Total()
	moneyOut, _ := satAdd(imports, interest)
	balance := satSub(moneyIn, moneyOut)

	topo := Topology{
		Nodes: make([]SankeyNode, 0, 1+len(sourceNodeOrder)+len(sinkNodeOrder)),
		Edges: make([]SankeyEdge, 0, len(sourceNodeOrder)+len(sinkNodeOrder)),
	}

	topo.Nodes = append(topo.Nodes, SankeyNode{
		ID: NodeBudget, Label: "City budget", Kind: KindBudget, Amount: balance,
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

// nodeLabel is nodeMeta's label half, for the ordered-iteration call sites
// that already know the category is valid.
func nodeLabel(cat NodeCategory) string {
	label, _, _ := nodeMeta(cat)
	return label
}

// Node returns one Sankey node by category (AC-10's query half). A
// Provisional node is returned with amount zero without requiring any wired
// dependency; a real node requires the dependency that backs it. An unknown
// category is rejected with ErrUnknownCategory — never a zero-value node
// silently returned as if it were a real, built producer (AC-10).
func (f *FiscalAPI) Node(cat NodeCategory) (SankeyNode, error) {
	if err := f.checkNotCopied("Node"); err != nil {
		return SankeyNode{}, err
	}
	label, kind, ok := nodeMeta(cat)
	if !ok {
		return SankeyNode{}, errs.New(ErrUnknownCategory, f.correlationID, map[string]any{"category": escapeControl(string(cat))})
	}
	if isProvisional(cat) {
		return SankeyNode{ID: cat, Label: label, Kind: kind, Amount: 0, Provisional: true}, nil
	}

	var amount finance.Money
	switch cat {
	case NodeBudget:
		fin, err := f.requireFinance("Node")
		if err != nil {
			return SankeyNode{}, err
		}
		t, err := f.requireTax("Node")
		if err != nil {
			return SankeyNode{}, err
		}
		amount = budgetBalance(fin, t)
	case NodeImports:
		fin, err := f.requireFinance("Node")
		if err != nil {
			return SankeyNode{}, err
		}
		amount = fin.ImportsTotal()
	case NodeInterest:
		fin, err := f.requireFinance("Node")
		if err != nil {
			return SankeyNode{}, err
		}
		amount = fin.DebtServiceTotal()
	case NodeTaxRevenue:
		t, err := f.requireTax("Node")
		if err != nil {
			return SankeyNode{}, err
		}
		amount = taxBreakdownFrom(t).Total()
	}

	return SankeyNode{ID: cat, Label: label, Kind: kind, Amount: amount}, nil
}

// MoneyIn returns the budget's total inflow this tick: the tax revenue plus
// the (Provisional, zero) external producer inflows. Recomputed at query time
// from engine.finance/engine.tax — never a persisted accumulator (AC-9).
func (f *FiscalAPI) MoneyIn() (finance.Money, error) {
	if err := f.checkNotCopied("MoneyIn"); err != nil {
		return 0, err
	}
	t, err := f.requireTax("MoneyIn")
	if err != nil {
		return 0, err
	}
	return taxBreakdownFrom(t).Total(), nil
}

// MoneyOut returns the budget's total outflow this tick: imports plus debt
// interest (leakage is Provisional and zero). Recomputed at query time from
// engine.finance's ledger — never a persisted accumulator (AC-9).
func (f *FiscalAPI) MoneyOut() (finance.Money, error) {
	if err := f.checkNotCopied("MoneyOut"); err != nil {
		return 0, err
	}
	fin, err := f.requireFinance("MoneyOut")
	if err != nil {
		return 0, err
	}
	out, _ := satAdd(fin.ImportsTotal(), fin.DebtServiceTotal())
	return out, nil
}

// BudgetBalance returns the city budget balance this tick: money-in minus
// money-out, with saturating subtraction (GR#16). Recomputed at query time —
// there is no persisted budgetBalance field (AC-9).
func (f *FiscalAPI) BudgetBalance() (finance.Money, error) {
	if err := f.checkNotCopied("BudgetBalance"); err != nil {
		return 0, err
	}
	fin, err := f.requireFinance("BudgetBalance")
	if err != nil {
		return 0, err
	}
	t, err := f.requireTax("BudgetBalance")
	if err != nil {
		return 0, err
	}
	return budgetBalance(fin, t), nil
}

// budgetBalance computes money-in minus money-out from live dependency
// queries (the callers have already acquired the pointers).
func budgetBalance(fin *finance.FinanceAPI, t *tax.TaxAPI) finance.Money {
	moneyIn := taxBreakdownFrom(t).Total()
	moneyOut, _ := satAdd(fin.ImportsTotal(), fin.DebtServiceTotal())
	return satSub(moneyIn, moneyOut)
}

// DrillThrough resolves a node (or edge endpoint) category to engine.finance's
// actual ledger lines — reusing FinanceAPI's own Lines / LinesByCategory,
// never a second drill-through index built inside this package (AC-8). A
// Provisional node resolves to an empty line set (no producer module has
// posted a line — an honest zero, not a fabricated trace). An unknown
// category is rejected with ErrUnknownCategory.
func (f *FiscalAPI) DrillThrough(cat NodeCategory) ([]finance.Entry, error) {
	if err := f.checkNotCopied("DrillThrough"); err != nil {
		return nil, err
	}
	if _, _, ok := nodeMeta(cat); !ok {
		return nil, errs.New(ErrUnknownCategory, f.correlationID, map[string]any{"category": escapeControl(string(cat))})
	}
	if isProvisional(cat) {
		return []finance.Entry{}, nil
	}
	fin, err := f.requireFinance("DrillThrough")
	if err != nil {
		return nil, err
	}
	switch cat {
	case NodeBudget:
		return fin.Lines(finance.AcctTreasury), nil
	case NodeImports:
		return fin.LinesByCategory(finance.CatImports), nil
	case NodeInterest:
		return fin.LinesByCategory(finance.CatDebtService), nil
	case NodeTaxRevenue:
		// The budget's tax inflow spans finance's three tax categories; the
		// concatenation is in fixed order (GR#21).
		var out []finance.Entry
		for _, c := range financeTaxCategories {
			out = append(out, fin.LinesByCategory(c)...)
		}
		return out, nil
	default:
		return nil, errs.New(ErrUnknownCategory, f.correlationID, map[string]any{"category": escapeControl(string(cat))})
	}
}

// financeTaxCategories is the ordered set of finance categories the budget's
// tax inflow posts through (engine.finance's own taxCategories set).
var financeTaxCategories = []finance.Category{
	finance.CatTaxIncome, finance.CatTaxSales, finance.CatTaxCorp,
}

// The §54 tax-line ↔ engine.tax data-category mapping. The data-category
// strings are engine.tax's data enum values (data/tax_instruments.json); they
// are duplicated here only because engine.tax does not export them (the
// weakness-pattern-#2 shape, held by the drift test
// TestTaxCategoryMappingCoversLoadedInstruments).
const (
	dataCatIncome          = "income"
	dataCatConsumption     = "consumption"
	dataCatCorporateProfit = "corporateProfit"
	dataCatProperty        = "property"
	dataCatImport          = "import"
)

// taxLineForCategory maps an engine.tax instrument data category onto its
// §54 budget line, and reports whether the category is one of the five known
// categories. It is a structural mapping (data enum → spec-named line), not a
// numeric constant — the revenue figures come from TaxAPI (GR#15).
func taxLineForCategory(cat string) (taxLine, bool) {
	switch cat {
	case dataCatIncome:
		return lineIncome, true
	case dataCatConsumption:
		return lineSales, true
	case dataCatCorporateProfit:
		return lineCorporation, true
	case dataCatProperty:
		return lineRates, true
	case dataCatImport:
		return lineDuties, true
	default:
		return 0, false
	}
}

// taxLine is one of the five §54 budget-inflow tax lines.
type taxLine int

const (
	lineIncome taxLine = iota
	lineSales
	lineCorporation
	lineRates
	lineDuties
)

// taxBreakdownFrom buckets the wired engine.tax's per-instrument revenue into
// the five §54 tax lines by each instrument's data category (AC-3). Every
// figure is a live TaxAPI query — never a cached lump "tax revenue" figure.
// Unknown data categories (a future instrument category) are skipped in the
// breakdown but caught by the drift test, so the breakdown's Total always
// equals TaxAPI.RevenueTotal for the current data set.
func taxBreakdownFrom(t *tax.TaxAPI) TaxBreakdown {
	var b TaxBreakdown
	for _, info := range t.Instruments() {
		line, ok := taxLineForCategory(info.Category)
		if !ok {
			continue
		}
		switch line {
		case lineIncome:
			b.Income, _ = satAdd(b.Income, info.Revenue)
		case lineSales:
			b.Sales, _ = satAdd(b.Sales, info.Revenue)
		case lineCorporation:
			b.Corporation, _ = satAdd(b.Corporation, info.Revenue)
		case lineRates:
			b.Rates, _ = satAdd(b.Rates, info.Revenue)
		case lineDuties:
			b.Duties, _ = satAdd(b.Duties, info.Revenue)
		}
	}
	return b
}

// TaxBreakdown returns the per-type decomposition of the budget's tax inflow
// (AC-3): five lines summed from engine.tax's per-instrument revenue, whose
// total equals TaxAPI.RevenueTotal queried independently. Recomputed at query
// time.
func (f *FiscalAPI) TaxBreakdown() (TaxBreakdown, error) {
	if err := f.checkNotCopied("TaxBreakdown"); err != nil {
		return TaxBreakdown{}, err
	}
	t, err := f.requireTax("TaxBreakdown")
	if err != nil {
		return TaxBreakdown{}, err
	}
	return taxBreakdownFrom(t), nil
}
