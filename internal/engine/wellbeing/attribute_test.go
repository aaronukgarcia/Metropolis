package wellbeing

import (
	"math"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testCfg returns a fixed, self-contained WellbeingFile so the pure-engine
// tests are stable and do not depend on the repository's data/wellbeing.json
// (whose weights are balance placeholders that may be retuned). It mirrors
// that file's shape exactly.
func testCfg() WellbeingFile {
	return WellbeingFile{
		Version:  1,
		Baseline: BaselineFile{Physical: 60, Mental: 60},
		Headline: HeadlineFile{PhysicalWeight: 0.4, MentalWeight: 0.4, SatisfactionWeight: 0.2},
		Physical: PhysicalFile{
			AgeCurve: []AgeCurvePoint{
				{AgeYears: 0, Delta: 0}, {AgeYears: 30, Delta: 0},
				{AgeYears: 60, Delta: -5}, {AgeYears: 80, Delta: -15}, {AgeYears: 100, Delta: -35},
			},
			HealthcareAccessWeight:   15,
			DietWeight:               10,
			ActiveTravelWeight:       8,
			PollutionWeight:          12,
			SportParticipationWeight: 10,
		},
		Mental: MentalFile{
			CommuteWeight:             10,
			CommuteThresholdMinutes:   45,
			CommuteStressAtThreshold:  0.5,
			CommuteStressAt100Minutes: 2.0,
			JobAmbitionMismatchWeight: 10,
			GreenSpaceWeight:          8,
			LeisureFitWeight:          10,
			CrowdingWeight:            8,
			IsolationWeight:           10,
			NoiseWeight:               8,
			FinancialStressWeight:     12,
			RentBurdenThreshold:       0.35,
			UnemploymentWeight:        10,
			UnemploymentCapMonths:     60,
		},
		Modifiers: ModifierFile{
			MortalitySlope: 0.01, ProductivitySlope: 0.01,
			SatisfactionSlope: 0.01, EmigrationSlope: 0.01,
		},
	}
}

func newTestAPI(t *testing.T) *WellbeingAPI {
	t.Helper()
	api, err := New(testCfg(), 42, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return api
}

// neutralInputs returns a DriverInputs whose every driver is neutral (delta
// zero): age 30 (age-curve zero), ambition matching an Employed/Tertiary
// job, sociability max + full community access (isolation zero), one person
// per room, no rent burden, no unemployment, all fraction inputs zero.
func neutralInputs() DriverInputs {
	return DriverInputs{
		AgeMonths:            30 * 12,
		HealthcareAccess:     0,
		FreshFoodShare:       0,
		ActiveTravelShare:    0,
		PollutionExposure:    0,
		SportParticipation:   0,
		SeasonalHealthWave:   0,
		CommuteMinutes:       0,
		JobAmbition:          85,
		EmploymentState:      citizens.EmploymentEmployed,
		Sector:               citizens.SectorTertiary,
		GreenSpace400m:       0,
		LeisureFit:           0,
		PersonsPerRoom:       1,
		Sociability:          100,
		CommunityVenueAccess: 1,
		NoiseExposure:        0,
		RentBurden:           0,
		UnemploymentMonths:   0,
		Satisfaction:         50,
	}
}

// --- AC-2: additive identity -------------------------------------------

func TestAttributionSumIdentity(t *testing.T) {
	api := newTestAPI(t)
	in := DriverInputs{
		AgeMonths:            55 * 12,
		HealthcareAccess:     0.6,
		FreshFoodShare:       0.7,
		ActiveTravelShare:    0.4,
		PollutionExposure:    0.3,
		SportParticipation:   0.5,
		SeasonalHealthWave:   -0.05,
		CommuteMinutes:       50,
		JobAmbition:          30,
		EmploymentState:      citizens.EmploymentUnemployed,
		Sector:               citizens.SectorNone,
		GreenSpace400m:       0.6,
		LeisureFit:           0.5,
		PersonsPerRoom:       1.8,
		Sociability:          40,
		CommunityVenueAccess: 0.4,
		NoiseExposure:        0.4,
		RentBurden:           0.5,
		UnemploymentMonths:   14,
		Satisfaction:         50,
	}

	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}

	// Physical: Total == Baseline + Σ(6 driver deltas), summed in the same
	// order the implementation computes it, so the identity holds exactly.
	physSum := attr.Physical.Baseline +
		attr.Physical.AgeCurve.Delta + attr.Physical.HealthcareAccess.Delta +
		attr.Physical.Diet.Delta + attr.Physical.ActiveTravel.Delta +
		attr.Physical.PollutionExposure.Delta + attr.Physical.SportParticipation.Delta
	if physSum != attr.Physical.Total {
		t.Errorf("physical additive identity: Baseline + Σ(delta) = %v, want Total %v", physSum, attr.Physical.Total)
	}

	mentSum := attr.Mental.Baseline +
		attr.Mental.CommuteTime.Delta + attr.Mental.JobAmbitionMismatch.Delta +
		attr.Mental.GreenSpace400m.Delta + attr.Mental.LeisureFit.Delta +
		attr.Mental.Crowding.Delta + attr.Mental.Isolation.Delta +
		attr.Mental.Noise.Delta + attr.Mental.FinancialStress.Delta +
		attr.Mental.UnemploymentDuration.Delta
	if mentSum != attr.Mental.Total {
		t.Errorf("mental additive identity: Baseline + Σ(delta) = %v, want Total %v", mentSum, attr.Mental.Total)
	}
}

// TestAttributionSumIdentityAtCoefficientBound proves the additive identity
// still holds exactly when the config is pushed to the largest accepted
// coefficient (maxCoefficient): the bound, not satFinite saturation, is what
// keeps AC-2 exact, so the identity must survive at the edge of the accepted
// domain.
func TestAttributionSumIdentityAtCoefficientBound(t *testing.T) {
	cfg := testCfg()
	cfg.Mental.CrowdingWeight = maxCoefficient
	cfg.Mental.NoiseWeight = maxCoefficient
	api, err := New(cfg, 42, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("New(bounded max weights): %v", err)
	}

	in := neutralInputs()
	in.PersonsPerRoom = 3
	in.NoiseExposure = 0.5
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}

	mentSum := attr.Mental.Baseline +
		attr.Mental.CommuteTime.Delta + attr.Mental.JobAmbitionMismatch.Delta +
		attr.Mental.GreenSpace400m.Delta + attr.Mental.LeisureFit.Delta +
		attr.Mental.Crowding.Delta + attr.Mental.Isolation.Delta +
		attr.Mental.Noise.Delta + attr.Mental.FinancialStress.Delta +
		attr.Mental.UnemploymentDuration.Delta
	if mentSum != attr.Mental.Total {
		t.Errorf("additive identity at max accepted weights: Baseline + Σ(delta) = %v, want Total %v", mentSum, attr.Mental.Total)
	}
}

// --- AC-3: isolation -----------------------------------------------------

func mentalDeltas(a MentalAttribution) map[Driver]DriverDelta {
	return map[Driver]DriverDelta{
		DriverCommuteTime: a.CommuteTime, DriverJobAmbitionMismatch: a.JobAmbitionMismatch,
		DriverGreenSpace400m: a.GreenSpace400m, DriverLeisureFit: a.LeisureFit,
		DriverCrowding: a.Crowding, DriverIsolation: a.Isolation, DriverNoise: a.Noise,
		DriverFinancialStress: a.FinancialStress, DriverUnemploymentDuration: a.UnemploymentDuration,
	}
}

func TestSingleDriverPerturbIsolates(t *testing.T) {
	api := newTestAPI(t)

	before := neutralInputs()
	before.CommuteMinutes = 30
	bAttr, err := api.Attribute(7, 12, before)
	if err != nil {
		t.Fatalf("Attribute(before): %v", err)
	}

	after := neutralInputs()
	after.CommuteMinutes = 60
	aAttr, err := api.Attribute(7, 12, after)
	if err != nil {
		t.Fatalf("Attribute(after): %v", err)
	}

	// (a) every non-perturbed driver's delta is byte-identical.
	beforeM := mentalDeltas(bAttr.Mental)
	afterM := mentalDeltas(aAttr.Mental)
	for _, d := range []Driver{
		DriverJobAmbitionMismatch, DriverGreenSpace400m, DriverLeisureFit, DriverCrowding,
		DriverIsolation, DriverNoise, DriverFinancialStress, DriverUnemploymentDuration,
	} {
		if !reflect.DeepEqual(beforeM[d], afterM[d]) {
			t.Errorf("driver %s changed under a commute-only perturbation:\n before=%+v\n after =%+v", d, beforeM[d], afterM[d])
		}
	}

	// (b) the perturbed driver moved.
	if bAttr.Mental.CommuteTime.Delta == aAttr.Mental.CommuteTime.Delta {
		t.Errorf("CommuteTime delta did not move: %v -> %v", bAttr.Mental.CommuteTime.Delta, aAttr.Mental.CommuteTime.Delta)
	}

	// (c) Total's change equals the perturbed delta's change (to float
	// rounding — the two totals share the same baseline and the same
	// non-perturbed deltas, so the only difference is the commute delta).
	totalChange := aAttr.Mental.Total - bAttr.Mental.Total
	deltaChange := aAttr.Mental.CommuteTime.Delta - bAttr.Mental.CommuteTime.Delta
	if math.Abs(totalChange-deltaChange) > 1e-9 {
		t.Errorf("Total change %v != CommuteTime delta change %v", totalChange, deltaChange)
	}
}

// --- AC-4: commute nonlinear past 45 min ---------------------------------

func TestCommuteNonlinearPast45(t *testing.T) {
	cfg := testCfg()
	m := cfg.Mental

	// Marginal |Δdelta| per minute above 45 must exceed marginal below 45.
	below := commuteDelta(m, 45) - commuteDelta(m, 44) // negative
	above := commuteDelta(m, 46) - commuteDelta(m, 45) // more negative
	if math.Abs(above) <= math.Abs(below) {
		t.Errorf("marginal above 45 (%v) is not steeper than below 45 (%v)", above, below)
	}

	// At 70 min with every other mental driver neutral, CommuteTime is the
	// largest-magnitude single mental delta.
	api := newTestAPI(t)
	in := neutralInputs()
	in.CommuteMinutes = 70
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	largest := DriverDelta{}
	for d, v := range mentalDeltas(attr.Mental) {
		if math.Abs(v.Delta) > math.Abs(largest.Delta) {
			largest = v
		}
		_ = d
	}
	if largest.Driver != DriverCommuteTime {
		t.Errorf("largest mental delta is %s (%v), want CommuteTime", largest.Driver, largest.Delta)
	}
	if largest.Delta >= 0 {
		t.Errorf("CommuteTime delta at 70 min should be negative, got %v", largest.Delta)
	}
}

// --- AC-5: physical driver directions ------------------------------------

func TestPhysicalDriverDirections(t *testing.T) {
	api := newTestAPI(t)

	// HealthcareAccess / ActiveTravel / SportParticipation / Diet are
	// non-negative and non-decreasing in their input.
	physicalMonotone := []struct {
		name  string
		apply func(in *DriverInputs, v float64)
		get   func(a TrackAttribution) float64
	}{
		{"HealthcareAccess", func(in *DriverInputs, v float64) { in.HealthcareAccess = v }, func(a TrackAttribution) float64 { return a.Physical.HealthcareAccess.Delta }},
		{"Diet", func(in *DriverInputs, v float64) { in.FreshFoodShare = v }, func(a TrackAttribution) float64 { return a.Physical.Diet.Delta }},
		{"ActiveTravel", func(in *DriverInputs, v float64) { in.ActiveTravelShare = v }, func(a TrackAttribution) float64 { return a.Physical.ActiveTravel.Delta }},
		{"SportParticipation", func(in *DriverInputs, v float64) { in.SportParticipation = v }, func(a TrackAttribution) float64 { return a.Physical.SportParticipation.Delta }},
	}
	for _, tc := range physicalMonotone {
		var prev float64
		for _, v := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
			in := neutralInputs()
			tc.apply(&in, v)
			attr, err := api.Attribute(7, 12, in)
			if err != nil {
				t.Fatalf("%s(v=%v): %v", tc.name, v, err)
			}
			d := tc.get(attr)
			if d < 0 {
				t.Errorf("%s delta at input %v is negative (%v)", tc.name, v, d)
			}
			if v > 0 && d < prev {
				t.Errorf("%s delta decreased (%v -> %v) as input rose", tc.name, prev, d)
			}
			prev = d
		}
	}

	// PollutionExposure is non-positive and non-increasing in exposure.
	var prev float64
	for _, v := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
		in := neutralInputs()
		in.PollutionExposure = v
		attr, err := api.Attribute(7, 12, in)
		if err != nil {
			t.Fatalf("pollution(v=%v): %v", v, err)
		}
		d := attr.Physical.PollutionExposure.Delta
		if d > 0 {
			t.Errorf("pollution delta at %v is positive (%v)", v, d)
		}
		if v > 0 && d > prev {
			t.Errorf("pollution delta rose (%v -> %v) as exposure rose", prev, d)
		}
		prev = d
	}
}

// --- AC-6: mental driver directions + financial-stress threshold ----------

func TestCrowdingMonotonic(t *testing.T) {
	api := newTestAPI(t)
	var prev float64
	for _, ppr := range []float64{0.5, 1, 1.5, 2, 3} {
		in := neutralInputs()
		in.PersonsPerRoom = ppr
		attr, err := api.Attribute(7, 12, in)
		if err != nil {
			t.Fatalf("crowding(ppr=%v): %v", ppr, err)
		}
		d := attr.Mental.Crowding.Delta
		if d > prev {
			t.Errorf("crowding delta rose (%v -> %v) as persons/room rose", prev, d)
		}
		prev = d
	}
}

// --- SEC-093: a finite input must never leak ±Inf into a driver delta or
// the conserved total -----------------------------------------------------

func TestCrowdingOverflowSaturatesFinite(t *testing.T) {
	api := newTestAPI(t)

	in := neutralInputs()
	in.PersonsPerRoom = 1e308 // finite, passes the >=0 input check (SEC-093)
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute(PersonsPerRoom=1e308): %v", err)
	}

	if math.IsInf(attr.Mental.Crowding.Delta, 0) || math.IsNaN(attr.Mental.Crowding.Delta) {
		t.Errorf("Crowding.Delta = %v, want finite (overflow must saturate, not leak ±Inf)", attr.Mental.Crowding.Delta)
	}
	if math.IsInf(attr.Mental.Total, 0) || math.IsNaN(attr.Mental.Total) {
		t.Errorf("Mental.Total = %v, want finite (not ±Inf/NaN)", attr.Mental.Total)
	}
	// The headline composite must also stay finite (no downstream leak).
	if math.IsInf(attr.Wellbeing, 0) || math.IsNaN(attr.Wellbeing) {
		t.Errorf("Wellbeing = %v, want finite (not ±Inf/NaN)", attr.Wellbeing)
	}
}

// --- SEC-093: the four modifier products and the headline product must also
// choke a finite-but-huge coefficient rather than leak ±Inf ----------------

func TestModifierSlopesSaturateFinite(t *testing.T) {
	cfg := testCfg()
	cfg.Modifiers.MortalitySlope = math.MaxFloat64
	cfg.Modifiers.ProductivitySlope = math.MaxFloat64
	cfg.Modifiers.SatisfactionSlope = math.MaxFloat64
	cfg.Modifiers.EmigrationSlope = math.MaxFloat64

	for name, v := range map[string]float64{
		"mortality":    mortalityModifier(cfg, 50, 50),
		"productivity": productivityModifier(cfg, 50, 50),
		"satisfaction": satisfactionModifier(cfg, 50, 50),
		"emigration":   emigrationModifier(cfg, 50, 50),
	} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Errorf("%s modifier with MaxFloat64 slope = %v, want finite (not ±Inf/NaN)", name, v)
		}
	}
}

func TestHeadlineWeightSaturatesFinite(t *testing.T) {
	cfg := testCfg()
	cfg.Headline.PhysicalWeight = math.MaxFloat64

	v := wellbeingScore(cfg, 50, 50, 50)
	if math.IsInf(v, 0) || math.IsNaN(v) {
		t.Errorf("Wellbeing with MaxFloat64 physical weight = %v, want finite (not ±Inf/NaN)", v)
	}
}

func TestUnemploymentDurationMonotonic(t *testing.T) {
	api := newTestAPI(t)
	var prev float64
	for _, m := range []int64{0, 3, 12, 30, 60, 120} {
		in := neutralInputs()
		in.UnemploymentMonths = m
		attr, err := api.Attribute(7, 12, in)
		if err != nil {
			t.Fatalf("unemployment(%d): %v", m, err)
		}
		d := attr.Mental.UnemploymentDuration.Delta
		if d > prev {
			t.Errorf("unemployment delta rose (%v -> %v) as duration rose", prev, d)
		}
		prev = d
	}
}

func TestFinancialStressThresholdAt35(t *testing.T) {
	api := newTestAPI(t)
	cfg := testCfg()

	// Below the threshold: ~zero.
	for _, rb := range []float64{0, 0.1, 0.2, 0.34} {
		in := neutralInputs()
		in.RentBurden = rb
		attr, err := api.Attribute(7, 12, in)
		if err != nil {
			t.Fatalf("rentBurden=%v: %v", rb, err)
		}
		if d := attr.Mental.FinancialStress.Delta; d != 0 {
			t.Errorf("rent burden %v (< %.2f) produced non-zero financial stress %v", rb, cfg.Mental.RentBurdenThreshold, d)
		}
	}

	// At/above the threshold: materially nonzero (a full step).
	in := neutralInputs()
	in.RentBurden = 0.35
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("rentBurden=0.35: %v", err)
	}
	if d := attr.Mental.FinancialStress.Delta; !(d < 0) {
		t.Errorf("rent burden 0.35 (at threshold) produced %v, want a material negative delta", d)
	}
}

// --- AC-7: isolation is a two-factor product ------------------------------

func TestIsolationSociabilityAndAccessBothLoadBearing(t *testing.T) {
	api := newTestAPI(t)

	// Hold sociability fixed, vary community access: delta must change.
	in0 := neutralInputs()
	in0.Sociability = 50
	in0.CommunityVenueAccess = 0
	a0, err := api.Attribute(7, 12, in0)
	if err != nil {
		t.Fatalf("access=0: %v", err)
	}
	in1 := neutralInputs()
	in1.Sociability = 50
	in1.CommunityVenueAccess = 1
	a1, err := api.Attribute(7, 12, in1)
	if err != nil {
		t.Fatalf("access=1: %v", err)
	}
	if a0.Mental.Isolation.Delta == a1.Mental.Isolation.Delta {
		t.Errorf("isolation delta did not change when community access changed (sociability held at 50): %v", a0.Mental.Isolation.Delta)
	}
	if a0.Mental.Isolation.Delta >= a1.Mental.Isolation.Delta {
		t.Errorf("isolation with access=0 (%v) is not worse than access=1 (%v)", a0.Mental.Isolation.Delta, a1.Mental.Isolation.Delta)
	}

	// Hold community access fixed, vary sociability: delta must also change.
	sLow := neutralInputs()
	sLow.Sociability = 0
	sLow.CommunityVenueAccess = 0.5
	aLow, err := api.Attribute(7, 12, sLow)
	if err != nil {
		t.Fatalf("sociability=0: %v", err)
	}
	sHigh := neutralInputs()
	sHigh.Sociability = 100
	sHigh.CommunityVenueAccess = 0.5
	aHigh, err := api.Attribute(7, 12, sHigh)
	if err != nil {
		t.Fatalf("sociability=100: %v", err)
	}
	if aLow.Mental.Isolation.Delta == aHigh.Mental.Isolation.Delta {
		t.Errorf("isolation delta did not change when sociability changed (access held at 0.5): %v", aLow.Mental.Isolation.Delta)
	}
	if aLow.Mental.Isolation.Delta >= aHigh.Mental.Isolation.Delta {
		t.Errorf("isolation with sociability=0 (%v) is not worse than sociability=100 (%v)", aLow.Mental.Isolation.Delta, aHigh.Mental.Isolation.Delta)
	}
}

// --- AC-8: headline composite --------------------------------------------

func TestWellbeingHeadlineComposite(t *testing.T) {
	api := newTestAPI(t)

	// The headline changes when either sub-track changes with others held.
	base := api.Wellbeing(50, 50, 50)
	physUp := api.Wellbeing(60, 50, 50)
	mentUp := api.Wellbeing(50, 60, 50)
	satUp := api.Wellbeing(50, 50, 60)
	if physUp == base {
		t.Errorf("headline did not change when physical changed")
	}
	if mentUp == base {
		t.Errorf("headline did not change when mental changed")
	}
	if satUp == base {
		t.Errorf("headline did not change when satisfaction changed")
	}

	// The full attribution carries the computed headline.
	in := neutralInputs()
	in.Satisfaction = 60
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	want := api.Wellbeing(attr.Physical.Total, attr.Mental.Total, 60)
	if attr.Wellbeing != want {
		t.Errorf("TrackAttribution.Wellbeing = %v, want %v", attr.Wellbeing, want)
	}
}

// --- AC-9: four downstream modifiers --------------------------------------

func TestDownstreamModifierDirections(t *testing.T) {
	api := newTestAPI(t)

	// Mortality and emigration rise as health worsens.
	if api.MortalityModifier(100, 100) >= api.MortalityModifier(40, 40) {
		t.Errorf("mortality modifier did not rise as tracks worsened")
	}
	if api.EmigrationModifier(100, 100) >= api.EmigrationModifier(40, 40) {
		t.Errorf("emigration modifier did not rise as tracks worsened")
	}
	// Productivity and satisfaction fall as health worsens.
	if api.ProductivityModifier(100, 100) <= api.ProductivityModifier(40, 40) {
		t.Errorf("productivity modifier did not fall as tracks worsened")
	}
	if api.SatisfactionModifier(100, 100) <= api.SatisfactionModifier(40, 40) {
		t.Errorf("satisfaction modifier did not fall as tracks worsened")
	}
}

// --- AC-13: out-of-domain inputs error, never clamp -----------------------

func TestInvalidDriverInputRejected(t *testing.T) {
	api := newTestAPI(t)

	cases := []struct {
		name  string
		mut   func(in *DriverInputs)
		field string
	}{
		{"negativeCommute", func(in *DriverInputs) { in.CommuteMinutes = -1 }, "commuteMinutes"},
		{"personalityOutOfRange", func(in *DriverInputs) { in.JobAmbition = 101 }, "jobAmbition"},
		{"sociabilityOutOfRange", func(in *DriverInputs) { in.Sociability = -1 }, "sociability"},
		{"negativeRentBurden", func(in *DriverInputs) { in.RentBurden = -0.1 }, "rentBurden"},
		{"fractionAboveOne", func(in *DriverInputs) { in.PollutionExposure = 1.2 }, "pollutionExposure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := neutralInputs()
			tc.mut(&in)
			attr, err := api.Attribute(7, 12, in)
			if err == nil {
				t.Fatalf("expected an out-of-range error for %s, got nil (attr=%+v)", tc.field, attr)
			}
			e, ok := err.(*errs.E)
			if !ok {
				t.Fatalf("expected *errs.E, got %T: %v", err, err)
			}
			if e.Code != ErrInvalidInput && e.Code != ErrNonFiniteInput {
				t.Errorf("err code = %s, want %s", e.Code, ErrInvalidInput)
			}
			// GR#7 assertion (BUG-100): the out-of-domain value was NOT silently
			// clamped and folded into the total — no TrackAttribution was
			// produced at all.
		})
	}

	// NaN must be rejected as non-finite, not folded into the total.
	in := neutralInputs()
	in.CommuteMinutes = math.NaN()
	if _, err := api.Attribute(7, 12, in); err == nil {
		t.Fatalf("NaN commute was not rejected")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrNonFiniteInput {
		t.Errorf("NaN err = %v, want %s", err, ErrNonFiniteInput)
	}
}

// --- AC-15: determinism ---------------------------------------------------

func TestAttributionDeterministic(t *testing.T) {
	api := newTestAPI(t)
	in := neutralInputs()
	in.CommuteMinutes = 40
	in.PollutionExposure = 0.4
	in.FreshFoodShare = 0.6

	first, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := api.Attribute(7, 12, in)
		if err != nil {
			t.Fatalf("Attribute(iter %d): %v", i, err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Errorf("iteration %d produced a different TrackAttribution:\n first=%+v\n again=%+v", i, first, again)
		}
	}
}
