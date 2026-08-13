package core

import (
	"testing"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

func TestBuffer_SetGetOutOfRangeIgnored(t *testing.T) {
	b := NewBuffer(5, 5)
	b.Set(2, 2, 'A', tcell.StyleDefault)
	if c := b.Get(2, 2); c.Rune != 'A' {
		t.Fatalf("Get(2,2) = %+v, want rune 'A'", c)
	}

	// Out-of-range Set must not panic and must be a no-op.
	b.Set(-1, 0, 'X', tcell.StyleDefault)
	b.Set(0, -1, 'X', tcell.StyleDefault)
	b.Set(100, 100, 'X', tcell.StyleDefault)

	if c := b.Get(100, 100); c != (Cell{}) {
		t.Fatalf("Get out of range = %+v, want zero Cell", c)
	}
}

func TestBuffer_ResizePreservesUsability(t *testing.T) {
	b := NewBuffer(3, 3)
	b.Set(1, 1, 'Z', tcell.StyleDefault)
	b.Resize(10, 2)
	w, h := b.Size()
	if w != 10 || h != 2 {
		t.Fatalf("Size() = %d,%d want 10,2", w, h)
	}
	// Resize blank-fills; the old 'Z' must not leak through at a
	// coordinate that still exists post-resize.
	if c := b.Get(1, 1); c.Rune != ' ' {
		t.Fatalf("post-resize Get(1,1) = %+v, want blank", c)
	}
}

// TestBuffer_Set_StripsControlAndEscapeRunes is SEC-011's core
// regression: Buffer.Set is the single chokepoint every drawText/
// drawRow/drawTitle routine in ui.core/ui.widgets/ui.screens.debug
// funnels through, so a raw ESC (or any other non-printable control
// byte) handed to Set must never survive into a Cell — otherwise
// Flush would reproduce the original byte sequence on the real
// terminal via SetContent, i.e. terminal-escape injection.
func TestBuffer_Set_StripsControlAndEscapeRunes(t *testing.T) {
	b := NewBuffer(10, 1)
	controls := []rune{0x1B /* ESC */, '\r', '\a' /* BEL */, '\t', '\n', 0x00, 0x9B /* C1 CSI */}
	for x, r := range controls {
		b.Set(x, 0, r, tcell.StyleDefault)
		got := b.Get(x, 0).Rune
		if got == r {
			t.Fatalf("Set(%d,0,%U) left the raw control rune in the cell: got %U, want it replaced", x, r, got)
		}
		if unicode.IsPrint(got) == false {
			t.Fatalf("Set(%d,0,%U) replacement rune %U is itself non-printable", x, r, got)
		}
	}
}

// TestBuffer_Set_PreservesOwnGlyphs is the false-positive check: every
// deliberate box-drawing/block/geometric-shape glyph this package
// draws itself (border.go's box-drawing set, sparkline.go's block
// levels, bignum.go's delta arrows, queuelane.go's cargo glyphs) must
// pass through Set unmodified — the sanitiser must not mistake a
// legitimate Unicode symbol for a control character.
func TestBuffer_Set_PreservesOwnGlyphs(t *testing.T) {
	glyphs := []rune{
		// border.go box-drawing (light + heavy).
		'─', '│', '┌', '┐', '└', '┘', '├', '┤', '━', '┃', '┏', '┓', '┗', '┛',
		// sparkline.go block levels.
		'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█',
		// bignum.go delta arrows.
		'▬', '▲', '▼',
		// queuelane.go cargo glyphs.
		'▮', '◆', '●', '▩',
	}
	b := NewBuffer(len(glyphs), 1)
	for x, r := range glyphs {
		b.Set(x, 0, r, tcell.StyleDefault)
		if got := b.Get(x, 0).Rune; got != r {
			t.Fatalf("Set(%d,0,%U) = %U, want the glyph preserved unmodified", x, r, got)
		}
	}
}

func TestBuffer_Fill(t *testing.T) {
	b := NewBuffer(4, 4)
	b.Fill('#', tcell.StyleDefault)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if c := b.Get(x, y); c.Rune != '#' {
				t.Fatalf("Get(%d,%d) = %+v, want '#'", x, y, c)
			}
		}
	}
}
