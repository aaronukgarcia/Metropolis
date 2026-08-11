package world

// This file implements data/georef.json's designDecision (Option (a),
// decided by Aaron 2026-08-08, AC-3): the start tile is an artistically
// COMPRESSED representation of real ground, not a literal 2km crop. The
// real shore-to-escarpment-crest span at this location is ~4.4km
// (georef-notes.md §2) — more than double the 2km tile. Real elevations
// and feature identities are preserved (terrain_import.go samples real
// source values verbatim); only WHERE in the 200-row output grid each
// real-world position lands is non-linearly warped, so all four §2.1
// bands (escarpment start, M20 corridor, shelf, shore) are genuinely
// present in-tile instead of the escarpment simply being cropped off.

// compressBreakpoints defines the piecewise-linear compression curve as
// (outputV, realV) control points, both in [0,1]. outputV is the
// fraction of the way up the OUTPUT 200-row grid (0=south edge,
// 1=north edge); realV is the corresponding fraction of the way up the
// FULL real-world span the source grid covers (0=south/shore end,
// 1=north/escarpment-crest end).
//
// The curve deliberately gives the shore+shelf+M20 band (real 0-0.55,
// i.e. roughly the first ~2.4km of the ~4.4km span, per georef-notes.md
// §2's "shore ~N135000-135200 to escarpment crest ~N139600") the first
// 75% of output rows — near 1:1, low compression, since that is the
// premium buildable band the player spends most of their time on — and
// compresses the remaining real 0.55-1.0 (the escarpment's rise toward
// its crest, which the tile can only ever show the BEGINNING of; see
// georef.json's own notes) into the final 25% of output rows. This is a
// two-segment piecewise-linear warp (still non-linear overall, and
// trivially extensible to more segments if a future tuning pass wants a
// smoother curve) rather than the uniform 1:1 linear squeeze a naive
// "just downsample the whole source extent" implementation would
// produce.
var compressBreakpoints = [][2]float64{
	{0.0, 0.0},
	{0.75, 0.55},
	{1.0, 1.0},
}

// compressV maps an output-grid vertical fraction (0=south, 1=north) to
// the real-world vertical fraction of the source grid's full extent it
// should sample from, via compressBreakpoints. Monotonic and
// deterministic — a pure function of its input, no randomness, no wall
// clock (AC-16, AC-18).
func compressV(outputV float64) float64 {
	if outputV <= 0 {
		return compressBreakpoints[0][1]
	}
	last := len(compressBreakpoints) - 1
	if outputV >= 1 {
		return compressBreakpoints[last][1]
	}
	for i := 0; i < last; i++ {
		x0, y0 := compressBreakpoints[i][0], compressBreakpoints[i][1]
		x1, y1 := compressBreakpoints[i+1][0], compressBreakpoints[i+1][1]
		if outputV >= x0 && outputV <= x1 {
			if x1 == x0 {
				return y0
			}
			t := (outputV - x0) / (x1 - x0)
			return y0 + t*(y1-y0)
		}
	}
	return compressBreakpoints[last][1]
}
