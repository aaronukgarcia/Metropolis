package dash_test

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// recordingNavigator records the DrillTargets it is asked to navigate to.
type recordingNavigator struct {
	got []dash.DrillTarget
}

func (n *recordingNavigator) Navigate(target dash.DrillTarget) error {
	n.got = append(n.got, target)
	return nil
}

// TestDrillNavigatesToTarget is AC-6: firing Enter (Drill) on a selected
// tile resolves its DrillTarget and invokes navigation with exactly that
// target — this package does not reimplement navigation.
func TestDrillNavigatesToTarget(t *testing.T) {
	target := dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-42"}
	tile, err := dash.NewBignumTile("cash", target, dash.BignumSpec{})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}

	nav := &recordingNavigator{}
	d := dash.NewDashboard(l, nav, nil)

	if err := d.Drill("cash", ""); err != nil {
		t.Fatalf("Drill: %v", err)
	}
	if len(nav.got) != 1 {
		t.Fatalf("navigator received %d calls, want 1", len(nav.got))
	}
	if nav.got[0] != target {
		t.Fatalf("navigator received %+v, want %+v", nav.got[0], target)
	}
}

// TestDrillVanishedEntityBignum is AC-9's bignum representative: a scalar
// tile whose DrillTarget's subscription/entity no longer exists shows a
// "no longer available" registry error, not a crash or silent no-op.
func TestDrillVanishedEntityBignum(t *testing.T) {
	target := dash.DrillTarget{ViewName: "f2.ledger"}
	tile, err := dash.NewBignumTile("cash", target, dash.BignumSpec{})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}

	resolver := dash.NewMapResolver() // target NOT marked live
	nav := &recordingNavigator{}
	d := dash.NewDashboard(l, nav, resolver)

	err = d.Drill("cash", "")
	assertDrillUnavailable(t, err)
	if len(nav.got) != 0 {
		t.Fatalf("navigator was called %d times on a vanished entity, want 0", len(nav.got))
	}
}

// TestDrillVanishedEntityTableRow is AC-9's table-row representative.
func TestDrillVanishedEntityTableRow(t *testing.T) {
	drill := dash.DrillTarget{ViewName: "f2.ledger"}
	tile, err := dash.NewTableTile("tbl", drill, dash.TableSpec{
		Columns: []widgets.Column{{Title: "c", Width: 4}},
		Rows:    []dash.TableRow{{Cells: []string{"x"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-7"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}

	resolver := dash.NewMapResolver()
	nav := &recordingNavigator{}
	d := dash.NewDashboard(l, nav, resolver)

	if err := d.Drill("tbl", "row:0"); !assertDrillUnavailable(t, err) {
		return
	}
	if len(nav.got) != 0 {
		t.Fatal("navigator called on a vanished table-row entity")
	}
}

// TestDrillVanishedEntityDiagramElement is AC-9's diagram-element
// representative.
func TestDrillVanishedEntityDiagramElement(t *testing.T) {
	drill := dash.DrillTarget{ViewName: "f2.ledger"}
	tile, err := dash.NewDiagramTile("dia", drill, dash.DiagramSpec{
		Hits: []dash.DiagramHit{{SourceID: "E7", Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "edge-7"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}

	resolver := dash.NewMapResolver()
	nav := &recordingNavigator{}
	d := dash.NewDashboard(l, nav, resolver)

	if err := d.Drill("dia", "hit:0"); !assertDrillUnavailable(t, err) {
		return
	}
	if len(nav.got) != 0 {
		t.Fatal("navigator called on a vanished diagram element")
	}
}

// TestDrillLiveEntityNavigates is the positive counterpart to AC-9: a
// marked-live target navigates (not falsely reported unavailable).
func TestDrillLiveEntityNavigates(t *testing.T) {
	target := dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-1"}
	tile, err := dash.NewBignumTile("cash", target, dash.BignumSpec{})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}

	resolver := dash.NewMapResolver()
	resolver.Mark(target)
	nav := &recordingNavigator{}
	d := dash.NewDashboard(l, nav, resolver)

	if err := d.Drill("cash", ""); err != nil {
		t.Fatalf("Drill on a live entity: %v", err)
	}
	if len(nav.got) != 1 {
		t.Fatalf("navigator calls = %d, want 1", len(nav.got))
	}
}

func TestDrillUnknownTile(t *testing.T) {
	d := dash.NewDashboard(dash.NewLayout("f1"), &recordingNavigator{}, nil)
	if err := d.Drill("missing", ""); err == nil {
		t.Fatal("Drill(unknown tile) returned nil error")
	}
}

func assertDrillUnavailable(t *testing.T, err error) bool {
	t.Helper()
	if err == nil {
		t.Fatal("Drill on a vanished entity returned nil error, want MET-U800")
		return false
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not *errs.E: %v", err)
		return false
	}
	if e.Code != "MET-U800" {
		t.Fatalf("error code = %q, want MET-U800", e.Code)
		return false
	}
	return true
}
