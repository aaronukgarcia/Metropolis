package spiral

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// This file loads engine.spiral's GR#15 data-sourced tuning: the decay/
// blight/recovery coefficients and the ghost-city death-warning figures
// (spiral.json). Every balance number this package uses comes from this
// file, never a Go literal (ASM-241, GR#15) — the ACs check mechanism and
// SHAPE (monotonic effects, deterministic frontier, positive recovery
// costs), not the specific magnitudes, which are left untuned pending the
// M2 balance pass.
//
// The ghost-city figures (warningThresholdMonths / minWarningLeadMonths)
// deliberately duplicate engine.projections' embedded deathwarnings.json
// ghostCity entry: engine.projections owns the warning-side observation of
// that threshold, and this package owns the death-condition gate that must
// consume it, but engine.projections does not export the value. That is a
// legitimate cross-module duplication (GR#20 keeps the packages decoupled),
// so a drift test (TestGhostCityConfigDriftsAgainstProjections) asserts the
// two agree, per the standing remedy for weakness pattern #2 — silent
// divergence is the only forbidden outcome.

//go:embed spiral.json
var embeddedSpiralJSON []byte

// decayConfig is spiral.json's "decay" block (ASM-241): the per-severity
// coefficients for the three AC-3 effects plus the monthly severity growth.
type decayConfig struct {
	LandValueDragPerSeverityMicropounds  int64  `json:"landValueDragPerSeverityMicropounds"`
	HazardPressurePerSeverity            int    `json:"hazardPressurePerSeverity"`
	DemolitionCostBaseMicropounds        int64  `json:"demolitionCostBaseMicropounds"`
	DemolitionCostPerSeverityMicropounds int64  `json:"demolitionCostPerSeverityMicropounds"`
	DemolitionCostPerMonthMicropounds    int64  `json:"demolitionCostPerMonthMicropounds"`
	SeverityGrowthPerMonth               int    `json:"severityGrowthPerMonth"`
	AbandonSeverityStart                 int    `json:"abandonSeverityStart"`
	Disclosure                           string `json:"disclosure"`
}

// blightConfig is spiral.json's "blight" block (ASM-241): the severity at
// which a decayed cell becomes a spread source, and the severity ceiling.
type blightConfig struct {
	SpreadSeverityThreshold int    `json:"spreadSeverityThreshold"`
	MaxSeverity             int    `json:"maxSeverity"`
	Disclosure              string `json:"disclosure"`
}

// recoveryConfig is spiral.json's "recovery" block (ASM-241): the cost and
// severity-reduction of each AC-5 recovery command.
type recoveryConfig struct {
	InvestmentCostMicropounds       int64  `json:"investmentCostMicropounds"`
	InvestmentSeverityReduction     int    `json:"investmentSeverityReduction"`
	TaxReliefCostPerCellMicropounds int64  `json:"taxReliefCostPerCellMicropounds"`
	TaxReliefSeverityReduction      int    `json:"taxReliefSeverityReduction"`
	Disclosure                      string `json:"disclosure"`
}

// ghostCityConfig is spiral.json's "ghostCity" block (AC-20/AC-15): the
// data-sourced warning threshold and minimum lead time the death-condition
// gate compares against.
type ghostCityConfig struct {
	WarningThresholdMonths float64 `json:"warningThresholdMonths"`
	MinWarningLeadMonths   float64 `json:"minWarningLeadMonths"`
	Disclosure             string  `json:"disclosure"`
}

// stageConfig is spiral.json's "stage" block (ASM-241): the emigration-onset
// attractiveness threshold AC-2's first transition is a threshold on.
type stageConfig struct {
	EmigrationAttractivenessThreshold float64 `json:"emigrationAttractivenessThreshold"`
	Disclosure                        string  `json:"disclosure"`
}

// config is spiral.json's full schema.
type config struct {
	Version   int             `json:"version"`
	Decay     decayConfig     `json:"decay"`
	Blight    blightConfig    `json:"blight"`
	Recovery  recoveryConfig  `json:"recovery"`
	GhostCity ghostCityConfig `json:"ghostCity"`
	Stage     stageConfig     `json:"stage"`
}

var (
	configOnce sync.Once
	loadedCfg  config
	configErr  error
)

// loadConfig unmarshals the embedded spiral.json exactly once per process.
// The embedded bytes are fixed at compile time, so a failure is a build
// defect — but it fails loudly (registry-sourced) rather than panicking,
// matching engine.projections' embedded-config convention.
func loadConfig(correlationID string) (config, error) {
	configOnce.Do(func() {
		if err := json.Unmarshal(embeddedSpiralJSON, &loadedCfg); err != nil {
			configErr = spiralConfigInvalidWrapped(correlationID, "spiral.json", err)
			return
		}
		if err := validateConfig(loadedCfg, correlationID); err != nil {
			configErr = err
			return
		}
	})
	return loadedCfg, configErr
}

// validateConfig enforces the structural non-negativity/positivity rules a
// well-formed spiral.json must satisfy — every coefficient that feeds a
// positive-in-direction effect or cost must be strictly positive, and the
// severity ceiling must exceed the spread threshold so the frontier is
// reachable (a config where nothing can ever spread would be a silent
// dead-end, exactly the class this package exists to surface).
func validateConfig(c config, correlationID string) error {
	if c.Decay.LandValueDragPerSeverityMicropounds <= 0 ||
		c.Decay.HazardPressurePerSeverity <= 0 ||
		c.Decay.DemolitionCostBaseMicropounds <= 0 ||
		c.Decay.DemolitionCostPerSeverityMicropounds <= 0 ||
		c.Decay.DemolitionCostPerMonthMicropounds <= 0 ||
		c.Decay.SeverityGrowthPerMonth <= 0 ||
		c.Decay.AbandonSeverityStart <= 0 {
		return spiralConfigInvalid(correlationID, "spiral.json", "decay")
	}
	if c.Blight.SpreadSeverityThreshold <= 0 || c.Blight.MaxSeverity < c.Blight.SpreadSeverityThreshold {
		return spiralConfigInvalid(correlationID, "spiral.json", "blight")
	}
	if c.Recovery.InvestmentCostMicropounds <= 0 ||
		c.Recovery.InvestmentSeverityReduction <= 0 ||
		c.Recovery.TaxReliefCostPerCellMicropounds <= 0 ||
		c.Recovery.TaxReliefSeverityReduction <= 0 {
		return spiralConfigInvalid(correlationID, "spiral.json", "recovery")
	}
	if c.GhostCity.WarningThresholdMonths <= 0 || c.GhostCity.MinWarningLeadMonths <= 0 ||
		c.GhostCity.Disclosure == "" {
		return spiralConfigInvalid(correlationID, "spiral.json", "ghostCity")
	}
	if c.Stage.EmigrationAttractivenessThreshold < 0 {
		return spiralConfigInvalid(correlationID, "spiral.json", "stage")
	}
	return nil
}
