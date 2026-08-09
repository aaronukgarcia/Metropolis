package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestBrailleCanvas_DotMapping(t *testing.T) {
	// Each of the 8 dot positions, set alone, must produce the known
	// standard Braille codepoint (brailleBase + the single dot's bit).
	cases := []struct {
		px, py int
		want   rune
	}{
		{0, 0, brailleBase + 0x01}, // dot 1
		{0, 1, brailleBase + 0x02}, // dot 2
		{0, 2, brailleBase + 0x04}, // dot 3
		{0, 3, brailleBase + 0x40}, // dot 7
		{1, 0, brailleBase + 0x08}, // dot 4
		{1, 1, brailleBase + 0x10}, // dot 5
		{1, 2, brailleBase + 0x20}, // dot 6
		{1, 3, brailleBase + 0x80}, // dot 8
	}
	for _, c := range cases {
		canvas := NewBrailleCanvas(1, 1)
		canvas.SetDot(c.px, c.py)
		if got := canvas.Rune(0, 0); got != c.want {
			t.Errorf("SetDot(%d,%d) -> Rune = %U, want %U", c.px, c.py, got, c.want)
		}
	}
}

func TestBrailleCanvas_AllDotsSet(t *testing.T) {
	canvas := NewBrailleCanvas(1, 1)
	for py := 0; py < 4; py++ {
		for px := 0; px < 2; px++ {
			canvas.SetDot(px, py)
		}
	}
	if got, want := canvas.Rune(0, 0), brailleBase+0xFF; got != want {
		t.Errorf("all dots set -> Rune = %U, want %U", got, want)
	}
}

func TestBrailleCanvas_OutOfRangeIgnored(t *testing.T) {
	canvas := NewBrailleCanvas(1, 1)
	canvas.SetDot(-1, 0)
	canvas.SetDot(0, -1)
	canvas.SetDot(2, 0)
	canvas.SetDot(0, 4)
	if got := canvas.Rune(0, 0); got != brailleBase {
		t.Fatalf("out-of-range SetDot calls affected the canvas: %U", got)
	}
}

func TestBrailleChart_HorizontalLineKnownMask(t *testing.T) {
	// Two equal points (flat) span one cell (1x1 rect = 2x4 dots): both
	// map to dot-row dotsH/2=2, dot-columns 0 and 1 -> a horizontal
	// 2-dot line at row 2, i.e. dots (0,2) and (1,2): bits 0x04 (dot3)
	// and 0x20 (dot6), mask 0x24.
	buf := core.NewBuffer(1, 1)
	BrailleChart(buf, core.Rect{X: 0, Y: 0, W: 1, H: 1}, []float64{0, 0}, nil, tcell.StyleDefault, tcell.StyleDefault)

	want := brailleBase + 0x24
	if got := buf.Get(0, 0).Rune; got != want {
		t.Fatalf("cell(0,0) rune = %U, want %U", got, want)
	}
}

func TestBrailleChart_HistoryAndProjectionOccupySeparateCellsWithDistinctStyles(t *testing.T) {
	buf := core.NewBuffer(2, 1)
	historyStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
	projStyle := tcell.StyleDefault.Foreground(tcell.ColorGray).Dim(true)

	BrailleChart(buf, core.Rect{X: 0, Y: 0, W: 2, H: 1}, []float64{5}, []float64{5}, historyStyle, projStyle)

	c0 := buf.Get(0, 0)
	c1 := buf.Get(1, 0)
	wantRune := brailleBase + 0x04 // single flat point -> dot row 2 -> dot3 bit
	if c0.Rune != wantRune {
		t.Fatalf("history cell rune = %U, want %U", c0.Rune, wantRune)
	}
	if c1.Rune != wantRune {
		t.Fatalf("projection cell rune = %U, want %U", c1.Rune, wantRune)
	}
	if c0.Style != historyStyle {
		t.Fatalf("history cell style = %v, want %v", c0.Style, historyStyle)
	}
	if c1.Style != projStyle {
		t.Fatalf("projection cell style = %v, want %v", c1.Style, projStyle)
	}
}

func TestBrailleChart_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(2, 2)
	BrailleChart(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, nil, nil, tcell.StyleDefault, tcell.StyleDefault)
	BrailleChart(nil, core.Rect{X: 0, Y: 0, W: 2, H: 2}, []float64{1}, nil, tcell.StyleDefault, tcell.StyleDefault)
	BrailleChart(buf, core.Rect{X: 0, Y: 0, W: 2, H: 2}, nil, nil, tcell.StyleDefault, tcell.StyleDefault)
}

func TestBrailleChart_Deterministic(t *testing.T) {
	buf1 := core.NewBuffer(4, 3)
	buf2 := core.NewBuffer(4, 3)
	hist := []float64{1, 5, 2, 8, 3}
	proj := []float64{3, 6, 4}
	rect := core.Rect{X: 0, Y: 0, W: 4, H: 3}
	BrailleChart(buf1, rect, hist, proj, tcell.StyleDefault, tcell.StyleDefault.Dim(true))
	BrailleChart(buf2, rect, hist, proj, tcell.StyleDefault, tcell.StyleDefault.Dim(true))
	g1, g2 := gridRunes(buf1), gridRunes(buf2)
	for i := range g1 {
		if g1[i] != g2[i] {
			t.Fatalf("BrailleChart not deterministic: row %d %q vs %q", i, g1[i], g2[i])
		}
	}
}
