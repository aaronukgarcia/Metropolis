package widgets

import (
	"bytes"
	"encoding/csv"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

type fakeTable struct {
	rows [][]string
}

func (f fakeTable) NumRows() int { return len(f.rows) }
func (f fakeTable) Cell(row, col int) string {
	return f.rows[row][col]
}

func sampleTable() fakeTable {
	return fakeTable{rows: [][]string{
		{"Elm St", "120", "residential"},
		{"Ash Ave", "45", "commercial"},
		{"Birch Rd", "300", "residential"},
		{"Cedar Ct", "10", "industrial"},
	}}
}

func TestTable_CycleSortAcrossThreeColumns(t *testing.T) {
	numCols := 3
	s := SortState{}
	var seen []int
	for i := 0; i < 3; i++ {
		s = CycleSort(s, numCols)
		seen = append(seen, s.Column)
		if !s.Ascending {
			t.Fatalf("cycle %d not ascending", i)
		}
	}
	want := []int{1, 2, 0}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("cycle sequence = %v, want %v", seen, want)
		}
	}
}

func TestTable_SortRowsByColumn(t *testing.T) {
	data := sampleTable()
	idx := SortRows(data, SortState{Column: 1, Ascending: true})
	// Column 1 (volume) as strings sorts lexicographically: "10" < "120"
	// < "300" < "45" (this is a documented consequence of string-only
	// comparison, not a bug — see TableData.Cell's doc comment).
	want := []int{3, 0, 2, 1}
	if !intsEqual(idx, want) {
		t.Fatalf("SortRows asc col1 = %v, want %v", idx, want)
	}

	idxDesc := SortRows(data, SortState{Column: 1, Ascending: false})
	wantDesc := []int{1, 2, 0, 3}
	if !intsEqual(idxDesc, wantDesc) {
		t.Fatalf("SortRows desc col1 = %v, want %v", idxDesc, wantDesc)
	}
}

func TestTable_FilterSubstring(t *testing.T) {
	data := sampleTable()
	all := []int{0, 1, 2, 3}
	got := FilterSubstring(data, all, 3, "residential")
	want := []int{0, 2}
	if !intsEqual(got, want) {
		t.Fatalf("FilterSubstring = %v, want %v", got, want)
	}

	// Case-insensitive.
	got2 := FilterSubstring(data, all, 3, "RESIDENTIAL")
	if !intsEqual(got2, want) {
		t.Fatalf("FilterSubstring case-insensitive = %v, want %v", got2, want)
	}

	// Empty query matches everything.
	got3 := FilterSubstring(data, all, 3, "")
	if !intsEqual(got3, all) {
		t.Fatalf("FilterSubstring empty query = %v, want %v", got3, all)
	}
}

func TestTable_WindowScroll(t *testing.T) {
	rows := []int{0, 1, 2, 3, 4}
	got := VisibleRows(rows, Window{Offset: 1, Height: 2})
	if !intsEqual(got, []int{1, 2}) {
		t.Fatalf("VisibleRows = %v, want [1 2]", got)
	}
	if got := VisibleRows(rows, Window{Offset: 10, Height: 2}); got != nil {
		t.Fatalf("out-of-range offset should return nil, got %v", got)
	}
	if got := VisibleRows(rows, Window{Offset: 0, Height: 0}); got != nil {
		t.Fatalf("zero height should return nil, got %v", got)
	}
}

func TestTable_ZeroRowsDoesNotPanic(t *testing.T) {
	empty := fakeTable{}
	idx := SortRows(empty, SortState{Column: 0, Ascending: true})
	if len(idx) != 0 {
		t.Fatalf("SortRows on empty table = %v, want empty", idx)
	}
	buf := core.NewBuffer(20, 3)
	cols := []Column{{Title: "Name", Width: 10}, {Title: "N", Width: 5}}
	DrawTable(buf, core.Rect{X: 0, Y: 0, W: 20, H: 3}, empty, cols, nil, tcell.StyleDefault, tcell.StyleDefault)
}

func TestTable_ExportCSVMatchesFilteredSortedView(t *testing.T) {
	data := sampleTable()
	cols := []Column{{Title: "Name", Width: 10}, {Title: "Vol", Width: 5}, {Title: "Zone", Width: 12}}

	sorted := SortRows(data, SortState{Column: 2, Ascending: true})
	filtered := FilterSubstring(data, sorted, 3, "residential")

	var buf bytes.Buffer
	if err := ExportCSV(&buf, data, cols, filtered); err != nil {
		t.Fatalf("ExportCSV error: %v", err)
	}

	r := csv.NewReader(&buf)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("exported CSV did not parse: %v", err)
	}
	if len(records) != len(filtered)+1 {
		t.Fatalf("record count = %d, want %d (header + %d rows)", len(records), len(filtered)+1, len(filtered))
	}
	if len(records[0]) != 3 {
		t.Fatalf("header column count = %d, want 3", len(records[0]))
	}
	wantHeader := []string{"Name", "Vol", "Zone"}
	for i, h := range wantHeader {
		if records[0][i] != h {
			t.Fatalf("header[%d] = %q, want %q", i, records[0][i], h)
		}
	}
	for i, rowIdx := range filtered {
		for c := 0; c < 3; c++ {
			want := data.Cell(rowIdx, c)
			if records[i+1][c] != want {
				t.Fatalf("record[%d][%d] = %q, want %q", i+1, c, records[i+1][c], want)
			}
		}
	}
}

func TestTable_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(10, 5)
	DrawTable(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, sampleTable(), nil, nil, tcell.StyleDefault, tcell.StyleDefault)
	DrawTable(nil, core.Rect{X: 0, Y: 0, W: 10, H: 5}, sampleTable(), nil, nil, tcell.StyleDefault, tcell.StyleDefault)
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
