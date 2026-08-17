package roads

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the road footprint geometry: the deterministic mapping from a
// road's (start, end, class width) to the set of world cells it occupies.
// It is the coarse baseline-one approximation of road width — the segment's
// Bresenham centerline dilated by a square stamp of the class's widthCells —
// chosen for determinism and simplicity, not physical accuracy (a real
// lane-width model is engine.build/traffic's later refinement). The
// approximation is documented, pure (a function of its inputs only) and
// order-independent (the returned slice is sorted), which is what AC-5's
// widening check and AC-14's determinism gate actually need.

// cellKey is a canonical, comparable cell identity for dedup and sort. It
// carries tile + local row/col so a footprint cell maps exactly to a
// world.CellAt argument.
type cellKey struct {
	tileX, tileY, row, col int
}

// keyFor derives a cellKey from a CellRef.
func keyFor(c CellRef) cellKey {
	return cellKey{tileX: c.Tile.X, tileY: c.Tile.Y, row: c.Local.Row, col: c.Local.Col}
}

// refFromKey rebuilds a CellRef from a cellKey.
func refFromKey(k cellKey) CellRef {
	return CellRef{
		Tile:  world.TileCoord{X: k.tileX, Y: k.tileY},
		Local: world.CellLocal{Row: k.row, Col: k.col},
	}
}

// cellRefInDomain reports whether c names a real world cell: its tile inside
// the 30x30 expansion extent and its local row/col inside the 200x200 tile
// grid. The bounds are the world module's own — world.TileCoord.InExtent /
// world.CellLocal.InBounds — never a roads-local magic number (GR#15). A
// coordinate that fails this check would make computeFootprint's Bresenham
// walk unbounded (a 500,001-cell stamp at Row=500000) or wrap the globalXY
// multiply into a far-negative coordinate (SEC-222).
func cellRefInDomain(c CellRef) bool {
	return c.Tile.InExtent() && c.Local.InBounds()
}

// globalXY converts a cell to its global (easting, northing) cell index. The
// tile*TileSizeCells multiply and the local add use checked int64 arithmetic
// (num.SafeMul / num.SatAddChecked), so a hostile Tile.X/Y large enough to
// wrap int64 is reported via ok=false rather than silently becoming a
// far-negative coordinate that feeds garbage geometry to the Bresenham walk
// (GR#16). ok is true only when both coordinates convert without overflow.
func globalXY(c CellRef) (x, y int, ok bool) {
	tx, overflowX := num.SafeMul(int64(c.Tile.X), int64(world.TileSizeCells))
	ty, overflowY := num.SafeMul(int64(c.Tile.Y), int64(world.TileSizeCells))
	if overflowX || overflowY {
		return 0, 0, false
	}
	gx, satX := num.SatAddChecked(tx, int64(c.Local.Col))
	gy, satY := num.SatAddChecked(ty, int64(c.Local.Row))
	if satX || satY {
		return 0, 0, false
	}
	return int(gx), int(gy), true
}

// keyFromGlobal converts a global cell index back to a cellKey, using
// floored division so the mapping is correct for any sign (a stamp can dip
// a cell below the map origin at the expansion extent's edge).
func keyFromGlobal(x, y int) cellKey {
	tx := floorDiv(x, world.TileSizeCells)
	ty := floorDiv(y, world.TileSizeCells)
	col := floorMod(x, world.TileSizeCells)
	row := floorMod(y, world.TileSizeCells)
	return cellKey{tileX: tx, tileY: ty, row: row, col: col}
}

// floorDiv is integer division rounding toward -inf (not toward zero, which
// is what Go's / does), so tile/local decomposition is sign-correct.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// floorMod is the floored remainder, always in [0, b) for positive b.
func floorMod(a, b int) int { return a - floorDiv(a, b)*b }

// abs returns the absolute value of x (x is a small cell delta, far from
// MinInt, so the negation is safe).
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// computeFootprint returns the sorted, deduplicated set of world cells a
// road of the given width occupies between start and end: the Bresenham
// centerline dilated by a square stamp of width widthCells (half =
// (widthCells-1)/2 cells on each side). Pure and deterministic (AC-15:
// the result is built from a fixed Bresenham walk plus a sorted key list,
// never a map iteration).
func computeFootprint(start, end CellRef, widthCells int) []CellRef {
	// Reject before the Bresenham walk (GR#1). The public mutation entry
	// point ([RoadsAPI.AddNode]) already rejects an out-of-domain CellRef, so
	// this branch is unreachable through the API; it exists so a future
	// internal caller that forgets the check cannot hang the engine building
	// an unbounded footprint from a hostile coordinate (SEC-222).
	if !cellRefInDomain(start) || !cellRefInDomain(end) {
		return nil
	}
	x0, y0, ok0 := globalXY(start)
	x1, y1, ok1 := globalXY(end)
	if !ok0 || !ok1 {
		return nil
	}

	half := (widthCells - 1) / 2
	if half < 0 {
		half = 0
	}

	seen := make(map[cellKey]struct{})
	stamp := func(x, y int) {
		for dy := -half; dy <= half; dy++ {
			for dx := -half; dx <= half; dx++ {
				seen[keyFromGlobal(x+dx, y+dy)] = struct{}{}
			}
		}
	}

	// Bresenham's line algorithm (integer only, deterministic).
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	x, y := x0, y0
	for {
		stamp(x, y)
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x += sx
		}
		if e2 <= dx {
			err += dx
			y += sy
		}
	}

	keys := make([]cellKey, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.tileX != b.tileX {
			return a.tileX < b.tileX
		}
		if a.tileY != b.tileY {
			return a.tileY < b.tileY
		}
		if a.row != b.row {
			return a.row < b.row
		}
		return a.col < b.col
	})
	out := make([]CellRef, len(keys))
	for i, k := range keys {
		out[i] = refFromKey(k)
	}
	return out
}
