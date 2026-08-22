package world

import "testing"

// TestSynthesizeTerrain_EmitsMoreThanGrass is BUG-329: a land start tile
// must not be 40,000 cells of uniform grass. The synthesiser is a
// placeholder (ASM-214 still owns real OS Terrain 50) but it has to be
// capable of more than one Surface or every downstream system that
// varies by cover is unexercised.
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
	kinds := 0
	for s, n := range counts {
		if n > 0 {
			kinds++
			t.Logf("%s: %d", s, n)
		}
	}
	if kinds < 2 {
		t.Fatalf("synthesised start tile has %d surface kind(s), want at least 2", kinds)
	}
}
