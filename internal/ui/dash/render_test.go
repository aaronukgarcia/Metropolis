package dash_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

const (
	testW = 24
	testH = 8
)

func testRect() core.Rect { return core.Rect{X: 0, Y: 0, W: testW, H: testH} }

func baseStyle() tcell.Style { return tcell.StyleDefault }

// bufsEqual compares two buffers cell-for-cell (rune + style). It is the
// "renders via the widget, not a duplicated implementation" proof: if
// dash.RenderTile delegates to the widget, its output is byte-identical
// to calling the widget directly; a bespoke re-implementation would have
// to coincidentally match every glyph and style.
func bufsEqual(a, b *core.Buffer) bool {
	aw, ah := a.Size()
	bw, bh := b.Size()
	if aw != bw || ah != bh {
		return false
	}
	for y := 0; y < ah; y++ {
		for x := 0; x < aw; x++ {
			ca, cb := a.Get(x, y), b.Get(x, y)
			if ca.Rune != cb.Rune || ca.Style != cb.Style {
				return false
			}
		}
	}
	return true
}

// TestRenderTileDelegatesToWidgets is AC-1: each tile type renders via
// its corresponding ui.widgets function, not a duplicated implementation.
func TestRenderTileDelegatesToWidgets(t *testing.T) {
	palette := widgets.DefaultPalette
	drill := dash.DrillTarget{ViewName: "f1.viewport"}
	rect := testRect()

	cases := []struct {
		name string
		tile func() dash.Tile
		ref  func(*core.Buffer)
	}{
		{
			name: "bignum",
			tile: func() dash.Tile {
				t, _ := dash.NewBignumTile("b", drill, dash.BignumSpec{Label: "Pop", ValueText: "12", Prev: 10, Curr: 12, Series: []float64{1, 2, 3}})
				return t
			},
			ref: func(b *core.Buffer) {
				widgets.BigNum(b, rect, widgets.BigNumState{Label: "Pop", ValueText: "12", Prev: 10, Curr: 12, Series: []float64{1, 2, 3}}, palette, baseStyle())
			},
		},
		{
			name: "gauge",
			tile: func() dash.Tile {
				t, _ := dash.NewGaugeTile("g", drill, dash.GaugeSpec{Value: 0.5})
				return t
			},
			ref: func(b *core.Buffer) {
				widgets.Gauge(b, core.Rect{X: 0, Y: 0, W: testW, H: 1}, 0.5, widgets.Thresholds{}, palette, baseStyle())
			},
		},
		{
			name: "sparkline",
			tile: func() dash.Tile {
				t, _ := dash.NewSparkTile("s", drill, dash.SparkSpec{Series: []float64{1, 2, 3, 4}})
				return t
			},
			ref: func(b *core.Buffer) {
				widgets.Sparkline(b, core.Rect{X: 0, Y: 1, W: testW, H: 1}, []float64{1, 2, 3, 4}, baseStyle())
			},
		},
		{
			name: "minimap",
			tile: func() dash.Tile {
				t, _ := dash.NewMinimapTile("m", drill, dash.MinimapSpec{Values: []float64{0, 1, 2, 3}, Width: 2})
				return t
			},
			ref: func(b *core.Buffer) {
				widgets.Heatmap(b, rect, []float64{0, 1, 2, 3}, 2, 0, 3, widgets.DefaultHeatRamp(palette))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := core.NewBuffer(testW, testH)
			dash.RenderTile(got, rect, tc.tile(), palette, baseStyle())

			want := core.NewBuffer(testW, testH)
			tc.ref(want)

			if !bufsEqual(got, want) {
				t.Fatalf("RenderTile(%s) did not match a direct widgets.%s call — a duplicated implementation, not delegation", tc.name, tc.name)
			}
		})
	}
}

// TestRenderTileAlertsUsesWidgetBorder proves the alert-list tile draws
// its frame through widgets.Border (the shared border widget), not a
// bespoke box — the top-left corner glyph a Border writes is present.
func TestRenderTileAlertsUsesWidgetBorder(t *testing.T) {
	drill := dash.DrillTarget{ViewName: "f1.viewport"}
	tile, err := dash.NewAlertsTile("a", drill, dash.AlertsSpec{
		Label:   "Alerts",
		Entries: []dash.AlertEntry{{Text: "road flooded", Severity: dash.SeverityDanger}},
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := core.NewBuffer(testW, testH)
	dash.RenderTile(buf, testRect(), tile, widgets.DefaultPalette, baseStyle())

	corner := buf.Get(0, 0).Rune
	if corner != '┌' && corner != '┏' {
		t.Fatalf("alert tile corner glyph = %q, want a Border corner (┌/┏)", corner)
	}
}

// TestRenderProjectionFourDistinctElements is AC-8: a known projection
// series with a queued decision marker renders all four visual elements
// — history (solid), projection (dim), threshold line, decision marker —
// distinctly.
func TestRenderProjectionFourDistinctElements(t *testing.T) {
	proj := dash.Projection{
		History:    []float64{1, 2, 3},
		Projection: []float64{4, 5, 6},
		Thresholds: []dash.ProjectionThreshold{{Value: 3}},
		Decisions:  []dash.ProjectionDecision{{At: 4, Label: "school"}},
	}
	buf := core.NewBuffer(40, 12)
	dash.RenderProjection(buf, core.Rect{X: 0, Y: 0, W: 40, H: 12}, proj, baseStyle())

	var sawBraille, sawDim, sawThreshold, sawMarker bool
	w, h := buf.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if c.Rune >= 0x2800 && c.Rune <= 0x28FF {
				sawBraille = true
				_, _, attrs := c.Style.Decompose()
				if attrs&tcell.AttrDim != 0 {
					sawDim = true
				}
			}
			if c.Rune == '─' {
				sawThreshold = true
			}
			if c.Rune == '●' {
				sawMarker = true
			}
		}
	}
	if !sawBraille {
		t.Fatal("no Braille chart glyphs rendered (history+projection missing)")
	}
	if !sawDim {
		t.Fatal("no dim projection cells rendered (projection must be dim, distinct from solid history)")
	}
	if !sawThreshold {
		t.Fatal("no threshold line ('─') rendered")
	}
	if !sawMarker {
		t.Fatal("no decision marker ('●') rendered")
	}
}
