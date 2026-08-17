package census

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FileCensus is the census config filename, relative to the resolved data
// directory. It is loaded directly by this package (not registered in
// foundation/data's §24 set) so that engine.census owns its own loader —
// the same module-owned-loader precedent engine.market/engine.season use.
const FileCensus = "census.json"

// Number is one schema-validated numeric parameter in data/census.json.
// Every numeric entry carries its unit and a disclosure comment naming it
// a balance placeholder (AC-16/AC-27): a downstream consumer never has to
// guess whether a figure is years, miles, points, or a fraction.
type Number struct {
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Disclosure string  `json:"disclosure"`
}

// Meta is data/census.json's documentation block (AC-16/AC-27).
type Meta struct {
	Module        string   `json:"module"`
	FeatureKey    string   `json:"featureKey"`
	SpecRefs      []string `json:"specRefs"`
	BalanceRegime string   `json:"balanceRegime"`
}

// BellCurves holds the §18/§45/§46 bell-curve parameters the census uses
// only as observation benchmarks and synthetic-seed parameters — never as
// census-side mutation (ASM-644). Every field is a balance placeholder.
type BellCurves struct {
	LifespanMeanYears            Number `json:"lifespanMeanYears"`
	LifespanSpreadYears          Number `json:"lifespanSpreadYears"`
	RetirementAgeYears           Number `json:"retirementAgeYears"`
	AnnualMileage                Number `json:"annualMileage"`
	CrimeEducationElasticity     Number `json:"crimeEducationElasticity"`
	BlueWhiteCollarBaselineBlue  Number `json:"blueWhiteCollarBaselineBlue"`
	BlueWhiteCollarBaselineWhite Number `json:"blueWhiteCollarBaselineWhite"`
	HappinessWeightPhysical      Number `json:"happinessWeightPhysical"`
	HappinessWeightMental        Number `json:"happinessWeightMental"`
	HappinessWeightSatisfaction  Number `json:"happinessWeightSatisfaction"`
}

// Thresholds holds the thread thresholds (CC lag, regulator alarm levels)
// — every value a balance placeholder (ASM-646).
type Thresholds struct {
	ConsistencyCheckInLagTicks Number `json:"consistencyCheckInLagTicks"`
	CrimeRate                  Number `json:"crimeRate"`
	UnfedFraction              Number `json:"unfedFraction"`
	UneducatedFraction         Number `json:"uneducatedFraction"`
	UneducatedAttainmentFloor  Number `json:"uneducatedAttainmentFloor"`
}

// Config is the census's loaded data-file configuration.
type Config struct {
	Version    int        `json:"version"`
	Comment    string     `json:"$comment"`
	Meta       Meta       `json:"meta"`
	BellCurves BellCurves `json:"bellCurves"`
	Thresholds Thresholds `json:"thresholds"`
}

// safeFloat64 rejects a non-finite float64 with a registry-sourced error
// (GR#16/SEC-093): a NaN/±Inf config value must never be stored where the
// census's aggregation arithmetic would consume it. It is the census-local
// coercion helper every load/serialise boundary funnels through.
func safeFloat64(v float64, field, correlationID string) (float64, error) {
	if !num.IsFinite(v) {
		return 0, errs.New(ErrCensusDataInvalid, correlationID, map[string]any{
			"field": field,
			"value": v,
		})
	}
	return v, nil
}

// safeInt64Months coerces a float64 year count to a whole-month count,
// rejecting non-finite input and the int64 range (GR#16 — never a bare
// int64(...) conversion of a config float that could wrap). Used to derive
// the retirement month (birth month + retirement age in months).
func safeInt64Months(years float64, field, correlationID string) (int64, error) {
	if _, err := safeFloat64(years, field, correlationID); err != nil {
		return 0, err
	}
	return num.SafeInt64(years * monthsPerYear)
}

// monthsPerYear is the fixed calendar-month count (a schema constant).
const monthsPerYear = 12

// validate rejects a schema-invalid Config (AC-22): a missing unit, a
// missing disclosure, a non-finite value, a negative lifespan/spread, an
// out-of-range threshold. No silent default substitution — the malformed
// parameter is named and the load fails.
func (cfg *Config) validate(correlationID string) error {
	bad := func(field string, rule string) error {
		return errs.New(ErrCensusDataInvalid, correlationID, map[string]any{
			"field": field,
			"rule":  rule,
		})
	}

	if cfg.Version <= 0 {
		return bad("version", "must be positive")
	}
	if cfg.Meta.BalanceRegime == "" || cfg.Meta.FeatureKey == "" {
		return bad("meta", "balanceRegime and featureKey are required (AC-16)")
	}

	// Every numeric entry: finite value, non-empty unit, non-empty disclosure.
	numbers := []struct {
		field string
		n     Number
	}{
		{"bellCurves.lifespanMeanYears", cfg.BellCurves.LifespanMeanYears},
		{"bellCurves.lifespanSpreadYears", cfg.BellCurves.LifespanSpreadYears},
		{"bellCurves.retirementAgeYears", cfg.BellCurves.RetirementAgeYears},
		{"bellCurves.annualMileage", cfg.BellCurves.AnnualMileage},
		{"bellCurves.crimeEducationElasticity", cfg.BellCurves.CrimeEducationElasticity},
		{"bellCurves.blueWhiteCollarBaselineBlue", cfg.BellCurves.BlueWhiteCollarBaselineBlue},
		{"bellCurves.blueWhiteCollarBaselineWhite", cfg.BellCurves.BlueWhiteCollarBaselineWhite},
		{"bellCurves.happinessWeightPhysical", cfg.BellCurves.HappinessWeightPhysical},
		{"bellCurves.happinessWeightMental", cfg.BellCurves.HappinessWeightMental},
		{"bellCurves.happinessWeightSatisfaction", cfg.BellCurves.HappinessWeightSatisfaction},
		{"thresholds.consistencyCheckInLagTicks", cfg.Thresholds.ConsistencyCheckInLagTicks},
		{"thresholds.crimeRate", cfg.Thresholds.CrimeRate},
		{"thresholds.unfedFraction", cfg.Thresholds.UnfedFraction},
		{"thresholds.uneducatedFraction", cfg.Thresholds.UneducatedFraction},
		{"thresholds.uneducatedAttainmentFloor", cfg.Thresholds.UneducatedAttainmentFloor},
	}
	for _, e := range numbers {
		if !num.IsFinite(e.n.Value) {
			return bad(e.field, "value must be finite")
		}
		if e.n.Unit == "" {
			return bad(e.field, "unit is required (AC-16/AC-27)")
		}
		if e.n.Disclosure == "" {
			return bad(e.field, "disclosure is required (AC-16/AC-27)")
		}
	}

	// Positive bell-curve magnitudes (a negative lifespan/spread is a
	// data-authoring bug, never a silently-clamped value).
	if cfg.BellCurves.LifespanMeanYears.Value <= 0 {
		return bad("bellCurves.lifespanMeanYears", "must be positive")
	}
	if cfg.BellCurves.LifespanSpreadYears.Value < 0 {
		return bad("bellCurves.lifespanSpreadYears", "must be non-negative")
	}
	if cfg.BellCurves.RetirementAgeYears.Value <= 0 {
		return bad("bellCurves.retirementAgeYears", "must be positive")
	}
	if cfg.BellCurves.AnnualMileage.Value < 0 {
		return bad("bellCurves.annualMileage", "must be non-negative")
	}

	// Happiness weights compose a distribution (§18): each in [0,1] and the
	// three summing to ~1. The blue/white baselines are observation
	// benchmarks only (AC-17's split is emergent), each in [0,1].
	hsum := cfg.BellCurves.HappinessWeightPhysical.Value +
		cfg.BellCurves.HappinessWeightMental.Value +
		cfg.BellCurves.HappinessWeightSatisfaction.Value
	if !approxOne(hsum) {
		return bad("bellCurves.happinessWeight*", "weights must sum to 1.0")
	}

	// Threshold sanity (AC-22: "a threshold outside a sane range").
	if cfg.Thresholds.ConsistencyCheckInLagTicks.Value < 1 {
		return bad("thresholds.consistencyCheckInLagTicks", "must be >= 1 tick")
	}
	if cfg.Thresholds.CrimeRate.Value < 0 || cfg.Thresholds.CrimeRate.Value > 1 {
		return bad("thresholds.crimeRate", "must be in [0,1]")
	}
	if cfg.Thresholds.UnfedFraction.Value < 0 || cfg.Thresholds.UnfedFraction.Value > 1 {
		return bad("thresholds.unfedFraction", "must be in [0,1]")
	}
	if cfg.Thresholds.UneducatedFraction.Value < 0 || cfg.Thresholds.UneducatedFraction.Value > 1 {
		return bad("thresholds.uneducatedFraction", "must be in [0,1]")
	}
	return nil
}

// approxOne reports whether v is within the documented tolerance of 1.0.
func approxOne(v float64) bool {
	const tol = 1e-6
	d := v - 1.0
	if d < 0 {
		d = -d
	}
	return d < tol
}

// RetirementMonths returns the retirement month for a citizen born at
// birthMonth: birth month + data-file retirement age in months (ASM-646).
// The retirement age is a data-file placeholder, never a hardcoded Go
// constant (GR#15). The addition saturates at the int64 extremes
// (num.SatAdd) so an extreme config retirement age plus a source-controlled
// birth month can never wrap into a negative retirement month (SEC-161, the
// SEC-131 unchecked-arithmetic class at this site).
func (cfg *Config) RetirementMonths(birthMonth int64, correlationID string) (int64, error) {
	months, err := safeInt64Months(cfg.BellCurves.RetirementAgeYears.Value, "retirementAgeYears", correlationID)
	if err != nil {
		return 0, err
	}
	return num.SatAdd(birthMonth, months), nil
}

// CheckInLagTicks returns the CC lag threshold as a whole tick count
// (coerced through the safe-int64 boundary, GR#16).
func (cfg *Config) CheckInLagTicks(correlationID string) (int64, error) {
	v, err := safeFloat64(cfg.Thresholds.ConsistencyCheckInLagTicks.Value, "consistencyCheckInLagTicks", correlationID)
	if err != nil {
		return 0, err
	}
	return num.SafeInt64(v)
}

// LoadConfig reads and validates data/census.json from dir, returning the
// parsed Config. Every failure is a registry-sourced *errs.E (AC-22).
func LoadConfig(dir, correlationID string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(filepath.Join(dir, FileCensus))
	if err != nil {
		return cfg, errs.Wrap(ErrCensusDataInvalid, correlationID, err, map[string]any{
			"path":  filepath.Join(dir, FileCensus),
			"cause": err.Error(),
		})
	}
	// DisallowUnknownFields rejects an unrecognised parameter key (AC-22):
	// a data-authoring typo must surface at load time, never be silently
	// ignored by encoding/json's default struct unmarshal.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(ErrCensusDataInvalid, correlationID, err, map[string]any{
			"path":  filepath.Join(dir, FileCensus),
			"cause": err.Error(),
		})
	}
	if err := cfg.validate(correlationID); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadDefaultConfig resolves data/'s directory via foundation/data and
// loads data/census.json — the convenience entry point for tests and
// callers that don't already have a resolved data directory in hand.
func LoadDefaultConfig(correlationID string) (Config, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return Config{}, err
	}
	return LoadConfig(dir, correlationID)
}
