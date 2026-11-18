package flyout

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// bigTable builds a menu with one list of n entries (letters from the CJK
// block, which has no case, so they stay unique).
func bigTable(t testing.TB, n int) *Table {
	items := make([]Item, n)
	for i := range items {
		items[i] = actR(fmt.Sprintf("Entry %d", i), rune(0x4e00+i), "a", "£1")
	}
	tb, err := NewTable([]Item{sub("Big", 'b', items...)})
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

// Criterion 24-26: moving and drawing allocate nothing, and the number of
// entries read does not depend on the size of the tree. No wall clock.
func TestSpeed_NoAllocationsPerFrame(t *testing.T) {
	m := New(bigTable(t, 500))
	m.Open()
	run(m, "Down")
	buf := core.NewBuffer(testW, testH)
	area := fullArea(testW, testH)
	keyDown, keyUp := k("Down"), k("Up")
	if n := testing.AllocsPerRun(50, func() {
		m.Press(keyDown)
		m.Draw(buf, area)
	}); n != 0 {
		t.Fatalf("Press+Draw allocated %v times per frame, want 0", n)
	}
	if n := testing.AllocsPerRun(50, func() {
		m.Press(keyUp)
		lay := m.Layout(area)
		_ = lay.Hit(5, 5)
	}); n != 0 {
		t.Fatalf("Press+Layout+Hit allocated %v times, want 0", n)
	}
	sm := sampleMenu(t)
	sm.Open()
	run(sm, "b", "h")
	if n := testing.AllocsPerRun(50, func() { sm.Draw(buf, area) }); n != 0 {
		t.Fatalf("three-level Draw allocated %v times, want 0", n)
	}
	run(sm, "Esc", "Esc", "Esc")
	if n := testing.AllocsPerRun(50, func() { sm.Draw(buf, area) }); n != 0 {
		t.Fatalf("closed Draw allocated %v times, want 0", n)
	}
}

// drawVisitBound is the fixed number of entry reads one draw may make beyond
// the rows shown: the bar entries drawn, plus the boxes' own bookkeeping.
const drawVisitBound = 16

// Criterion 26: a 10,000-entry list costs the same to draw as a 40-entry one,
// and both are bounded by the rows shown plus a fixed constant.
func TestSpeed_DrawCostFollowsRowsShownNotTreeSize(t *testing.T) {
	area := fullArea(testW, 20)
	buf := core.NewBuffer(testW, 20)
	var visits []int
	for _, n := range []int{40, 10000} {
		m := New(bigTable(t, n))
		m.Open()
		run(m, "Down", "End") // deep into the list, so scrolling is in play
		rowsShown := m.Layout(area).Boxes[0].Vis
		m.ResetVisits()
		m.Draw(buf, area)
		if got := m.Visits(); got > 2*rowsShown+drawVisitBound {
			t.Fatalf("n=%d: one draw read %d entries for %d rows shown", n, got, rowsShown)
		}
		visits = append(visits, m.Visits())
	}
	if visits[0] != visits[1] {
		t.Fatalf("draw reads depend on tree size: %v", visits)
	}
}

// Criterion 24: opening, moving, going back and closing read no state but the
// menu's own: a script over the tree runs with no collaborator at all (the
// package has none), and every frame draws.
func TestSpeed_OneKeyShowsInTheNextFrame(t *testing.T) {
	m := sampleMenu(t)
	buf := core.NewBuffer(testW, testH)
	area := fullArea(testW, testH)
	m.Draw(buf, area)
	before := rows(buf)
	m.Open()
	m.Draw(buf, area)
	after := rows(buf)
	if equal(before, after) {
		t.Fatal("the frame after Open is the same as before: not drawn")
	}
	m.Press(k("Down"))
	m.Draw(buf, area)
	if equal(after, rows(buf)) {
		t.Fatal("a key press is not in the very next frame")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Closing restores the frame exactly (criterion 5), for a screen of map glyphs.
func TestClose_LeavesTheFrameAsItWas(t *testing.T) {
	m := sampleMenu(t)
	area := fullArea(testW, testH)
	frameWith := func() []string {
		buf := mapBuffer(testW, testH)
		m.Draw(buf, area) // the bar is always drawn
		return rows(buf)
	}
	closed := frameWith()
	m.Open()
	run(m, "b", "h")
	if equal(closed, frameWith()) {
		t.Fatal("open frame equals closed frame")
	}
	run(m, "Esc", "Esc", "Esc")
	if !equal(closed, frameWith()) {
		t.Fatal("frame after close differs from the frame before open")
	}
}
