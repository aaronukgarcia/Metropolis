package leisure

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestAccessFactorGuardsNonFinite proves GR#16 for the access-time penalty
// primitive: NaN in never yields NaN out (nor ±Inf), whatever the threshold
// inputs.
func TestAccessFactorGuardsNonFinite(t *testing.T) {
	cases := []struct {
		name                  string
		minutes, free, budget float64
	}{
		{name: "NaN minutes", minutes: math.NaN(), free: 15, budget: 90},
		{name: "NaN free", minutes: 30, free: math.NaN(), budget: 90},
		{name: "NaN budget", minutes: 30, free: 15, budget: math.NaN()},
		{name: "Inf minutes", minutes: math.Inf(1), free: 15, budget: 90},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := accessFactor(tc.minutes, tc.free, tc.budget)
			if !isFiniteTest(f) {
				t.Fatalf("accessFactor(%v, %v, %v) = %v, want finite", tc.minutes, tc.free, tc.budget, f)
			}
		})
	}
}

// TestComputeBudgetGuardsNonFinite proves GR#16 for the weekly-budget
// primitive: a NaN commute (or overtime) never yields a NaN budget field.
func TestComputeBudgetGuardsNonFinite(t *testing.T) {
	cfg := testConfig()
	var w citizens.LeisureWeights
	w[citizens.LeisureSport] = 50

	b := computeBudget(cfg, StageEmployed, math.NaN(), math.NaN(), w)
	for name, f := range map[string]float64{
		"WorkHours":     b.WorkHours,
		"Discretionary": b.Discretionary,
		"LeisureHours":  b.LeisureHours,
		"RestHours":     b.RestHours,
		"CommuteHours":  b.CommuteHours,
		"OvertimeHours": b.OvertimeHours,
		"OvertimeWage":  b.OvertimeWage,
	} {
		if !isFiniteTest(f) {
			t.Fatalf("computeBudget(NaN commute, NaN overtime).%s = %v, want finite", name, f)
		}
	}

	// The poisoned commute/overtime must have been treated as zero.
	if b.CommuteHours != 0 || b.OvertimeHours != 0 {
		t.Fatalf("non-finite commute/overtime must be zeroed, got commute=%v overtime=%v",
			b.CommuteHours, b.OvertimeHours)
	}
}

// isFiniteTest reports whether f is neither NaN nor ±Inf (a local mirror of
// foundation/num.IsFinite so the guard test stands alone from the fix under
// test).
func isFiniteTest(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
