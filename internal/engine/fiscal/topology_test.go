package fiscal

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSankeyTopologyWellFormed asserts every edge references an existing node
// and that the fixed node set (budget, all money-in sources, all money-out
// sinks) is present (AC-1/AC-2).
func TestSankeyTopologyWellFormed(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 1_000_000_000)
	if _, err := fin.SettleImports(100_000); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	if err := fin.ServiceDebt(50_000, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	topo, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology: %v", err)
	}

	nodeIDs := make(map[NodeCategory]bool, len(topo.Nodes))
	for _, n := range topo.Nodes {
		nodeIDs[n.ID] = true
	}
	for _, e := range topo.Edges {
		if !nodeIDs[e.From] {
			t.Errorf("edge %q -> %q references unknown source node %q", e.From, e.To, e.From)
		}
		if !nodeIDs[e.To] {
			t.Errorf("edge %q -> %q references unknown target node %q", e.From, e.To, e.To)
		}
	}

	// Every money-in/out category node is present and named.
	for _, cat := range append(append([]NodeCategory{}, sourceNodeOrder...), sinkNodeOrder...) {
		if !nodeIDs[cat] {
			t.Errorf("node %q missing from topology", cat)
		}
	}
	if !nodeIDs[NodeBudget] {
		t.Errorf("budget node missing from topology")
	}
}

// TestProvisionalNodes asserts the unbuilt-producer nodes are queryable with
// amount zero and the Provisional flag set (AC-2), never a fabricated
// non-zero figure.
func TestProvisionalNodes(t *testing.T) {
	f, _, _ := newTestFiscal(t)

	for _, cat := range []NodeCategory{NodeExports, NodeTourism, NodeOutCommuterWages, NodeFDI, NodeGrants, NodeLeakage} {
		node, err := f.Node(cat)
		if err != nil {
			t.Fatalf("Node(%q): %v", cat, err)
		}
		if !node.Provisional {
			t.Errorf("Node(%q).Provisional = false, want true", cat)
		}
		if node.Amount != 0 {
			t.Errorf("Node(%q).Amount = %d, want 0 (no fabricated non-zero figure for an unbuilt producer)", cat, int64(node.Amount))
		}
	}
}

// TestTaxBreakdownMatchesIndependentQuery asserts the per-type breakdown sums
// to the total tax revenue queried independently from engine.tax, and that
// each line matches a from-scratch bucket over TaxAPI.Instruments (AC-3).
func TestTaxBreakdownMatchesIndependentQuery(t *testing.T) {
	f, _, taxAPI := newTestFiscal(t)

	// Give each instrument a distinct base so per-instrument bucketing is
	// actually exercised (not all-zero).
	bases := []finance.Money{1000, 2000, 3000, 4000, 5000, 6000}
	infos := taxAPI.Instruments()
	if len(infos) != len(bases) {
		t.Fatalf("expected %d instruments, got %d", len(bases), len(infos))
	}
	for i, info := range infos {
		if err := taxAPI.SetBase(info.ID, bases[i]); err != nil {
			t.Fatalf("SetBase(%q): %v", info.ID, err)
		}
	}

	got, err := f.TaxBreakdown()
	if err != nil {
		t.Fatalf("TaxBreakdown: %v", err)
	}

	// Independent from-scratch bucket over TaxAPI.Instruments.
	var want TaxBreakdown
	for _, info := range taxAPI.Instruments() {
		switch info.Category {
		case dataCatIncome:
			want.Income, _ = satAdd(want.Income, info.Revenue)
		case dataCatConsumption:
			want.Sales, _ = satAdd(want.Sales, info.Revenue)
		case dataCatCorporateProfit:
			want.Corporation, _ = satAdd(want.Corporation, info.Revenue)
		case dataCatProperty:
			want.Rates, _ = satAdd(want.Rates, info.Revenue)
		case dataCatImport:
			want.Duties, _ = satAdd(want.Duties, info.Revenue)
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("TaxBreakdown() = %+v, want %+v", got, want)
	}
	if got.Total() != taxAPI.RevenueTotal() {
		t.Errorf("TaxBreakdown().Total() = %d, want tax.RevenueTotal() = %d", int64(got.Total()), int64(taxAPI.RevenueTotal()))
	}
}

// TestDrillThroughMatchesFinanceLines asserts a Sankey edge's drill-through
// equals engine.finance's own line query for the same category (AC-8).
func TestDrillThroughMatchesFinanceLines(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 1_000_000_000)
	if _, err := fin.SettleImports(111_000); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	if err := fin.ServiceDebt(77_000, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	got, err := f.DrillThrough(NodeImports)
	if err != nil {
		t.Fatalf("DrillThrough(NodeImports): %v", err)
	}
	want := fin.LinesByCategory(finance.CatImports)
	if len(got) != len(want) {
		t.Fatalf("DrillThrough(NodeImports) returned %d entries, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Provisional nodes drill through to an empty (honest) line set.
	if lines, err := f.DrillThrough(NodeExports); err != nil || len(lines) != 0 {
		t.Errorf("DrillThrough(NodeExports) = (%d lines, %v), want (0, nil)", len(lines), err)
	}
}

// TestUnknownCategoryRejected asserts an unknown category returns a
// registry-sourced error naming the category, never a zero-value node
// silently returned as a real producer (AC-10).
func TestUnknownCategoryRejected(t *testing.T) {
	f, _, _ := newTestFiscal(t)

	_, err := f.Node(NodeCategory("nonsense"))
	if err == nil {
		t.Fatal("Node(unknown) returned nil error, want ErrUnknownCategory")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("Node(unknown) error is %T, want *errs.E", err)
	}
	if e.Code != ErrUnknownCategory {
		t.Errorf("Node(unknown) error code = %q, want %q", e.Code, ErrUnknownCategory)
	}

	if _, err := f.DrillThrough(NodeCategory("nonsense")); err == nil {
		t.Fatal("DrillThrough(unknown) returned nil error, want ErrUnknownCategory")
	}
}

// TestNoDriftRecompute asserts the topology's node totals are recomputed at
// query time from a synthetic engine.finance ledger state, across two
// different ledger states in one run (proving it is not cached-and-stale,
// AC-9).
func TestNoDriftRecompute(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 10_000_000_000)

	if _, err := fin.SettleImports(1_000_000); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	topo1, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology #1: %v", err)
	}
	if got := nodeAmountOf(t, topo1, NodeImports); got != fin.ImportsTotal() {
		t.Errorf("state 1 imports node = %d, want finance.ImportsTotal() = %d", int64(got), int64(fin.ImportsTotal()))
	}

	// Second ledger state: more imports plus debt interest.
	if _, err := fin.SettleImports(500_000); err != nil {
		t.Fatalf("SettleImports #2: %v", err)
	}
	if err := fin.ServiceDebt(200_000, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	topo2, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology #2: %v", err)
	}
	if got := nodeAmountOf(t, topo2, NodeImports); got != fin.ImportsTotal() {
		t.Errorf("state 2 imports node = %d, want finance.ImportsTotal() = %d", int64(got), int64(fin.ImportsTotal()))
	}
	if got := nodeAmountOf(t, topo2, NodeInterest); got != fin.DebtServiceTotal() {
		t.Errorf("state 2 interest node = %d, want finance.DebtServiceTotal() = %d", int64(got), int64(fin.DebtServiceTotal()))
	}
	if nodeAmountOf(t, topo1, NodeImports) == nodeAmountOf(t, topo2, NodeImports) {
		t.Errorf("imports node did not change between ledger states — cached, not recomputed")
	}
}

// TestSankeyTopologyDeterministic asserts identical topology output across
// repeated runs of the same ledger state (AC-11/GR#21).
func TestSankeyTopologyDeterministic(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 10_000_000_000)
	if _, err := fin.SettleImports(1_000_000); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	if err := fin.ServiceDebt(300_000, 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}

	a, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology #1: %v", err)
	}
	b, err := f.SankeyTopology()
	if err != nil {
		t.Fatalf("SankeyTopology #2: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("SankeyTopology is not deterministic:\n%+v\n!=\n%+v", a, b)
	}
}

// TestTaxCategoryMappingCoversLoadedInstruments is the weakness-pattern-#2
// drift guard for the duplicated engine.tax data-category strings: every data
// category present in the loaded instrument set must map to a §54 budget line
// (otherwise an instrument's revenue silently drops out of TaxBreakdown and
// the "sum equals RevenueTotal" guarantee breaks).
func TestTaxCategoryMappingCoversLoadedInstruments(t *testing.T) {
	taxAPI, err := tax.LoadDefault("test")
	if err != nil {
		t.Fatalf("tax.LoadDefault: %v", err)
	}
	for _, info := range taxAPI.Instruments() {
		if _, ok := taxLineForCategory(info.Category); !ok {
			t.Errorf("data category %q (instrument %q) has no §54 budget line — add it to taxLineForCategory", info.Category, info.ID)
		}
	}
}

// TestBasisPointScaleMatchesFinance is the weakness-pattern-#2 drift guard for
// the duplicated basis-point scale: fiscal.moneyTimesRate must agree with
// engine.finance's own rate application, verified through finance's public
// CollectTax API (finance does not export its scale).
func TestBasisPointScaleMatchesFinance(t *testing.T) {
	f, _ := New(validTestConfig(), "test")
	if f == nil {
		t.Fatal("New returned nil")
	}
	fin := finance.NewFinanceAPI("test")
	if err := f.SetFinance(fin); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 100_000_000_000)

	const wages finance.Money = 1_000_000_000 // £1,000
	if _, err := fin.PostWages(wages); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	// finance applies BasisPoints(2000) = 20% to the wage base.
	receipts, err := fin.CollectTax(finance.TaxRates{IncomeRate: 2000}, wages, 0, 0)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	got, err := f.moneyTimesRate(wages, 20.0)
	if err != nil {
		t.Fatalf("moneyTimesRate: %v", err)
	}
	if got != receipts.Income {
		t.Errorf("moneyTimesRate(£1000, 20%%) = %d, want finance CollectTax income = %d", int64(got), int64(receipts.Income))
	}
}

// validTestConfig returns a minimal valid Config (used where the data-file
// loader is not wanted, e.g. the basis-point drift test).
func validTestConfig() Config {
	return Config{
		Version: 1,
		Municipality: MunicipalityConfig{
			FundingTargetPerMonthMicroPounds: 10000000000,
			PermitSpeedAtZeroFunding:         0.5,
			PermitSpeedAtFullFunding:         2.0,
			BuildCostErrorAtZeroFunding:      0.15,
			BuildCostErrorAtFullFunding:      0.0,
			LayoutBonusAtZeroFunding:         0.0,
			LayoutBonusAtFullFunding:         1.0,
			CorruptionThreshold:              0.3,
			CorruptionMax:                    0.5,
		},
		Childcare: ChildcareConfig{
			SubsidyPerPlacePerMonthMicroPounds:     300000000,
			SecondEarnerUpliftPerPlace:             0.8,
			SecondEarnerAvgWagePerMonthMicroPounds: 1800000000,
		},
	}
}
