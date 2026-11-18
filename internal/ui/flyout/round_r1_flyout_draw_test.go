package flyout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func wideTree() []Item {
	long := strings.Repeat("W", 300)
	var many []Item
	for i := 0; i < 60; i++ {
		many = append(many, act(fmt.Sprintf("Row %d £é…", i), rune('a'+i%26)+0, fmt.Sprintf("r%d", i)))
	}
	// distinct letters (not '#': that is the map sentinel and a letter is now drawn): use mix of lower/upper digits
	letters := []rune("abcdefghijklmnopqrstuvwxyz0123456789!@~$%^&*()-=+[]{};:,.<>/?")
	for i := range many {
		many[i].Letter = letters[i]
	}
	deep := sub("L1", 'a', sub("L2", 'b', sub("L3", 'c', act("Leaf £", 'd', "leaf"), grey("G", 'e', long))))
	return []Item{
		sub("Alpha", 'a', deep, sub("Many", 'm', many...), actR(long, 'l', "long", long)),
		sub("Beta", 'b', actR("£x", 'x', "x", "£1"), act("Café", 'c', "c")),
		sub("Zed", 'z', deep, act("Q", 'q', "q")),
	}
}

func TestRoundR1_DrawBounds(t *testing.T) {
	rc := func(x, y, w, h int) core.Rect { return core.Rect{X: x, Y: y, W: w, H: h} }
	areas := []core.Rect{rc(0, 0, 0, 0), rc(0, 0, 1, 1), rc(0, 0, 5, 3), rc(0, 0, 30, 10), rc(0, 0, 120, 30), rc(0, 0, 200, 60), rc(7, 5, 60, 20), rc(150, 40, 50, 20), rc(-5, -3, 40, 12), rc(190, 55, 30, 30), rc(10, 10, 300, 100)}
	scripts := [][]string{
		{}, {"Down"}, {"a"}, {"a", "a"}, {"a", "a", "b"}, {"a", "a", "b", "c"}, {"a", "m"}, {"a", "m", "End"}, {"a", "m", "Down", "Down", "Down", "Down", "Down", "Down", "Down", "Down", "Down", "Down", "Down", "Down"},
		{"a", "l"}, {"Right", "Right"}, {"z", "a", "b", "c", "Down", "e"}, {"a", "a", "b", "c", "e"},
	}
	for _, ar := range areas {
		for si, sc := range scripts {
			m := New(mustTable(t, wideTree()))
			m.Open()
			for _, s := range sc {
				m.Press(k(s))
			}
			buf := mapBuffer(200, 60)
			m.Draw(buf, ar)
			for y := 0; y < 60; y++ {
				for x := 0; x < 200; x++ {
					if !inside(ar, x, y) && buf.Get(x, y).Rune != '#' {
						t.Fatalf("area %v script %d wrote outside at %d,%d: %q", ar, si, x, y, buf.Get(x, y).Rune)
					}
				}
			}
			// buffer smaller than area: no panic
			small := mapBuffer(10, 5)
			m.Draw(small, ar)
			// box blanked: no '#' inside any box rect
			lay := m.Layout(ar)
			for k := 0; k < lay.N; k++ {
				r := lay.Boxes[k].Rect
				for y := r.Y; y < r.Y+r.H; y++ {
					for x := r.X; x < r.X+r.W; x++ {
						if buf.Get(x, y).Rune == '#' {
							t.Fatalf("area %v script %d box %d leaves map at %d,%d", ar, si, k, x, y)
						}
						if !inside(ar, x, y) && r.W > 0 {
							t.Fatalf("box outside area")
						}
					}
				}
			}
			// hit sweep at area
			for y := ar.Y - 1; y < ar.Y+ar.H+1; y++ {
				for x := ar.X - 1; x < ar.X+ar.W+1; x++ {
					if x < 0 || y < 0 || x >= 200 || y >= 60 {
						continue
					}
					h := lay.Hit(x, y)
					if h.Kind == HitItem {
						b := &lay.Boxes[h.Level]
						e := &b.list.entries[h.Index]
						if e.kind == KindSeparator {
							t.Fatalf("hit on separator")
						}
						row := y - b.Rect.Y - 1
						if b.Above {
							row--
						}
						if b.Off+row != h.Index {
							t.Fatalf("index mismatch")
						}
						// drawn glyph at label start equals label's first rune (when label cell exists)
						covered := false // a deeper box may lie over this label's first cell
						for dk := h.Level + 1; dk < lay.N; dk++ {
							covered = covered || inside(lay.Boxes[dk].Rect, b.Rect.X+2+letterCols, y)
						}
						if !covered && b.LabelW > 1 && b.Rect.X+2+letterCols >= 0 {
							want, _ := utf8.DecodeRuneInString(e.label)
							got := buf.Get(b.Rect.X+2+letterCols, y).Rune
							if got != want && !inside(core.Rect{}, 0, 0) {
								t.Fatalf("area %v script %d hit (%d,%d) item %d %q but drawn %q", ar, si, x, y, h.Index, e.label, got)
							}
						}
					}
				}
			}
		}
	}
}

// Lead ruling (a): letter must be visible.
func TestRoundR1_LetterVisible(t *testing.T) {
	items := []Item{sub("Build", 'b', act("Dwelling", 'q', "d"), act("Flats", 'f', "f"))}
	m := New(mustTable(t, items))
	m.Open()
	m.Press(k("Down"))
	buf := core.NewBuffer(30, 8)
	m.Draw(buf, fullArea(30, 8))
	for _, r := range rows(buf) {
		t.Log("|" + r + "|")
	}
	// is q visible anywhere in the box, plain text?
	if !strings.Contains(strings.Join(rows(buf), "\n"), "q") && !strings.Contains(strings.Join(rows(buf), "\n"), "Q") {
		t.Errorf("letter 'q' of Dwelling is not shown anywhere in the frame")
	}
}

// Plain-text dump: distinguishable without styles?
func TestRoundR1_PlainFrame(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	m.Press(k("Down"))
	buf := core.NewBuffer(60, 12)
	m.Draw(buf, fullArea(60, 12))
	for _, r := range rows(buf) {
		t.Log("|" + r + "|")
	}
}

func TestRoundR1_RandomClicksMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 500; i++ {
		m := New(mustTable(t, wideTree()))
		m.Open()
		area := core.Rect{X: 0, Y: 0, W: 120, H: 30}
		for s := 0; s < 6; s++ {
			m.Press(k([]string{"a", "m", "Down", "Right", "b", "c", "Left"}[rng.Intn(7)]))
		}
		if !m.IsOpen() {
			continue
		}
		lay := m.Layout(area)
		// click outside any box
		x, y := rng.Intn(120), rng.Intn(30)
		h := lay.Hit(x, y)
		before := m.Path()
		r := m.Click(area, x, y)
		if h.Kind == HitOutside && !r.Closed {
			t.Fatalf("outside click did not close")
		}
		if h.Kind == HitBox && (r.Closed || r.Action != "") {
			t.Fatalf("border click acted")
		}
		_ = before
	}
}
