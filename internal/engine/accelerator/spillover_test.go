package accelerator

import "testing"

// TestResearchRateMultiplierRaisesOutput is AC-7: the accelerator exposes a
// data-sourced research-rate multiplier that, applied to engine.education's
// research output, raises it — the same figure the expert gate reads. Offline
// the multiplier is the identity; online it raises the output (direction, not
// magnitude — balance-number regime).
func TestResearchRateMultiplierRaisesOutput(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())

	const baseline = 1000
	if m := a.ResearchMultiplier(); m != 1 {
		t.Fatalf("offline multiplier = %v, want identity 1", m)
	}
	if got := a.AppliedResearch(baseline); got != baseline {
		t.Fatalf("offline AppliedResearch(%d) = %d, want %d (unchanged)", baseline, got, baseline)
	}

	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := a.ResearchMultiplier()
	if m <= 1 {
		t.Fatalf("online multiplier = %v, want > 1 (raises research output)", m)
	}
	boosted := a.AppliedResearch(baseline)
	if boosted <= baseline {
		t.Errorf("AppliedResearch(%d) = %d, want strictly above the baseline (research-rate ××)", baseline, boosted)
	}
}

// TestHealthSpilloverImprovesWellbeing is AC-8: the accelerator's research
// spills over into health through the engine.wellbeing seam (never a phantom
// engine.health module). The wellbeing outcome improves with the accelerator
// online (direction only).
func TestHealthSpilloverImprovesWellbeing(t *testing.T) {
	a, u := loadTestAPI(t)
	d := wireAll(t, a, u, a.ExpertGateThreshold())

	// Offline: no spillover posted, no improvement.
	if a.HealthSpillover() != 0 {
		t.Fatalf("offline HealthSpillover = %v, want 0", a.HealthSpillover())
	}

	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := a.Operate(1); err != nil {
		t.Fatalf("Operate: %v", err)
	}

	if a.HealthSpillover() <= 0 {
		t.Errorf("online HealthSpillover = %v, want > 0", a.HealthSpillover())
	}
	if d.wellbeing.posts == 0 {
		t.Fatal("no health spillover posted into the wellbeing seam while online")
	}
	if d.wellbeing.outcome <= 0 {
		t.Errorf("wellbeing outcome = %v, want improved (> 0) with the accelerator online", d.wellbeing.outcome)
	}
}

// TestFdiAnchorDrawRaisesProspectFigure is AC-9: the accelerator draws an FDI
// anchor prospect through the engine.fdi seam — a measurable,
// accelerator-conditional increase in the prospect figure, not a static
// string.
func TestFdiAnchorDrawRaisesProspectFigure(t *testing.T) {
	a, u := loadTestAPI(t)
	d := wireAll(t, a, u, a.ExpertGateThreshold())

	before := d.fdi.prospects
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	after := d.fdi.prospects

	if after <= before {
		t.Errorf("FDI prospect figure after build = %d, want strictly above before (%d)", after, before)
	}
	if a.FdiAnchorDraw() <= 0 {
		t.Errorf("FdiAnchorDraw = %d, want > 0 (a real draw, not a badge)", a.FdiAnchorDraw())
	}
}
