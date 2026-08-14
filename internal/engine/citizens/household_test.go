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
