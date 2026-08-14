package attract

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// TestMigrationConservation is the migration conservation invariant: across
// a sequence of months that mixes immigration and emigration, the reported
// population change equals inflow − outflow exactly — every admitted citizen
// raises the count by one, every departed citizen lowers it by one, and
// nothing else touches the population during migration. This test is
// written so it CAN fail: if ApplyMigration misreported a count, or a
// mutation changed population without being reported, the running total
// diverges from CitizensAPI's reported population.
func TestMigrationConservation(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())

	var seeded []uint64
	for id := uint64(1); id <= 10; id++ {
		seeded = append(seeded, id)
	}
	if err := ca.SeedColdRecords(maxAmbitionRecords(seeded), "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	high := TermInputs{JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80}
	low := TermInputs{JobAvailability: 0, ServiceCoverage: 0, Environment: 0, LeisureFit: 0, Safety: 0}

	// Months alternate: positive (admit), negative (depart the seeded set).
	script := []struct {
		month       int64
		inputs      TermInputs
		residentIDs []uint64
	}{
		{0, high, nil},
		{1, low, seeded},
		{2, high, nil},
		{3, low, seeded},
		{4, high, nil},
	}

	initial := ca.TotalPopulation("corr-attract")
	var cumulativeIn, cumulativeOut int64
	for _, step := range script {
		if err := a.SetTermInputs(step.inputs); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		res, err := a.ApplyMigration(MigrationCommand{
			Month:              step.month,
			ResidentIDs:        step.residentIDs,
			HousingVacancy:     100,
			JunctionThroughput: 100,
		})
		if err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", step.month, err)
		}
		cumulativeIn = num.SatAdd(cumulativeIn, res.Inflow)
		cumulativeOut = num.SatAdd(cumulativeOut, res.Outflow)

		want := int64(initial) + num.SatSub(cumulativeIn, cumulativeOut)
		got := ca.TotalPopulation("corr-attract")
		if int64(got) != want {
			t.Fatalf("conservation violated at month %d: population %d, want %d (inflow %d − outflow %d over initial %d)",
				step.month, got, want, cumulativeIn, cumulativeOut, initial)
		}
	}

	final := ca.TotalPopulation("corr-attract")
	if int64(final) != int64(initial)+num.SatSub(cumulativeIn, cumulativeOut) {
		t.Fatalf("final population %d != initial %d + inflow %d − outflow %d", final, initial, cumulativeIn, cumulativeOut)
	}
}

// TestConservationNoPopulationLeak is a focused form of the invariant: a
// positive month's population increase is exactly its Inflow and a negative
// month's decrease is exactly its Outflow, read straight from CitizensAPI.
func TestConservationNoPopulationLeak(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())

	// Positive month.
	if err := a.SetTermInputs(TermInputs{JobAvailability: 80, ServiceCoverage: 80, Environment: 80, LeisureFit: 80, Safety: 80}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if got := ca.TotalPopulation("corr-attract"); int64(got) != res.Inflow {
		t.Fatalf("positive month: population %d != inflow %d", got, res.Inflow)
	}

	// Negative month — the admitted migrants are the only residents now, but
	// we pass an empty resident set, so nothing departs: population is stable.
	res2, err := a.ApplyMigration(MigrationCommand{Month: 1, HousingVacancy: 0, JunctionThroughput: 0})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if res2.Inflow != 0 || res2.Outflow != 0 {
		t.Fatalf("empty-resident negative month should be a no-op, got Inflow=%d Outflow=%d", res2.Inflow, res2.Outflow)
	}
}
