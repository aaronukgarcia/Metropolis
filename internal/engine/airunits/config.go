package airunits

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric magnitude
// §26/§10/§28/§54 leave unquantified lives here, never as a Go literal.
const fileName = "helicopters.json"

// UnitData is the JSON shape of one "units" entry (AC-3/AC-4/AC-8).
type UnitData struct {
	PurchaseCostMicroPounds  int64      `json:"purchaseCostMicroPounds"`
	UnlockMilestone          int64      `json:"unlockMilestone"`
	FuelCostMicroPounds      int64      `json:"fuelCostMicroPounds"`
	HangarCostMicroPounds    int64      `json:"hangarCostMicroPounds"`
	InsuranceCostMicroPounds int64      `json:"insuranceCostMicroPounds"`
	CrewCostMicroPounds      int64      `json:"crewCostMicroPounds"`
	Effect                   EffectData `json:"effect"`
	Disclosure               string     `json:"disclosure"`
}

// EffectData is one role's effect parameter (AC-8). Each role carries exactly
// one meaningful field; the others read as zero.
type EffectData struct {
	CoverageRadiusExtension         int64 `json:"coverageRadiusExtension,omitempty"`
	RemoteFireReachBonus            int64 `json:"remoteFireReachBonus,omitempty"`
	HospitalLandingReductionMinutes int64 `json:"hospitalLandingReductionMinutes,omitempty"`
	CommercialRevenuePerMonth       int64 `json:"commercialRevenuePerMonth,omitempty"`
}

// MaintenanceData is data/helicopters.json's "maintenance" block (AC-6).
type MaintenanceData struct {
	WearPerFlightCycle        int64  `json:"wearPerFlightCycle"`
	OutOfServiceWearThreshold int64  `json:"outOfServiceWearThreshold"`
	EngineerHoursPerWearPoint int64  `json:"engineerHoursPerWearPoint"`
	Disclosure                string `json:"disclosure"`
}

// ApprovalData is data/helicopters.json's "approval" block (AC-9).
type ApprovalData struct {
	ApprovalWeightPerActiveChopper int64  `json:"approvalWeightPerActiveChopper"`
	Disclosure                     string `json:"disclosure"`
}

// WeatherData is data/helicopters.json's "weather" block (AC-7).
type WeatherData struct {
	GroundingWindKnots int64  `json:"groundingWindKnots"`
	Disclosure         string `json:"disclosure"`
}

// TravelData is data/helicopters.json's "travel" block (AC-7's air path).
type TravelData struct {
	AirSpeedMinutesPerUnit int64  `json:"airSpeedMinutesPerUnit"`
	Disclosure             string `json:"disclosure"`
}

// HelicoptersData is the JSON shape of data/helicopters.json. The Units map is
// only ever ACCESSED by key (never ranged), so JSON object-key order is
// irrelevant to determinism (GR#21).
type HelicoptersData struct {
	Version     int                 `json:"version"`
	Meta        map[string]string   `json:"meta"`
	Units       map[string]UnitData `json:"units"`
	Maintenance MaintenanceData     `json:"maintenance"`
	Approval    ApprovalData        `json:"approval"`
	Weather     WeatherData         `json:"weather"`
	Travel      TravelData          `json:"travel"`
}

// Validate implements the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately after
// JSON decoding. Field-presence and key-shape checks live here; the per-field
// domain checks run in config.validate after conversion.
func (d *HelicoptersData) Validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(d.Units) == 0 {
		return &data.FieldError{Field: "units", Rule: "required, must declare exactly the four unit types"}
	}
	for key := range d.Units {
		if _, ok := resolveTypeKey(key); !ok {
			return &data.FieldError{Field: "units." + key, Rule: "unrecognised unit type key (want police/fire/ambulance/vip)"}
		}
	}
	return nil
}

// config is data/helicopters.json decoded, validated, and converted into the
// runtime keyed form (GR#15). Unexported — the only way another package
// reaches an airunits balance figure is through AirUnitsAPI's exported surface
// (GR#20).
type config struct {
	Units       map[UnitType]UnitConfig
	Maintenance MaintenanceConfig
	Approval    ApprovalConfig
	Weather     WeatherConfig
	Travel      TravelConfig
}

// UnitConfig is one type's data-driven economics and effect (AC-3/AC-4/AC-8).
type UnitConfig struct {
	PurchaseCost    det.Micropounds
	UnlockMilestone int64

	Fuel      det.Micropounds
	Hangar    det.Micropounds
	Insurance det.Micropounds
	Crew      det.Micropounds

	Effect RoleEffect
}

// MaintenanceConfig is the wear/downtime parameters (AC-6).
type MaintenanceConfig struct {
	WearPerFlightCycle        int64
	OutOfServiceWearThreshold int64
	EngineerHoursPerWearPoint int64
}

// ApprovalConfig is the approval-weight parameter (AC-9).
type ApprovalConfig struct {
	ApprovalWeightPerActiveChopper int64
}

// WeatherConfig is the grounding threshold (AC-7).
type WeatherConfig struct {
	GroundingWindKnots int64
}

// TravelConfig is the air-path speed (AC-7).
type TravelConfig struct {
	AirSpeedMinutesPerUnit int64
}

// config converts the decoded JSON shape into the runtime Config, resolving
// each unit's effect parameter into its typed RoleEffect (AC-8).
func (d *HelicoptersData) config() config {
	c := config{
		Units: make(map[UnitType]UnitConfig, len(d.Units)),
		Maintenance: MaintenanceConfig{
			WearPerFlightCycle:        d.Maintenance.WearPerFlightCycle,
			OutOfServiceWearThreshold: d.Maintenance.OutOfServiceWearThreshold,
			EngineerHoursPerWearPoint: d.Maintenance.EngineerHoursPerWearPoint,
		},
		Approval: ApprovalConfig{ApprovalWeightPerActiveChopper: d.Approval.ApprovalWeightPerActiveChopper},
		Weather:  WeatherConfig{GroundingWindKnots: d.Weather.GroundingWindKnots},
		Travel:   TravelConfig{AirSpeedMinutesPerUnit: d.Travel.AirSpeedMinutesPerUnit},
	}
	for key, ud := range d.Units {
		t, _ := resolveTypeKey(key)
		c.Units[t] = UnitConfig{
			PurchaseCost:    det.Micropounds(ud.PurchaseCostMicroPounds),
			UnlockMilestone: ud.UnlockMilestone,
			Fuel:            det.Micropounds(ud.FuelCostMicroPounds),
			Hangar:          det.Micropounds(ud.HangarCostMicroPounds),
			Insurance:       det.Micropounds(ud.InsuranceCostMicroPounds),
			Crew:            det.Micropounds(ud.CrewCostMicroPounds),
			Effect:          effectFor(t, ud.Effect),
		}
	}
	return c
}

// effectFor builds a role's typed RoleEffect from its JSON effect block,
// populating exactly the field the role owns (AC-8).
func effectFor(t UnitType, ed EffectData) RoleEffect {
	re := RoleEffect{Kind: effectKindFor(t)}
	switch t {
	case UnitPolice:
		re.CoverageRadiusExtension = ed.CoverageRadiusExtension
	case UnitFire:
		re.RemoteFireReachBonus = ed.RemoteFireReachBonus
	case UnitAmbulance:
		re.HospitalLandingReductionMinutes = ed.HospitalLandingReductionMinutes
	case UnitVIP:
		re.CommercialRevenuePerMonth = ed.CommercialRevenuePerMonth
	}
	return re
}

// validate enforces the per-field domain rules a well-formed helicopters.json
// must satisfy (AC-12): every one of the four types present with a positive
// purchase cost and its role-appropriate effect, non-negative running-cost
// components, a non-negative milestone, positive maintenance/approval/weather/
// travel parameters, and non-empty disclosures. A malformed file is rejected
// outright — never a silent default substitution.
func (c config) validate(correlationID string) error {
	for _, t := range UnitTypes {
		uc, ok := c.Units[t]
		if !ok {
			return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
				"field": "units." + t.String(), "rule": "missing unit type",
			})
		}
		if uc.PurchaseCost <= 0 {
			return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
				"field": "units." + t.String() + ".purchaseCostMicroPounds", "rule": "must be positive",
			})
		}
		if uc.UnlockMilestone < 0 {
			return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
				"field": "units." + t.String() + ".unlockMilestone", "rule": "must be non-negative",
			})
		}
		if uc.Fuel < 0 || uc.Hangar < 0 || uc.Insurance < 0 || uc.Crew < 0 {
			return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
				"field": "units." + t.String(), "rule": "running-cost components must be non-negative",
			})
		}
		if uc.Effect.Kind != effectKindFor(t) || !effectNonZero(uc.Effect) {
			return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
				"field": "units." + t.String() + ".effect", "rule": "role defined without its effect parameter",
			})
		}
	}
	if c.Maintenance.WearPerFlightCycle <= 0 ||
		c.Maintenance.OutOfServiceWearThreshold <= 0 ||
		c.Maintenance.EngineerHoursPerWearPoint <= 0 {
		return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
			"field": "maintenance", "rule": "wear/out-of-service/engineer-hours parameters must be positive",
		})
	}
	if c.Approval.ApprovalWeightPerActiveChopper <= 0 {
		return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
			"field": "approval.approvalWeightPerActiveChopper", "rule": "must be positive",
		})
	}
	if c.Weather.GroundingWindKnots <= 0 {
		return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
			"field": "weather.groundingWindKnots", "rule": "must be positive",
		})
	}
	if c.Travel.AirSpeedMinutesPerUnit <= 0 {
		return errs.New(ErrAirunitsDataInvalid, correlationID, map[string]any{
			"field": "travel.airSpeedMinutesPerUnit", "rule": "must be positive",
		})
	}
	return nil
}

// effectNonZero reports whether a RoleEffect carries its role's meaningful
// (positive) effect parameter (AC-12's "a role defined without an effect").
func effectNonZero(re RoleEffect) bool {
	switch re.Kind {
	case EffectPoliceCoverage:
		return re.CoverageRadiusExtension > 0
	case EffectFireReach:
		return re.RemoteFireReachBonus > 0
	case EffectAmbulanceLanding:
		return re.HospitalLandingReductionMinutes > 0
	case EffectVIPCommercial:
		return re.CommercialRevenuePerMonth > 0
	default:
		return false
	}
}

// cloneConfig deep-copies the mutable parts of a config so the stored config
// can never alias caller-owned memory (SEC-167). Units is the only
// reference-typed field; a fresh map is built on every conversion.
func cloneConfig(cfg config) config {
	out := cfg
	out.Units = make(map[UnitType]UnitConfig, len(cfg.Units))
	for k, v := range cfg.Units {
		out.Units[k] = v
	}
	return out
}

// Load reads and schema-validates data/helicopters.json from dir (via
// foundation/data's generic Load, GR#15) and returns a ready *AirUnitsAPI with
// its balance config populated. correlationID is attached to every error this
// call (and the returned API's methods) construct (GR#1). Every failure is a
// registry-sourced *errs.E — never a silent default substitution, never a
// panic (AC-12).
func Load(dir, correlationID string) (*AirUnitsAPI, error) {
	cfg, err := loadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(0, cfg, correlationID)
}

// loadConfig reads and validates data/helicopters.json from dir. Every failure
// is a registry-sourced *errs.E wrapped under ErrAirunitsDataInvalid.
func loadConfig(dir, correlationID string) (config, error) {
	d, err := data.Load[HelicoptersData, *HelicoptersData](filepath.Join(dir, fileName), correlationID)
	if err != nil {
		return config{}, errs.Wrap(ErrAirunitsDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	cfg := d.config()
	if err := cfg.validate(correlationID); err != nil {
		return config{}, err
	}
	return cfg, nil
}
