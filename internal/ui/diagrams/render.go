package diagrams

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// drawText writes s left-to-right starting at (x, y), one rune per cell.
// Out-of-range coordinates are dropped by core.Buffer.Set, never a panic.
func drawText(buf *core.Buffer, x, y int, s string, style tcell.Style) {
	col := x
	for _, r := range s {
		buf.Set(col, y, r, style)
		col++
	}
}

// drawLabel writes label into the interior row of a box rect (row Y+1),
// clipped to the interior width. A rect shorter than 3 rows has no interior
// row, so it draws nothing.
func drawLabel(buf *core.Buffer, rect core.Rect, label string, style tcell.Style) {
	if rect.H < 3 {
		return
	}
	interior := rect.W - 2
	col := rect.X + 1
	end := rect.X + 1 + interior
	for _, r := range label {
		if col >= end {
			break
		}
		buf.Set(col, rect.Y+1, r, style)
		col++
	}
}

// boundsOfRects returns the bounding rectangle of every hit with positive
// area. Zero-area rects (e.g. a zero-width Sankey band) are skipped. A
// zero Rect is returned when nothing has area.
func boundsOfRects(hits []Hit) core.Rect {
	first := true
	var minX, minY, maxX, maxY int
	for _, h := range hits {
		if h.Rect.W <= 0 || h.Rect.H <= 0 {
			continue
		}
		if first {
			minX, minY = h.Rect.X, h.Rect.Y
			maxX = h.Rect.X + h.Rect.W - 1
			maxY = h.Rect.Y + h.Rect.H - 1
			first = false
			continue
		}
		minX = min(minX, h.Rect.X)
		minY = min(minY, h.Rect.Y)
		maxX = max(maxX, h.Rect.X+h.Rect.W-1)
		maxY = max(maxY, h.Rect.Y+h.Rect.H-1)
	}
	if first {
		return core.Rect{}
	}
	return core.Rect{X: minX, Y: minY, W: maxX - minX + 1, H: maxY - minY + 1}
}
