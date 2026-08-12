package world

import (
	"strings"
	"testing"
)

// TestCompressionMappingPreservesAllFourFeatures is AC-3's required
// test: the imported+compressed grid must contain all four §2.1 bands —
// rising ground near the north edge, a corridor/junction band north of
// centre, a low-slope shelf band, and a shoreline/sea band at the south
// edge — even though the real-world span they're drawn from (~4.4km)
// exceeds the 2km tile.
func TestCompressionMappingPreservesAllFourFeatures(t *testing.T) {
	src := a90x90Fixture()
	heights, err := ImportTerrain(src, "test-corr")
	if err != nil {
		t.Fatalf("ImportTerrain: %v", err)
	}

	// 1. Rising ground near the north edge (row 0..a few rows in) should
	// be measurably higher than the shelf band in the middle.
	northElev := avgRow(heights, 0, 5)
	midElev := avgRow(heights, 90, 95)
	if northElev <= midElev {
		t.Fatalf("expected north-edge elevation (%.1f) > shelf-band elevation (%.1f)", northElev, midElev)
	}

	// 2. A corridor/junction band north of centre: DeriveMotorwayCorridor
	// should find a row inside the upper third of the grid.
	corridorRow, junction, ok := DeriveMotorwayCorridor(heights)
	if !ok {
		t.Fatal("expected DeriveMotorwayCorridor to find a corridor row")
	}
	if corridorRow >= TileSizeCells/2 {
		t.Fatalf("expected corridor row (%d) to be north of centre (< %d)", corridorRow, TileSizeCells/2)
	}
	if junction.Row != corridorRow {
		t.Fatalf("expected junction row (%d) to equal corridor row (%d)", junction.Row, corridorRow)
	}

	// 3. A low-slope shelf band: the middle band should be mostly flat/gentle.
	flatCount := 0
	for row := 100; row < 150; row++ {
		for col := 0; col < TileSizeCells; col++ {
			sc := classifySlope(heights, row, col)
			if sc == SlopeFlat || sc == SlopeGentle {
				flatCount++
			}
		}
	}
	total := 50 * TileSizeCells
	if float64(flatCount)/float64(total) < 0.5 {
		t.Fatalf("expected the shelf band to be mostly flat/gentle, got %d/%d", flatCount, total)
	}

	// 4. Shoreline/sea band at the south edge: last few rows should be
	// below sea level.
	southElev := avgRow(heights, TileSizeCells-5, TileSizeCells)
	if southElev >= 0 {
		t.Fatalf("expected south-edge elevation < 0 (sea), got %.1f", southElev)
	}

	// 5. SEC-043 (found independently three times: the original junior,
	// Tester-1, Destructive-2): assertions 1-4 above ALL pass unchanged
	// even if compressV is gutted to `return outputV` (identity, no
	// compression) — the fixture's real elevation profile alone
	// satisfies "north > mid", "a corridor row exists", "mid is mostly
	// flat", "south is below sea level" regardless of whether any
	// compression happened. This assertion targets compressV's actual
	// non-linear EFFECT rather than downstream structure that survives
	// without it. Worked arithmetic: the fixture's shelf/M20 boundary
	// sits at real north-fraction 0.55, which is exactly
	// compressBreakpoints' (0.75, 0.55) control point. Under the GENUINE
	// curve, real 0.55 lands at output fraction 0.75 (~row 50), so rows
	// 22-28 (output fraction ~0.86-0.89) sample real north-fraction
	// ~0.72-0.79 — solidly inside the corridor band (~50-60m). Under an
	// IDENTITY mapping (outputV used as realV directly, i.e. what a
	// gutted compressV would produce), the SAME rows 22-28 sample real
	// north-fraction ~0.86-0.89 directly — solidly inside the escarpment
	// band (~120m+, since escarpment starts at real north-fraction
	// 0.85). The two mappings disagree by more than 60m at this specific
	// band precisely BECAUSE compressV is non-linear; an identity
	// compressV collapses that gap to zero, which is what this
	// assertion is built to catch. See
	// TestCompressionMappingPreservesAllFourFeatures_CorridorBand_ProvenFail
	// for the companion proof that an identity mapping DOES trip this.
	corridorBandElev := avgRow(heights, 22, 29)
	if corridorBandElev >= 100 {
		t.Fatalf("expected rows 22-28 to sample the compressed corridor band (~50-60m), got %.1f — this is escarpment-band elevation (~120m+), which is where an UNCOMPRESSED/identity compressV would put this real-world position; compressV may not be applying real non-linear compression (SEC-043)", corridorBandElev)
	}
	if corridorBandElev < 40 {
		t.Fatalf("expected rows 22-28 to sample the compressed corridor band (~50-60m), got %.1f — too low, looks like the shelf band", corridorBandElev)
	}
}

// TestCompressionMappingPreservesAllFourFeatures_CorridorBand_ProvenFail:
// PROOF (SEC-043) — reproduces ImportTerrain's exact sampling loop for
// rows 22-28 but skips compressV entirely, using outputV directly as
// the real-world sample fraction (exactly what a gutted
// `func compressV(outputV float64) float64 { return outputV }` would
// produce). Confirms assertion 5 above is genuinely discriminating: the
// SAME fixture, sampled without real compression, puts rows 22-28 in
// escarpment territory (>=100m), which assertion 5 correctly rejects —
// so a future regression that guts compressV back to identity fails
// THIS package's own headline test, not only the low-level
// TestCompressVIsNonLinear.
func TestCompressionMappingPreservesAllFourFeatures_CorridorBand_ProvenFail(t *testing.T) {
	src := a90x90Fixture()
	srcSpanE := float64(src.Header.NCols-1) * src.Header.CellSize
	srcSpanN := float64(src.Header.NRows-1) * src.Header.CellSize

	sum, n := 0.0, 0
	for row := 22; row < 29; row++ {
		outputV := 1.0 - float64(row)/float64(TileSizeCells-1)
		realV := outputV // identity mapping: what a gutted compressV returns
		for col := 0; col < TileSizeCells; col++ {
			u := float64(col) / float64(TileSizeCells-1)
			sum += bilinearSample(src, u*srcSpanE, realV*srcSpanN)
			n++
		}
	}
	avg := sum / float64(n)
	if avg < 100 {
		t.Fatalf("sanity check failed: an identity mapping should put rows 22-28 in escarpment territory (>=100m), got %.1f — the discriminating assertion's premise doesn't hold, investigate the fixture/arithmetic before trusting assertion 5", avg)
	}
}

// TestCompressionMappingPreservesAllFourFeatures_ProvenFail: PROOF —
// with a FLAT, featureless fixture (no real elevation variation), the
// same north-vs-shelf comparison must fail, confirming assertion 1
// above is discriminating real structure, not always trivially true.
func TestCompressionMappingPreservesAllFourFeatures_ProvenFail(t *testing.T) {
	flatFixture := func() *SourceGrid {
		src := fixtureAsciiGrid(90, 90, 620000, 132500, 50, func(row, col int) float64 { return 42 })
		sg, err := ParseAsciiGrid(strings.NewReader(src), "flat-fixture", "test-corr")
		if err != nil {
			t.Fatalf("fixture setup: %v", err)
		}
		return sg
	}()
	heights, err := ImportTerrain(flatFixture, "test-corr")
	if err != nil {
		t.Fatalf("ImportTerrain: %v", err)
	}
	northElev := avgRow(heights, 0, 5)
	midElev := avgRow(heights, 90, 95)
	if northElev > midElev {
		t.Fatalf("sanity check failed: a flat fixture should not show north > shelf, got north=%.1f shelf=%.1f", northElev, midElev)
	}
}

func avgRow(heights [][]float32, fromRow, toRow int) float64 {
	sum := 0.0
	n := 0
	for r := fromRow; r < toRow; r++ {
		for _, h := range heights[r] {
			sum += float64(h)
			n++
		}
	}
	return sum / float64(n)
}

// TestCompressVIsNonLinear directly proves the mapping is a genuine
// non-uniform compression, not a disguised 1:1 linear crop — a naive
// linear/identity mapping would put compressV(0.5) at 0.5; the real
// Option (a) curve instead gives the shore/shelf/M20 band (the first
// 75% of output rows) most of the real span's low end, so the midpoint
// of the OUTPUT grid corresponds to well UNDER half of the real span.
func TestCompressVIsNonLinear(t *testing.T) {
	got := compressV(0.5)
	if got >= 0.5 {
		t.Fatalf("expected compressV(0.5) < 0.5 (non-uniform compression favouring the shore/shelf band), got %.4f", got)
	}
}

// TestCompressVIsNonLinear_ProvenFail: PROOF — an identity mapping
// (compressV(v)=v, i.e. no compression at all) WOULD satisfy
// got>=0.5 at v=0.5, confirming the assertion above is discriminating
// real non-linear behaviour rather than something that always holds.
func TestCompressVIsNonLinear_ProvenFail(t *testing.T) {
	identity := func(v float64) float64 { return v }
	got := identity(0.5)
	if got < 0.5 {
		t.Fatalf("sanity check failed: identity(0.5) should equal 0.5, not be less than it")
	}
}

// TestCompressVControlPoints is BUG-065's regression test: pins the
// SPECIFIC piecewise-linear compression curve compressBreakpoints
// defines, not merely "some monotonic non-linear function whose
// downstream elevation-band sample happens to land in a plausible
// range" (SEC-043's own fix, AC-30, is exactly that weaker property —
// see this test's ProvenFail-style companions below for why it is not
// enough on its own).
//
// Exact expected values, derived from compressBreakpoints' REAL current
// control points (compression.go: {0,0}, {0.75,0.55}, {1,1} — self-
// checked below per GR#15, so this test fails loudly rather than
// silently degrading if those constants ever change):
//
//   - compressV(0.75): 0.75 is exactly the second breakpoint's outputV,
//     so the piecewise-linear interpolation returns its realV
//     UNCHANGED: 0.55 exactly (t=(0.75-0)/(0.75-0)=1.0 on the first
//     segment). BUG-065's own suggested tolerance: [0.53, 0.57].
//   - compressV(0.375): the midpoint of the first segment [0, 0.75].
//     t=(0.375-0)/(0.75-0)=0.5, so realV = 0 + 0.5*(0.55-0) = 0.275
//     exactly. Tight tolerance: [0.271, 0.279] (+-0.004) — chosen
//     tight enough that BOTH of BUG-065's own live-verified
//     counterexamples fail it (see the two companion tests below),
//     which is the concrete bar AC-32b sets rather than a subjective
//     "tight enough".
//
// Live-verified (disposable `git worktree add --detach HEAD`, real repo
// untouched, worktree removed after use): with the REAL compressV body
// replaced in-place by (1) the power-law v^1.5 substitution and (2) the
// alt-breakpoint (0.70,0.50) curve, THIS test fails both times —
// `compressV(0.75) = 0.6495 / 0.5833, want in [0.53,0.57]` — and passes
// again once the mutation is reverted. The two companion tests below
// reproduce both curves as disposable LOCAL functions (compressV itself
// is never mutated in the real tree) purely so the discrimination proof
// runs as part of the normal `go test` suite without needing a worktree
// every time.
func TestCompressVControlPoints(t *testing.T) {
	// GR#15 self-check: this test's expected values are only valid
	// while compressBreakpoints' middle control point is (0.75, 0.55).
	// If it ever changes, this test must fail loudly rather than keep
	// silently asserting stale numbers against a moved curve.
	if len(compressBreakpoints) != 3 ||
		compressBreakpoints[1][0] != 0.75 || compressBreakpoints[1][1] != 0.55 {
		t.Fatalf("test assumption broken: compressBreakpoints' middle control point changed from (0.75,0.55) to %v — recompute this test's expected control-point values from the new breakpoints before trusting it", compressBreakpoints)
	}

	if got := compressV(0.75); got < 0.53 || got > 0.57 {
		t.Fatalf("compressV(0.75) = %.4f, want in [0.53,0.57] (exact value under the real curve: 0.55)", got)
	}
	if got := compressV(0.375); got < 0.271 || got > 0.279 {
		t.Fatalf("compressV(0.375) = %.4f, want in [0.271,0.279] (exact value under the real curve: 0.275)", got)
	}
}

// TestCompressVControlPoints_RejectsPowerLawCounterexample is BUG-065's
// own live-verified counterexample #1: a power-law curve
// compressV(v)=v^1.5 — a genuinely different, wrong non-linear mapping,
// NOT the spec's piecewise-linear breakpoints and NOT identity — that
// Destructive-3 proved passes SEC-043's own corridorBandElev assertion
// AND TestCompressVIsNonLinear unchanged. Reproduces the curve here as a
// disposable local function (compressV itself is never mutated — the
// real repo stays untouched) and confirms TestCompressVControlPoints'
// own two assertions correctly reject it, closing the exact gap BUG-065
// named.
func TestCompressVControlPoints_RejectsPowerLawCounterexample(t *testing.T) {
	powerLawV15 := func(v float64) float64 {
		// v^1.5 = v * sqrt(v), computed without importing math (mirrors
		// the BA's own scratch-repro technique) via a few Newton
		// iterations — precision to several decimal places is more
		// than enough to clear or miss a +-0.004/+-0.02 band.
		if v <= 0 {
			return 0
		}
		x := v
		for i := 0; i < 30; i++ {
			x = 0.5 * (x + v/x)
		}
		return v * x
	}

	got75 := powerLawV15(0.75)
	got375 := powerLawV15(0.375)
	fail75 := got75 < 0.53 || got75 > 0.57
	fail375 := got375 < 0.271 || got375 > 0.279
	if !fail75 && !fail375 {
		t.Fatalf("sanity check failed: expected the power-law substitution (v^1.5) to fail at LEAST one of TestCompressVControlPoints' two bands — got compressV(0.75)=%.4f (band [0.53,0.57]), compressV(0.375)=%.4f (band [0.271,0.279]); if both are now inside band, TestCompressVControlPoints is no longer discriminating this counterexample", got75, got375)
	}
	t.Logf("power-law v^1.5 counterexample: compressV(0.75)=%.4f (want [0.53,0.57], fail=%v), compressV(0.375)=%.4f (want [0.271,0.279], fail=%v) — correctly rejected", got75, fail75, got375, fail375)
}

// TestCompressVControlPoints_RejectsAltBreakpointCounterexample is
// BUG-065's own live-verified counterexample #2: the same piecewise-
// linear SHAPE as the real curve, but with the wrong breakpoint —
// (0.70, 0.50) instead of the real (0.75, 0.55) — which Destructive-3
// also proved passes every existing test unchanged. Reproduces the
// alternative curve locally (real compressBreakpoints untouched) and
// confirms TestCompressVControlPoints' assertions reject it too.
func TestCompressVControlPoints_RejectsAltBreakpointCounterexample(t *testing.T) {
	altBreakpoints := [][2]float64{{0.0, 0.0}, {0.70, 0.50}, {1.0, 1.0}}
	altCompressV := func(outputV float64) float64 {
		last := len(altBreakpoints) - 1
		for i := 0; i < last; i++ {
			x0, y0 := altBreakpoints[i][0], altBreakpoints[i][1]
			x1, y1 := altBreakpoints[i+1][0], altBreakpoints[i+1][1]
			if outputV >= x0 && outputV <= x1 {
				t := (outputV - x0) / (x1 - x0)
				return y0 + t*(y1-y0)
			}
		}
		return altBreakpoints[last][1]
	}

	got75 := altCompressV(0.75)
	got375 := altCompressV(0.375)
	fail75 := got75 < 0.53 || got75 > 0.57
	fail375 := got375 < 0.271 || got375 > 0.279
	if !fail75 && !fail375 {
		t.Fatalf("sanity check failed: expected the alt-breakpoint (0.70,0.50) substitution to fail at LEAST one of TestCompressVControlPoints' two bands — got compressV(0.75)=%.4f (band [0.53,0.57]), compressV(0.375)=%.4f (band [0.271,0.279])", got75, got375)
	}
	t.Logf("alt-breakpoint (0.70,0.50) counterexample: compressV(0.75)=%.4f (want [0.53,0.57], fail=%v), compressV(0.375)=%.4f (want [0.271,0.279], fail=%v) — correctly rejected", got75, fail75, got375, fail375)
}

func TestCompressVMonotonicAndBounded(t *testing.T) {
	prev := -1.0
	for i := 0; i <= 100; i++ {
		v := float64(i) / 100
		out := compressV(v)
		if out < 0 || out > 1 {
			t.Fatalf("compressV(%.2f)=%.4f out of [0,1]", v, out)
		}
		if out < prev {
			t.Fatalf("compressV not monotonic at v=%.2f: %.4f < previous %.4f", v, out, prev)
		}
		prev = out
	}
}
