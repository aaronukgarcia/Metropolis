package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Focus selects which glyph/attribute set Border draws with — UI-SPEC
// §2: "box-drawing borders (─│┌┐└┘├┤), heavy variants for focused pane,
// dim for unfocused."
type Focus int

const (
	// Unfocused draws the light box-drawing glyph set with the dim
	// attribute set on the style.
	Unfocused Focus = iota
	// Focused draws the heavy box-drawing glyph set (━┃┏┓┗┛), no dim
	// attribute.
	Focused
)

// borderGlyphs is one full box-drawing rune set: the eight positions a
// rectangular panel border needs (corners, edges, and the two tee
// junctions a title separator or docked scrollbar might use later).
type borderGlyphs struct {
	horiz, vert             rune
	topLeft, topRight       rune
	bottomLeft, bottomRight rune
	teeLeft, teeRight       rune
}

var lightGlyphs = borderGlyphs{
	horiz: '─', vert: '│',
	topLeft: '┌', topRight: '┐',
	bottomLeft: '└', bottomRight: '┘',
	teeLeft: '├', teeRight: '┤',
}

var heavyGlyphs = borderGlyphs{
	horiz: '━', vert: '┃',
	topLeft: '┏', topRight: '┓',
	bottomLeft: '┗', bottomRight: '┛',
	teeLeft: '┣', teeRight: '┫',
}

func glyphsFor(f Focus) borderGlyphs {
	if f == Focused {
		return heavyGlyphs
	}
	return lightGlyphs
}

// styleFor returns base with the dim attribute applied for Unfocused
// panes (a second, independent signal alongside the light glyph set —
// AC-1 asks for "a heavy variant for the focused pane and a dim variant
// for unfocused panes").
func styleFor(f Focus, base tcell.Style) tcell.Style {
	if f == Unfocused {
		return base.Dim(true)
	}
	return base.Dim(false)
}

// Border draws a rectangular panel border into buf within rect, in the
// glyph/attribute set focus selects, with an optional title embedded in
// the top edge ("┌── Title ──┐" style). rect must be at least 2x2 to
// draw a real border with interior; smaller/degenerate rects (AC-11:
// zero, negative, 1-wide/1-tall) degrade to drawing whatever corners
// and edges fit rather than panicking — core.Buffer.Set already
// silently drops any coordinate outside the buffer, so the only
// degenerate case Border itself must guard is rect.W or rect.H being
// too small to have distinct corners, which the loop bounds below
// handle by construction (a 1-wide rect just draws its single vertical
// run twice, overwriting itself harmlessly; a <=0 rect draws nothing).
func Border(buf *core.Buffer, rect core.Rect, focus Focus, title string, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	g := glyphsFor(focus)
	style := styleFor(focus, base)

	x0, y0 := rect.X, rect.Y
	x1, y1 := rect.X+rect.W-1, rect.Y+rect.H-1

	// Top and bottom edges.
	for x := x0 + 1; x < x1; x++ {
		buf.Set(x, y0, g.horiz, style)
		if rect.H > 1 {
			buf.Set(x, y1, g.horiz, style)
		}
	}
	// Left and right edges.
	for y := y0 + 1; y < y1; y++ {
		buf.Set(x0, y, g.vert, style)
		if rect.W > 1 {
			buf.Set(x1, y, g.vert, style)
		}
	}
	// Corners.
	buf.Set(x0, y0, g.topLeft, style)
	if rect.W > 1 {
		buf.Set(x1, y0, g.topRight, style)
	}
	if rect.H > 1 {
		buf.Set(x0, y1, g.bottomLeft, style)
	}
	if rect.W > 1 && rect.H > 1 {
		buf.Set(x1, y1, g.bottomRight, style)
	}

	drawTitle(buf, rect, title, style)
}

// drawTitle overwrites a span of the top edge with " title " (space
// padded), starting two cells in from the top-left corner, clipped to
// the available width — a title longer than the border simply gets
// truncated to what fits rather than overflowing the pane (no
// allocation beyond the rune iteration, no panic on a degenerate/empty
// title).
func drawTitle(buf *core.Buffer, rect core.Rect, title string, style tcell.Style) {
	if title == "" || rect.W < 5 || rect.H < 1 {
		return
	}
	x0, y0 := rect.X, rect.Y
	x1 := rect.X + rect.W - 1
	// Available interior width for "  title  " between the corners.
	maxLabel := (x1 - 1) - (x0 + 2)
	if maxLabel < 1 {
		return
	}

	x := x0 + 2
	buf.Set(x, y0, ' ', style)
	x++
	n := 0
	for _, r := range title {
		if n >= maxLabel-2 {
			break
		}
		buf.Set(x, y0, r, style)
		x++
		n++
	}
	if x <= x1-1 {
		buf.Set(x, y0, ' ', style)
	}
}
