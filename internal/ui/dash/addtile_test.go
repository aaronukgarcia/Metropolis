package dash

import "testing"

// TestAddTileRequireDrillTarget is AC-4's boundary check: AddTile
// re-validates a tile's DrillTarget fail-closed, so even a Tile that
// slipped past the constructors (e.g. a hand-constructed value reaching
// across a package boundary, or a future bug) cannot be added to a
// Layout without a valid drill target. White-box, because the public
// constructors correctly refuse to produce a zero-drill Tile at all.
func TestAddTileRequireDrillTarget(t *testing.T) {
	l := NewLayout("f1")

	// A zero-drill Tile built directly (the lower-level path AC-4 says
	// must be prevented from bypassing the required DrillTarget).
	zero := Tile{id: "bad", kind: KindBigNum}
	if err := l.AddTile(zero); err == nil {
		t.Fatal("AddTile accepted a Tile with a zero DrillTarget")
	}
	if l.Len() != 0 {
		t.Fatalf("AddTile left a rejected tile in the layout: len=%d", l.Len())
	}

	// An invalid-view-name tile is rejected the same way (no grammar
	// bypass at the boundary).
	invalid := Tile{id: "alsoBad", kind: KindBigNum, drill: DrillTarget{ViewName: "NOT VALID"}}
	if err := l.AddTile(invalid); err == nil {
		t.Fatal("AddTile accepted a Tile with an invalid DrillTarget view name")
	}
}
