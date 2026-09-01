package citizens

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-087 (mkey feat.deathwave) inc1 — the death-queue smoothing budget's
// data-file config, loaded exactly like fertility.go's FertilityConfig
// (module-owned-loader precedent shared with engine.census/engine.market/
// engine.season): GR#15 requires the monthly budget to be data, never a
// bare Go literal (AC-5).

// FileMortality is the mortality/death-queue config filename, relative to
// the resolved data directory.
const FileMortality = "mortality.json"

// MortalityNumber is one schema-validated numeric parameter in
// data/mortality.json — mirrors FertilityNumber/engine.census's Number type
// exactly (value + unit + disclosure).
type MortalityNumber struct {
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Disclosure string  `json:"disclosure"`
}

// MortalityMeta is data/mortality.json's documentation block (AC-19): it
// names engine.season as the (inc2) emergency source and FEAT-088 as the
// handoff target, and states the balance-placeholder regime.
type MortalityMeta struct {
	Module          string   `json:"module"`
	FeatureKey      string   `json:"featureKey"`
	SpecRefs        []string `json:"specRefs"`
	EmergencySource string   `json:"emergencySource"`
	HandoffTarget   string   `json:"handoffTarget"`
	BalanceRegime   string   `json:"balanceRegime"`
}

// MortalityParams holds the death-queue smoothing budget placeholders
// (FEAT-087). MonthlyDeathBudget is the only field inc1 consumes;
// MonthlyEmergencyBudget is reserved for inc2 (ASM-579) and is validated
// (never left to rot as an unchecked field) but not yet read by any inc1
// code path.
type MortalityParams struct {
	MonthlyDeathBudget     MortalityNumber `json:"monthlyDeathBudget"`
	MonthlyEmergencyBudget MortalityNumber `json:"monthlyEmergencyBudget"`
}

// MortalityConfig is the loaded data/mortality.json configuration.
type MortalityConfig struct {
	Version int             `json:"version"`
	Comment string          `json:"$comment"`
	Meta    MortalityMeta   `json:"meta"`
	Params  MortalityParams `json:"params"`
}

// validate rejects a schema-invalid MortalityConfig: a missing unit or
// disclosure, a non-finite value, a non-positive/non-integer monthly
// budget, or a negative emergency budget. No silent default substitution
// (AC-12/GR#7/GR#15) — the malformed parameter is named and the load
// fails, mirroring FertilityConfig.validate exactly.
func (cfg *MortalityConfig) validate(correlationID string) error {
	bad := func(rule string) error {
		return errs.New(ErrMortalityDataInvalid, correlationID, map[string]any{"rule": rule})
	}

	if cfg.Version <= 0 {
		return bad("version must be positive")
	}
	if cfg.Meta.BalanceRegime == "" || cfg.Meta.FeatureKey == "" {
		return bad("meta.balanceRegime and meta.featureKey are required")
	}

	numbers := []struct {
		field string
		n     MortalityNumber
	}{
		{"params.monthlyDeathBudget", cfg.Params.MonthlyDeathBudget},
		{"params.monthlyEmergencyBudget", cfg.Params.MonthlyEmergencyBudget},
	}
	for _, e := range numbers {
		if !num.IsFinite(e.n.Value) {
			return bad(e.field + ".value must be finite")
		}
		if e.n.Unit == "" {
			return bad(e.field + ".unit is required")
		}
		if e.n.Disclosure == "" {
			return bad(e.field + ".disclosure is required")
		}
	}

	budget := cfg.Params.MonthlyDeathBudget.Value
	if budget <= 0 {
		return bad("params.monthlyDeathBudget.value must be a positive integer (a non-positive budget would silently re-enable the cliff or freeze the queue forever)")
	}
	if budget != float64(int64(budget)) {
		return bad("params.monthlyDeathBudget.value must be a whole number of deaths/month")
	}

	emergency := cfg.Params.MonthlyEmergencyBudget.Value
	if emergency < 0 {
		return bad("params.monthlyEmergencyBudget.value must be non-negative (0 is the documented unbounded sentinel)")
	}
	if emergency != float64(int64(emergency)) {
		return bad("params.monthlyEmergencyBudget.value must be a whole number of deaths/month")
	}

	return nil
}

// LoadMortalityConfig reads and validates data/mortality.json from dir,
// returning the parsed MortalityConfig. Every failure is a
// registry-sourced *errs.E (GR#7/AC-12).
func LoadMortalityConfig(dir, correlationID string) (MortalityConfig, error) {
	var cfg MortalityConfig
	path := filepath.Join(dir, FileMortality)
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, errs.Wrap(ErrMortalityDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"rule":  "file must exist and be readable",
			"cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(ErrMortalityDataInvalid, correlationID, err, map[string]any{
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

// LoadDefaultMortalityConfig resolves data/'s directory via foundation/data
// and loads data/mortality.json.
func LoadDefaultMortalityConfig(correlationID string) (MortalityConfig, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return MortalityConfig{}, err
	}
	return LoadMortalityConfig(dir, correlationID)
}

// MonthlyDeathBudget returns the loaded config's smoothing budget as an
// integer deaths/month cap, for DeathQueue.Realise's budget argument.
func (cfg MortalityConfig) MonthlyDeathBudget() int {
	return int(cfg.Params.MonthlyDeathBudget.Value)
}
