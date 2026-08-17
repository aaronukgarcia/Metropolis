package crime

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file loads engine.crime's GR#15 data-sourced tuning (crime.json):
// the nine per-type base generation rates, the per-driver elasticities,
// the concave-deterrence half-saturation, the clearance/prevention
// coefficients, the default strategy mix, the gang formation/removal
// thresholds and respawn window, the threat-dial lead window, and the
// justice-chain rates/thresholds. Every balance number this package uses
// comes from this file, never a Go literal (GR#15, ASM-327) — the ACs
// check mechanism and SHAPE, not the specific magnitudes, which are left
// untuned pending the M2 balance pass.

//go:embed crime.json
var embeddedCrimeJSON []byte

// generationConfig is crime.json's "generation" block: the per-type base
// rates (per 100k per month) and the per-driver elasticities.
type generationConfig struct {
	BaseRatesPer100kPerMonth map[string]float64 `json:"baseRatesPer100kPerMonth"`
	DriverElasticity         map[string]float64 `json:"driverElasticity"`
}

// deterrenceConfig is crime.json's "deterrence" block (AC-4).
type deterrenceConfig struct {
	HalfSaturationCoverage float64 `json:"halfSaturationCoverage"`
	Disclosure             string  `json:"disclosure"`
}

// clearanceConfig is crime.json's "clearance" block (AC-5).
type clearanceConfig struct {
	RatePerDetective float64 `json:"ratePerDetective"`
	MaxRate          float64 `json:"maxRate"`
	Disclosure       string  `json:"disclosure"`
}

// preventionConfig is crime.json's "prevention" block (AC-5).
type preventionConfig struct {
	ScalePerInfrastructure float64 `json:"scalePerInfrastructure"`
	Disclosure             string  `json:"disclosure"`
}

// mixConfig is crime.json's "mix" block (AC-10/AC-15).
type mixConfig struct {
	DefaultPatrol    float64 `json:"defaultPatrol"`
	DefaultDetective float64 `json:"defaultDetective"`
	DefaultCommunity float64 `json:"defaultCommunity"`
	Total            float64 `json:"total"`
	Disclosure       string  `json:"disclosure"`
}

// safetyConfig is crime.json's "safety" block (US-6/AC-1 safety term).
type safetyConfig struct {
	HalfSaturationActiveCrime float64 `json:"halfSaturationActiveCrime"`
	Disclosure                string  `json:"disclosure"`
}

// gangsConfig is crime.json's "gangs" block (AC-6/AC-7/AC-8/AC-9).
type gangsConfig struct {
	FormationMonths            int      `json:"formationMonths"`
	YouthUnemploymentThreshold float64  `json:"youthUnemploymentThreshold"`
	BlightThreshold            float64  `json:"blightThreshold"`
	LowClearanceThreshold      float64  `json:"lowClearanceThreshold"`
	RegenerationThreshold      float64  `json:"regenerationThreshold"`
	StrengthDecayPerMonth      float64  `json:"strengthDecayPerMonth"`
	RemovedStrengthThreshold   float64  `json:"removedStrengthThreshold"`
	CrimeUplift                float64  `json:"crimeUplift"`
	RecruitmentRate            float64  `json:"recruitmentRate"`
	RespawnWindowMonths        int      `json:"respawnWindowMonths"`
	Names                      []string `json:"names"`
	Disclosure                 string   `json:"disclosure"`
}

// threatConfig is crime.json's "threat" block (AC-11).
type threatConfig struct {
	MinLeadMonths             int     `json:"minLeadMonths"`
	MaxLevel                  float64 `json:"maxLevel"`
	GrowthPerExposure         float64 `json:"growthPerExposure"`
	DampingPerFunding         float64 `json:"dampingPerFunding"`
	DampingPerLiaison         float64 `json:"dampingPerLiaison"`
	BaseTriggerProbability    float64 `json:"baseTriggerProbability"`
	FundingProbabilityDamping float64 `json:"fundingProbabilityDamping"`
	Disclosure                string  `json:"disclosure"`
}

// justiceConfig is crime.json's "justice" block (AC-12/AC-13).
type justiceConfig struct {
	ReleaseNoChargeRate           float64 `json:"releaseNoChargeRate"`
	ConvictionRate                float64 `json:"convictionRate"`
	PrisonSentenceRate            float64 `json:"prisonSentenceRate"`
	BacklogReleaseThreshold       int64   `json:"backlogReleaseThreshold"`
	BacklogPressureHalfSaturation float64 `json:"backlogPressureHalfSaturation"`
	Disclosure                    string  `json:"disclosure"`
}

// config is crime.json's full schema.
type config struct {
	Version    int              `json:"version"`
	Disclosure string           `json:"disclosure"`
	Generation generationConfig `json:"generation"`
	Deterrence deterrenceConfig `json:"deterrence"`
	Clearance  clearanceConfig  `json:"clearance"`
	Prevention preventionConfig `json:"prevention"`
	Mix        mixConfig        `json:"mix"`
	Safety     safetyConfig     `json:"safety"`
	Gangs      gangsConfig      `json:"gangs"`
	Threat     threatConfig     `json:"threat"`
	Justice    justiceConfig    `json:"justice"`
}

var (
	configOnce sync.Once
	loadedCfg  config
	configErr  error
)

// loadConfig unmarshals and validates the embedded crime.json exactly once
// per process. The embedded bytes are fixed at compile time, so a failure
// is a build defect — but it fails loudly (registry-sourced) rather than
// panicking, matching engine.spiral's embedded-config convention.
func loadConfig(correlationID string) (config, error) {
	configOnce.Do(func() {
		if err := json.Unmarshal(embeddedCrimeJSON, &loadedCfg); err != nil {
			configErr = errs.Wrap(ErrConfigInvalid, correlationID, err, map[string]any{
				"file":  "crime.json",
				"cause": err.Error(),
			})
			return
		}
		if err := validateConfig(loadedCfg, correlationID); err != nil {
			configErr = err
			return
		}
	})
	return loadedCfg, configErr
}

// validateConfig enforces the structural rules a well-formed crime.json
// must satisfy: every base rate and elasticity is non-negative, the
// deterrence/prevention/clearance coefficients are positive and finite, the
// mix weights sum to the documented total, and the gang/threat/justice
// thresholds are positive and ordered so no mechanism is a silent dead-end
// (a config where a gang can never form, or a threat can never fire, would
// be exactly the class this package exists to surface).
func validateConfig(c config, correlationID string) error {
	// generation: all nine types present with non-negative rates, all eight
	// drivers present with non-negative elasticity.
	for _, t := range crimeTypeKeys {
		key := typeJSONKey(t)
		r, ok := c.Generation.BaseRatesPer100kPerMonth[key]
		if !ok || !num.IsFinite(r) || r < 0 {
			return errs.New(ErrConfigInvalid, correlationID, map[string]any{
				"field": "generation.baseRatesPer100kPerMonth." + key, "value": r,
			})
		}
	}
	for d := Driver(0); d < numDrivers; d++ {
		key := driverJSONKey(d)
		e, ok := c.Generation.DriverElasticity[key]
		if !ok || !num.IsFinite(e) || e < 0 {
			return errs.New(ErrConfigInvalid, correlationID, map[string]any{
				"field": "generation.driverElasticity." + key, "value": e,
			})
		}
	}

	if !num.IsFinite(c.Deterrence.HalfSaturationCoverage) || c.Deterrence.HalfSaturationCoverage <= 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "deterrence.halfSaturationCoverage", "value": c.Deterrence.HalfSaturationCoverage,
		})
	}
	if !num.IsFinite(c.Clearance.RatePerDetective) || c.Clearance.RatePerDetective <= 0 ||
		!num.IsFinite(c.Clearance.MaxRate) || c.Clearance.MaxRate <= 0 || c.Clearance.MaxRate > 1 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "clearance", "value": c.Clearance,
		})
	}
	if !num.IsFinite(c.Prevention.ScalePerInfrastructure) || c.Prevention.ScalePerInfrastructure <= 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "prevention.scalePerInfrastructure", "value": c.Prevention.ScalePerInfrastructure,
		})
	}
	if !num.IsFinite(c.Mix.Total) || c.Mix.Total <= 0 ||
		!num.IsFinite(c.Mix.DefaultPatrol) || c.Mix.DefaultPatrol < 0 ||
		!num.IsFinite(c.Mix.DefaultDetective) || c.Mix.DefaultDetective < 0 ||
		!num.IsFinite(c.Mix.DefaultCommunity) || c.Mix.DefaultCommunity < 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{"field": "mix", "value": c.Mix})
	}
	defaultSum := c.Mix.DefaultPatrol + c.Mix.DefaultDetective + c.Mix.DefaultCommunity
	if !approxEq(defaultSum, c.Mix.Total) {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "mix.defaultSum", "value": defaultSum,
		})
	}

	if !num.IsFinite(c.Safety.HalfSaturationActiveCrime) || c.Safety.HalfSaturationActiveCrime <= 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{
			"field": "safety.halfSaturationActiveCrime", "value": c.Safety.HalfSaturationActiveCrime,
		})
	}

	g := c.Gangs
	if g.FormationMonths <= 0 || g.RespawnWindowMonths <= 0 ||
		!num.IsFinite(g.YouthUnemploymentThreshold) || g.YouthUnemploymentThreshold < 0 ||
		!num.IsFinite(g.BlightThreshold) || g.BlightThreshold < 0 ||
		!num.IsFinite(g.LowClearanceThreshold) || g.LowClearanceThreshold < 0 ||
		!num.IsFinite(g.RegenerationThreshold) || g.RegenerationThreshold < 0 ||
		!num.IsFinite(g.StrengthDecayPerMonth) || g.StrengthDecayPerMonth <= 0 ||
		!num.IsFinite(g.RemovedStrengthThreshold) || g.RemovedStrengthThreshold <= 0 ||
		g.RemovedStrengthThreshold >= 1 ||
		!num.IsFinite(g.CrimeUplift) || g.CrimeUplift < 0 ||
		!num.IsFinite(g.RecruitmentRate) || g.RecruitmentRate < 0 ||
		len(g.Names) == 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{"field": "gangs", "value": g})
	}

	t := c.Threat
	if t.MinLeadMonths <= 0 || !num.IsFinite(t.MaxLevel) || t.MaxLevel <= 0 ||
		!num.IsFinite(t.GrowthPerExposure) || t.GrowthPerExposure < 0 ||
		!num.IsFinite(t.DampingPerFunding) || t.DampingPerFunding < 0 ||
		!num.IsFinite(t.DampingPerLiaison) || t.DampingPerLiaison < 0 ||
		!num.IsFinite(t.BaseTriggerProbability) || t.BaseTriggerProbability < 0 || t.BaseTriggerProbability > 1 ||
		!num.IsFinite(t.FundingProbabilityDamping) || t.FundingProbabilityDamping < 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{"field": "threat", "value": t})
	}

	j := c.Justice
	if !num.IsFinite(j.ReleaseNoChargeRate) || j.ReleaseNoChargeRate < 0 || j.ReleaseNoChargeRate > 1 ||
		!num.IsFinite(j.ConvictionRate) || j.ConvictionRate < 0 || j.ConvictionRate > 1 ||
		!num.IsFinite(j.PrisonSentenceRate) || j.PrisonSentenceRate < 0 || j.PrisonSentenceRate > 1 ||
		j.BacklogReleaseThreshold <= 0 ||
		!num.IsFinite(j.BacklogPressureHalfSaturation) || j.BacklogPressureHalfSaturation <= 0 {
		return errs.New(ErrConfigInvalid, correlationID, map[string]any{"field": "justice", "value": j})
	}
	return nil
}

// typeJSONKey maps a CrimeType to its crime.json base-rate key.
func typeJSONKey(t CrimeType) string {
	switch t {
	case CrimePettyTheft:
		return "pettyTheft"
	case CrimeBurglary:
		return "burglary"
	case CrimeVehicleCrime:
		return "vehicleCrime"
	case CrimeCriminalDamage:
		return "criminalDamage"
	case CrimeViolent:
		return "violentCrime"
	case CrimeDrugsSupply:
		return "drugsSupply"
	case CrimeOrganised:
		return "organisedCrime"
	case CrimeFraudCyber:
		return "fraudCyber"
	case CrimeSmuggling:
		return "smuggling"
	default:
		return ""
	}
}

// driverJSONKey maps a Driver to its crime.json elasticity key.
func driverJSONKey(d Driver) string {
	switch d {
	case DriverDeprivation:
		return "deprivation"
	case DriverInequality:
		return "inequality"
	case DriverYouthUnemployment:
		return "youthUnemployment"
	case DriverBlight:
		return "blight"
	case DriverLeisureDesert:
		return "leisureDesert"
	case DriverLowPresence:
		return "lowPresence"
	case DriverEraWealth:
		return "eraWealth"
	case DriverSmugglingPressure:
		return "smugglingPressure"
	default:
		return ""
	}
}

// approxEq reports whether a and b agree within a tight tolerance — the
// float config validation's sum check (AC-15's documented-total rule).
func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
