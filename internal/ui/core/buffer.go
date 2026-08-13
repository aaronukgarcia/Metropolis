package core

import (
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// replacementRune is drawn in place of any non-printable rune Set is
// asked to write — SEC-011: this package is the single trust boundary
// between caller-supplied strings (which may carry raw control/escape
// bytes, e.g. a wrapped Go error's text reaching the F12 error-tail
// pane via errs.Entry.Msg) and the real terminal, which would
// reconstruct a raw ESC-led byte sequence from consecutive cells and
// execute it (cursor repositioning, scrollback clearing, OSC
// title-bar spoofing, terminal-emulator-specific exploits). U+FFFD
// (the Unicode replacement character) is itself unicode.IsPrint==true,
// so it never re-triggers the same filter.
const replacementRune = '�'

// sanitizeRune returns r unchanged if it is safe to hand to a real
// terminal (unicode.IsPrint==true — this already covers every
// box-drawing, block-element, and geometric-shape glyph this package
// draws itself: border.go's ─│┌┐└┘├┤━┃┏┓┗┛, sparkline.go's ▁▂▃▄▅▆▇█,
// bignum.go's ▬▲▼, and queuelane.go's ▮◆●▲▩ are all category L/M/N/P/S
// and pass IsPrint unmodified — verified by direct rune check, not
// assumed), and replacementRune otherwise. This is the ONE chokepoint
// SEC-011 asks for: every drawText/drawRow/drawTitle routine in
// ui.core, ui.widgets, and ui.screens.debug funnels through Buffer.Set
// before a rune becomes a Cell, so filtering here enforces the
// untrusted-text trust boundary centrally rather than per widget.
func sanitizeRune(r rune) rune {
	if !unicode.IsPrint(r) {
		return replacementRune
	}
	return r
}

// Cell is one styled terminal cell: a single primary rune (combining
// runes are not modelled at the Buffer level — v1 scope, UI-SPEC §1) and
// a tcell.Style. It is a small value type deliberately, so a Buffer's
// backing slice never boxes cells through an interface (doc.go's
// zero-allocation note).
type Cell struct {
	Rune  rune
	Style tcell.Style
}

// blankCell is what Buffer.Resize and NewBuffer fill with: a space on
// the default style, matching tcell's own notion of an empty cell.
var blankCell = Cell{Rune: ' ', Style: tcell.StyleDefault}

// Buffer is a flat, pre-sized grid of styled Cells covering one full
// terminal-sized rectangle. It has no notion of "front" or "back" —
// that distinction lives in how a caller uses a pair of Buffers (see
// diff.go's Flush) — so the same type serves both roles.
type Buffer struct {
	w, h  int
	cells []Cell
}

// NewBuffer returns a Buffer of the given size, every cell blank. w and
// h are clamped to 0 if negative.
func NewBuffer(w, h int) *Buffer {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	b := &Buffer{w: w, h: h, cells: make([]Cell, w*h)}
	b.Fill(blankCell.Rune, blankCell.Style)
	return b
}

// Size returns the buffer's width and height.
func (b *Buffer) Size() (int, int) { return b.w, b.h }

func (b *Buffer) idx(x, y int) (int, bool) {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return 0, false
	}
	return y*b.w + x, true
}

// Set writes a cell at (x, y). Out-of-range coordinates are silently
// ignored (matching tcell.Screen.SetContent's own out-of-range
// behaviour), never a panic — a widget computing a coordinate slightly
// off during a resize race must not be able to crash T-RENDER.
//
// SEC-011: r is passed through sanitizeRune first, so any non-printable
// rune (raw ESC 0x1B, CR, BEL, and every other C0/C1 control or format
// character) is replaced before it can ever reach a Cell and, via
// Flush, tcell.Screen.SetContent — this is the single point every
// render path in ui.core/ui.widgets/ui.screens.debug funnels through,
// so the terminal-escape-injection trust boundary is enforced once,
// here, rather than needing every widget author to remember it.
func (b *Buffer) Set(x, y int, r rune, style tcell.Style) {
	if i, ok := b.idx(x, y); ok {
		b.cells[i] = Cell{Rune: sanitizeRune(r), Style: style}
	}
}

// Get returns the cell at (x, y), or the zero Cell if out of range.
func (b *Buffer) Get(x, y int) Cell {
	i, ok := b.idx(x, y)
	if !ok {
		return Cell{}
	}
	return b.cells[i]
}

// Fill sets every cell in the buffer to (r, style). This is a Buffer
// (data-model) operation, not a screen operation — it never touches a
// tcell.Screen and is not the "clear" that UI-SPEC §1 forbids on the
// flush path (Flush in diff.go never calls this or anything like it on
// the screen side).
func (b *Buffer) Fill(r rune, style tcell.Style) {
	for i := range b.cells {
		b.cells[i] = Cell{Rune: r, Style: style}
	}
}

// Resize reallocates the buffer to (w, h), blank-filled. Used on
// terminal resize (render.go); front and back buffers must always be
// resized together and then reconciled by a full Sync rather than a
// partial Flush (the very next Flush after a resize necessarily rewrites
// every cell, since the old front no longer corresponds to any prior
// on-screen state at the new dimensions).
func (b *Buffer) Resize(w, h int) {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	b.w, b.h = w, h
	need := w * h
	if cap(b.cells) >= need {
		b.cells = b.cells[:need]
	} else {
		b.cells = make([]Cell, need)
	}
	b.Fill(blankCell.Rune, blankCell.Style)
}
