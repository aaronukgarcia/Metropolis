package widgets

import (
	"encoding/csv"
	"io"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Column is one table column: a display title and a fixed cell width to
// render/clip within.
type Column struct {
	Title string
	Width int
}

// TableData is the row/cell source a Table renders and sorts — a small
// interface rather than a concrete row type so a screen can wrap
// whatever domain slice it already has (school records, budget lines,
// …) without copying it into a widget-owned shape.
type TableData interface {
	// NumRows returns the total row count.
	NumRows() int
	// Cell returns row's value in col as display text, for both
	// rendering and sort/filter comparison (comparisons here are always
	// string comparisons — a caller wanting numeric sort should format
	// numeric cells zero-padded/fixed-width, same discipline a real
	// spreadsheet CSV export needs anyway).
	Cell(row, col int) string
}

// SortState is a Table's current sort key: which column, and which
// direction.
type SortState struct {
	Column    int
	Ascending bool
}

// CycleSort advances to the next sort column (wrapping past the last
// column back to 0), resetting to ascending — UI-SPEC §4's "sortable
// (`s` cycles columns)." A caller wanting direction-toggle-on-repeat
// instead of always-ascending can special-case "same column pressed
// again" itself; CycleSort's contract is only the column cycle.
// numCols <= 0 returns the zero SortState (degenerate: nothing to sort
// by).
func CycleSort(s SortState, numCols int) SortState {
	if numCols <= 0 {
		return SortState{}
	}
	next := s.Column + 1
	if next >= numCols {
		next = 0
	}
	return SortState{Column: next, Ascending: true}
}

// SortRows returns a permutation of [0, data.NumRows()) sorted by
// state's column/direction, via stable string comparison of
// data.Cell(row, state.Column). Stability matters: repeated cycling
// through columns should feel like refining the previous order, not
// randomising ties.
func SortRows(data TableData, state SortState) []int {
	n := data.NumRows()
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a := data.Cell(idx[i], state.Column)
		b := data.Cell(idx[j], state.Column)
		if state.Ascending {
			return a < b
		}
		return a > b
	})
	return idx
}

// FilterSubstring returns the subset of row indices in rows (a
// caller-supplied index set — typically the output of SortRows, so
// filter composes with sort) whose row has query as a case-insensitive
// substring in any of the first numCols cells. An empty query matches
// every row (no filter applied, not "no rows match").
func FilterSubstring(data TableData, rows []int, numCols int, query string) []int {
	if query == "" {
		out := make([]int, len(rows))
		copy(out, rows)
		return out
	}
	q := strings.ToLower(query)
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		for c := 0; c < numCols; c++ {
			if strings.Contains(strings.ToLower(data.Cell(r, c)), q) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// FilterPredicate is the general form: keep rows for which pred(row) is
// true. FilterSubstring is implementable in terms of this, but is kept
// as its own function since substring-across-all-columns is the common
// case (UI-SPEC §4's inline filter query) and callers with a bespoke
// predicate (numeric range, enum match) reach for this one directly.
func FilterPredicate(rows []int, pred func(row int) bool) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		if pred(r) {
			out = append(out, r)
		}
	}
	return out
}

// Window is a scroll position: which row of a (sorted, filtered) index
// set is first visible, and how many rows are visible.
type Window struct {
	Offset, Height int
}

// VisibleRows returns the slice of rows within win, clamped to rows'
// bounds. A negative Offset clamps to 0; an Offset past the end or a
// non-positive Height returns nil (an empty visible window, not a
// panic — AC-11's "table with zero rows" and any equally degenerate
// scroll state both land here).
func VisibleRows(rows []int, win Window) []int {
	if win.Offset < 0 {
		win.Offset = 0
	}
	if win.Height <= 0 || win.Offset >= len(rows) {
		return nil
	}
	end := win.Offset + win.Height
	if end > len(rows) {
		end = len(rows)
	}
	return rows[win.Offset:end]
}

// DrawTable renders a header row (cols' titles) followed by one row per
// entry in visible (typically VisibleRows' output), each cell clipped
// to its Column.Width, into buf starting at rect.Y. Rendering stops at
// rect.H rows total (header included) or rect.W columns' worth of
// width, whichever comes first — a table wider or taller than its rect
// clips rather than overflows into neighbouring panes.
func DrawTable(buf *core.Buffer, rect core.Rect, data TableData, cols []Column, visible []int, headerStyle, rowStyle tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	y := rect.Y
	drawRow(buf, rect, y, cols, headerStyle, func(c int) string { return cols[c].Title })
	y++

	for _, r := range visible {
		if y >= rect.Y+rect.H {
			break
		}
		row := r
		drawRow(buf, rect, y, cols, rowStyle, func(c int) string { return data.Cell(row, c) })
		y++
	}
}

func drawRow(buf *core.Buffer, rect core.Rect, y int, cols []Column, style tcell.Style, text func(col int) string) {
	x := rect.X
	limit := rect.X + rect.W
	for c := range cols {
		if x >= limit {
			return
		}
		w := cols[c].Width
		colLimit := x + w
		if colLimit > limit {
			colLimit = limit
		}
		cx := x
		for _, r := range text(c) {
			if cx >= colLimit {
				break
			}
			buf.Set(cx, y, r, style)
			cx++
		}
		for cx < colLimit {
			buf.Set(cx, y, ' ', style)
			cx++
		}
		x = colLimit + 1 // one-cell gap between columns
	}
}

// ExportCSV writes cols' titles as a header row followed by one row per
// entry in rows, to w, via encoding/csv — UI-SPEC §4's "exportable
// (`x` -> CSV to the save folder)." The caller owns opening the
// destination file in the save folder; this function only knows how to
// serialise a TableData view to a Writer, keeping it testable against a
// bytes.Buffer without touching a filesystem.
func ExportCSV(w io.Writer, data TableData, cols []Column, rows []int) error {
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Title
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		record := make([]string, len(cols))
		for c := range cols {
			record[c] = data.Cell(r, c)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
