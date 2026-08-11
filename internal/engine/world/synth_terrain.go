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
			t.terrain.slope[idx] = classifySlope(heights, row, col)
			if h < 0 {
				t.terrain.surface[idx] = SurfaceWater
			} else {
				t.terrain.surface[idx] = SurfaceGrass
			}
		}
	}
}

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
	return float32(total)
}

func noiseCorner(cx, cy int, salt uint64) float64 {
	h := hashCoord(cx, cy, salt)
	return float64(h%10000) / 10000.0 // [0,1)
}

func frac(v float64) float64 {
	return v - float64(int(v))
}
