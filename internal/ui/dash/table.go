package dash

import (
	"io"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// SortedRows returns a permutation of the table's row indices sorted by
// state's column/direction, via ui.widgets' shared SortRows (AC-7: this
// package wires the dashboard table tile's data through ui.widgets'
// table contract, it does not re-implement sorting). The permutation is
// an index set, so each visible row's DrillTarget stays attached to its
// original row — a sort that rebuilt row structs without carrying the
// drill target forward would silently reintroduce the dead end
// AuditDrillCoverage exists to catch.
func (t *TableSpec) SortedRows(state widgets.SortState) []int {
	return widgets.SortRows(t, state)
}

// Filter returns the subset of rows (typically SortedRows' output) whose
// cells contain query as a case-insensitive substring, via ui.widgets'
// FilterSubstring. Like sorting, it operates on row indices, so
// DrillTargets are preserved by construction.
func (t *TableSpec) Filter(rows []int, query string) []int {
	return widgets.FilterSubstring(t, rows, len(t.Columns), query)
}

// Visible returns the actual TableRow values for the given row indices,
// preserving each row's DrillTarget. This is the shape a dashboard
// renders and the shape AC-7's "sorted/filtered rows retain their
// original DrillTargets" test asserts against: the caller gets real rows
// (with their drills), not renumbered stand-ins.
func (t *TableSpec) Visible(rows []int) []TableRow {
	out := make([]TableRow, 0, len(rows))
	for _, r := range rows {
		if r < 0 || r >= len(t.Rows) {
			continue
		}
		out = append(out, t.Rows[r])
	}
	return out
}

// ExportCSV writes the table's columns and the given rows to w as CSV,
// via ui.widgets' ExportCSV (UI-SPEC §4's "exportable (`x` -> CSV to the
// save folder)"). The caller owns opening the destination file in the
// save folder; this function only serialises to a Writer, keeping it
// testable against a bytes.Buffer.
func (t *TableSpec) ExportCSV(w io.Writer, rows []int) error {
	return widgets.ExportCSV(w, t, t.Columns, rows)
}
