package season

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Curve name constants — the JSON keys inside data/seasonal.json's
// "curves" map. These are schema *names*, not the numeric curve
// *values* GR#15/AC-10 forbid hardcoding — the values themselves are
// read only from the loaded file (see curveValue).
const (
	curvePower        = "electricityWinterPeak"
	curveWater        = "waterSummerPeak"
	curveGas          = "gasSeasonal"
	curveHarvest      = "harvestCalendar"
	curveConstruction = "constructionSpeedMultiplier"
	curveSchoolIntake = "schoolIntakeGate"
	curveLeisureBeach = "leisureBeachWeight"
	curveLeisureIndr  = "leisureIndoorWeight"
	curveHealthWave   = "healthWaveModifier"
)

// requiredCurves is every curve name this package's SeasonAPI needs
// present in data/seasonal.json's "curves" map. Checked explicitly at
// Load time (AC-12) because foundation.data's schema validation has no
// notion of which curve names a particular consumer requires — it only
// checks that whatever curves ARE present are individually well-formed
// (exactly 12 points, no negative multiplier).
var requiredCurves = []string{
	curvePower, curveWater, curveGas, curveHarvest, curveConstruction,
	curveSchoolIntake, curveLeisureBeach, curveLeisureIndr, curveHealthWave,
}

// monthsPerYear is the fixed number of calendar months a
// foundation/data.MonthlyCurve defines (enforced by its own Validate).
// A schema constant, not a seasonal multiplier value.
const monthsPerYear = 12

// schoolIntakeGateThreshold is the "on" threshold for the
// schoolIntakeGate curve's 0.0/1.0 gate values. A schema convention
// (any value at or above this counts as "intake month"), not a
// seasonal multiplier itself.
const schoolIntakeGateThreshold = 0.5

// SeasonAPI is the pure, stateless (after construction) month-index
// curve lookup contract — code.json's "engine.season" inbound
// interface (SeasonAPI, "pure functions of month index from
// seasonal.json"). Every query method takes an absolute month index
// (matching engine.core's Clock.Month(): int64, 0 = world genesis
// month, monotonically increasing) and returns the same value every
// time it is called with the same index — no hidden mutable state, no
// side effects (AC-1, AC-14).
//
// The zero value is not usable; construct via [Load] or [LoadDefault].
// A *SeasonAPI is safe for concurrent use by multiple goroutines: its
// curve map is populated once at construction and never mutated
// afterward (AC-16).
type SeasonAPI struct {
	curves        map[string]data.MonthlyCurve
	correlationID string
}

// Load reads and validates data/seasonal.json from dir (via
// foundation/data.LoadSeasonal) and checks that every curve
// engine.season requires is present, returning a ready-to-query
// *SeasonAPI. correlationID is attached to every error this call (and
// the returned SeasonAPI's query methods) construct (GR#1). Every
// failure is a registry-sourced *errs.E — never a silent
// default-to-1.0 substitution, never a panic (AC-12).
func Load(dir, correlationID string) (*SeasonAPI, error) {
	seasonal, err := data.LoadSeasonal(dir, correlationID)
	if err != nil {
		// MET-E500's registered template has a "{cause}" placeholder
		// (BUG-191, same weakness class as BUG-099's engine.market fix) —
		// populate it from the wrapped error's own text so the rendered
		// message actually names the failure instead of leaving the
		// literal "{cause}" in the operator/log-visible text.
		return nil, errs.Wrap(ErrSeasonalDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	for _, name := range requiredCurves {
		if _, ok := seasonal.Curves[name]; !ok {
			return nil, errs.New(ErrMissingCurve, correlationID, map[string]any{
				"curve": name,
				"dir":   dir,
			})
		}
	}

	if err := validateSchoolIntakeGateShape(seasonal.Curves[curveSchoolIntake], correlationID); err != nil {
		return nil, err
	}

	return &SeasonAPI{curves: seasonal.Curves, correlationID: correlationID}, nil
}

// validateSchoolIntakeGateShape enforces BUG-059's fix: schoolIntakeGate
// must have exactly one calendar month at or above
// schoolIntakeGateThreshold, matching the "exactly one intake month per
// 12-month cycle" contract IsSchoolIntakeMonth's doc comment and
// data/seasonal.json's own curve comment both state in prose (§9/US-4 —
// education's stage-transition gate is a once-per-year boolean trigger,
// not a continuous multiplier a slightly-off value merely mistunes).
// foundation/data.Seasonal.Validate cannot catch this: it validates
// every curve generically (length, non-negativity) with no notion of
// which curve name means "boolean gate" versus "continuous weight".
// This is deliberately the only curve this package shape-validates
// beyond the generic per-curve checks — see ASM-223 for why the other
// seven curves' prose-only shape descriptions (e.g. "winter +15% peak",
// "lumped, not smooth") are not similarly enforced.
func validateSchoolIntakeGateShape(curve data.MonthlyCurve, correlationID string) error {
	qualifying := 0
	for _, m := range curve.Multipliers {
		if m >= schoolIntakeGateThreshold {
			qualifying++
		}
	}
	if qualifying != 1 {
		return errs.New(ErrIntakeGateShapeInvalid, correlationID, map[string]any{
			"curve":      curveSchoolIntake,
			"threshold":  schoolIntakeGateThreshold,
			"qualifying": qualifying,
		})
	}
	return nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*SeasonAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// calendarMonth converts an absolute month index (0 = world genesis,
// matching engine.core's Clock.Month()) into a 0-based calendar-month
// position (0 = January ... 11 = December), per data/seasonal.json's
// documented "meta.monthIndexConvention". Returns ErrNegativeMonthIndex
// for monthIndex < 0 (AC-13) instead of a negative-modulo wraparound or
// an out-of-bounds panic.
func (s *SeasonAPI) calendarMonth(monthIndex int64) (int, error) {
	if monthIndex < 0 {
		return 0, errs.New(ErrNegativeMonthIndex, s.correlationID, map[string]any{
			"monthIndex": monthIndex,
		})
	}
	return int(monthIndex % monthsPerYear), nil
}

// curveValue looks up curveName's multiplier/weight at monthIndex's
// calendar month. Shared by every exported query method below — the
// single place that turns a month index + curve name into a float64,
// so every curve's error handling (negative index, missing curve) is
// identical.
func (s *SeasonAPI) curveValue(curveName string, monthIndex int64) (float64, error) {
	cm, err := s.calendarMonth(monthIndex)
	if err != nil {
		return 0, err
	}
	curve, ok := s.curves[curveName]
	if !ok {
		return 0, errs.New(ErrCurveLookupFailed, s.correlationID, map[string]any{
			"curve": curveName,
		})
	}
	return curve.Multipliers[cm], nil
}

// PowerDemandMultiplier returns the §17.1 electricity seasonal
// multiplier at monthIndex (winter +15% peak; value read from
// data/seasonal.json's "electricityWinterPeak" curve).
func (s *SeasonAPI) PowerDemandMultiplier(monthIndex int64) (float64, error) {
	return s.curveValue(curvePower, monthIndex)
}

// WaterDemandMultiplier returns the §17.1 water seasonal multiplier at
// monthIndex (summer +25% peak; from "waterSummerPeak").
func (s *SeasonAPI) WaterDemandMultiplier(monthIndex int64) (float64, error) {
	return s.curveValue(curveWater, monthIndex)
}

// GasDemandMultiplier returns the §17.1 gas seasonal multiplier at
// monthIndex (x2.2 January, x0.2 July; from "gasSeasonal").
func (s *SeasonAPI) GasDemandMultiplier(monthIndex int64) (float64, error) {
	return s.curveValue(curveGas, monthIndex)
}

// HarvestCalendar returns the §9 harvest-arrival curve at monthIndex —
// a lumped, not smooth, staples-arrival weighting (from
// "harvestCalendar"; per-crop-category detail belongs to
// engine.farming, out of scope here — see the acceptance doc's AC-5 /
// Out of scope notes).
func (s *SeasonAPI) HarvestCalendar(monthIndex int64) (float64, error) {
	return s.curveValue(curveHarvest, monthIndex)
}

// ConstructionSpeedMultiplier returns the §9 construction-speed
// seasonal multiplier at monthIndex (<1.0 = winter slowdown; from
// "constructionSpeedMultiplier").
func (s *SeasonAPI) ConstructionSpeedMultiplier(monthIndex int64) (float64, error) {
	return s.curveValue(curveConstruction, monthIndex)
}

// IsSchoolIntakeMonth reports whether monthIndex is the §9 September
// school-year intake gate — true for exactly one calendar month per
// 12-month cycle. Which calendar month counts as "the gate" is itself
// data-derived (data/seasonal.json's "schoolIntakeGate" curve: 1.0 at
// the gate month, 0.0 elsewhere) rather than a hardcoded "September ==
// month 8" literal in Go (GR#15) — moving the intake month is a data
// edit, never a code change. The "exactly one" half of this contract is
// not just documentation: [Load] rejects (MET-E504, BUG-059) any
// schoolIntakeGate curve with zero or two-or-more months at or above
// schoolIntakeGateThreshold, so a *SeasonAPI returned by Load can never
// silently violate it — see validateSchoolIntakeGateShape.
func (s *SeasonAPI) IsSchoolIntakeMonth(monthIndex int64) (bool, error) {
	v, err := s.curveValue(curveSchoolIntake, monthIndex)
	if err != nil {
		return false, err
	}
	return v >= schoolIntakeGateThreshold, nil
}

// LeisureWeights is the §9/§41 leisure-mix weighting pair [SeasonAPI.
// LeisureMix] returns: Beach (outdoor, summer-weighted) and Indoor
// (winter-weighted). The two are independent weights, not a normalised
// distribution (data/seasonal.json's meta block documents this
// explicitly) — a consuming module combines them however its own
// demand model requires.
type LeisureWeights struct {
	Beach  float64
	Indoor float64
}

// LeisureMix returns the §9/§41 leisure-mix weights at monthIndex (from
// "leisureBeachWeight"/"leisureIndoorWeight").
func (s *SeasonAPI) LeisureMix(monthIndex int64) (LeisureWeights, error) {
	beach, err := s.curveValue(curveLeisureBeach, monthIndex)
	if err != nil {
		return LeisureWeights{}, err
	}
	indoor, err := s.curveValue(curveLeisureIndr, monthIndex)
	if err != nil {
		return LeisureWeights{}, err
	}
	return LeisureWeights{Beach: beach, Indoor: indoor}, nil
}

// HealthWaveModifier returns the §9/§18 minor winter health-wave
// modifier at monthIndex — a small non-positive adjustment to
// physical-health drift. data/seasonal.json's "healthWaveModifier"
// curve stores a non-negative severity (0.0 = no wave, higher = worse)
// because foundation/data.Seasonal.Validate rejects negative
// multipliers for every curve by schema design; this method negates
// the stored severity so callers see the negative adjustment the spec
// describes, rather than requiring every consumer to remember to flip
// the sign itself.
func (s *SeasonAPI) HealthWaveModifier(monthIndex int64) (float64, error) {
	v, err := s.curveValue(curveHealthWave, monthIndex)
	if err != nil {
		return 0, err
	}
	return -v, nil
}
