package world

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
)

func hashHeights(h [][]float32) [32]byte {
	sh := sha256.New()
	buf := make([]byte, 4)
	for _, row := range h {
		for _, v := range row {
			binary.LittleEndian.PutUint32(buf, math.Float32bits(v))
			sh.Write(buf)
		}
	}
	var out [32]byte
	copy(out[:], sh.Sum(nil))
	return out
}

func newReaderFromString(s string) *strings.Reader {
	return strings.NewReader(s)
}

// TestImportDeterministic is AC-16's importer determinism test:
// importing the same source twice must produce byte-identical output.
func TestImportDeterministic(t *testing.T) {
	src := a90x90Fixture()
	h1, err := ImportTerrain(src, "corr-1")
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	h2, err := ImportTerrain(src, "corr-2")
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if hashHeights(h1) != hashHeights(h2) {
		t.Fatal("expected two imports of the same source to produce identical output, hashes differ")
	}
}

// TestImportDeterministic_ProvenFail: PROOF — importing two DIFFERENT
// sources must produce different hashes, confirming the hash comparison
// above is content-sensitive, not a constant that always matches.
func TestImportDeterministic_ProvenFail(t *testing.T) {
	src1 := a90x90Fixture()
	src2Str := fixtureAsciiGrid(90, 90, 620000, 132500, 50, func(row, col int) float64 { return float64(row + col) })
	src2, err := ParseAsciiGrid(newReaderFromString(src2Str), "different-fixture", "corr")
	if err != nil {
		t.Fatalf("fixture 2 setup: %v", err)
	}
	h1, err := ImportTerrain(src1, "corr-1")
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	h2, err := ImportTerrain(src2, "corr-2")
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if hashHeights(h1) == hashHeights(h2) {
		t.Fatal("sanity check failed: two genuinely different sources produced identical hashes")
	}
}

// TestSynthesizedTerrainDeterministicAcrossWorldInstances: the
// placeholder terrain synthesis (synth_terrain.go) is also required to
// be deterministic (AC-16 applies to every derivation this package
// performs, not just the real importer) — two independent World
// instances querying the same TileCoord must see identical terrain.
func TestSynthesizedTerrainDeterministicAcrossWorldInstances(t *testing.T) {
	api1 := NewWorldAPI(TileCoord{15, 13})
	api2 := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{8, 8}

	c1, err := api1.CellAt(tc, CellLocal{Row: 42, Col: 17}, "corr")
	if err != nil {
		t.Fatalf("CellAt api1: %v", err)
	}
	c2, err := api2.CellAt(tc, CellLocal{Row: 42, Col: 17}, "corr")
	if err != nil {
		t.Fatalf("CellAt api2: %v", err)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("expected identical synthesized terrain across independent World instances, got %+v vs %+v", c1, c2)
	}
}
