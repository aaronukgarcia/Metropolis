package wellbeing

import (
	"reflect"
	"testing"
)

// TestReconstructAttributionColdCitizen (AC-18) proves attribution is
// reconstructed on demand, not carried in stored per-citizen state: two
// independently-constructed APIs over the same (seed, citizenID, month,
// inputs) produce a byte-identical TrackAttribution with no intermediate
// carried between calls. This is the pattern that lets a WARM/COLD citizen's
// attribution be recomputed at inspection time without any durable
// per-citizen attribution history.
func TestReconstructAttributionColdCitizen(t *testing.T) {
	in := neutralInputs()
	in.CommuteMinutes = 38
	in.PollutionExposure = 0.45
	in.FreshFoodShare = 0.55

	apiA := newTestAPI(t)
	apiB := newTestAPI(t) // a fresh reconstruction, as if from a cold shard

	a, err := apiA.Attribute(12345, 24, in)
	if err != nil {
		t.Fatalf("apiA.Attribute: %v", err)
	}
	b, err := apiB.Attribute(12345, 24, in)
	if err != nil {
		t.Fatalf("apiB.Attribute: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two reconstructions differ:\n a=%+v\n b=%+v", a, b)
	}
}

// TestAgeDriverAccumulatesOverTicks shows the age driver is a dynamic
// accumulator term, not a flat baseline: as a citizen ages (month advances),
// the physical age-curve delta drifts more negative while every other
// physical driver stays put.
func TestAgeDriverAccumulatesOverTicks(t *testing.T) {
	api := newTestAPI(t)

	young := neutralInputs()
	young.AgeMonths = 30 * 12
	aYoung, err := api.Attribute(7, 30*12, young)
	if err != nil {
		t.Fatalf("young: %v", err)
	}

	old := neutralInputs()
	old.AgeMonths = 90 * 12
	aOld, err := api.Attribute(7, 90*12, old)
	if err != nil {
		t.Fatalf("old: %v", err)
	}

	if aOld.Physical.AgeCurve.Delta >= aYoung.Physical.AgeCurve.Delta {
		t.Errorf("age-curve delta did not decline with age: %v -> %v", aYoung.Physical.AgeCurve.Delta, aOld.Physical.AgeCurve.Delta)
	}
	// Only the age driver moved; the other physical drivers are identical.
	if aYoung.Physical.HealthcareAccess.Delta != aOld.Physical.HealthcareAccess.Delta {
		t.Errorf("HealthcareAccess delta changed with age")
	}
	if aYoung.Physical.Diet.Delta != aOld.Physical.Diet.Delta {
		t.Errorf("Diet delta changed with age")
	}
}
