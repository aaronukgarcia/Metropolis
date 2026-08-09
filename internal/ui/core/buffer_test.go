package core

import (
	"testing"

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
