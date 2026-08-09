package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestBorder_FocusedUsesHeavyGlyphs(t *testing.T) {
	buf := core.NewBuffer(5, 3)
	Border(buf, core.Rect{X: 0, Y: 0, W: 5, H: 3}, Focused, "", tcell.StyleDefault)

	g := heavyGlyphs
	want := []string{
		string(g.topLeft) + string(g.horiz) + string(g.horiz) + string(g.horiz) + string(g.topRight),
		string(g.vert) + "   " + string(g.vert),
		string(g.bottomLeft) + string(g.horiz) + string(g.horiz) + string(g.horiz) + string(g.bottomRight),
	}
	assertGrid(t, buf, want)
}

func TestBorder_UnfocusedUsesLightGlyphsAndDimAttribute(t *testing.T) {
	buf := core.NewBuffer(5, 3)
	Border(buf, core.Rect{X: 0, Y: 0, W: 5, H: 3}, Unfocused, "", tcell.StyleDefault)

	g := lightGlyphs
	want := []string{
		string(g.topLeft) + string(g.horiz) + string(g.horiz) + string(g.horiz) + string(g.topRight),
		string(g.vert) + "   " + string(g.vert),
		string(g.bottomLeft) + string(g.horiz) + string(g.horiz) + string(g.horiz) + string(g.bottomRight),
	}
	assertGrid(t, buf, want)

	_, _, attrs := buf.Get(0, 0).Style.Decompose()
	if attrs&tcell.AttrDim == 0 {
		t.Fatalf("unfocused border corner style has no Dim attribute: %v", attrs)
	}

	// Focused must NOT carry Dim, so the two states are distinguishable
	// by attribute as well as glyph.
	buf2 := core.NewBuffer(5, 3)
	Border(buf2, core.Rect{X: 0, Y: 0, W: 5, H: 3}, Focused, "", tcell.StyleDefault)
	_, _, attrs2 := buf2.Get(0, 0).Style.Decompose()
	if attrs2&tcell.AttrDim != 0 {
		t.Fatalf("focused border corner style unexpectedly carries Dim: %v", attrs2)
	}
}

func TestBorder_TitleEmbeddedInTopEdge(t *testing.T) {
	buf := core.NewBuffer(10, 3)
	Border(buf, core.Rect{X: 0, Y: 0, W: 10, H: 3}, Focused, "Cash", tcell.StyleDefault)

	g := heavyGlyphs
	wantTop := string(g.topLeft) + string(g.horiz) + " Cash " + string(g.horiz) + string(g.topRight)
	got := gridRunes(buf)
	if got[0] != wantTop {
		t.Fatalf("top row = %q, want %q", got[0], wantTop)
	}
}

func TestBorder_DegenerateRectDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(5, 5)
	Border(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, Focused, "x", tcell.StyleDefault)
	Border(buf, core.Rect{X: 0, Y: 0, W: -3, H: -3}, Unfocused, "x", tcell.StyleDefault)
	Border(buf, core.Rect{X: 0, Y: 0, W: 1, H: 1}, Focused, "x", tcell.StyleDefault)
	Border(nil, core.Rect{X: 0, Y: 0, W: 5, H: 5}, Focused, "x", tcell.StyleDefault)
}
