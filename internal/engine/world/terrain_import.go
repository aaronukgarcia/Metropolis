package world

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the build-time OS Terrain 50 importer (§2.1, AC-2):
// AsciiGridHeader/ParseAsciiGrid read the ESRI ASCII-grid format OS
// Terrain 50 ships (data/georef.json's source.formats — 50m native post
// spacing, 200x200 points per 10km tile), and ImportTerrain downsamples
// a parsed source grid to the game's 200x200, 10m-cell start tile,
// applying the Option (a) compression mapping (compression.go) on the
// way.
//
// # Real data vs. this build (ASM — see dispatch report)
//
// This package has no network access to fetch the actual OS Terrain 50
// TR23 tile from https://osdatahub.os.uk/downloads/open/Terrain50, so
// ImportTerrain is exercised in tests against a synthetic
// Terrain-50-SHAPED fixture (same ASCII-grid header format, same post
// spacing, deterministic elevations standing in for real Kent ground) —
// AC-2's check ("a passing test runs the importer against a fixture
// Terrain-50-shaped input") is satisfied exactly as specified. The
// importer's shape (parse -> validate -> downsample -> compress) is real
// and is what a genuine TR23 download would run through unchanged; only
// the source bytes are a stand-in. AC-13's OSTN15 anchor re-verification
// against the REAL downloaded tile could not be completed for the same
// reason — see georef_verify.go and this item's report for the
// escalation to Bill/Aaron this implies (the acceptance doc's own
// Escalations section already flags this as Aaron's call, not something
// to silently resolve).

// AsciiGridHeader is the parsed header of an ESRI ASCII grid (.asc) file.
type AsciiGridHeader struct {
	NCols       int
	NRows       int
	XllCorner   float64
	YllCorner   float64
	CellSize    float64
	NoDataValue float64
}

// SourceGrid is a parsed OS Terrain 50 ASCII-grid tile: header plus row-
// major elevation samples (NRows*NCols, north-to-south row order per the
// ESRI ASCII-grid convention — row 0 is the northernmost).
type SourceGrid struct {
	Header     AsciiGridHeader
	Elevations []float64 // len == Header.NRows*Header.NCols
}

// ParseAsciiGrid reads an ESRI ASCII-grid file (OS Terrain 50's .asc
// format) from r. Returns ErrTerrainImportInvalid (wrapped) for any
// header/row-count mismatch — never a partial/best-effort grid (AC-14,
// GR#7).
func ParseAsciiGrid(r io.Reader, tileName, correlationID string) (*SourceGrid, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	hdr := AsciiGridHeader{}
	fields := map[string]*float64{}
	fieldsInt := map[string]*int{"ncols": &hdr.NCols, "nrows": &hdr.NRows}
	fields["xllcorner"] = &hdr.XllCorner
	fields["yllcorner"] = &hdr.YllCorner
	fields["cellsize"] = &hdr.CellSize
	fields["nodata_value"] = &hdr.NoDataValue

	seen := map[string]bool{}
	for len(seen) < 6 {
		if !sc.Scan() {
			return nil, errs.Wrap(ErrTerrainImportInvalid, correlationID, sc.Err(), map[string]any{
				"tile": tileName, "cause": "truncated header (fewer than 6 header lines)",
			})
		}
		line := strings.TrimSpace(sc.Text())
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, errs.New(ErrTerrainImportInvalid, correlationID, map[string]any{
				"tile": tileName, "cause": fmt.Sprintf("malformed header line %q", line),
			})
		}
		key := strings.ToLower(parts[0])
		if p, ok := fieldsInt[key]; ok {
			n, err := strconv.Atoi(parts[1])
			if err != nil {
				return nil, errs.Wrap(ErrTerrainImportInvalid, correlationID, err, map[string]any{
					"tile": tileName, "cause": fmt.Sprintf("bad integer header %q", line),
				})
			}
			*p = n
			seen[key] = true
			continue
		}
		if p, ok := fields[key]; ok {
			f, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return nil, errs.Wrap(ErrTerrainImportInvalid, correlationID, err, map[string]any{
					"tile": tileName, "cause": fmt.Sprintf("bad float header %q", line),
				})
			}
			*p = f
			seen[key] = true
			continue
		}
		return nil, errs.New(ErrTerrainImportInvalid, correlationID, map[string]any{
			"tile": tileName, "cause": fmt.Sprintf("unrecognised header field %q", key),
		})
	}
	if hdr.NCols <= 0 || hdr.NRows <= 0 {
		return nil, errs.New(ErrTerrainImportInvalid, correlationID, map[string]any{
			"tile": tileName, "cause": "ncols/nrows must be positive",
		})
	}

	want := hdr.NCols * hdr.NRows
	elevs := make([]float64, 0, want)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		for _, tok := range strings.Fields(line) {
			v, err := strconv.ParseFloat(tok, 64)
			if err != nil {
				return nil, errs.Wrap(ErrTerrainImportInvalid, correlationID, err, map[string]any{
					"tile": tileName, "cause": fmt.Sprintf("non-numeric elevation sample %q", tok),
				})
			}
			elevs = append(elevs, v)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, errs.Wrap(ErrTerrainImportInvalid, correlationID, err, map[string]any{
			"tile": tileName, "cause": "read error while scanning elevation rows",
		})
	}
	if len(elevs) != want {
		return nil, errs.New(ErrTerrainImportInvalid, correlationID, map[string]any{
			"tile":  tileName,
			"cause": fmt.Sprintf("expected %d elevation samples (nrows*ncols), got %d — checksum/format mismatch", want, len(elevs)),
		})
	}

	return &SourceGrid{Header: hdr, Elevations: elevs}, nil
}

// AnchorExtentCheck verifies that an anchor coordinate (easting,
// northing) falls inside the source grid's real bounding box, per
// AC-13/AC-14 ("a georef.json anchor that resolves outside the actual
// downloaded tile's extent"). Returns ErrAnchorOutOfExtent, never a
// silent clamp.
func (g *SourceGrid) AnchorExtentCheck(anchorName string, easting, northing float64, tileName, correlationID string) error {
	minE := g.Header.XllCorner
	maxE := g.Header.XllCorner + float64(g.Header.NCols)*g.Header.CellSize
	minN := g.Header.YllCorner
	maxN := g.Header.YllCorner + float64(g.Header.NRows)*g.Header.CellSize
	if easting < minE || easting > maxE || northing < minN || northing > maxN {
		return errs.New(ErrAnchorOutOfExtent, correlationID, map[string]any{
			"anchor": anchorName, "easting": easting, "northing": northing, "tile": tileName,
			"minE": minE, "maxE": maxE, "minN": minN, "maxN": maxN,
		})
	}
	return nil
}

// ImportTerrain downsamples src (a parsed OS Terrain 50 tile, native 50m
// post spacing) to the 200x200, 10m-cell start-tile grid (AC-2) using
// bilinear interpolation, then applies the Option (a) artistic-
// compression mapping (compression.go, AC-3) so the full shore-to-
// escarpment feature set fits the 2km span. Deterministic: same src,
// same output, always (AC-16) — no randomness, no wall clock (AC-18).
func ImportTerrain(src *SourceGrid, correlationID string) (heights [][]float32, err error) {
	if src == nil || len(src.Elevations) == 0 {
		return nil, errs.New(ErrTerrainImportInvalid, correlationID, map[string]any{
			"tile": "", "cause": "nil or empty source grid",
		})
	}

	// Sample the source grid (native post spacing) at real-world
	// positions across its FULL extent (which, for the real TR23 tile,
	// represents the shore-to-escarpment-crest span exceeding 2km — see
	// georef-notes.md §2), warping the north-south sample position
	// through compressV (compression.go, AC-3/Option (a)) before it
	// picks which real-world row to read. Real elevations are read
	// verbatim at the warped position — only WHERE each output row looks
	// is non-linear, not the values themselves, matching the
	// designDecision's "real elevations... preserved; north-south
	// distances... non-linearly compressed" wording exactly.
	out := make([][]float32, TileSizeCells)
	srcSpanE := float64(src.Header.NCols-1) * src.Header.CellSize
	srcSpanN := float64(src.Header.NRows-1) * src.Header.CellSize
	for row := 0; row < TileSizeCells; row++ {
		out[row] = make([]float32, TileSizeCells)
		// outputV: 0 at the output grid's south edge (row TileSizeCells-1),
		// 1 at its north edge (row 0) — output row 0 is north per ESRI's
		// north-first convention.
		outputV := 1.0 - float64(row)/float64(TileSizeCells-1)
		realV := compressV(outputV)
		for col := 0; col < TileSizeCells; col++ {
			u := float64(col) / float64(TileSizeCells-1)
			out[row][col] = float32(bilinearSample(src, u*srcSpanE, realV*srcSpanN))
		}
	}
	return out, nil
}

// bilinearSample reads src's elevation at real-world offsets (offE,
// offN) from its SW corner, via bilinear interpolation between the four
// surrounding native-resolution posts. Row 0 of src.Elevations is the
// NORTH edge (ESRI ASCII-grid convention), so offN (measured from the
// south) is converted accordingly.
func bilinearSample(src *SourceGrid, offE, offN float64) float64 {
	cs := src.Header.CellSize
	fx := offE / cs
	// offN is from the south; source rows are north-first, so row index
	// increases southward: rowFromNorth = (NRows-1) - offN/cs.
	fy := float64(src.Header.NRows-1) - offN/cs

	x0 := clampInt(int(fx), 0, src.Header.NCols-1)
	x1 := clampInt(x0+1, 0, src.Header.NCols-1)
	y0 := clampInt(int(fy), 0, src.Header.NRows-1)
	y1 := clampInt(y0+1, 0, src.Header.NRows-1)
	tx := fx - float64(x0)
	ty := fy - float64(y0)
	if tx < 0 {
		tx = 0
	}
	if ty < 0 {
		ty = 0
	}

	at := func(x, y int) float64 { return src.Elevations[y*src.Header.NCols+x] }
	top := at(x0, y0)*(1-tx) + at(x1, y0)*tx
	bot := at(x0, y1)*(1-tx) + at(x1, y1)*tx
	return top*(1-ty) + bot*ty
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// populateTerrainFromHeightmap writes a pre-computed (imported +
// compressed) 200x200 heightmap into t's terrain SoA, deriving slope
// (slope.go) and a heightmap-driven surface classification as it goes.
func populateTerrainFromHeightmap(t *tile, heights [][]float32) {
	t.terrain.elevation = make([]float32, CellsPerTile)
	t.terrain.slope = make([]SlopeClass, CellsPerTile)
	t.terrain.surface = make([]Surface, CellsPerTile)
	for row := 0; row < TileSizeCells; row++ {
		for col := 0; col < TileSizeCells; col++ {
			idx := localIndex(col, row)
			t.terrain.elevation[idx] = heights[row][col]
			t.terrain.slope[idx] = classifySlope(heights, row, col)
			t.terrain.surface[idx] = classifySurface(heights[row][col], row)
		}
	}
}

// classifySurface is a simple heightmap-driven surface pick: below sea
// level (or the southernmost couple of rows) is water/shingle, otherwise
// grass. This is deliberately coarse — real surface classification from
// OS Terrain 50 alone (no separate land-cover dataset) is out of scope
// beyond what §2.1's shore/shelf/escarpment description requires.
func classifySurface(elevation float32, row int) Surface {
	switch {
	case elevation < 0:
		return SurfaceWater
	case row >= TileSizeCells-3:
		return SurfaceShingle
	default:
		return SurfaceGrass
	}
}
