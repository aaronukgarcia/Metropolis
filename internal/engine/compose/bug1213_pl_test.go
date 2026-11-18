package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uifinance "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// BUG-1213 (FEAT-083 leg 3): the f2.finance P&L is the TREASURY statement
// derived from the one money ledger. Net equals the actual treasury change
// exactly, every itemised row equals an independent sum of treasury-account
// postings, household consumption sits in a separate City economy block, and
// a Save + Load rebuilds the same figures as an uninterrupted run.

// plDecode runs the real publish function and decodes it through the real UI
// screen, returning the screen's P&L view.
func plDecode(t *testing.T, st *simState) (uifinance.PLView, bool) {
	t.Helper()
	raw, err := st.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}
	scr := uifinance.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-pl")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: raw})
	return scr.PL()
}

func plRowOf(items []uifinance.PLItem, label string) (uifinance.PLItem, bool) {
	for _, it := range items {
		if it.Label == label {
			return it, true
		}
	}
	return uifinance.PLItem{}, false
}

// plCityRow finds a City economy row by label.
func plCityRow(t *testing.T, v uifinance.PLView, label string) uifinance.PLItem {
	t.Helper()
	it, ok := plRowOf(v.CityEconomy, label)
	if !ok {
		t.Fatalf("City economy block has no %q row: %+v", label, v.CityEconomy)
	}
	return it
}

// plMonthRecord is one closed month as the test observed it from OUTSIDE the
// statement code: the published view plus the treasury change measured
// tick by tick.
type plMonthRecord struct {
	ledgerMonth   int64
	view          uifinance.PLView
	actualDelta   int64 // treasury balance change over the ledger month
	monthTxns     []finance.Transaction
	treasuryAtEnd int64
}

// plTreasuryWindowNet is the net effect of the CURRENT ledger window's
// postings on the treasury account (credits up, debits down).
func plTreasuryWindowNet(st *simState) int64 {
	var net int64
	for _, e := range st.finance.Lines(finance.AcctTreasury) {
		if e.Side == finance.SideCredit {
			net += int64(e.Amount)
		} else {
			net -= int64(e.Amount)
		}
	}
	return net
}

// plRunMonths advances a fresh default composition one tick at a time. Every
// time the ledger month rolls over (BeginMonth ran this tick) it records the
// P&L the screen now shows (the statement of the month that just ended) and
// that month's treasury change, measured independently: the treasury balance
// at the roll-over tick minus the window postings made since BeginMonth is
// the balance at the end of the closed month.
func plRunMonths(t *testing.T, months int) []plMonthRecord {
	t.Helper()
	e, comp := newTestEngineSeed(t, 42)
	st := comp.state
	var recs []plMonthRecord
	endBal := int64(0) // treasury before the opening capital was posted
	lastMonth := st.finance.Month()
	for tick := 0; tick < 30*months+30 && len(recs) < months; tick++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
		m := st.finance.Month()
		if m == lastMonth {
			continue
		}
		bal := ledgerBalance(st.finance, finance.AcctTreasury)
		closedEnd := bal - plTreasuryWindowNet(st)
		view, have := plDecode(t, st)
		if !have {
			t.Fatalf("no P&L published at the close of ledger month %d", lastMonth)
		}
		recs = append(recs, plMonthRecord{
			ledgerMonth:   lastMonth,
			view:          view,
			actualDelta:   closedEnd - endBal,
			monthTxns:     st.finance.MonthTransactions(lastMonth),
			treasuryAtEnd: closedEnd,
		})
		endBal = closedEnd
		lastMonth = m
	}
	if len(recs) < months {
		t.Fatalf("only %d month closes in the run, want %d", len(recs), months)
	}
	return recs
}

// RED-PROOF target: dropping a category from the net makes this fail.
func TestBUG1213_PL_NetEqualsTreasuryDelta_Months1to6(t *testing.T) {
	recs := plRunMonths(t, 6)
	for _, r := range recs {
		if r.view.Net == nil {
			t.Fatalf("ledger month %d: no Net line", r.ledgerMonth)
		}
		if r.view.Net.ValueMicropounds != r.actualDelta {
			t.Errorf("ledger month %d: Net = %d, actual treasury change = %d", r.ledgerMonth, r.view.Net.ValueMicropounds, r.actualDelta)
		}
		// And the displayed rows add up to the Net (income - outflow + other).
		var sum int64
		for _, it := range r.view.Revenues {
			sum += it.ValueMicropounds
		}
		for _, it := range r.view.Expenses {
			sum -= it.ValueMicropounds
		}
		if r.view.Other != nil {
			sum += r.view.Other.ValueMicropounds
		}
		if sum != r.view.Net.ValueMicropounds {
			t.Errorf("ledger month %d: rows sum to %d but Net shows %d", r.ledgerMonth, sum, r.view.Net.ValueMicropounds)
		}
	}
}

// plIndependentRows maps each shown label to the ledger categories it must
// equal, written out here rather than read from the production row tables.
var plIndependentIncome = map[string][]finance.Category{
	"Income tax":                {"tax.income"},
	"Sales tax":                 {"tax.sales"},
	"Corporation tax":           {"tax.corp"},
	"Council tax":               {"tax.council"},
	"Opening capital (one-off)": {"opening.capital"},
}

var plIndependentOutflow = map[string][]finance.Category{
	"Public sector wages": {"wages"},
	"Benefits":            {"benefits.unemployment", "benefits.housing"},
	"Social support":      {"social.support"},
	"Coastal processing":  {"coastal.processing"},
	"Coastal integration": {"coastal.integration"},
	"Coastal hotels":      {"coastal.hotels"},
	"Coastal departure":   {"coastal.departure"},
}

func plTreasurySum(txns []finance.Transaction, side finance.Side, cats []finance.Category) int64 {
	var total int64
	for _, tx := range txns {
		for _, e := range tx.Entries {
			if e.Account != finance.AcctTreasury || e.Side != side {
				continue
			}
			for _, c := range cats {
				if e.Category == c {
					total += int64(e.Amount)
				}
			}
		}
	}
	return total
}

func TestBUG1213_PL_EveryRowEqualsIndependentTreasurySum(t *testing.T) {
	recs := plRunMonths(t, 6)
	named := map[string]bool{}
	for _, r := range recs {
		for label, cats := range plIndependentIncome {
			want := plTreasurySum(r.monthTxns, finance.SideCredit, cats)
			got, ok := plRowOf(r.view.Revenues, label)
			if want == 0 {
				if ok && got.ValueMicropounds != 0 {
					t.Errorf("month %d income %q shown %d, independent sum 0", r.ledgerMonth, label, got.ValueMicropounds)
				}
				continue
			}
			if !ok || got.ValueMicropounds != want {
				t.Errorf("month %d income %q shown %v, independent treasury sum %d", r.ledgerMonth, label, got, want)
			}
			named[label] = true
		}
		for label, cats := range plIndependentOutflow {
			want := plTreasurySum(r.monthTxns, finance.SideDebit, cats)
			got, ok := plRowOf(r.view.Expenses, label)
			if want == 0 {
				if ok && got.ValueMicropounds != 0 {
					t.Errorf("month %d outflow %q shown %d, independent sum 0", r.ledgerMonth, label, got.ValueMicropounds)
				}
				continue
			}
			if !ok || got.ValueMicropounds != want {
				t.Errorf("month %d outflow %q shown %v, independent treasury sum %d", r.ledgerMonth, label, got, want)
			}
			named[label] = true
		}
		// The default composition posts only categories the table itemises:
		// no residue row is expected naturally.
		if r.view.Other != nil {
			t.Errorf("month %d: unexpected 'not itemised' row %+v on the default composition", r.ledgerMonth, *r.view.Other)
		}
	}
	// The large early outflow is the coastal bill; it must be a named row.
	for _, label := range []string{"Coastal processing", "Coastal integration", "Income tax", "Public sector wages", "Social support"} {
		if !named[label] {
			t.Errorf("row %q never appeared with a non-zero value in months 1-6", label)
		}
	}
}

// RED-PROOF target: dropping the consumption row fails here.
func TestBUG1213_PL_CityEconomyConsumptionMatchesLedgerAndMoves(t *testing.T) {
	recs := plRunMonths(t, 4)
	var seen []int64
	for _, r := range recs {
		var spend, wages int64
		for _, tx := range r.monthTxns {
			for _, e := range tx.Entries {
				if e.Category == finance.CatSpend && e.Account == finance.AcctHouseholds && e.Side == finance.SideDebit {
					spend += int64(e.Amount)
				}
				if e.Category == finance.CatWages && e.Side == finance.SideDebit {
					wages += int64(e.Amount)
				}
			}
		}
		c := plCityRow(t, r.view, "Household consumption")
		if c.ValueMicropounds != spend {
			t.Errorf("month %d consumption = %d, independent household spend sum = %d", r.ledgerMonth, c.ValueMicropounds, spend)
		}
		w := plCityRow(t, r.view, "Wages paid (all employers)")
		if w.ValueMicropounds != wages {
			t.Errorf("month %d city wages = %d, independent wages sum = %d", r.ledgerMonth, w.ValueMicropounds, wages)
		}
		// Consumption is NOT treasury money: it is never one of the treasury rows.
		if _, ok := plRowOf(r.view.Revenues, "Household consumption"); ok {
			t.Errorf("month %d: Household consumption leaked into the treasury income rows", r.ledgerMonth)
		}
		seen = append(seen, c.ValueMicropounds)
	}
	moved := false
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[i-1] {
			moved = true
		}
	}
	if !moved || seen[len(seen)-1] == 0 {
		t.Fatalf("consumption did not move between closes (or is zero): %v", seen)
	}
	// Previous column: absent on the first close, the first month's figure on the second.
	if c := plCityRow(t, recs[0].view, "Household consumption"); c.PrevValueMicropounds != nil {
		t.Errorf("first close must have no previous-month figure, got %d", *c.PrevValueMicropounds)
	}
	c1 := plCityRow(t, recs[1].view, "Household consumption")
	if c1.PrevValueMicropounds == nil || *c1.PrevValueMicropounds != seen[0] {
		t.Errorf("second close previous figure = %v, want first month's %d", c1.PrevValueMicropounds, seen[0])
	}
}

// A residue of either sign renders. It cannot occur on the default
// composition (every category it posts is itemised), so the cases are built
// directly from ledger transactions with an unitemised category.
func TestBUG1213_PL_ResidueShownWhetherPositiveOrNegative(t *testing.T) {
	mk := func(side finance.Side, amt finance.Money) []finance.Transaction {
		other := finance.AcctExternal
		oside := finance.SideDebit
		if side == finance.SideDebit {
			oside = finance.SideCredit
		}
		return []finance.Transaction{{Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: side, Amount: amt, Category: "zz.unitemised"},
			{Account: other, Side: oside, Amount: amt, Category: "zz.unitemised"},
		}}}
	}
	for _, tc := range []struct {
		name string
		side finance.Side
		want int64
	}{
		{"negative", finance.SideDebit, -7_000_000},
		{"positive", finance.SideCredit, 7_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &simState{}
			snap := plBuildSnapshot(3, mk(tc.side, 7_000_000))
			if snap.net != tc.want {
				t.Fatalf("net = %d, want %d", snap.net, tc.want)
			}
			st.plPub.Store(&financePLHistory{latest: snap})
			v, have := plDecode2(t, st)
			if !have || v.Other == nil {
				t.Fatalf("no 'not itemised' row: have=%v view=%+v", have, v)
			}
			if v.Other.Label != "Other treasury movements (not itemised)" || v.Other.ValueMicropounds != tc.want {
				t.Fatalf("row = %+v, want label 'Other treasury movements (not itemised)' value %d", *v.Other, tc.want)
			}
			if v.Net == nil || v.Net.ValueMicropounds != tc.want {
				t.Fatalf("net line = %+v, want %d", v.Net, tc.want)
			}
		})
	}
}

// plDecode2 is plDecode for a bare simState that only has a P&L history
// (no finance ledger): it decodes the patch's "pl" section directly.
func plDecode2(t *testing.T, st *simState) (uifinance.PLView, bool) {
	t.Helper()
	view := buildFinancePL(st)
	if view == nil {
		return uifinance.PLView{}, false
	}
	patch := struct {
		SchemaVersion int            `json:"schemaVersion"`
		PL            *financePLView `json:"pl"`
	}{SchemaVersion: financeWireSchemaVersion, PL: view}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	scr := uifinance.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-pl2")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: raw})
	return scr.PL()
}

// RED-PROOF target: reporting the wrong ledger month after a load fails here.
// (A Load into an already-run composition is impossible: the engine seals at
// its first AdvanceTicks, so the plPub reset in Load is defensive only.)
func TestBUG1213_PL_FirstCloseAfterLoadMatchesUninterruptedControl(t *testing.T) {
	eA, a := newTestEngineSeed(t, 42)
	if err := eA.AdvanceTicks(errs.NewCorrelationID(), 90); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	clk, err := eA.Clock()
	if err != nil {
		t.Fatal(err)
	}
	if err := eA.AdvanceTicks(errs.NewCorrelationID(), 30); err != nil {
		t.Fatal(err)
	}
	ctl, haveCtl := plDecode(t, a.state)
	if !haveCtl || ctl.Net == nil {
		t.Fatalf("control has no P&L: %+v", ctl)
	}

	eB, b := newTestEngineSeed(t, 42)
	if err := b.LoadAt(dir, clk.Tick()); err != nil {
		t.Fatal(err)
	}
	if v, have := plDecode(t, b.state); have {
		t.Fatalf("a freshly loaded composition shows a P&L before its first close: %+v", v)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), 30); err != nil {
		t.Fatal(err)
	}
	got, have := plDecode(t, b.state)
	if !have || got.Net == nil {
		t.Fatalf("no P&L at the first close after the load: %+v", got)
	}
	if got.Net.ValueMicropounds != ctl.Net.ValueMicropounds {
		t.Errorf("Net after load = %d, control = %d", got.Net.ValueMicropounds, ctl.Net.ValueMicropounds)
	}
	nonZero := func(in []uifinance.PLItem) []uifinance.PLItem {
		var out []uifinance.PLItem
		for _, it := range in {
			if it.ValueMicropounds != 0 {
				out = append(out, it)
			}
		}
		return out
	}
	same := func(label string, g, c []uifinance.PLItem) {
		g, c = nonZero(g), nonZero(c)
		if len(g) != len(c) {
			t.Errorf("%s rows after load %v differ from control %v", label, g, c)
			return
		}
		for i := range g {
			if g[i].Label != c[i].Label || g[i].ValueMicropounds != c[i].ValueMicropounds {
				t.Errorf("%s row %d after load = %s %d, control = %s %d", label, i, g[i].Label, g[i].ValueMicropounds, c[i].Label, c[i].ValueMicropounds)
			}
		}
	}
	same("income", got.Revenues, ctl.Revenues)
	same("outflow", got.Expenses, ctl.Expenses)
	same("city", got.CityEconomy, ctl.CityEconomy)
	if (got.Other == nil) != (ctl.Other == nil) {
		t.Errorf("residue row presence after load %v vs control %v", got.Other, ctl.Other)
	}
	// Nothing invented, and the previous column is honestly absent.
	if got.Other != nil {
		t.Errorf("load invented a 'not itemised' row: %+v", *got.Other)
	}
	if ctl.Period == got.Period {
		t.Errorf("control period %q should carry a 'vs' half the restored run lacks", ctl.Period)
	}
	for _, it := range append(append(append([]uifinance.PLItem{}, got.Revenues...), got.Expenses...), got.CityEconomy...) {
		if it.PrevValueMicropounds != nil {
			t.Errorf("row %q has a previous-month figure on the first close after a load", it.Label)
		}
	}
	// A second close afterwards carries the previous column.
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), 30); err != nil {
		t.Fatal(err)
	}
	next, _ := plDecode(t, b.state)
	if nc := plCityRow(t, next, "Household consumption"); nc.PrevValueMicropounds == nil {
		t.Errorf("second close after the load still has no previous column")
	}
}
