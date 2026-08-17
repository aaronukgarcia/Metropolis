package wellbeing

import "github.com/aaronukgarcia/Metropolis/internal/engine/citizens"

// Driver is the canonical name of one §18 wellbeing driver. It is the
// drill-through key: every DriverDelta carries the Driver it was computed
// from, and every physical/mental attribution result exposes each driver by
// its own exported field (AC-1's "driver-decomposed scores; every driver
// drill-through" pattern) — the name is never collapsed into a single
// blended score.
type Driver string

// The fifteen §18 drivers. Physical: AgeCurve, HealthcareAccess, Diet,
// ActiveTravel, PollutionExposure, SportParticipation. Mental: CommuteTime,
// JobAmbitionMismatch, GreenSpace400m, LeisureFit, Crowding, Isolation,
// Noise, FinancialStress, UnemploymentDuration.
const (
	DriverAgeCurve             Driver = "AgeCurve"
	DriverHealthcareAccess     Driver = "HealthcareAccess"
	DriverDiet                 Driver = "Diet"
	DriverActiveTravel         Driver = "ActiveTravel"
	DriverPollutionExposure    Driver = "PollutionExposure"
	DriverSportParticipation   Driver = "SportParticipation"
	DriverCommuteTime          Driver = "CommuteTime"
	DriverJobAmbitionMismatch  Driver = "JobAmbitionMismatch"
	DriverGreenSpace400m       Driver = "GreenSpace400m"
	DriverLeisureFit           Driver = "LeisureFit"
	DriverCrowding             Driver = "Crowding"
	DriverIsolation            Driver = "Isolation"
	DriverNoise                Driver = "Noise"
	DriverFinancialStress      Driver = "FinancialStress"
	DriverUnemploymentDuration Driver = "UnemploymentDuration"
)

// DriverDelta is one driver's named, independently-computed contribution to
// a track (AC-1/AC-2/AC-3). Each delta is a pure function of ITS OWN input
// plus the month and the data-loaded weights — never of another driver's
// input (AC-3 isolation), and never of a post-hoc split of a blended score.
type DriverDelta struct {
	// Driver is the canonical driver name (drill-through).
	Driver Driver
	// Delta is the signed contribution in track points (positive = better).
	Delta float64
	// Input is the raw input value this delta was computed from, so a
	// drill-through can show "why" not just "how much".
	Input float64
	// Confidence is 1.0 when the upstream data was present, and 0.0 when
	// it was missing and the driver degraded to its neutral fallback (AC-14).
	Confidence float64
	// Source names the registered module that supplied Input (e.g.
	// "engine.world", "engine.shopping", "engine.traffic", "engine.season"),
	// or "direct" for inputs passed straight to the pure engine.
	Source string
}

// PhysicalAttribution is the driver-decomposed physical track. The six
// exported driver fields are the §18 physical drivers verbatim (AC-1):
// AgeCurve, HealthcareAccess, Diet, ActiveTravel, PollutionExposure,
// SportParticipation.
type PhysicalAttribution struct {
	// Baseline is the data-sourced neutral offset plus the §9/§18 seasonal
	// health wave plus the deterministic per-citizen/month offset. It is
	// NEVER a hardcoded literal (GR#15).
	Baseline float64
	// SeasonalHealthWave is the §9/§18 winter physical-health wave sourced
	// from engine.season's HealthWaveModifier (already signed ≤ 0). It is
	// folded into Baseline and exposed separately for drill-through (AC-10).
	SeasonalHealthWave float64

	AgeCurve           DriverDelta
	HealthcareAccess   DriverDelta
	Diet               DriverDelta
	ActiveTravel       DriverDelta
	PollutionExposure  DriverDelta
	SportParticipation DriverDelta

	// Total is the conserved sum Baseline + Σ(driver.Delta). For every
	// accepted (bounded) config the additive identity
	// Total == Baseline + Σ(driver.Delta) holds exactly (AC-2): Validate's
	// maxCoefficient bound keeps every coefficient too small to overflow an
	// in-domain driver product, and the one runtime-unbounded driver input —
	// crowding's persons-per-room — is saturated finite by satFinite before it
	// reaches the sum, so the conserved sum never overflows. satFinite on the
	// final sum is a defensive backstop against a future unbounded input, not
	// a clamp that fires for any accepted config.
	Total float64
}

// MentalAttribution is the driver-decomposed mental track. The nine
// exported driver fields are the §18 mental drivers verbatim (AC-1):
// CommuteTime, JobAmbitionMismatch, GreenSpace400m, LeisureFit, Crowding,
// Isolation, Noise, FinancialStress, UnemploymentDuration.
type MentalAttribution struct {
	// Baseline is the data-sourced neutral offset plus the deterministic
	// per-citizen/month offset. No seasonal term (the health wave is
	// physical-only, §9/§18).
	Baseline float64

	CommuteTime          DriverDelta
	JobAmbitionMismatch  DriverDelta
	GreenSpace400m       DriverDelta
	LeisureFit           DriverDelta
	Crowding             DriverDelta
	Isolation            DriverDelta
	Noise                DriverDelta
	FinancialStress      DriverDelta
	UnemploymentDuration DriverDelta

	// Total is the conserved sum Baseline + Σ(driver.Delta). For every
	// accepted (bounded) config the additive identity
	// Total == Baseline + Σ(driver.Delta) holds exactly (AC-2): Validate's
	// maxCoefficient bound keeps every coefficient too small to overflow an
	// in-domain driver product, and the one runtime-unbounded driver input —
	// crowding's persons-per-room — is saturated finite by satFinite before it
	// reaches the sum, so the conserved sum never overflows. satFinite on the
	// final sum is a defensive backstop against a future unbounded input, not
	// a clamp that fires for any accepted config.
	Total float64
}

// TrackAttribution is the full per-citizen attribution result (AC-15's
// "byte-identical TrackAttribution"): both tracks, the headline composite,
// and the satisfaction score it was computed with. It is reconstructed on
// demand — never stored as a durable per-citizen field (AC-18).
type TrackAttribution struct {
	CitizenID uint64
	Month     int64

	Physical PhysicalAttribution
	Mental   MentalAttribution

	// Satisfaction is the 0-100 satisfaction score fed to the headline
	// composite, derived from engine.citizens' five satisfaction components
	// (mean) by the gather path.
	Satisfaction float64
	// Wellbeing is the §18 headline composite f(physical, mental,
	// satisfaction).
	Wellbeing float64
}

// DriverInputs is the pure-engine input set: every raw driver value the
// attribution is a deterministic function of. It is what makes each driver
// independently computable (AC-3): perturbing exactly one field here moves
// exactly one driver delta in the result. All fraction inputs are in [0,1]
// unless noted.
type DriverInputs struct {
	// Physical drivers.
	AgeMonths          int64   // AgeCurve
	HealthcareAccess   float64 // [0,1] HealthcareAccess
	FreshFoodShare     float64 // [0,1] Diet
	ActiveTravelShare  float64 // [0,1] ActiveTravel
	PollutionExposure  float64 // [0,1] PollutionExposure
	SportParticipation float64 // [0,1] SportParticipation (physicality × sport venue access)
	SeasonalHealthWave float64 // ≤ 0, engine.season's HealthWaveModifier (physical baseline term)

	// Mental drivers.
	CommuteMinutes       float64                  // CommuteTime (door-to-door, §19.3)
	JobAmbition          float64                  // [0,100] personality axis — JobAmbitionMismatch
	EmploymentState      citizens.EmploymentState // JobAmbitionMismatch
	Sector               citizens.Sector          // JobAmbitionMismatch
	GreenSpace400m       float64                  // [0,1] GreenSpace400m
	LeisureFit           float64                  // [0,1] LeisureFit
	PersonsPerRoom       float64                  // Crowding
	Sociability          float64                  // [0,100] personality axis — Isolation
	CommunityVenueAccess float64                  // [0,1] Isolation
	NoiseExposure        float64                  // [0,1] Noise
	RentBurden           float64                  // [0,∞) rent/income — FinancialStress
	UnemploymentMonths   int64                    // UnemploymentDuration

	// Satisfaction is the 0-100 satisfaction score fed to the headline
	// composite f(physical, mental, satisfaction), derived by the gather
	// path from engine.citizens' five satisfaction components.
	Satisfaction float64
}

// ContextInputs is the pushed per-citizen context the gather path
// (AttributeCitizen) needs beyond what the citizen record and the wired
// sources already carry: the household-derived crowding figure, the
// rent/income pair for the rent-burden threshold, the unemployment
// duration, and the two venue-access figures. It mirrors engine.attract's
// TermInputs pattern for inputs no registered outbound call can compute
// (ASM-6).
type ContextInputs struct {
	PersonsPerRoom           float64 // Crowding (from the citizen's household)
	MonthlyRentMicroPounds   int64   // FinancialStress numerator
	MonthlyIncomeMicroPounds int64   // FinancialStress denominator
	UnemploymentMonths       int64   // UnemploymentDuration
	CommunityVenueAccess     float64 // [0,1] Isolation (social/community venues)
	SportVenueAccess         float64 // [0,1] SportParticipation (sport venues)
	LeisureFit               float64 // [0,1] LeisureFit (from engine.leisure, pushed)
}
