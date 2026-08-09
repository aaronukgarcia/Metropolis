package stub

import (
	"reflect"
	"testing"
)

// AC-3: the fixture loads into a 64x64 grid.
func TestGenerateFolkestone64_Dimensions(t *testing.T) {
	w := GenerateFolkestone64()
	if w.Width != FixtureWidth || w.Height != FixtureHeight {
		t.Fatalf("dimensions = %dx%d, want %dx%d", w.Width, w.Height, FixtureWidth, FixtureHeight)
	}
	if len(w.Cells) != FixtureHeight {
		t.Fatalf("len(Cells) = %d, want %d", len(w.Cells), FixtureHeight)
	}
	for y, row := range w.Cells {
		if len(row) != FixtureWidth {
			t.Fatalf("row %d width = %d, want %d", y, len(row), FixtureWidth)
		}
	}
}

// AC-11/GR#21: pure function of a constant seed — two generations must be
// byte-identical.
func TestGenerateFolkestone64_Deterministic(t *testing.T) {
	a := GenerateFolkestone64()
	b := GenerateFolkestone64()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("GenerateFolkestone64() is not deterministic across two calls")
	}
}

// AC-3: terrain bands evoking shore/shelf/motorway/escarpment must all
// be present.
func TestGenerateFolkestone64_TerrainBandsPresent(t *testing.T) {
	w := GenerateFolkestone64()
	seen := map[TerrainKind]bool{}
	for _, row := range w.Cells {
		for _, c := range row {
			seen[c.Terrain] = true
		}
	}
	for _, want := range []TerrainKind{TerrainShore, TerrainShelf, TerrainMotorway, TerrainEscarpment} {
		if !seen[want] {
			t.Errorf("terrain band %q not present anywhere in the fixture", want)
		}
	}
}

// AC-3: a few named roads/buildings.
func TestGenerateFolkestone64_NamedFeatures(t *testing.T) {
	w := GenerateFolkestone64()

	roads := map[string]bool{}
	buildings := map[string]bool{}
	for _, row := range w.Cells {
		for _, c := range row {
			if c.Road != "" {
				roads[c.Road] = true
			}
			if c.Building != "" {
				buildings[c.Building] = true
			}
		}
	}

	if len(roads) == 0 {
		t.Error("no named roads found in the fixture")
	}
	if len(buildings) == 0 {
		t.Error("no named buildings found in the fixture")
	}
	for _, r := range namedRoads {
		if !roads[r.name] {
			t.Errorf("named road %q not found placed on the grid", r.name)
		}
	}
	for _, b := range namedBuildings {
		if !buildings[b.name] {
			t.Errorf("named building %q not found placed on the grid", b.name)
		}
	}
}

func TestWorld_Cell_OutOfBounds(t *testing.T) {
	w := GenerateFolkestone64()
	if _, ok := w.Cell(-1, 0); ok {
		t.Error("Cell(-1, 0) ok = true, want false")
	}
	if _, ok := w.Cell(0, FixtureHeight); ok {
		t.Error("Cell(0, FixtureHeight) ok = true, want false")
	}
	if _, ok := w.Cell(FixtureWidth, 0); ok {
		t.Error("Cell(FixtureWidth, 0) ok = true, want false")
	}
	c, ok := w.Cell(0, 0)
	if !ok || c.X != 0 || c.Y != 0 {
		t.Errorf("Cell(0, 0) = %#v, %v, want valid (0,0)", c, ok)
	}
}
