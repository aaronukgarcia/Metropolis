package flyout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// shownLetterRow returns the rune drawn in the letter column of a box row, or
// 0 when the box is too narrow to hold it.
func shownLetterRow(buf *core.Buffer, b *Box, y int) rune {
	x := b.Rect.X + 2
	if x >= b.Rect.X+b.Rect.W-1 {
		return 0
	}
	return buf.Get(x, y).Rune
}

func TestRoundR2_ExoticLettersShownAndPressable(t *testing.T) {
	letters := "aAbZ09!?~£é€ñÉßıſµǆİ#"
	for _, lt := range letters {
		items := []Item{sub("Menu", 'm', act("Entry one", lt, "one"), act("Other", 'o', "other"))}
		tb, err := NewTable(items)
		if err != nil {
			t.Logf("letter %q refused by NewTable: %v", lt, err)
			continue
		}
		m := New(tb)
		m.Open()
		m.Press(k("Down"))
		buf := core.NewBuffer(40, 8)
		a := fullArea(40, 8)
		m.Draw(buf, a)
		lay := m.Layout(a)
		b := &lay.Boxes[0]
		y := b.Rect.Y + 1
		got := shownLetterRow(buf, b, y)
		if buf.Get(b.Rect.X+3, y).Rune != ' ' {
			t.Errorf("letter %q: cell after letter is %q", lt, buf.Get(b.Rect.X+3, y).Rune)
		}
		m2 := New(tb)
		m2.Open()
		m2.Press(k("m"))
		r := m2.Press(keys.KeyRune(got))
		t.Logf("letter %q shown %q; pressing shown activates=%q", lt, got, r.Action)
		if r.Action != "one" && !strings.ContainsRune("ıſµ", lt) {
			t.Errorf("letter %q: shown %q but pressing the shown rune does not activate it (got %q)", lt, got, r.Action)
		}
		m3 := New(tb)
		m3.Open()
		m3.Press(k("m"))
		if r := m3.Press(keys.KeyRune(lt)); r.Action != "one" {
			t.Errorf("letter %q: pressing the configured letter does not activate (%q)", lt, r.Action)
		}
	}
}

func TestRoundR2_WidthSweepLetterVisible(t *testing.T) {
	for w := 1; w <= 40; w++ {
		m := sampleMenu(t)
		m.Open()
		run(m, "b", "h")
		a := fullArea(w, 12)
		buf := core.NewBuffer(w, 12)
		m.Draw(buf, a)
		lay := m.Layout(a)
		lost, shown := 0, 0
		for kk := 0; kk < lay.N; kk++ {
			b := &lay.Boxes[kk]
			if b.Rect.H == 0 {
				continue
			}
			for i := 0; i < b.Vis; i++ {
				y := b.Rect.Y + 1 + i
				if b.Above {
					y++
				}
				e := &b.list.entries[b.Off+i]
				if e.kind == KindSeparator {
					continue
				}
				covered := false
				for dk := kk + 1; dk < lay.N; dk++ {
					covered = covered || inside(lay.Boxes[dk].Rect, b.Rect.X+2, y)
				}
				if covered {
					continue
				}
				if shownLetterRow(buf, b, y) == unicode.ToUpper(e.letter) {
					shown++
				} else {
					lost++
				}
			}
		}
		t.Logf("width %2d: boxes %d shown %d lost %d | row1 %q", w, lay.N, shown, lost, rows(buf)[1])
	}
}

func TestRoundR2_HitMatchesDrawnRows(t *testing.T) {
	scripts := [][]string{{"b"}, {"b", "h"}, {"b", "Down"}, {"s"}, {"g", "Down"}}
	for _, sc := range scripts {
		for _, w := range []int{120, 60, 30, 20} {
			m := sampleMenu(t)
			m.Open()
			run(m, sc...)
			a := fullArea(w, 30)
			buf := core.NewBuffer(w, 30)
			m.Draw(buf, a)
			lay := m.Layout(a)
			for y := 0; y < 30; y++ {
				for x := 0; x < w; x++ {
					h := lay.Hit(x, y)
					if h.Kind != HitItem {
						continue
					}
					b := &lay.Boxes[h.Level]
					e := &b.list.entries[h.Index]
					lc := b.Rect.X + 2
					if lc >= b.Rect.X+b.Rect.W-1 {
						continue
					}
					covered := false
					for dk := h.Level + 1; dk < lay.N; dk++ {
						covered = covered || inside(lay.Boxes[dk].Rect, lc, y)
					}
					if !covered {
						if got := buf.Get(lc, y).Rune; got != unicode.ToUpper(e.letter) {
							t.Fatalf("script %v w %d hit (%d,%d)=entry %q but letter cell shows %q", sc, w, x, y, e.label, got)
						}
					}
				}
			}
			for i := 0; i < m.t.BarLen(); i++ {
				bx, bw, ok := lay.BarCell(i)
				if !ok {
					continue
				}
				for x := bx; x < bx+bw; x++ {
					if h := lay.Hit(x, 0); h.Kind != HitBar || h.Bar != i {
						t.Fatalf("bar %d cell %d hit %+v", i, x, h)
					}
				}
				nx, _, ok2 := lay.BarCell(i + 1)
				for x := bx + bw; ok2 && x < nx; x++ {
					if h := lay.Hit(x, 0); h.Kind != HitBarRow {
						t.Fatalf("gap cell %d hit %+v", x, h)
					}
				}
			}
		}
	}
	m := sampleMenu(t)
	buf := core.NewBuffer(60, 3)
	m.Draw(buf, fullArea(60, 3))
	t.Logf("closed bar |%s|", rows(buf)[0])
	m.Open()
	m.Draw(buf, fullArea(60, 3))
	t.Logf("open bar   |%s|", rows(buf)[0])
	if !strings.Contains(rows(buf)[0], "[B Build]") {
		t.Errorf("focused bar entry not [B Build]")
	}
}

func realisticTree(rng *rand.Rand, depth int) []Item {
	n := 3 + rng.Intn(14)
	out := make([]Item, 0, n)
	pool := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	for i := 0; i < n; i++ {
		lab := strings.Repeat("x", 5+rng.Intn(26))
		right := strings.Repeat("9", rng.Intn(26))
		var it Item
		if depth < 3 && rng.Intn(3) > 0 {
			it = sub(lab, pool[i], realisticTree(rng, depth+1)...)
			it.Right = right
		} else {
			it = actR(lab, pool[i], fmt.Sprintf("a%d", i), right)
		}
		out = append(out, it)
	}
	return out
}

func TestRoundR2_RealisticOcclusion(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	frames, hidMarker, hidAll, hidPart := 0, 0, 0, 0
	fullByW := map[int]int{}
	firstBad, dump, dumpNote := "", "", ""
	steps := []string{"Right", "Down", "Up", "Right", "End", "Left"}
	for tr := 0; tr < 300; tr++ {
		bar := []Item{}
		for i := 0; i < 5; i++ {
			bar = append(bar, sub(fmt.Sprintf("Bar%d", i), rune('a'+i), realisticTree(rng, 1)...))
		}
		tb, err := NewTable(bar)
		if err != nil {
			t.Fatal(err)
		}
		for _, area := range []core.Rect{{W: 120, H: 30}, {W: 80, H: 24}} {
			m := New(tb)
			m.Open()
			for s := 0; s < 40; s++ {
				m.Press(k(steps[rng.Intn(len(steps))]))
				if !m.IsOpen() {
					break
				}
				lay := m.Layout(area)
				frames++
				for kk := 0; kk < lay.N-1; kk++ {
					b := &lay.Boxes[kk]
					if b.Rect.H == 0 {
						continue
					}
					hi := m.hl[kk]
					if hi < b.Off || hi >= b.Off+b.Vis {
						continue
					}
					y := b.Rect.Y + 1 + (hi - b.Off)
					if b.Above {
						y++
					}
					cov, tot := 0, 0
					mk := false
					for x := b.Rect.X + 1; x < b.Rect.X+b.Rect.W-1; x++ {
						tot++
						c := false
						for dk := kk + 1; dk < lay.N; dk++ {
							c = c || inside(lay.Boxes[dk].Rect, x, y)
						}
						if c {
							cov++
							if x <= b.Rect.X+2 {
								mk = true
							}
						}
					}
					if cov > 0 {
						hidPart++
						if firstBad == "" {
							firstBad = fmt.Sprintf("tree %d area %v level %d covered %d of %d cells (box %v, deeper %v)", tr, area, kk, cov, tot, b.Rect, lay.Boxes[kk+1].Rect)
						}
					}
					if mk {
						hidMarker++
						if dump == "" && cov == tot {
							db := core.NewBuffer(area.W, area.H)
							m.Draw(db, area)
							dump = strings.Join(rows(db), "\n")
							dumpNote = fmt.Sprintf("level %d of %d, highlighted row y=%d, area %v, boxes %v %v %v path %v", kk, lay.N, y, area, lay.Boxes[0].Rect, lay.Boxes[1].Rect, lay.Boxes[2].Rect, m.Path())
						}
					}
					if cov == tot {
						hidAll++
						fullByW[area.W]++
					}
				}
			}
		}
	}
	t.Logf("fully covered by area width: %v", fullByW)
	t.Logf("frames %d; ancestor highlighted rows with any cell covered %d, marker or letter covered %d, fully covered %d; first: %s", frames, hidPart, hidMarker, hidAll, firstBad)
	t.Logf("%s\n%s", dumpNote, dump)
	if hidMarker > 0 || hidAll > 0 {
		t.Errorf("realistic content hides an ancestor highlighted row: marker or letter %d, whole %d of %d frames; first %s", hidMarker, hidAll, frames, firstBad)
	}
}
