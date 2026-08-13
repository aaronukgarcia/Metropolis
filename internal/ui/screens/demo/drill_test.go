package demo

import "testing"

// TestDrillTargets_RegistersDocumentedFigures is DEMO-8's check: every
// SF-2-documented whole-view aggregate this screen displays (excluding
// DEMO-3's blocked workforce totals) is registered into the drill-through
// pair list -- the pyramid total, one entry per non-retired typology, and
// both distinct commuting-leak directions.
func TestDrillTargets_RegistersDocumentedFigures(t *testing.T) {
	typologies := []TypologyRow{
		{Typology: "Terrace", Demand: 100, Stock: 90},
		{Typology: "Bungalow", Demand: 10, Stock: 9, Retired: true},
	}
	commute := CommuteFigures{OutCommuters: 5, InCommuters: 3}

	targets := DrillTargets(typologies, commute)

	byID := map[string]DrillTarget{}
	for _, tgt := range targets {
		byID[tgt.WidgetID] = tgt
	}

	if _, ok := byID["demo.pyramid.total"]; !ok {
		t.Errorf("missing drill target for the pyramid total")
	}
	if _, ok := byID["demo.typology.Terrace"]; !ok {
		t.Errorf("missing drill target for typology Terrace")
	}
	if _, ok := byID["demo.typology.Bungalow"]; ok {
		t.Errorf("retired typology Bungalow should not get a drill target (nothing live to drill into)")
	}
	if _, ok := byID["demo.commute.out"]; !ok {
		t.Errorf("missing drill target for out-commuting")
	}
	if _, ok := byID["demo.commute.in"]; !ok {
		t.Errorf("missing drill target for in-commuting")
	}
	if byID["demo.commute.out"].Target == byID["demo.commute.in"].Target {
		t.Errorf("out/in commuting drill targets must be distinct: both = %q", byID["demo.commute.out"].Target)
	}
}

func TestDrillTargets_EmptyInputsStillRegistersPyramidAndCommute(t *testing.T) {
	targets := DrillTargets(nil, CommuteFigures{})
	if len(targets) != 3 { // pyramid total + commute out + commute in
		t.Fatalf("len(targets) = %d, want 3 for no typologies", len(targets))
	}
}
