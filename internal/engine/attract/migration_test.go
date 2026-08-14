package attract

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// maxAmbitionRecords builds valid cold records at maximum ambition for the
// given ids — the emigration hazard then saturates at 1.0 under any positive
// decline, making the negative-branch assertions deterministic.
func maxAmbitionRecords(ids []uint64) []citizens.ColdRecord {
	out := make([]citizens.ColdRecord, len(ids))
	for i, id := range ids {
		out[i] = mkResident(id, 100)
	}
	return out
}

// TestBidirectionalMigration is AC-4: the monthly net migration g(A −
// A_world) is signed and genuinely bidirectional — a positive gap admits
// citizens (population increases) and a negative gap removes citizens
// (population count actually decreases via CitizensAPI's departure command,
// not just a negative intermediate number).
func TestBidirectionalMigration(t *testing.T) {
	// Positive branch: a large positive gap admits migrants.
	aPos, caPos, _, _ := newAPI(t, validConfig())
	if err := aPos.SetTermInputs(TermInputs{
		JobAvailability:        80,
		ServiceCoverage:        80,
		Environment:            80,
		LeisureFit:             80,
		Safety:                 80,
		MonthlyRentMicroPounds: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs(pos): %v", err)
	}
	resPos, err := aPos.ApplyMigration(MigrationCommand{
		Month:              0,
		HousingVacancy:     100,
		JunctionThroughput: 100,
	})
	if err != nil {
		t.Fatalf("ApplyMigration(pos): %v", err)
	}
	if !(resPos.Net > 0) {
		t.Fatalf("positive-gap migration Net = %v, want > 0", resPos.Net)
	}
	afterPos := caPos.TotalPopulation("corr-attract")
	if afterPos <= 0 {
		t.Fatalf("positive branch did not increase population: %d", afterPos)
	}
	if resPos.Inflow <= 0 || resPos.Outflow != 0 {
		t.Fatalf("positive branch Inflow=%d Outflow=%d, want Inflow>0 Outflow=0", resPos.Inflow, resPos.Outflow)
	}

	// Negative branch: a large negative gap removes residents. Residents are
	// max-ambition so every emigration hazard is 1.0 → the decline is
	// deterministic and the count must drop.
	aNeg, caNeg, _, _ := newAPI(t, validConfig())
	var residents []uint64
	for id := uint64(1); id <= 10; id++ {
		residents = append(residents, id)
	}
	if err := caNeg.SeedColdRecords(maxAmbitionRecords(residents), "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := aNeg.SetTermInputs(TermInputs{
		JobAvailability:        0,
		ServiceCoverage:        0,
		Environment:            0,
		LeisureFit:             0,
		Safety:                 0,
		MonthlyRentMicroPounds: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs(neg): %v", err)
	}
	resNeg, err := aNeg.ApplyMigration(MigrationCommand{
		Month:              0,
		ResidentIDs:        residents,
		HousingVacancy:     100,
		JunctionThroughput: 100,
	})
	if err != nil {
		t.Fatalf("ApplyMigration(neg): %v", err)
	}
	if !(resNeg.Net < 0) {
		t.Fatalf("negative-gap migration Net = %v, want < 0", resNeg.Net)
	}
	afterNeg := caNeg.TotalPopulation("corr-attract")
	if afterNeg >= 10 {
		t.Fatalf("negative branch did not decrease population: before 10, after %d", afterNeg)
	}
	if resNeg.Outflow <= 0 || resNeg.Inflow != 0 {
		t.Fatalf("negative branch Inflow=%d Outflow=%d, want Inflow=0 Outflow>0", resNeg.Inflow, resNeg.Outflow)
	}

	// The two gaps have opposite sign (AC-4's "g()'s output has opposite sign").
	if resPos.Net*resNeg.Net >= 0 {
		t.Fatalf("g() did not reverse sign across the two scenarios: pos=%v neg=%v", resPos.Net, resNeg.Net)
	}
}

// TestPopulationDecline is AC-4's decline check read straight from
// CitizensAPI: after a negative-gap month the reported population count
// decreases, and the decrease equals the reported outflow.
func TestPopulationDecline(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())
	var ids []uint64
	for id := uint64(1); id <= 20; id++ {
		ids = append(ids, id)
	}
	if err := ca.SeedColdRecords(maxAmbitionRecords(ids), "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := a.SetTermInputs(TermInputs{
		JobAvailability:        0,
		ServiceCoverage:        0,
		Environment:            0,
		LeisureFit:             0,
		Safety:                 0,
		MonthlyRentMicroPounds: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	before := ca.TotalPopulation("corr-attract")
	res, err := a.ApplyMigration(MigrationCommand{Month: 0, ResidentIDs: ids, HousingVacancy: 0, JunctionThroughput: 0})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	after := ca.TotalPopulation("corr-attract")
	if after >= before {
		t.Fatalf("population did not decline: before %d after %d", before, after)
	}
	if int64(before-after) != res.Outflow {
		t.Fatalf("population decline %d != reported outflow %d", before-after, res.Outflow)
	}
}

// TestPersonalityWeightedEmigration is AC-6: the per-resident emigration
// hazard is a function of the ambition personality axis — a higher-ambition
// citizen's hazard is strictly greater for an identical decline.
func TestPersonalityWeightedEmigration(t *testing.T) {
	const decline = 0.5
	low := EmigrationHazard(10, decline)
	high := EmigrationHazard(90, decline)
	if !(high > low) {
		t.Fatalf("higher-ambition hazard %v not strictly greater than lower-ambition %v", high, low)
	}
	prev := -1.0
	for ambition := 0.0; ambition <= 100; ambition += 10 {
		h := EmigrationHazard(ambition, decline)
		if h < 0 || h > 1 {
			t.Fatalf("hazard out of [0,1]: ambition %v → %v", ambition, h)
		}
		if h < prev {
			t.Fatalf("hazard not monotone in ambition: %v then %v", prev, h)
		}
		prev = h
	}
	if got := EmigrationHazard(100, 0); got != 0 {
		t.Fatalf("hazard with zero decline = %v, want 0", got)
	}
}

// TestCapacityConstraint is AC-7: a large positive gap with zero housing
// vacancy admits zero migrants — realised immigration is capped by vacancy
// and throughput, not by g() alone.
func TestCapacityConstraint(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())
	if err := a.SetTermInputs(TermInputs{
		JobAvailability:        100,
		ServiceCoverage:        100,
		Environment:            100,
		LeisureFit:             100,
		Safety:                 100,
		MonthlyRentMicroPounds: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}

	res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 0, JunctionThroughput: 1000})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if !(res.Net > 0) {
		t.Fatalf("gap should be positive, Net=%v", res.Net)
	}
	if res.Inflow != 0 {
		t.Fatalf("zero vacancy admitted %d migrants — capacity not enforced", res.Inflow)
	}
	if got := ca.TotalPopulation("corr-attract"); got != 0 {
		t.Fatalf("population = %d, want 0 under zero vacancy", got)
	}

	res2, err := a.ApplyMigration(MigrationCommand{Month: 1, HousingVacancy: 1000, JunctionThroughput: 4})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if res2.Inflow > 4 {
		t.Fatalf("throughput cap not enforced: admitted %d with throughput 4", res2.Inflow)
	}
}

// TestMigrationDeterminism is AC-12's observable half: the same command
// sequence over two independent APIs yields identical results — no
// shared/global RNG, no wall clock.
func TestMigrationDeterminism(t *testing.T) {
	run := func() (int, float64) {
		a, ca, _, _ := newAPI(t, validConfig())
		var ids []uint64
		for id := uint64(1); id <= 30; id++ {
			ids = append(ids, id)
		}
		if err := ca.SeedColdRecords(maxAmbitionRecords(ids), "corr-attract"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		_ = a.SetTermInputs(TermInputs{JobAvailability: 60, ServiceCoverage: 60, Environment: 60, LeisureFit: 60, Safety: 60})
		for m := int64(0); m < 5; m++ {
			if _, err := a.ApplyMigration(MigrationCommand{
				Month:              m,
				ResidentIDs:        ids,
				HousingVacancy:     100,
				JunctionThroughput: 100,
			}); err != nil {
				t.Fatalf("ApplyMigration: %v", err)
			}
		}
		return ca.TotalPopulation("corr-attract"), a.Reputation()
	}
	pop1, rep1 := run()
	pop2, rep2 := run()
	if pop1 != pop2 || rep1 != rep2 {
		t.Fatalf("determinism violated: run1=(pop %d, rep %v) run2=(pop %d, rep %v)", pop1, rep1, pop2, rep2)
	}
}
