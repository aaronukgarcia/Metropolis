package dash

import (
	"strconv"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestAuditDrillCoverage_ReachesTableRowsAndDiagramHits is AC-5's
// exhaustive-coverage check: a synthetic Layout covering every known tile
// type, including a table with >=10 rows and an embedded diagram tile with
// >=5 hit-test elements. It removes (white-box, post-construction) the
// DrillTarget from exactly one table row (row 7 of 10) and one diagram
// hit, and asserts AuditDrillCoverage reports EXACTLY those two gaps —
// proving the walk reaches inside aggregate tiles rather than only
// checking top-level tile identity.
func TestAuditDrillCoverage_ReachesTableRowsAndDiagramHits(t *testing.T) {
	ledger := DrillTarget{ViewName: "f2.ledger"}

	// A table tile with 10 rows, each with a distinct DrillTarget.
	rows := make([]TableRow, 10)
	for i := range rows {
		rows[i] = TableRow{
			Cells: []string{string(rune('a' + i)), "100"},
			Drill: DrillTarget{ViewName: "f2.ledger", EntityID: protocol.EntityID("line-" + strconv.Itoa(i))},
		}
	}
	tableTile, err := NewTableTile("tbl", ledger, TableSpec{
		Columns: []widgets.Column{{Title: "c", Width: 4}},
		Rows:    rows,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A diagram tile with 5 hit-test elements.
	hits := make([]DiagramHit, 5)
	for i := range hits {
		hits[i] = DiagramHit{
			SourceID: "E" + strconv.Itoa(i),
			Drill:    DrillTarget{ViewName: "f2.ledger", EntityID: protocol.EntityID("edge-" + strconv.Itoa(i))},
		}
	}
	diagramTile, err := NewDiagramTile("dia", ledger, DiagramSpec{Hits: hits})
	if err != nil {
		t.Fatal(err)
	}

	// One scalar tile of each remaining kind so the walk's coverage is
	// proven across the full closed set.
	bn, err := NewBignumTile("bn", ledger, BignumSpec{})
	if err != nil {
		t.Fatal(err)
	}
	gg, err := NewGaugeTile("gg", ledger, GaugeSpec{})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := NewSparkTile("sp", ledger, SparkSpec{})
	if err != nil {
		t.Fatal(err)
	}
	mm, err := NewMinimapTile("mm", ledger, MinimapSpec{})
	if err != nil {
		t.Fatal(err)
	}
	al, err := NewAlertsTile("al", ledger, AlertsSpec{})
	if err != nil {
		t.Fatal(err)
	}

	l := NewLayout("f1")
	for _, tile := range []Tile{bn, gg, sp, mm, al, tableTile, diagramTile} {
		if err := l.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}

	// Remove the DrillTarget from one specific element buried inside the
	// table (row 7 of 10) and one specific diagram hit-test element —
	// white-box, since the public constructors correctly refuse to build
	// a zero-drill element.
	if err := removeTableRowDrill(&l, "tbl", 6); err != nil {
		t.Fatal(err)
	}
	if err := removeDiagramHitDrill(&l, "dia", 2); err != nil {
		t.Fatal(err)
	}

	gaps := AuditDrillCoverage(l)
	if len(gaps) != 2 {
		t.Fatalf("AuditDrillCoverage reported %d gaps, want exactly 2: %+v", len(gaps), gaps)
	}
	got := map[string]Gap{}
	for _, g := range gaps {
		got[g.TileID+"\x00"+g.ElementID] = g
	}
	if g, ok := got["tbl\x00row:6"]; !ok {
		t.Fatalf("missing expected gap for table row 6; gaps = %+v", gaps)
	} else if g.Kind != KindTable {
		t.Fatalf("table gap kind = %q, want table", g.Kind)
	}
	if g, ok := got["dia\x00hit:2"]; !ok {
		t.Fatalf("missing expected gap for diagram hit 2; gaps = %+v", gaps)
	} else if g.Kind != KindDiagram {
		t.Fatalf("diagram gap kind = %q, want diagram", g.Kind)
	}
}

// TestAuditDrillCoverage_CleanLayoutReportsNoGaps confirms a fully-valid
// layout has no gaps (the audit is not a false-positive generator).
func TestAuditDrillCoverage_CleanLayoutReportsNoGaps(t *testing.T) {
	l := DefaultLayout("f1")
	if gaps := AuditDrillCoverage(l); len(gaps) != 0 {
		t.Fatalf("clean default layout reported gaps: %+v", gaps)
	}
}

// removeTableRowDrill zeroes one table row's DrillTarget in place (the
// shared *TableSpec), reaching inside the aggregate tile the way a
// hand-edited profile or a future bug would.
func removeTableRowDrill(l *Layout, tileID string, row int) error {
	for i := range l.tiles {
		if l.tiles[i].id == tileID {
			l.tiles[i].table.Rows[row].Drill = DrillTarget{}
			return nil
		}
	}
	return nil
}

// removeDiagramHitDrill zeroes one diagram hit's DrillTarget in place.
func removeDiagramHitDrill(l *Layout, tileID string, hit int) error {
	for i := range l.tiles {
		if l.tiles[i].id == tileID {
			l.tiles[i].diagram.Hits[hit].Drill = DrillTarget{}
			return nil
		}
	}
	return nil
}
