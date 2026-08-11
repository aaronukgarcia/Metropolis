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
