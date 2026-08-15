package dash_test

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestNewTileRejectsNilDrillTarget is AC-4's construction-time check: a
// tile cannot be constructed with a zero/empty DrillTarget.
func TestNewTileRejectsNilDrillTarget(t *testing.T) {
	if _, err := dash.NewBignumTile("t", dash.DrillTarget{}, dash.BignumSpec{}); err == nil {
		t.Fatal("NewBignumTile with a nil DrillTarget returned nil error, want rejection")
	}
	if _, err := dash.NewGaugeTile("t", dash.DrillTarget{}, dash.GaugeSpec{}); err == nil {
		t.Fatal("NewGaugeTile with a nil DrillTarget returned nil error, want rejection")
	}
}

// TestNewTableTileRequiresDrillTargetOnEveryRow is AC-4/AC-5's element
// check: a table row with a zero DrillTarget is rejected at construction.
func TestNewTableTileRequiresDrillTargetOnEveryRow(t *testing.T) {
	drill := dash.DrillTarget{ViewName: "f2.ledger"}
	spec := dash.TableSpec{
		Columns: []widgets.Column{{Title: "c", Width: 4}},
		Rows: []dash.TableRow{
			{Cells: []string{"ok"}, Drill: drill},
			{Cells: []string{"bad"}, Drill: dash.DrillTarget{}}, // row 1: zero drill
		},
	}
	if _, err := dash.NewTableTile("t", drill, spec); err == nil {
		t.Fatal("NewTableTile with a zero-drill row returned nil error, want rejection")
	}
}

// TestNewDiagramTileRequiresDrillTargetOnEveryHit is AC-5's diagram check.
func TestNewDiagramTileRequiresDrillTargetOnEveryHit(t *testing.T) {
	drill := dash.DrillTarget{ViewName: "f2.ledger"}
	spec := dash.DiagramSpec{
		Hits: []dash.DiagramHit{
			{SourceID: "E1", Drill: drill},
			{SourceID: "E2", Drill: dash.DrillTarget{}}, // zero drill
		},
	}
	if _, err := dash.NewDiagramTile("t", drill, spec); err == nil {
		t.Fatal("NewDiagramTile with a zero-drill hit returned nil error, want rejection")
	}
}

func TestNewTileRequireDrillTarget(t *testing.T) {
	// Every constructor path funnels through newTile's requireDrill, but
	// exercise each exported constructor once so a future refactor that
	// drops one path is caught.
	drill := dash.DrillTarget{ViewName: "f1.viewport"}
	constructors := []func() (dash.Tile, error){
		func() (dash.Tile, error) { return dash.NewBignumTile("b", drill, dash.BignumSpec{}) },
		func() (dash.Tile, error) { return dash.NewGaugeTile("g", drill, dash.GaugeSpec{}) },
		func() (dash.Tile, error) { return dash.NewSparkTile("s", drill, dash.SparkSpec{}) },
		func() (dash.Tile, error) { return dash.NewMinimapTile("m", drill, dash.MinimapSpec{}) },
		func() (dash.Tile, error) { return dash.NewAlertsTile("a", drill, dash.AlertsSpec{}) },
		func() (dash.Tile, error) {
			return dash.NewTableTile("t", drill, dash.TableSpec{Columns: []widgets.Column{{Title: "c", Width: 4}}})
		},
		func() (dash.Tile, error) { return dash.NewDiagramTile("d", drill, dash.DiagramSpec{}) },
	}
	for i, ctor := range constructors {
		tile, err := ctor()
		if err != nil {
			t.Fatalf("constructor %d: %v", i, err)
		}
		if tile.Drill().ViewName != "f1.viewport" {
			t.Fatalf("constructor %d: Drill() = %+v", i, tile.Drill())
		}
	}
}

func TestTileKindAndID(t *testing.T) {
	tile, err := dash.NewSparkTile("cash", dash.DrillTarget{ViewName: "f2.ledger"}, dash.SparkSpec{Series: []float64{1}})
	if err != nil {
		t.Fatalf("NewSparkTile: %v", err)
	}
	if tile.ID() != "cash" || tile.Kind() != dash.KindSpark {
		t.Fatalf("got id=%q kind=%q", tile.ID(), tile.Kind())
	}
}

func TestConstructionErrorsAreRegistrySourced(t *testing.T) {
	_, err := dash.NewBignumTile("t", dash.DrillTarget{}, dash.BignumSpec{})
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not *errs.E: %v", err)
	}
	if e.Code == "" || e.Code == "MET-F003" {
		t.Fatalf("construction error fell back to %q, want a registered MET-U code", e.Code)
	}
}
