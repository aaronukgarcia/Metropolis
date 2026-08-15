package dash_test

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

const encDrillView = "f2.ledger"

func tableTileForTest(t *testing.T, id string) dash.Tile {
	t.Helper()
	drill := dash.DrillTarget{ViewName: encDrillView}
	tile, err := dash.NewTableTile(id, drill, dash.TableSpec{
		Columns: []widgets.Column{{Title: "c", Width: 4}},
		Rows: []dash.TableRow{
			{Cells: []string{"x"}, Drill: dash.DrillTarget{ViewName: encDrillView, EntityID: "line-7"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tile
}

// TestTileTableAccessorReturnsDeepCopy is SEC-063's PoC inverted: mutating
// a returned table spec's row DrillTarget through the exported accessor
// must NOT reach the stored tile, or a caller could reintroduce the
// drill-through dead end AC-4 makes structurally unconstructible.
func TestTileTableAccessorReturnsDeepCopy(t *testing.T) {
	l := dash.NewLayout("f1")
	if err := l.AddTile(tableTileForTest(t, "tbl")); err != nil {
		t.Fatal(err)
	}
	orig := dash.DrillTarget{ViewName: encDrillView, EntityID: "line-7"}

	// The exact SEC-063 mutation: reach inside a Tiles() snapshot and zero
	// a row's drill through the returned *TableSpec.
	got := l.Tiles()[0].Table()
	got.Rows[0].Drill = dash.DrillTarget{}

	if gaps := dash.AuditDrillCoverage(l); len(gaps) != 0 {
		t.Fatalf("AuditDrillCoverage after accessor mutation = %+v, want none (stored tile was corrupted)", gaps)
	}
	again, ok := l.FindTile("tbl")
	if !ok {
		t.Fatal("tile missing")
	}
	if got := again.Table().Rows[0].Drill; got != orig {
		t.Fatalf("stored row drill = %+v, want %+v (accessor returned an aliased spec)", got, orig)
	}
}

// TestTileTableFindTileReturnsDeepCopy is the FindTile arm of the same
// leak: FindTile returns a Tile value whose table pointer must not alias
// the layout's stored spec.
func TestTileTableFindTileReturnsDeepCopy(t *testing.T) {
	l := dash.NewLayout("f1")
	if err := l.AddTile(tableTileForTest(t, "tbl")); err != nil {
		t.Fatal(err)
	}
	orig := dash.DrillTarget{ViewName: encDrillView, EntityID: "line-7"}

	got, ok := l.FindTile("tbl")
	if !ok {
		t.Fatal("tile missing")
	}
	got.Table().Rows[0].Drill = dash.DrillTarget{}

	again, ok := l.FindTile("tbl")
	if !ok {
		t.Fatal("tile missing after mutation")
	}
	if got := again.Table().Rows[0].Drill; got != orig {
		t.Fatalf("stored row drill = %+v, want %+v (FindTile returned an aliased spec)", got, orig)
	}
}

// TestTileDiagramAccessorReturnsDeepCopy is the diagram arm of SEC-063: a
// returned *DiagramSpec must be a deep copy so a zeroed hit DrillTarget
// does not corrupt the stored tile.
func TestTileDiagramAccessorReturnsDeepCopy(t *testing.T) {
	drill := dash.DrillTarget{ViewName: encDrillView}
	tile, err := dash.NewDiagramTile("dia", drill, dash.DiagramSpec{
		Hits: []dash.DiagramHit{{SourceID: "E7", Drill: dash.DrillTarget{ViewName: encDrillView, EntityID: "edge-7"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	l := dash.NewLayout("f1")
	if err := l.AddTile(tile); err != nil {
		t.Fatal(err)
	}
	orig := dash.DrillTarget{ViewName: encDrillView, EntityID: "edge-7"}

	l.Tiles()[0].Diagram().Hits[0].Drill = dash.DrillTarget{}

	if gaps := dash.AuditDrillCoverage(l); len(gaps) != 0 {
		t.Fatalf("AuditDrillCoverage after diagram accessor mutation = %+v, want none", gaps)
	}
	again, ok := l.FindTile("dia")
	if !ok {
		t.Fatal("tile missing")
	}
	if got := again.Diagram().Hits[0].Drill; got != orig {
		t.Fatalf("stored hit drill = %+v, want %+v (accessor returned an aliased spec)", got, orig)
	}
}

// TestNewTableTileDoesNotAliasCallerSpec is SEC-063's constructor-side
// alias: the constructor's own caller still holds `spec`, and mutating it
// after construction must not reach the stored tile.
func TestNewTableTileDoesNotAliasCallerSpec(t *testing.T) {
	drill := dash.DrillTarget{ViewName: encDrillView}
	rowDrill := dash.DrillTarget{ViewName: encDrillView, EntityID: "line-7"}
	spec := dash.TableSpec{
		Columns: []widgets.Column{{Title: "c", Width: 4}},
		Rows: []dash.TableRow{
			{Cells: []string{"x"}, Drill: rowDrill},
		},
	}
	tile, err := dash.NewTableTile("tbl", drill, spec)
	if err != nil {
		t.Fatal(err)
	}

	spec.Rows[0].Drill = dash.DrillTarget{}
	spec.Rows[0].Cells[0] = "hacked"

	got := tile.Table().Rows[0]
	if got.Drill != rowDrill {
		t.Fatalf("stored row drill = %+v, want %+v (constructor aliased the caller's spec)", got.Drill, rowDrill)
	}
	if got.Cells[0] != "x" {
		t.Fatalf("stored row cell = %q, want %q (constructor aliased the caller's Cells)", got.Cells[0], "x")
	}
}

// TestNewDiagramTileDoesNotAliasCallerSpec is the diagram constructor arm.
func TestNewDiagramTileDoesNotAliasCallerSpec(t *testing.T) {
	drill := dash.DrillTarget{ViewName: encDrillView}
	hitDrill := dash.DrillTarget{ViewName: encDrillView, EntityID: "edge-7"}
	spec := dash.DiagramSpec{Hits: []dash.DiagramHit{{SourceID: "E7", Drill: hitDrill}}}
	tile, err := dash.NewDiagramTile("dia", drill, spec)
	if err != nil {
		t.Fatal(err)
	}

	spec.Hits[0].Drill = dash.DrillTarget{}

	if got := tile.Diagram().Hits[0].Drill; got != hitDrill {
		t.Fatalf("stored hit drill = %+v, want %+v (constructor aliased the caller's spec)", got, hitDrill)
	}
}

// TestScalarSpecAccessorsReturnDeepCopies covers the same class for the
// scalar specs' slice fields (Series/Values/Entries), which are also
// mutable handles into the stored tile.
func TestScalarSpecAccessorsReturnDeepCopies(t *testing.T) {
	drill := dash.DrillTarget{ViewName: encDrillView}

	bn, err := dash.NewBignumTile("bn", drill, dash.BignumSpec{Series: []float64{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	bn.Bignum().Series[0] = 999
	if bn.Bignum().Series[0] != 1 {
		t.Fatal("Bignum() returned an aliased Series")
	}

	sp, err := dash.NewSparkTile("sp", drill, dash.SparkSpec{Series: []float64{4, 5}})
	if err != nil {
		t.Fatal(err)
	}
	sp.Spark().Series[0] = 999
	if sp.Spark().Series[0] != 4 {
		t.Fatal("Spark() returned an aliased Series")
	}

	mm, err := dash.NewMinimapTile("mm", drill, dash.MinimapSpec{Values: []float64{6, 7}})
	if err != nil {
		t.Fatal(err)
	}
	mm.Minimap().Values[0] = 999
	if mm.Minimap().Values[0] != 6 {
		t.Fatal("Minimap() returned an aliased Values")
	}

	al, err := dash.NewAlertsTile("al", drill, dash.AlertsSpec{Entries: []dash.AlertEntry{{Text: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	al.Alerts().Entries[0].Text = "hacked"
	if al.Alerts().Entries[0].Text != "a" {
		t.Fatal("Alerts() returned an aliased Entries")
	}
}

// TestDashboardLayoutReturnsDefensiveCopy closes the Dashboard.Layout()
// arm of the same "shallow defensive copy" class (demonstrated by the
// Destructive probe alongside SEC-063/SEC-064): editing the returned
// layout must not corrupt the dashboard's own layout.
func TestDashboardLayoutReturnsDefensiveCopy(t *testing.T) {
	l := dash.NewLayout("f1")
	for _, id := range []string{"a", "b", "c"} {
		tile, err := dash.NewBignumTile(id, dash.DrillTarget{ViewName: "f1.viewport"}, dash.BignumSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}
	d := dash.NewDashboard(l, nil, nil)

	edit := d.Layout()
	if err := edit.RemoveTile("a"); err != nil {
		t.Fatalf("RemoveTile on the returned copy: %v", err)
	}

	var ids []string
	for _, tile := range d.Layout().Tiles() {
		ids = append(ids, tile.ID())
	}
	want := []string{"a", "b", "c"}
	if len(ids) != len(want) {
		t.Fatalf("dashboard layout after editing the Layout() copy = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("dashboard layout after editing the Layout() copy = %v, want %v", ids, want)
		}
	}
}

func dashboardTileIDs(t *testing.T, d *dash.Dashboard) []string {
	t.Helper()
	var ids []string
	for _, tile := range d.Layout().Tiles() {
		ids = append(ids, tile.ID())
	}
	return ids
}

func assertTileIDs(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("dashboard tiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dashboard tiles = %v, want %v", got, want)
		}
	}
}

// TestDashboardDoesNotAliasCallerLayout is SEC-092: NewDashboard and
// SetLayout must clone the caller's Layout, or the caller's `l` and the
// dashboard's `d.layout.tiles` share one backing array — mutating `l`
// after construction/replacement would silently corrupt the dashboard.
func TestDashboardDoesNotAliasCallerLayout(t *testing.T) {
	l := dash.NewLayout("f1")
	for _, id := range []string{"a", "b", "c"} {
		tile, err := dash.NewBignumTile(id, dash.DrillTarget{ViewName: "f1.viewport"}, dash.BignumSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}
	d := dash.NewDashboard(l, nil, nil)

	// Mutate the caller's layout AFTER construction.
	if err := l.RemoveTile("a"); err != nil {
		t.Fatalf("caller RemoveTile: %v", err)
	}
	assertTileIDs(t, dashboardTileIDs(t, d), []string{"a", "b", "c"})

	// SetLayout must clone too: build a replacement and mutate the caller's
	// value after the swap.
	repl := dash.NewLayout("f1")
	for _, id := range []string{"x", "y"} {
		tile, err := dash.NewBignumTile(id, dash.DrillTarget{ViewName: "f1.viewport"}, dash.BignumSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if err := repl.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}
	d.SetLayout(repl)
	if err := repl.RemoveTile("x"); err != nil {
		t.Fatalf("caller RemoveTile after SetLayout: %v", err)
	}
	assertTileIDs(t, dashboardTileIDs(t, d), []string{"x", "y"})
}
