package citizens

import "testing"

// These four tests are the Destructive round-1 regression suite. Each one
// fails on the pre-fix code and passes only after the corresponding defect
// is fixed — a test that does not fail on the old code is not a fix.

// TestLifeEventMutationsWriteThroughToCold (defect 1): hot-tier life-event
// mutations must reach the cold store (the single source of truth), so a
// player who gives a hot citizen money/a job/an illness and then pans away
// (demote to COLD) does not see the change silently discarded.
func TestLifeEventMutationsWriteThroughToCold(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 1
	c.Fidelity = FidelityHot
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("birth: %v", err)
	}

	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventWealth, CitizenID: 1, Wealth: 123456}); err != nil {
		t.Fatalf("wealth: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1, Employment: EmploymentEmployed, Sector: SectorTertiary}); err != nil {
		t.Fatalf("employment: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventHealth, CitizenID: 1, HealthBand: HealthPoor}); err != nil {
		t.Fatalf("health: %v", err)
	}

	// Pan away.
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 1, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}

	got, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen vanished after demote")
	}
	if got.Wealth != 123456 {
		t.Fatalf("wealth reverted on demote: got %d, want 123456", got.Wealth)
	}
	if got.Employment.State != EmploymentEmployed || got.Employment.Sector != SectorTertiary {
		t.Fatalf("employment reverted on demote: got %+v, want employed/tertiary", got.Employment)
	}
	if got.HealthBand != HealthPoor {
		t.Fatalf("health band reverted on demote: got %v, want %v", got.HealthBand, HealthPoor)
	}
}

// TestEducationWriteThroughToCold (defect 1, education half): the education
// personality drift must also reach the cold store.
func TestEducationWriteThroughToCold(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 2
	c.Fidelity = FidelityHot
	c.Education.Attainment = 40 // shift = 40/20 = +2 on ambition/novelty
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("birth: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventEducation, CitizenID: 2}); err != nil {
		t.Fatalf("education: %v", err)
	}
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 2, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	got, ok := api.CitizenAt(2, "corr")
	if !ok {
		t.Fatal("citizen vanished after demote")
	}
	// testCitizen personality = 50; attainment 40 drifts ambition to 52.
	if got.Personality[AxisAmbition] != 52 {
		t.Fatalf("ambition drift not written through: got %d, want 52", got.Personality[AxisAmbition])
	}
}

// TestBirthRejectsOutOfRangeAttainment (defect 2): an attainment score that
// would wrap in the int16 cold column is rejected with a registry error,
// never narrowed to a corrupted value, and nothing is persisted.
func TestBirthRejectsOutOfRangeAttainment(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	before := api.TotalPopulation("corr")
	c := testCitizen()
	c.ID = 5
	c.Fidelity = FidelityHot
	c.Education.Attainment = 40000 // int16(40000) == -25536 (wrap corruption)
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c})
	if err == nil {
		t.Fatal("expected out-of-range attainment to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrAttainmentOutOfRange)
	if after := api.TotalPopulation("corr"); after != before {
		t.Fatalf("invalid birth persisted: population %d -> %d", before, after)
	}
}

// TestValidateCitizenRejectsOutOfRangeAttainment (defect 2, direct): the
// validator itself rejects an out-of-range attainment.
func TestValidateCitizenRejectsOutOfRangeAttainment(t *testing.T) {
	c := testCitizen()
	c.Education.Attainment = 40000
	err := ValidateCitizen(c, func(uint64) bool { return true }, "corr")
	assertRegistryCode(t, err, ErrAttainmentOutOfRange)
}

// TestSatisfactionDriftNeverNegative (defect 3): the satisfaction clamp's
// lower bound is 0 (the documented 0-100 contract), not int8's type-wide
// minimum — a negative drift must not push satisfaction below 0.
func TestSatisfactionDriftNeverNegative(t *testing.T) {
	if got := clampSat(-3); got != 0 {
		t.Fatalf("clampSat(-3) = %d, want 0 (satisfaction floor is 0, not int8 min)", got)
	}
	if got := clampSat(101); got != 100 {
		t.Fatalf("clampSat(101) = %d, want 100", got)
	}
	if got := clampSat(50); got != 50 {
		t.Fatalf("clampSat(50) = %d, want 50", got)
	}

	// End-to-end: a cold citizen with satisfaction 0 receiving a negative
	// drift must stay at 0, never go negative.
	s := newColdShard(0)
	rec := mkRecord(1, 0)
	rec.BirthMonth = 0
	rec.SatHousing = 0
	s.append(rec)
	params := ColdPassParams{MortalityMultiplier: 0, SatisfactionDrift: -3}
	s.applyMonthly(1, 0, params, nil)
	if s.satHousing[0] < 0 {
		t.Fatalf("housing satisfaction drifted below 0: %d", s.satHousing[0])
	}
}

// TestHotRecordClockAdvances (defect 4): a hot citizen's derived age must
// track the sim clock across months, not freeze at the promotion month and
// then jump on demote.
func TestHotRecordClockAdvances(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 1
	c.BirthMonth = 0
	c.Fidelity = FidelityHot
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("birth: %v", err)
	}

	if got, _ := api.CitizenAt(1, "corr"); got.Age() != 0 {
		t.Fatalf("age at birth = %d, want 0", got.Age())
	}

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 1: %v", err)
	}
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth 2: %v", err)
	}

	got, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen vanished after two months")
	}
	if got.Age() != 2 {
		t.Fatalf("hot citizen age = %d after two months, want 2 (hot record clock must track the sim clock)", got.Age())
	}
}

// --- Round-2 regression suite: the hot→cold narrowing class, fixed
// systematically, not one-field-at-a-time. Each test fails on the
// pre-round-2 code. ---

// TestBirthRejectsOutOfRangeSatisfaction (round-2 defect 1): a satisfaction
// component outside [0,100] is rejected at birth, never narrowed to a
// wrapped int8 (int8(200) == -56).
func TestBirthRejectsOutOfRangeSatisfaction(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	before := api.TotalPopulation("corr")
	c := testCitizen()
	c.ID = 10
	c.Fidelity = FidelityHot
	c.Satisfaction[SatHousing] = 200
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c})
	if err == nil {
		t.Fatal("expected out-of-range satisfaction to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrFieldOutOfRange)
	if after := api.TotalPopulation("corr"); after != before {
		t.Fatalf("invalid birth persisted: population %d -> %d", before, after)
	}
}

// TestSeedRejectsOutOfRangeSatisfaction (round-2 defect 1, seed path): the
// bulk-seed path enforces the same boundary contract.
func TestSeedRejectsOutOfRangeSatisfaction(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	r := mkRecord(1, 0)
	r.SatHousing = 200
	err = api.SeedColdRecords([]ColdRecord{r}, "corr")
	if err == nil {
		t.Fatal("expected out-of-range satisfaction to be rejected on seed, got nil")
	}
	assertRegistryCode(t, err, ErrFieldOutOfRange)
	if got := api.TotalPopulation("corr"); got != 0 {
		t.Fatalf("invalid seed persisted: population %d, want 0", got)
	}
}

// TestBirthRejectsOutOfRangeBirthMonth (round-2 defect 2): a birthMonth
// that would overflow the int16 age delta (40000) is rejected, never
// wrapped to -25536.
func TestBirthRejectsOutOfRangeBirthMonth(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 11
	c.Fidelity = FidelityHot
	c.BirthMonth = 40000
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c})
	if err == nil {
		t.Fatal("expected out-of-range birthMonth to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrInvalidBirthMonth)
}

// TestBirthRejectsOutOfRangeEmployment (round-2 defect 3): an out-of-domain
// employment state/sector enum is rejected, never packed to invalid
// (15,15) values.
func TestBirthRejectsOutOfRangeEmployment(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 12
	c.Fidelity = FidelityHot
	c.Employment.State = EmploymentState(255)
	c.Employment.Sector = Sector(255)
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c})
	if err == nil {
		t.Fatal("expected out-of-range employment enum to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrFieldOutOfRange)
}

// TestBirthRejectsOutOfRangeSchooling (round-2 defect 4): a schooling score
// outside int16 is rejected like attainment, so hot and cold never diverge
// (no silent clamp to 32767 in one store and 40000 in the other).
func TestBirthRejectsOutOfRangeSchooling(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 13
	c.Fidelity = FidelityHot
	c.Education.SchoolingMonths = 40000
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c})
	if err == nil {
		t.Fatal("expected out-of-range schooling to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrFieldOutOfRange)
}

// TestHotColdLeisureConsistentAfterClockAdvance (round-2 defect 5): the
// age-derived Leisure must be re-derived when the hot record's Month
// advances, so a hot citizen and the cold record it shadows stay
// observationally identical.
func TestHotColdLeisureConsistentAfterClockAdvance(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 1
	c.BirthMonth = 0
	c.Fidelity = FidelityHot
	c.Education.Attainment = 40
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("birth: %v", err)
	}

	// Advance 121 months so age ≥ 120 — the point where DeriveLeisureWeights'
	// age spread (ageMonths/120) changes, so a stale hot Leisure and a fresh
	// cold Leisure would diverge.
	for i := 0; i < 121; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", i, err)
		}
	}

	hot, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("hot citizen vanished")
	}
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 1, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	cold, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("cold citizen vanished after demote")
	}
	if hot.Leisure != cold.Leisure {
		t.Fatalf("hot vs cold leisure diverge after clock advance: hot=%v cold=%v", hot.Leisure, cold.Leisure)
	}
}

// --- Round-3 regression suite: the command entry point + the two
// cold→hot field-drop bugs, fixed as a design change (one shared validator
// set across all five entry points). Each test fails on the pre-fix code. ---

// TestEmploymentCommandRejectsOutOfRangeEnum (round-3 entry point 3): the
// command path must validate employment enums exactly like birth — 255/255
// (would pack to 15,15) and 6 (outside the 0-5 domain, EmploymentOffMap=5
// being the widened extension's new legal top value — FEAT-198) are both
// rejected, and nothing is persisted.
func TestEmploymentCommandRejectsOutOfRangeEnum(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seed := mkRecord(1, 0) // EmploymentStudent / SectorPrimary
	if err := api.SeedColdRecords([]ColdRecord{seed}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1, Employment: EmploymentState(255), Sector: Sector(255)})
	assertRegistryCode(t, err, ErrFieldOutOfRange)

	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventEmployment, CitizenID: 1, Employment: EmploymentState(6), Sector: SectorTertiary})
	assertRegistryCode(t, err, ErrFieldOutOfRange)

	got, _ := api.CitizenAt(1, "corr")
	if got.Employment.State != seed.EmploymentState || got.Employment.Sector != seed.Sector {
		t.Fatalf("employment mutated despite rejection: got %+v, want %v/%v", got.Employment, seed.EmploymentState, seed.Sector)
	}
}

// TestHealthCommandRejectsOutOfRangeBand (round-3 entry point 3): the
// command path must validate the health band exactly like birth — 255 and 6
// (outside the 0-5 domain) are both rejected.
func TestHealthCommandRejectsOutOfRangeBand(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seed := mkRecord(1, 0) // HealthPoor
	if err := api.SeedColdRecords([]ColdRecord{seed}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventHealth, CitizenID: 1, HealthBand: HealthBand(255)})
	assertRegistryCode(t, err, ErrFieldOutOfRange)

	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventHealth, CitizenID: 1, HealthBand: HealthBand(6)})
	assertRegistryCode(t, err, ErrFieldOutOfRange)

	got, _ := api.CitizenAt(1, "corr")
	if got.HealthBand != seed.HealthBand {
		t.Fatalf("health band mutated despite rejection: got %v, want %v", got.HealthBand, seed.HealthBand)
	}
}

// TestWidenPreservesStage (round-3 field-drop a): cold→hot widening carries
// the current stage into Education.Stages, so currentStage() reads it back.
func TestWidenPreservesStage(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	rec := mkRecord(1, 0)
	rec.Stage = StageUniversity
	if err := api.SeedColdRecords([]ColdRecord{rec}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	got, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen not found")
	}
	if currentStage(got.Education) != StageUniversity {
		t.Fatalf("widened citizen lost its stage: currentStage = %v, want %v", currentStage(got.Education), StageUniversity)
	}
}

// TestBirthAndDemotePreserveSchool (round-3 field-drop b): both Workplace
// and School survive hot→cold→hot with no silent loss.
func TestBirthAndDemotePreserveSchool(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 1
	c.Fidelity = FidelityHot
	c.Workplace = 999
	c.School = 12345
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("birth: %v", err)
	}
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 1, Target: FidelityCold}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	got, ok := api.CitizenAt(1, "corr")
	if !ok {
		t.Fatal("citizen not found after demote")
	}
	if got.Workplace != 999 || got.School != 12345 {
		t.Fatalf("workplace/school lost on demote: workplace=%d school=%d, want 999/12345", got.Workplace, got.School)
	}
}

// --- Round-4 regression suite: narrow-typed arithmetic overflow in derived
// computations. Each test fails on the pre-fix code. ---

// TestLifeWriteSocialContactsNoOverflow (round-4): the sociability axis is
// int8; scaling it (sociability * 9 / 100) must promote to int32 BEFORE the
// multiply, or sociability ≥ 15 overflows int8 (15×9 = 135 > 127) and wraps
// to 255/-1.
func TestLifeWriteSocialContactsNoOverflow(t *testing.T) {
	cases := []struct {
		sociability int8
		want        uint8
	}{
		{15, 1}, {45, 4}, {100, 9}, {20, 1},
	}
	for _, tc := range cases {
		rec := mkRecord(1, 0)
		rec.Personality[AxisSociability] = tc.sociability
		d := LifeWrite(1, 1, 0, rec, DistrictStats{HealthRate: 1.0, EmploymentRate: 1.0})
		if d.SocialContacts != tc.want {
			t.Fatalf("sociability %d -> social contacts %d, want %d", tc.sociability, d.SocialContacts, tc.want)
		}
	}
}

// TestSatisfactionDriftPromotedNoOverflow (round-4, second instance found
// by the scan): the satisfaction drift is applied in int32, so a large
// drift clamps to 100 rather than wrapping through int8 arithmetic
// (int8(50) + int8(200) would wrap to -6 → 0, silently losing the drift).
func TestSatisfactionDriftPromotedNoOverflow(t *testing.T) {
	s := newColdShard(0)
	rec := mkRecord(1, 0)
	rec.BirthMonth = 0
	rec.SatHousing = 50
	s.append(rec)
	params := ColdPassParams{MortalityMultiplier: 0, SatisfactionDrift: 200}
	s.applyMonthly(1, 0, params, nil)
	if s.satHousing[0] != 100 {
		t.Fatalf("housing satisfaction after +200 drift = %d, want 100 (clamped, no int8 wrap)", s.satHousing[0])
	}
}

// --- FEAT-169 destructive-review REJECT: duplicate citizen id rejection ---
//
// An independent destructive round found that engine.attract's admitted-
// migrant ids and this package's own fertility-born child ids partitioned
// the SAME high-bit id range (both `1<<62`) by convention, not by a shared
// allocator — with FEAT-169 wiring both live into one composition, a
// duplicate citizen id was reachable within months of simulated play, and
// LifeEventBirth appended it unconditionally, silently ALIASING an
// existing citizen (invisible to TotalPopulation's row-count-based
// conservation view: the row count stays right, only per-id lookups start
// returning the wrong citizen). fertilityChildIDBase moved to `1<<63`
// (fixing the range collision itself — see fertility.go/errors.go), and
// these two tests are the DEFENSE-IN-DEPTH regression coverage for the
// remaining case: a LifeEventBirth whose id already exists must be
// rejected outright, regardless of how it got there.

// TestBirthRejectsDuplicateColdID: a second LifeEventBirth at an id already
// present in the cold store must be rejected with ErrDuplicateCitizenID,
// not silently appended as a second row under the same id.
func TestBirthRejectsDuplicateColdID(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 42
	c.Fidelity = FidelityCold
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("first birth: %v", err)
	}
	before := api.TotalPopulation("corr")

	// A structurally different record (different personality) at the SAME
	// id — proving this is an id check, not a duplicate-content check.
	dup := testCitizen()
	dup.ID = 42
	dup.Fidelity = FidelityCold
	dup.Personality[0] = 99
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: dup})
	if err == nil {
		t.Fatal("expected a duplicate birth at an existing cold id to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrDuplicateCitizenID)
	if after := api.TotalPopulation("corr"); after != before {
		t.Fatalf("duplicate birth persisted: population %d -> %d, want unchanged", before, after)
	}
	// The ORIGINAL record must be intact (not overwritten/aliased).
	got, ok := api.CitizenAt(42, "corr")
	if !ok {
		t.Fatal("original citizen 42 vanished after the rejected duplicate birth")
	}
	if got.Personality[0] == 99 {
		t.Fatal("original citizen 42 was overwritten by the rejected duplicate birth")
	}
}

// TestBirthRejectsDuplicateHotID: same as above, but the existing citizen
// is HOT (elevated), proving the duplicate check covers both tiers — a
// cold-only check would miss a collision against a currently-elevated
// citizen.
func TestBirthRejectsDuplicateHotID(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	c := testCitizen()
	c.ID = 77
	c.Fidelity = FidelityHot
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: c}); err != nil {
		t.Fatalf("first birth: %v", err)
	}
	before := api.TotalPopulation("corr")

	dup := testCitizen()
	dup.ID = 77
	dup.Fidelity = FidelityCold
	err = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: dup})
	if err == nil {
		t.Fatal("expected a duplicate birth at an existing hot id to be rejected, got nil")
	}
	assertRegistryCode(t, err, ErrDuplicateCitizenID)
	if after := api.TotalPopulation("corr"); after != before {
		t.Fatalf("duplicate birth persisted: population %d -> %d, want unchanged", before, after)
	}
}
