package accelerator

import "testing"

// TestDeterministicSameSeedSameOutcome is AC-14: the draw, spillover, FDI,
// and prestige magnitudes are deterministic functions of (worldSeed, tick,
// prior state, data file, commands) — no wall clock, no shared RNG. Two
// identical runs produce byte-identical outcomes. (The seed is carried by
// [New] for the signature contract; the facility's outputs are data-driven,
// so a fixed seed yields identical results across runs.)
func TestDeterministicSameSeedSameOutcome(t *testing.T) {
	type outcome struct {
		prestige int64
		mult     float64
		spill    float64
		fdi      int64
		base     float64
		peak     float64
	}
	run := func() outcome {
		a, u := loadTestAPI(t)
		wireAll(t, a, u, a.ExpertGateThreshold())
		if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		for tick := int64(1); tick <= 5; tick++ {
			if err := a.Operate(tick); err != nil {
				t.Fatalf("Operate(%d): %v", tick, err)
			}
		}
		base, err := a.ResolvedDemand(demandOptions())
		if err != nil {
			t.Fatalf("ResolvedDemand: %v", err)
		}
		peak, err := a.PeakDemand(demandOptions())
		if err != nil {
			t.Fatalf("PeakDemand: %v", err)
		}
		return outcome{
			prestige: a.Prestige(),
			mult:     a.ResearchMultiplier(),
			spill:    a.HealthSpillover(),
			fdi:      a.FdiAnchorDraw(),
			base:     base.Power,
			peak:     peak.Power,
		}
	}

	first := run()
	second := run()
	if first != second {
		t.Errorf("identical runs diverged: first %+v != second %+v", first, second)
	}
}
