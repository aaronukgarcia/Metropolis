package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Delta is the direction a value moved between two readings — UI-SPEC
// §2's big-number tile "delta arrow."
type Delta int

const (
	// DeltaFlat: curr == prev.
	DeltaFlat Delta = iota
	// DeltaUp: curr > prev.
	DeltaUp
	// DeltaDown: curr < prev.
	DeltaDown
)

// deltaArrow is the glyph table for Delta, precomputed.
var deltaArrow = [3]rune{'▬', '▲', '▼'}

// DeriveDelta compares curr against prev. NaN on either side (a
// degenerate/missing-previous-reading case) reports DeltaFlat rather
// than an undefined comparison result — Go's `>`/`<` both evaluate
// false against NaN, which already falls through to DeltaFlat, so this
// is a documented consequence rather than special-cased logic.
func DeriveDelta(prev, curr float64) Delta {
	switch {
	case curr > prev:
		return DeltaUp
	case curr < prev:
		return DeltaDown
	default:
		return DeltaFlat
	}
}

// BigNumState is a big-number tile's full input: the label, a
// pre-formatted value string (formatting money/counts/percentages is a
// caller concern — this widget only lays glyphs out, it does not know
// currency symbols or locale), the previous/current raw readings (for
// delta derivation), a trend series for the embedded sparkline, and the
// Thresholds that derive the tile's colour state.
type BigNumState struct {
	Label      string
	ValueText  string
	Prev, Curr float64
	Series     []float64
	Thresholds Thresholds
}

// BigNum draws a dashboard tile into rect:
//
//	row 0: Label
//	row 1: ValueText + delta arrow
//	row 2: embedded Sparkline (if rect.H >= 3)
//
// Threshold colouring (palette.ThresholdStyle against Thresholds.State
// of Curr) is applied to the value+arrow row. This layout is
// deliberately simple — UI-SPEC §2 leaves tile chrome to the
// dashboard-composition layer (MOD-038, out of this widget's scope);
// BigNum's job is only that value/delta/sparkline/threshold-colour are
// each correctly derived and drawn somewhere legible, which is what
// AC-7's test checks.
func BigNum(buf *core.Buffer, rect core.Rect, state BigNumState, palette Palette, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}

	labelStyle := base
	drawText(buf, rect.X, rect.Y, rect.W, state.Label, labelStyle)

	if rect.H < 2 {
		return
	}
	st := palette.ThresholdStyle(state.Thresholds.State(state.Curr), base)
	delta := DeriveDelta(state.Prev, state.Curr)
	valueRow := rect.Y + 1
	x := drawText(buf, rect.X, valueRow, rect.W, state.ValueText, st)
	if x < rect.X+rect.W {
		buf.Set(x, valueRow, ' ', st)
		x++
	}
	if x < rect.X+rect.W {
		buf.Set(x, valueRow, deltaArrow[delta], st)
	}

	if rect.H < 3 {
		return
	}
	sparkRect := core.Rect{X: rect.X, Y: rect.Y + 2, W: rect.W, H: 1}
	Sparkline(buf, sparkRect, state.Series, base)
}

// drawText writes s into buf starting at (x, y), clipped to maxW cells,
// and returns the x coordinate immediately after the last cell written
// (useful for callers laying out more content on the same row). No
// allocation beyond the string's own rune iteration (range over a
// string does not allocate).
func drawText(buf *core.Buffer, x, y, maxW int, s string, style tcell.Style) int {
	cx := x
	limit := x + maxW
	for _, r := range s {
		if cx >= limit {
			break
		}
		buf.Set(cx, y, r, style)
		cx++
	}
	return cx
}
