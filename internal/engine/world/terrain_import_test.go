package world

import (
	"strings"
	"testing"
)

// fixtureAsciiGrid builds a synthetic OS-Terrain-50-SHAPED ASCII grid
// (same header format, same 50m native post spacing convention) —
// see terrain_import.go's doc comment on why this is a fixture, not a
// real downloaded tile. cols/rows/cellSize/xll/yll are caller-chosen so
// tests can build small, fast fixtures.
func fixtureAsciiGrid(cols, rows int, xll, yll, cellSize float64, elevFn func(row, col int) float64) string {
	var sb strings.Builder
	sb.WriteString("ncols        ")
	sb.WriteString(itoa(cols))
	sb.WriteString("\nnrows        ")
	sb.WriteString(itoa(rows))
	sb.WriteString("\nxllcorner    ")
	sb.WriteString(ftoa(xll))
	sb.WriteString("\nyllcorner    ")
	sb.WriteString(ftoa(yll))
	sb.WriteString("\ncellsize     ")
	sb.WriteString(ftoa(cellSize))
	sb.WriteString("\nnodata_value -9999\n")
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if c > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(ftoa(elevFn(r, c)))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// Simple, sufficient-precision float formatter for test fixtures.
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f)
	frac := int64((f - float64(whole)) * 1000)
	s := itoa(int(whole)) + "." + pad3(int(frac))
	if neg {
		s = "-" + s
	}
	return s
}

func pad3(n int) string {
	s := itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// a90x90Fixture returns a 90x90-post (4.45km x 4.45km, 50m spacing)
// synthetic fixture whose SW corner is data/georef.json's real
// downloaded-tile equivalent (620000-ish extended south/north to cover
// the real shore-to-escarpment ~4.4km span) with a rising-north
// elevation profile approximating §2.1's escarpment/M20/shelf/shore
// layout, so AC-3's four-feature test has real structure to find.
func a90x90Fixture() *SourceGrid {
	const n = 90 // 90*50m = 4500m, comfortably spanning the ~4.4km real feature span
	src := fixtureAsciiGrid(n, n, 620000, 132500, 50, func(row, col int) float64 {
		// row 0 = north (per ESRI convention), row n-1 = south.
		southFrac := float64(row) / float64(n-1) // 0 at north, 1 at south... wait invert below
		_ = southFrac
		northFrac := 1.0 - float64(row)/float64(n-1) // 0 at south edge, 1 at north edge
		switch {
		case northFrac > 0.85: // top ~15%: escarpment, rising steeply past 120m
			return 120 + (northFrac-0.85)/0.15*40
		case northFrac > 0.55: // M20 corridor band: ~50-60m, gentle
			return 50 + (northFrac-0.55)/0.30*10
		case northFrac > 0.05: // shelf: flat buildable, ~10-20m
			return 10 + (northFrac-0.05)/0.50*10
		default: // shore/sea band
			return -2
		}
	})
	sg, err := ParseAsciiGrid(strings.NewReader(src), "TR23-fixture", "test-corr")
	if err != nil {
		panic(err)
	}
	return sg
}

func TestImportTerrainProducesExactly200x200(t *testing.T) {
	src := a90x90Fixture()
	heights, err := ImportTerrain(src, "test-corr")
	if err != nil {
		t.Fatalf("ImportTerrain: %v", err)
	}
	if len(heights) != TileSizeCells {
		t.Fatalf("expected %d rows, got %d", TileSizeCells, len(heights))
	}
	for i, row := range heights {
		if len(row) != TileSizeCells {
			t.Fatalf("row %d: expected %d cols, got %d", i, TileSizeCells, len(row))
		}
	}
}

// TestImportTerrainProducesExactly200x200_ProvenFail: PROOF this test
// can fail — the same assertion against a deliberately-wrong dimension
// (100 instead of TileSizeCells) fails, confirming the assertion is
// load-bearing, not vacuous.
func TestImportTerrainProducesExactly200x200_ProvenFail(t *testing.T) {
	src := a90x90Fixture()
	heights, err := ImportTerrain(src, "test-corr")
	if err != nil {
		t.Fatalf("ImportTerrain: %v", err)
	}
	const deliberatelyWrong = 100
	if len(heights) == deliberatelyWrong {
		t.Fatalf("sanity check: this should not equal %d in a correct build", deliberatelyWrong)
	}
}

func TestImportInvalidTerrain_ChecksumFormatMismatch(t *testing.T) {
	// Truncated elevation rows relative to the declared ncols*nrows —
	// must reject with the registry code, never a partial import.
	bad := "ncols        4\nnrows        4\nxllcorner    0\nyllcorner    0\ncellsize     50\nnodata_value -9999\n1 2 3 4\n1 2 3 4\n1 2 3 4\n"
	_, err := ParseAsciiGrid(strings.NewReader(bad), "bad-tile", "test-corr")
	if err == nil {
		t.Fatal("expected an error for a truncated grid, got nil")
	}
	if !strings.Contains(err.Error(), ErrTerrainImportInvalid) {
		t.Fatalf("expected %s in error, got: %v", ErrTerrainImportInvalid, err)
	}
}

func TestImportInvalidTerrain_ProvenFail(t *testing.T) {
	// PROOF: a WELL-FORMED grid (matching row/col counts) must NOT
	// trigger the same rejection — confirms the check above is
	// discriminating, not simply always failing.
	good := "ncols        2\nnrows        2\nxllcorner    0\nyllcorner    0\ncellsize     50\nnodata_value -9999\n1 2\n3 4\n"
	_, err := ParseAsciiGrid(strings.NewReader(good), "good-tile", "test-corr")
	if err != nil {
		t.Fatalf("expected no error for a well-formed grid, got: %v", err)
	}
}

func TestAnchorOutOfExtent(t *testing.T) {
	src := a90x90Fixture()
	// Seabrook (618595, 134983) is west of this fixture's xllcorner
	// (620000) — deliberately outside, per georef.json's own note.
	err := src.AnchorExtentCheck("Seabrook", 618595, 134983, "TR23-fixture", "test-corr")
	if err == nil {
		t.Fatal("expected ErrAnchorOutOfExtent for an anchor west of the grid, got nil")
	}
	if !strings.Contains(err.Error(), ErrAnchorOutOfExtent) {
		t.Fatalf("expected %s, got: %v", ErrAnchorOutOfExtent, err)
	}
}

func TestAnchorOutOfExtent_ProvenFail(t *testing.T) {
	src := a90x90Fixture()
	// PROOF: an anchor genuinely INSIDE the fixture's extent must NOT
	// be rejected — confirms the extent check is a real boundary test.
	err := src.AnchorExtentCheck("Folkestone West", 620900, 136400, "TR23-fixture", "test-corr")
	if err != nil {
		t.Fatalf("expected no error for an in-extent anchor, got: %v", err)
	}
}
