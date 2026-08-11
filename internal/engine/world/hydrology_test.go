package world

import (
	"reflect"
	"testing"
)

func slopedHeights(n int, highCol int) [][]float32 {
	h := make([][]float32, n)
	for r := 0; r < n; r++ {
		h[r] = make([]float32, n)
		for c := 0; c < n; c++ {
			// Elevation falls away from highCol in both directions and
			// falls toward the south (higher row index) — funnels flow
			// toward the south edge near highCol.
			distFromRidge := float64(c - highCol)
			if distFromRidge < 0 {
				distFromRidge = -distFromRidge
			}
			h[r][c] = float32(50 - distFromRidge*2 - float64(r)*0.5)
		}
	}
	return h
}

func TestHydrologyDerivedFromHeightmapOnly(t *testing.T) {
	heights := slopedHeights(30, 15)
	path := DeriveHydrology(heights)
	if len(path) < 2 {
		t.Fatalf("expected a multi-cell stream path, got %d cells", len(path))
	}
	// The path should generally move toward higher row indices (south,
	// downhill in this fixture) as it goes from source to outlet.
	if path[0].Row >= path[len(path)-1].Row {
		t.Fatalf("expected path to flow from a higher row (%d) to a lower... i.e. source row < outlet row, got source=%d outlet=%d",
			path[0].Row, path[0].Row, path[len(path)-1].Row)
	}
}

func TestHydrologyChangesWithInputHeightmap(t *testing.T) {
	pathA := DeriveHydrology(slopedHeights(30, 10))
	pathB := DeriveHydrology(slopedHeights(30, 20))
	if reflect.DeepEqual(pathA, pathB) {
		t.Fatal("expected a different ridge position to produce a different derived stream path, got identical paths")
	}
}

// TestHydrologyChangesWithInputHeightmap_ProvenFail: PROOF — the SAME
// heightmap given twice must produce an IDENTICAL path (determinism,
// AC-16), confirming the inequality check above is comparing real
// content, not e.g. two independently-random paths that would always
// differ regardless of input.
func TestHydrologyChangesWithInputHeightmap_ProvenFail(t *testing.T) {
	h := slopedHeights(30, 10)
	pathA := DeriveHydrology(h)
	pathB := DeriveHydrology(h)
	if !reflect.DeepEqual(pathA, pathB) {
		t.Fatalf("sanity check failed: identical input must produce identical output, got different paths")
	}
}
