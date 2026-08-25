package world

import "testing"

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
