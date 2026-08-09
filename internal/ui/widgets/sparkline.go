package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// SparklineWidth is the fixed cell width every sparkline renders at —
// UI-SPEC §2: "every number that can trend carries a 12-cell sparkline
// of its last 24 months."
const SparklineWidth = 12

// sparkGlyphs is the eight-level block-height glyph table, precomputed
// once at package init so Sparkline's hot path only ever indexes into
// it (AC-13's zero-allocation contract).
var sparkGlyphs = [8]rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparklineFlatLevel is the glyph level a flat (min == max, including a
// single-point) series renders at: the middle of the eight levels.
// There is no relative variation to encode for a flat series, so rather
// than collapsing to the floor glyph (which would visually read as
// "near zero" / "bad", a false signal) every populated bucket renders
// at this fixed mid-height.
const sparklineFlatLevel = 3

// sparkLevel maps v's position within [min, max] to a 0-7 glyph-table
// index: level = round((v-min)/(max-min) * 7), clamped to [0,7]. This
// is the single normalisation every trending number in the game shares
// (UI-SPEC §2: "same idiom" for cash, road volume, school roll, …).
func sparkLevel(v, min, max float64) int {
	if max <= min {
		return sparklineFlatLevel
	}
	frac := (v - min) / (max - min)
	level := int(frac*float64(len(sparkGlyphs)-1) + 0.5)
	if level < 0 {
		level = 0
	}
	if level > len(sparkGlyphs)-1 {
		level = len(sparkGlyphs) - 1
	}
	return level
}

// Sparkline draws series into rect's first row, at most SparklineWidth
// cells wide (rect.W is clamped down to SparklineWidth if wider; a
// narrower rect simply draws fewer cells — every Set call is
// individually bounds-checked by core.Buffer so a too-small rect never
// panics, it just clips).
//
// series is downsampled into exactly width buckets by index-proportional
// binning (bucket = i*width/len(series)) and averaged within each
// bucket — the "24 months into 12 cells" idiom from UI-SPEC §2 falls
// out of this for a 24-length series (exactly two months per bucket);
// shorter or longer series bucket proportionally instead of assuming a
// fixed 24. A bucket with no series points assigned to it (more cells
// than data points) renders a blank space rather than a glyph — AC-11's
// "zero-length sparkline data" degenerate case is exactly "every bucket
// blank," which this produces for free when series is empty (the
// per-point loop below simply never runs).
//
// Zero-allocation: all per-call state is two fixed-size [SparklineWidth]
// arrays on the stack; no slice or map is allocated in this function.
func Sparkline(buf *core.Buffer, rect core.Rect, series []float64, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	width := rect.W
	if width > SparklineWidth {
		width = SparklineWidth
	}

	var sums [SparklineWidth]float64
	var counts [SparklineWidth]int

	n := len(series)
	var min, max float64
	if n > 0 {
		min, max = series[0], series[0]
	}
	for _, v := range series {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	for i, v := range series {
		bucket := i * width / n
		if bucket >= width {
			bucket = width - 1
		}
		if bucket < 0 {
			bucket = 0
		}
		sums[bucket] += v
		counts[bucket]++
	}
	flat := n > 0 && max <= min

	y := rect.Y
	for c := 0; c < width; c++ {
		x := rect.X + c
		if counts[c] == 0 {
			buf.Set(x, y, ' ', style)
			continue
		}
		avg := sums[c] / float64(counts[c])
		var level int
		if flat {
			level = sparklineFlatLevel
		} else {
			level = sparkLevel(avg, min, max)
		}
		buf.Set(x, y, sparkGlyphs[level], style)
	}
}
