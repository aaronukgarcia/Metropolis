package wellbeing

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- SEC-157: config slice aliasing on construction -----------------------

// TestNewDeepCopiesAgeCurve proves New stores a deep copy of the caller's
// age-curve slice, so a post-New mutation of cfg.Physical.AgeCurve cannot
// change the running API's age-curve arithmetic (the "config immutable after
// New" invariant, AC-17/GR#3, and GR#21 determinism). Pre-fix, New stored the
// WellbeingFile by value, which aliased the []AgeCurvePoint backing array and
// let this exact mutation flip the age-100 AgeCurve delta from -35 to -9999.
func TestNewDeepCopiesAgeCurve(t *testing.T) {
	cfg := testCfg()
	api, err := New(cfg, 42, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Age 100 clamps to the last age-curve anchor (testCfg index 4, delta -35),
	// so the AgeCurve driver reads cfg.Physical.AgeCurve[4].Delta directly —
	// no interpolation float in the way.
	in := neutralInputs()
	in.AgeMonths = 100 * 12
	before, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute(before): %v", err)
	}
	if before.Physical.AgeCurve.Delta != -35 {
		t.Fatalf("pre-mutation AgeCurve delta = %v, want -35 (age-100 anchor)", before.Physical.AgeCurve.Delta)
	}

	// Mutate the CALLER's config slice after New. The stored config must not
	// alias it, so Attribute output must stay byte-identical.
	cfg.Physical.AgeCurve[4].Delta = -9999

	after, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute(after): %v", err)
	}

	if !reflect.DeepEqual(before, after) {
		t.Errorf("caller mutation of cfg.Physical.AgeCurve[4].Delta after New changed Attribute output:\n before=%+v\n after =%+v", before, after)
	}
}

// --- SEC-158: a citizen-derived axis left un-re-validated ------------------

// TestPhysicalityAxisRevalidated proves the gather path re-validates the
// physicality personality axis against 0-100 before folding it into the
// sport-participation product. Pre-fix, physicality=200 with venue access 0.5
// produced SportParticipation=(200/100)*0.5=1.0 — in-domain, silently
// accepted, and a full-weight sport delta (10) instead of the maximum valid
// 100's 5.0. Ambition and sociability were already re-validated; this closes
// the third (SEC-139 non-wrap sibling).
func TestPhysicalityAxisRevalidated(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	ctx := neutralContext()
	ctx.SportVenueAccess = 0.5

	// Upper bound out of domain.
	cit := testCitizen()
	cit.Personality[citizens.AxisPhysicality] = 200
	if _, err := api.AttributeCitizen(cit, 0, ctx); err == nil {
		t.Fatalf("SEC-158: AttributeCitizen accepted physicality=200 (got nil error)")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrInvalidInput {
		t.Errorf("SEC-158: err = %v, want *errs.E code %s", err, ErrInvalidInput)
	}

	// Lower bound out of domain (same class, opposite end).
	cit.Personality[citizens.AxisPhysicality] = -1
	if _, err := api.AttributeCitizen(cit, 0, ctx); err == nil {
		t.Fatalf("SEC-158: AttributeCitizen accepted physicality=-1 (got nil error)")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrInvalidInput {
		t.Errorf("SEC-158 lower bound: err = %v, want *errs.E code %s", err, ErrInvalidInput)
	}

	// In-domain physicality 100 is accepted and yields the correct delta
	// (sport weight 10 x participation (100/100)*0.5 = 5.0).
	cit.Personality[citizens.AxisPhysicality] = 100
	attr, err := api.AttributeCitizen(cit, 0, ctx)
	if err != nil {
		t.Fatalf("SEC-158: AttributeCitizen(physicality=100): %v", err)
	}
	if want := 5.0; attr.Physical.SportParticipation.Delta != want {
		t.Errorf("SEC-158: physicality=100 sport delta = %v, want %v", attr.Physical.SportParticipation.Delta, want)
	}
}
