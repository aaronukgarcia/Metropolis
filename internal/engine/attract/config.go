package attract

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Schema bounds on the float64 config inputs (FEAT-086 — the float64 path).
// They are NOT balance numbers; they exist so a finite-but-absurd config
// (MigrationRate = 1e308, a reputation Max of 1e308, an A_world of 1e308)
// can never drive the migration float64 math to +Inf/NaN, which would
// otherwise escape as Net=+Inf with err==nil. A million is orders of
// magnitude beyond this project's 100M-citizen ceiling yet keeps every
// product/sum comfortably finite, and the int64 saturating path bounds the
// result on the way to a citizen count.
const (
	maxMigrationRate       = 1e6
	maxReputationMagnitude = 1e6
	minWorldScore          = -1e6
	maxWorldScore          = 1e6
)

// validateWorldScore rejects a non-finite or out-of-range A_world baseline
// with a registry-sourced error. It is the single choke point for the world
// score, called BOTH at construction (Config.validate) and on every runtime
// read (ApplyMigration), so a stateful/dynamic WorldPool that was finite at
// construction and turns NaN/±Inf/absurd later can never inject a non-finite
// value into the migration math (defect #2).
func validateWorldScore(aworld float64, correlationID string) error {
	if !isFinite(aworld) || aworld < minWorldScore || aworld > maxWorldScore {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "aWorld",
			"value": aworld,
		})
	}
	return nil
}

// ReputationConfig is the §11 asymmetric-momentum parameter set (the
// Detroit-trap mechanic, US-2/AC-5): RiseRate and FallRate are the
// per-month convergence rates of the reputation momentum toward a positive
// vs negative deviation of the six-term fundamentals from their baseline.
// The asymmetry is structural: FallRate must strictly exceed RiseRate, so
// a falling city's reputation repels faster than a rising city's attracts
// — "cities rising attract beyond fundamentals; cities falling repel
// beyond fundamentals". Max is the |reputation| clamp.
type ReputationConfig struct {
	RiseRate float64
	FallRate float64
	Max      float64
}

// Config is engine.attract's runtime configuration, loaded from data
// (GR#15) — the seven weights, the §4 world-pool seam, the migration-rate
// coefficient, and the reputation-momentum parameters. No field carries a
// literal default in this package's source; every value arrives through
// [New] or [ParseConfig].
type Config struct {
	Weights       Weights
	World         WorldPool
	MigrationRate float64
	Reputation    ReputationConfig
}

// validate checks the whole Config, in field order: weights, the world-pool
// seam (a missing A_world is ErrWorldPoolMissing, AC-11), the migration
// rate, and the reputation parameters (including the strict asymmetry).
// It returns nil for a valid Config.
func (c Config) validate(correlationID string) error {
	if err := c.Weights.validate(correlationID); err != nil {
		return err
	}
	if c.World == nil {
		return errs.New(ErrWorldPoolMissing, correlationID, nil)
	}
	if err := validateWorldScore(c.World.AWorld(), correlationID); err != nil {
		return err
	}
	if !isFinite(c.MigrationRate) || c.MigrationRate < 0 || c.MigrationRate > maxMigrationRate {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "migrationRate",
			"value": c.MigrationRate,
		})
	}
	r := c.Reputation
	if !isFinite(r.RiseRate) || r.RiseRate < 0 || r.RiseRate > 1 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "reputation.riseRate",
			"value": r.RiseRate,
		})
	}
	if !isFinite(r.FallRate) || r.FallRate < 0 || r.FallRate > 1 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "reputation.fallRate",
			"value": r.FallRate,
		})
	}
	// US-2: a symmetric (or reversed) momentum would make the Detroit trap
	// a marketing description rather than a mechanic. Reject it outright.
	if !(r.FallRate > r.RiseRate) {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "reputation.asymmetry",
			"value": "fallRate must exceed riseRate",
		})
	}
	if !isFinite(r.Max) || r.Max <= 0 || r.Max > maxReputationMagnitude {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "reputation.max",
			"value": r.Max,
		})
	}
	return nil
}

// configJSON is ParseConfig's wire shape — the one "data-loading path" where
// the weights' and A_world's numeric values legitimately live (AC-2/AC-8).
// AWorld is a *float64 so that a JSON document that omits "aWorld" is
// distinguishable from one that sets it to a genuine 0: the former yields a
// nil WorldPool (rejected as ErrWorldPoolMissing, AC-11), the latter a
// StaticWorldPool{0}.
type configJSON struct {
	Weights struct {
		JobAvailability      float64 `json:"jobAvailability"`
		HousingAffordability float64 `json:"housingAffordability"`
		ServiceCoverage      float64 `json:"serviceCoverage"`
		Environment          float64 `json:"environment"`
		LeisureFit           float64 `json:"leisureFit"`
		Safety               float64 `json:"safety"`
		Reputation           float64 `json:"reputation"`
	} `json:"weights"`
	AWorld        *float64 `json:"aWorld"`
	MigrationRate float64  `json:"migrationRate"`
	Reputation    struct {
		RiseRate float64 `json:"riseRate"`
		FallRate float64 `json:"fallRate"`
		Max      float64 `json:"max"`
	} `json:"reputation"`
}

// ParseConfig decodes and validates a JSON config document into a Config.
// It is the data-loading path: JSON syntax errors and schema-validation
// failures are both surfaced as registry-sourced errors (ErrConfigInvalid /
// ErrInvalidWeights / ErrWorldPoolMissing), never a silent zero-valued
// substitution (AC-10/AC-11).
func ParseConfig(data []byte, correlationID string) (Config, error) {
	var raw configJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, errs.Wrap(ErrConfigInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	cfg := Config{
		Weights: Weights{
			JobAvailability:      raw.Weights.JobAvailability,
			HousingAffordability: raw.Weights.HousingAffordability,
			ServiceCoverage:      raw.Weights.ServiceCoverage,
			Environment:          raw.Weights.Environment,
			LeisureFit:           raw.Weights.LeisureFit,
			Safety:               raw.Weights.Safety,
			Reputation:           raw.Weights.Reputation,
		},
		MigrationRate: raw.MigrationRate,
		Reputation: ReputationConfig{
			RiseRate: raw.Reputation.RiseRate,
			FallRate: raw.Reputation.FallRate,
			Max:      raw.Reputation.Max,
		},
	}
	if raw.AWorld != nil {
		cfg.World = NewStaticWorldPool(*raw.AWorld)
	}
	// A missing aWorld leaves cfg.World nil, which validate rejects with
	// ErrWorldPoolMissing (AC-11) — never a silent A_world = 0.
	if err := cfg.validate(correlationID); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
