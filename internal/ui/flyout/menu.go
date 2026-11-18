package flyout

import (
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// Result is what one input did. It is a plain value; reading it never
// allocates.
type Result struct {
	// Handled is true when the menu consumed the input. While the menu is
	// open every key and click is handled (criterion 7); closed, a key is
	// never handled and only a click on the bar row is.
	Handled bool
	// Action is the ActionID of the action just picked, "" otherwise. The
	// menu has closed itself when it is set.
	Action string
	// Notice is the reason of a greyed entry that was just tried, "" otherwise.
	Notice string
	// Closed is true when this input closed the menu.
	Closed bool
}

// Menu is the menu's state. It is pure: it holds no engine, goroutine,
// clock or I/O, so one key press changes it and the very next Draw shows the
// result (criterion 25). It has no lock and is not safe for concurrent use;
// the caller owns it from one goroutine (the render and input path of the
// terminal). Copying a Menu copies its state, which is harmless.
type Menu struct {
	t *Table

	open  bool
	inBar bool // focus on the bar; list 1 is a preview with no highlight
	bar   int  // the highlighted bar entry
	n     int  // lists open, 1..MaxDepth
	hl    [MaxDepth]int
	off   [MaxDepth]int // first shown entry of each list (scroll)

	notice string

	// last picked path: bar entry then one highlight per list.
	rem    [MaxDepth + 1]int
	remLen int

	// visits counts entries read by layout, drawing and navigation; a test
	// reads it to prove the cost follows the rows shown, not the tree.
	visits int
}

// New builds a closed Menu over t. A nil t yields a Menu whose methods log
// MET-V991 and do nothing.
func New(t *Table) *Menu { return &Menu{t: t} }

func (m *Menu) built() bool {
	if m == nil || m.t == nil {
		_ = errs.New(ErrMenuNotBuilt, errs.NewCorrelationID(), nil)
		return false
	}
	return true
}

// IsOpen reports whether the menu is open.
func (m *Menu) IsOpen() bool { return m != nil && m.open }

// InBar reports whether the focus is on the bar (just opened, or after a
// bar entry was picked with the mouse hover) rather than inside a list.
func (m *Menu) InBar() bool { return m != nil && m.open && m.inBar }

// Notice is the reason of the greyed entry last tried, or "". It clears on
// the next input.
func (m *Menu) Notice() string {
	if m == nil {
		return ""
	}
	return m.notice
}

// Visits is the number of list entries read since the last ResetVisits.
func (m *Menu) Visits() int { return m.visits }

// ResetVisits zeroes the Visits counter.
func (m *Menu) ResetVisits() { m.visits = 0 }

// Path returns the highlighted bar entry then the highlight in each open
// list. It allocates and is meant for callers that need the state (hint row,
// tests), not for per-frame use.
func (m *Menu) Path() []int {
	if m == nil || !m.open {
		return nil
	}
	out := make([]int, 0, m.n+1)
	out = append(out, m.bar)
	out = append(out, m.hl[:m.n]...)
	return out
}

// Open opens the menu with the focus on the bar, on the bar entry last used.
// Opening an open menu does nothing.
func (m *Menu) Open() {
	if !m.built() || m.open {
		return
	}
	m.open = true
	m.inBar = true
	m.notice = ""
	m.bar = 0
	if m.remLen > 0 {
		m.bar = m.rem[0]
	}
	m.enterBar(m.bar)
}

// Close closes the menu. The last picked path is kept.
func (m *Menu) Close() {
	if m == nil {
		return
	}
	m.open = false
	m.inBar = false
	m.notice = ""
	m.n = 0
}

// Toggle opens a closed menu and closes an open one (the opener key).
func (m *Menu) Toggle() {
	if m.IsOpen() {
		m.Close()
		return
	}
	m.Open()
}

// listAt returns list k (0 is the bar's drop-down for the highlighted bar
// entry) by walking the highlights. Cost is the depth, never the tree size.
func (m *Menu) listAt(k int) *list {
	l := m.t.bar.entries[m.bar].sub
	for j := 0; j < k; j++ {
		l = l.entries[m.hl[j]].sub
	}
	return l
}

// initial is the highlight a list gets when entered: the remembered one when
// the path so far matches the last picked path, else the first selectable.
func (m *Menu) initial(k int, l *list) int {
	if m.remLen > k+1 && m.rem[0] == m.bar {
		same := true
		for j := 0; j < k; j++ {
			if m.rem[j+1] != m.hl[j] {
				same = false
				break
			}
		}
		if same {
			r := m.rem[k+1]
			if r >= 0 && r < len(l.entries) && l.entries[r].kind != KindSeparator {
				return r
			}
		}
	}
	return l.first
}

// enterBar makes bar entry b the current one with its list dropped.
func (m *Menu) enterBar(b int) {
	m.bar = b
	m.n = 1
	m.off[0] = 0
	m.hl[0] = m.initial(0, m.listAt(0))
}

// step moves the highlight of the deepest list by dir (1 or -1), wrapping and
// skipping separators.
func (m *Menu) step(dir int) {
	k := m.n - 1
	l := m.listAt(k)
	h := m.hl[k]
	for i := 0; i < len(l.entries); i++ {
		h += dir
		if h < 0 {
			h = len(l.entries) - 1
		} else if h >= len(l.entries) {
			h = 0
		}
		if l.entries[h].kind != KindSeparator {
			break
		}
	}
	m.hl[k] = h
}

// current is the highlighted entry of the deepest list.
func (m *Menu) current() *entry {
	k := m.n - 1
	return &m.listAt(k).entries[m.hl[k]]
}

// moveBar switches to the neighbouring bar entry (wrapping).
func (m *Menu) moveBar(dir int) {
	n := len(m.t.bar.entries)
	m.enterBar((m.bar + dir + n) % n)
}

// activate acts on the highlighted entry of the deepest list: a submenu
// opens, a greyed action reports its reason, an action is picked.
func (m *Menu) activate(r *Result) {
	e := m.current()
	switch {
	case e.kind == KindSubmenu:
		m.push(e)
	case e.grey != "":
		m.notice = e.grey
		r.Notice = e.grey
	default:
		m.remember()
		r.Action = e.action
		r.Closed = true
		m.Close()
	}
}

// push opens the sub-list of e under the highlighted entry.
func (m *Menu) push(e *entry) {
	if m.n >= MaxDepth { // unreachable for a validated table; fail safe
		return
	}
	k := m.n
	m.off[k] = 0
	m.hl[k] = m.initial(k, e.sub)
	m.n++
}

// remember records the current path as the last picked one.
func (m *Menu) remember() {
	m.rem[0] = m.bar
	copy(m.rem[1:], m.hl[:m.n])
	m.remLen = m.n + 1
}

// Press feeds one key to the menu. Closed, it handles nothing. Open, it
// handles every key, whether or not the key does anything, so a key the menu
// ignores never reaches the game underneath (criterion 7).
//
//	F10, Esc    close (Esc first goes back one level when inside a sub-list)
//	Up, Down    move the highlight, wrapping, skipping separators
//	Home, End   first and last entry
//	Left        back one level; in the first list, the previous bar entry
//	Right       enter a sub-list; otherwise the next bar entry
//	Enter       act on the highlighted entry (a submenu opens, an action is
//	            returned and the menu closes, a greyed entry gives its reason)
//	a letter    on the bar: that bar entry, focus into its list; in a list:
//	            jump to the entry with that letter (either case) and act
func (m *Menu) Press(k keys.Key) Result {
	if !m.built() || !m.open {
		return Result{}
	}
	r := Result{Handled: true}
	m.notice = ""
	switch k.Special {
	case "":
		if k.Rune != 0 {
			m.letter(k.Rune, &r)
		}
	case "F10":
		m.Close()
		r.Closed = true
	case "Esc":
		if m.inBar || m.n == 1 {
			m.Close()
			r.Closed = true
		} else {
			m.n--
		}
	case "Up", "Down":
		dir := 1
		if k.Special == "Up" {
			dir = -1
		}
		if m.inBar {
			m.inBar = false
			if dir < 0 {
				m.hl[0] = m.listAt(0).last
			}
		} else {
			m.step(dir)
		}
	case "Home", "End":
		if !m.inBar {
			l := m.listAt(m.n - 1)
			if k.Special == "Home" {
				m.hl[m.n-1] = l.first
			} else {
				m.hl[m.n-1] = l.last
			}
		}
	case "Left":
		switch {
		case m.inBar || m.n == 1:
			m.moveBar(-1)
		default:
			m.n--
		}
	case "Right":
		switch {
		case m.inBar:
			m.moveBar(1)
		case m.current().kind == KindSubmenu:
			m.push(m.current())
		case m.n == 1:
			m.moveBar(1)
		}
	case "Enter":
		if m.inBar {
			m.inBar = false
		} else {
			m.activate(&r)
		}
	}
	return r
}

// letter handles a rune key.
func (m *Menu) letter(ch rune, r *Result) {
	lc := unicode.ToLower(ch)
	if m.inBar {
		for i := range m.t.bar.entries {
			m.visits++
			if m.t.bar.entries[i].letter == lc {
				m.enterBar(i)
				m.inBar = false
				return
			}
		}
		return
	}
	k := m.n - 1
	l := m.listAt(k)
	for i := range l.entries {
		m.visits++
		if l.entries[i].kind != KindSeparator && l.entries[i].letter == lc {
			m.hl[k] = i
			m.activate(r)
			return
		}
	}
}
