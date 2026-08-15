package dash_test

import (
	"bytes"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// sortedTableTile builds a table tile with 4 rows, each carrying a
// distinct DrillTarget, plus a sortable first column.
func sortedTableTile(t *testing.T) dash.Tile {
	t.Helper()
	drill := dash.DrillTarget{ViewName: "f2.ledger"}
	spec := dash.TableSpec{
		Columns: []widgets.Column{{Title: "Name", Width: 8}, {Title: "Amount", Width: 8}},
		Rows: []dash.TableRow{
			{Cells: []string{"delta", "40"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-delta"}},
			{Cells: []string{"alpha", "10"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-alpha"}},
			{Cells: []string{"charlie", "30"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-charlie"}},
			{Cells: []string{"bravo", "20"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-bravo"}},
		},
	}
	tile, err := dash.NewTableTile("tbl", drill, spec)
	if err != nil {
		t.Fatal(err)
	}
	return tile
}

// TestTableSortPreservesDrillTargets is AC-7's load-bearing check: a
// sorted table's visible rows retain their ORIGINAL DrillTargets, not
// renumbered/lost ones. It sorts by the Name column and asserts the
// visible rows' EntityIDs are exactly the original set (reordered, but
// none lost and none renumbered).
func TestTableSortPreservesDrillTargets(t *testing.T) {
	tile := sortedTableTile(t)
	spec := tile.Table()

	order := spec.SortedRows(widgets.SortState{Column: 0, Ascending: true})
	if len(order) != 4 {
		t.Fatalf("SortedRows returned %d indices, want 4", len(order))
	}

	// The sort must produce the rows in alpha, bravo, charlie, delta
	// order by Name — proving the sort actually ran.
	wantOrder := []string{"line-alpha", "line-bravo", "line-charlie", "line-delta"}
	got := make([]string, 0, 4)
	for _, idx := range order {
		got = append(got, spec.Visible([]int{idx})[0].Drill.EntityID)
	}
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			t.Fatalf("sorted order = %v, want %v (a sort that dropped/reordered drills would also break this)", got, wantOrder)
		}
	}
}

// TestTableFilterPreservesDrillTargets is AC-7's filter arm: filtered
// rows keep their own DrillTargets.
func TestTableFilterPreservesDrillTargets(t *testing.T) {
	tile := sortedTableTile(t)
	spec := tile.Table()

	order := spec.SortedRows(widgets.SortState{Column: 0, Ascending: true})
	filtered := spec.Filter(order, "a") // matches alpha, bravo, charlie, delta (all contain 'a')
	if len(filtered) != 4 {
		t.Fatalf("Filter('a') = %d rows, want 4", len(filtered))
	}
	// The filtered set must still map every row back to a distinct
	// original DrillTarget — no row is rebuilt without its drill.
	seen := map[string]bool{}
	for _, row := range spec.Visible(filtered) {
		seen[row.Drill.EntityID] = true
	}
	for _, want := range []string{"line-alpha", "line-bravo", "line-charlie", "line-delta"} {
		if !seen[want] {
			t.Fatalf("filtered rows lost DrillTarget %q (got %v)", want, seen)
		}
	}
}

// TestTableExportCSV confirms the dashboard table tile exports through
// ui.widgets' CSV contract (AC-7's export arm).
func TestTableExportCSV(t *testing.T) {
	tile := sortedTableTile(t)
	spec := tile.Table()

	var buf bytes.Buffer
	if err := spec.ExportCSV(&buf, []int{0, 1}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Name", "delta", "alpha"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("CSV output %q missing %q", out, want)
		}
	}
}
