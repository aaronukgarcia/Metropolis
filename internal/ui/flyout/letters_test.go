package flyout

import (
	"strings"
	"testing"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// hiddenLetterItems is a tree whose letters are deliberately not in their
// labels (and one that is), three lists deep.
func hiddenLetterItems() []Item {
	return []Item{
		sub("Build", 'x',
			sub("Housing", 'z',
				act("Dwelling", 'q', "d"),
				act("Flats", 'w', "f"),
				sub("Towers", 'k', act("Tall", 'j', "t"), grey("Taller", 'v', "later")),
			),
			act("Jobs", 'j', "jobs"),
		),
		sub("Screens", 'y', act("Map", 'u', "m"), sep, act("Finance", 'f', "fin")),
		sub("Game", 'c', act("Pause", 'p', "p")),
	}
}

// walkOpenPaths calls visit with the letters that open every list of items,
// the bar's drop-downs first.
func walkOpenPaths(items []Item, prefix []string, visit func(path []string, list []Item)) {
	for _, it := range items {
		if it.Kind != KindSubmenu {
			continue
		}
		p := append(append([]string{}, prefix...), string(unicode.ToLower(it.Letter)))
		visit(p, it.Items)
		walkOpenPaths(it.Items, p, visit)
	}
}

// Lead ruling: every reachable entry's letter shows as plain text, upper
// case, in its own column before the label, with no style needed.
func TestLetters_EveryReachableLetterInAPlainDump(t *testing.T) {
	for name, items := range map[string][]Item{"sample": sampleItems(), "hidden": hiddenLetterItems()} {
		tb := mustTable(t, items)
		count := 0
		walkOpenPaths(items, nil, func(path []string, list []Item) {
			m := New(tb)
			m.Open()
			run(m, path...)
			buf := core.NewBuffer(testW, testH)
			m.Draw(buf, fullArea(testW, testH))
			rs := rows(buf)
			dump := strings.Join(rs, "\n")
			for _, bi := range items {
				want := string(unicode.ToUpper(bi.Letter)) + " " + bi.Label
				if !strings.Contains(rs[0], want) {
					t.Fatalf("%s path %v: bar row %q lacks %q", name, path, rs[0], want)
				}
			}
			for _, it := range list {
				if it.Kind == KindSeparator {
					continue
				}
				count++
				want := string(unicode.ToUpper(it.Letter)) + " " + it.Label
				if !strings.Contains(dump, want) {
					t.Fatalf("%s path %v: frame lacks %q:\n%s", name, path, want, dump)
				}
			}
		})
		if count == 0 {
			t.Fatalf("%s: walked nothing", name)
		}
	}
}

// The ruled layouts, exactly.
func TestLetters_RuledShapes(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b")
	buf := core.NewBuffer(testW, testH)
	m.Draw(buf, fullArea(testW, testH))
	rs := rows(buf)
	if !strings.HasPrefix(rs[0], "[B Build]  S Screens   G Game") {
		t.Fatalf("bar row = %q", rs[0])
	}
	all := strings.Join(rs, "\n")
	for _, want := range []string{"►H Housing", " J Jobs", "not active yet"} {
		if !strings.Contains(all, want) {
			t.Fatalf("frame lacks %q:\n%s", want, all)
		}
	}
	// A separator row carries no letter.
	for _, r := range rs {
		if strings.Contains(r, "├") && strings.ContainsAny(strings.Trim(r, "├─┤ "), "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Fatalf("separator row has a letter: %q", r)
		}
	}
}

// The letter column and the highlight marker are never the part cut: a narrow
// area cuts the label first.
func TestLetters_NarrowCutsLabelNotLetter(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	for w := 12; w <= 30; w++ {
		buf := core.NewBuffer(w, testH)
		m.Draw(buf, fullArea(w, testH))
		lay := m.Layout(fullArea(w, testH))
		b := lay.Boxes[lay.N-1]
		if b.Rect.W < 8 {
			continue
		}
		rs := rows(buf)
		row := []rune(rs[b.Rect.Y+1+0])
		// the deepest list is on top; its first entry (Dwelling) is highlighted
		x := b.Rect.X + 1
		if row[x] != '►' || row[x+1] != 'D' || row[x+2] != ' ' {
			t.Fatalf("w=%d: first list row %q lost marker/letter at %d", w, string(row), x)
		}
	}
}

// A pathological right text must not let a sub-list cover its parent.
func TestLayout_HugeRightTextKeepsParentRowVisible(t *testing.T) {
	long := strings.Repeat("9", 300)
	items := []Item{
		sub("Build", 'b',
			sub("Housing", 'h', actR("Dwelling", 'd', "d", long), act("Flats", 'f', "f")),
			act("Jobs", 'j', "j"),
		),
	}
	m := New(mustTable(t, items))
	m.Open()
	run(m, "b", "h")
	area := fullArea(testW, testH)
	lay := m.Layout(area)
	if lay.N != 2 {
		t.Fatalf("open lists = %d", lay.N)
	}
	p, c := lay.Boxes[0], lay.Boxes[1]
	if c.Rect.X < p.Rect.X+p.Rect.W && c.Rect.X+c.Rect.W > p.Rect.X {
		t.Fatalf("child %v overlaps parent %v", c.Rect, p.Rect)
	}
	buf := core.NewBuffer(testW, testH)
	m.DrawLayout(buf, &lay)
	rs := rows(buf)
	prow := []rune(rs[p.Rect.Y+1])
	got := string(prow[p.Rect.X : p.Rect.X+p.Rect.W])
	if !strings.Contains(got, "►H Housing") {
		t.Fatalf("parent's highlighted row not visible: %q", got)
	}
}
