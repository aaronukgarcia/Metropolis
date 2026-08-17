package crime

import "github.com/aaronukgarcia/Metropolis/internal/foundation/num"

// The §28 generation/deterrence/clearance/prevention decomposition (AC-1,
// AC-4, AC-5). The three policing mechanisms are three SEPARATE, pure
// functions of three independent levers, so "more police = less crime" is
// true for three different, drill-through-inspectable reasons (GR#3's
// single-source-of-truth / independent-terms requirement). The magnitude
// coefficients arrive from crime.json (GR#15); these functions encode only
// the SHAPE.

// clampUnit clamps a driver fraction to [0,1] (GR#16 — a driver is a
// fraction; a non-finite or out-of-range value is coerced, not propagated).
func clampUnit(f float64) float64 {
	if !num.IsFinite(f) {
		return 0
	}
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// DeterrenceFor is the concave deterrence reduction (AC-4): the fraction of
// crime reduced by a given patrol coverage (units per 10k). It is a
// saturating curve deterrence = coverage / (coverage + halfSaturation),
// whose marginal reduction-per-patrol-unit strictly decreases as coverage
// rises — the first cars cut most, diminishing returns thereafter. The
// half-saturation is data-loaded (GR#15); a non-finite coverage reads as
// zero coverage (never NaN/±Inf out).
func DeterrenceFor(coverage, halfSaturation float64) float64 {
	if !num.IsFinite(coverage) || coverage <= 0 || !num.IsFinite(halfSaturation) || halfSaturation <= 0 {
		return 0
	}
	return coverage / (coverage + halfSaturation)
}

// ClearanceFor is the detective-clearance rate (AC-5): the fraction of
// active/known offenders cleared (suppressing persistence). It rises
// linearly in detective capacity up to a data-loaded ceiling — moving
// detectives moves ONLY persistence, never the driver-driven generation.
func ClearanceFor(detectives, ratePerDetective, maxRate float64) float64 {
	if !num.IsFinite(detectives) || detectives <= 0 ||
		!num.IsFinite(ratePerDetective) || ratePerDetective <= 0 ||
		!num.IsFinite(maxRate) || maxRate <= 0 {
		return 0
	}
	r := detectives * ratePerDetective
	if r > maxRate {
		return maxRate
	}
	return r
}

// PreventionFor is the prevention reduction (AC-5): the fraction of NEW
// generation cut by youth/job/lighting/community provision. It is
// concave-in-infrastructure (diminishing returns), and moving prevention
// moves ONLY generation, never the clearance/persistence term.
func PreventionFor(infrastructure, scale float64) float64 {
	if !num.IsFinite(infrastructure) || infrastructure <= 0 || !num.IsFinite(scale) || scale <= 0 {
		return 0
	}
	return infrastructure * scale / (1 + infrastructure*scale)
}

// driverValues computes the eight normalised driver values from a
// DistrictInput. DriverInequality is the genuine neighbour comparison
// (AC-3): the gap between this district's own deprivation and its adjacent
// districts' wealth, expressed on a common "poorness" axis — NOT a synonym
// for this district's own deprivation. DriverSmugglingPressure scales with
// port/harbour throughput against customs funding (AC-2). DriverLowPresence
// is the absence of police presence.
func driverValues(in DistrictInput) [numDrivers]float64 {
	var v [numDrivers]float64
	v[DriverDeprivation] = clampUnit(in.OwnDeprivation)
	// inequality = |ownDeprivation - (1 - neighbourWealth)|: a district that
	// is much poorer than its rich neighbours (own high, neighbour high) has
	// a large gap; a uniformly-poor or uniformly-rich district has a small
	// one. Holding ownDeprivation fixed while varying neighbourWealth moves
	// this term and nothing else (AC-3).
	gap := in.OwnDeprivation - (1 - in.NeighbourWealth)
	if gap < 0 {
		gap = -gap
	}
	v[DriverInequality] = clampUnit(gap)
	v[DriverYouthUnemployment] = clampUnit(in.YouthUnemployment)
	v[DriverBlight] = clampUnit(in.Blight)
	v[DriverLeisureDesert] = clampUnit(in.YouthLeisureDesert)
	v[DriverLowPresence] = clampUnit(1 - in.PolicePresence)
	v[DriverEraWealth] = clampUnit(in.EraWealth)
	v[DriverSmugglingPressure] = clampUnit(in.PortThroughput * (1 - clampUnit(in.CustomsFunding)))
	return v
}

// rawGeneration computes the driver-driven generation rate for one crime
// type, before deterrence/prevention (AC-1's "driver-attributable
// generation term"). It is the product of the data-loaded base rate, a
// population normalisation (per 100k eligible pool), and one
// (1 + elasticity·driver) factor per driver the type structurally responds
// to — so a driver only affects the types that list it (AC-1/AC-2/AC-3).
func rawGeneration(cfg config, t CrimeType, drivers [numDrivers]float64, eligiblePool int64) float64 {
	base := cfg.Generation.BaseRatesPer100kPerMonth[typeJSONKey(t)]
	if eligiblePool <= 0 {
		return 0
	}
	// population normalisation: base rate is per 100k per month.
	scale := float64(eligiblePool) / 100000.0
	factor := 1.0
	for _, d := range typeDrivers[t] {
		e := cfg.Generation.DriverElasticity[driverJSONKey(d)]
		factor *= 1 + e*drivers[d]
	}
	out := base * scale * factor
	if !num.IsFinite(out) {
		return 0
	}
	return out
}

// effectiveGeneration folds the driver-driven generation through the
// deterrence and prevention reductions (the actual new crime added this
// month). Deterrence and prevention both cut generation but through
// different, independently-movable channels (AC-4/AC-5).
func effectiveGeneration(raw, deterrenceReduction, preventionReduction float64) float64 {
	out := raw * (1 - deterrenceReduction) * (1 - preventionReduction)
	if !num.IsFinite(out) || out < 0 {
		return 0
	}
	return out
}
