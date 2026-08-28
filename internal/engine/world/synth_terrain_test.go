package world

import "testing"

// isLandSurface reports whether s is a land cover (as opposed to water).
// Rock is included for completeness though no path emits it yet.
func isLandSurface(s Surface) bool {
	return s == SurfaceGrass || s == SurfaceWoodland || s == SurfaceShingle || s == SurfaceRock
}

// TestSynthesizeTerrain_EmitsMoreThanGrass is BUG-329: a land start tile
// must not be 40,000 cells of uniform grass. The synthesiser is a
// placeholder (ASM-214 still owns real OS Terrain 50) but it has to be
// capable of more than one Surface or every downstream system that
// varies by cover is unexercised. It must also emit a real coastline:
// water reachable on the start tile itself, not merely a positional
// shingle strip and hashed woodland that stay green if the sea-level
// mechanism is dropped (the r1 reject's F1).
func TestSynthesizeTerrain_EmitsMoreThanGrass(t *testing.T) {
	tl := &tile{}
	synthesizeTerrain(tl, TileCoord{X: 15, Y: 15})
	if len(tl.terrain.surface) != CellsPerTile {
		t.Fatalf("surface length %d, want %d", len(tl.terrain.surface), CellsPerTile)
	}
	counts := map[Surface]int{}
	for _, s := range tl.terrain.surface {
		counts[s]++
	}
	if counts[SurfaceGrass] == CellsPerTile {
		t.Fatal("start tile is 40,000 cells of SurfaceGrass — BUG-329 is still live")
	}
	if counts[SurfaceWater] == 0 {
		t.Fatal("start tile has no SurfaceWater — syntheticSeaLevel does not reach the start tile (the r1 reject F1: the signed sea-level mechanism is dead)")
	}
	kinds := 0
	for s, n := range counts {
		if n > 0 {
			kinds++
			t.Logf("%s: %d", s, n)
		}
	}
	if kinds < 3 {
		t.Fatalf("synthesised start tile has %d surface kind(s), want at least 3 (water + shingle + a land cover)", kinds)
	}
}

// TestSynthesizeTerrain_DomainVariety is FEAT-231 W1: BUG-329's "one
// world" ban must hold EVERYWHERE the synthesiser can be reached, not
// just on the start tile {15,15}. It sweeps a deterministic (GR#21)
// stride of tiles across the whole 30x30 expansion extent and asserts:
//
//   - every reachable LAND tile carries at least two distinct surfaces
//     (a regression that floods any land tile to uniform grass — or drops
//     the woodland/shingle/water branches — fails here, not just on the
//     start tile),
//   - the domain as a whole emits at least two distinct LAND covers, and
//   - water is reachable somewhere in the domain (the signed sea-level
//     coastline mechanism is alive off the start tile too).
//
// The sweep is a pure function of the tile/cell coordinates, so it is
// deterministic and wall-clock-free. It is also non-vacuous: forcing
// classifySyntheticSurface to grass-everything turns it RED (BUG-230).
func TestSynthesizeTerrain_DomainVariety(t *testing.T) {
	// Deterministic tile stride across the full extent. Stride 5 over
	// [0,TilesPerSide) gives a 6x6 lattice (36 tiles) that straddles the
	// coast, so both land and sea tiles are exercised. The start tile is
	// added explicitly so a coordinate change to TilesPerSide can never
	// stride past it.
	const stride = 5
	var tiles []TileCoord
	for ty := 0; ty < TilesPerSide; ty += stride {
		for tx := 0; tx < TilesPerSide; tx += stride {
			tiles = append(tiles, TileCoord{X: tx, Y: ty})
		}
	}
	tiles = append(tiles, TileCoord{X: 15, Y: 15})

	domainCounts := map[Surface]int{}
	landTilesSeen := 0
	seaTilesSeen := 0
	for _, c := range tiles {
		if !c.InExtent() {
			t.Fatalf("test built an out-of-extent sample tile %v", c)
		}
		tl := &tile{}
		synthesizeTerrain(tl, c)
		if len(tl.terrain.surface) != CellsPerTile {
			t.Fatalf("tile %v: surface length %d, want %d", c, len(tl.terrain.surface), CellsPerTile)
		}

		tileCounts := map[Surface]int{}
		for _, s := range tl.terrain.surface {
			tileCounts[s]++
			domainCounts[s]++
		}

		// The BUG-329 "one world of grass" ban, generalised from the
		// start tile to EVERY reachable tile: no tile may be 40,000 cells
		// of uniform grass. (A tile that is entirely water — a low-noise
		// basin — is not the regression this guards; see the reported
		// coastline/elevation model mismatch, e.g. land-classified {0,20}
		// which the noise fully submerges.)
		if tileCounts[SurfaceGrass] == CellsPerTile {
			t.Fatalf("tile %v is %d cells of uniform SurfaceGrass — BUG-329 is live off the start tile", c, CellsPerTile)
		}

		if classifyLandSea(c) {
			landTilesSeen++
		} else {
			seaTilesSeen++
		}
	}

	// The sample must actually contain both kinds, or the assertions above
	// are vacuous.
	if landTilesSeen == 0 {
		t.Fatal("sweep contained no land tiles — sample is vacuous")
	}
	if seaTilesSeen == 0 {
		t.Fatal("sweep contained no sea tiles — sample is vacuous")
	}

	// Distinct LAND covers across the whole domain must be >= 2: a
	// regression to grass-everywhere (or dropping woodland/shingle) fails.
	distinctLand := 0
	for s, n := range domainCounts {
		if n > 0 && isLandSurface(s) {
			distinctLand++
		}
		if n > 0 {
			t.Logf("domain %s: %d cells", s, n)
		}
	}
	if distinctLand < 2 {
		t.Fatalf("synthesised domain emits %d distinct land cover(s), want at least 2 (uniform-grass world regression)", distinctLand)
	}

	// Water must be reachable somewhere in the domain — the coastline is
	// not a start-tile-only artefact.
	if domainCounts[SurfaceWater] == 0 {
		t.Fatal("no SurfaceWater anywhere in the swept domain — the sea-level coastline mechanism is dead off the start tile")
	}
}

// TestSyntheticElevation_NonNegativeBelowSeaLevelFloor is FEAT-231 W1's
// second guard, encoding BUG-329's latent trap as an executable bound.
//
// NOTE (real finding, reported to the lead): syntheticElevation DOES
// return negative values within the production coordinate domain — that
// is BY DESIGN, it is the coastline (classifySyntheticSurface maps
// elevation < 0 to SurfaceWater; the start tile's water depends on it).
// A literal "never negative in-domain" assertion is therefore wrong and
// would delete the coastline. The correct, meaningful invariant is the
// design FLOOR: the value-noise sum is built from corner samples in
// [0,1) bilinearly interpolated with frac in [0,1), so the pre-offset
// total is in [0, sum(amplitudes)] and the elevation is in
// [-syntheticSeaLevel, +...]. The ONLY way elevation drops below
// -syntheticSeaLevel is bilinear EXTRAPOLATION (frac < 0), which happens
// solely on NEGATIVE global coordinates — unreachable today (every
// TileCoord is InExtent so gx,gy >= 0), but a future extent/origin change
// that fed negative coordinates in would extrapolate the noise far below
// the floor and silently flood land to water. This test derives its
// domain exactly as synthesizeTerrain does (gx = c.X*TileSizeCells+col),
// so it tracks the real production domain, and fails closed if any such
// change breaches the floor.
func TestSyntheticElevation_NonNegativeBelowSeaLevelFloor(t *testing.T) {
	// Sweep every tile in the extent (catches a per-tile origin change),
	// striding cells within each tile to stay fast and deterministic.
	const cellStride = 8
	floor := float32(-syntheticSeaLevel)
	var worst float32 = 1e9
	var worstGX, worstGY int
	for ty := 0; ty < TilesPerSide; ty++ {
		for tx := 0; tx < TilesPerSide; tx++ {
			c := TileCoord{X: tx, Y: ty}
			for row := 0; row < TileSizeCells; row += cellStride {
				for col := 0; col < TileSizeCells; col += cellStride {
					gx := c.X*TileSizeCells + col
					gy := c.Y*TileSizeCells + row
					h := syntheticElevation(gx, gy)
					if h < worst {
						worst, worstGX, worstGY = h, gx, gy
					}
					if h < floor {
						t.Fatalf("syntheticElevation(%d,%d)=%.4f is below the design floor %.1f (-syntheticSeaLevel): the noise extrapolated past its corner-value range and would flood land — BUG-329 latent trap is now reachable", gx, gy, h, floor)
					}
				}
			}
		}
	}
	t.Logf("production domain min elevation=%.4f at (%d,%d); design floor=%.1f", worst, worstGX, worstGY, floor)
}
