package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestHeatmap_ForegroundGlyphUntouchedByBackgroundMetric(t *testing.T) {
	buf := core.NewBuffer(2, 1)
	fg := tcell.ColorGreen
	buf.Set(0, 0, '#', tcell.StyleDefault.Foreground(fg))
	buf.Set(1, 0, '@', tcell.StyleDefault.Foreground(fg))

	ramp := HeatRamp{tcell.ColorBlue, tcell.ColorRed}
	Heatmap(buf, core.Rect{X: 0, Y: 0, W: 2, H: 1}, []float64{0.0, 1.0}, 2, 0, 1, ramp)

	c0 := buf.Get(0, 0)
	c1 := buf.Get(1, 0)
	if c0.Rune != '#' || c1.Rune != '@' {
		t.Fatalf("Heatmap changed foreground glyphs: got %q, %q, want '#','@'", c0.Rune, c1.Rune)
	}
	f0, b0, _ := c0.Style.Decompose()
	f1, b1, _ := c1.Style.Decompose()
	if f0 != fg || f1 != fg {
		t.Fatalf("Heatmap changed foreground colour: got %v,%v want %v", f0, f1, fg)
	}
	if b0 != tcell.ColorBlue {
		t.Fatalf("cell 0 background = %v, want ColorBlue (value 0.0)", b0)
	}
	if b1 != tcell.ColorRed {
		t.Fatalf("cell 1 background = %v, want ColorRed (value 1.0)", b1)
	}
}

func TestHeatmap_SameForegroundDifferentMetricChangesOnlyBackground(t *testing.T) {
	buf := core.NewBuffer(1, 2)
	buf.Set(0, 0, 'X', tcell.StyleDefault)
	buf.Set(0, 1, 'X', tcell.StyleDefault)

	ramp := HeatRamp{tcell.ColorBlue, tcell.ColorRed}
	Heatmap(buf, core.Rect{X: 0, Y: 0, W: 1, H: 1}, []float64{0.0}, 1, 0, 1, ramp)
	Heatmap(buf, core.Rect{X: 0, Y: 1, W: 1, H: 1}, []float64{1.0}, 1, 0, 1, ramp)

	c0 := buf.Get(0, 0)
	c1 := buf.Get(0, 1)
	if c0.Rune != 'X' || c1.Rune != 'X' {
		t.Fatalf("foreground glyph changed: %q, %q", c0.Rune, c1.Rune)
	}
	_, b0, _ := c0.Style.Decompose()
	_, b1, _ := c1.Style.Decompose()
	if b0 == b1 {
		t.Fatalf("different metric values produced the same background colour: %v", b0)
	}
}

func TestHeatmap_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(2, 2)
	Heatmap(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, nil, 0, 0, 1, HeatRamp{tcell.ColorBlue})
	Heatmap(nil, core.Rect{X: 0, Y: 0, W: 2, H: 2}, []float64{1}, 1, 0, 1, HeatRamp{tcell.ColorBlue})
	Heatmap(buf, core.Rect{X: 0, Y: 0, W: 2, H: 2}, []float64{}, 2, 0, 1, nil)
}
