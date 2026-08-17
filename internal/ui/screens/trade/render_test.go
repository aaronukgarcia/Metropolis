package trade

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderBuf(w, h int, draw func(*core.Buffer, core.Rect)) *core.Buffer {
	buf := core.NewBuffer(w, h)
	draw(buf, core.Rect{X: 0, Y: 0, W: w, H: h})
	return buf
}

func TestRenderContracts_UnavailableAndEmpty(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)

	buf := renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderContracts(b, r, nil, false, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "unavailable") {
		t.Errorf("TRD-8: unavailable contracts rendered blank instead of 'unavailable': %v", rows)
	}

	buf = renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderContracts(b, r, []ImportContract{}, true, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "none") {
		t.Errorf("empty contract list rendered without 'none': %v", rows)
	}
}

func TestRenderContracts_ShowsTermPenaltyAndPrice(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	contracts := []ImportContract{
		{ID: "c-1", Commodity: "grain", TermMonths: 12, MonthsRemaining: 8, CancellationPenaltyMicropounds: 1500000, PricePerUnitMicropounds: 45_000_000, Status: StatusActive},
		{ID: "c-2", Commodity: "fuel", TermMonths: 6, MonthsRemaining: 2, CancellationPenaltyMicropounds: 0, PricePerUnitMicropounds: 90_000_000, Status: StatusCancelled},
	}
	buf := renderBuf(90, 4, func(b *core.Buffer, r core.Rect) {
		RenderContracts(b, r, contracts, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 4})
	if !rowContains(rows, "c-1") || !rowContains(rows, "grain") || !rowContains(rows, "12mo") {
		t.Errorf("contract row missing term/commodity: %v", rows)
	}
	if !rowContains(rows, "£45.00") {
		t.Errorf("contract row missing £/unit price: %v", rows)
	}
	if !rowContains(rows, "£1.50") {
		t.Errorf("contract row missing penalty £1.50 (1500000 micropounds): %v", rows)
	}
	if !rowContains(rows, "[cancelled]") {
		t.Errorf("cancelled contract row missing [cancelled] marker: %v", rows)
	}
}

func TestRenderJunctions_UsesQueueLaneGlyphs(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	junctions := []JunctionQueue{
		{JunctionID: "junction:14", Label: "Gyratory", Approaches: []JunctionApproach{
			{ApproachID: "north", Cargo: widgets.CargoFreight, TruckCount: 12, WaitSeconds: 45},
		}},
	}
	buf := renderBuf(80, 4, func(b *core.Buffer, r core.Rect) {
		RenderJunctions(b, r, junctions, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 4})
	if !rowContains(rows, "45s") {
		t.Errorf("queue lane missing wait-time figure: %v", rows)
	}
	// The signature truck glyph (CargoFreight = '▩') must be present in the
	// lane row (TRD-2's "cargo-coded glyphs growing leftward").
	foundGlyph := false
	for _, row := range rows {
		for _, r := range row {
			if r == '▩' {
				foundGlyph = true
			}
		}
	}
	if !foundGlyph {
		t.Errorf("junction queue lane missing the cargo glyph: %v", rows)
	}
}

func TestRenderWarehouse_ShowsBufferPolicy(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	rows := []WarehouseCommodity{
		{Commodity: "grain", StockTonnes: 1200, CapacityTonnes: 2000, BufferTonnesPerDay: 25, FlowTonnesPerDay: 18},
	}
	buf := renderBuf(90, 3, func(b *core.Buffer, r core.Rect) {
		RenderWarehouse(b, r, rows, true, style)
	})
	text := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 3})
	if !rowContains(text, "grain") || !rowContains(text, "25t/day") || !rowContains(text, "1200t") {
		t.Errorf("warehouse row missing stock/buffer figures: %v", text)
	}
}

func TestRenderPort_UnlockGatingAndUnavailable(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)

	// Absent port -> "unavailable" (TRD-8), distinct from unlocked==false.
	buf := renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderPort(b, r, PortState{}, false, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "unavailable") {
		t.Errorf("absent port rendered without 'unavailable': %v", rows)
	}

	// Present but not unlocked -> "not yet unlocked" (TRD-4: reflect the
	// unlock state, no tier-gating logic of this screen's own).
	buf = renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderPort(b, r, PortState{Unlocked: false}, true, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "not yet unlocked") {
		t.Errorf("locked port rendered without 'not yet unlocked': %v", rows)
	}

	// Unlocked -> figures.
	port := PortState{Unlocked: true, Berths: 4, CraneRateTonnesPerHour: 40, OperatingHoursPerDay: 16, CustomsThroughputTonnesPerDay: 1500, SmugglingRisk: 0.35}
	buf = renderBuf(80, 7, func(b *core.Buffer, r core.Rect) {
		RenderPort(b, r, port, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 7})
	for _, want := range []string{"berths: 4", "40t/hr", "1500t/day", "35%"} {
		if !rowContains(rows, want) {
			t.Errorf("port panel missing %q: %v", want, rows)
		}
	}
}

func TestRenderBalance_ShowsCommodityAndArtery(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	balance := BalanceOfTradeView{
		Imports: TradeLedgerView{
			ByCommodity: []TradeFlow{{Key: "grain", TonnesPerDay: 40, ValuePerDayMicropounds: 1800000}},
			ByArtery:    []TradeFlow{{Key: "sea", TonnesPerDay: 60, ValuePerDayMicropounds: 2700000}},
		},
		Exports: TradeLedgerView{
			ByCommodity: []TradeFlow{{Key: "machinery", TonnesPerDay: 12, ValuePerDayMicropounds: 9600000}},
			ByArtery:    []TradeFlow{{Key: "rail", TonnesPerDay: 12, ValuePerDayMicropounds: 9600000}},
		},
	}
	buf := renderBuf(90, 8, func(b *core.Buffer, r core.Rect) {
		RenderBalance(b, r, balance, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 8})
	for _, want := range []string{"imports", "exports", "grain", "via sea", "40t/day", "£1.80/day", "machinery", "via rail"} {
		if !rowContains(rows, want) {
			t.Errorf("balance panel missing %q: %v", want, rows)
		}
	}
}

func TestRenderSafety_UnavailableWhenBlocked(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)

	// TRD-6 is blocked on a registry edge; the honest state is "unavailable"
	// (TRD-8), never a fabricated comparison and never blank.
	buf := renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderSafety(b, r, nil, false, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "unavailable") {
		t.Errorf("absent safety section rendered without 'unavailable': %v", rows)
	}

	// When the section IS delivered (forward-compatible), it renders the
	// comparison.
	corridors := []SafetyCorridor{{Corridor: "port-refinery", PipelineCapacityTonnesPerDay: 500, TruckMovementsPerDay: 120, LeakRisk: 0.02}}
	buf = renderBuf(90, 3, func(b *core.Buffer, r core.Rect) {
		RenderSafety(b, r, corridors, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 3})
	if !rowContains(rows, "port-refinery") || !rowContains(rows, "500t/day") || !rowContains(rows, "120/day") {
		t.Errorf("safety corridor missing figures: %v", rows)
	}
}

func TestDrillTargets_EveryFigureHasASource(t *testing.T) {
	s := newScreenWithData(t, "sub-drill")
	contracts, _ := s.Contracts()
	junctions, _ := s.Junctions()
	warehouse, _ := s.Warehouse()
	balance, _ := s.Balance()
	safety, _ := s.Safety()

	targets := DrillTargets(contracts, junctions, warehouse, balance, safety)

	// SF-5: every target must be a resolvable dash.DrillTarget (non-empty
	// ViewName), and the count must cover every figure the screen displays.
	if len(targets) == 0 {
		t.Fatal("DrillTargets returned no targets")
	}
	for i, tg := range targets {
		if !tg.Valid() {
			t.Errorf("target[%d] = %+v is not resolvable (empty ViewName)", i, tg)
		}
		if tg.ViewName != ViewSubscriptionName {
			t.Errorf("target[%d].ViewName = %q, want %q (SF-5: this screen's single view)", i, tg.ViewName, ViewSubscriptionName)
		}
	}
	// 2 contracts + 1 junction + 2 commodities + port + (1+1 import flows + 1+1 export flows) + 1 corridor = 11.
	if len(targets) != 11 {
		t.Errorf("DrillTargets returned %d targets, want 11 (2 contracts + 1 junction + 2 commodities + port + 4 flows + 1 corridor)", len(targets))
	}
}

func TestFormatPounds_Deterministic(t *testing.T) {
	cases := []struct {
		micropounds int64
		want        string
	}{
		{1_500_000, "£1.50"},
		{45_000_000, "£45.00"},
		{0, "£0.00"},
		{1_000_000_000, "£1000.00"},
		{-1_500_000, "-£1.50"},
		{math.MaxInt64, "£9223372036854.78"},
		{math.MinInt64, "-£9223372036854.78"},
	}
	for _, c := range cases {
		if got := formatPounds(c.micropounds); got != c.want {
			t.Errorf("formatPounds(%d) = %q, want %q", c.micropounds, got, c.want)
		}
	}
}

// TestFormatRisk_ClampsDomainBeforeConversion is the SEC-143 regression plus
// the class-covering matrix: a risk outside [0,1] (including the finite 1e300
// that overflows a float→int conversion, plus NaN/±Inf) must clamp to the
// nearest valid percentage, never render 0% for an out-of-range-high risk.
func TestFormatRisk_ClampsDomainBeforeConversion(t *testing.T) {
	cases := []struct {
		risk float64
		want string
	}{
		{0, "0%"},
		{0.02, "2%"},
		{0.35, "35%"},
		{1, "100%"},
		{1.5, "100%"},
		{1e300, "100%"},
		{-0.5, "0%"},
		{math.NaN(), "0%"},
		{math.Inf(1), "100%"},
		{math.Inf(-1), "0%"},
	}
	for _, c := range cases {
		if got := formatRisk(c.risk); got != c.want {
			t.Errorf("formatRisk(%v) = %q, want %q", c.risk, got, c.want)
		}
	}
}

// TestRenderHeader_StaleMarker covers the staleness dot rendering (SF-8 /
// UI-SPEC §1).
func TestRenderHeader_StaleMarker(t *testing.T) {
	buf := renderBuf(40, 1, func(b *core.Buffer, r core.Rect) {
		RenderHeader(b, r, true, true, tcell.StyleDefault)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 40, H: 1}); !rowContains(rows, "stale") {
		t.Errorf("stale header missing '(stale)' marker: %v", rows)
	}
}
