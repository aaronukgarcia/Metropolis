package social

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Config is engine.social's runtime configuration — the balance numbers §40
// describes only by direction and mechanism, never by magnitude. Every field
// is data-sourced from data/social.json (GR#15): the values here are
// placeholders pending the M2 balance pass, so rebalancing is a data edit,
// never a code change.
type Config struct {
	// RoughSleepingLocation is the documented location rough sleeping is
	// attributed to (town centre, §40). Data-sourced so a future district
	// model can move it without a code change.
	RoughSleepingLocation string

	// Caseload holds the per-driver caseload-generation rates (decomposed,
	// one rate per category-driver coupling — AC-2).
	Caseload CaseloadConfig

	// HostelCapacity is the hostel-placement capacity (§40/AC-7), registered
	// into engine.services at RegisterServices.
	HostelCapacity int64

	// FosterCapacity is the fostering-placement capacity (§40/AC-9),
	// registered into engine.services at RegisterServices.
	FosterCapacity int64

	// CarersReleasedPerFundingUnit is the informal carers released back to
	// the workforce per unit of disability & carers funding at fixed caseload
	// (§40/AC-8). Placeholder.
	CarersReleasedPerFundingUnit float64

	// InterventionHarmThreshold is the funding-quality below which a
	// child-protection intervention is recorded as harm (§40/AC-6).
	InterventionHarmThreshold float64
}

// CaseloadConfig is the decomposed per-driver caseload rate set. Each rate
// is cases-per-month per unit of its driver, so raising exactly one driver
// moves exactly the categories §40 couples it to (AC-2's isolation check).
type CaseloadConfig struct {
	FamilyPerDeprivation             float64
	FamilyPerCrowdingStress          float64
	FamilyPerFinancialStress         float64
	CrisisFamilyCases                float64 // cases per crisis event
	HomelessnessPerDeprivation       float64
	HomelessnessPerUnemploymentMonth float64
	HomelessnessPerFinancialStress   float64
	DisabilityPerDeprivation         float64
	FosteringPerCrowdingStress       float64
	FosteringPerFinancialStress      float64
	AddictionPerPressure             float64
	UnemploymentCapMonths            float64 // unemployment-duration saturation cap
}

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. Every caseload rate
// must be finite and non-negative, the unemployment cap strictly positive,
// the two capacities non-negative, the carers figure non-negative, and the
// harm threshold in [0,1].
func (c Config) validate(correlationID string) error {
	if c.RoughSleepingLocation == "" {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "roughSleepingLocation", "rule": "required",
		})
	}
	if c.HostelCapacity < 0 {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "hostelCapacity", "value": c.HostelCapacity,
		})
	}
	if c.FosterCapacity < 0 {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "fosterCapacity", "value": c.FosterCapacity,
		})
	}
	if !numFinite(c.CarersReleasedPerFundingUnit) || c.CarersReleasedPerFundingUnit < 0 {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "carersReleasedPerFundingUnit", "value": c.CarersReleasedPerFundingUnit,
		})
	}
	if !numFinite(c.InterventionHarmThreshold) || c.InterventionHarmThreshold < 0 || c.InterventionHarmThreshold > 1 {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "interventionHarmThreshold", "value": c.InterventionHarmThreshold,
		})
	}
	cc := c.Caseload
	rates := []struct {
		name string
		v    float64
	}{
		{"caseload.familyPerDeprivation", cc.FamilyPerDeprivation},
		{"caseload.familyPerCrowdingStress", cc.FamilyPerCrowdingStress},
		{"caseload.familyPerFinancialStress", cc.FamilyPerFinancialStress},
		{"caseload.crisisFamilyCases", cc.CrisisFamilyCases},
		{"caseload.homelessnessPerDeprivation", cc.HomelessnessPerDeprivation},
		{"caseload.homelessnessPerUnemploymentMonth", cc.HomelessnessPerUnemploymentMonth},
		{"caseload.homelessnessPerFinancialStress", cc.HomelessnessPerFinancialStress},
		{"caseload.disabilityPerDeprivation", cc.DisabilityPerDeprivation},
		{"caseload.fosteringPerCrowdingStress", cc.FosteringPerCrowdingStress},
		{"caseload.fosteringPerFinancialStress", cc.FosteringPerFinancialStress},
		{"caseload.addictionPerPressure", cc.AddictionPerPressure},
		{"caseload.unemploymentCapMonths", cc.UnemploymentCapMonths},
	}
	for _, r := range rates {
		if !numFinite(r.v) || r.v < 0 {
			return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
				"field": r.name, "value": r.v,
			})
		}
	}
	if cc.UnemploymentCapMonths <= 0 {
		return errs.New(ErrSocialDataInvalid, correlationID, map[string]any{
			"field": "caseload.unemploymentCapMonths", "rule": "must be strictly positive",
		})
	}
	return nil
}

// numFinite reports whether f is a finite IEEE-754 value (GR#16: NaN/±Inf
// must never cross the configuration boundary into stored state).
func numFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// caseloadCount converts a non-negative float caseload magnitude to an int64
// case count, rounding half away from zero and saturating via foundation/num
// (GR#16: no bare int64(v) that wraps on overflow). NaN and non-positive
// values yield 0; +Inf saturates to math.MaxInt64 (through
// num.ClampInt64FromFloat) so a rate*driver product that overflows to +Inf
// trips the SEC-195 proposal ceiling rather than silently collapsing to zero
// (SEC-199: the finite-guard must not conflate "catastrophic overflow" with
// "legitimately empty" — weakness pattern #6).
func caseloadCount(v float64) int64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	return num.ClampInt64FromFloat(math.Round(v))
}
