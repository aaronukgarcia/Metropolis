package finance

// FEAT-233 (FEAT-1972079848, "Sankey fiscal flow view fed by
// finance.FlowMatrix query seam"): the month-close money in/out matrix the
// fiscal Sankey view publishes.
//
// The decomposition follows ASM-1220's definition exactly:
//
//   - money-IN is the BUDGET INFLOW only: the tax revenue the current
//     month-close window collected, decomposed per tax category. The
//     external producer inflows (exports/tourism/out-commuter wages/FDI/
//     grants) are Provisional this sprint — their producer modules have not
//     landed, so they post no ledger lines and appear here as nothing rather
//     than as a fabricated figure (AC-2 discipline, mirrored from
//     engine.fiscal's topology).
//   - money-OUT is the EXTERNAL OUTFLOW only: imports (CatImports) and debt
//     interest (CatDebtService) paid away to AcctExternal. Leakage is
//     Provisional and zero (no category exists for it yet).
//   - opex, wages, construction and household spend are INTERNAL
//     REDISTRIBUTION (money moving between the city's own RoleMoney
//     accounts) and are deliberately NOT rows of this matrix — including
//     them would double-count flows the budget-balance reading must not
//     see (ASM-1220).
//
// Like every aggregate in this package, each row is a sum over retrievable
// ledger lines (AC-11 drill-through: LinesByCategory(cat) within the same
// BeginMonth window returns exactly the entries each row sums), computed at
// query time from the current tick's transaction log — never a cached or
// persisted accumulator.

// FlowLine is one row of the FlowMatrix: a flow category and its
// month-close amount.
type FlowLine struct {
	Category Category `json:"category"`
	Amount   Money    `json:"amountMicropounds"`
}

// FlowMatrix is FEAT-233's month-close money in/out decomposition
// (ASM-1220 semantics). Inflows and ExternalOutflows are emitted in fixed,
// deterministic order (the ordered category slices below — GR#21, never Go
// map iteration); Totals are saturating sums over those rows (GR#16).
type FlowMatrix struct {
	// Month is the simulation month the matrix window belongs to (the last
	// BeginMonth argument).
	Month int64 `json:"month"`

	// Inflows is the budget-inflow side: one row per tax category, in
	// taxCategories' fixed order. External producer inflows are Provisional
	// (zero, absent until their modules post lines).
	Inflows []FlowLine `json:"inflows"`

	// ExternalOutflows is the money-out side: imports then debt interest,
	// in externalOutflowCategories' fixed order. Leakage is Provisional.
	ExternalOutflows []FlowLine `json:"externalOutflows"`

	// TotalIn / TotalOut are the saturating sums over Inflows /
	// ExternalOutflows respectively (ASM-1220's money-in and money-out).
	TotalIn  Money `json:"totalInMicropounds"`
	TotalOut Money `json:"totalOutMicropounds"`
}

// externalOutflowCategories is the fixed money-out order per ASM-1220:
// imports then debt interest. Ordered slice, never a map (GR#21). Leakage
// has no ledger category yet — it is Provisional zero and deliberately not
// listed, so an honest absence stays distinguishable from a real zero sum.
var externalOutflowCategories = []Category{CatImports, CatDebtService}

// FlowMatrix returns the current month-close window's money in/out matrix
// per ASM-1220 (see this file's type docs). The window is FinanceAPI's
// tick scope — everything posted since the last BeginMonth call — so a
// caller polling monthly reads exactly one month's close. Safe for
// concurrent use; computed from one consistent snapshot under f.mu.
func (f *FinanceAPI) FlowMatrix() FlowMatrix {
	if err := f.checkNotCopied("FlowMatrix"); err != nil {
		return FlowMatrix{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	m := FlowMatrix{
		Month:            f.month,
		Inflows:          make([]FlowLine, 0, len(taxCategories)),
		ExternalOutflows: make([]FlowLine, 0, len(externalOutflowCategories)),
	}

	for _, cat := range taxCategories {
		var total Money
		for _, e := range f.linesLocked(AcctTreasury) {
			if e.Side == SideCredit && e.Category == cat {
				total, _ = satAddMoney(total, e.Amount)
			}
		}
		m.Inflows = append(m.Inflows, FlowLine{Category: cat, Amount: total})
		m.TotalIn, _ = satAddMoney(m.TotalIn, total)
	}

	for _, cat := range externalOutflowCategories {
		amount := f.treasuryDebit(cat)
		m.ExternalOutflows = append(m.ExternalOutflows, FlowLine{Category: cat, Amount: amount})
		m.TotalOut, _ = satAddMoney(m.TotalOut, amount)
	}

	return m
}
