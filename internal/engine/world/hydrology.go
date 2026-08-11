package world

import "sort"

// This file derives real hydrology (§2.1: "Real hydrology... derived
// from the terrain flow model") from an imported heightmap via a
// standard D8 flow-direction + flow-accumulation model (AC-8) — never
// hand-authored stream coordinates. DeriveHydrology's only input is the
// heightmap; the same heightmap always yields the same stream path
// (deterministic, AC-16), and a different heightmap yields a different
// path (hydrology_test.go proves both directions).

// CellLocal is a (row, col) position within one tile's 200x200 local
// grid — used by hydrology/off-map-anchor code that works purely in
// tile-local coordinates rather than global world cell IDs.
type CellLocal struct {
	Row, Col int
}

// InBounds reports whether both Row and Col fall individually inside the
// documented tile-local domain [0, TileSizeCells) — BUG-063: a check on
// only the COMPOSITE localIndex(col,row) result is not equivalent to
// this. localIndex is row*TileSizeCells+col; a Col that is a negative
// exact multiple of TileSizeCells (e.g. Col:-200 with TileSizeCells=200)
// combines with an in-range Row to land back inside [0, CellsPerTile),
// silently ALIASING a different, legitimate cell instead of being
// rejected — live-reproduced via ApplyOwnershipCommand overwriting cell
// (0,0) from a nominally out-of-range coordinate. Every caller that
// turns a caller-supplied CellLocal into an index (worldapi.go's CellAt
// and ApplyOwnershipCommand) MUST call this FIRST and reject before
// ever computing localIndex — never bounds-check the composite alone.
func (c CellLocal) InBounds() bool {
	return c.Col >= 0 && c.Col < TileSizeCells && c.Row >= 0 && c.Row < TileSizeCells
}

// d8Offsets are the eight D8 neighbour offsets (row, col) and their
// centre-to-centre distance in cell units, in a fixed, deterministic
// iteration order (N, NE, E, SE, S, SW, W, NW) — tie-breaks in
// flowDirection always prefer the lowest index in this order, which is
// what makes the whole model reproducible on flat ground.
var d8Offsets = []struct {
	dr, dc int
	dist   float64
}{
	{-1, 0, 1.0},
	{-1, 1, 1.4142135623730951},
	{0, 1, 1.0},
	{1, 1, 1.4142135623730951},
	{1, 0, 1.0},
	{1, -1, 1.4142135623730951},
	{0, -1, 1.0},
	{-1, -1, 1.4142135623730951},
}

// flowDirections computes, for every cell, the index into d8Offsets of
// its steepest downhill neighbour, or -1 if the cell is a local pit
// (every neighbour is >= its own elevation — a sink).
func flowDirections(heights [][]float32) [][]int {
	n := len(heights)
	dirs := make([][]int, n)
	for r := 0; r < n; r++ {
		dirs[r] = make([]int, len(heights[r]))
		for c := range dirs[r] {
			dirs[r][c] = steepestDescent(heights, r, c)
		}
	}
	return dirs
}

func steepestDescent(heights [][]float32, r, c int) int {
	nRows := len(heights)
	nCols := len(heights[0])
	best := -1
	bestGrade := 0.0
	h := float64(heights[r][c])
	for i, off := range d8Offsets {
		nr, nc := r+off.dr, c+off.dc
		if nr < 0 || nr >= nRows || nc < 0 || nc >= nCols {
			continue
		}
		drop := h - float64(heights[nr][nc])
		grade := drop / off.dist
		if grade > bestGrade {
			bestGrade = grade
			best = i
		}
	}
	return best
}

// flowAccumulation computes, per cell, how many upstream cells
// (including itself) drain through it — the standard D8 accumulation
// count. Cells are processed in strictly descending elevation order,
// which is sufficient for correctness regardless of the flow graph's
// shape (a cell only ever contributes to a strictly-lower-or-equal
// neighbour, so by the time a cell is processed all of its own upstream
// contributors at a higher or equal elevation have already been added
// to it — equal-elevation ties are broken by original row-major index,
// deterministic and stable).
func flowAccumulation(heights [][]float32, dirs [][]int) [][]int {
	n := len(heights)
	acc := make([][]int, n)
	type cellRef struct{ r, c int }
	order := make([]cellRef, 0, n*len(heights[0]))
	for r := 0; r < n; r++ {
		acc[r] = make([]int, len(heights[r]))
		for c := range acc[r] {
			acc[r][c] = 1
			order = append(order, cellRef{r, c})
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return heights[order[i].r][order[i].c] > heights[order[j].r][order[j].c]
	})
	for _, cr := range order {
		d := dirs[cr.r][cr.c]
		if d < 0 {
			continue
		}
		off := d8Offsets[d]
		nr, nc := cr.r+off.dr, cr.c+off.dc
		acc[nr][nc] += acc[cr.r][cr.c]
	}
	return acc
}

// DeriveHydrology runs the flow-accumulation model over heights and
// returns the main stream's path (source to outlet) as a sequence of
// local cells — the highest-accumulation channel in the tile,
// representing the Pent/Seabrook-class stream line §2.1 asks for. Its
// ONLY input is heights (AC-8): no hand-placed coordinates, no
// randomness, no wall clock.
func DeriveHydrology(heights [][]float32) []CellLocal {
	if len(heights) == 0 || len(heights[0]) == 0 {
		return nil
	}
	dirs := flowDirections(heights)
	acc := flowAccumulation(heights, dirs)

	// Outlet: the cell with the single highest accumulation overall —
	// the point where the largest volume of upstream flow exits.
	outR, outC, best := 0, 0, -1
	for r := range acc {
		for c := range acc[r] {
			if acc[r][c] > best {
				best = acc[r][c]
				outR, outC = r, c
			}
		}
	}

	// Walk upstream from the outlet, at each step following the
	// dominant tributary (the inflow neighbour with the highest
	// accumulation), until reaching a headwater (acc==1) or a cell with
	// no inflow neighbour found — guards against pathological loops
	// with a hard cap at n*n steps (D8 flow-direction graphs are
	// acyclic by construction — steepest descent only ever moves to a
	// strictly lower cell along an existing edge, ties excluded by
	// grade>0 — but the cap is cheap insurance against a future change
	// to the direction model breaking that invariant silently).
	n := len(heights)
	maxSteps := n * len(heights[0])
	path := make([]CellLocal, 0, n)
	r, c := outR, outC
	for step := 0; step < maxSteps; step++ {
		path = append(path, CellLocal{Row: r, Col: c})
		if acc[r][c] <= 1 {
			break
		}
		nr, nc, found := dominantInflow(dirs, acc, r, c)
		if !found {
			break
		}
		r, c = nr, nc
	}
	// Reverse so the path reads source -> outlet.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// dominantInflow finds the neighbour of (r, c) whose flow direction
// points INTO (r, c) with the highest accumulation — the main upstream
// tributary. Ties break toward the lowest d8Offsets index, deterministic.
func dominantInflow(dirs, acc [][]int, r, c int) (int, int, bool) {
	nRows := len(dirs)
	nCols := len(dirs[0])
	bestR, bestC, bestAcc := 0, 0, -1
	found := false
	for i, off := range d8Offsets {
		nr, nc := r+off.dr, c+off.dc
		if nr < 0 || nr >= nRows || nc < 0 || nc >= nCols {
			continue
		}
		// (nr,nc) flows INTO (r,c) iff its own flow direction is the
		// OPPOSITE of the offset i used to reach it from (r,c) (d8Offsets
		// is ordered N,NE,E,SE,S,SW,W,NW, so the opposite of index i is
		// (i+4)%8 — e.g. N's opposite is S). The coordinate check below
		// is a cheap sanity confirmation, always true given that
		// relationship, kept because it costs nothing and documents the
		// invariant explicitly rather than trusting the index arithmetic
		// silently.
		oppIdx := (i + 4) % 8
		if dirs[nr][nc] != oppIdx {
			continue
		}
		off2 := d8Offsets[oppIdx]
		if nr+off2.dr != r || nc+off2.dc != c {
			continue
		}
		if acc[nr][nc] > bestAcc {
			bestAcc = acc[nr][nc]
			bestR, bestC = nr, nc
			found = true
		}
	}
	return bestR, bestC, found
}
