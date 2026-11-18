package flyout

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

const (
	testW = 120
	testH = 30
)

// Criterion 11: a box drawn over a buffer full of map glyphs leaves none of
// them inside the box, and the bar row is blank of them; the fill is not
// over-wide (map glyphs remain outside).
func TestDraw_BoxesBlankTheirWholeArea(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	buf := mapBuffer(testW, testH)
	area := fullArea(testW, testH)
	lay := m.Layout(area)
	m.DrawLayout(buf, &lay)
	if lay.N != 2 {
		t.Fatalf("open lists = %d, want 3", lay.N)
	}
	for k := 0; k < lay.N; k++ {
		r := lay.Boxes[k].Rect
		if r.W == 0 || r.H == 0 {
			t.Fatalf("box %d missing", k)
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if buf.Get(x, y).Rune == '#' {
					t.Fatalf("map glyph left inside box %d at (%d,%d)", k, x, y)
				}
			}
		}
	}
	for x := 0; x < testW; x++ {
		if buf.Get(x, 0).Rune == '#' {
			t.Fatalf("map glyph left on the bar row at x=%d", x)
		}
	}
	// Not over-wide: every cell outside the bar and the boxes is still a map glyph.
	for y := 1; y < testH; y++ {
		for x := 0; x < testW; x++ {
			in := false
			for k := 0; k < lay.N; k++ {
				in = in || inside(lay.Boxes[k].Rect, x, y)
			}
			if !in && buf.Get(x, y).Rune != '#' {
				t.Fatalf("cell (%d,%d) outside every box was overwritten", x, y)
			}
		}
	}
}

// Every list of the shipped-shape tree, drawn open over map glyphs, leaves no
// glyph in any box (the BUG-1234 class, swept over every path).
func TestDraw_EveryListBlanksOverMap(t *testing.T) {
	tb := mustTable(t, sampleItems())
	var visit func(l *list, letters []string)
	visit = func(l *list, letters []string) {
		m := New(tb)
		m.Open()
		run(m, letters...)
		buf := mapBuffer(testW, testH)
		lay := m.Layout(fullArea(testW, testH))
		m.DrawLayout(buf, &lay)
		for k := 0; k < lay.N; k++ {
			r := lay.Boxes[k].Rect
			for y := r.Y; y < r.Y+r.H; y++ {
				for x := r.X; x < r.X+r.W; x++ {
					if buf.Get(x, y).Rune == '#' {
						t.Fatalf("path %v box %d: glyph at (%d,%d)", letters, k, x, y)
					}
				}
			}
		}
		for i := range l.entries {
			if l.entries[i].kind == KindSubmenu {
				visit(l.entries[i].sub, append(append([]string(nil), letters...), string(l.entries[i].letter)))
			}
		}
	}
	for b := range tb.bar.entries {
		visit(tb.bar.entries[b].sub, []string{string(tb.bar.entries[b].letter)})
	}
}

// The three-level frame, exact. Pins the look: bar with the highlighted entry
// in brackets, a marker on the highlighted rows, the right column aligned, the
// submenu arrow, the greyed reason in text.
func TestDraw_ThreeLevelFrame(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	buf := mapBuffer(testW, testH)
	m.Draw(buf, fullArea(testW, testH))
	want := []string{
		"[B Build]  S Screens   G Game",
		"┌───────────────────────────────────┐┌───────────────────────────────┐",
		"│►H Housing                       > ││►D Dwelling   £15,000  8 homes │",
		"│ J Jobs           not active yet > ││ F Flats     £40,000  24 homes │",
		"├───────────────────────────────────┤└───────────────────────────────┘",
		"│ Y Buy this cell                   │",
		"│ R Demolish here                   │",
		"└───────────────────────────────────┘",
	}
	got := rows(buf)
	for i, w := range want {
		n := len([]rune(w))
		row := []rune(got[i])
		if string(row[:n]) != w {
			t.Errorf("row %d:\n got %q\nwant %q", i, string(row[:n]), w)
		}
		// Right of the frame: the bar row is blank, other rows still map glyphs.
		fill := '#'
		if i == 0 {
			fill = ' '
		}
		for x := n; x < len(row); x++ {
			if row[x] != fill {
				t.Fatalf("row %d col %d = %q, want %q", i, x, row[x], fill)
			}
		}
	}
	if got[8] != strings.Repeat("#", testW) {
		t.Errorf("row 8 not untouched: %q", got[8])
	}
}

// The highlighted entry is distinct without colour: a marker rune, reverse
// attribute and (for greyed) dim, plus the reason as text.
func TestDraw_HighlightAndGreyReadWithoutColour(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b") // Build list, Housing highlighted
	buf := core.NewBuffer(testW, testH)
	lay := m.Layout(fullArea(testW, testH))
	m.DrawLayout(buf, &lay)
	b := lay.Boxes[0]
	row := b.Rect.Y + 1 // Housing
	marker := buf.Get(b.Rect.X+1, row)
	if marker.Rune != runeHighlight {
		t.Fatalf("highlight marker = %q", marker.Rune)
	}
	_, _, attrs := marker.Style.Decompose()
	if attrs&1<<2 == 0 && marker.Style == buf.Get(b.Rect.X+1, row+2).Style {
		t.Fatal("highlighted row is styled like an ordinary row")
	}
	// The other rows carry no marker.
	if r := buf.Get(b.Rect.X+1, row+1).Rune; r == runeHighlight {
		t.Fatal("a second row carries the marker")
	}
	// Greyed: the reason is on the row as text, and the row is dim.
	jobs := rowText(buf, b.Rect.Y+2, b.Rect)
	if !strings.Contains(jobs, "Jobs") || !strings.Contains(jobs, "not active yet") {
		t.Fatalf("greyed row = %q", jobs)
	}
	if buf.Get(b.Rect.X+2+letterCols, b.Rect.Y+2).Style == buf.Get(b.Rect.X+2+letterCols, row).Style {
		t.Fatal("greyed row is not styled differently from the highlighted one")
	}
	plain := buf.Get(b.Rect.X+2+letterCols, b.Rect.Y+4).Style // Buy this cell: ordinary
	if buf.Get(b.Rect.X+2+letterCols, b.Rect.Y+2).Style == plain {
		t.Fatal("greyed row is not dimmed against an ordinary row")
	}
}

func rowText(buf *core.Buffer, y int, r core.Rect) string {
	var b strings.Builder
	for x := r.X; x < r.X+r.W; x++ {
		b.WriteRune(buf.Get(x, y).Rune)
	}
	return b.String()
}

// The letter is underlined in the label so the key reads without colour.
func TestDraw_LetterUnderlined(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	buf := core.NewBuffer(testW, testH)
	m.Draw(buf, fullArea(testW, testH))
	// Bar: "[B Build]" is 9 cells; "Screens" is at x=10: lead, letter, space,
	// then the label, whose 'S' is at x=13 and 'c' at x=14.
	_, _, a := buf.Get(13, 0).Style.Decompose()
	_, _, b := buf.Get(14, 0).Style.Decompose()
	if a == b {
		t.Fatalf("letter cell style equals its neighbour (attrs %v)", a)
	}
}

// Right-hand column: every row's text ends in the same cell.
func TestDraw_RightColumnAligned(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	buf := core.NewBuffer(testW, testH)
	lay := m.Layout(fullArea(testW, testH))
	m.DrawLayout(buf, &lay)
	b := lay.Boxes[1]
	var ends []int
	for i := 0; i < 2; i++ {
		txt := strings.TrimRight(rowText(buf, b.Rect.Y+1+i, core.Rect{X: b.Rect.X + 1, W: b.Rect.W - 2}), " │")
		ends = append(ends, utf8.RuneCountInString(txt))
		if !strings.Contains(txt, "£") {
			t.Fatalf("row %d lost its cost: %q", i, txt)
		}
	}
	if ends[0] != ends[1] {
		t.Fatalf("right column not aligned: ends %v", ends)
	}
	// Multi-byte glyphs are one cell: the box width is the rune width, not bytes.
	if got, want := b.Rect.W, 2+2+letterCols+len("Dwelling")+2+utf8.RuneCountInString("£40,000  24 homes"); got != want {
		t.Fatalf("box width = %d, want %d (cells, not bytes)", got, want)
	}
}

// Criterion 13: a list taller than the room scrolls; the highlight is always
// on screen and the marker lines count what is hidden.
func TestDraw_LongListScrolls(t *testing.T) {
	var items []Item
	for i := 0; i < 40; i++ {
		items = append(items, act(fmt.Sprintf("Item %02d", i), rune('a'+i%26)+rune(i/26)*0, fmt.Sprintf("id%d", i)))
	}
	// letters must be unique: use a-z then A-N-ish distinct runes.
	for i := range items {
		items[i].Letter = rune(0x4e00 + i)
	}
	m := New(mustTable(t, []Item{sub("Long", 'l', items...)}))
	m.Open()
	run(m, "Down")
	area := fullArea(testW, 12) // 12 rows: bar + at most 11 rows of box
	seenAbove, seenBelow := false, false
	for step := 0; step < 100; step++ { // a full Down-run with wrap
		buf := core.NewBuffer(testW, 12)
		lay := m.Layout(area)
		m.DrawLayout(buf, &lay)
		b := lay.Boxes[0]
		if b.Rect.Y+b.Rect.H > 12 {
			t.Fatalf("step %d: box runs off the area: %+v", step, b.Rect)
		}
		hl := m.Path()[1]
		if hl < b.Off || hl >= b.Off+b.Vis {
			t.Fatalf("step %d: highlight %d outside window [%d,%d)", step, hl, b.Off, b.Off+b.Vis)
		}
		// The highlighted row is drawn with the marker.
		found := false
		for y := b.Rect.Y + 1; y < b.Rect.Y+b.Rect.H-1; y++ {
			if buf.Get(b.Rect.X+1, y).Rune == runeHighlight {
				found = true
			}
		}
		if !found {
			t.Fatalf("step %d: no highlighted row on screen", step)
		}
		text := strings.Join(rows(buf), "\n")
		if b.Above {
			seenAbove = true
			if !strings.Contains(text, fmt.Sprintf("... %d more above", b.Off)) {
				t.Fatalf("step %d: above marker missing for off=%d", step, b.Off)
			}
		} else if strings.Contains(text, "more above") {
			t.Fatalf("step %d: stray above marker", step)
		}
		if b.Below {
			seenBelow = true
			if !strings.Contains(text, fmt.Sprintf("... %d more below", 40-(b.Off+b.Vis))) {
				t.Fatalf("step %d: below marker missing", step)
			}
		}
		run(m, "Down")
	}
	if !seenAbove || !seenBelow {
		t.Fatalf("never saw both markers (above=%v below=%v)", seenAbove, seenBelow)
	}
	// Upward too.
	for step := 0; step < 45; step++ {
		run(m, "Up")
		lay := m.Layout(area)
		b := lay.Boxes[0]
		hl := m.Path()[1]
		if hl < b.Off || hl >= b.Off+b.Vis {
			t.Fatalf("up step %d: highlight %d outside [%d,%d)", step, hl, b.Off, b.Off+b.Vis)
		}
	}
}

// Criterion 14 (r3): open lists form one left-to-right cascade whose boxes
// never share a column; a list that would pass the right edge shifts the whole
// cascade left instead of flipping onto its parent.
func TestLayout_CascadeShiftsLeftAtTheRightEdge(t *testing.T) {
	items := sampleItems()
	items[2].Items = append(items[2].Items, sub("Speed", 'e', actR("Faster", 'f', "f", "]"), actR("Slower", 's', "s", "[")))
	m := New(mustTable(t, items))
	m.Open()
	run(m, "g", "e")
	wide := m.Layout(fullArea(testW, testH))
	if wide.Boxes[1].Rect.X != wide.Boxes[0].Rect.X+wide.Boxes[0].Rect.W {
		t.Fatalf("at full width list 2 should start where list 1 ends: %+v", wide.Boxes[1])
	}
	// Narrow the area so the cascade does not fit from the bar entry.
	right := wide.Boxes[0].Rect.X + wide.Boxes[0].Rect.W + wide.Boxes[1].Rect.W - 1
	lay := m.Layout(fullArea(right, testH))
	b2, b1 := lay.Boxes[1], lay.Boxes[0]
	if b2.Rect.X != b1.Rect.X+b1.Rect.W {
		t.Fatalf("list 2 does not start where list 1 ends: %+v vs %+v", b2.Rect, b1.Rect)
	}
	if b1.Rect.X >= wide.Boxes[0].Rect.X {
		t.Fatalf("cascade did not shift left: %d vs %d", b1.Rect.X, wide.Boxes[0].Rect.X)
	}
	if !lay.disjoint() {
		t.Fatalf("boxes overlap or leave the area: %+v", lay.Boxes[:lay.N])
	}
}

// Criterion 12: nothing is drawn outside the area or the buffer at any size,
// and no size panics.
func TestDraw_NeverOutsideAnySize(t *testing.T) {
	tb := mustTable(t, sampleItems())
	sizes := [][2]int{{0, 0}, {1, 1}, {2, 2}, {5, 3}, {10, 4}, {20, 6}, {40, 10}, {40, 12}, {60, 24}, {120, 30}, {200, 60}}
	paths := [][]string{nil, {"b"}, {"b", "h"}, {"b", "j"}, {"s"}, {"s", "v"}, {"g", "Down"}}
	for _, sz := range sizes {
		for _, p := range paths {
			m := New(tb)
			m.Open()
			run(m, p...)
			// A guard border: draw into a buffer one cell bigger each way, then
			// check the guard ring was never touched.
			full := mapBuffer(sz[0]+2, sz[1]+2)
			area := core.Rect{X: 1, Y: 1, W: sz[0], H: sz[1]}
			m.Draw(full, area)
			for y := 0; y < sz[1]+2; y++ {
				for x := 0; x < sz[0]+2; x++ {
					if !inside(area, x, y) && full.Get(x, y).Rune != '#' {
						t.Fatalf("size %v path %v: wrote outside the area at (%d,%d)", sz, p, x, y)
					}
				}
			}
			// And into a buffer smaller than the area: no panic.
			m.Draw(core.NewBuffer(sz[0]/2, sz[1]/2), core.Rect{W: sz[0], H: sz[1]})
		}
	}
}

// Criterion 12: every entry stays reachable at small sizes (it scrolls).
func TestLayout_EveryEntryReachableAtSmallSizes(t *testing.T) {
	var items []Item
	for i := 0; i < 20; i++ {
		items = append(items, act(fmt.Sprintf("Entry %d", i), rune(0x4e00+i), "x"))
	}
	m := New(mustTable(t, []Item{sub("Long", 'l', items...)}))
	for _, sz := range [][2]int{{40, 12}, {60, 24}, {120, 30}, {40, 5}} {
		m.Close()
		m.Open()
		run(m, "Down")
		for i := 0; i < 20; i++ {
			lay := m.Layout(fullArea(sz[0], sz[1]))
			b := lay.Boxes[0]
			if b.Rect.H == 0 {
				t.Fatalf("size %v: no box", sz)
			}
			if h := m.Path()[1]; h < b.Off || h >= b.Off+b.Vis {
				t.Fatalf("size %v entry %d: not shown", sz, h)
			}
			run(m, "Down")
		}
	}
}

// Over-wide content is cut: the right column goes first, then the label.
func TestLayout_NarrowAreaCutsRightColumnFirst(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "b", "h")
	buf := core.NewBuffer(40, 20)
	lay := m.Layout(fullArea(40, 20))
	m.DrawLayout(buf, &lay)
	for k := 0; k < lay.N; k++ {
		r := lay.Boxes[k].Rect
		if r.X+r.W > 40 {
			t.Fatalf("box %d crosses the right edge: %+v", k, r)
		}
	}
	text := strings.Join(rows(buf), "\n")
	if !strings.Contains(text, "Dwelling") {
		t.Fatal("the label was cut before the right column")
	}
	tiny := core.NewBuffer(24, 20)
	lay = m.Layout(fullArea(24, 20))
	m.DrawLayout(tiny, &lay)
	if !strings.Contains(strings.Join(rows(tiny), "\n"), "…") {
		t.Fatal("no ellipsis where text was cut")
	}
}

// The notice for a greyed entry shows on the deepest box's footer.
func TestDraw_NoticeOnFooter(t *testing.T) {
	m := sampleMenu(t)
	m.Open()
	run(m, "s", "v")
	buf := core.NewBuffer(testW, testH)
	lay := m.Layout(fullArea(testW, testH))
	m.DrawLayout(buf, &lay)
	b := lay.Boxes[0]
	if !b.Foot {
		t.Fatal("no footer line")
	}
	foot := rowText(buf, b.Rect.Y+b.Rect.H-2, b.Rect)
	if !strings.Contains(foot, "no engine command yet") {
		t.Fatalf("footer = %q", foot)
	}
	run(m, "Up")
	lay = m.Layout(fullArea(testW, testH))
	if lay.Boxes[0].Foot {
		t.Fatal("footer survived the next key")
	}
}

// Criterion 15: the bar is drawn closed too, and its row is blanked first.
func TestDraw_BarClosedAndBlanked(t *testing.T) {
	m := sampleMenu(t)
	buf := mapBuffer(testW, testH)
	m.Draw(buf, fullArea(testW, testH))
	row := rows(buf)[0]
	if !strings.HasPrefix(row, " B Build   S Screens   G Game ") {
		t.Fatalf("closed bar = %q", row)
	}
	if strings.Contains(row, "#") {
		t.Fatalf("closed bar row not blanked: %q", row)
	}
	if strings.Contains(rows(buf)[1], " ") {
		t.Fatal("a closed menu drew below the bar")
	}
}
