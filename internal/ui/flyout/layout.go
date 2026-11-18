package flyout

import "github.com/aaronukgarcia/Metropolis/internal/ui/core"

// Box is where one open list is drawn and what part of it shows.
type Box struct {
	// Rect includes the border. A zero H means the list does not fit at all
	// (the area is too small) and nothing is drawn or hit for it.
	Rect core.Rect
	// Level is 0 for the bar's drop-down, 1 for its sub-list, ...
	Level int
	// Off is the index of the first entry shown; Vis how many entries show.
	Off, Vis int
	// Above and Below say that rows are hidden, shown as a marker line that
	// takes one of the box's rows.
	Above, Below bool
	// Foot is true when the box carries the footer line for a greyed
	// entry's reason.
	Foot bool
	// LabelW and RightW are the column widths after any cutting; Arrow is
	// true when a sub-list marker column exists.
	LabelW, RightW int
	Arrow          bool
	// Collapsed is true for an old level squeezed to a one-column strip: Rect
	// is that single cell, on the level's highlighted row, and only the
	// highlight marker is drawn there.
	Collapsed bool
	list      *list
}

// Layout is every position the menu uses for one frame: the bar row, the
// bar entry cells and one Box per open list. It is a value with no slices of
// its own, so computing it does not allocate.
type Layout struct {
	Area core.Rect
	// Open is true when the menu is open; N boxes are valid then.
	Open  bool
	N     int
	Boxes [MaxDepth]Box
	t     *Table
}

// BarCell returns the cells of bar entry i on the bar row, or ok=false when
// the entry starts beyond the area. The cell is clipped to the area.
func (l *Layout) BarCell(i int) (x, w int, ok bool) {
	if l.t == nil || i < 0 || i >= len(l.t.barX) {
		return 0, 0, false
	}
	x = l.Area.X + l.t.barX[i]
	w = l.t.barW[i]
	if x >= l.Area.X+l.Area.W {
		return 0, 0, false
	}
	if x+w > l.Area.X+l.Area.W {
		w = l.Area.X + l.Area.W - x
	}
	return x, w, w > 0
}

// Layout constants (the one place they are stated).
const (
	// minLabelRunes is the fewest label runes a shrunk box keeps; the marker
	// and letter columns are never cut.
	minLabelRunes = 4
	// stripW is the width of a collapsed level: one column showing only the
	// highlighted row's marker.
	stripW = 1
	// minBoxRows is the fewest rows (border, one entry, border) a box needs.
	minBoxRows = 3
)

// Layout works out where everything goes inside area. The bar is the area's
// top row; lists drop below it and never leave the area. The open lists form
// one left-to-right cascade whose boxes never share a column: level k+1
// starts where level k ends. When the natural widths do not fit, the whole
// cascade shifts left (level 0 may start left of its bar entry), then boxes
// shrink widest first (right column first, then the label, never the marker or
// letter column, to minLabelRunes label runes), and as a last resort the
// oldest levels collapse to a stripW column that shows only the highlight
// marker, so the deepest list is always usable. It also scrolls each list so
// its highlight shows (the one piece of state it updates). Cost depends on the
// number of open lists, not the size of the tree.
func (m *Menu) Layout(area core.Rect) Layout {
	lay := Layout{Area: area}
	if !m.built() {
		return lay
	}
	lay.t = m.t
	if !m.open || area.W <= 0 || area.H <= 0 {
		return lay
	}
	lay.Open = true
	right := area.X + area.W
	maxY := area.Y + area.H - 1

	// Pass 1: natural and minimum width of every open list.
	var wNat, wMin, fix [MaxDepth]int
	for k := 0; k < m.n; k++ {
		l := m.listAt(k)
		b := &lay.Boxes[k]
		b.Level = k
		b.list = l
		b.Foot = m.notice != "" && k == m.n-1
		b.Arrow = l.anySub
		// the highlight marker, the letter column (letter and a space) and one
		// cell of padding: none of these is ever the part that is cut
		f := 2 + letterCols
		if b.Arrow {
			f += 2
		}
		fix[k] = f
		w := 2 + f + l.labelW
		if l.rightW > 0 {
			w += 2 + l.rightW
		}
		if len(l.entries) > area.H-3 {
			// The list may scroll: be wide enough for "... N more above".
			if mw := 2 + 2 + markerWidth(len(l.entries)); w < mw {
				w = mw
			}
		}
		wNat[k] = w
		ml := l.labelW
		if ml > minLabelRunes {
			ml = minLabelRunes
		}
		wMin[k] = 2 + f + ml
		if wMin[k] > w {
			wMin[k] = w
		}
	}

	// Pass 2: how many of the oldest levels collapse, then the widths and the
	// cascade's first column.
	c := 0
	for ; c < m.n-1; c++ {
		need := c
		for k := c; k < m.n; k++ {
			need += wMin[k]
		}
		if need <= area.W {
			break
		}
	}
	avail := area.W - c
	var wd [MaxDepth]int
	sum := 0
	for k := c; k < m.n; k++ {
		wd[k] = wNat[k]
		sum += wd[k]
	}
	for sum > avail {
		wi := -1
		for k := c; k < m.n; k++ {
			if wd[k] > wMin[k] && (wi < 0 || wd[k] > wd[wi]) {
				wi = k
			}
		}
		if wi < 0 {
			break
		}
		wd[wi]--
		sum--
	}
	if sum > avail {
		// Only reachable for the deepest list alone in a very narrow area.
		wd[m.n-1] -= sum - avail
		if wd[m.n-1] < 0 {
			wd[m.n-1] = 0
		}
		sum = avail
	}
	var colX [MaxDepth]int
	x := area.X + c
	if c == 0 {
		x = area.X + m.t.barX[m.bar]
		if x+sum > right {
			x = right - sum
		}
		if x < area.X {
			x = area.X
		}
	}
	for k := 0; k < m.n; k++ {
		if k < c {
			colX[k] = area.X + k
			continue
		}
		colX[k] = x
		x += wd[k]
	}

	// Pass 3: columns, then rows. A box's top row is its parent's highlighted
	// row, moved up only as far as its rows need.
	var hrow [MaxDepth]int // screen row of each level's highlighted entry
	for k := 0; k < m.n; k++ {
		l := m.listAt(k)
		b := &lay.Boxes[k]
		w := wd[k]
		if k < c {
			w = wNat[k]
		}
		lw, rw := l.labelW, l.rightW
		rest := w - 2 - fix[k]
		if rest < 0 {
			rest = 0
		}
		gap := 0
		if rw > 0 {
			gap = 2
		}
		if lw+gap+rw > rest {
			rw -= lw + gap + rw - rest
			if rw < 0 {
				rw = 0
			}
			gap = 0
			if rw > 0 {
				gap = 2
			}
			if lw+gap+rw > rest {
				lw = rest - gap - rw
				if lw < 0 {
					lw = 0
				}
			}
		}
		b.LabelW, b.RightW = lw, rw
		var y int
		if k == 0 {
			y = area.Y + 1
		} else {
			y = hrow[k-1] - 1
		}
		// Height: all entries plus borders (and the footer), shifted up when
		// it would cross the bottom, then cut and scrolled.
		h := len(l.entries) + 2
		if b.Foot {
			h++
		}
		if y+h-1 > maxY {
			y = maxY - h + 1
		}
		if y < area.Y+1 {
			y = area.Y + 1
		}
		if y+h-1 > maxY {
			h = maxY - y + 1
		}
		capRows := h - 2
		if b.Foot {
			capRows--
		}
		if h < minBoxRows || w < 3 || capRows < 1 {
			// No room for even one row: leave a zero box (nothing drawn).
			b.Rect = core.Rect{X: colX[k], Y: y}
			hrow[k] = y + 1 + m.hl[k] - m.off[k]
			continue
		}
		m.off[k], b.Vis, b.Above, b.Below = window(len(l.entries), capRows, m.hl[k], m.off[k])
		b.Off = m.off[k]
		// Shrink the box to the rows shown when scrolled less than full.
		used := b.Vis + 2
		if b.Above {
			used++
		}
		if b.Below {
			used++
		}
		if b.Foot {
			used++
		}
		if used < h {
			h = used
		}
		hr := y + 1 + m.hl[k] - b.Off
		if b.Above {
			hr++
		}
		hrow[k] = hr
		m.visits += b.Vis
		if k < c {
			b.Collapsed = true
			b.Rect = core.Rect{X: colX[k], Y: hr, W: stripW, H: 1}
			continue
		}
		b.Rect = core.Rect{X: colX[k], Y: y, W: w, H: h}
	}
	lay.N = m.n
	return lay
}

// disjoint reports whether no two open boxes share a cell and every box lies
// inside the area. It is the layout's invariant, checked by the tests.
func (l *Layout) disjoint() bool {
	for i := 0; i < l.N; i++ {
		a := l.Boxes[i].Rect
		if a.H == 0 {
			continue
		}
		if a.X < l.Area.X || a.Y < l.Area.Y || a.X+a.W > l.Area.X+l.Area.W || a.Y+a.H > l.Area.Y+l.Area.H {
			return false
		}
		for j := i + 1; j < l.N; j++ {
			o := l.Boxes[j].Rect
			if o.H == 0 {
				continue
			}
			if a.X < o.X+o.W && o.X < a.X+a.W && a.Y < o.Y+o.H && o.Y < a.Y+a.H {
				return false
			}
		}
	}
	return true
}

// window picks the scroll offset and the rows shown for a list of n entries
// in capRows rows so the highlight h is inside the window. A row is spent on
// each marker line ("... N more above/below") that is needed. It returns the
// offset, the entries shown and the two marker flags.
func window(n, capRows, h, off int) (int, int, bool, bool) {
	if n <= capRows {
		return 0, n, false, false
	}
	if capRows < 3 {
		// No room for markers: just keep the highlight in view.
		if h < off {
			off = h
		}
		if h >= off+capRows {
			off = h - capRows + 1
		}
		return clampOff(off, n, capRows), capRows, false, false
	}
	for pass := 0; pass < 4; pass++ {
		off = clampOff(off, n, capRows-1)
		above := off > 0
		vis := capRows
		if above {
			vis--
		}
		below := off+vis < n
		if below {
			vis--
		}
		switch {
		case h < off:
			off = h
		case h >= off+vis:
			off = h - vis + 1
		default:
			return off, vis, above, below
		}
	}
	// Settled after the passes above (the loop converges in two); recompute.
	above := off > 0
	vis := capRows
	if above {
		vis--
	}
	below := off+vis < n
	if below {
		vis--
	}
	return off, vis, above, below
}

// markerWidth is the cells of the longest marker line a list of n entries can
// show: "... N more above" with N up to n.
func markerWidth(n int) int {
	d := 1
	for v := n; v >= 10; v /= 10 {
		d++
	}
	return len("... ") + d + len(" more above")
}

func clampOff(off, n, rows int) int {
	if off > n-rows {
		off = n - rows
	}
	if off < 0 {
		off = 0
	}
	return off
}
