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

// WeatherEmergencyThresholds holds the inc2 (ASM-579) local-derivation
// thresholds: a declared weather emergency is winter-shaped when
// engine.season's HealthWaveModifier magnitude at a month is at or above
// WinterHealthWaveThreshold, or drought-shaped when engine.season's
// WaterDemandMultiplier at a month is at or above
// DroughtWaterDemandThreshold. Direction/shape only (ASM-579's ruling) —
// neither threshold is a spec-pinned cutoff, both are GR#15 data, and
// neither invents a NEW engine.season curve or consumes feat.disasters.
type WeatherEmergencyThresholds struct {
	WinterHealthWaveThreshold   MortalityNumber `json:"winterHealthWaveThreshold"`
	DroughtWaterDemandThreshold MortalityNumber `json:"droughtWaterDemandThreshold"`
}

// MortalityParams holds the death-queue smoothing budget placeholders
// (FEAT-087). MonthlyDeathBudget is inc1's throughput cap; inc2
// (ASM-579/AC-6..8) additionally consumes MonthlyEmergencyBudget (the
// suspension-throughput override) and WeatherEmergency (the local
// emergency-declaration thresholds) — both were reserved-but-unread by
// inc1 and are now live.
type MortalityParams struct {
	MonthlyDeathBudget     MortalityNumber            `json:"monthlyDeathBudget"`
	MonthlyEmergencyBudget MortalityNumber            `json:"monthlyEmergencyBudget"`
	WeatherEmergency       WeatherEmergencyThresholds `json:"weatherEmergency"`
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
		{"params.weatherEmergency.winterHealthWaveThreshold", cfg.Params.WeatherEmergency.WinterHealthWaveThreshold},
		{"params.weatherEmergency.droughtWaterDemandThreshold", cfg.Params.WeatherEmergency.DroughtWaterDemandThreshold},
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

	// AC-6/AC-7 (ASM-579): both emergency-declaration thresholds must be
	// non-negative — a negative winter magnitude threshold or a negative
	// water-demand-multiplier threshold would make EVERY month qualify as
	// an emergency (HealthWaveModifier's magnitude and WaterDemandMultiplier
	// are both non-negative by data/seasonal.json's own schema), silently
	// suspending the smoothing budget every month rather than for a genuine
	// weather-driven event.
	if cfg.Params.WeatherEmergency.WinterHealthWaveThreshold.Value < 0 {
		return bad("params.weatherEmergency.winterHealthWaveThreshold.value must be non-negative")
	}
	if cfg.Params.WeatherEmergency.DroughtWaterDemandThreshold.Value < 0 {
		return bad("params.weatherEmergency.droughtWaterDemandThreshold.value must be non-negative")
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

// MonthlyEmergencyBudget returns the loaded config's declared-emergency
// throughput override (AC-6/inc2). 0 is the documented "unbounded" sentinel
// — a caller that reads 0 must release the ENTIRE queue that month (see
// weatheremergency.go's EmergencyRealise), never treat it as a budget of
// zero (which would freeze the queue during the one event AC-6 exists to
// make non-smoothed).
func (cfg MortalityConfig) MonthlyEmergencyBudget() int {
	return int(cfg.Params.MonthlyEmergencyBudget.Value)
}

// WinterHealthWaveThreshold returns the ASM-579 local-derivation threshold
// against which engine.season's HealthWaveModifier magnitude is compared
// to declare a winter-shaped weather emergency (AC-7).
func (cfg MortalityConfig) WinterHealthWaveThreshold() float64 {
	return cfg.Params.WeatherEmergency.WinterHealthWaveThreshold.Value
}

// DroughtWaterDemandThreshold returns the ASM-579 local-derivation
// threshold against which engine.season's WaterDemandMultiplier is
// compared to declare a drought-shaped weather emergency (AC-7).
func (cfg MortalityConfig) DroughtWaterDemandThreshold() float64 {
	return cfg.Params.WeatherEmergency.DroughtWaterDemandThreshold.Value
}
