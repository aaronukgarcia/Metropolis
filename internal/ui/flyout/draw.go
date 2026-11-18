package flyout

import (
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Border and marker glyphs. Every one is a single cell and printable, so
// core.Buffer.Set keeps it.
const (
	runeHighlight = '►' // marks the highlighted entry so it reads without colour
	runeSubmenu   = '>'
	runeEllipsis  = '…'
)

var (
	styleBase  = tcell.StyleDefault
	styleSel   = tcell.StyleDefault.Reverse(true)
	styleDim   = tcell.StyleDefault.Dim(true)
	styleSelDm = tcell.StyleDefault.Reverse(true).Dim(true)
)

// Draw draws the bar on the first row of area and, when the menu is open,
// each open list as a box. The bar row is blanked before it is drawn and each
// box is blanked in full (border included) before its text, so nothing drawn
// earlier shows through (BUG-1234). Nothing is written outside area, and
// Buffer.Set ignores anything outside the buffer, so any size is safe.
func (m *Menu) Draw(buf *core.Buffer, area core.Rect) {
	if !m.built() || buf == nil || area.W <= 0 || area.H <= 0 {
		return
	}
	lay := m.Layout(area)
	m.DrawLayout(buf, &lay)
}

// DrawLayout draws a Layout already computed by Layout (so a caller that also
// hit-tests the same frame computes it once).
func (m *Menu) DrawLayout(buf *core.Buffer, lay *Layout) {
	if !m.built() || buf == nil {
		return
	}
	area := lay.Area
	if area.W <= 0 || area.H <= 0 {
		return
	}
	m.drawBar(buf, area, lay)
	if !lay.Open {
		return
	}
	for k := 0; k < lay.N; k++ {
		if b := &lay.Boxes[k]; b.Collapsed {
			buf.Set(b.Rect.X, b.Rect.Y, runeHighlight, styleBase)
		} else {
			m.drawBox(buf, b, area)
		}
	}
}

func (m *Menu) drawBar(buf *core.Buffer, area core.Rect, lay *Layout) {
	fillRect(buf, core.Rect{X: area.X, Y: area.Y, W: area.W, H: 1}, styleBase)
	for i := range m.t.bar.entries {
		x, w, ok := lay.BarCell(i)
		if !ok {
			break
		}
		e := &m.t.bar.entries[i]
		st := styleBase
		lead, trail := ' ', ' '
		if m.open && i == m.bar {
			st = styleSel
			lead, trail = '[', ']'
		}
		fillRect(buf, core.Rect{X: x, Y: area.Y, W: w, H: 1}, st)
		buf.Set(x, area.Y, lead, st)
		if w >= 2 {
			buf.Set(x+1, area.Y, unicode.ToUpper(e.letter), st)
		}
		drawFit(buf, x+1+letterCols, area.Y, w-2-letterCols, e.label, st, e.letterAt)
		if w >= e.labelW+2+letterCols {
			buf.Set(x+w-1, area.Y, trail, st)
		}
		m.visits++
	}
}

func (m *Menu) drawBox(buf *core.Buffer, b *Box, area core.Rect) {
	r := b.Rect
	if r.W < 3 || r.H < 3 {
		return
	}
	// Blank the whole rectangle first: whatever was under it is gone.
	fillRect(buf, r, styleBase)
	// Border.
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		buf.Set(x, r.Y, '─', styleBase)
		buf.Set(x, r.Y+r.H-1, '─', styleBase)
	}
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		buf.Set(r.X, y, '│', styleBase)
		buf.Set(r.X+r.W-1, y, '│', styleBase)
	}
	buf.Set(r.X, r.Y, '┌', styleBase)
	buf.Set(r.X+r.W-1, r.Y, '┐', styleBase)
	buf.Set(r.X, r.Y+r.H-1, '└', styleBase)
	buf.Set(r.X+r.W-1, r.Y+r.H-1, '┘', styleBase)

	l := b.list
	deepest := b.Level == m.n-1
	row := r.Y + 1
	if b.Above {
		drawMore(buf, r.X+2, row, r.W-4, b.Off, true)
		row++
	}
	for i := 0; i < b.Vis; i++ {
		idx := b.Off + i
		e := &l.entries[idx]
		m.visits++
		if e.kind == KindSeparator {
			buf.Set(r.X, row, '├', styleBase)
			buf.Set(r.X+r.W-1, row, '┤', styleBase)
			for x := r.X + 1; x < r.X+r.W-1; x++ {
				buf.Set(x, row, '─', styleBase)
			}
			row++
			continue
		}
		picked := idx == m.hl[b.Level]
		if m.inBar && b.Level == 0 {
			picked = false
		}
		st := styleBase
		if e.grey != "" {
			st = styleDim
		}
		if picked && deepest {
			st = styleSel
			if e.grey != "" {
				st = styleSelDm
			}
			fillRect(buf, core.Rect{X: r.X + 1, Y: row, W: r.W - 2, H: 1}, st)
		}
		x0 := r.X + 1
		if picked {
			buf.Set(x0, row, runeHighlight, st)
		}
		if x0+1 < r.X+r.W-1 {
			buf.Set(x0+1, row, unicode.ToUpper(e.letter), st)
		}
		lx := x0 + 1 + letterCols
		drawFit(buf, lx, row, b.LabelW, e.label, st, e.letterAt)
		if b.RightW > 0 {
			rx := lx + b.LabelW + 2
			tw := e.rightW
			if tw > b.RightW {
				tw = b.RightW
			}
			drawFit(buf, rx+b.RightW-tw, row, b.RightW, e.right, st, -1)
		}
		if b.Arrow && e.kind == KindSubmenu {
			ax := lx + b.LabelW + 1
			if b.RightW > 0 {
				ax += 2 + b.RightW
			}
			buf.Set(ax, row, runeSubmenu, st)
		}
		row++
	}
	if b.Below {
		drawMore(buf, r.X+2, row, r.W-4, len(l.entries)-(b.Off+b.Vis), false)
	}
	if b.Foot {
		drawFit(buf, r.X+2, r.Y+r.H-2, r.W-4, m.notice, styleBase, -1)
	}
}

// fillRect writes a blank cell over every cell of r.
func fillRect(buf *core.Buffer, r core.Rect, st tcell.Style) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			buf.Set(x, y, ' ', st)
		}
	}
}

// drawFit writes s at (x, y) in at most w cells, ending with an ellipsis when
// it had to be cut. The rune at index ul (when not negative) is underlined,
// which is how an entry's letter shows without colour.
func drawFit(buf *core.Buffer, x, y, w int, s string, st tcell.Style, ul int) {
	if w <= 0 {
		return
	}
	cut := utf8.RuneCountInString(s) > w
	i := 0
	for _, r := range s {
		if i >= w {
			return
		}
		if cut && i == w-1 {
			buf.Set(x+i, y, runeEllipsis, st)
			return
		}
		cs := st
		if i == ul {
			cs = st.Underline(true)
		}
		buf.Set(x+i, y, r, cs)
		i++
	}
}

// drawMore writes the "... N more above/below" marker line.
func drawMore(buf *core.Buffer, x, y, w, n int, above bool) {
	if w <= 0 {
		return
	}
	var b [40]byte
	k := copy(b[:], "... ")
	var d [12]byte
	j := len(d)
	if n == 0 {
		j--
		d[j] = '0'
	}
	for n > 0 && j > 0 {
		j--
		d[j] = byte('0' + n%10)
		n /= 10
	}
	k += copy(b[k:], d[j:])
	if above {
		k += copy(b[k:], " more above")
	} else {
		k += copy(b[k:], " more below")
	}
	if k > w {
		k = w
		if k > 0 {
			b[k-1] = '.'
		}
	}
	for i := 0; i < k; i++ {
		buf.Set(x+i, y, rune(b[i]), styleDim)
	}
}
