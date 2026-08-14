package attract

import (
	"math"
	"testing"
)

// The saturating-arithmetic unit tests that used to live here (satAdd/
// satSub/safeMul/clampInt64FromFloat) now live in foundation/num's own
// test suite (FEAT-086 DRY refactor). This file keeps engine.attract's
// end-to-end numeric fuzz test, which exercises ApplyMigration through the
// public API rather than the (now shared) helpers directly.

// TestMigrationNumericFuzzing is FEAT-086's end-to-end form: fuzzed capacity
// and gap inputs to the migration command never wrap a count negative or
// panic — the result is always a non-negative inflow/outflow, and population
// never invents or destroys citizens beyond the reported counts.
func TestMigrationNumericFuzzing(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())
	if err := a.SetTermInputs(TermInputs{JobAvailability: 100, ServiceCoverage: 100, Environment: 100, LeisureFit: 100, Safety: 100}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}

	cases := []MigrationCommand{
		{Month: 0, HousingVacancy: math.MaxInt64, JunctionThroughput: math.MaxInt64},
		{Month: 1, HousingVacancy: 0, JunctionThroughput: math.MaxInt64},
		{Month: 2, HousingVacancy: math.MaxInt64, JunctionThroughput: 0},
	}
	for _, cmd := range cases {
		res, err := a.ApplyMigration(cmd)
		if err != nil {
			t.Fatalf("ApplyMigration(%+v): %v", cmd, err)
		}
		if res.Inflow < 0 || res.Outflow < 0 {
			t.Fatalf("negative Inflow/Outflow from fuzzed command: %+v", res)
		}
	}
	pop := ca.TotalPopulation("corr-attract")
	if pop < 0 {
		t.Fatalf("population wrapped negative: %d", pop)
	}

	// Negative-capacity inputs are rejected, never wrapped.
	if _, err := a.ApplyMigration(MigrationCommand{Month: 3, HousingVacancy: -1}); err == nil {
		t.Fatal("negative vacancy accepted")
	} else {
		isErr(t, err, ErrInvalidCapacity)
	}
	if _, err := a.ApplyMigration(MigrationCommand{Month: 3, JunctionThroughput: -1}); err == nil {
		t.Fatal("negative throughput accepted")
	} else {
		isErr(t, err, ErrInvalidCapacity)
	}
	// Out-of-range / non-finite term inputs are rejected, never clamped silently.
	if err := a.SetTermInputs(TermInputs{JobAvailability: math.Inf(1)}); err == nil {
		t.Fatal("+Inf term accepted")
	} else {
		isErr(t, err, ErrInvalidTermInput)
	}
	if err := a.SetTermInputs(TermInputs{JobAvailability: 150}); err == nil {
		t.Fatal("out-of-range term accepted")
	} else {
		isErr(t, err, ErrInvalidTermInput)
	}
	if err := a.SetTermInputs(TermInputs{MonthlyRentMicroPounds: -1}); err == nil {
		t.Fatal("negative rent accepted")
	} else {
		isErr(t, err, ErrInvalidTermInput)
	}
}
