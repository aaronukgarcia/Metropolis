package wellbeing

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- SEC-139: satisfaction components must not wrap the int32 running sum ---

// TestSatisfactionScoreInt32SumWraps reproduces the SEC-139 wrap: five int32
// components {1e9, 1e9, 1e9, 1e9, 294967546} overflow the int32 running sum
// to exactly 250, so the mean is 50.0 — a valid 0-100 value that silently
// passed validation pre-fix. Post-fix the components are re-validated against
// their 0-100 contract and the caller gets a registry-sourced ErrInvalidInput
// instead of a silently-fabricated neutral satisfaction.
func TestSatisfactionScoreInt32SumWraps(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	cit := testCitizen()
	cit.Satisfaction = citizens.Satisfaction{1e9, 1e9, 1e9, 1e9, 294967546}

	_, err := api.AttributeCitizen(cit, 0, neutralContext())
	if err == nil {
		t.Fatalf("SEC-139: AttributeCitizen accepted an int32-wrapped satisfaction sum as a valid mean (got nil error)")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("SEC-139: want *errs.E, got %T: %v", err, err)
	}
	if e.Code != ErrInvalidInput {
		t.Errorf("SEC-139: error code = %s, want %s", e.Code, ErrInvalidInput)
	}
}

// TestSatisfactionComponentOutOfRangeRejected closes the non-wrap sibling of
// SEC-139: a single component outside 0-100 ({200, 0, 0, 0, 0}) averages to
// an in-domain 40.0 even after widening the accumulator, so component-level
// re-validation is what rejects it — the mean-level check alone would accept
// it as a valid neutral-ish satisfaction.
func TestSatisfactionComponentOutOfRangeRejected(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	cit := testCitizen()
	cit.Satisfaction = citizens.Satisfaction{200, 0, 0, 0, 0}

	if _, err := api.AttributeCitizen(cit, 0, neutralContext()); err == nil {
		t.Fatalf("SEC-139 sibling: AttributeCitizen accepted an out-of-domain satisfaction component that averages in-domain")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrInvalidInput {
		t.Errorf("SEC-139 sibling: err = %v, want %s", err, ErrInvalidInput)
	}
}

// TestSatisfactionScoreValidMeanUnchanged is the positive control: a valid
// citizen (all components 0-100) still yields the exact arithmetic mean, and
// widening the accumulator did not change the value.
func TestSatisfactionScoreValidMeanUnchanged(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	cit := testCitizen() // Satisfaction{50, 50, 50, 50, 50}
	attr, err := api.AttributeCitizen(cit, 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(valid citizen): %v", err)
	}
	if attr.Satisfaction != 50.0 {
		t.Errorf("valid-citizen satisfaction mean = %v, want 50.0", attr.Satisfaction)
	}
}
