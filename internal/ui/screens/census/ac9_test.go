package census

// AC-9 (KPI tiles registered into ui.dash's drill graph).

import (
	"testing"
)

// TestDashRegistration_EveryFigureHasDrillTarget asserts every AC-5/AC-7
// figure has a corresponding dash.DrillTarget registration, and that
// every target names the real, registered "f6.census" view (never a
// fabricated non-view, GR#3).
func TestDashRegistration_EveryFigureHasDrillTarget(t *testing.T) {
	s := newScreenWithData(t, "sub-dash")
	kpis, _ := s.KPITiles()
	bio, haveBio := s.SelectedBio()

	targets := DrillTargets(kpis, bio, haveBio)

	// 8 KPI tiles + 5 bio facets.
	wantLen := len(kpis) + 5
	if len(targets) != wantLen {
		t.Fatalf("DrillTargets produced %d targets, want %d (8 KPI tiles + 5 bio facets)", len(targets), wantLen)
	}

	seenKPI := map[string]bool{}
	seenFacet := map[string]bool{}
	for _, tgt := range targets {
		if tgt.ViewName != ViewSubscriptionName {
			t.Errorf("target %q ViewName = %q, want %q", tgt.EntityID, tgt.ViewName, ViewSubscriptionName)
		}
		if !tgt.Valid() {
			t.Errorf("target %q is not valid (a dead end)", tgt.EntityID)
		}
		switch {
		case len(tgt.EntityID) > 4 && tgt.EntityID[:4] == "kpi.":
			seenKPI[string(tgt.EntityID[4:])] = true
		case len(tgt.EntityID) > 4 && tgt.EntityID[:4] == "bio.":
			seenFacet[string(tgt.EntityID)] = true
		}
	}
	for _, k := range kpis {
		if !seenKPI[k.Key] {
			t.Errorf("KPI %q has no dash.DrillTarget registration", k.Key)
		}
	}
	wantFacets := []string{"education", "employment", "family", "retirement", "income"}
	for _, f := range wantFacets {
		key := "bio." + bio.GUID + "." + f
		if !seenFacet[key] {
			t.Errorf("bio facet %q has no dash.DrillTarget registration", f)
		}
	}
}

// TestDashRegistration_NoBioNoFacetTargets proves a screen with no
// selected citizen registers zero bio-facet targets (never a fabricated
// target for data that does not exist).
func TestDashRegistration_NoBioNoFacetTargets(t *testing.T) {
	targets := DrillTargets(nil, CitizenBio{}, false)
	if len(targets) != 0 {
		t.Errorf("DrillTargets with no KPIs and no bio produced %d targets, want 0", len(targets))
	}
}
