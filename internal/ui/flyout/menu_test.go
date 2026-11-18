package flyout

import (
	"reflect"
	"testing"
)

// Criterion 3: opening focuses the bar on the first entry with its list dropped.
func TestMenu_Open(t *testing.T) {
	m := sampleMenu(t)
	if m.IsOpen() {
		t.Fatal("a new menu is open")
	}
	m.Open()
	if !m.IsOpen() || !m.InBar() {
		t.Fatalf("after Open: open=%v inBar=%v", m.IsOpen(), m.InBar())
	}
	if got := m.Path(); !reflect.DeepEqual(got, []int{0, 0}) {
		t.Fatalf("path = %v, want [0 0] (first bar entry, its first entry)", got)
	}
	m.Toggle()
	if m.IsOpen() {
		t.Fatal("Toggle on an open menu left it open")
	}
	m.Toggle()
	if !m.IsOpen() {
		t.Fatal("Toggle on a closed menu did not open it")
	}
}

// Criterion 8: closed, no key is handled.
func TestMenu_ClosedHandlesNothing(t *testing.T) {
	m := sampleMenu(t)
	for _, tok := range []string{"b", "q", " ", "Esc", "Enter", "F10", "Down"} {
		if r := m.Press(k(tok)); r.Handled || r.Action != "" {
			t.Errorf("closed menu handled %q: %+v", tok, r)
		}
	}
	if m.IsOpen() {
		t.Fatal("a key opened a closed menu")
	}
}

// Criterion 7: while open every key is handled, even one that does nothing.
func TestMenu_OpenHandlesEveryKey(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	for _, tok := range []string{"q", " ", "]", "1", "Tab", "F1", "PgUp", "z"} {
		r := m.Press(k(tok))
		if !r.Handled {
			t.Errorf("open menu let %q through", tok)
		}
		if r.Action != "" {
			t.Errorf("%q picked %q", tok, r.Action)
		}
	}
	if !m.IsOpen() {
		t.Fatal("an unbound key closed the menu")
	}
}

// Criterion 4: Up and Down wrap and skip separators.
func TestMenu_UpDownWrapSkipSeparators(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b") // into the Build list: Housing, Jobs, ---, Buy, Demolish
	want := []int{0, 1, 3, 4, 0, 1}
	var got []int
	got = append(got, m.Path()[1])
	for i := 1; i < len(want); i++ {
		run(m, "Down")
		got = append(got, m.Path()[1])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Down run = %v, want %v", got, want)
	}
	run(m, "Up", "Up")
	if p := m.Path()[1]; p != 4 {
		// 1 -> 0 -> wraps to 4
		t.Fatalf("Up wrap landed on %d, want 4", p)
	}
	run(m, "Up")
	if p := m.Path()[1]; p != 3 {
		t.Fatalf("Up did not skip the separator: %d", p)
	}
}

func TestMenu_HomeEnd(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "End")
	if p := m.Path()[1]; p != 4 {
		t.Fatalf("End -> %d, want 4", p)
	}
	run(m, "Home")
	if p := m.Path()[1]; p != 0 {
		t.Fatalf("Home -> %d, want 0", p)
	}
}

// Criterion 4: Left and Right at the first level move across the bar.
func TestMenu_LeftRightAcrossBar(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "Right")
	if p := m.Path(); p[0] != 1 {
		t.Fatalf("Right: bar = %d, want 1", p[0])
	}
	run(m, "Right", "Right")
	if p := m.Path(); p[0] != 0 {
		t.Fatalf("Right x3 did not wrap: bar = %d", p[0])
	}
	run(m, "Left")
	if p := m.Path(); p[0] != 2 {
		t.Fatalf("Left did not wrap: bar = %d, want 2", p[0])
	}
	// Inside the first list the same keys still move the bar.
	run(m, "Down", "Left")
	if p := m.Path(); p[0] != 1 || len(p) != 2 {
		t.Fatalf("Left inside the first list: path %v, want bar 1", p)
	}
	run(m, "Right")
	if p := m.Path(); p[0] != 2 {
		t.Fatalf("Right on a leaf in the first list: bar %d, want 2", p[0])
	}
}

// Criterion 4 and 5: Enter or Right opens a sub-list, Left or Esc goes back.
func TestMenu_EnterBackAndClose(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "Down") // focus into Build's list, Housing highlighted
	run(m, "Enter")
	if got := m.Path(); !reflect.DeepEqual(got, []int{0, 0, 0}) {
		t.Fatalf("Enter on Housing: path %v, want [0 0 0]", got)
	}
	run(m, "Left")
	if got := m.Path(); !reflect.DeepEqual(got, []int{0, 0}) {
		t.Fatalf("Left: path %v, want [0 0]", got)
	}
	run(m, "Right")
	if len(m.Path()) != 3 {
		t.Fatalf("Right on a submenu did not open it: %v", m.Path())
	}
	r := run(m, "Esc")
	if r.Closed || !m.IsOpen() || len(m.Path()) != 2 {
		t.Fatalf("Esc inside a sub-list: closed=%v path=%v", r.Closed, m.Path())
	}
	r = run(m, "Esc")
	if !r.Closed || m.IsOpen() {
		t.Fatalf("Esc at the first list did not close: %+v", r)
	}
	// Esc at the bar closes too.
	m.Open()
	if r := run(m, "Esc"); !r.Closed || m.IsOpen() {
		t.Fatalf("Esc at the bar did not close: %+v", r)
	}
	// F10 closes from anywhere.
	m.Open()
	run(m, "b", "h")
	if r := run(m, "F10"); !r.Closed || m.IsOpen() {
		t.Fatalf("F10 did not close: %+v", r)
	}
}

// Criterion 5 and 32: a leaf picked by letter returns its id and closes.
func TestMenu_PickByLetterReturnsIDAndCloses(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	r := run(m, "b", "h", "d")
	if r.Action != "build:dwelling" || !r.Closed || m.IsOpen() {
		t.Fatalf("pick: %+v open=%v", r, m.IsOpen())
	}
	// Either case.
	m.Open()
	r = run(m, "B", "H", "F")
	if r.Action != "build:flats" {
		t.Fatalf("upper-case letters: %+v", r)
	}
	// Enter in place of the last letter.
	m.Open()
	r = run(m, "B", "H", "Enter")
	if r.Action != "build:flats" && r.Action != "build:dwelling" {
		t.Fatalf("Enter pick: %+v", r)
	}
}

// Criterion 6: a letter that is no entry's letter does nothing.
func TestMenu_UnknownLetterDoesNothing(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b")
	before := m.Path()
	r := run(m, "z")
	if r.Action != "" || r.Notice != "" || !reflect.DeepEqual(m.Path(), before) {
		t.Fatalf("unknown letter changed things: %+v path %v -> %v", r, before, m.Path())
	}
	// A bar letter that matches nothing on the bar does nothing either.
	m.Close()
	m.Open()
	run(m, "z")
	if !m.InBar() || m.Path()[0] != 0 {
		t.Fatal("unknown bar letter moved the focus")
	}
}

// Criterion 9: greyed entries can be highlighted but not run.
func TestMenu_GreyedEntryReportsReasonAndSendsNothing(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	r := run(m, "s", "v") // Screens > Save city (greyed action)
	if r.Action != "" {
		t.Fatalf("a greyed entry returned action %q", r.Action)
	}
	if r.Notice != "no engine command yet" || m.Notice() != r.Notice {
		t.Fatalf("notice = %q / %q", r.Notice, m.Notice())
	}
	if !m.IsOpen() {
		t.Fatal("a greyed entry closed the menu")
	}
	// Enter on it behaves the same.
	run(m, "Up") // move off, notice clears
	if m.Notice() != "" {
		t.Fatalf("notice survived the next key: %q", m.Notice())
	}
	run(m, "Down")
	r = run(m, "Enter")
	if r.Action != "" || r.Notice == "" {
		t.Fatalf("Enter on a greyed entry: %+v", r)
	}
}

// A greyed submenu opens (criterion 30) and its greyed leaves do not run.
func TestMenu_GreyedSubmenuOpens(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	r := run(m, "b", "j")
	if len(m.Path()) != 3 || r.Action != "" {
		t.Fatalf("greyed submenu: path %v %+v", m.Path(), r)
	}
	r = run(m, "o")
	if r.Action != "" || r.Notice != "not active yet" {
		t.Fatalf("greyed leaf: %+v", r)
	}
}

// Criterion 38: the menu remembers the last pick, so repeating is
// F10, B, Enter, Enter.
func TestMenu_RemembersLastPick(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h", "f") // Flats, the second entry
	m.Open()
	r := run(m, "b", "Enter", "Enter")
	if r.Action != "build:flats" {
		t.Fatalf("repeat gave %q, want build:flats", r.Action)
	}
	// A different path starts from its first entry, not from the memory.
	m.Open()
	run(m, "s", "Enter")
	m.Open()
	run(m, "b", "h")
	if m.Path()[2] != 0 {
		// after Screens > Map, Build > Housing > ... has no memory: first entry
		t.Fatalf("highlight in Housing = %d, want 0", m.Path()[2])
	}
}

// Closing without picking forgets nothing and leaves nothing open.
func TestMenu_CloseKeepsMemoryButNotState(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h", "f")
	m.Open()
	run(m, "b", "h", "Esc", "Esc", "Esc")
	if m.IsOpen() {
		t.Fatal("menu still open")
	}
	m.Open()
	if r := run(m, "b", "Enter", "Enter"); r.Action != "build:flats" {
		t.Fatalf("memory lost on an Esc close: %+v", r)
	}
}

// A zero Menu and a nil table do not panic.
func TestMenu_NotBuiltIsSafe(t *testing.T) {
	var m Menu
	m.Open()
	if r := m.Press(k("a")); r.Handled {
		t.Fatal("an unbuilt menu handled a key")
	}
	if m.IsOpen() {
		t.Fatal("an unbuilt menu opened")
	}
	New(nil).Open()
	var np *Menu
	np.Close()
	if np.IsOpen() || np.Notice() != "" || np.Path() != nil {
		t.Fatal("nil menu not inert")
	}
}
