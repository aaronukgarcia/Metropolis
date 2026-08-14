package citizens

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// Gompertz-Makeham monthly mortality (§5.1/§5.2, AC-11). The hazard is
//
//	h(age) = C + A * exp(B * ageYears)
//
// — C is the Makeham age-independent (accident/background) term, and
// A·exp(B·age) is the Gompertz exponential aging term — then modified by
// health band and healthcare access.
//
// The three base parameters below are ACTUARIAL PLACEHOLDERS pending the
// balance pass (GR#15's balance regime: a directional model with
// documented placeholders, not a fabricated precise curve). They are NOT
// cold-pass measured parameters: the cold-pass PARAMETERS (AC-8) are
// sample-derived; these are the actuarial curve's fixed shape, analogous
// to engine.season's curve-shape constants. Every number here is a rate
// per MONTH (the hazard is drawn once per citizen per month).
const (
	// makehamAgeIndependent is the C term: the monthly background
	// (non-aging) mortality rate.
	makehamAgeIndependent = 1e-5
	// gompertzScale is the A term: the Gompertz scale at age 0.
	gompertzScale = 1e-6
	// gompertzRate is the B term: the per-YEAR exponential rate, so the
	// hazard roughly doubles every ln(2)/B years.
	gompertzRate = 0.075

	// healthBandStep is the multiplicative penalty per band BELOW the
	// healthiest band (worse health ⇒ higher hazard).
	healthBandStep = 0.25

	// accessStep is the multiplicative relief at full (100) healthcare
	// access (better access ⇒ lower hazard).
	accessStep = 0.5
)

// MortalityHazard returns the per-month probability of death for a citizen
// of the given age (months), health band, and healthcare access (0-100),
// from the Gompertz-Makeham curve. The result is clamped to [0, 1].
// Directional guarantees (AC-11): for fixed health/access the hazard
// increases with age; for fixed age it decreases as the health band
// improves and as healthcare access improves.
func MortalityHazard(ageMonths int64, health HealthBand, healthcareAccess uint8) float64 {
	ageYears := float64(ageMonths) / 12.0
	base := makehamAgeIndependent + gompertzScale*math.Exp(gompertzRate*ageYears)

	// Health modifier: a band BELOW MaxHealthBand multiplies the hazard up
	// (worse health ⇒ higher hazard). Band is coerced into range so a
	// stray out-of-range value degrades to the worst-band factor rather
	// than producing a negative multiplier (GR#16: never trust a stored
	// field's declared range).
	band := health
	if band > MaxHealthBand {
		band = MaxHealthBand
	}
	healthFactor := 1 + float64(MaxHealthBand-band)*healthBandStep

	// Access modifier: 0 access ⇒ no relief, 100 access ⇒ (1 - accessStep)
	// relief. Coerced into [0, 100].
	access := int(healthcareAccess)
	if access < 0 {
		access = 0
	} else if access > 100 {
		access = 100
	}
	accessFactor := 1 - float64(access)/100.0*accessStep

	h := base * healthFactor * accessFactor
	if h < 0 {
		return 0
	}
	if h > 1 {
		return 1
	}
	return h
}

// MortalityDeath is the per-person monthly death decision (AC-11/AC-15):
// the citizen dies iff a draw from the independent counter-based hash
// stream hash(worldSeed, id, month, "mortality") falls below the
// Gompertz-Makeham hazard at their age. It is a pure function of
// (seed, id, month, age, health, access) — no shared RNG object, no wall
// clock. The cold pass supplies the explicit age (month - birthMonth).
func MortalityDeath(seed uint64, id uint64, month, ageMonths int64, health HealthBand, access uint8) bool {
	stream := det.NewStream(seed, id, month, "mortality")
	return stream.Float64() < MortalityHazard(ageMonths, health, access)
}
