package widgets

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestQueueLane_GlyphCountMatchesLength(t *testing.T) {
	buf := core.NewBuffer(20, 1)
	QueueLane(buf, core.Rect{X: 0, Y: 0, W: 20, H: 1}, 5, CargoFreight, 42, tcell.StyleDefault)

	row := gridRunes(buf)[0]
	g := CargoFreight.Glyph()
	if got := strings.Count(row, string(g)); got != 5 {
		t.Fatalf("glyph count = %d, want 5 (row: %q)", got, row)
	}
	if !strings.Contains(row, "42s") {
		t.Fatalf("row %q does not contain wait-time figure %q", row, "42s")
	}
	// The queue grows leftward from the right edge: the rightmost cell
	// must be a cargo glyph.
	rr := []rune(row)
	if rr[len(rr)-1] != g {
		t.Fatalf("rightmost cell is not the cargo glyph: %q", row)
	}
}

func TestQueueLane_LengthClampedToAvailableWidth(t *testing.T) {
	buf := core.NewBuffer(10, 1)
	QueueLane(buf, core.Rect{X: 0, Y: 0, W: 10, H: 1}, 1000, CargoGeneral, 5, tcell.StyleDefault)
	row := gridRunes(buf)[0]
	g := CargoGeneral.Glyph()
	count := strings.Count(row, string(g))
	if count <= 0 || count > 10 {
		t.Fatalf("glyph count %d not clamped sanely into a 10-wide rect (row %q)", count, row)
	}
}

func TestQueueLane_NegativeLengthRendersZeroGlyphs(t *testing.T) {
	buf := core.NewBuffer(10, 1)
	QueueLane(buf, core.Rect{X: 0, Y: 0, W: 10, H: 1}, -5, CargoGeneral, 0, tcell.StyleDefault)
	row := gridRunes(buf)[0]
	g := CargoGeneral.Glyph()
	if strings.Count(row, string(g)) != 0 {
		t.Fatalf("negative length rendered glyphs: %q", row)
	}
	if !strings.Contains(row, "0s") {
		t.Fatalf("row %q missing wait figure", row)
	}
}

func TestQueueLane_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(5, 1)
	QueueLane(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, 3, CargoGeneral, 5, tcell.StyleDefault)
	QueueLane(nil, core.Rect{X: 0, Y: 0, W: 5, H: 1}, 3, CargoGeneral, 5, tcell.StyleDefault)
	QueueLane(buf, core.Rect{X: 0, Y: 0, W: 5, H: 1}, 3, CargoGeneral, -10, tcell.StyleDefault)
}

func TestQueueLane_Deterministic(t *testing.T) {
	buf1 := core.NewBuffer(15, 1)
	buf2 := core.NewBuffer(15, 1)
	QueueLane(buf1, core.Rect{X: 0, Y: 0, W: 15, H: 1}, 7, CargoPassenger, 30, tcell.StyleDefault)
	QueueLane(buf2, core.Rect{X: 0, Y: 0, W: 15, H: 1}, 7, CargoPassenger, 30, tcell.StyleDefault)
	if a, b := gridRunes(buf1)[0], gridRunes(buf2)[0]; a != b {
		t.Fatalf("QueueLane not deterministic: %q vs %q", a, b)
	}
}
