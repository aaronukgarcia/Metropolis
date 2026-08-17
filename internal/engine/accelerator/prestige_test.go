package accelerator

import "testing"

// TestPrestigeZeroBeforeOperationalNonzeroAfter is AC-10: prestige is a
// queryable numeric output on the accelerator's own surface — zero before the
// facility is operational, nonzero after.
func TestPrestigeZeroBeforeOperationalNonzeroAfter(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())

	if p := a.Prestige(); p != 0 {
		t.Fatalf("prestige before operational = %d, want 0", p)
	}

	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p := a.Prestige(); p <= 0 {
		t.Fatalf("prestige after operational = %d, want > 0", p)
	}
}

// TestPrestigeAccumulatesPerTick is AC-15's accumulation clause: prestige is
// accumulated through num's saturating helpers across ticks, so it grows
// deterministically while the facility operates.
func TestPrestigeAccumulatesPerTick(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	afterBuild := a.Prestige()
	if err := a.Operate(1); err != nil {
		t.Fatalf("Operate(1): %v", err)
	}
	afterOne := a.Prestige()
	if afterOne <= afterBuild {
		t.Errorf("prestige after one tick = %d, want > after build %d", afterOne, afterBuild)
	}

	// Re-running the same tick is idempotent (GR#1): no double accumulation.
	if err := a.Operate(1); err != nil {
		t.Fatalf("Operate(1) re-run: %v", err)
	}
	if p := a.Prestige(); p != afterOne {
		t.Errorf("prestige after re-running tick 1 = %d, want unchanged %d (idempotent)", p, afterOne)
	}
}
