package accelerator

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileName is this module's balance-data file (GR#15): every magnitude the
// spec leaves unquantified lives here (data/accelerator.json), never as a Go
// literal — see the file's own $comment/disclosures for the placeholder
// status of each value.
const fileName = "accelerator.json"

// Config is engine.accelerator's runtime configuration, converted from the
// JSON shape [AcceleratorData]. Every field is data-sourced from
// data/accelerator.json (GR#15); the magnitudes are placeholders pending
// Aaron's balance pass (balance-number regime).
type Config struct {
	// ConsumptionRef is the key into data/consumption.json's classes map the
	// draw resolves through (§17.2 coefficient row). The facility never
	// hard-codes a utility number.
	ConsumptionRef string

	// FacilityThroughput is the occupancy scaling the consumptionRef class
	// coefficients (1 = one accelerator facility).
	FacilityThroughput float64

	// ElectricityPeakMultiplier is the peak/base split: peak electricity
	// draw = base × this (> 1, so the peak figure is above the base figure
	// — AC-5's peak-load-awareness).
	ElectricityPeakMultiplier float64

	// ResearchRateMultiplier is the data-sourced research-rate multiplier
	// applied to engine.education's research output (> 1, so an online
	// accelerator raises the figure — AC-7).
	ResearchRateMultiplier float64

	// HealthSpillover is the wellbeing-track points the accelerator adds per
	// tick while online (AC-8). Non-negative.
	HealthSpillover float64

	// FdiAnchorDraw is the prospect-figure points the accelerator adds to
	// engine.fdi's queryable prospect figure when built (AC-9). int64 — the
	// anchor draw is a point figure, never a float (GR#16).
	FdiAnchorDraw int64

	// PrestigeBase is the prestige granted once the facility is operational
	// (AC-10: zero before operational, nonzero after). int64 — prestige is
	// never a float (GR#16).
	PrestigeBase int64

	// PrestigePerTick is the prestige accumulated per tick while online,
	// added through foundation/num's saturating helpers (AC-15).
	PrestigePerTick int64

	// ExpertGateThreshold is the numeric research-output threshold the shared
	// expert gate (FEAT-055) measures against (AC-3). int64, matching
	// engine.education's research-output unit.
	ExpertGateThreshold int64
}

// AcceleratorData is the JSON shape of data/accelerator.json (the module's
// Store). The documentation-only keys ($comment, meta, units, disclosures)
// are deliberately not modelled here — they are for Aaron's balance read and
// the AC-18 unit/disclosure requirements, never runtime inputs.
type AcceleratorData struct {
	Version                   int     `json:"version"`
	ConsumptionRef            string  `json:"consumptionRef"`
	FacilityThroughput        float64 `json:"facilityThroughput"`
	ElectricityPeakMultiplier float64 `json:"electricityPeakMultiplier"`
	ResearchRateMultiplier    float64 `json:"researchRateMultiplier"`
	HealthSpillover           float64 `json:"healthSpillover"`
	FdiAnchorDraw             int64   `json:"fdiAnchorDraw"`
	PrestigeBase              int64   `json:"prestigeBase"`
	PrestigePerTick           int64   `json:"prestigePerTick"`
	ExpertGateThreshold       int64   `json:"expertGateThreshold"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. Field-presence and per-field domain are checked here;
// the cross-field peak/base and multiplier>1 rules are the direction
// guarantees AC-5/AC-7 assert.
func (d *AcceleratorData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if d.ConsumptionRef == "" {
		return &data.FieldError{Field: "consumptionRef", Rule: "required, must name a data/consumption.json class"}
	}
	if !num.IsFinite(d.FacilityThroughput) || d.FacilityThroughput < 0 {
		return &data.FieldError{Field: "facilityThroughput", Rule: "must be finite and >= 0"}
	}
	if !num.IsFinite(d.ElectricityPeakMultiplier) || d.ElectricityPeakMultiplier <= 1 {
		return &data.FieldError{Field: "electricityPeakMultiplier", Rule: "must be finite and > 1 (peak above base)"}
	}
	if !num.IsFinite(d.ResearchRateMultiplier) || d.ResearchRateMultiplier <= 1 {
		return &data.FieldError{Field: "researchRateMultiplier", Rule: "must be finite and > 1 (raises research output)"}
	}
	if !num.IsFinite(d.HealthSpillover) || d.HealthSpillover < 0 {
		return &data.FieldError{Field: "healthSpillover", Rule: "must be finite and >= 0"}
	}
	if d.FdiAnchorDraw < 0 {
		return &data.FieldError{Field: "fdiAnchorDraw", Rule: "must be >= 0"}
	}
	if d.PrestigeBase < 0 {
		return &data.FieldError{Field: "prestigeBase", Rule: "must be >= 0"}
	}
	if d.PrestigePerTick < 0 {
		return &data.FieldError{Field: "prestigePerTick", Rule: "must be >= 0"}
	}
	if d.ExpertGateThreshold < 0 {
		return &data.FieldError{Field: "expertGateThreshold", Rule: "must be >= 0"}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *AcceleratorData (the generic data.Load's PT constraint).
func (d *AcceleratorData) Validate() error { return d.validate() }

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. It is the runtime
// counterpart of [AcceleratorData.validate] (which names the JSON fields for
// foundation.data's FieldError); both enforce the same domain, so a
// hand-constructed Config and a loaded one are equally guarded.
func (c Config) validate(correlationID string) error {
	if c.ConsumptionRef == "" {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "consumptionRef"})
	}
	if !num.IsFinite(c.FacilityThroughput) || c.FacilityThroughput < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "facilityThroughput"})
	}
	if !num.IsFinite(c.ElectricityPeakMultiplier) || c.ElectricityPeakMultiplier <= 1 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "electricityPeakMultiplier"})
	}
	if !num.IsFinite(c.ResearchRateMultiplier) || c.ResearchRateMultiplier <= 1 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "researchRateMultiplier"})
	}
	if !num.IsFinite(c.HealthSpillover) || c.HealthSpillover < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "healthSpillover"})
	}
	if c.FdiAnchorDraw < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "fdiAnchorDraw"})
	}
	if c.PrestigeBase < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "prestigeBase"})
	}
	if c.PrestigePerTick < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "prestigePerTick"})
	}
	if c.ExpertGateThreshold < 0 {
		return errs.New(ErrDataInvalid, correlationID, map[string]any{"field": "expertGateThreshold"})
	}
	return nil
}

// config converts the decoded JSON shape into the runtime [Config].
func (d *AcceleratorData) config() Config {
	return Config{
		ConsumptionRef:            d.ConsumptionRef,
		FacilityThroughput:        d.FacilityThroughput,
		ElectricityPeakMultiplier: d.ElectricityPeakMultiplier,
		ResearchRateMultiplier:    d.ResearchRateMultiplier,
		HealthSpillover:           d.HealthSpillover,
		FdiAnchorDraw:             d.FdiAnchorDraw,
		PrestigeBase:              d.PrestigeBase,
		PrestigePerTick:           d.PrestigePerTick,
		ExpertGateThreshold:       d.ExpertGateThreshold,
	}
}

// Load reads and schema-validates data/accelerator.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *AcceleratorAPI with its balance Config populated. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every failure is a registry-sourced *errs.E — never a
// silent default substitution, never a panic.
func Load(dir, correlationID string) (*AcceleratorAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[AcceleratorData, *AcceleratorData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return New(d.config(), 0, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot
// wiring, tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*AcceleratorAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
