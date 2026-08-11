package world

import "testing"

func TestOffMapConnectionsPresent(t *testing.T) {
	heights, err := ImportTerrain(a90x90Fixture(), "corr")
	if err != nil {
		t.Fatalf("ImportTerrain: %v", err)
	}
	conns := OffMapConnections(heights)

	haveMotorway, havePower, haveSea := false, false, false
	sellindgeFound := false
	for _, c := range conns {
		switch c.Kind {
		case OffMapMotorway:
			haveMotorway = true
		case OffMapPower:
			havePower = true
			if c.PowerTrancheCapacityMW <= 0 {
				t.Fatal("expected the power connection to carry a positive tranche capacity")
			}
			if containsCode(c.Name, "Sellindge") {
				sellindgeFound = true
			}
		case OffMapSea:
			haveSea = true
			if !c.Dormant {
				t.Fatal("expected the sea/port connection to be dormant at start")
			}
		}
	}
	if !haveMotorway || !havePower || !haveSea {
		t.Fatalf("expected all three §2.2 off-map connection kinds, got motorway=%v power=%v sea=%v", haveMotorway, havePower, haveSea)
	}
	if !sellindgeFound {
		t.Fatal("expected the power connection to reference Sellindge by name")
	}
}

// TestOffMapConnectionsPresent_ProvenFail: PROOF — an empty heightmap
// (ImportTerrain never ran) must yield NO connections rather than a
// fabricated set, confirming OffMapConnections genuinely depends on
// real heightmap input rather than returning a hardcoded constant.
func TestOffMapConnectionsPresent_ProvenFail(t *testing.T) {
	conns := OffMapConnections(nil)
	if len(conns) != 0 {
		t.Fatalf("sanity check failed: expected no connections for a nil heightmap, got %d", len(conns))
	}
}
