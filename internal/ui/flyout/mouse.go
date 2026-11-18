package flyout

import "github.com/aaronukgarcia/Metropolis/internal/ui/core"

// HitKind says what a position is over.
type HitKind int

const (
	// HitOutside is a position over neither the bar nor an open box.
	HitOutside HitKind = iota
	// HitBar is a bar entry; Bar says which.
	HitBar
	// HitBarRow is the bar row where no entry is (the gap or the rest of it).
	HitBarRow
	// HitItem is a list entry; Level and Index say which.
	HitItem
	// HitBox is a box's border, marker line, separator or footer: inside the
	// menu, but no entry.
	HitBox
)

// Hit is the answer of Layout.Hit.
type Hit struct {
	Kind  HitKind
	Bar   int // for HitBar
	Level int // for HitItem and HitBox: which open list
	Index int // for HitItem: the entry index within that list
}

// Hit reports what the cell (x, y) is over in this layout. It is a pure
// function of the layout: no menu state is read or changed. Boxes are tested
// deepest first, since a sub-list can overlap its parent when it was clamped.
func (l *Layout) Hit(x, y int) Hit {
	if l.t == nil {
		return Hit{}
	}
	a := l.Area
	if y == a.Y && x >= a.X && x < a.X+a.W {
		for i := range l.t.barX {
			bx, bw, ok := l.BarCell(i)
			if !ok {
				break
			}
			if x >= bx && x < bx+bw {
				return Hit{Kind: HitBar, Bar: i}
			}
		}
		return Hit{Kind: HitBarRow}
	}
	if !l.Open {
		return Hit{}
	}
	for k := l.N - 1; k >= 0; k-- {
		b := &l.Boxes[k]
		r := b.Rect
		if r.H == 0 || x < r.X || x >= r.X+r.W || y < r.Y || y >= r.Y+r.H {
			continue
		}
		if x == r.X || x == r.X+r.W-1 || y == r.Y || y == r.Y+r.H-1 {
			return Hit{Kind: HitBox, Level: k}
		}
		row := y - r.Y - 1
		if b.Above {
			row--
		}
		if row < 0 || row >= b.Vis {
			return Hit{Kind: HitBox, Level: k}
		}
		idx := b.Off + row
		if b.list.entries[idx].kind == KindSeparator {
			return Hit{Kind: HitBox, Level: k}
		}
		return Hit{Kind: HitItem, Level: k, Index: idx}
	}
	return Hit{}
}

// Click handles a left click at (x, y) inside area. Closed, only a click on
// the bar row is handled: it opens the menu (on the entry clicked, if any).
// Open, every click is handled: a bar entry drops its list, a list entry runs
// or opens, a click on a box's border does nothing, and a click anywhere
// else closes the menu and is swallowed (so it moves no cursor and reaches
// no screen).
func (m *Menu) Click(area core.Rect, x, y int) Result {
	if !m.built() {
		return Result{}
	}
	lay := m.Layout(area)
	h := lay.Hit(x, y)
	if !m.open {
		if h.Kind != HitBar && h.Kind != HitBarRow {
			return Result{}
		}
		m.Open()
		if h.Kind == HitBar {
			m.enterBar(h.Bar)
			m.inBar = false
		}
		return Result{Handled: true}
	}
	r := Result{Handled: true}
	m.notice = ""
	switch h.Kind {
	case HitBar:
		m.enterBar(h.Bar)
		m.inBar = false
	case HitItem:
		m.inBar = false
		m.n = h.Level + 1
		m.hl[h.Level] = h.Index
		m.activate(&r)
	case HitBarRow, HitBox:
		// inside the menu, on nothing: no change
	default:
		m.Close()
		r.Closed = true
	}
	return r
}

// Hover handles pointer motion at (x, y). Open, the entry under the pointer
// becomes the highlight (a bar entry switches the open list; an entry of the
// deepest list moves its highlight; a parent list's entry is left alone so a
// diagonal move toward a sub-list never closes it). Nothing is opened, run or
// closed by hovering, and with no motion events the menu works unchanged.
// It reports whether the state changed.
func (m *Menu) Hover(area core.Rect, x, y int) bool {
	if !m.built() || !m.open {
		return false
	}
	lay := m.Layout(area)
	h := lay.Hit(x, y)
	switch h.Kind {
	case HitBar:
		if h.Bar == m.bar && m.n == 1 {
			return false
		}
		m.notice = ""
		m.enterBar(h.Bar)
		return true
	case HitItem:
		if h.Level != m.n-1 || (h.Index == m.hl[h.Level] && !m.inBar) {
			return false
		}
		m.notice = ""
		m.inBar = false
		m.hl[h.Level] = h.Index
		return true
	}
	return false
}
