package wellbeing

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// This file holds the pure per-driver arithmetic: one function per §18
// driver, each a deterministic function of ITS OWN input (plus the loaded
// weights and, where relevant, the month). No driver reads another driver's
// input, so perturbing one input moves exactly one delta (AC-3 isolation).
// Every weight arrives from data/wellbeing.json (GR#15); the only literals
// here are structural (clamps to the documented [0,1]/[0,100] domains and
// the employment→career-level mapping, a documented placeholder — ASM-5).

// clamp01 clamps a fraction input to [0,1] after validation has rejected
// out-of-domain values. Kept here so every driver applies the same bound.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// --- physical drivers ---------------------------------------------------

// ageCurveDelta interpolates the data age curve at ageYears (linear between
// sorted anchors, clamped to the end anchors outside the range). Older age ⇒
// more negative physical delta (the age accumulator term).
func ageCurveDelta(curve []AgeCurvePoint, ageYears float64) float64 {
	if ageYears <= curve[0].AgeYears {
		return curve[0].Delta
	}
	last := curve[len(curve)-1]
	if ageYears >= last.AgeYears {
		return last.Delta
	}
	for i := 1; i < len(curve); i++ {
		if ageYears <= curve[i].AgeYears {
			lo, hi := curve[i-1], curve[i]
			span := hi.AgeYears - lo.AgeYears
			if span <= 0 {
				return lo.Delta
			}
			t := (ageYears - lo.AgeYears) / span
			return lo.Delta + t*(hi.Delta-lo.Delta)
		}
	}
	return last.Delta
}

// healthcareDelta: non-negative in healthcare access (AC-5).
func healthcareDelta(p PhysicalFile, access float64) float64 {
	return p.HealthcareAccessWeight * clamp01(access)
}

// dietDelta: non-negative in fresh-food share (AC-5).
func dietDelta(p PhysicalFile, freshFoodShare float64) float64 {
	return p.DietWeight * clamp01(freshFoodShare)
}

// activeTravelDelta: non-negative in active-travel mode share (AC-5).
func activeTravelDelta(p PhysicalFile, activeTravelShare float64) float64 {
	return p.ActiveTravelWeight * clamp01(activeTravelShare)
}

// pollutionDelta: non-positive in pollution exposure (AC-5).
func pollutionDelta(p PhysicalFile, exposure float64) float64 {
	return -p.PollutionWeight * clamp01(exposure)
}

// sportDelta: non-negative in sport participation (physicality × sport venue
// access, §18 "sport × physicality") (AC-5).
func sportDelta(p PhysicalFile, participation float64) float64 {
	return p.SportParticipationWeight * clamp01(participation)
}

// --- mental drivers -----------------------------------------------------

// commuteStress maps door-to-door minutes to a unitless stress value: linear
// from (0,0) to (threshold, commuteStressAtThreshold), then linear again to
// (100, commuteStressAt100Minutes). The second slope is steeper (enforced at
// Load), giving the §18 nonlinear past-45-minutes penalty (AC-4).
func commuteStress(m MentalFile, minutes float64) float64 {
	if minutes < 0 {
		minutes = 0
	}
	t := m.CommuteThresholdMinutes
	if minutes <= t {
		return (minutes / t) * m.CommuteStressAtThreshold
	}
	// Linear from (t, stressAtThreshold) to (100, stressAt100).
	const upperMinutes = 100.0
	if minutes >= upperMinutes {
		return m.CommuteStressAt100Minutes
	}
	span := upperMinutes - t
	return m.CommuteStressAtThreshold + ((minutes-t)/span)*(m.CommuteStressAt100Minutes-m.CommuteStressAtThreshold)
}

// commuteDelta: negative in commute time, steeper past the threshold (AC-4).
func commuteDelta(m MentalFile, minutes float64) float64 {
	return -m.CommuteWeight * commuteStress(m, minutes)
}

// jobLevel maps an employment state/sector pair to a 0-1 "career level" the
// citizen's ambition axis is compared against (§18 job-ambition mismatch).
// This mapping is a documented placeholder (ASM-5) — the exact per-sector
// ambition profile is a balance decision, not spec-given.
func jobLevel(state citizens.EmploymentState, sector citizens.Sector) float64 {
	switch state {
	case citizens.EmploymentUnemployed:
		return 0.1
	case citizens.EmploymentRetired:
		return 0.3
	case citizens.EmploymentStudent:
		return 0.5
	case citizens.EmploymentNone:
		return 0.0
	case citizens.EmploymentEmployed:
		switch sector {
		case citizens.SectorPublic:
			return 0.9
		case citizens.SectorTertiary:
			return 0.85
		case citizens.SectorSecondary:
			return 0.6
		case citizens.SectorPrimary:
			return 0.5
		default:
			return 0.5
		}
	case citizens.EmploymentOffMap:
		// Off-map-employed (extcommute pool, §21) citizens have a real,
		// often well-paid job — just not one with a sector recorded here
		// (extcommute.Assign writes SectorNone, ICD §4). Without this
		// explicit case the pre-existing `default: return 0.0` below would
		// silently score them WORSE than EmploymentUnemployed's 0.1,
		// corrupting jobAmbitionMismatchDelta for every dormitory-town
		// resident (docs/planning/icd/engine.citizens-offmap.md §11, REAL
		// SILENT-MISCLASSIFICATION RISK). Per the ICD: treat as the
		// employed-equivalent baseline — the same unknown/off-map-sector
		// 0.5 the EmploymentEmployed switch's own default/SectorPrimary
		// case above uses. Documented ASM-5-class placeholder, same as the
		// rest of this function — not a new balance decision (FEAT-198).
		return 0.5
	default:
		return 0.0
	}
}

// jobAmbitionMismatchDelta: negative in the |ambition − career level| gap
// (symmetric mismatch, documented placeholder). Ambition is a 0-100 axis;
// the gap is normalised to [0,1].
func jobAmbitionMismatchDelta(m MentalFile, ambition float64, state citizens.EmploymentState, sector citizens.Sector) float64 {
	gap := math.Abs(ambition/100.0 - jobLevel(state, sector))
	if gap > 1 {
		gap = 1
	}
	return -m.JobAmbitionMismatchWeight * gap
}

// greenSpaceDelta: non-negative in green space within 400m.
func greenSpaceDelta(m MentalFile, green float64) float64 {
	return m.GreenSpaceWeight * clamp01(green)
}

// leisureFitDelta: non-negative in leisure fit.
func leisureFitDelta(m MentalFile, fit float64) float64 {
	return m.LeisureFitWeight * clamp01(fit)
}

// crowdingDelta: non-increasing in persons/room — zero at or below one
// person per room, increasingly negative as crowding grows (AC-6).
func crowdingDelta(m MentalFile, personsPerRoom float64) float64 {
	if personsPerRoom < 0 {
		personsPerRoom = 0
	}
	stress := personsPerRoom - 1
	if stress < 0 {
		stress = 0
	}
	return -m.CrowdingWeight * stress
}

// isolationDelta: negative in the §18 isolation product (1 − sociability)
// × (1 − community venue access). Both factors are load-bearing (AC-7).
func isolationDelta(m MentalFile, sociability, communityAccess float64) float64 {
	lonely := 1 - clamp01(sociability/100.0)
	noAccess := 1 - clamp01(communityAccess)
	return -m.IsolationWeight * lonely * noAccess
}

// noiseDelta: non-increasing in noise exposure.
func noiseDelta(m MentalFile, noise float64) float64 {
	return -m.NoiseWeight * clamp01(noise)
}

// financialStressDelta: the §18 rent-burden threshold effect — exactly zero
// below the threshold, a full-step penalty at or above it (not a smooth
// penalty starting at £0 of rent) (AC-6).
func financialStressDelta(m MentalFile, rentBurden float64) float64 {
	if rentBurden < 0 {
		rentBurden = 0
	}
	if rentBurden < m.RentBurdenThreshold {
		return 0
	}
	return -m.FinancialStressWeight
}

// unemploymentDelta: non-increasing in unemployment duration, saturating at
// the data cap (AC-6 — a duration curve, not a single employed/unemployed
// boolean).
func unemploymentDelta(m MentalFile, months int64) float64 {
	if months < 0 {
		months = 0
	}
	f := float64(months)
	cap := m.UnemploymentCapMonths
	if f > cap {
		f = cap
	}
	return -m.UnemploymentWeight * (f / cap)
}

// --- headline and downstream modifiers ----------------------------------

// headlineWeight returns the three §18 headline weights (data-sourced).
func headlineWeights(f WellbeingFile) (phys, ment, sat float64) {
	return f.Headline.PhysicalWeight, f.Headline.MentalWeight, f.Headline.SatisfactionWeight
}

// wellbeingScore is the §18 headline composite f(physical, mental,
// satisfaction): a data-weighted linear combination of the two tracks and
// the satisfaction score, each on [0,100]. Exposed publicly as
// (*WellbeingAPI).Wellbeing (AC-8). Each weight×track product is saturated
// finite before the sum, so a finite-but-huge headline weight (or track)
// can never leak ±Inf into the public headline (SEC-093).
func wellbeingScore(f WellbeingFile, physical, mental, satisfaction float64) float64 {
	phys, ment, sat := headlineWeights(f)
	return satFinite(satFinite(phys*physical) + satFinite(ment*mental) + satFinite(sat*satisfaction))
}

// trackMean is the shared (physical+mental)/2 midpoint the four downstream
// modifiers pivot around.
func trackMean(physical, mental float64) float64 {
	return (physical + mental) / 2
}

// deviationProduct is the shared slope×(100 − trackMean) term behind the four
// downstream modifiers, saturated finite: a finite-but-huge slope overflows
// the product, and satFinite chokes it to the sign-appropriate finite
// extreme rather than leaking ±Inf into the public modifier (SEC-093).
func deviationProduct(slope, physical, mental float64) float64 {
	return satFinite(slope * (100 - trackMean(physical, mental)))
}

// mortalityModifier returns the §18 mortality-hazard multiplier: 1.0 at
// perfect health, rising as the tracks worsen (mortality risk up) (AC-9).
func mortalityModifier(f WellbeingFile, physical, mental float64) float64 {
	return satFinite(1 + deviationProduct(f.Modifiers.MortalitySlope, physical, mental))
}

// productivityModifier returns the §18 productivity multiplier: 1.0 at
// perfect health, falling as the tracks worsen (productivity down) (AC-9).
func productivityModifier(f WellbeingFile, physical, mental float64) float64 {
	return satFinite(1 - deviationProduct(f.Modifiers.ProductivitySlope, physical, mental))
}

// satisfactionModifier returns the §18 satisfaction multiplier: 1.0 at
// perfect health, falling as the tracks worsen (satisfaction down) (AC-9).
func satisfactionModifier(f WellbeingFile, physical, mental float64) float64 {
	return satFinite(1 - deviationProduct(f.Modifiers.SatisfactionSlope, physical, mental))
}

// emigrationModifier returns the §18 emigration-probability multiplier: 1.0
// at perfect health, rising as the tracks worsen (emigration risk up)
// (AC-9).
func emigrationModifier(f WellbeingFile, physical, mental float64) float64 {
	return satFinite(1 + deviationProduct(f.Modifiers.EmigrationSlope, physical, mental))
}
