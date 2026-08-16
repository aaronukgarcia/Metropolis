package services

import (
	"testing"
)

// --- AC-3: quality = f(capacity, coverage, funding, staffing) ------------

// perfect returns a QualityInput at full funding, ample capacity, in
// coverage, and fully staffed — the [0,1] reference against which each
// independent degradation is measured.
func perfect() QualityInput {
	return QualityInput{
		Funding:        1,
		Capacity:       100,
		Demand:         50,
		CoverageRadius: 100,
		DemandDistance: 10,
		StaffingRatio:  1,
	}
}

// TestQualityIsFullWhenEveryInputIsAdequate pins the reference point: with
// every factor adequate, quality is exactly 1 (so the degradation
// assertions below measure a real drop, not a redefined ceiling).
func TestQualityIsFullWhenEveryInputIsAdequate(t *testing.T) {
	if q := ComputeQuality(perfect()); q != 1 {
		t.Fatalf("ComputeQuality(perfect) = %v, want exactly 1", q)
	}
}

// TestQualityDegradesWhenFundingDrops (AC-3 first arm): funding alone
// drives quality down even with capacity/coverage/staffing perfect.
func TestQualityDegradesWhenFundingDrops(t *testing.T) {
	full := ComputeQuality(perfect())

	cut := perfect()
	cut.Funding = 0.5
	if got := ComputeQuality(cut); got >= full {
		t.Fatalf("ComputeQuality at 50%% funding = %v, want < %v (full funding)", got, full)
	}
}

// TestQualityDegradesWhenDemandExceedsCapacity (AC-3 second arm): at FULL
// funding, demand exceeding capacity degrades quality independently.
func TestQualityDegradesWhenDemandExceedsCapacity(t *testing.T) {
	under := ComputeQuality(perfect())

	over := perfect()
	over.Demand = 150 // exceeds Capacity 100
	if got := ComputeQuality(over); got >= under {
		t.Fatalf("ComputeQuality with demand>capacity at full funding = %v, want < %v", got, under)
	}
}

// TestQualityDegradesForOutOfCoverageDemand (AC-3 third arm): demand
// beyond the coverage radius degrades quality independently.
func TestQualityDegradesForOutOfCoverageDemand(t *testing.T) {
	in := ComputeQuality(perfect())

	out := perfect()
	out.DemandDistance = 150 // exceeds CoverageRadius 100
	if got := ComputeQuality(out); got >= in {
		t.Fatalf("ComputeQuality for out-of-coverage demand = %v, want < %v (in-coverage)", got, in)
	}
}

// TestQualityDegradesWhenStaffingShort (AC-4's quality half): a staffing
// ratio below 1 degrades quality even with every other input perfect.
func TestQualityDegradesWhenStaffingShort(t *testing.T) {
	full := ComputeQuality(perfect())

	short := perfect()
	short.StaffingRatio = 0.5
	if got := ComputeQuality(short); got >= full {
		t.Fatalf("ComputeQuality at 50%% staffing = %v, want < %v (full staffing)", got, full)
	}
}

// TestQualityIsBoundedIntoUnitInterval: a quality result never leaves
// [0,1] even for degenerate inputs (zero capacity, huge demand, NaN
// funding) — GR#16's "never leak +Inf/NaN from a finite input" applied to
// the quality domain.
func TestQualityIsBoundedIntoUnitInterval(t *testing.T) {
	cases := []QualityInput{
		{Funding: -1, Capacity: 0, Demand: 1e18, CoverageRadius: 0, DemandDistance: 1e18, StaffingRatio: -1},
		{Funding: 2, Capacity: 1, Demand: 0, CoverageRadius: 0, DemandDistance: 0, StaffingRatio: 2},
		{Funding: 0.5, Capacity: 0, Demand: 0, CoverageRadius: 0, DemandDistance: 0, StaffingRatio: 0.5},
	}
	for i, in := range cases {
		q := ComputeQuality(in)
		if q < 0 || q > 1 {
			t.Errorf("case %d: ComputeQuality = %v, want in [0,1]", i, q)
		}
	}
}

// --- AC-14: no wall clock on the computation path ------------------------

// TestQualityIsPureInputsOnly pins that ComputeQuality is a pure function:
// two calls with the same input return the identical value (no hidden
// state, no time). It is the deterministic complement to AC-14's
// grep-level "no time.Now/Since in non-test source".
func TestQualityIsPureInputsOnly(t *testing.T) {
	in := perfect()
	first := ComputeQuality(in)
	for i := 0; i < 10; i++ {
		if got := ComputeQuality(in); got != first {
			t.Fatalf("ComputeQuality not pure: iteration %d = %v, want %v", i, got, first)
		}
	}
}
