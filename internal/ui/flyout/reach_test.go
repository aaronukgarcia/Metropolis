package flyout

import (
	"reflect"
	"testing"
)

// Criteria 4 and 6: every entry of a tree is reached by letters alone, and
// does what its kind says. The expected outcome is read from the table, so a
// new node added to a fixture is covered with no other change (criterion 1).
func TestMenu_EveryEntryReachedByLetter(t *testing.T) {
	tb := mustTable(t, sampleItems())
	reached := 0
	var visit func(l *list, letters []string, path []int)
	visit = func(l *list, letters []string, path []int) {
		for i := range l.entries {
			e := &l.entries[i]
			if e.kind == KindSeparator {
				continue
			}
			m := New(tb)
			m.Open()
			r := run(m, letters...)
			if r.Action != "" {
				t.Fatalf("path %v ran an action early", letters)
			}
			r = run(m, string(e.letter))
			reached++
			want := append(append([]int(nil), path...), i)
			switch {
			case e.kind == KindSubmenu:
				if got := m.Path(); len(got) != len(want)+1 || got[len(got)-2] != i {
					t.Errorf("letter %q in %v: path %v", e.letter, letters, got)
				}
			case e.grey != "":
				if r.Action != "" || r.Notice != e.grey {
					t.Errorf("greyed %q: %+v", e.label, r)
				}
			default:
				if r.Action != e.action || !r.Closed {
					t.Errorf("action %q: %+v", e.label, r)
				}
			}
			if e.kind == KindSubmenu {
				visit(e.sub, append(append([]string(nil), letters...), string(e.letter)), want)
			}
		}
	}
	for b := range tb.bar.entries {
		visit(tb.bar.entries[b].sub, []string{string(tb.bar.entries[b].letter)}, []int{b})
	}
	if reached < 12 {
		t.Fatalf("only %d entries reached", reached)
	}
}

// Criterion 1: adding a node to the fixture makes it reachable with no other
// code change.
func TestMenu_NewNodeIsReachable(t *testing.T) {
	items := sampleItems()
	items[2].Items = append(items[2].Items, act("Zoom", 'z', "game:zoom"))
	m := New(mustTable(t, items))
	m.Open()
	if r := run(m, "g", "z"); r.Action != "game:zoom" {
		t.Fatalf("new node not reachable: %+v", r)
	}
}

// Arrow-only navigation reaches the same entries as letters do.
func TestMenu_ArrowsReachDwelling(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	r := run(m, "Down", "Enter", "Enter")
	if r.Action != "build:dwelling" {
		t.Fatalf("arrows: %+v path %v", r, m.Path())
	}
	m.Open()
	run(m, "Down", "Right")
	if !reflect.DeepEqual(m.Path(), []int{0, 0, 0}) {
		t.Fatalf("path %v", m.Path())
	}
}
