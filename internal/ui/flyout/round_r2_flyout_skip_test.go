package flyout

import (
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// The r1 drawn-glyph check, restated without the deeper-box skip as two
// counts: cells checked and held, cells skipped for a deeper box. The check
// must hold for every cell not covered, for both the letter and the label's
// first rune, and the skipped cells must be a small part of the whole.
func TestRoundR2_DrawnGlyphCheckSkipCount(t *testing.T) {
	rc := func(x, y, w, h int) core.Rect { return core.Rect{X: x, Y: y, W: w, H: h} }
	areas := []core.Rect{rc(0, 0, 30, 10), rc(0, 0, 120, 30), rc(0, 0, 200, 60), rc(7, 5, 60, 20), rc(150, 40, 50, 20)}
	scripts := [][]string{{"a"}, {"a", "a"}, {"a", "a", "b"}, {"a", "a", "b", "c"}, {"a", "m"}, {"z", "a", "b", "c"}}
	checked, skipped := 0, 0
	for _, ar := range areas {
		for _, sc := range scripts {
			m := New(mustTable(t, wideTree()))
			m.Open()
			for _, s := range sc {
				m.Press(k(s))
			}
			buf := core.NewBuffer(200, 60)
			m.Draw(buf, ar)
			lay := m.Layout(ar)
			for y := ar.Y; y < ar.Y+ar.H; y++ {
				for x := ar.X; x < ar.X+ar.W; x++ {
					h := lay.Hit(x, y)
					if h.Kind != HitItem {
						continue
					}
					b := &lay.Boxes[h.Level]
					e := &b.list.entries[h.Index]
					covered := false
					for dk := h.Level + 1; dk < lay.N; dk++ {
						covered = covered || inside(lay.Boxes[dk].Rect, b.Rect.X+2+letterCols, y) || inside(lay.Boxes[dk].Rect, b.Rect.X+2, y)
					}
					if covered {
						skipped++
						continue
					}
					checked++
					if b.LabelW > 1 {
						want, _ := utf8.DecodeRuneInString(e.label)
						if got := buf.Get(b.Rect.X+2+letterCols, y).Rune; got != want {
							t.Fatalf("label start: want %q got %q", want, got)
						}
					}
					if got := buf.Get(b.Rect.X+2, y).Rune; got != unicode.ToUpper(e.letter) {
						t.Fatalf("letter: want %q got %q", unicode.ToUpper(e.letter), got)
					}
				}
			}
		}
	}
	t.Logf("checked %d, skipped as covered %d", checked, skipped)
	if checked == 0 || skipped*4 > checked {
		t.Errorf("too many cells skipped: checked %d skipped %d", checked, skipped)
	}
}

func TestRoundR2_ZeroAllocOwnTree(t *testing.T) {
	m := New(mustTable(t, hiddenLetterItems()))
	m.Open()
	buf := core.NewBuffer(120, 30)
	a := fullArea(120, 30)
	dn, up, rt, lf := k("Down"), k("Up"), k("Right"), k("Left")
	allocs := testing.AllocsPerRun(50, func() {
		m.Press(dn)
		m.Draw(buf, a)
		m.Press(rt)
		m.Draw(buf, a)
		m.Press(lf)
		m.Press(up)
		m.Draw(buf, a)
	})
	if allocs != 0 {
		t.Errorf("allocs per run: %v", allocs)
	}
}
