package attract

import (
	"math"
	"testing"
)

// TestReputationAsymmetry is AC-5: applying equal-magnitude, equal-duration
// positive and negative shocks to the six non-reputation terms from an
// identical steady-state baseline produces a strictly larger-magnitude
// reputation contribution after the NEGATIVE shock. A symmetric EMA would
// produce equal departures and fail this; the FallRate > RiseRate asymmetry
// makes the Detroit trap a mechanic (US-2).
func TestReputationAsymmetry(t *testing.T) {
	cfg := validConfig()
	r := cfg.Reputation

	// Positive shock: anchor at 50, then hold fundamentals at 60 for 5 months.
	var pos reputationState
	pos.advance(50, r.RiseRate, r.FallRate, r.Max)
	for i := 0; i < 5; i++ {
		pos.advance(60, r.RiseRate, r.FallRate, r.Max)
	}

	// Negative shock: same anchor, then hold fundamentals at 40 for 5 months.
	var neg reputationState
	neg.advance(50, r.RiseRate, r.FallRate, r.Max)
	for i := 0; i < 5; i++ {
		neg.advance(40, r.RiseRate, r.FallRate, r.Max)
	}

	if !(pos.value > 0) {
		t.Fatalf("positive shock should yield positive reputation, got %v", pos.value)
	}
	if !(neg.value < 0) {
		t.Fatalf("negative shock should yield negative reputation, got %v", neg.value)
	}
	if math.Abs(neg.value) <= math.Abs(pos.value) {
		t.Fatalf("reputation asymmetry violated: |neg|=%v must strictly exceed |pos|=%v",
			math.Abs(neg.value), math.Abs(pos.value))
	}
}

// TestReputationAsymmetricMigration is AC-5's end-to-end form: the same
// asymmetric momentum driven through ApplyMigration's once-per-month
// advance, with equal-magnitude opposite shocks on the five pushed terms
// (housing stays fixed). The migration itself is capacity/emigrant-capped to
// zero side effects so only reputation moves.
func TestReputationAsymmetricMigration(t *testing.T) {
	steady := TermInputs{
		JobAvailability:        50,
		ServiceCoverage:        50,
		Environment:            50,
		LeisureFit:             50,
		Safety:                 50,
		MonthlyRentMicroPounds: 0,
	}
	up := steady
	up.JobAvailability, up.ServiceCoverage, up.Environment, up.LeisureFit, up.Safety = 70, 70, 70, 70, 70
	down := steady
	down.JobAvailability, down.ServiceCoverage, down.Environment, down.LeisureFit, down.Safety = 30, 30, 30, 30, 30

	run := func(shock TermInputs) float64 {
		a, _, _, _ := newAPI(t, validConfig())
		_ = a.SetTermInputs(steady)
		// Anchor the reputation baseline at month 0, then shock for 5 months.
		// Empty ResidentIDs + zero vacancy/throughput = no migration side effect.
		for m := int64(0); m <= 5; m++ {
			in := steady
			if m > 0 {
				in = shock
			}
			if err := a.SetTermInputs(in); err != nil {
				t.Fatalf("SetTermInputs: %v", err)
			}
			if _, err := a.ApplyMigration(MigrationCommand{Month: m}); err != nil {
				t.Fatalf("ApplyMigration: %v", err)
			}
		}
		return a.Reputation()
	}

	pos := run(up)
	neg := run(down)
	if math.Abs(neg) <= math.Abs(pos) {
		t.Fatalf("asymmetric migration reputation violated: |neg|=%v must exceed |pos|=%v", math.Abs(neg), math.Abs(pos))
	}
}
