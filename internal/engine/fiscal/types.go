package fiscal

import "github.com/aaronukgarcia/Metropolis/internal/engine/finance"

// NodeCategory names one node in the whole-economy Sankey topology. It is a
// fixed, closed enum (the §54 money-in/money-out/budget/tax categories); an
// unknown category is rejected with ErrUnknownCategory (AC-10), never
// normalised or silently treated as a real producer.
type NodeCategory string

// The fixed whole-economy Sankey categories (§54). Ordered slices derived
// from these are the only iteration order this package ever uses for a
// monetary sum (GR#21) — never a Go map.
const (
	// NodeBudget is the central node: the city budget (AcctTreasury).
	NodeBudget NodeCategory = "budget"

	// Money-in source nodes (external producers → budget). exports, tourism,
	// out-commuter wages, FDI and grants are Provisional this sprint (their
	// producer modules are later sprints); their amount is zero until they
	// land.
	NodeExports          NodeCategory = "exports"
	NodeTourism          NodeCategory = "tourism"
	NodeOutCommuterWages NodeCategory = "outCommuterWages"
	NodeFDI              NodeCategory = "fdi"
	NodeGrants           NodeCategory = "grants"

	// NodeTaxRevenue is the budget-inflow side's tax inflow: the sum of the
	// per-type tax revenue [TaxBreakdown] decomposes.
	NodeTaxRevenue NodeCategory = "taxRevenue"

	// Money-out sink nodes (budget → external world). imports and interest
	// are real (finance's CatImports / CatDebtService); leakage is
	// Provisional.
	NodeImports  NodeCategory = "imports"
	NodeLeakage  NodeCategory = "leakage"
	NodeInterest NodeCategory = "interest"
)

// NodeKind tags a Sankey node as a money source, a money sink, or the
// central budget node (AC-1's "category tag — source/sink/budget").
type NodeKind uint8

const (
	// KindSource is a money-in node feeding the budget.
	KindSource NodeKind = iota
	// KindSink is a money-out node draining the budget.
	KindSink
	// KindBudget is the central budget node.
	KindBudget
)

// SankeyNode is one node of the whole-economy Sankey topology (AC-1/AC-2):
// a fixed category, a display label, its kind, the recomputed money amount,
// and the Provisional flag marking an unbuilt-producer placeholder.
type SankeyNode struct {
	ID          NodeCategory
	Label       string
	Kind        NodeKind
	Amount      finance.Money
	Provisional bool
}

// SankeyEdge is one directed flow of the topology: from a source node to the
// budget, or from the budget to a sink node. Its amount equals the
// non-budget endpoint's node amount (one ledger-derived figure, one view).
type SankeyEdge struct {
	From   NodeCategory
	To     NodeCategory
	Amount finance.Money
}

// Topology is the whole-economy Sankey graph (AC-1): ordered node and edge
// slices (fixed order, GR#21), recomputed at query time from
// engine.finance/engine.tax (never cached — AC-9).
type Topology struct {
	Nodes []SankeyNode
	Edges []SankeyEdge
}

// TaxBreakdown is the per-type decomposition of the budget's tax inflow
// (AC-3): the five §54 tax lines, each a sum over engine.tax's per-instrument
// revenue in that category. It is recomputed at query time from
// TaxAPI.Instruments — never a cached lump "tax revenue" figure.
type TaxBreakdown struct {
	Income      finance.Money // income tax (data category "income")
	Sales       finance.Money // sales tax / VAT share ("consumption")
	Corporation finance.Money // corporation tax ("corporateProfit")
	Rates       finance.Money // rates & council tax ("property")
	Duties      finance.Money // duties & levies ("import")
}

// Total sums the five lines with saturating addition (GR#16).
func (b TaxBreakdown) Total() finance.Money {
	t, _ := satAdd(b.Income, b.Sales)
	t, _ = satAdd(t, b.Corporation)
	t, _ = satAdd(t, b.Rates)
	t, _ = satAdd(t, b.Duties)
	return t
}

// ChildcareNetLine is §54's "childcare subsidy shown as a net line" (AC-6):
// the gross subsidy spend, the income-tax yield the subsidy unlocks via
// higher second-earner participation, and the net, as three distinct
// queryable values — never a single number the player has to trust is
// "mostly self-funding". Net = max(0, GrossSpend − TaxYield) (SEC-149,
// GR#16 money-is-never-negative): when TaxYield exceeds GrossSpend the line
// clamps to zero rather than going negative, and that surplus is not
// redistributed anywhere — callers must not reconstruct GrossSpend from
// Net + TaxYield.
type ChildcareNetLine struct {
	GrossSpend finance.Money
	TaxYield   finance.Money
	Net        finance.Money
}
