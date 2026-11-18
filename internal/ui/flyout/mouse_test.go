package flyout

import (
	"reflect"
	"testing"
)

// clickAt clicks the middle of the first row of the entry the layout says is
// at (level, index).
func itemPos(t *testing.T, m *Menu, level, index int) (int, int) {
	t.Helper()
	lay := m.Layout(fullArea(testW, testH))
	b := lay.Boxes[level]
	row := index - b.Off
	if b.Above {
		row++
	}
	if row < 0 || row >= b.Vis+1 {
		t.Fatalf("entry %d of level %d not shown", index, level)
	}
	return b.Rect.X + 2, b.Rect.Y + 1 + row
}

func barPos(t *testing.T, m *Menu, i int) (int, int) {
	t.Helper()
	lay := m.Layout(fullArea(testW, testH))
	x, w, ok := lay.BarCell(i)
	if !ok {
		t.Fatalf("bar entry %d not on screen", i)
	}
	return x + w/2, 0
}

// Criterion 19-23 (pure side): hit testing.
func TestHit_BarItemBoxAndOutside(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	lay := m.Layout(fullArea(testW, testH))
	if h := lay.Hit(2, 0); h.Kind != HitBar || h.Bar != 0 {
		t.Fatalf("bar hit = %+v", h)
	}
	if h := lay.Hit(10, 0); h.Kind != HitBar || h.Bar != 1 {
		t.Fatalf("second bar entry hit = %+v", h)
	}
	if h := lay.Hit(testW-1, 0); h.Kind != HitBarRow {
		t.Fatalf("empty bar row hit = %+v", h)
	}
	b0, b1 := lay.Boxes[0].Rect, lay.Boxes[1].Rect
	if h := lay.Hit(b0.X+3, b0.Y+2); h.Kind != HitItem || h.Level != 0 || h.Index != 1 {
		t.Fatalf("Jobs hit = %+v", h)
	}
	if h := lay.Hit(b1.X+3, b1.Y+1); h.Kind != HitItem || h.Level != 1 || h.Index != 0 {
		t.Fatalf("Dwelling hit = %+v", h)
	}
	if h := lay.Hit(b0.X, b0.Y+1); h.Kind != HitBox {
		t.Fatalf("border hit = %+v", h)
	}
	// The separator row is inside the box but is no entry.
	if h := lay.Hit(b0.X+3, b0.Y+3); h.Kind != HitBox {
		t.Fatalf("separator hit = %+v", h)
	}
	if h := lay.Hit(testW-1, testH-1); h.Kind != HitOutside {
		t.Fatalf("far corner hit = %+v", h)
	}
	if h := lay.Hit(-5, -5); h.Kind != HitOutside {
		t.Fatalf("negative hit = %+v", h)
	}
	// A closed menu's layout hits only the bar.
	m.Close()
	cl := m.Layout(fullArea(testW, testH))
	if h := cl.Hit(b0.X+3, b0.Y+2); h.Kind != HitOutside {
		t.Fatalf("closed menu hit a box: %+v", h)
	}
}

// Criterion 20: mouse and keys give the same results.
func TestClick_SameAsKeys(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	// Closed: a click off the bar is not handled.
	if r := m.Click(area, 5, 5); r.Handled || m.IsOpen() {
		t.Fatalf("closed click off the bar: %+v", r)
	}
	// A click on the bar row opens (criterion 23).
	x, y := barPos(t, m, 0)
	if r := m.Click(area, x, y); !r.Handled || !m.IsOpen() {
		t.Fatalf("bar click: %+v open=%v", r, m.IsOpen())
	}
	// Another bar entry switches lists.
	x, y = barPos(t, m, 1)
	m.Click(area, x, y)
	if p := m.Path(); p[0] != 1 || len(p) != 2 {
		t.Fatalf("path after clicking Screens: %v", p)
	}
	// Click Build, then Housing opens, then Dwelling runs.
	x, y = barPos(t, m, 0)
	m.Click(area, x, y)
	x, y = itemPos(t, m, 0, 0)
	m.Click(area, x, y)
	if len(m.Path()) != 3 {
		t.Fatalf("clicking a submenu did not open it: %v", m.Path())
	}
	x, y = itemPos(t, m, 1, 0)
	r := m.Click(area, x, y)
	if r.Action != "build:dwelling" || m.IsOpen() || !r.Closed {
		t.Fatalf("clicking Dwelling: %+v open=%v", r, m.IsOpen())
	}
	// The pick was remembered exactly as a key pick is.
	m.Open()
	if r := run(m, "b", "Enter", "Enter"); r.Action != "build:dwelling" {
		t.Fatalf("memory after a click pick: %+v", r)
	}
}

// Criterion 20: outside click closes and is swallowed; greyed click gives reason.
func TestClick_OutsideClosesAndSwallows(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	m.Open()
	run(m, "b")
	r := m.Click(area, testW-2, testH-2)
	if !r.Handled || !r.Closed || m.IsOpen() || r.Action != "" {
		t.Fatalf("outside click: %+v open=%v", r, m.IsOpen())
	}
	// Once closed, the same click is not handled: it reaches the screen.
	if r := m.Click(area, testW-2, testH-2); r.Handled {
		t.Fatal("a click on a closed menu was swallowed")
	}
}

func TestClick_GreyedShowsReasonSendsNothing(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	m.Open()
	run(m, "s")
	x, y := itemPos(t, m, 0, 2) // Save city (greyed)
	r := m.Click(area, x, y)
	if r.Action != "" || r.Notice != "no engine command yet" || !m.IsOpen() {
		t.Fatalf("greyed click: %+v", r)
	}
}

func TestClick_BorderAndSeparatorDoNothing(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	m.Open()
	run(m, "b")
	before := m.Path()
	lay := m.Layout(area)
	b := lay.Boxes[0].Rect
	for _, p := range [][2]int{{b.X, b.Y + 1}, {b.X + 3, b.Y + 3}, {b.X + 3, b.Y}} {
		r := m.Click(area, p[0], p[1])
		if !r.Handled || r.Closed || r.Action != "" || !m.IsOpen() {
			t.Fatalf("click at %v: %+v", p, r)
		}
	}
	if !reflect.DeepEqual(m.Path(), before) {
		t.Fatalf("border clicks moved the highlight: %v -> %v", before, m.Path())
	}
}

// Criterion 21: hover moves the highlight; no motion is needed.
func TestHover_MovesHighlightOnly(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	if m.Hover(area, 3, 0) {
		t.Fatal("hover on a closed menu changed it")
	}
	m.Open()
	run(m, "b")
	x, y := itemPos(t, m, 0, 3) // Buy this cell
	if !m.Hover(area, x, y) {
		t.Fatal("hover over an entry changed nothing")
	}
	if p := m.Path(); p[1] != 3 || len(p) != 2 {
		t.Fatalf("hover path %v", p)
	}
	if m.Hover(area, x, y) {
		t.Fatal("hovering the same entry again reported a change")
	}
	// Hover over a submenu entry highlights it but does not open it.
	x, y = itemPos(t, m, 0, 0)
	m.Hover(area, x, y)
	if len(m.Path()) != 2 {
		t.Fatalf("hover opened a sub-list: %v", m.Path())
	}
	// Hover over another bar entry switches the list.
	bx, by := barPos(t, m, 2)
	m.Hover(area, bx, by)
	if p := m.Path(); p[0] != 2 {
		t.Fatalf("hover over bar: %v", p)
	}
	// Hover outside changes nothing and closes nothing.
	if m.Hover(area, testW-1, testH-1) || !m.IsOpen() {
		t.Fatal("hover outside changed the menu")
	}
	// Hover over a parent-list entry while a sub-list is open leaves it open.
	m.Close()
	m.Open()
	run(m, "b", "h")
	before := m.Path()
	x, y = itemPos(t, m, 0, 1)
	if m.Hover(area, x, y) {
		t.Fatal("hovering a parent-list entry reported a change")
	}
	if !reflect.DeepEqual(m.Path(), before) {
		t.Fatalf("hovering a parent entry changed the open path: %v -> %v", before, m.Path())
	}
}

// Criterion 22/23: the key path alone is complete, and closed clicks that
// are not on the bar are never handled.
func TestClick_ClosedOnlyBarRowHandled(t *testing.T) {
	area := fullArea(testW, testH)
	m := sampleMenu(t)
	for _, p := range [][2]int{{0, 1}, {50, 15}, {testW - 1, testH - 1}, {-1, 0}, {testW, 0}} {
		if r := m.Click(area, p[0], p[1]); r.Handled {
			t.Fatalf("closed menu handled a click at %v", p)
		}
	}
	if m.IsOpen() {
		t.Fatal("a stray click opened the menu")
	}
	if r := m.Click(area, testW-1, 0); !r.Handled || !m.IsOpen() {
		t.Fatalf("a click on the empty bar row did not open the menu: %+v", r)
	}
}
