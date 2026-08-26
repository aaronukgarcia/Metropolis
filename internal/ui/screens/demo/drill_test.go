package demo

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
)

// TestDrillTargets_RegistersDocumentedFigures is DEMO-8's check: every
// SF-2-documented whole-view aggregate this screen displays (excluding
// DEMO-3's blocked workforce totals) is registered into the drill-through
// list as a canonical dash.DrillTarget (ViewName, EntityID) pair -- the
// pyramid total, one entry per non-retired typology, and both distinct
// commuting-leak directions. The bespoke screen-local widget metadata is
// not part of the canonical drill contract (GR#3 / BUG-239).
func TestDrillTargets_RegistersDocumentedFigures(t *testing.T) {
	typologies := []TypologyRow{
		{Typology: "Terrace", Demand: 100, Stock: 90},
		{Typology: "Bungalow", Demand: 10, Stock: 9, Retired: true},
	}
	commute := CommuteFigures{OutCommuters: 5, InCommuters: 3}

	targets := DrillTargets(typologies, commute)

	// Index targets by ViewName for verification. The canonical type
	// carries ViewName (where to drill) and optional EntityID, not
	// screen-specific widget metadata.
	byView := map[string]dash.DrillTarget{}
	for _, tgt := range targets {
		byView[tgt.ViewName] = tgt
	}

	if _, ok := byView["citizen.population"]; !ok {
		t.Errorf("missing drill target for the pyramid total (citizen.population)")
	}
	if _, ok := byView["household.typology.Terrace"]; !ok {
		t.Errorf("missing drill target for typology Terrace")
	}
	if _, ok := byView["household.typology.Bungalow"]; ok {
		t.Errorf("retired typology Bungalow should not get a drill target (nothing live to drill into)")
	}
	if _, ok := byView["extcommute.out"]; !ok {
		t.Errorf("missing drill target for out-commuting")
	}
	if _, ok := byView["extcommute.in"]; !ok {
		t.Errorf("missing drill target for in-commuting")
	}
	if byView["extcommute.out"] == byView["extcommute.in"] {
		t.Errorf("out/in commuting drill targets must be distinct")
	}
}

func TestDrillTargets_EmptyInputsStillRegistersPyramidAndCommute(t *testing.T) {
	targets := DrillTargets(nil, CommuteFigures{})
	if len(targets) != 3 { // pyramid total + commute out + commute in
		t.Fatalf("len(targets) = %d, want 3 for no typologies", len(targets))
	}
}
