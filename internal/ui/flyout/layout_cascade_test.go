package flyout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// checkCascade verifies, for the frame the menu would draw now, that no two
// open boxes share a cell, every box is inside the area, the highlighted row
// of every open level shows its marker and letter (or its collapsed strip
// marker) and the deepest list shows the start of its highlighted label. It
// returns the first failure.
func checkCascade(m *Menu, area core.Rect) string {
	buf := core.NewBuffer(area.W, area.H)
	m.Draw(buf, area)
	lay := m.Layout(area)
	if !lay.Open {
		return ""
	}
	if !lay.disjoint() {
		return fmt.Sprintf("boxes overlap or leave the area: %v", rectsOf(&lay))
	}
	for kk := 0; kk < lay.N; kk++ {
		b := &lay.Boxes[kk]
		if b.Rect.H == 0 {
			return fmt.Sprintf("level %d has no box", kk)
		}
		hi := m.hl[kk]
		if b.Collapsed {
			if kk == lay.N-1 {
				return "the deepest level is collapsed"
			}
			if got := buf.Get(b.Rect.X, b.Rect.Y).Rune; got != runeHighlight {
				return fmt.Sprintf("level %d strip shows %q at %v", kk, got, b.Rect)
			}
			continue
		}
		if hi < b.Off || hi >= b.Off+b.Vis {
			return fmt.Sprintf("level %d highlight %d outside [%d,%d)", kk, hi, b.Off, b.Off+b.Vis)
		}
		y := b.Rect.Y + 1 + (hi - b.Off)
		if b.Above {
			y++
		}
		e := &b.list.entries[hi]
		if !m.inBar || kk != 0 {
			if got := buf.Get(b.Rect.X+1, y).Rune; got != runeHighlight {
				return fmt.Sprintf("level %d marker hidden: %q at (%d,%d) box %v", kk, got, b.Rect.X+1, y, b.Rect)
			}
		}
		if got := buf.Get(b.Rect.X+2, y).Rune; got != unicode.ToUpper(e.letter) {
			return fmt.Sprintf("level %d letter hidden: %q want %q box %v", kk, got, unicode.ToUpper(e.letter), b.Rect)
		}
		if kk == lay.N-1 {
			first, _ := utf8.DecodeRuneInString(e.label)
			if got := buf.Get(b.Rect.X+2+letterCols, y).Rune; got != first {
				return fmt.Sprintf("deepest label start hidden: %q want %q box %v", got, first, b.Rect)
			}
		}
	}
	return ""
}

func rectsOf(l *Layout) []core.Rect {
	out := []core.Rect{}
	for i := 0; i < l.N; i++ {
		out = append(out, l.Boxes[i].Rect)
	}
	return out
}

func TestLayout_CascadeProperty(t *testing.T) {
	areas := []core.Rect{{W: 120, H: 30}, {W: 100, H: 30}, {W: 80, H: 24}, {W: 60, H: 20}, {W: 40, H: 12}}
	steps := []string{"Right", "Down", "Up", "Right", "End", "Left", "Enter", "Home", "Right", "Down"}
	rng := rand.New(rand.NewSource(2024))
	frames, deepest, collapsed := 0, 0, 0
	for tr := 0; tr < 120; tr++ {
		bar := []Item{}
		for i := 0; i < 5; i++ {
			bar = append(bar, sub(fmt.Sprintf("Bar%d", i), rune('a'+i), realisticTree(rng, 0)...))
		}
		tb, err := NewTable(bar)
		if err != nil {
			t.Fatal(err)
		}
		for _, area := range areas {
			m := New(tb)
			m.Open()
			for s := 0; s < 40; s++ {
				m.Press(k(steps[rng.Intn(len(steps))]))
				if !m.IsOpen() {
					m.Open()
				}
				frames++
				if msg := checkCascade(m, area); msg != "" {
					t.Fatalf("tree %d area %v step %d path %v: %s", tr, area, s, m.Path(), msg)
				}
				lay := m.Layout(area)
				if lay.N == MaxDepth {
					deepest++
				}
				if lay.N > 0 && lay.Boxes[0].Collapsed {
					collapsed++
				}
			}
		}
	}
	t.Logf("frames %d, four-deep frames %d, frames with a collapsed level %d", frames, deepest, collapsed)
	if deepest == 0 || collapsed == 0 {
		t.Fatalf("the walk never reached four lists (%d) or a collapsed level (%d)", deepest, collapsed)
	}
}

// cascadeMenu opens a four-list path in a tree with long labels.
func cascadeMenu(t *testing.T) *Menu { return cascadeMenuAt(t, 0) }

// cascadeMenuAt puts the four-list path under the bar entry "Build", with a
// lead entry of the given label width before it so the cascade starts lead
// columns into the bar.
func cascadeMenuAt(t *testing.T, lead int) *Menu {
	t.Helper()
	l4 := []Item{actR("Fourth level entry", 'f', "f", "right4"), actR("Another leaf", 'a', "a", "r")}
	l3 := []Item{sub("Third level list", 't', l4...), actR("Third other", 'o', "o", "r")}
	l2 := []Item{sub("Second level list", 's', l3...), actR("Second other", 'o', "o", "r")}
	l1 := []Item{sub("First level list", 'f', l2...), actR("First other", 'o', "o", "r")}
	bar := []Item{sub("Build", 'b', l1...), sub("Zone", 'z', actR("Zed", 'z', "z", ""))}
	if lead > 0 {
		bar = append([]Item{sub(strings.Repeat("A", lead), 'a', actR("Aa", 'a', "a", ""))}, bar...)
	}
	tb, err := NewTable(bar)
	if err != nil {
		t.Fatal(err)
	}
	m := New(tb)
	m.Open()
	run(m, "b", "f", "s", "t")
	return m
}

// The cascade starts under its bar entry; when it would pass the right edge
// the whole cascade shifts left, with no box leaving the area.
func TestLayout_CascadeShiftFrame(t *testing.T) {
	m := cascadeMenuAt(t, 40)
	area := core.Rect{W: 120, H: 30}
	lay := m.Layout(area)
	bx, _, _ := lay.BarCell(1)
	if lay.N != 4 || !lay.disjoint() || lay.Boxes[0].Rect.X >= bx {
		t.Fatalf("expected a left shift from bar column %d: %v", bx, rectsOf(&lay))
	}
	if r := lay.Boxes[3].Rect; r.X+r.W != area.X+area.W {
		t.Fatalf("shifted cascade should end at the right edge: %v", r)
	}
	if msg := checkCascade(m, area); msg != "" {
		t.Fatal(msg)
	}
	buf := core.NewBuffer(area.W, area.H)
	m.Draw(buf, area)
	t.Logf("bar column %d, cascade starts at %d\n%s", bx, lay.Boxes[0].Rect.X, strings.Join(rows(buf)[:5], "\n"))
}

// Shift and shrink together in a 60 by 20 area.
func TestLayout_CascadeShiftAndShrinkFrame(t *testing.T) {
	m := cascadeMenuAt(t, 20)
	area := core.Rect{W: 60, H: 20}
	if msg := checkCascade(m, area); msg != "" {
		t.Fatal(msg)
	}
	lay := m.Layout(area)
	if lay.N != 4 || lay.Boxes[0].Rect.X != 0 || lay.Boxes[3].Rect.X+lay.Boxes[3].Rect.W != area.W {
		t.Fatalf("expected a cascade filling the width: %v", rectsOf(&lay))
	}
	buf := core.NewBuffer(area.W, area.H)
	m.Draw(buf, area)
	t.Logf("\n%s", strings.Join(rows(buf)[:5], "\n"))
}

func TestLayout_CascadeFramesPinned(t *testing.T) {
	pins := map[int][]string{
		120: {
			"[B Build]  Z Zone",
			"┌─────────────────────────┐┌──────────────────────────┐┌─────────────────────────┐┌──────────────────────────────┐",
			"│►F First level list    > ││►S Second level list    > ││►T Third level list    > ││►F Fourth level entry  right4 │",
			"│ O First other       r   ││ O Second other       r   ││ O Third other       r   ││ A Another leaf             r │",
			"└─────────────────────────┘└──────────────────────────┘└─────────────────────────┘└──────────────────────────────┘",
		},
		60: {
			"[B Build]  Z Zone",
			"┌─────────────┐┌─────────────┐┌─────────────┐┌─────────────┐",
			"│►F First … > ││►S Second… > ││►T Third … > ││►F Fourth l… │",
			"│ O First …   ││ O Second…   ││ O Third …   ││ A Another … │",
			"└─────────────┘└─────────────┘└─────────────┘└─────────────┘",
		},
	}
	for _, sz := range []core.Rect{{W: 120, H: 30}, {W: 60, H: 20}} {
		m := cascadeMenu(t)
		buf := core.NewBuffer(sz.W, sz.H)
		m.Draw(buf, sz)
		lay := m.Layout(sz)
		if lay.N != 4 || !lay.disjoint() {
			t.Fatalf("%v: %d lists, disjoint %v", sz, lay.N, lay.disjoint())
		}
		for kk := 1; kk < lay.N; kk++ {
			p, b := lay.Boxes[kk-1], lay.Boxes[kk]
			if !p.Collapsed && b.Rect.X != p.Rect.X+p.Rect.W {
				t.Fatalf("%v: level %d starts at %d, parent ends at %d", sz, kk, b.Rect.X, p.Rect.X+p.Rect.W)
			}
		}
		got := rows(buf)
		for i, want := range pins[sz.W] {
			if g := strings.TrimRight(got[i], " "); g != want {
				t.Errorf("%v row %d:\n got %q\nwant %q", sz, i, g, want)
			}
		}
		t.Logf("%v\n%s", sz, strings.Join(got[:6], "\n"))
	}
}

// Rule 2 last resort: in a very narrow area the oldest levels collapse and
// the deepest list stays whole.
func TestLayout_NarrowAreaCollapsesOldestLevels(t *testing.T) {
	m := cascadeMenu(t)
	area := core.Rect{W: 30, H: 12}
	lay := m.Layout(area)
	if !lay.Boxes[0].Collapsed || lay.Boxes[lay.N-1].Collapsed {
		t.Fatalf("expected the oldest level collapsed and the deepest whole: %v", rectsOf(&lay))
	}
	if msg := checkCascade(m, area); msg != "" {
		t.Fatal(msg)
	}
	buf := core.NewBuffer(area.W, area.H)
	m.Draw(buf, area)
	t.Logf("\n%s", strings.Join(rows(buf)[:5], "\n"))
}
