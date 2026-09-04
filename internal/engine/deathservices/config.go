package deathservices

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// isFinite is a local, stdlib-only equivalent of foundation/num.IsFinite.
// HISTORY: written during round 2 when the foundation.num edge was not
// yet registered in this worktree's code.json (the round-2 rework
// inlined the check rather than race the lead's SSOT merge). That edge
// HAS since landed (commit 997bba5, engine.deathservices ->
// foundation.num registered), so importing num.IsFinite is now legal --
// this helper is kept because it is a one-line verbatim copy with zero
// behavioural difference, and swapping it is pure churn; a future edit
// touching this file anyway may replace it with num.IsFinite and delete
// this helper.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// config.go loads data/deathservices.json (GR#15: every capacity/threshold
// this module reasons with is data-sourced, never a bare Go literal),
// mirroring engine.citizens' MortalityConfig/mortalityconfig.go loader
// precedent exactly: same os.ReadFile + json.Decoder(DisallowUnknownFields)
// + validate() shape, same Load/LoadDefault split.

// FileDeathServices is the death-services config filename, relative to the
// resolved data directory.
const FileDeathServices = "deathservices.json"

// Number is one schema-validated numeric parameter in
// data/deathservices.json: value + unit + placeholder flag + disclosure
// (AC-22). Mirrors citizens.MortalityNumber/FertilityNumber exactly, with
// an added Placeholder bool since this file mixes two spec-given
// (non-placeholder) capacities with several balance placeholders.
type Number struct {
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	Placeholder bool    `json:"placeholder"`
	SpecRef     string  `json:"specRef,omitempty"`
	Disclosure  string  `json:"disclosure"`
}

// Meta is data/deathservices.json's documentation block (AC-22).
type Meta struct {
	Module        string   `json:"module"`
	BowCode       string   `json:"bowCode"`
	SpecRefs      []string `json:"specRefs"`
	Upstream      string   `json:"upstream"`
	BalanceRegime string   `json:"balanceRegime"`
}

// Params holds every loaded numeric parameter (AC-2/AC-3/AC-5/AC-7/AC-11/
// AC-13).
type Params struct {
	GraveyardPlotCapacity            Number `json:"graveyardPlotCapacity"`
	PlotReuseHorizonMonths           Number `json:"plotReuseHorizonMonths"`
	CremationDailyThroughputPerBody  Number `json:"cremationDailyThroughputPerBody"`
	CremationCostPerBodyMicropounds  Number `json:"cremationCostPerBodyMicropounds"`
	HearseMonthlyTransportBudget     Number `json:"hearseMonthlyTransportBudget"`
	DispensationVanBodyCapacity      Number `json:"dispensationVanBodyCapacity"`
	DispensationThroughputMultiplier Number `json:"dispensationThroughputMultiplier"`
	DispensationWellbeingPenalty     Number `json:"dispensationWellbeingPenalty"`
	DispensationApprovalPenalty      Number `json:"dispensationApprovalPenalty"`
	BacklogCapacityCeiling           Number `json:"backlogCapacityCeiling"`
}

// Config is the loaded data/deathservices.json configuration.
type Config struct {
	Version int    `json:"version"`
	Comment string `json:"$comment"`
	Meta    Meta   `json:"meta"`
	Params  Params `json:"params"`
}

// validate rejects a schema-invalid Config: a missing unit/disclosure, a
// non-finite value, or a non-positive capacity/throughput/budget where a
// non-positive value would be nonsensical. No silent default substitution
// (GR#15/GR#7) -- the malformed parameter is named and the load fails.
func (cfg *Config) validate(correlationID string) error {
	// bad's ctx carries BOTH "rule" (this file's own diagnostic key,
	// pre-existing) and "cause" (the registry template's own token, render
	// gate B1 fix) -- both name the identical validation-failure text; two
	// keys for one value rather than renaming either, so any external
	// caller already matching on "rule" is unaffected.
	bad := func(rule string) error {
		return errs.New(ErrDeathServicesDataInvalid, correlationID, map[string]any{"rule": rule, "cause": rule})
	}

	if cfg.Version <= 0 {
		return bad("version must be positive")
	}
	if cfg.Meta.Module == "" || cfg.Meta.BowCode == "" {
		return bad("meta.module and meta.bowCode are required")
	}

	numbers := []struct {
		field string
		n     Number
	}{
		{"params.graveyardPlotCapacity", cfg.Params.GraveyardPlotCapacity},
		{"params.plotReuseHorizonMonths", cfg.Params.PlotReuseHorizonMonths},
		{"params.cremationDailyThroughputPerBody", cfg.Params.CremationDailyThroughputPerBody},
		{"params.cremationCostPerBodyMicropounds", cfg.Params.CremationCostPerBodyMicropounds},
		{"params.hearseMonthlyTransportBudget", cfg.Params.HearseMonthlyTransportBudget},
		{"params.dispensationVanBodyCapacity", cfg.Params.DispensationVanBodyCapacity},
		{"params.dispensationThroughputMultiplier", cfg.Params.DispensationThroughputMultiplier},
		{"params.dispensationWellbeingPenalty", cfg.Params.DispensationWellbeingPenalty},
		{"params.dispensationApprovalPenalty", cfg.Params.DispensationApprovalPenalty},
		{"params.backlogCapacityCeiling", cfg.Params.BacklogCapacityCeiling},
	}
	for _, e := range numbers {
		if !isFinite(e.n.Value) {
			return bad(e.field + ".value must be finite")
		}
		if e.n.Unit == "" {
			return bad(e.field + ".unit is required")
		}
		if e.n.Disclosure == "" {
			return bad(e.field + ".disclosure is required")
		}
	}

	if cfg.Params.GraveyardPlotCapacity.Value <= 0 {
		return bad("params.graveyardPlotCapacity.value must be positive")
	}
	if cfg.Params.PlotReuseHorizonMonths.Value < 0 {
		return bad("params.plotReuseHorizonMonths.value must be non-negative")
	}
	if cfg.Params.CremationDailyThroughputPerBody.Value <= 0 {
		return bad("params.cremationDailyThroughputPerBody.value must be positive")
	}
	if cfg.Params.CremationCostPerBodyMicropounds.Value < 0 {
		return bad("params.cremationCostPerBodyMicropounds.value must be non-negative")
	}
	if cfg.Params.HearseMonthlyTransportBudget.Value <= 0 {
		return bad("params.hearseMonthlyTransportBudget.value must be positive")
	}
	if cfg.Params.DispensationVanBodyCapacity.Value <= 1 {
		return bad("params.dispensationVanBodyCapacity.value must exceed 1 (a van-capacity of 1 would not lift AC-11's cap)")
	}
	if cfg.Params.DispensationThroughputMultiplier.Value <= 1 {
		return bad("params.dispensationThroughputMultiplier.value must exceed 1 (AC-11 requires dispensation throughput to exceed the normal budget)")
	}
	if cfg.Params.DispensationWellbeingPenalty.Value >= 0 {
		return bad("params.dispensationWellbeingPenalty.value must be negative (AC-13)")
	}
	if cfg.Params.DispensationApprovalPenalty.Value >= 0 {
		return bad("params.dispensationApprovalPenalty.value must be negative (AC-13)")
	}
	if cfg.Params.BacklogCapacityCeiling.Value <= 0 {
		return bad("params.backlogCapacityCeiling.value must be positive")
	}

	return nil
}

// LoadConfig reads and validates data/deathservices.json from dir,
// returning the parsed Config. Every failure is a registry-sourced *errs.E
// (GR#7).
func LoadConfig(dir, correlationID string) (Config, error) {
	var cfg Config
	path := filepath.Join(dir, FileDeathServices)
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, errs.Wrap(ErrDeathServicesDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"rule":  "file must exist and be readable",
			"cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(ErrDeathServicesDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"rule":  "JSON must decode with no unknown fields",
			"cause": err.Error(),
		})
	}
	if err := cfg.validate(correlationID); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadDefaultConfig resolves data/'s directory via foundation/data and
// loads data/deathservices.json.
func LoadDefaultConfig(correlationID string) (Config, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return Config{}, err
	}
	return LoadConfig(dir, correlationID)
}

// GraveyardPlotCapacity returns the configured per-cemetery plot capacity
// (AC-2, spec-given 2k seed).
func (cfg Config) GraveyardPlotCapacity() int64 {
	return int64(cfg.Params.GraveyardPlotCapacity.Value)
}

// PlotReuseHorizonMonths returns the configured plot-reuse horizon in
// months (AC-3, placeholder).
func (cfg Config) PlotReuseHorizonMonths() int64 {
	return int64(cfg.Params.PlotReuseHorizonMonths.Value)
}

// CremationDailyThroughputPerBody returns the configured crematorium daily
// throughput cap (AC-5, spec-given 12/d seed).
func (cfg Config) CremationDailyThroughputPerBody() int64 {
	return int64(cfg.Params.CremationDailyThroughputPerBody.Value)
}

// CremationCostPerBodyMicropounds returns the configured per-body cremation
// cost in micro-pounds (AC-5/AC-6, placeholder).
func (cfg Config) CremationCostPerBodyMicropounds() int64 {
	return int64(cfg.Params.CremationCostPerBodyMicropounds.Value)
}

// HearseMonthlyTransportBudget returns the configured monthly hearse
// throughput budget in bodies/month (AC-7/AC-9, placeholder).
func (cfg Config) HearseMonthlyTransportBudget() int64 {
	return int64(cfg.Params.HearseMonthlyTransportBudget.Value)
}

// DispensationVanBodyCapacity returns the configured multi-body van/truck
// capacity per trip while dispensation is active (AC-11, placeholder).
func (cfg Config) DispensationVanBodyCapacity() int64 {
	return int64(cfg.Params.DispensationVanBodyCapacity.Value)
}

// DispensationThroughputMultiplier returns the configured throughput
// multiplier applied to the normal hearse budget while dispensation is
// active (AC-11, placeholder).
func (cfg Config) DispensationThroughputMultiplier() float64 {
	return cfg.Params.DispensationThroughputMultiplier.Value
}

// DispensationMonthlyBudget returns the effective monthly throughput while
// dispensation is active: hearseMonthlyTransportBudget *
// dispensationThroughputMultiplier, rounded down (AC-11).
func (cfg Config) DispensationMonthlyBudget() int64 {
	return int64(float64(cfg.HearseMonthlyTransportBudget()) * cfg.DispensationThroughputMultiplier())
}

// DispensationWellbeingPenalty returns the configured (negative) wellbeing
// delta applied while dispensation is active (AC-13, placeholder).
func (cfg Config) DispensationWellbeingPenalty() float64 {
	return cfg.Params.DispensationWellbeingPenalty.Value
}

// DispensationApprovalPenalty returns the configured (negative) approval
// delta applied while dispensation is active (AC-13, placeholder).
func (cfg Config) DispensationApprovalPenalty() float64 {
	return cfg.Params.DispensationApprovalPenalty.Value
}

// BacklogCapacityCeiling returns the configured backlog crisis threshold
// (AC-22 assumption 7, placeholder, informational-only in inc1).
func (cfg Config) BacklogCapacityCeiling() int64 {
	return int64(cfg.Params.BacklogCapacityCeiling.Value)
}
