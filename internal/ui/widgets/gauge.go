package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// gaugeGlyphs is the five-level block-fill table for one cell's worth
// of gauge: empty, quarter, half, three-quarter, full — UI-SPEC §2's
// "block-element gauges (█▓▒░) for capacities (junction slots, berth
// utilisation, prison occupancy)." Index 0 is empty (a space, not one
// of the four spec glyphs, since an unfilled cell has no ink) through
// index 4, full block.
var gaugeGlyphs = [5]rune{' ', '░', '▒', '▓', '█'}

// Gauge draws a horizontal block-fill bar into rect's first row. value
// is clamped to [0,1] before rendering — AC-11's "a gauge value outside
// [0,1]" degenerate case renders the clamped extreme (0 or 1) rather
// than panicking or producing a garbage fill length. thresholds
// (optionally zero-valued to mean "no threshold colouring") derives a
// ThresholdState from value against the *unclamped* input semantics
// (i.e. against whatever scale value was already normalised to by the
// caller — Gauge does not itself know the caller's raw min/max, only
// the already-normalised [0,1] fraction), and palette recolours the
// filled portion accordingly.
//
// Fill resolution is quarter-cell: rect.W*4 total quarter-units are
// available, filled = round(value * rect.W * 4) of them. A gauge with
// rect.W=8 and value=0.5 has 32 quarter-units, 16 filled, which is
// exactly 4 whole cells and zero remainder — AC-6's worked example
// ("0.5 fill on an 8-cell gauge produces 4 full blocks").
//
// Zero-allocation: no slice/map allocated; gaugeGlyphs is a package-level
// precomputed table.
func Gauge(buf *core.Buffer, rect core.Rect, value float64, thresholds Thresholds, palette Palette, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}

	fillStyle := palette.ThresholdStyle(thresholds.State(value), style)

	totalQuarters := int(value*float64(rect.W)*4 + 0.5)
	if totalQuarters < 0 {
		totalQuarters = 0
	}
	maxQuarters := rect.W * 4
	if totalQuarters > maxQuarters {
		totalQuarters = maxQuarters
	}
	fullCells := totalQuarters / 4
	remainder := totalQuarters % 4

	y := rect.Y
	for c := 0; c < rect.W; c++ {
		x := rect.X + c
		switch {
		case c < fullCells:
			buf.Set(x, y, gaugeGlyphs[4], fillStyle)
		case c == fullCells && remainder > 0:
			buf.Set(x, y, gaugeGlyphs[remainder], fillStyle)
		default:
			buf.Set(x, y, gaugeGlyphs[0], style)
		}
	}
}
