package citizens

import "testing"

// BUG-369 regression: the cold pass's mortality path removed deceased
// citizens via a bare ColdShard.removeAt with NO household dissolution,
// unlike the hot path's removeHouseholdMemberLocked (the F1 fix). A cold
// elder's death therefore left three kinds of corrupt-shaped state behind:
// the surviving partner kept Partner=dead-id (a dangling reference that
// blocks legitimate re-partnering), the household's Members list retained
// the dead id forever (a leaked member, double-listed if the survivor ever
// re-partners), and householdChildCountLocked counted the unresolvable
// dead member as a CHILD against MaxChildrenPerHousehold (silently
// suppressing re-conception). These tests mirror the attacker's repro:
// a cold elder dies in the monthly pass while their partner and a genuine
// young child share the household.
//
// The elder's death is GUARANTEED, not probabilistic: their age pushes the
// Gompertz-Makeham hazard past 1, where MortalityHazard clamps, so every
// possible hash-stream draw kills them (GR#21: the fixture is deterministic,
// never a lucky seed for the assertion that matters). The widow is placed
// far above the fertility window (FertilityHazard exactly 0 there) so the
// fertility pass can never fire for the couple mid-scenario.

const bug369ElderID uint64 = 5001
const bug369WidowID uint64 = 5002
const bug369ChildID uint64 = 5003

// runBug369Scenario builds the repro fresh: elder + widow partnered via the
// real LifeEventPartner household-formation path, one genuine young child
// added to the household, then one full AdvanceMonth. Returns the api and
// the household id.
func runBug369Scenario(t *testing.T) (*CitizensAPI, uint64) {
	t.Helper()
	api, err := NewCitizensAPI(42, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}

	const month = int64(20000)
	elder := mkRecord(bug369ElderID, 0)
	elder.BirthMonth = month - 12000 // age 1000y: hazard clamps to 1, death guaranteed
	elder.Household = 0
	elder.Partner = 0
	widow := mkRecord(bug369WidowID, 0)
	widow.BirthMonth = month - 1100 // age ~92y: above the fertility window, below cap-risk ages
	widow.Household = 0
	widow.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{elder, widow}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: bug369ElderID, PartnerID: bug369WidowID}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(bug369ElderID, "corr")
	if !ok {
		t.Fatal("household not formed")
	}

	child := mkRecord(bug369ChildID, 0)
	child.BirthMonth = month - 1 // a genuine young child: must count toward the cap
	child.Household = safeUint32(hh.ID)
	child.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{child}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords child: %v", err)
	}
	api.mu.Lock()
	api.households[hh.ID].AddMember(child.ID)
	api.month = month
	api.mu.Unlock()

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	return api, hh.ID
}

// TestColdDeathClearsWidowPartnerAndPrunesHousehold is the attacker-repro
// regression: the elder's cold-pass death must dissolve the pairing and
// prune the household exactly as LifeEventDeath does.
func TestColdDeathClearsWidowPartnerAndPrunesHousehold(t *testing.T) {
	api, hh := runBug369Scenario(t)

	if _, ok := api.coldRecord(bug369ElderID); ok {
		t.Fatal("elder survived a hazard>=1 month -- fixture broken")
	}

	widow, ok := api.CitizenAt(bug369WidowID, "corr")
	if !ok {
		t.Fatal("widow vanished alongside the elder (over-removal)")
	}
	if widow.Partner != 0 {
		t.Fatalf("widow Partner = %d after elder's cold-pass death, want 0 (pairing must dissolve)", widow.Partner)
	}
	if widow.Household != hh {
		t.Fatalf("widow Household = %d, want %d (survivor keeps the household)", widow.Household, hh)
	}

	h, ok := api.Household(hh, "corr")
	if !ok {
		t.Fatal("household deleted despite surviving members (widow + child)")
	}
	for _, m := range h.Members {
		if m == bug369ElderID {
			t.Fatal("household Members still lists the dead elder (leaked member)")
		}
	}
	foundWidow, foundChild := false, false
	for _, m := range h.Members {
		switch m {
		case bug369WidowID:
			foundWidow = true
		case bug369ChildID:
			foundChild = true
		}
	}
	if !foundWidow || !foundChild {
		t.Fatalf("household Members = %v, want exactly [%d %d]", h.Members, bug369WidowID, bug369ChildID)
	}
}

// TestColdDeathNotCountedAgainstFertilityCap: the dead elder must stop
// counting against MaxChildrenPerHousehold via householdChildCountLocked --
// pre-fix, an unresolvable (dead) member was counted as a child, silently
// suppressing the widow's future re-conception.
func TestColdDeathNotCountedAgainstFertilityCap(t *testing.T) {
	api, hh := runBug369Scenario(t)

	api.mu.RLock()
	count := api.householdChildCountLocked(hh, bug369WidowID, 0, api.month)
	api.mu.RUnlock()

	if count != 1 {
		t.Fatalf("householdChildCountLocked = %d, want 1 (only the genuine child; the dead elder must not count against the cap)", count)
	}
}

// TestColdDeathDissolutionDeterministic (GR#21): two identically-seeded
// runs of the death scenario produce byte-identical PopulationHash state --
// the dissolution work added to the monthly pass must merge deterministically
// (ascending shard order), never completion order.
func TestColdDeathDissolutionDeterministic(t *testing.T) {
	run := func() [32]byte {
		api, _ := runBug369Scenario(t)
		return api.PopulationHash("corr")
	}
	h1 := run()
	h2 := run()
	if h1 != h2 {
		t.Fatalf("PopulationHash differs across identical seeded runs: %x vs %x", h1, h2)
	}
}
