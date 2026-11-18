package flyout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func r3FourLevel(t *testing.T) *Menu {
	t.Helper()
	l4 := []Item{actR("Fourth level entry name", 'f', "f", "right-four-col"), actR("Another leaf here", 'a', "a", "r4")}
	l3 := []Item{sub("Third level list name", 't', l4...), actR("Third other entry", 'o', "o", "right-three")}
	l2 := []Item{sub("Second level list name", 's', l3...), actR("Second other entry", 'o', "o", "right-two-xx")}
	l1 := []Item{sub("First level list name", 'f', l2...), actR("First other entry", 'o', "o", "right-one-yyy")}
	tb := mustTable(t, []Item{sub("Build", 'b', l1...)})
	m := New(tb)
	m.Open()
	run(m, "Down", "Right", "Right", "Right")
	return m
}

type r3shape struct {
	n, c       int
	lab, rt, w [MaxDepth]int
}

func r3Shape(m *Menu, w int) r3shape {
	lay := m.Layout(core.Rect{W: w, H: 30})
	s := r3shape{n: lay.N}
	for k := 0; k < lay.N; k++ {
		b := &lay.Boxes[k]
		if b.Collapsed {
			s.c++
		}
		s.lab[k], s.rt[k], s.w[k] = b.LabelW, b.RightW, b.Rect.W
	}
	return s
}

func TestRoundR3_ShrinkOrder(t *testing.T) {
	m := r3FourLevel(t)
	var prev r3shape
	for w := 120; w >= 20; w-- {
		s := r3Shape(m, w)
		a := core.Rect{W: w, H: 30}
		buf := mapBuffer(w, 30)
		m.Draw(buf, a)
		if msg := r3Check(m, a, buf, w, 30); msg != "" {
			t.Fatalf("w=%d: %s", w, msg)
		}
		lay := m.Layout(a)
		for k := 0; k < lay.N; k++ {
			b := &lay.Boxes[k]
			if b.Rect.W > w {
				t.Fatalf("w=%d level %d wider than area", w, k)
			}
			if !b.Collapsed {
				if b.LabelW < b.list.labelW && b.RightW != 0 {
					t.Fatalf("w=%d level %d label cut (%d<%d) while right column still %d", w, k, b.LabelW, b.list.labelW, b.RightW)
				}
				if b.LabelW < minLabelRunes && b.LabelW < b.list.labelW && s.c == 0 {
					t.Fatalf("w=%d level %d label %d below minimum with nothing collapsed", w, k, b.LabelW)
				}
			}
		}
		if w < 120 {
			if s.c < prev.c {
				t.Fatalf("w=%d: collapsed count went %d -> %d as width fell (flicker)", w, prev.c, s.c)
			}
			for k := 0; k < s.n; k++ {
				if prev.c <= k && s.c <= k && s.c == prev.c {
					if s.lab[k] > prev.lab[k] || s.rt[k] > prev.rt[k] || s.w[k] > prev.w[k] {
						t.Fatalf("w=%d level %d grew as width fell: %+v -> %+v", w, k, prev, s)
					}
				}
			}
			if s.c > prev.c {
				for k := prev.c; k < prev.n; k++ {
					if prev.lab[k] > minLabelRunes {
						t.Fatalf("w=%d collapsed while level %d at w+1 still had label %d > min", w, k, prev.lab[k])
					}
				}
				t.Logf("w=%d: collapse %d -> %d, prev shape %+v", w, prev.c, s.c, prev)
			}
		}
		prev = s
	}
}

func rowRunes(buf *core.Buffer, x, y, w int) []rune {
	out := []rune{}
	for i := 0; i < w; i++ {
		r := buf.Get(x+i, y).Rune
		if r == 0 {
			r = ' '
		}
		out = append(out, r)
	}
	return out
}

func TestRoundR3_VerticalScroll(t *testing.T) {
	big := []Item{}
	for i := 0; i < 60; i++ {
		big = append(big, actR(fmt.Sprintf("Entry %02d", i), r3Letter(i), "a", "x"))
	}
	pad := []Item{}
	for i := 0; i < 15; i++ {
		pad = append(pad, act(fmt.Sprintf("Pad %02d", i), r3Letter(i+1), "p"))
	}
	pad = append(pad, sub("Big list", r3Letter(0), big...))
	for _, h := range []int{30, 24, 12, 8, 6} {
		tb := mustTable(t, []Item{sub("Menu", 'm', pad...)})
		m := New(tb)
		m.Open()
		run(m, "Down", "End")
		run(m, "Right")
		a := core.Rect{W: 120, H: h}
		check := func(tag string) {
			buf := mapBuffer(120, h)
			m.Draw(buf, a)
			if msg := r3Check(m, a, buf, 120, h); msg != "" {
				t.Fatalf("h=%d %s: %s\n%s", h, tag, msg, strings.Join(rows(buf), "\n"))
			}
			lay := m.Layout(a)
			b := &lay.Boxes[1]
			r := b.Rect
			row := r.Y + 1
			if b.Above {
				got := strings.TrimSpace(string(rowRunes(buf, r.X+1, row, r.W-2)))
				want := fmt.Sprintf("... %d more above", b.Off)
				if got != want {
					t.Fatalf("h=%d %s: above marker %q want %q", h, tag, got, want)
				}
				row++
			}
			if b.Below {
				brow := row + b.Vis
				got := strings.TrimSpace(string(rowRunes(buf, r.X+1, brow, r.W-2)))
				want := fmt.Sprintf("... %d more below", 60-(b.Off+b.Vis))
				if got != want {
					t.Fatalf("h=%d %s: below marker %q want %q", h, tag, got, want)
				}
			}
		}
		check("open")
		for _, seq := range []string{"End", "Home", "End"} {
			run(m, seq)
			check(seq)
		}
		for i := 0; i < 70; i++ {
			run(m, "Down")
			check("down")
		}
		for i := 0; i < 70; i++ {
			run(m, "Up")
			check("up")
		}
		run(m, "Left")
		for i := 0; i < 16; i++ {
			run(m, "Up")
			check("parent up")
		}
		run(m, "Right")
		check("reopen")
	}
}

func TestRoundR3_HitSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	cells, strips := 0, 0
	moves := []string{"Right", "Right", "Down", "Right", "Down", "Up", "End", "Left"}
	for tr := 0; tr < 12; tr++ {
		bar := []Item{}
		for i := 0; i < 4; i++ {
			bar = append(bar, sub(strings.Repeat("B", 1+rng.Intn(6)), r3Letter(i), r3List(rng, 1)...))
		}
		tb := mustTable(t, bar)
		for _, sz := range [][2]int{{120, 30}, {80, 24}, {40, 12}, {20, 6}} {
			a := core.Rect{X: 2, Y: 1, W: sz[0], H: sz[1]}
			m := New(tb)
			m.Open()
			for s := 0; s < 14; s++ {
				m.Press(k(moves[rng.Intn(len(moves))]))
				if !m.IsOpen() {
					m.Open()
				}
				buf := mapBuffer(sz[0]+6, sz[1]+4)
				m.Draw(buf, a)
				lay := m.Layout(a)
				for y := 0; y < sz[1]+4; y++ {
					for x := 0; x < sz[0]+6; x++ {
						h := lay.Hit(x, y)
						cells++
						in := x >= a.X && x < a.X+a.W && y >= a.Y && y < a.Y+a.H
						if !in && h.Kind != HitOutside {
							t.Fatalf("hit outside area at %d,%d: %+v", x, y, h)
						}
						if h.Kind == HitItem {
							b := &lay.Boxes[h.Level]
							if b.Collapsed {
								t.Fatalf("HitItem on a strip %d,%d", x, y)
							}
							e := &b.list.entries[h.Index]
							if e.kind == KindSeparator {
								t.Fatalf("HitItem on separator")
							}
							if buf.Get(b.Rect.X+2, y).Rune != unicode.ToUpper(e.letter) {
								t.Fatalf("tree %d %v s%d: HitItem(%d,%d) at %d,%d but drawn letter %q", tr, sz, s, h.Level, h.Index, x, y, buf.Get(b.Rect.X+2, y).Rune)
							}
						}
						if h.Kind == HitBox && h.Level < lay.N && lay.Boxes[h.Level].Collapsed {
							strips++
						}
						if (x+y)%7 == 0 {
							c := *m
							before := c
							r := c.Click(a, x, y)
							if c.n < 0 || c.n > MaxDepth {
								t.Fatalf("bad n after click")
							}
							if h.Kind == HitBox && (c.n != before.n || c.hl != before.hl || !r.Handled) {
								t.Fatalf("click on HitBox changed state at %d,%d", x, y)
							}
							c2 := *m
							if h.Kind == HitBox && c2.Hover(a, x, y) {
								t.Fatalf("hover on HitBox changed state")
							}
						}
					}
				}
				for kk := 0; kk < lay.N; kk++ {
					b := &lay.Boxes[kk]
					if b.Collapsed {
						continue
					}
					for i := 0; i < b.Vis; i++ {
						idx := b.Off + i
						y := b.Rect.Y + 1 + i
						if b.Above {
							y++
						}
						if b.list.entries[idx].kind == KindSeparator {
							continue
						}
						h := lay.Hit(b.Rect.X+3, y)
						if h.Kind != HitItem || h.Level != kk || h.Index != idx {
							t.Fatalf("drawn row level %d idx %d y=%d hit %+v", kk, idx, y, h)
						}
					}
				}
			}
		}
	}
	t.Logf("cells %d strip-cell hits %d", cells, strips)
}
