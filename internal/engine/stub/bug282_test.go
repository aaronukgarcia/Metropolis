package stub

import (
	"encoding/json"
	"testing"
)

// BUG-282: viewport snapshot omits elevation for ~189 shore cells (those
// with elevation 0 due to foldSeed texture 0). The schema contract (line 47-50
// of viewport.go) requires every cell in a full patch to carry both terrain
// and elevation, but the omitempty tags on ViewportCell cause zero values to
// be dropped.
//
// This test marshals the full snapshot and counts cells missing the "elevation"
// key. Before the fix, this should report ~189 missing (shore cells with
// elevation 0). After removing the omitempty tags, it should report 0.
func TestViewportSnapshot_AllCellsHaveElevation(t *testing.T) {
	w := GenerateFolkestone64()
	patch := fullViewportSnapshot(w)

	// Marshal to JSON
	b, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("failed to marshal snapshot: %v", err)
	}

	// Unmarshal into a generic map to inspect the raw JSON structure
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to unmarshal snapshot: %v", err)
	}

	cellsIface, ok := raw["cells"]
	if !ok {
		t.Fatal("snapshot missing 'cells' key")
	}

	cells, ok := cellsIface.([]interface{})
	if !ok {
		t.Fatalf("cells is not an array: %T", cellsIface)
	}

	if len(cells) != FixtureWidth*FixtureHeight {
		t.Fatalf("cell count = %d, want %d (full patch must include all cells)",
			len(cells), FixtureWidth*FixtureHeight)
	}

	missingElevation := 0
	missingTerrain := 0
	for i, cellIface := range cells {
		cellMap, ok := cellIface.(map[string]interface{})
		if !ok {
			t.Fatalf("cells[%d] is not a map: %T", i, cellIface)
		}

		// Contract requires both terrain and elevation to be present
		if _, ok := cellMap["terrain"]; !ok {
			missingTerrain++
		}
		if _, ok := cellMap["elevation"]; !ok {
			missingElevation++
		}
	}

	if missingTerrain > 0 {
		t.Errorf("found %d cells missing 'terrain' key (contract violation)", missingTerrain)
	}
	if missingElevation > 0 {
		t.Errorf("found %d cells missing 'elevation' key (contract violation)", missingElevation)
	}
}

// TestViewportSnapshot_ShoreElevationZero verifies that shore cells with
// elevation 0 are actually present in the snapshot (not skipped entirely).
// This catches the case where omitempty causes a zero-valued cell to be
// silently dropped.
func TestViewportSnapshot_ShoreElevationZero(t *testing.T) {
	// Shore band is y < 12, and elevationFor(x, y) for shore is 0 + texture.
	// When texture is 0, we get elevation = 0.
	// The fixture's foldSeed is deterministic, so some shore cells will have
	// texture 0 (elevation 0).

	w := GenerateFolkestone64()
	patch := fullViewportSnapshot(w)

	// Marshal and unmarshal to check raw JSON
	b, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cellsIface := raw["cells"].([]interface{})

	// Count shore cells with elevation 0 in the fixture
	shoreZeroInFixture := 0
	for _, row := range w.Cells {
		for _, c := range row {
			if c.Terrain == TerrainShore && c.Elevation == 0 {
				shoreZeroInFixture++
			}
		}
	}

	if shoreZeroInFixture == 0 {
		t.Fatal("test setup: no shore cells with elevation 0 found; test cannot verify the fix")
	}

	// Count shore cells with elevation 0 in the JSON snapshot
	shoreZeroInJSON := 0
	for _, cellIface := range cellsIface {
		cellMap := cellIface.(map[string]interface{})

		terrain, ok := cellMap["terrain"]
		if !ok || terrain != "shore" {
			continue
		}

		elev, ok := cellMap["elevation"]
		if !ok {
			// elevation key missing — if terrain is shore, this is a bug
			shoreZeroInJSON++
		} else if elvInt, ok := elev.(float64); ok && elvInt == 0 {
			// elevation present and is 0 — expected
		}
	}

	if shoreZeroInJSON > 0 {
		t.Errorf("found %d shore cells missing elevation key in JSON (expected 0); "+
			"fixture has %d shore cells with elevation 0",
			shoreZeroInJSON, shoreZeroInFixture)
	}
}
