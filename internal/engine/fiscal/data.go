package fiscal

import (
	"encoding/json"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §54 leaves unquantified lives here, never as a Go literal.
const fileName = "fiscal.json"

// Config is engine.fiscal's runtime balance configuration — the municipality
// funding curves and the childcare subsidy parameters §54 describes only by
// direction and mechanism, never by magnitude. Every field is data-sourced
// from data/fiscal.json (GR#15): the values are placeholders pending the M2
// balance pass, so rebalancing is a data edit, never a code change.
type Config struct {
	Version      int                `json:"version"`
	Municipality MunicipalityConfig `json:"municipality"`
	Childcare    ChildcareConfig    `json:"childcare"`
	// Meta is data/fiscal.json's documentation block (module/spec prose).
	// Declared explicitly — never consumed — because the BUG-281 r2 strict
	// loader rejects undeclared fields and only strips $-prefixed keys at
	// the top level.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// MunicipalityConfig is the §54 "municipality as a modelled department"
// curve set: the planning & administration funding target, and the four
// outputs' anchor values at zero and full funding. Each output is a linear
// interpolation between its anchors except corruption, which is a threshold
// shape (zero at/above corruptionThreshold, rising linearly below it).
type MunicipalityConfig struct {
	// FundingTargetPerMonthMicroPounds is the monthly planning &
	// administration funding level that corresponds to a 100% funding
	// fraction. Placeholder (derived — no external anchor).
	FundingTargetPerMonthMicroPounds int64 `json:"fundingTargetPerMonthMicroPounds"`

	// PermitSpeedMultiplier anchors: the multiplier on permit-processing speed
	// at zero funding and at full funding. Higher funding ⇒ faster permits.
	PermitSpeedAtZeroFunding float64 `json:"permitSpeedAtZeroFunding"`
	PermitSpeedAtFullFunding float64 `json:"permitSpeedAtFullFunding"`

	// Build-cost error rate anchors (fraction of project cost): the §54
	// "10–20% over" underfunding outcome and the (≈0) fully-funded outcome.
	// Lower funding ⇒ higher error.
	BuildCostErrorAtZeroFunding float64 `json:"buildCostErrorAtZeroFunding"`
	BuildCostErrorAtFullFunding float64 `json:"buildCostErrorAtFullFunding"`

	// Layout-quality bonus anchors: the probability/coefficient weight on the
	// §52 design-code compounding. Higher funding ⇒ more likely compounding
	// applies by default.
	LayoutBonusAtZeroFunding float64 `json:"layoutBonusAtZeroFunding"`
	LayoutBonusAtFullFunding float64 `json:"layoutBonusAtFullFunding"`

	// Corruption risk: a threshold shape. At or above corruptionThreshold
	// (a funding fraction) risk is zero; below it, risk rises linearly to
	// corruptionMax at zero funding ("only rises meaningfully at the low
	// end", §54).
	CorruptionThreshold float64 `json:"corruptionThreshold"`
	CorruptionMax       float64 `json:"corruptionMax"`
}

// ChildcareConfig is the §54 childcare-subsidy parameter set: the per-place
// subsidy, the second-earner participation each place unlocks, and the
// average second-earner wage those participants earn (the base the income
// tax applies to).
type ChildcareConfig struct {
	// SubsidyPerPlacePerMonthMicroPounds is the gross subsidy cost of one
	// childcare place for a month. Placeholder (derived — no external anchor).
	SubsidyPerPlacePerMonthMicroPounds int64 `json:"subsidyPerPlacePerMonthMicroPounds"`

	// SecondEarnerUpliftPerPlace is the fraction of a second earner (in
	// [0,1]) each subsidised place draws into work. Placeholder.
	SecondEarnerUpliftPerPlace float64 `json:"secondEarnerUpliftPerPlace"`

	// SecondEarnerAvgWagePerMonthMicroPounds is the average monthly wage a
	// newly-participating second earner earns (the income-tax base). Placeholder.
	SecondEarnerAvgWagePerMonthMicroPounds int64 `json:"secondEarnerAvgWagePerMonthMicroPounds"`
}

// Validate implements the foundation/data.Validator contract so the generic
// data.Load runs schema validation immediately after JSON decoding. Every
// failure is a *data.FieldError naming the offending field and rule.
func (c *Config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}

	m := c.Municipality
	if m.FundingTargetPerMonthMicroPounds <= 0 {
		return &data.FieldError{Field: "municipality.fundingTargetPerMonthMicroPounds", Rule: "must be positive"}
	}
	if err := orderedAnchors("permitSpeed", m.PermitSpeedAtZeroFunding, m.PermitSpeedAtFullFunding, true); err != nil {
		return err
	}
	if err := orderedAnchors("buildCostError", m.BuildCostErrorAtZeroFunding, m.BuildCostErrorAtFullFunding, false); err != nil {
		return err
	}
	if !num.IsFinite(m.BuildCostErrorAtZeroFunding) || m.BuildCostErrorAtZeroFunding < 0 || m.BuildCostErrorAtZeroFunding > 1 {
		return &data.FieldError{Field: "municipality.buildCostErrorAtZeroFunding", Rule: "must be finite and in [0,1]"}
	}
	if err := orderedAnchors("layoutBonus", m.LayoutBonusAtZeroFunding, m.LayoutBonusAtFullFunding, true); err != nil {
		return err
	}
	if !num.IsFinite(m.CorruptionThreshold) || m.CorruptionThreshold <= 0 || m.CorruptionThreshold > 1 {
		return &data.FieldError{Field: "municipality.corruptionThreshold", Rule: "must be finite and in (0,1]"}
	}
	if !num.IsFinite(m.CorruptionMax) || m.CorruptionMax < 0 || m.CorruptionMax > 1 {
		return &data.FieldError{Field: "municipality.corruptionMax", Rule: "must be finite and in [0,1]"}
	}

	cc := c.Childcare
	if cc.SubsidyPerPlacePerMonthMicroPounds <= 0 {
		return &data.FieldError{Field: "childcare.subsidyPerPlacePerMonthMicroPounds", Rule: "must be positive"}
	}
	if !num.IsFinite(cc.SecondEarnerUpliftPerPlace) || cc.SecondEarnerUpliftPerPlace < 0 || cc.SecondEarnerUpliftPerPlace > 1 {
		return &data.FieldError{Field: "childcare.secondEarnerUpliftPerPlace", Rule: "must be finite and in [0,1]"}
	}
	if cc.SecondEarnerAvgWagePerMonthMicroPounds <= 0 {
		return &data.FieldError{Field: "childcare.secondEarnerAvgWagePerMonthMicroPounds", Rule: "must be positive"}
	}
	return nil
}

// orderedAnchors checks a zero/full funding anchor pair: both finite and
// non-negative, and — depending on monotonic — the full value ordered
// relative to the zero value (full >= zero for a rising output, full <= zero
// for a falling output). Enforcing the direction in the data keeps a
// mis-authored curve from silently inverting an AC-5 monotonicity claim.
func orderedAnchors(field string, zero, full float64, rising bool) error {
	if !num.IsFinite(zero) || zero < 0 || !num.IsFinite(full) || full < 0 {
		return &data.FieldError{Field: "municipality." + field, Rule: "anchors must be finite and non-negative"}
	}
	if rising && full < zero {
		return &data.FieldError{Field: "municipality." + field, Rule: "full-funding anchor must be >= zero-funding anchor"}
	}
	if !rising && full > zero {
		return &data.FieldError{Field: "municipality." + field, Rule: "full-funding anchor must be <= zero-funding anchor"}
	}
	return nil
}

// Load reads and schema-validates data/fiscal.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready-to-wire
// *FiscalAPI with its balance Config populated. correlationID is attached to
// every error this call (and the returned API's methods) construct (GR#1).
// Every failure is a registry-sourced *errs.E — never a silent default
// substitution, never a panic.
func Load(dir, correlationID string) (*FiscalAPI, error) {
	path := filepath.Join(dir, fileName)
	cfg, err := data.Load[Config, *Config](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrFiscalDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return New(cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then Loads it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*FiscalAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
