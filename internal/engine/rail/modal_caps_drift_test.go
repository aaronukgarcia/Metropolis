package rail

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
)

// TestModalCapsAgreeWithFreight is the weakness-pattern-#2 drift guard for the
// GR#3 "no second driftable copy" invariant the stub states in rail.go. The
// rail stub keeps its own self-contained loader (loadModalCaps) for the modal
// caps — constructing a full *freight.FreightAPI just to read them would also
// load engine.market + engine.logistics — so it re-reads data/freight.json
// directly. Silent divergence from engine.freight is exactly the failure
// SEC-125 found: rail's loader read only maxTonnesPerMovement and missed sea's
// 3,000 t minTonnesPerMovement. This test imports the real source
// (engine.freight, the sanctioned test-file import exemption) and asserts the
// two agree on BOTH max and min for every mode — changing one requires
// changing the other.
func TestModalCapsAgreeWithFreight(t *testing.T) {
	r, err := NewRailAPI("rail-drift-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}
	f, err := freight.LoadDefault("rail-drift-test")
	if err != nil {
		t.Fatalf("freight.LoadDefault: %v", err)
	}

	for _, mode := range []freight.Mode{freight.ModeRoad, freight.ModeRail, freight.ModeSea} {
		cap, err := f.ModalCap(mode)
		if err != nil {
			t.Fatalf("freight.ModalCap(%s): %v", mode, err)
		}
		if got := r.modalCaps[mode]; got != cap.MaxTonnesPerMovement {
			t.Errorf("rail max drifted from freight for %s: rail=%d freight=%d — rail's loadModalCaps and freight's config both read data/freight.json; changing one requires changing the other (GR#3)", mode, got, cap.MaxTonnesPerMovement)
		}
		if got := r.modalMinCaps[mode]; got != cap.MinTonnesPerMovement {
			t.Errorf("rail min drifted from freight for %s: rail=%d freight=%d — rail's loadModalCaps and freight's config both read data/freight.json; changing one requires changing the other (GR#3)", mode, got, cap.MinTonnesPerMovement)
		}
	}
}
