package wellbeing

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// attribute is the pure, deterministic attribution engine (AC-15): a
// function of (worldSeed, citizenID, month, driver inputs). It computes
// every driver's delta independently from that driver's own input, folds
// the seasonal health wave into the physical baseline, and conserves the
// additive identity Total == Baseline + Σ(driver.Delta) exactly (AC-2).
// It is fully reconstructable on demand — nothing here reads or writes any
// durable per-citizen state (AC-18).
//
// sourceName labels each returned DriverDelta with the registered module
// that supplied its input in the gather path; the pure engine itself marks
// inputs "direct" (the caller passed them explicitly).
func attribute(f WellbeingFile, seed, citizenID uint64, month int64, in DriverInputs, correlationID, sourceName string) (TrackAttribution, error) {
	if err := validateDriverInputs(in, correlationID); err != nil {
		return TrackAttribution{}, err
	}

	jitter := baselineJitter(seed, citizenID, month)
	ageYears := float64(in.AgeMonths) / 12.0

	phys := PhysicalAttribution{
		Baseline:           f.Baseline.Physical + in.SeasonalHealthWave + jitter,
		SeasonalHealthWave: in.SeasonalHealthWave,
		AgeCurve:           dd(DriverAgeCurve, ageCurveDelta(f.Physical.AgeCurve, ageYears), ageYears, sourceName, 1.0),
		HealthcareAccess:   dd(DriverHealthcareAccess, healthcareDelta(f.Physical, in.HealthcareAccess), in.HealthcareAccess, sourceName, 1.0),
		Diet:               dd(DriverDiet, dietDelta(f.Physical, in.FreshFoodShare), in.FreshFoodShare, sourceName, 1.0),
		ActiveTravel:       dd(DriverActiveTravel, activeTravelDelta(f.Physical, in.ActiveTravelShare), in.ActiveTravelShare, sourceName, 1.0),
		PollutionExposure:  dd(DriverPollutionExposure, pollutionDelta(f.Physical, in.PollutionExposure), in.PollutionExposure, sourceName, 1.0),
		SportParticipation: dd(DriverSportParticipation, sportDelta(f.Physical, in.SportParticipation), in.SportParticipation, sourceName, 1.0),
	}
	phys.Total = satFinite(phys.Baseline +
		phys.AgeCurve.Delta + phys.HealthcareAccess.Delta + phys.Diet.Delta +
		phys.ActiveTravel.Delta + phys.PollutionExposure.Delta + phys.SportParticipation.Delta)

	ment := MentalAttribution{
		Baseline:             f.Baseline.Mental + jitter,
		CommuteTime:          dd(DriverCommuteTime, commuteDelta(f.Mental, in.CommuteMinutes), in.CommuteMinutes, sourceName, 1.0),
		JobAmbitionMismatch:  dd(DriverJobAmbitionMismatch, jobAmbitionMismatchDelta(f.Mental, in.JobAmbition, in.EmploymentState, in.Sector), in.JobAmbition, sourceName, 1.0),
		GreenSpace400m:       dd(DriverGreenSpace400m, greenSpaceDelta(f.Mental, in.GreenSpace400m), in.GreenSpace400m, sourceName, 1.0),
		LeisureFit:           dd(DriverLeisureFit, leisureFitDelta(f.Mental, in.LeisureFit), in.LeisureFit, sourceName, 1.0),
		Crowding:             dd(DriverCrowding, crowdingDelta(f.Mental, in.PersonsPerRoom), in.PersonsPerRoom, sourceName, 1.0),
		Isolation:            dd(DriverIsolation, isolationDelta(f.Mental, in.Sociability, in.CommunityVenueAccess), in.Sociability, sourceName, 1.0),
		Noise:                dd(DriverNoise, noiseDelta(f.Mental, in.NoiseExposure), in.NoiseExposure, sourceName, 1.0),
		FinancialStress:      dd(DriverFinancialStress, financialStressDelta(f.Mental, in.RentBurden), in.RentBurden, sourceName, 1.0),
		UnemploymentDuration: dd(DriverUnemploymentDuration, unemploymentDelta(f.Mental, in.UnemploymentMonths), float64(in.UnemploymentMonths), sourceName, 1.0),
	}
	ment.Total = satFinite(ment.Baseline +
		ment.CommuteTime.Delta + ment.JobAmbitionMismatch.Delta + ment.GreenSpace400m.Delta +
		ment.LeisureFit.Delta + ment.Crowding.Delta + ment.Isolation.Delta +
		ment.Noise.Delta + ment.FinancialStress.Delta + ment.UnemploymentDuration.Delta)

	return TrackAttribution{
		CitizenID:    citizenID,
		Month:        month,
		Physical:     phys,
		Mental:       ment,
		Satisfaction: in.Satisfaction,
		Wellbeing:    wellbeingScore(f, phys.Total, ment.Total, in.Satisfaction),
	}, nil
}

// dd builds one DriverDelta (the single constructor, so every delta carries
// its canonical Driver name for drill-through). It also applies the SEC-093
// finite choke: a delta whose computation overflows float64 (e.g. crowding's
// weight × an unbounded persons/room) saturates to the sign-appropriate
// finite extreme rather than leaking ±Inf into the conserved total.
func dd(driver Driver, delta, input float64, source string, confidence float64) DriverDelta {
	return DriverDelta{
		Driver:     driver,
		Delta:      satFinite(delta),
		Input:      input,
		Confidence: confidence,
		Source:     source,
	}
}

// satFinite saturates a non-finite float64 to the sign-appropriate finite
// extreme (SEC-093 "never leak +Inf/NaN from a finite input"): +Inf →
// math.MaxFloat64, -Inf → -math.MaxFloat64, NaN → 0. Finite values pass
// through unchanged. It is the single choke that keeps every driver delta
// (and therefore the conserved Total) finite even when a driver product
// overflows float64. A NaN delta cannot be produced once Validate and
// validateDriverInputs have rejected non-finite weights and inputs; the NaN
// branch is a neutral backstop, never to be mistaken for a real result.
func satFinite(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case math.IsInf(v, 1):
		return math.MaxFloat64
	case math.IsInf(v, -1):
		return -math.MaxFloat64
	default:
		return v
	}
}

// baselineJitter derives a small deterministic per-citizen/month offset from
// the counter-based hash stream (worldSeed, citizenID, month), mirroring
// engine.citizens' hash(worldSeed, id, month)-bound reconstruction (AC-18).
// It makes the baseline a genuine function of (seed, id, month) rather than
// a flat constant, and is bit-identical across calls/platforms (GR#21).
func baselineJitter(seed, citizenID uint64, month int64) float64 {
	s := det.NewStream(seed, citizenID, month, "wellbeing.baseline")
	return float64(s.IntN(7)) - 3.0 // [-3, +3]
}

// validateDriverInputs rejects an out-of-domain input with a registry-sourced
// error (AC-13) rather than silently clamping it into the total: a negative
// commute time, a personality axis outside 0-100, a negative rent burden, or
// a fraction outside [0,1] are all caller errors. A NaN/±Inf float is
// rejected separately (ErrNonFiniteInput, SEC-093) — missing upstream data
// is a GATHER-path concern (AC-14), never a raw-value concern here.
func validateDriverInputs(in DriverInputs, correlationID string) error {
	finites := []struct {
		name string
		v    float64
	}{
		{"healthcareAccess", in.HealthcareAccess},
		{"freshFoodShare", in.FreshFoodShare},
		{"activeTravelShare", in.ActiveTravelShare},
		{"pollutionExposure", in.PollutionExposure},
		{"sportParticipation", in.SportParticipation},
		{"seasonalHealthWave", in.SeasonalHealthWave},
		{"commuteMinutes", in.CommuteMinutes},
		{"jobAmbition", in.JobAmbition},
		{"greenSpace400m", in.GreenSpace400m},
		{"leisureFit", in.LeisureFit},
		{"personsPerRoom", in.PersonsPerRoom},
		{"sociability", in.Sociability},
		{"communityVenueAccess", in.CommunityVenueAccess},
		{"noiseExposure", in.NoiseExposure},
		{"rentBurden", in.RentBurden},
		{"satisfaction", in.Satisfaction},
	}
	for _, f := range finites {
		if !num.IsFinite(f.v) {
			return errs.New(ErrNonFiniteInput, correlationID, map[string]any{"field": f.name, "value": f.v})
		}
	}

	// Out-of-domain values (AC-13's named cases + the fraction domains).
	if in.CommuteMinutes < 0 {
		return invalid(correlationID, "commuteMinutes", in.CommuteMinutes)
	}
	if in.JobAmbition < 0 || in.JobAmbition > 100 {
		return invalid(correlationID, "jobAmbition", in.JobAmbition)
	}
	if in.Sociability < 0 || in.Sociability > 100 {
		return invalid(correlationID, "sociability", in.Sociability)
	}
	if in.RentBurden < 0 {
		return invalid(correlationID, "rentBurden", in.RentBurden)
	}
	if in.Satisfaction < 0 || in.Satisfaction > 100 {
		return invalid(correlationID, "satisfaction", in.Satisfaction)
	}
	if in.AgeMonths < 0 {
		return invalid(correlationID, "ageMonths", float64(in.AgeMonths))
	}
	if in.UnemploymentMonths < 0 {
		return invalid(correlationID, "unemploymentMonths", float64(in.UnemploymentMonths))
	}
	if in.PersonsPerRoom < 0 {
		return invalid(correlationID, "personsPerRoom", in.PersonsPerRoom)
	}
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"healthcareAccess", in.HealthcareAccess},
		{"freshFoodShare", in.FreshFoodShare},
		{"activeTravelShare", in.ActiveTravelShare},
		{"pollutionExposure", in.PollutionExposure},
		{"sportParticipation", in.SportParticipation},
		{"greenSpace400m", in.GreenSpace400m},
		{"leisureFit", in.LeisureFit},
		{"communityVenueAccess", in.CommunityVenueAccess},
		{"noiseExposure", in.NoiseExposure},
	} {
		if f.v < 0 || f.v > 1 {
			return invalid(correlationID, f.name, f.v)
		}
	}
	return nil
}

// invalid builds the single ErrInvalidInput error shape for AC-13.
func invalid(correlationID, field string, value float64) error {
	return errs.New(ErrInvalidInput, correlationID, map[string]any{"field": field, "value": value})
}
