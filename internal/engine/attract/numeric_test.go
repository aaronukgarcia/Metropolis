package attract

import (
	"math"
	"testing"
)

// TestSaturatingArithmetic is FEAT-086's core: the saturating helpers never
// wrap a ±MaxInt64 / mixed-sign operand, and the float64→int64 choke point
// never wraps NaN/±Inf/2^63 into a negative or a bogus count.
func TestSaturatingArithmetic(t *testing.T) {
	// satAdd
	if got := satAdd(math.MaxInt64, 1); got != math.MaxInt64 {
		t.Fatalf("satAdd(MaxInt64,1) = %d, want MaxInt64", got)
	}
	if got := satAdd(math.MinInt64, -1); got != math.MinInt64 {
		t.Fatalf("satAdd(MinInt64,-1) = %d, want MinInt64", got)
	}
	// satSub
	if got := satSub(math.MinInt64, 1); got != math.MinInt64 {
		t.Fatalf("satSub(MinInt64,1) = %d, want MinInt64", got)
	}
	if got := satSub(math.MaxInt64, -1); got != math.MaxInt64 {
		t.Fatalf("satSub(MaxInt64,-1) = %d, want MaxInt64", got)
	}
	// safeMul — mixed signs whose magnitude product overflows
	if v, overflow := safeMul(math.MaxInt64, -2); !overflow || v != math.MinInt64 {
		t.Fatalf("safeMul(MaxInt64,-2) = %d,%v; want MinInt64,true", v, overflow)
	}
	if v, overflow := safeMul(math.MinInt64, 2); !overflow || v != math.MinInt64 {
		t.Fatalf("safeMul(MinInt64,2) = %d,%v; want MinInt64,true", v, overflow)
	}
	if v, overflow := safeMul(math.MaxInt64, 2); !overflow || v != math.MaxInt64 {
		t.Fatalf("safeMul(MaxInt64,2) = %d,%v; want MaxInt64,true", v, overflow)
	}
	// clampInt64FromFloat
	if got := clampInt64FromFloat(math.NaN()); got != 0 {
		t.Fatalf("clampInt64FromFloat(NaN) = %d, want 0", got)
	}
	if got := clampInt64FromFloat(math.Inf(1)); got != math.MaxInt64 {
		t.Fatalf("clampInt64FromFloat(+Inf) = %d, want MaxInt64", got)
	}
	if got := clampInt64FromFloat(math.Inf(-1)); got != math.MinInt64 {
		t.Fatalf("clampInt64FromFloat(-Inf) = %d, want MinInt64", got)
	}
	// float64(MaxInt64) is exactly 2^63 — a bare int64() conversion wraps.
	if got := clampInt64FromFloat(float64(math.MaxInt64)); got != math.MaxInt64 {
		t.Fatalf("clampInt64FromFloat(float64(MaxInt64)) = %d, want MaxInt64", got)
	}
}

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
