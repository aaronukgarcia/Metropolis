package flyout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// r3Letters gives up to 60 distinct, printable, case-folding-stable letters.
var r3FootZero int

func r3Letter(i int) rune {
	if i < 26 {
		return rune('a' + i)
	}
	return rune(0x3b1 + (i - 26)) // Greek lower-case
}

func r3Label(rng *rand.Rand) string {
	n := 1 + rng.Intn(40)
	if rng.Intn(4) == 0 {
		n = 1 + rng.Intn(6)
	}
	pool := []rune("abcdefghij klmnopqrstuvwxyzéßñüøÅ→")
	var b strings.Builder
	for i := 0; i < n; i++ {
		r := pool[rng.Intn(len(pool))]
		if i == 0 && r == ' ' {
			r = 'x'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func r3List(rng *rand.Rand, depth int) []Item {
	n := 1 + rng.Intn(60)
	if rng.Intn(3) != 0 {
		n = 1 + rng.Intn(10)
	}
	items := []Item{}
	right := rng.Intn(31)
	hasSel := false
	nsub := 0
	for i := 0; i < n; i++ {
		if i > 0 && i < n-1 && rng.Intn(12) == 0 && items[len(items)-1].Kind != KindSeparator {
			items = append(items, sep)
			continue
		}
		lab := r3Label(rng)
		it := Item{Label: lab, Letter: r3Letter(i), Kind: KindAction, ActionID: "a"}
		if right > 0 && rng.Intn(2) == 0 {
			it.Right = strings.Repeat("9", 1+rng.Intn(right))
		}
		if depth < MaxDepth && nsub < 2 && rng.Intn(3) != 0 {
			nsub++
			sb := r3List(rng, depth+1)
			it = Item{Label: lab, Letter: r3Letter(i), Kind: KindSubmenu, Items: sb, Right: it.Right}
		}
		if rng.Intn(8) == 0 {
			it.Grey = "not yet"
			if it.Kind == KindAction {
				it.ActionID = ""
			}
		} else {
			hasSel = true
		}
		items = append(items, it)
	}
	if !hasSel {
		items = append(items, act("ok", 'z', "ok"))
	}
	return items
}

func r3Check(m *Menu, area core.Rect, buf *core.Buffer, bw, bh int) string {
	lay := m.Layout(area)
	if !lay.Open {
		return ""
	}
	type rc struct{ x, y, w, h, k int }
	var rs []rc
	for k := 0; k < lay.N; k++ {
		b := &lay.Boxes[k]
		r := b.Rect
		if r.H == 0 && m.notice != "" && area.H < 5 {
			r3FootZero++
			return ""
		}
		if r.H == 0 {
			return fmt.Sprintf("level %d has no box: rects %v n=%d entries=%d labelW=%d rightW=%d hl=%v off=%v", k, rectsOfR3(&lay), lay.N, len(b.list.entries), b.list.labelW, b.list.rightW, m.hl[:m.n], m.off[:m.n])
		}
		if r.W <= 0 || r.X < area.X || r.Y < area.Y+1 || r.X+r.W > area.X+area.W || r.Y+r.H > area.Y+area.H {
			return fmt.Sprintf("level %d box %v outside area %v", k, r, area)
		}
		if b.Collapsed && (r.W != 1 || r.H != 1) {
			return fmt.Sprintf("level %d bad strip %v", k, r)
		}
		for _, o := range rs {
			if r.X < o.x+o.w && o.x < r.X+r.W && r.Y < o.y+o.h && o.y < r.Y+r.H {
				return fmt.Sprintf("level %d box %v shares a cell with level %d", k, r, o.k)
			}
		}
		rs = append(rs, rc{r.X, r.Y, r.W, r.H, k})
		if b.Collapsed {
			if k == lay.N-1 {
				return "deepest collapsed"
			}
			if buf.Get(r.X, r.Y).Rune != runeHighlight {
				return fmt.Sprintf("level %d strip has no marker", k)
			}
			continue
		}
		hi := m.hl[k]
		if hi < b.Off || hi >= b.Off+b.Vis {
			return fmt.Sprintf("level %d highlight %d not in [%d,%d)", k, hi, b.Off, b.Off+b.Vis)
		}
		y := r.Y + 1 + hi - b.Off
		if b.Above {
			y++
		}
		if y >= r.Y+r.H-1 {
			return fmt.Sprintf("level %d highlight row %d outside box %v", k, y, r)
		}
		e := &b.list.entries[hi]
		if (!m.inBar || k != 0) && buf.Get(r.X+1, y).Rune != runeHighlight {
			return fmt.Sprintf("level %d marker hidden at (%d,%d) box %v", k, r.X+1, y, r)
		}
		if buf.Get(r.X+2, y).Rune != unicode.ToUpper(e.letter) {
			return fmt.Sprintf("level %d letter hidden at (%d,%d) box %v", k, r.X+2, y, r)
		}
		if k == lay.N-1 && b.LabelW >= minLabelRunes {
			first := []rune(e.label)[0]
			if unicode.ToUpper(buf.Get(r.X+2+letterCols, y).Rune) != unicode.ToUpper(first) {
				return fmt.Sprintf("deepest label start hidden box %v", r)
			}
		}
		if k > 0 {
			// aligned with parent's highlighted row (as drawn) unless moved up to fit
			p := &lay.Boxes[k-1]
			var py int
			if p.Collapsed {
				py = p.Rect.Y
			} else {
				py = p.Rect.Y + 1 + m.hl[k-1] - p.Off
				if p.Above {
					py++
				}
			}
			if r.Y+1 > py && r.Y+r.H-1 < area.Y+area.H-1 && r.Y+r.H != area.Y+area.H {
				return fmt.Sprintf("level %d top %d below parent highlight row %d without being bottom-fitted", k, r.Y, py)
			}
			if py < r.Y+1 || py > r.Y+r.H-2 {
				{
					// parent's row is not beside the child's rows: it is allowed only when the child was moved up
					if r.Y+1 < py && r.Y+r.H-1 != area.Y+area.H-1 {
						return fmt.Sprintf("level %d box %v not beside parent row %d", k, r, py)
					}
				}
			}
		}
	}
	// nothing outside the area
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			if x >= area.X && x < area.X+area.W && y >= area.Y && y < area.Y+area.H {
				continue
			}
			if buf.Get(x, y).Rune != '#' {
				return fmt.Sprintf("wrote outside area at (%d,%d)=%q", x, y, buf.Get(x, y).Rune)
			}
		}
	}
	return ""
}

func TestRoundR3_Cascade_OwnGenerator(t *testing.T) {
	sizes := [][2]int{{120, 30}, {119, 29}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {30, 8}, {20, 5}, {10, 4}}
	offs := [][2]int{{0, 0}, {3, 2}}
	toks := []string{"Right", "Right", "Right", "Down", "Down", "Up", "End", "Home", "Left", "Down", "Right", "Enter", "Esc", "Down", "Up"}
	rng := rand.New(rand.NewSource(7771))
	frames, deep, coll := 0, 0, 0
	for tr := 0; tr < 30; tr++ {
		nb := 1 + rng.Intn(8)
		bar := []Item{}
		for i := 0; i < nb; i++ {
			bar = append(bar, sub(strings.Repeat("B", 1+rng.Intn(10)), r3Letter(i), r3List(rng, 1)...))
		}
		tb, err := NewTable(bar)
		if err != nil {
			t.Fatal(err)
		}
		for _, sz := range sizes {
			for _, of := range offs {
				area := core.Rect{X: of[0], Y: of[1], W: sz[0], H: sz[1]}
				bw, bh := sz[0]+of[0]+2, sz[1]+of[1]+2
				m := New(tb)
				m.Open()
				for s := 0; s < 40; s++ {
					m.Press(k(toks[rng.Intn(len(toks))]))
					if !m.IsOpen() {
						m.Open()
					}
					buf := mapBuffer(bw, bh)
					m.Draw(buf, area)
					frames++
					lay := m.Layout(area)
					if lay.N == MaxDepth {
						deep++
					}
					if lay.N > 0 && lay.Boxes[0].Collapsed {
						coll++
					}
					if msg := r3Check(m, area, buf, bw, bh); msg != "" {
						t.Fatalf("tree %d area %v step %d path %v: %s\n%s", tr, area, s, m.Path(), msg, strings.Join(rows(buf), "\n"))
					}
				}
			}
		}
	}
	t.Logf("frames %d fourdeep %d collapsed %d footzero %d", frames, deep, coll, r3FootZero)
}

func rectsOfR3(l *Layout) []core.Rect {
	out := []core.Rect{}
	for i := 0; i < l.N; i++ {
		out = append(out, l.Boxes[i].Rect)
	}
	return out
}
