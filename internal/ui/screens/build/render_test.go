package build

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderBuf(w, h int, draw func(*core.Buffer, core.Rect)) *core.Buffer {
	buf := core.NewBuffer(w, h)
	draw(buf, core.Rect{X: 0, Y: 0, W: w, H: h})
	return buf
}

func TestRenderZones_ShowsEightZoneClasses(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	s := newScreenWithData(t, "sub-zones")
	zones, have := s.Zones()

	buf := renderBuf(90, 10, func(b *core.Buffer, r core.Rect) {
		RenderZones(b, r, zones, have, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 10})
	// BLD-2: all eight §34 zone classes are selectable/visible.
	for _, name := range []string{"Dwelling", "Shop", "Office", "Entertainment", "Farming", "Manufacturing", "Heavy Industry", "Mining"} {
		if !rowContains(rows, name) {
			t.Errorf("zones pane missing %q: %v", name, rows)
		}
	}
}

func TestRenderZones_Unavailable(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	buf := renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderZones(b, r, nil, false, style)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3}); !rowContains(rows, "unavailable") {
		t.Errorf("BLD-8: unavailable zones rendered blank instead of 'unavailable': %v", rows)
	}
}

func TestRenderQueue_ShowsFiguresAndUsesQueueLane(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	s := newScreenWithData(t, "sub-queue")
	orders, have := s.Queue()

	buf := renderBuf(90, 4, func(b *core.Buffer, r core.Rect) {
		RenderQueue(b, r, orders, have, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 4})

	// BLD-3: materials / labour / lead-time-remaining per item.
	for _, want := range []string{"#1 dwelling", "40/100t", "labour 20d", "lead 15d", "in-progress", "#2 manufacturing", "materials-pending"} {
		if !rowContains(rows, want) {
			t.Errorf("queue pane missing %q: %v", want, rows)
		}
	}
	// BLD-3: the row uses widgets.QueueLane's construction-freight glyph
	// (CargoFreight = '▩'), reused verbatim rather than reimplemented.
	foundGlyph := false
	for _, row := range rows {
		for _, r := range row {
			if r == '▩' {
				foundGlyph = true
			}
		}
	}
	if !foundGlyph {
		t.Errorf("build queue lane missing the construction-freight glyph: %v", rows)
	}
}

func TestRenderCatalogue_UnlockBadges(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	s := newScreenWithData(t, "sub-catalogue")
	entries, have := s.Catalogue()

	buf := renderBuf(90, 5, func(b *core.Buffer, r core.Rect) {
		RenderCatalogue(b, r, entries, have, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 90, H: 5})

	// BLD-5: the badge reflects the view's unlockState, not a local
	// recomputation — each of the three states renders its own badge.
	for _, want := range []string{"Footpath", "[unlocked]", "Motorway extension", "[locked]", "Avenue (2+2, parking)", "[in-progress]"} {
		if !rowContains(rows, want) {
			t.Errorf("catalogue pane missing %q: %v", want, rows)
		}
	}
}

func TestRenderCatalogue_UnavailableEntryBadge(t *testing.T) {
	// BLD-8: an entry whose unlockState is unavailable renders "unavailable"
	// as its badge, never blank.
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	entries := []CatalogueEntry{{ID: "x", Name: "Mystery", Section: "R", Unlock: UnlockUnavailable}}
	buf := renderBuf(80, 3, func(b *core.Buffer, r core.Rect) {
		RenderCatalogue(b, r, entries, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 3})
	if !rowContains(rows, "[unavailable]") {
		t.Errorf("catalogue entry with unavailable unlock rendered without badge: %v", rows)
	}
}

func TestRenderLandPrice_ShowsPrice(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	price := LandPriceView{Cell: protocol.CellRef{X: 2, Y: 3}, PriceMicropounds: 1_250_000}
	buf := renderBuf(60, 2, func(b *core.Buffer, r core.Rect) {
		RenderLandPrice(b, r, price, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 60, H: 2})
	if !rowContains(rows, "cell (2,3)") || !rowContains(rows, "£1.25") {
		t.Errorf("land-price pane missing cell/price figures: %v", rows)
	}
}

func TestRenderDemolition_ShowsCompensation(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	dem := DemolitionView{Cell: protocol.CellRef{X: 2, Y: 3}, CompensationMicropounds: 600_000}
	buf := renderBuf(60, 2, func(b *core.Buffer, r core.Rect) {
		RenderDemolition(b, r, dem, true, style)
	})
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 60, H: 2})
	if !rowContains(rows, "£0.60") {
		t.Errorf("demolition pane missing compensation figure: %v", rows)
	}
}

func TestDrillTargets_EveryFigureHasASource(t *testing.T) {
	s := newScreenWithData(t, "sub-drill")
	orders, _ := s.Queue()
	entries, _ := s.Catalogue()

	targets := DrillTargets(orders, entries)

	// SF-5/BLD-6: every target must be a resolvable dash.DrillTarget
	// (non-empty ViewName), all pointing at this screen's single view.
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
	// 2 queue orders + 3 catalogue entries + landPrice = 6.
	if len(targets) != 6 {
		t.Errorf("DrillTargets returned %d targets, want 6 (2 orders + 3 catalogue + landPrice)", len(targets))
	}
}

func TestFormatPounds_Deterministic(t *testing.T) {
	cases := []struct {
		micropounds int64
		want        string
	}{
		{1_250_000, "£1.25"},
		{600_000, "£0.60"},
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

// TestLaneLength_ClampsInt64ToInt is the GR#16 guard for the int64 lead-time
// figure feeding widgets.QueueLane's int length: an absurd or negative value
// clamps, never wraps.
func TestLaneLength_ClampsInt64ToInt(t *testing.T) {
	cases := []struct {
		remaining int64
		want      int
	}{
		{0, 0},
		{-5, 0},
		{15, 15},
		{30, 30},
		{31, 30},
		{math.MaxInt64, 30},
		{math.MinInt64, 0},
	}
	for _, c := range cases {
		if got := laneLength(c.remaining); got != c.want {
			t.Errorf("laneLength(%d) = %d, want %d", c.remaining, got, c.want)
		}
	}
}

func TestRenderHeader_StaleMarker(t *testing.T) {
	buf := renderBuf(40, 1, func(b *core.Buffer, r core.Rect) {
		RenderHeader(b, r, true, true, tcell.StyleDefault)
	})
	if rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 40, H: 1}); !rowContains(rows, "stale") {
		t.Errorf("stale header missing '(stale)' marker: %v", rows)
	}
}
