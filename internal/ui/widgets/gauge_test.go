package widgets

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestGauge_HalfFillOnEightCellsProducesFourFullBlocks(t *testing.T) {
	buf := core.NewBuffer(8, 1)
	Gauge(buf, core.Rect{X: 0, Y: 0, W: 8, H: 1}, 0.5, Thresholds{}, DefaultPalette, tcell.StyleDefault)

	full := string(gaugeGlyphs[4])
	empty := string(gaugeGlyphs[0])
	want := strings.Repeat(full, 4) + strings.Repeat(empty, 4)
	assertGrid(t, buf, []string{want})
}

func TestGauge_PartialCellUsesQuarterGlyph(t *testing.T) {
	buf := core.NewBuffer(8, 1)
	// 0.15625 * 8 * 4 = 5 quarter-units exactly -> 1 full cell + 1
	// quarter-fill cell.
	Gauge(buf, core.Rect{X: 0, Y: 0, W: 8, H: 1}, 0.15625, Thresholds{}, DefaultPalette, tcell.StyleDefault)

	want := string(gaugeGlyphs[4]) + string(gaugeGlyphs[1]) + strings.Repeat(string(gaugeGlyphs[0]), 6)
	assertGrid(t, buf, []string{want})
}

func TestGauge_ValueClampedOutsideZeroOne(t *testing.T) {
	buf := core.NewBuffer(4, 1)
	Gauge(buf, core.Rect{X: 0, Y: 0, W: 4, H: 1}, -1, Thresholds{}, DefaultPalette, tcell.StyleDefault)
	assertGrid(t, buf, []string{strings.Repeat(string(gaugeGlyphs[0]), 4)})

	buf2 := core.NewBuffer(4, 1)
	Gauge(buf2, core.Rect{X: 0, Y: 0, W: 4, H: 1}, 2, Thresholds{}, DefaultPalette, tcell.StyleDefault)
	assertGrid(t, buf2, []string{strings.Repeat(string(gaugeGlyphs[4]), 4)})
}

func TestGauge_ThresholdColoursFilledPortionOnly(t *testing.T) {
	buf := core.NewBuffer(4, 1)
	th := Thresholds{Warning: 0.5, Danger: 0.8, HigherIsBad: true}
	Gauge(buf, core.Rect{X: 0, Y: 0, W: 4, H: 1}, 0.9, th, DefaultPalette, tcell.StyleDefault)

	wantFillStyle := DefaultPalette.ThresholdStyle(StateDanger, tcell.StyleDefault)
	for x := 0; x < 4; x++ {
		c := buf.Get(x, 0)
		if c.Rune == gaugeGlyphs[0] {
			continue // unfilled track cell, not asserted here
		}
		if c.Style != wantFillStyle {
			t.Fatalf("filled cell %d style = %v, want danger threshold style %v", x, c.Style, wantFillStyle)
		}
	}
}

func TestGauge_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(4, 1)
	Gauge(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, 0.5, Thresholds{}, DefaultPalette, tcell.StyleDefault)
	Gauge(nil, core.Rect{X: 0, Y: 0, W: 4, H: 1}, 0.5, Thresholds{}, DefaultPalette, tcell.StyleDefault)
}
