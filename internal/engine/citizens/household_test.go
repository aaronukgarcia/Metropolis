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

// TestDeathDissolvesHousehold (BUG-235, invariant updated by the F1 fix): a
// departing citizen (mortality or emigration via LifeEventDeath) must be
// unwired from their household -- the inverse of the partnering wiring --
// so the surviving member's Partner reference is cleared (the pairing
// dissolves). Pre-BUG-235-fix, the dead citizen's id stayed in the
// household's Members list, orphaning the record (ErrOrphanedMember,
// MET-G606). Pre-F1-fix, dissolution/unwiring was (wrongly) keyed on raw
// membership count rather than the pairing, which broke as soon as a
// household had children (see household.go's dissolution-invariant doc).
// Under the current invariant a household PERSISTS as long as any member
// remains -- here the childless survivor keeps living in the same
// household alone -- and is deleted only once fully empty.
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
	// The household persists (the lone survivor still lives there) --
	// it is deleted only once its Members list is fully empty.
	hhAfter, ok := api.Household(householdID, "corr")
	if !ok {
		t.Fatal("household deleted while a member (the survivor) still remains")
	}
	if len(hhAfter.Members) != 1 || hhAfter.Members[0] != 2 {
		t.Fatalf("household members after death = %v, want [2]", hhAfter.Members)
	}
	// The surviving member still maps to the (persisting) household.
	survHH, ok := api.HouseholdOf(2, "corr")
	if !ok || survHH.ID != householdID {
		t.Fatalf("surviving member 2 must still map to household %d, got ok=%v id=%d", householdID, ok, survHH.ID)
	}
	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished")
	}
	// The PAIRING dissolves (Partner -> 0) but the Household reference is
	// left intact (F1 fix).
	if surv.Partner != 0 {
		t.Fatalf("surviving member 2 retains a partner reference: partner=%d", surv.Partner)
	}
	if surv.Household != householdID {
		t.Fatalf("surviving member 2 lost its household reference: household=%d, want %d", surv.Household, householdID)
	}
}

// TestDeathClearsHotSurvivorPartner (BUG-235/F1, hot path): the surviving
// member may be elevated (HOT); their Partner reference must be cleared in
// the hot cache too, not only the cold store, while their Household
// reference (the persisting household) is left untouched in both.
func TestDeathClearsHotSurvivorPartner(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0), mkRecord(2, 0)}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	// Elevate the survivor (2) to HOT so both the hot cache and the cold
	// store must be updated by the death unwiring.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 2, Target: FidelityHot}); err != nil {
		t.Fatalf("elevate: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 1, PartnerID: 2}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(2, "corr")
	if !ok {
		t.Fatal("household not formed")
	}
	householdID := hh.ID

	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1}); err != nil {
		t.Fatalf("death: %v", err)
	}

	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished")
	}
	if surv.Partner != 0 {
		t.Fatalf("hot survivor 2 retains a partner reference: partner=%d", surv.Partner)
	}
	if surv.Household != householdID {
		t.Fatalf("hot survivor 2 lost its household reference: household=%d, want %d", surv.Household, householdID)
	}
	survHH, ok := api.HouseholdOf(2, "corr")
	if !ok || survHH.ID != householdID {
		t.Fatalf("hot survivor 2 must still map to household %d, got ok=%v id=%d", householdID, ok, survHH.ID)
	}

	// The cold store (single source of truth) matches: demote and confirm
	// the same partner-cleared/household-intact state survives the round trip.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 2, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	cold, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving member 2 vanished after demote")
	}
	if cold.Partner != 0 {
		t.Fatalf("cold survivor 2 resurrected a partner reference: partner=%d", cold.Partner)
	}
	if cold.Household != householdID {
		t.Fatalf("cold survivor 2 lost its household reference: household=%d, want %d", cold.Household, householdID)
	}
}

// TestDeathWithChildrenPersistsHousehold (F1 regression, destructive-review
// REJECT on FEAT-160): pre-fix, removeHouseholdMemberLocked inferred "still
// a pair" from len(h.Members) >= pairingThreshold(2). Once a couple has
// children, a widowed parent's Members slice still holds parent + children
// (>= 2 entries) even though the ADULT PAIRING is gone, so the real
// LifeEventDeath command path never dissolved the pairing: the dead
// partner's id stayed in h.Members AND the survivor's cold Partner column
// still pointed at the dead id. This test builds exactly that shape (couple
// + 2 children) and kills one partner via the command path (never the
// cold-pass death path, which is BUG-270's separate flagged gap), then
// asserts: the dead id is gone from Members, the survivor's Partner == 0,
// and the household still exists with the survivor + both children.
func TestDeathWithChildrenPersistsHousehold(t *testing.T) {
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

	// Seed two children directly into the couple's household -- both the
	// cold record's Household column and the Household's own Members list,
	// exactly what birthChildLocked wires a real fertility birth into
	// (fertility.go), without depending on the probabilistic fertility draw.
	child1 := mkRecord(3, 0)
	child1.Household = safeUint32(householdID)
	child1.Partner = 0
	child2 := mkRecord(4, 0)
	child2.Household = safeUint32(householdID)
	child2.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{child1, child2}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords children: %v", err)
	}
	api.mu.Lock()
	api.households[householdID].AddMember(3)
	api.households[householdID].AddMember(4)
	api.mu.Unlock()

	// Kill partner 1 via the command path (LifeEventDeath), the same path
	// the real mortality/emigration flow uses -- never the cold-pass death
	// path (BUG-270's separate gap).
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1}); err != nil {
		t.Fatalf("death: %v", err)
	}

	// The dead id is gone from Members.
	hhAfter, ok := api.Household(householdID, "corr")
	if !ok {
		t.Fatal("household deleted while the survivor and children still remain")
	}
	for _, m := range hhAfter.Members {
		if m == 1 {
			t.Fatalf("dead partner 1 still present in household Members: %v", hhAfter.Members)
		}
	}
	wantMembers := map[uint64]bool{2: true, 3: true, 4: true}
	if len(hhAfter.Members) != len(wantMembers) {
		t.Fatalf("household Members after death = %v, want survivor+2 children (len %d)", hhAfter.Members, len(wantMembers))
	}
	for _, m := range hhAfter.Members {
		if !wantMembers[m] {
			t.Fatalf("unexpected member %d in household Members %v", m, hhAfter.Members)
		}
	}

	// The survivor's Partner column is cleared (the pairing dissolved, so
	// the survivor may legitimately re-partner later) -- this is F1's core
	// repro check.
	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("surviving parent 2 vanished")
	}
	if surv.Partner != 0 {
		t.Fatalf("surviving parent's Partner column still points at the dead partner: partner=%d, want 0", surv.Partner)
	}
	// The household itself PERSISTS (surviving parent + children stay a
	// household) -- the survivor's Household reference is untouched.
	if surv.Household != householdID {
		t.Fatalf("surviving parent lost its household reference: household=%d, want %d", surv.Household, householdID)
	}
}

// TestRepartneringDetachesFromOldHousehold (round-3 fix, P1 data-integrity
// REJECT): pre-fix, LifeEventPartner never unwired an incoming partner from
// a household they ALREADY belonged to before forming the new pairing --
// FormHousehold mints a fresh household and overwrites the citizen's own
// Household field, but the OLD household's Members list was never pruned,
// so a widowed survivor who re-partnered ended up double-listed (still a
// member of the old household AND the new one). This test builds exactly
// F1's regression shape (couple + 2 children), kills one partner via the
// command path, then has the survivor re-partner with a fresh citizen, and
// asserts: the survivor is a member of ONLY the new household (never the
// old one), and the old household is correctly cleaned up per the chosen
// orphan rule -- see below.
//
// Orphan rule (documented, chosen coherent extension of F1's own
// dissolution invariant -- "a household persists as long as ANY member
// remains"): re-partnering detaches ONLY the departing citizen from their
// old household; it does not carry children along into the new pairing.
// Here the old household still holds the couple's two children after the
// survivor leaves, so — per F1's own persistence rule — it persists as a
// childless-adult ("orphan") household containing just the two children,
// rather than being deleted or silently merged into the new pairing. No
// member is ever double-listed across the two households.
func TestRepartneringDetachesFromOldHousehold(t *testing.T) {
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
	oldHouseholdID := hh.ID

	// Seed two children into the couple's household, exactly mirroring
	// TestDeathWithChildrenPersistsHousehold's setup.
	child1 := mkRecord(3, 0)
	child1.Household = safeUint32(oldHouseholdID)
	child1.Partner = 0
	child2 := mkRecord(4, 0)
	child2.Household = safeUint32(oldHouseholdID)
	child2.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{child1, child2}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords children: %v", err)
	}
	api.mu.Lock()
	api.households[oldHouseholdID].AddMember(3)
	api.households[oldHouseholdID].AddMember(4)
	api.mu.Unlock()

	// Kill partner 1 via the command path -- the survivor (2) keeps the old
	// household per F1.
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1}); err != nil {
		t.Fatalf("death: %v", err)
	}

	// A fresh citizen (5) for the survivor to re-partner with. mkRecord's
	// default Household/Partner formula (id/2, id/2+1) is arbitrary
	// placeholder data unrelated to any real household -- zero it
	// explicitly (matching mkFertilityCouple's convention) so this citizen
	// starts genuinely unpaired.
	fresh := mkRecord(5, 0)
	fresh.Household = 0
	fresh.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{fresh}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords fresh citizen: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 2, PartnerID: 5}); err != nil {
		t.Fatalf("re-partner: %v", err)
	}

	// The survivor must now belong to exactly ONE household: the new one.
	newHH, ok := api.HouseholdOf(2, "corr")
	if !ok {
		t.Fatal("survivor has no household after re-partnering")
	}
	newHouseholdID := newHH.ID
	if newHouseholdID == oldHouseholdID {
		t.Fatalf("re-partnering must mint a NEW household distinct from the old one, got same id %d", oldHouseholdID)
	}
	if len(newHH.Members) != 2 {
		t.Fatalf("new household membership = %v, want survivor(2)+new partner(5)", newHH.Members)
	}
	foundSurvivor, foundNewPartner := false, false
	for _, m := range newHH.Members {
		if m == 2 {
			foundSurvivor = true
		}
		if m == 5 {
			foundNewPartner = true
		}
	}
	if !foundSurvivor || !foundNewPartner {
		t.Fatalf("new household members = %v, want [2 5]", newHH.Members)
	}

	// The OLD household must no longer list the survivor as a member (the
	// core repro: pre-fix this leaked forever).
	oldHH, ok := api.Household(oldHouseholdID, "corr")
	if !ok {
		t.Fatal("old household deleted while children still remain -- orphan rule violated")
	}
	for _, m := range oldHH.Members {
		if m == 2 {
			t.Fatalf("survivor 2 still listed in the OLD household's Members after re-partnering: %v", oldHH.Members)
		}
	}
	// Per the chosen orphan rule: the old household persists holding just
	// the two children.
	wantOldMembers := map[uint64]bool{3: true, 4: true}
	if len(oldHH.Members) != len(wantOldMembers) {
		t.Fatalf("old household members after re-partnering = %v, want just the 2 children", oldHH.Members)
	}
	for _, m := range oldHH.Members {
		if !wantOldMembers[m] {
			t.Fatalf("unexpected member %d left in old household %v", m, oldHH.Members)
		}
	}

	// The survivor's own Household/Partner fields point ONLY at the new
	// pairing -- no stale reference to the old household remains anywhere.
	surv, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("survivor vanished")
	}
	if surv.Household != newHouseholdID {
		t.Fatalf("survivor's Household field = %d, want new household %d", surv.Household, newHouseholdID)
	}
	if surv.Partner != 5 {
		t.Fatalf("survivor's Partner field = %d, want 5", surv.Partner)
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
