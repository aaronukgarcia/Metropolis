package compose

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// BUG-1213 (FEAT-083 leg 3): the f2.finance P&L sub-view, derived from the ONE
// money ledger (FEAT-231: no second money model).
//
// The P&L is the TREASURY statement: income to the treasury account and
// outflows from it, every row a real posting to/from AcctTreasury, with a Net
// line that is the month's treasury change exactly. Household consumption and
// total wages are CITY ECONOMY figures (household-to-firm and all-employer
// volumes); they are published in their own block outside the net so nobody
// reads them as treasury money.
//
// The statement for ledger month M is read from the ledger's append-only
// transaction log (FinanceAPI.MonthTransactions) once month M is complete,
// i.e. at the finance close of month M+1. The per-tick window
// (Lines/LinesByCategory) is NOT used: BeginMonth discards it, so everything
// posted between two finance ticks (coastal bills in the population phase,
// social support, daily construction) was invisible to a window read and
// showed up as an unexplained gap (the r1 "Coastal/other costs" row). Being a
// pure function of the persisted log, the statement is identical in an
// uninterrupted run and after Save + Load. It posts nothing and never feeds
// back into the simulation.

// plRow is one P&L row: a label and the ledger categories whose
// treasury-account postings (on the row's side) it sums. Slices (never maps)
// so row order is stable (GR#21).
type plRow struct {
	label string
	cats  []finance.Category
}

// plIncomeRows are the treasury INCOME rows (credits to the treasury account).
var plIncomeRows = []plRow{
	{"Income tax", []finance.Category{finance.CatTaxIncome}},
	{"Sales tax", []finance.Category{finance.CatTaxSales}},
	{"Corporation tax", []finance.Category{finance.CatTaxCorp}},
	{"Council tax", []finance.Category{finance.CatTaxCouncil}},
	{"Grants", []finance.Category{finance.CatGrants}},
	{"Loans drawn", []finance.Category{finance.CatLoan}},
	{"Opening capital (one-off)", []finance.Category{finance.Category("opening.capital")}},
}

// plOutflowRows are the treasury OUTFLOW rows (debits from the treasury
// account). The coastal rows are the engine.coastal cost lines
// (coastal_wire.go postCoastalCosts) under their own ledger categories.
var plOutflowRows = []plRow{
	{"Public sector wages", []finance.Category{finance.CatWages}},
	{"Benefits", []finance.Category{"benefits.unemployment", "benefits.housing"}},
	{"Social support", []finance.Category{finCatSocialSupport}},
	{"Upkeep and services", []finance.Category{finance.CatOpex, finance.CatMaintenance, finance.CatMaterials, finance.CatStaffWages, finance.CatCapex}},
	{"Construction", []finance.Category{finance.CatConstruction}},
	{"Debt service", []finance.Category{finance.CatDebtService, finance.CatDebtPrincipal}},
	{"Imports", []finance.Category{finance.CatImports}},
	{"Coastal hotels", []finance.Category{finCatCoastalHotels}},
	{"Coastal departure", []finance.Category{finCatCoastalDeparture}},
	{"Coastal processing", []finance.Category{finCatCoastalProcessing}},
	{"Coastal integration", []finance.Category{finCatCoastalIntegration}},
	{"Reserves and investment", []finance.Category{finance.CatReserveInterest, finance.CatReserveDeposit, finance.CatInvestment}},
}

// plResidueLabel names the signed row for treasury postings whose category is
// not itemised above (never a plug: it is the sum of real, uncategorised
// treasury postings, positive or negative).
const plResidueLabel = "Other treasury movements (not itemised)"

// Fixed labels the F2 frame tests read.
const (
	plNetLabel         = "Net treasury change"
	plConsumptionLabel = "Household consumption"
	plCityWagesLabel   = "Wages paid (all employers)"
)

// financePLSnapshot is one completed ledger month's treasury statement.
type financePLSnapshot struct {
	month       int64   // ledger month (0 = the opening month)
	income      []int64 // aligned with plIncomeRows
	outflow     []int64 // aligned with plOutflowRows (positive = money out)
	residue     int64   // signed: uncategorised treasury movements, + in / - out
	net         int64   // signed treasury change: income - outflow + residue
	consumption int64   // city economy: household -> firm spend volume
	cityWages   int64   // city economy: wages paid by every employer
}

// financePLHistory holds the newest closed statement and the one before it.
type financePLHistory struct {
	latest, prev *financePLSnapshot
}

// plRowIndex returns the index of the row listing cat, or -1.
func plRowIndex(rows []plRow, cat finance.Category) int {
	for i, r := range rows {
		for _, c := range r.cats {
			if c == cat {
				return i
			}
		}
	}
	return -1
}

// plBuildSnapshot aggregates one ledger month's transactions into a
// statement. A plain function over its inputs so tests can drive it directly.
func plBuildSnapshot(month int64, txns []finance.Transaction) *financePLSnapshot {
	s := &financePLSnapshot{
		month:   month,
		income:  make([]int64, len(plIncomeRows)),
		outflow: make([]int64, len(plOutflowRows)),
	}
	for _, tx := range txns {
		for _, e := range tx.Entries {
			amt := int64(e.Amount)
			// City economy (outside the net): volumes, not treasury money.
			if e.Category == finance.CatSpend && e.Account == finance.AcctHouseholds && e.Side == finance.SideDebit {
				s.consumption = num.SatAdd(s.consumption, amt)
			}
			if e.Category == finance.CatWages && e.Side == finance.SideDebit {
				s.cityWages = num.SatAdd(s.cityWages, amt)
			}
			if e.Account != finance.AcctTreasury {
				continue
			}
			if e.Side == finance.SideCredit {
				if i := plRowIndex(plIncomeRows, e.Category); i >= 0 {
					s.income[i] = num.SatAdd(s.income[i], amt)
				} else {
					s.residue = num.SatAdd(s.residue, amt)
				}
			} else {
				if i := plRowIndex(plOutflowRows, e.Category); i >= 0 {
					s.outflow[i] = num.SatAdd(s.outflow[i], amt)
				} else {
					s.residue = num.SatSub(s.residue, amt)
				}
			}
		}
	}
	net := s.residue
	for _, v := range s.income {
		net = num.SatAdd(net, v)
	}
	for _, v := range s.outflow {
		net = num.SatSub(net, v)
	}
	s.net = net
	return s
}

// plCloseMonth runs at the end of every monthly fiscal step. The ledger month
// that has just completed is the one BEFORE the month finance opened this
// tick (Month()-1); its statement is built from the transaction log and
// published as the newest snapshot. The previous column is kept only when it
// is the immediately preceding ledger month of THIS run (a fresh Composition
// after a load has none until its second close).
func plCloseMonth(st *simState) {
	if st.finance == nil {
		return
	}
	done := st.finance.Month() - 1
	if done < 0 {
		return
	}
	snap := plBuildSnapshot(done, st.finance.MonthTransactions(done))
	next := &financePLHistory{latest: snap}
	if old := st.plPub.Load(); old != nil && old.latest != nil && old.latest.month == done-1 {
		next.prev = old.latest
	}
	st.plPub.Store(next)
}

// plPubHolder is the publish-only mirror type (atomic pointer to an immutable
// history, safe to read from the subscription pump goroutine).
type plPubHolder = atomic.Pointer[financePLHistory]

// financePLItem mirrors ui.screen.finance's wirePLItem field-for-field.
type financePLItem struct {
	Label                string `json:"label"`
	ValueMicropounds     int64  `json:"valueMicropounds"`
	PrevValueMicropounds *int64 `json:"prevValueMicropounds,omitempty"`
}

// financePLView mirrors ui.screen.finance's wirePLView field-for-field.
type financePLView struct {
	Period      string          `json:"period"`
	Revenues    []financePLItem `json:"revenues"`
	Expenses    []financePLItem `json:"expenses"`
	Other       *financePLItem  `json:"other,omitempty"`
	Net         *financePLItem  `json:"net,omitempty"`
	CityEconomy []financePLItem `json:"cityEconomy,omitempty"`
}

// plLabelMonth is the 1-based month number shown to the player (ledger month
// 0 is "Month 1").
func plLabelMonth(ledgerMonth int64) string { return "Month " + itoa64(uint64(ledgerMonth+1)) }

// buildFinancePL renders the published history as the wire P&L; nil until the
// first month has completed. Rows that are zero this month and last month are
// omitted so the statement stays short; order is the fixed table order.
func buildFinancePL(st *simState) *financePLView {
	h := st.plPub.Load()
	if h == nil || h.latest == nil {
		return nil
	}
	cur, prev := h.latest, h.prev
	view := &financePLView{
		Period:   plLabelMonth(cur.month),
		Revenues: []financePLItem{},
		Expenses: []financePLItem{},
	}
	if prev != nil {
		view.Period += " vs " + plLabelMonth(prev.month)
	}
	item := func(label string, c int64, p int64) financePLItem {
		it := financePLItem{Label: label, ValueMicropounds: c}
		if prev != nil {
			v := p
			it.PrevValueMicropounds = &v
		}
		return it
	}
	prevAt := func(pick func(*financePLSnapshot) []int64, i int) int64 {
		if prev == nil {
			return 0
		}
		return pick(prev)[i]
	}
	inc := func(s *financePLSnapshot) []int64 { return s.income }
	out := func(s *financePLSnapshot) []int64 { return s.outflow }
	for i, r := range plIncomeRows {
		c, p := cur.income[i], prevAt(inc, i)
		if c != 0 || p != 0 {
			view.Revenues = append(view.Revenues, item(r.label, c, p))
		}
	}
	for i, r := range plOutflowRows {
		c, p := cur.outflow[i], prevAt(out, i)
		if c != 0 || p != 0 {
			view.Expenses = append(view.Expenses, item(r.label, c, p))
		}
	}
	var pr, pn, pc, pw int64
	if prev != nil {
		pr, pn, pc, pw = prev.residue, prev.net, prev.consumption, prev.cityWages
	}
	if cur.residue != 0 || pr != 0 {
		o := item(plResidueLabel, cur.residue, pr)
		view.Other = &o
	}
	n := item(plNetLabel, cur.net, pn)
	view.Net = &n
	view.CityEconomy = []financePLItem{
		item(plConsumptionLabel, cur.consumption, pc),
		item(plCityWagesLabel, cur.cityWages, pw),
	}
	return view
}
