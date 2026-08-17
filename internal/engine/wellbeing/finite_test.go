package wellbeing

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the class-covering test matrix for the SEC-093 class
// "a finite weight/slope config can produce a non-finite modifier/headline/
// total" (the r2 reject: modifier slopes, the headline composite, and the
// additive-identity contract all leaked or contradicted their doc). It is
// enumerated over every product-participating coefficient and every output
// seam rather than written as one regression test per demonstrated instance
// (v1.8 rule 2): a new weight/slope/delta added to WellbeingFile is covered
// the moment it lands in coefficientFields, not on the next attack round.

// coefficientFields enumerates every balance coefficient on WellbeingFile
// that participates in a multiplication (a headline weight, an age-curve
// delta, a driver weight, a commute-stress anchor, or a modifier slope).
var coefficientFields = []struct {
	name string
	set  func(c *WellbeingFile, v float64)
}{
	{"headline.physicalWeight", func(c *WellbeingFile, v float64) { c.Headline.PhysicalWeight = v }},
	{"headline.mentalWeight", func(c *WellbeingFile, v float64) { c.Headline.MentalWeight = v }},
	{"headline.satisfactionWeight", func(c *WellbeingFile, v float64) { c.Headline.SatisfactionWeight = v }},
	{"physical.ageCurve[0].delta", func(c *WellbeingFile, v float64) { c.Physical.AgeCurve[0].Delta = v }},
	{"physical.ageCurve[1].delta", func(c *WellbeingFile, v float64) { c.Physical.AgeCurve[1].Delta = v }},
	{"physical.ageCurve[2].delta", func(c *WellbeingFile, v float64) { c.Physical.AgeCurve[2].Delta = v }},
	{"physical.ageCurve[3].delta", func(c *WellbeingFile, v float64) { c.Physical.AgeCurve[3].Delta = v }},
	{"physical.ageCurve[4].delta", func(c *WellbeingFile, v float64) { c.Physical.AgeCurve[4].Delta = v }},
	{"physical.healthcareAccessWeight", func(c *WellbeingFile, v float64) { c.Physical.HealthcareAccessWeight = v }},
	{"physical.dietWeight", func(c *WellbeingFile, v float64) { c.Physical.DietWeight = v }},
	{"physical.activeTravelWeight", func(c *WellbeingFile, v float64) { c.Physical.ActiveTravelWeight = v }},
	{"physical.pollutionWeight", func(c *WellbeingFile, v float64) { c.Physical.PollutionWeight = v }},
	{"physical.sportParticipationWeight", func(c *WellbeingFile, v float64) { c.Physical.SportParticipationWeight = v }},
	{"mental.commuteWeight", func(c *WellbeingFile, v float64) { c.Mental.CommuteWeight = v }},
	{"mental.jobAmbitionMismatchWeight", func(c *WellbeingFile, v float64) { c.Mental.JobAmbitionMismatchWeight = v }},
	{"mental.greenSpaceWeight", func(c *WellbeingFile, v float64) { c.Mental.GreenSpaceWeight = v }},
	{"mental.leisureFitWeight", func(c *WellbeingFile, v float64) { c.Mental.LeisureFitWeight = v }},
	{"mental.crowdingWeight", func(c *WellbeingFile, v float64) { c.Mental.CrowdingWeight = v }},
	{"mental.isolationWeight", func(c *WellbeingFile, v float64) { c.Mental.IsolationWeight = v }},
	{"mental.noiseWeight", func(c *WellbeingFile, v float64) { c.Mental.NoiseWeight = v }},
	{"mental.financialStressWeight", func(c *WellbeingFile, v float64) { c.Mental.FinancialStressWeight = v }},
	{"mental.unemploymentWeight", func(c *WellbeingFile, v float64) { c.Mental.UnemploymentWeight = v }},
	{"mental.commuteStressAtThreshold", func(c *WellbeingFile, v float64) { c.Mental.CommuteStressAtThreshold = v }},
	{"mental.commuteStressAt100Minutes", func(c *WellbeingFile, v float64) { c.Mental.CommuteStressAt100Minutes = v }},
	{"modifiers.mortalitySlope", func(c *WellbeingFile, v float64) { c.Modifiers.MortalitySlope = v }},
	{"modifiers.productivitySlope", func(c *WellbeingFile, v float64) { c.Modifiers.ProductivitySlope = v }},
	{"modifiers.satisfactionSlope", func(c *WellbeingFile, v float64) { c.Modifiers.SatisfactionSlope = v }},
	{"modifiers.emigrationSlope", func(c *WellbeingFile, v float64) { c.Modifiers.EmigrationSlope = v }},
}

// TestOverBoundCoefficientRejectedEveryField is the boundary layer of the
// class fix: every product-participating coefficient at the over-bound
// extreme (MaxFloat64) is REJECTED by New with the registry-sourced
// ErrDataInvalid, so no finite config can reach the arithmetic carrying an
// overflow-capable weight/slope (SEC-093 finding 1 + 2's "New accepts"
// half).
func TestOverBoundCoefficientRejectedEveryField(t *testing.T) {
	for _, f := range coefficientFields {
		t.Run(f.name, func(t *testing.T) {
			cfg := testCfg()
			f.set(&cfg, math.MaxFloat64)
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err == nil {
				t.Fatalf("New accepted %s=MaxFloat64; want ErrDataInvalid (SEC-093 boundary)", f.name)
			} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
				t.Errorf("err = %v, want registry code %s", err, ErrDataInvalid)
			}
		})
	}
}

// TestCoefficientBoundIsInclusive proves the sane-coefficient bound accepts
// the edge value (<= maxCoefficient is inclusive), so a legitimate config at
// the top of the accepted domain is not wrongly rejected. The two commute
// stress anchors carry a cross-field rule (at100 > threshold) that caps the
// valid threshold below maxCoefficient, so they are excluded from the exact-
// edge check (they are still covered by the over-bound rejection above).
func TestCoefficientBoundIsInclusive(t *testing.T) {
	for _, f := range coefficientFields {
		t.Run(f.name, func(t *testing.T) {
			if f.name == "mental.commuteStressAtThreshold" || f.name == "mental.commuteStressAt100Minutes" {
				t.Skip("commute stress anchors carry a cross-field constraint (at100 > threshold)")
			}
			cfg := testCfg()
			f.set(&cfg, maxCoefficient)
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err != nil {
				t.Fatalf("New rejected %s=maxCoefficient (%v), want accepted (inclusive bound)", f.name, err)
			}
		})
	}
}

// extremeConfig pushes every product-participating coefficient to the
// accepted extreme (maxCoefficient) so the arithmetic backstop is exercised
// at the worst accepted-config point, not only at the shipped balance values.
func extremeConfig() WellbeingFile {
	cfg := testCfg()
	cfg.Headline = HeadlineFile{PhysicalWeight: maxCoefficient, MentalWeight: maxCoefficient, SatisfactionWeight: maxCoefficient}
	cfg.Physical.HealthcareAccessWeight = maxCoefficient
	cfg.Physical.DietWeight = maxCoefficient
	cfg.Physical.ActiveTravelWeight = maxCoefficient
	cfg.Physical.PollutionWeight = maxCoefficient
	cfg.Physical.SportParticipationWeight = maxCoefficient
	cfg.Mental.CommuteWeight = maxCoefficient
	cfg.Mental.JobAmbitionMismatchWeight = maxCoefficient
	cfg.Mental.GreenSpaceWeight = maxCoefficient
	cfg.Mental.LeisureFitWeight = maxCoefficient
	cfg.Mental.CrowdingWeight = maxCoefficient
	cfg.Mental.IsolationWeight = maxCoefficient
	cfg.Mental.NoiseWeight = maxCoefficient
	cfg.Mental.FinancialStressWeight = maxCoefficient
	cfg.Mental.UnemploymentWeight = maxCoefficient
	cfg.Modifiers = ModifierFile{
		MortalitySlope: maxCoefficient, ProductivitySlope: maxCoefficient,
		SatisfactionSlope: maxCoefficient, EmigrationSlope: maxCoefficient,
	}
	return cfg
}

// allDriverDeltas enumerates the fifteen driver-delta output seams so a
// finiteness check is a sweep, not a hand-picked list.
func allDriverDeltas(a TrackAttribution) []struct {
	name string
	d    DriverDelta
} {
	return []struct {
		name string
		d    DriverDelta
	}{
		{"physical.AgeCurve", a.Physical.AgeCurve},
		{"physical.HealthcareAccess", a.Physical.HealthcareAccess},
		{"physical.Diet", a.Physical.Diet},
		{"physical.ActiveTravel", a.Physical.ActiveTravel},
		{"physical.PollutionExposure", a.Physical.PollutionExposure},
		{"physical.SportParticipation", a.Physical.SportParticipation},
		{"mental.CommuteTime", a.Mental.CommuteTime},
		{"mental.JobAmbitionMismatch", a.Mental.JobAmbitionMismatch},
		{"mental.GreenSpace400m", a.Mental.GreenSpace400m},
		{"mental.LeisureFit", a.Mental.LeisureFit},
		{"mental.Crowding", a.Mental.Crowding},
		{"mental.Isolation", a.Mental.Isolation},
		{"mental.Noise", a.Mental.Noise},
		{"mental.FinancialStress", a.Mental.FinancialStress},
		{"mental.UnemploymentDuration", a.Mental.UnemploymentDuration},
	}
}

func assertFinite(t *testing.T, name string, v float64) {
	t.Helper()
	if math.IsInf(v, 0) || math.IsNaN(v) {
		t.Errorf("%s = %v, want finite (SEC-093: never leak +Inf/NaN from a finite input)", name, v)
	}
}

// TestAllSeamsFiniteAtExtremeRuntimeInputs is the arithmetic-backstop half of
// the class fix: a config at the accepted extreme, driven by extreme-but-
// finite runtime inputs (unbounded persons-per-room and seasonal wave,
// MaxInt64 ages/durations, an unbounded commute and rent burden), keeps every
// output seam finite — all fifteen deltas, both baselines, both totals, and
// the headline composite.
func TestAllSeamsFiniteAtExtremeRuntimeInputs(t *testing.T) {
	api, err := New(extremeConfig(), 42, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("New(extreme config): %v", err)
	}

	in := DriverInputs{
		AgeMonths:            math.MaxInt64,
		HealthcareAccess:     1,
		FreshFoodShare:       1,
		ActiveTravelShare:    1,
		PollutionExposure:    1,
		SportParticipation:   1,
		SeasonalHealthWave:   math.MaxFloat64,
		CommuteMinutes:       math.MaxFloat64,
		JobAmbition:          0,
		EmploymentState:      citizens.EmploymentUnemployed,
		Sector:               citizens.SectorNone,
		GreenSpace400m:       1,
		LeisureFit:           1,
		PersonsPerRoom:       math.MaxFloat64,
		Sociability:          0,
		CommunityVenueAccess: 0,
		NoiseExposure:        1,
		RentBurden:           math.MaxFloat64,
		UnemploymentMonths:   math.MaxInt64,
		Satisfaction:         100,
	}

	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute(extreme finite inputs): %v", err)
	}

	for _, dd := range allDriverDeltas(attr) {
		assertFinite(t, dd.name+".Delta", dd.d.Delta)
	}
	assertFinite(t, "Physical.Baseline", attr.Physical.Baseline)
	assertFinite(t, "Physical.Total", attr.Physical.Total)
	assertFinite(t, "Mental.Baseline", attr.Mental.Baseline)
	assertFinite(t, "Mental.Total", attr.Mental.Total)
	assertFinite(t, "Wellbeing", attr.Wellbeing)
}

// TestModifierAndHeadlineFiniteForExtremeFiniteArgs exercises the public
// modifier and headline seams with extreme-but-finite track arguments (the
// caller-controlled floats no Validate sees), proving the four
// slope×(100−mean) products and the weight×track headline products saturate
// rather than leak ±Inf (SEC-093 finding 1 + 2's "product overflows" half).
func TestModifierAndHeadlineFiniteForExtremeFiniteArgs(t *testing.T) {
	api := newTestAPI(t)

	extreme := []struct{ physical, mental, satisfaction float64 }{
		{0, 0, 0},
		{100, 100, 100},
		{math.MaxFloat64, math.MaxFloat64, 50},
		{-math.MaxFloat64, -math.MaxFloat64, 50},
		{math.MaxFloat64, -math.MaxFloat64, 50},
		{1e308, 1e308, 50},
		{-1e308, -1e308, 50},
		{50, math.MaxFloat64, 50},
		{-1e308, 100, 50},
	}
	for _, e := range extreme {
		assertFinite(t, "MortalityModifier", api.MortalityModifier(e.physical, e.mental))
		assertFinite(t, "ProductivityModifier", api.ProductivityModifier(e.physical, e.mental))
		assertFinite(t, "SatisfactionModifier", api.SatisfactionModifier(e.physical, e.mental))
		assertFinite(t, "EmigrationModifier", api.EmigrationModifier(e.physical, e.mental))
		assertFinite(t, "Wellbeing", api.Wellbeing(e.physical, e.mental, e.satisfaction))
	}
}

// TestAdditiveIdentityContractAtExtremes pins down how Total behaves at the
// extreme (the r2 finding 3 contract): Total is always finite, and wherever
// the recomputed Baseline + Σ(delta) stays finite it equals Total exactly;
// if the recomputed sum ever overflowed, Total would be the saturated finite
// extreme (the documented backstop), never ±Inf/NaN.
func TestAdditiveIdentityContractAtExtremes(t *testing.T) {
	// Bounded inputs: the identity is exact on both tracks.
	api := newTestAPI(t)
	in := neutralInputs()
	in.CommuteMinutes = 50
	in.PersonsPerRoom = 2
	in.NoiseExposure = 0.5
	attr, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute(bounded): %v", err)
	}
	assertIdentity(t, "physical(bounded)", attr.Physical.Baseline, physicalDeltaList(attr.Physical), attr.Physical.Total)
	assertIdentity(t, "mental(bounded)", attr.Mental.Baseline, mentalDeltaList(attr.Mental), attr.Mental.Total)

	// Extreme accepted config + unbounded persons-per-room: crowding saturates
	// finite and the identity still holds (one saturated delta sums finitely
	// with the bounded baseline and the other deltas).
	eapi, err := New(extremeConfig(), 42, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("New(extreme config): %v", err)
	}
	ex := neutralInputs()
	ex.PersonsPerRoom = math.MaxFloat64
	ex.NoiseExposure = 1
	eattr, err := eapi.Attribute(7, 12, ex)
	if err != nil {
		t.Fatalf("Attribute(crowding saturates): %v", err)
	}
	assertIdentity(t, "mental(crowding saturates)", eattr.Mental.Baseline, mentalDeltaList(eattr.Mental), eattr.Mental.Total)
	assertFinite(t, "Wellbeing(crowding saturates)", eattr.Wellbeing)
}

// assertIdentity encodes the additive-identity contract: Total must be
// finite, and when Baseline + Σ(delta) is finite it must equal Total exactly
// (AC-2); if the recomputed sum overflowed, Total is the saturated finite
// extreme rather than ±Inf/NaN (the documented SEC-093 backstop).
func assertIdentity(t *testing.T, name string, baseline float64, deltas []DriverDelta, total float64) {
	t.Helper()
	assertFinite(t, name+".Total", total)
	sum := baseline
	for _, d := range deltas {
		sum += d.Delta
	}
	if !math.IsInf(sum, 0) && !math.IsNaN(sum) {
		if sum != total {
			t.Errorf("%s additive identity: Baseline + Σ(delta) = %v, want Total %v", name, sum, total)
		}
		return
	}
	if math.IsInf(total, 0) || math.IsNaN(total) {
		t.Errorf("%s Total = %v, want a finite saturated extreme when the naive sum overflows", name, total)
	}
}

func physicalDeltaList(a PhysicalAttribution) []DriverDelta {
	return []DriverDelta{a.AgeCurve, a.HealthcareAccess, a.Diet, a.ActiveTravel, a.PollutionExposure, a.SportParticipation}
}

func mentalDeltaList(a MentalAttribution) []DriverDelta {
	return []DriverDelta{a.CommuteTime, a.JobAmbitionMismatch, a.GreenSpace400m, a.LeisureFit, a.Crowding, a.Isolation, a.Noise, a.FinancialStress, a.UnemploymentDuration}
}
