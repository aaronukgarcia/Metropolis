package dash_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestAuditDrillCoverageDeterministic is AC-12's determinism check: the
// audit's walk order is slice order (never map iteration), so repeated
// runs produce the same gap sequence. A map-range leak feeding the walk
// order would make this flaky under -count=N.
func TestAuditDrillCoverageDeterministic(t *testing.T) {
	l := dash.DefaultLayout("f1")
	first := dash.AuditDrillCoverage(l)
	for i := 0; i < 50; i++ {
		again := dash.AuditDrillCoverage(l)
		if len(first) != len(again) {
			t.Fatalf("run %d: gap count %d != %d", i, len(again), len(first))
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("run %d: gap %d changed order (%+v vs %+v)", i, j, first[j], again[j])
			}
		}
	}
}

// TestRenderDeterministic asserts rendering a layout twice produces
// byte-identical buffers (no map-order leak in the render path).
func TestRenderDeterministic(t *testing.T) {
	l := dash.DefaultLayout("f1")
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 30}

	a := core.NewBuffer(40, 30)
	dash.Render(a, rect, l, widgets.DefaultPalette, tcell.StyleDefault)
	b := core.NewBuffer(40, 30)
	dash.Render(b, rect, l, widgets.DefaultPalette, tcell.StyleDefault)

	if !bufsEqual(a, b) {
		t.Fatal("two renders of the same layout diverged")
	}
}
