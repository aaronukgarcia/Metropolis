package demo

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// PyramidRow is one dot-row's worth of aggregated male/female counts —
// ComputePyramidBars' pure output, kept separate from rendering so
// DEMO-1's "bar widths trace to the fixture's per-month-age counts"
// check can assert against this data directly, independent of any
// terminal-cell/dot-mask concern.
type PyramidRow struct {
	Male   int
	Female int
}

// ComputePyramidBars groups ages (expected pre-sorted ascending by
// MonthAge, as Screen.Population already returns it) into dotsH rows,
// oldest at row 0 (top) down to youngest at row dotsH-1 (bottom) — the
// conventional population-pyramid orientation. Each age bucket's Male/
// Female counts are summed into exactly one row via a deterministic,
// monotonic index mapping (i*dotsH/n), so the same input always
// produces the same grouping (GR#21) and adjacent age-months land in
// the same or adjacent rows (never scattered) — this is what makes the
// pyramid read as "smooth" per §13-F6's month-age requirement rather
// than a jagged year-bucketed chart: the smoothing comes from having up
// to 1200 distinct month-age input rows to distribute across a much
// smaller terminal-cell dot grid, not from any interpolation.
//
// A dotsH <= 0 or empty ages returns nil.
func ComputePyramidBars(ages []AgeBucket, dotsH int) []PyramidRow {
	n := len(ages)
	if n == 0 || dotsH <= 0 {
		return nil
	}
	rows := make([]PyramidRow, dotsH)
	for i := 0; i < n; i++ {
		age := ages[n-1-i] // oldest first
		row := i * dotsH / n
		if row >= dotsH {
			row = dotsH - 1
		}
		rows[row].Male += age.Male
		rows[row].Female += age.Female
	}
	return rows
}

// scaleDots converts count (out of maxCount) to a dot span within
// [0, spanDots], rounding to the nearest dot. maxCount<=0 or
// spanDots<=0 returns 0 (nothing to scale against / no room to draw).
func scaleDots(count, maxCount, spanDots int) int {
	if maxCount <= 0 || spanDots <= 0 {
		return 0
	}
	d := int(math.Round(float64(count) / float64(maxCount) * float64(spanDots)))
	if d < 0 {
		d = 0
	}
	if d > spanDots {
		d = spanDots
	}
	return d
}

// RenderPopulationPyramid draws the month-age population pyramid
// (DEMO-1) into rect: male bars grow left from the vertical centre
// line, female bars grow right, using widgets.BrailleCanvas — MOD-010's
// existing Braille dot-addressable primitive (SetDot/Mask/Rune) — for
// the sub-cell 2x4 dot resolution UI-SPEC §2 promises, rather than this
// package reimplementing dot-plane math itself. See doc.go's "no
// dedicated ui.widgets Pyramid function" note for why this composes
// BrailleCanvas directly instead of calling a single Pyramid(...)
// widget entry point — no such function exists in ui.widgets today,
// despite this item's acceptance criteria describing one; ASM logged.
//
// Style is picked per terminal cell (male style left of the centre
// cell, female style at/right of it) — a documented simplification, the
// same one BrailleChart (ui.widgets/braille.go) already uses for its
// own history/projection boundary cell, since a terminal cell carries
// one tcell.Style, not one per dot.
func RenderPopulationPyramid(buf *core.Buffer, rect core.Rect, ages []AgeBucket, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	dotsW := rect.W * 2
	dotsH := rect.H * 4
	rows := ComputePyramidBars(ages, dotsH)
	if len(rows) == 0 {
		return
	}

	maxCount := 0
	for _, r := range rows {
		if r.Male > maxCount {
			maxCount = r.Male
		}
		if r.Female > maxCount {
			maxCount = r.Female
		}
	}
	if maxCount == 0 {
		return
	}

	center := dotsW / 2
	leftSpan := center
	rightSpan := dotsW - center

	canvas := widgets.NewBrailleCanvas(rect.W, rect.H)
	for y, row := range rows {
		maleDots := scaleDots(row.Male, maxCount, leftSpan)
		for k := 0; k < maleDots; k++ {
			canvas.SetDot(center-1-k, y)
		}
		femaleDots := scaleDots(row.Female, maxCount, rightSpan)
		for k := 0; k < femaleDots; k++ {
			canvas.SetDot(center+k, y)
		}
	}

	maleStyle := palette.Style(widgets.TokenWater)
	femaleStyle := palette.Style(widgets.TokenWarning)
	centerCell := center / 2
	for cy := 0; cy < rect.H; cy++ {
		for cx := 0; cx < rect.W; cx++ {
			if canvas.Mask(cx, cy) == 0 {
				continue
			}
			style := maleStyle
			if cx >= centerCell {
				style = femaleStyle
			}
			buf.Set(rect.X+cx, rect.Y+cy, canvas.Rune(cx, cy), style)
		}
	}
}
