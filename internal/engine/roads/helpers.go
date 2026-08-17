package roads

import "fmt"

// cellRefString renders a CellRef for error context (GR#1: the offending
// cell is named, not silently dropped).
func cellRefString(c CellRef) string {
	return fmt.Sprintf("tile(%d,%d) cell(%d,%d)", c.Tile.X, c.Tile.Y, c.Local.Row, c.Local.Col)
}

// isSummerMonth is the simulation-calendar predicate for the "summer"
// roadworks window (§51/AC-17): true when monthIndex's calendar month is
// June, July or August (calendar months 5–7 of the 12-month cycle). It is a
// pure month-index function — never a wall-clock date/time check.
func isSummerMonth(monthIndex int64) bool {
	m := monthIndex % 12
	if m < 0 {
		m += 12
	}
	return m >= 5 && m <= 7
}
