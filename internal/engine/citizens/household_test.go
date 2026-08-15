package citizens

import "testing"

// TestPartneringCreatesSharedHousehold (AC-12): a partnering event creates
// a real household entity and both partners share its id (in BOTH the cold
// store and the reconstructed view).
func TestPartneringCreatesSharedHousehold(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0), mkRecord(2, 0)}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	cmd := LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 1, PartnerID: 2}
	if err := api.ApplyLifeEventCommand(cmd); err != nil {
		t.Fatalf("partner: %v", err)
	}

	a, okA := api.CitizenAt(1, "corr")
	b, okB := api.CitizenAt(2, "corr")
	if !okA || !okB {
		t.Fatalf("CitizenAt after partnering: a=%v b=%v", okA, okB)
	}
	if a.Household == 0 || a.Household != b.Household {
		t.Fatalf("partners must share a non-zero householdId, got a=%d b=%d", a.Household, b.Household)
	}
	if a.Partner != 2 || b.Partner != 1 {
		t.Fatalf("partner links wrong: a.Partner=%d b.Partner=%d", a.Partner, b.Partner)
	}

	hh, ok := api.Household(a.Household, "corr")
	if !ok {
		t.Fatalf("household %d not registered", a.Household)
	}
	if len(hh.Members) != 2 {
		t.Fatalf("household membership = %d, want 2", len(hh.Members))
	}
}

// TestDeathDissolvesHousehold (BUG-235): a departing citizen (mortality or
// emigration via LifeEventDeath) must be unwired from their household — the
// inverse of the partnering wiring — so the household is dissolved once it
// drops below the pairing threshold and the surviving member's HouseholdOf
// mapping is cleared. Pre-fix, the dead citizen's id stayed in the
// household's Members list, orphaning the record so re-querying it failed
// with ErrOrphanedMember (MET-G606).
func TestDeathDissolvesHousehold(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0), mkRecord(2, 0)}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 1, PartnerID: 2}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(1, "corr")
	if !ok {
		t.Fatal("household not formed")
	}
	householdID := hh.ID

	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1}); err != nil {
		t.Fatalf("death: %v", err)
	}

	// The departed citizen is gone from both stores.
	if _, ok := api.CitizenAt(1, "corr"); ok {
		t.Fatal("departed citizen 1 still resolves")
	}
	// The household dropped below the pairing threshold and is dissolved.
	if _, ok := api.Household(householdID, "corr"); ok {
		t.Fatal("household survived a drop below the pairing threshold")
	}
	// The surviving member's HouseholdOf mapping is cleared.
	if _, ok := api.HouseholdOf(2, "corr"); ok {
		t.Fatal("surviving member 2 still maps to a dissolved household")
	}
	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished")
	}
	if surv.Household != 0 || surv.Partner != 0 {
		t.Fatalf("surviving member 2 retains household/partner references: household=%d partner=%d", surv.Household, surv.Partner)
	}
}

// TestDeathClearsHotSurvivorHousehold (BUG-235, hot path): the surviving
// member may be elevated (HOT); their household/partner references must be
// cleared in the hot cache too, not only the cold store — otherwise a hot
// survivor keeps reporting the dissolved household id.
func TestDeathClearsHotSurvivorHousehold(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0), mkRecord(2, 0)}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	// Elevate the survivor (2) to HOT so both the hot cache and the cold
	// store must be cleared by the death unwiring.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 2, Target: FidelityHot}); err != nil {
		t.Fatalf("elevate: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 1, PartnerID: 2}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1}); err != nil {
		t.Fatalf("death: %v", err)
	}

	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished")
	}
	if surv.Household != 0 || surv.Partner != 0 {
		t.Fatalf("hot survivor 2 retains household/partner references: household=%d partner=%d", surv.Household, surv.Partner)
	}
	if _, ok := api.HouseholdOf(2, "corr"); ok {
		t.Fatal("hot survivor 2 still maps to a dissolved household")
	}

	// The cold store (single source of truth) is cleared too: demote and
	// confirm the household reference did not resurrect from cold columns.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 2, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	cold, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished after demote")
	}
	if cold.Household != 0 || cold.Partner != 0 {
		t.Fatalf("cold survivor 2 resurrected household/partner references: household=%d partner=%d", cold.Household, cold.Partner)
	}
}

// TestOvercrowdingDerived (AC-12): overcrowding is derivable from household
// composition and dwelling size.
func TestOvercrowdingDerived(t *testing.T) {
	small := Household{ID: 1, Members: []uint64{1, 2, 3}, DwellingRooms: 2}
	if !small.Overcrowded() {
		t.Fatal("3 members in 2 rooms must be overcrowded")
	}
	big := Household{ID: 2, Members: []uint64{1, 2}, DwellingRooms: 4}
	if big.Overcrowded() {
		t.Fatal("2 members in 4 rooms must not be overcrowded")
	}
}
