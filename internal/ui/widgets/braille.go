package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// brailleBase is U+2800 BRAILLE PATTERN BLANK, the zero-dots codepoint;
// every other Braille pattern in the block is brailleBase plus an
// 8-bit dot mask, per the Unicode Braille Patterns block's own layout.
const brailleBase = rune(0x2800)

// brailleBit maps a dot's (subX, subY) position within a cell's 2x4
// sub-grid to its bit in the Braille pattern mask, per the standard
// Braille cell dot numbering:
//
//	1 4
//	2 5
//	3 6
//	7 8
//
// subX in {0,1} (left/right column), subY in {0,1,2,3} (row, top to
// bottom) — dot 7 and 8 (bottom row) are numbered out of raster order
// in the historical Braille numbering, which is exactly why this is a
// lookup table rather than a formula.
var brailleBit = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40}, // left column: dots 1,2,3,7
	{0x08, 0x10, 0x20, 0x80}, // right column: dots 4,5,6,8
}

// BrailleCanvas is a dot-addressable drawing surface backing a
// rect of terminal cells: cellsW*2 dot-columns by cellsH*4 dot-rows,
// the "effective 2x4x resolution" UI-SPEC §2 describes for Braille
// charts. Each cell holds one 8-bit dot mask.
type BrailleCanvas struct {
	cellsW, cellsH int
	masks          []uint8
}

// NewBrailleCanvas returns a blank canvas covering cellsW x cellsH
// terminal cells (cellsW*2 x cellsH*4 addressable dots). Negative sizes
// clamp to 0, matching core.NewBuffer's own convention.
func NewBrailleCanvas(cellsW, cellsH int) *BrailleCanvas {
	if cellsW < 0 {
		cellsW = 0
	}
	if cellsH < 0 {
		cellsH = 0
	}
	return &BrailleCanvas{cellsW: cellsW, cellsH: cellsH, masks: make([]uint8, cellsW*cellsH)}
}

// SetDot lights the dot at dot-coordinate (px, py), where px is in
// [0, cellsW*2) and py is in [0, cellsH*4). Out-of-range coordinates
// are silently ignored (never a panic), matching core.Buffer.Set's
// out-of-range discipline.
func (c *BrailleCanvas) SetDot(px, py int) {
	if c == nil || px < 0 || py < 0 {
		return
	}
	cellX, cellY := px/2, py/4
	if cellX >= c.cellsW || cellY >= c.cellsH {
		return
	}
	subX, subY := px%2, py%4
	i := cellY*c.cellsW + cellX
	c.masks[i] |= brailleBit[subX][subY]
}

// Mask returns the raw dot mask for cell (cellX, cellY), or 0 if out of
// range or nothing has been set there.
func (c *BrailleCanvas) Mask(cellX, cellY int) uint8 {
	if c == nil || cellX < 0 || cellY < 0 || cellX >= c.cellsW || cellY >= c.cellsH {
		return 0
	}
	return c.masks[cellY*c.cellsW+cellX]
}

// Rune returns the Braille pattern codepoint for cell (cellX, cellY):
// brailleBase plus whatever dots are set there. A cell with no dots set
// returns brailleBase itself (U+2800, the blank Braille pattern) rather
// than a plain space — callers that want a literal blank should check
// Mask() == 0 first, per BrailleChart's own convention below.
func (c *BrailleCanvas) Rune(cellX, cellY int) rune {
	return brailleBase + rune(c.Mask(cellX, cellY))
}

// brailleLine sets every dot on the Bresenham line from (x0,y0) to
// (x1,y1) inclusive, the standard integer-only line algorithm (no
// floating point, no allocation).
func brailleLine(c *BrailleCanvas, x0, y0, x1, y1 int) {
	dx := abs(x1 - x0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	dy := -abs(y1 - y0)
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.SetDot(x0, y0)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// plotSeries draws series as a connected line onto canvas, mapping
// index i to dot-column startCol+i*colStep/max(1,len(series)-1) style
// spacing (evenly spanning [startCol, startCol+dotsSpan)) and value to
// dot-row via min/max normalisation (higher value -> lower dot-row,
// since row 0 is the visual top). A single-point series draws one dot,
// not a line (nothing to connect to). An empty series draws nothing.
func plotSeries(canvas *BrailleCanvas, series []float64, startCol, dotsSpan, dotsH int, min, max float64) {
	n := len(series)
	if n == 0 || dotsH <= 0 {
		return
	}
	dotRow := func(v float64) int {
		if max <= min {
			return dotsH / 2
		}
		norm := (v - min) / (max - min)
		row := dotsH - 1 - int(norm*float64(dotsH-1)+0.5)
		if row < 0 {
			row = 0
		}
		if row > dotsH-1 {
			row = dotsH - 1
		}
		return row
	}
	dotCol := func(i int) int {
		if n == 1 {
			return startCol
		}
		return startCol + i*(dotsSpan-1)/(n-1)
	}

	prevX, prevY := dotCol(0), dotRow(series[0])
	canvas.SetDot(prevX, prevY)
	for i := 1; i < n; i++ {
		x, y := dotCol(i), dotRow(series[i])
		brailleLine(canvas, prevX, prevY, x, y)
		prevX, prevY = x, y
	}
}

// BrailleChart draws a two-series line chart into rect: history in
// historyStyle (solid), projection in projectionStyle (dim — UI-SPEC
// §2's "projections with confidence bands as dim dots" idiom, minus
// the confidence band, which is a caller-composed second projection
// draw at a different Y-offset if wanted). Both series share one
// value scale (min/max computed across both, so they plot on the same
// vertical axis) and occupy consecutive horizontal spans of the dot
// grid: history gets the first
// len(history)/(len(history)+len(projection)) fraction of the width,
// projection the rest — so a chart with only history fills the whole
// width, and one with only projection does too.
//
// history and projection may each be nil/empty (AC-11): an all-empty
// call draws nothing (every cell left as-is in buf, since there is no
// sane "blank" opinion to impose over whatever the caller already
// drew there); a chart with at least one point in either series always
// draws at least that point.
//
// Where history and projection land in the same cell (the single
// transition cell at the boundary), history's style takes drawing
// priority — documented simplification, since a terminal cell has one
// Style, not one per dot.
func BrailleChart(buf *core.Buffer, rect core.Rect, history, projection []float64, historyStyle, projectionStyle tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if len(history) == 0 && len(projection) == 0 {
		return
	}

	dotsW := rect.W * 2
	dotsH := rect.H * 4

	min, max := combinedRange(history, projection)

	histCanvas := NewBrailleCanvas(rect.W, rect.H)
	projCanvas := NewBrailleCanvas(rect.W, rect.H)

	total := len(history) + len(projection)
	histSpan := dotsW
	if total > 0 {
		histSpan = len(history) * dotsW / total
	}
	if len(history) > 0 && histSpan < 1 {
		histSpan = 1
	}
	projStart := histSpan
	projSpan := dotsW - histSpan

	plotSeries(histCanvas, history, 0, histSpan, dotsH, min, max)
	plotSeries(projCanvas, projection, projStart, projSpan, dotsH, min, max)

	for cy := 0; cy < rect.H; cy++ {
		for cx := 0; cx < rect.W; cx++ {
			x, y := rect.X+cx, rect.Y+cy
			if hm := histCanvas.Mask(cx, cy); hm != 0 {
				buf.Set(x, y, brailleBase+rune(hm), historyStyle)
				continue
			}
			if pm := projCanvas.Mask(cx, cy); pm != 0 {
				buf.Set(x, y, brailleBase+rune(pm), projectionStyle)
			}
		}
	}
}

// combinedRange returns the min/max across both series. If both are
// empty, returns (0,0). This is a small helper split out for testability
// of the "shared vertical scale" contract.
func combinedRange(a, b []float64) (min, max float64) {
	first := true
	scan := func(s []float64) {
		for _, v := range s {
			if first {
				min, max = v, v
				first = false
				continue
			}
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	scan(a)
	scan(b)
	return min, max
}
