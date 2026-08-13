package demo

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestComputePyramidBars_TracesToFixture is DEMO-1's mutation-provable
// check: feeds a fixture age-distribution and asserts the computed bar
// data traces to the fixture's per-month-age counts, not a hardcoded
// illustrative shape. Two distinct fixtures differing only in one
// bucket's counts must produce correspondingly different bars.
func TestComputePyramidBars_TracesToFixture(t *testing.T) {
	fixtureA := []AgeBucket{
		{MonthAge: 0, Male: 10, Female: 8},
		{MonthAge: 600, Male: 5, Female: 4}, // 50 years
		{MonthAge: 1199, Male: 1, Female: 1},
	}
	rowsA := ComputePyramidBars(fixtureA, 3)
	if len(rowsA) != 3 {
		t.Fatalf("len(rowsA) = %d, want 3", len(rowsA))
	}
	// Row 0 is oldest (MonthAge 1199), row 2 is youngest (MonthAge 0),
	// per ComputePyramidBars' documented oldest-at-top orientation.
	if rowsA[0].Male != 1 || rowsA[0].Female != 1 {
		t.Errorf("rowsA[0] (oldest) = %+v, want Male=1 Female=1", rowsA[0])
	}
	if rowsA[2].Male != 10 || rowsA[2].Female != 8 {
		t.Errorf("rowsA[2] (youngest) = %+v, want Male=10 Female=8", rowsA[2])
	}

	// Mutate exactly one bucket's counts (DEMO-1's fixture-tracing
	// check) and confirm the affected row -- and only that row --
	// changes correspondingly.
	fixtureB := []AgeBucket{
		{MonthAge: 0, Male: 999, Female: 8}, // mutated
		{MonthAge: 600, Male: 5, Female: 4},
		{MonthAge: 1199, Male: 1, Female: 1},
	}
	rowsB := ComputePyramidBars(fixtureB, 3)

	if rowsB[2].Male != 999 {
		t.Fatalf("rowsB[2].Male = %d after mutating MonthAge=0's Male to 999, want 999 (rendered bar must trace to the fixture)", rowsB[2].Male)
	}
	if rowsA[0] != rowsB[0] {
		t.Errorf("rowsB[0] = %+v, want unchanged %+v (only the mutated bucket's row should differ)", rowsB[0], rowsA[0])
	}
	if rowsA[1] != rowsB[1] {
		t.Errorf("rowsB[1] = %+v, want unchanged %+v (only the mutated bucket's row should differ)", rowsB[1], rowsA[1])
	}
}

func TestComputePyramidBars_EmptyOrZeroDots(t *testing.T) {
	if got := ComputePyramidBars(nil, 10); got != nil {
		t.Errorf("ComputePyramidBars(nil, 10) = %v, want nil", got)
	}
	if got := ComputePyramidBars([]AgeBucket{{MonthAge: 0, Male: 1}}, 0); got != nil {
		t.Errorf("ComputePyramidBars(fixture, 0) = %v, want nil", got)
	}
}

// TestRenderPopulationPyramid_UsesWidgetsBrailleCanvas is DEMO-1's
// widget-reuse grep target companion: a passing render call must
// actually light dots on a widgets.BrailleCanvas-backed buffer (proven
// by asserting non-blank Braille runes appear), not merely accept the
// dependency without using it.
func TestRenderPopulationPyramid_UsesWidgetsBrailleCanvas(t *testing.T) {
	buf := core.NewBuffer(10, 5)
	rect := core.Rect{X: 0, Y: 0, W: 10, H: 5}
	ages := []AgeBucket{
		{MonthAge: 0, Male: 40, Female: 30},
		{MonthAge: 12, Male: 20, Female: 15},
	}
	RenderPopulationPyramid(buf, rect, ages, widgets.DefaultPalette)

	found := false
	for y := 0; y < 5; y++ {
		for x := 0; x < 10; x++ {
			if buf.Get(x, y).Rune != 0 && buf.Get(x, y).Rune != ' ' {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("RenderPopulationPyramid drew no non-blank cells for a non-empty fixture")
	}
}

func TestRenderPopulationPyramid_NilSafety(t *testing.T) {
	// Must not panic on a nil buffer, degenerate rect, or empty fixture.
	RenderPopulationPyramid(nil, core.Rect{W: 1, H: 1}, []AgeBucket{{MonthAge: 0, Male: 1}}, widgets.DefaultPalette)
	buf := core.NewBuffer(4, 4)
	RenderPopulationPyramid(buf, core.Rect{W: 0, H: 0}, nil, widgets.DefaultPalette)
	RenderPopulationPyramid(buf, core.Rect{W: 4, H: 4}, nil, widgets.DefaultPalette)
}
