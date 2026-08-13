package demo

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func renderedText(buf *core.Buffer, rect core.Rect) []string {
	var lines []string
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		var sb strings.Builder
		for x := rect.X; x < rect.X+rect.W; x++ {
			c := buf.Get(x, y)
			if c.Rune == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Rune)
			}
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return lines
}

// TestHoursByActivity_SaturdayHoursByActivity is DEMO-4's check: a
// fixture activity/hours breakdown renders totals that sum to a value
// no test hardcodes (drawn from the fixture), and changing the fixture
// changes the rendered breakdown.
func TestHoursByActivity_SaturdayHoursByActivity(t *testing.T) {
	fixtureA := []ActivityHours{
		{Activity: "Sport", Hours: 12.5},
		{Activity: "Shopping", Hours: 8.0},
	}
	buf := core.NewBuffer(60, 4)
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 4}
	RenderHoursByActivity(buf, rect, fixtureA, tcell.StyleDefault)
	linesA := renderedText(buf, rect)

	wantTotalA := HoursTotal(fixtureA) // drawn from the fixture, not hardcoded
	if wantTotalA != 20.5 {
		t.Fatalf("sanity: HoursTotal(fixtureA) = %v, want 20.5", wantTotalA)
	}
	foundTotal := false
	for _, l := range linesA {
		if strings.Contains(l, "Total:") && strings.Contains(l, "20.5h") {
			foundTotal = true
		}
	}
	if !foundTotal {
		t.Fatalf("rendered output %q does not contain the fixture-derived total 20.5h", linesA)
	}

	// Mutate exactly one activity's hours and confirm the rendered
	// breakdown changes correspondingly.
	fixtureB := []ActivityHours{
		{Activity: "Sport", Hours: 99.0}, // mutated
		{Activity: "Shopping", Hours: 8.0},
	}
	buf2 := core.NewBuffer(60, 4)
	RenderHoursByActivity(buf2, rect, fixtureB, tcell.StyleDefault)
	linesB := renderedText(buf2, rect)

	if linesA[0] == linesB[0] {
		t.Errorf("row 0 unchanged after mutating Sport's hours 12.5 -> 99.0: %q", linesB[0])
	}
	// Note: row 1 (Shopping)'s own hours figure is unchanged, but its bar
	// is drawn proportional to the shared max-hours scale (12.5 -> 99.0
	// here), so its bar length legitimately changes too -- this is
	// RenderHoursByActivity's relative-bar-scale design (shared with
	// RenderPersonality/RenderLeisureTaste's histogram scaling), unlike
	// DEMO-5's typology rows, which are absolute figures and are the ones
	// that carry the per-figure "every OTHER row byte-identical" SF-3
	// obligation (see typology_test.go).

	wantTotalB := HoursTotal(fixtureB)
	if wantTotalB != 107.0 {
		t.Fatalf("sanity: HoursTotal(fixtureB) = %v, want 107.0", wantTotalB)
	}
	foundTotalB := false
	for _, l := range linesB {
		if strings.Contains(l, "Total:") && strings.Contains(l, "107.0h") {
			foundTotalB = true
		}
	}
	if !foundTotalB {
		t.Fatalf("rendered output %q does not contain the mutated fixture's total 107.0h", linesB)
	}
}

func TestHoursByActivity_NilAndEmptySafety(t *testing.T) {
	RenderHoursByActivity(nil, core.Rect{W: 1, H: 1}, []ActivityHours{{Activity: "x", Hours: 1}}, tcell.StyleDefault)
	buf := core.NewBuffer(4, 4)
	RenderHoursByActivity(buf, core.Rect{X: 0, Y: 0, W: 4, H: 4}, nil, tcell.StyleDefault)
	if got := HoursTotal(nil); got != 0 {
		t.Errorf("HoursTotal(nil) = %v, want 0", got)
	}
}
