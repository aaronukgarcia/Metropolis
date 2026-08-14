package citizens

import "testing"

// TestFidelityPromotionAndDemotion (AC-4): a citizen promoted COLD→HOT on
// viewport entry, demoted HOT→WARM→COLD, with the cold store remaining the
// persistent source of truth throughout.
func TestFidelityPromotionAndDemotion(t *testing.T) {
	api, err := NewCitizensAPI(3, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(10, 0)}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	if got := api.FidelityOf(10, "corr"); got != FidelityCold {
		t.Fatalf("initial fidelity = %v, want COLD", got)
	}

	// Viewport entry: promote to HOT.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 10, Target: FidelityHot}); err != nil {
		t.Fatalf("promote to HOT: %v", err)
	}
	if got := api.FidelityOf(10, "corr"); got != FidelityHot {
		t.Fatalf("after promote, fidelity = %v, want HOT", got)
	}
	// Still in the cold store (source of truth), with a rich hot record.
	if api.TotalPopulation("corr") != 1 {
		t.Fatalf("promotion must not change population, got %d", api.TotalPopulation("corr"))
	}
	if _, ok := api.CitizenAt(10, "corr"); !ok {
		t.Fatal("promoted citizen not reachable via CitizenAt")
	}

	// Demote HOT→WARM.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 10, Target: FidelityWarm}); err != nil {
		t.Fatalf("demote to WARM: %v", err)
	}
	if got := api.FidelityOf(10, "corr"); got != FidelityWarm {
		t.Fatalf("after demote, fidelity = %v, want WARM", got)
	}

	// Demote WARM→COLD.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 10, Target: FidelityCold}); err != nil {
		t.Fatalf("demote to COLD: %v", err)
	}
	if got := api.FidelityOf(10, "corr"); got != FidelityCold {
		t.Fatalf("after final demote, fidelity = %v, want COLD", got)
	}
}

// TestFidelityTiersAreThree (AC-4): the Fidelity enum exposes exactly the
// three documented tiers with distinct values.
func TestFidelityTiersAreThree(t *testing.T) {
	if FidelityCold == FidelityWarm || FidelityWarm == FidelityHot || FidelityCold == FidelityHot {
		t.Fatal("FidelityCold/Warm/Hot must be three distinct values")
	}
	for _, f := range []Fidelity{FidelityCold, FidelityWarm, FidelityHot} {
		if f.String() == "UNKNOWN" {
			t.Fatalf("Fidelity %d has no canonical name", f)
		}
	}
}
