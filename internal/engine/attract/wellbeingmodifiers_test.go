package attract

import "testing"

// TestSetWellbeingModifiers_DefaultNeutral proves the documented no-op: an
// AttractAPI that never calls SetWellbeingModifiers (every existing New(...)
// caller) produces the exact same MigrationResult as one that explicitly
// wires a neutral (1.0, 1.0) getter (MOD-034's downstream-effect
// application seam).
func TestSetWellbeingModifiers_DefaultNeutral(t *testing.T) {
	unwired, _, _, _ := newAPI(t, validConfig())
	if err := unwired.SetTermInputs(TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(unwired): %v", err)
	}
	resUnwired, err := unwired.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration(unwired): %v", err)
	}

	wiredNeutral, _, _, _ := newAPI(t, validConfig())
	if err := wiredNeutral.SetWellbeingModifiers(func() (float64, float64) { return 1.0, 1.0 }); err != nil {
		t.Fatalf("SetWellbeingModifiers: %v", err)
	}
	if err := wiredNeutral.SetTermInputs(TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(wired): %v", err)
	}
	resWired, err := wiredNeutral.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration(wired): %v", err)
	}

	if resUnwired.A != resWired.A || resUnwired.Net != resWired.Net || resUnwired.Inflow != resWired.Inflow {
		t.Fatalf("neutral (1.0,1.0) modifiers changed the result: unwired=%+v wired=%+v", resUnwired, resWired)
	}
}

// TestSetWellbeingModifiers_SatisfactionScalesScore proves the
// SatisfactionModifier seam: with everything else held fixed, a lower
// satisfaction modifier (worse cohort wellbeing) must produce a strictly
// LOWER attractiveness score A, and (since G is linear/monotonic in
// validConfig) a strictly lower Net — never a higher one, and never
// unaffected. This is the directional check: worse wellbeing => less
// attractive city.
func TestSetWellbeingModifiers_SatisfactionScalesScore(t *testing.T) {
	full, _, _, _ := newAPI(t, validConfig())
	if err := full.SetWellbeingModifiers(func() (float64, float64) { return 1.0, 1.0 }); err != nil {
		t.Fatalf("SetWellbeingModifiers(full): %v", err)
	}
	if err := full.SetTermInputs(TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(full): %v", err)
	}
	resFull, err := full.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration(full): %v", err)
	}

	halved, _, _, _ := newAPI(t, validConfig())
	if err := halved.SetWellbeingModifiers(func() (float64, float64) { return 0.5, 1.0 }); err != nil {
		t.Fatalf("SetWellbeingModifiers(halved): %v", err)
	}
	if err := halved.SetTermInputs(TermInputs{
		JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80,
	}); err != nil {
		t.Fatalf("SetTermInputs(halved): %v", err)
	}
	resHalved, err := halved.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration(halved): %v", err)
	}

	if !(resHalved.A < resFull.A) {
		t.Fatalf("halved satisfaction modifier did not lower A: full=%v halved=%v", resFull.A, resHalved.A)
	}
	if !(resHalved.Net < resFull.Net) {
		t.Fatalf("halved satisfaction modifier did not lower Net: full=%v halved=%v", resFull.Net, resHalved.Net)
	}
}

// TestSetWellbeingModifiers_EmigrationScalesHazard proves the
// EmigrationModifier seam via applyEmigration's per-resident hazard: at
// maximum ambition and a full decline, EmigrationHazard alone already
// saturates at 1.0 (migration_test.go's TestBidirectionalMigration relies
// on exactly this), so the ONLY way to observe the modifier is to drive it
// to 0 — a wellbeing seam returning emigration=0 must suppress every
// departure even though the underlying per-resident hazard is saturated.
func TestSetWellbeingModifiers_EmigrationScalesHazard(t *testing.T) {
	var residents []uint64
	for id := uint64(1); id <= 10; id++ {
		residents = append(residents, id)
	}

	a, ca, _, _ := newAPI(t, validConfig())
	if err := ca.SeedColdRecords(maxAmbitionRecords(residents), "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := a.SetWellbeingModifiers(func() (float64, float64) { return 1.0, 0.0 }); err != nil {
		t.Fatalf("SetWellbeingModifiers: %v", err)
	}
	if err := a.SetTermInputs(TermInputs{
		JobAvailability: 0, ServiceCoverage: 0, Environment: 0, LeisureFit: 0, Safety: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	res, err := a.ApplyMigration(MigrationCommand{
		Month:              0,
		ResidentIDs:        residents,
		HousingVacancy:     100,
		JunctionThroughput: 100,
	})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if !(res.Net < 0) {
		t.Fatalf("fixture invalid: Net = %v, want < 0 (decline scenario)", res.Net)
	}
	if res.Outflow != 0 {
		t.Fatalf("EmigrationModifier=0 did not suppress departures: Outflow = %d, want 0", res.Outflow)
	}
	after := ca.TotalPopulation("corr-attract")
	if after != 10 {
		t.Fatalf("EmigrationModifier=0 case: population = %d, want unchanged at 10", after)
	}
}

// TestSetWellbeingModifiers_CopiedValueRejected proves the SEC-020 copy
// guard: SetWellbeingModifiers on a struct-copied *AttractAPI must fail
// closed and must not mutate the copy's wellbeingModifiers field.
func TestSetWellbeingModifiers_CopiedValueRejected(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	cp := attractCopy(a)
	if err := cp.SetWellbeingModifiers(func() (float64, float64) { return 0.5, 0.5 }); err == nil {
		t.Fatalf("AttractAPI.SetWellbeingModifiers on a struct copy returned nil error")
	}
	if cp.wellbeingModifiers != nil {
		t.Fatalf("AttractAPI.SetWellbeingModifiers mutated a struct copy's wellbeingModifiers field despite returning an error")
	}
}
