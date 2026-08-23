package world

// synthesizeTerrain fills t.terrain for every tile OTHER than the real
// imported start tile (see grid.go's World.startCoord/startHeight and
// terrain_import.go's ImportTerrain). This is a deliberate, documented
// scope boundary (ASM — see dispatch report): this build has no network
// access to download the other ~899 real OS Terrain 50 tiles covering
// the ~60x60km Kent expansion extent, so their terrain is a
// deterministic value-noise placeholder — real elevations and feature
// identity, not just "real ground", are only genuinely present in the
// one licensed, importer-populated start tile. A real build would
// replace this function tile-by-tile with real ImportTerrain calls as
// each tile's actual OS Terrain 50 source data is downloaded; the
// per-tile storage/query/ownership model (grid.go, worldapi.go) does not
// care which path populated a tile's terrain, so that swap is
// non-breaking when it happens.
//
// Deterministic (AC-16) and wall-clock-free (AC-18): a pure function of
// the tile's own TileCoord and local cell position via hashCoord's
// integer mixing, never math/rand, never time.
func synthesizeTerrain(t *tile, c TileCoord) {
	t.terrain.elevation = make([]float32, CellsPerTile)
	t.terrain.slope = make([]SlopeClass, CellsPerTile)
	t.terrain.surface = make([]Surface, CellsPerTile)

	heights := make([][]float32, TileSizeCells)
	for row := 0; row < TileSizeCells; row++ {
		heights[row] = make([]float32, TileSizeCells)
		for col := 0; col < TileSizeCells; col++ {
			globalX := c.X*TileSizeCells + col
			globalY := c.Y*TileSizeCells + row
			heights[row][col] = syntheticElevation(globalX, globalY)
		}
	}

	onLand := classifyLandSea(c)
	for row := 0; row < TileSizeCells; row++ {
		for col := 0; col < TileSizeCells; col++ {
			idx := localIndex(col, row)
			h := heights[row][col]
			if !onLand {
				h = -2 // sea tiles sit below the water surface
				heights[row][col] = h
			}
			t.terrain.elevation[idx] = h
			slope := classifySlope(heights, row, col)
			t.terrain.slope[idx] = slope
			gx := c.X*TileSizeCells + col
			gy := c.Y*TileSizeCells + row
			t.terrain.surface[idx] = classifySyntheticSurface(h, row, col, gx, gy)
		}
	}
}

// syntheticSeaLevel is a placeholder offset (balance-number regime,
// BUG-329) subtracted from the unsigned value-noise sum so a portion of
// each land tile sits below 0 m AOD and reads as water (a coastline).
// The value is chosen so the START tile {15,15} — whose unsigned noise
// sum spans roughly [38.8, 49.4] — dips below 0 on its low cells. This
// is not Folkestone — ASM-214 still owns the real OS Terrain 50 import.
// Do not "fix" a later screenshot by retuning the glyphs; retune this
// offset to move the coastline, or replace the function.
const syntheticSeaLevel = 42.0

// syntheticElevation returns a smooth, deterministic pseudo-elevation
// (metres AOD) for a global cell position, built from a small sum of
// hash-seeded "waves" at different scales — cheap value noise without
// pulling in a dependency, sufficient for a build-time placeholder
// terrain that is not meant to represent real Kent ground (see this
// file's doc comment).
func syntheticElevation(gx, gy int) float32 {
	var total float64
	scales := []struct {
		wavelength float64
		amplitude  float64
		salt       uint64
	}{
		{4000, 60, 0xA1},
		{800, 15, 0xB2},
		{150, 4, 0xC3},
		{80, 12, 0xE1},
	}
	for _, s := range scales {
		cellX := int(float64(gx) / s.wavelength)
		cellY := int(float64(gy) / s.wavelength)
		h00 := noiseCorner(cellX, cellY, s.salt)
		h10 := noiseCorner(cellX+1, cellY, s.salt)
		h01 := noiseCorner(cellX, cellY+1, s.salt)
		h11 := noiseCorner(cellX+1, cellY+1, s.salt)
		fx := frac(float64(gx) / s.wavelength)
		fy := frac(float64(gy) / s.wavelength)
		top := h00*(1-fx) + h10*fx
		bot := h01*(1-fx) + h11*fx
		total += (top*(1-fy) + bot*fy) * s.amplitude
	}
	return float32(total - syntheticSeaLevel)
}

// classifySyntheticSurface picks a Surface for a synthesised land cell.
// Sea tiles are forced to SurfaceWater by the caller (h set < 0).
// Order is deliberate: water, then a southern shingle strip (the import
// path's only non-grass land branch, now reachable on synth), then hashed
// woodland patches, else grass. Rock is intentionally absent from the
// synthesised path: the placeholder value-noise gradient never reaches a
// steep/unbuildable slope, so a rock branch here would be dead. No
// terrain path currently emits rock — it is a §2.4 slope-band surface
// with no synth or import emitter yet. Deterministic: a pure
// function of (elevation, row, col, global coordinates) via hashCoord.
// Placeholder, not a designed landscape.
func classifySyntheticSurface(elevation float32, row, col, globalX, globalY int) Surface {
	if elevation < 0 {
		return SurfaceWater
	}
	if row >= TileSizeCells-3 {
		return SurfaceShingle
	}
	if hashCoord(globalX, globalY, 0xD4)%5 == 0 {
		return SurfaceWoodland
	}
	return SurfaceGrass
}

func noiseCorner(cx, cy int, salt uint64) float64 {
	h := hashCoord(cx, cy, salt)
	return float64(h%10000) / 10000.0 // [0,1)
}

func frac(v float64) float64 {
	return v - float64(int(v))
}
