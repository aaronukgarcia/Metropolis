package leisure

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §42/§5.1 leave unquantified lives here, never as a Go literal.
const fileName = "leisure.json"

// LifeStageHours is the JSON shape of one life-stage baseline allocation.
type LifeStageHours struct {
	Work      float64 `json:"work"`
	Education float64 `json:"education"`
	Sleep     float64 `json:"sleep"`
	Chores    float64 `json:"chores"`
}

// LeisureData is the JSON shape of data/leisure.json. The maps are keyed by
// the names LifeStage.String()/EventKind.String()/categoryKey() render, and
// are only ever ACCESSED by key (never ranged), so JSON object-key order is
// irrelevant to determinism (GR#21).
type LeisureData struct {
	Version                int                       `json:"version"`
	HoursPerWeek           float64                   `json:"hoursPerWeek"`
	LifeStages             map[string]LifeStageHours `json:"lifeStages"`
	AccessFreeMinutes      float64                   `json:"accessFreeMinutes"`
	AccessBudgetMinutes    float64                   `json:"accessBudgetMinutes"`
	OvertimeWageRate       float64                   `json:"overtimeWageRate"`
	NoveltyDecayBase       float64                   `json:"noveltyDecayBase"`
	NoveltyDecayPerNovelty float64                   `json:"noveltyDecayPerNovelty"`
	FreshnessRecovery      float64                   `json:"freshnessRecovery"`
	EventCrowd             map[string]int64          `json:"eventCrowd"`
	MatchThreshold         float64                   `json:"matchThreshold"`
	DefaultPopulationTaste map[string]float64        `json:"defaultPopulationTaste"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. It checks field presence; the per-field domain checks
// run in Config.validate after conversion (the split mirrors foundation.data's
// "per-field vs semantic" division).
func (d *LeisureData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	for s := LifeStage(0); s < numLifeStages; s++ {
		if _, ok := d.LifeStages[s.String()]; !ok {
			return &data.FieldError{Field: "lifeStages." + s.String(), Rule: "required for every life stage"}
		}
	}
	for k := EventKind(0); k < numEventKinds; k++ {
		if _, ok := d.EventCrowd[k.String()]; !ok {
			return &data.FieldError{Field: "eventCrowd." + k.String(), Rule: "required for every event kind"}
		}
	}
	for c := 0; c < NumCategories; c++ {
		if _, ok := d.DefaultPopulationTaste[categoryKey(c)]; !ok {
			return &data.FieldError{Field: "defaultPopulationTaste." + categoryKey(c), Rule: "required for every venue category"}
		}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *LeisureData.
func (d *LeisureData) Validate() error { return d.validate() }

// config converts the decoded JSON shape into the runtime Config. The
// life-stage/event-kind/category keys are looked up by the fixed enums in
// order, so the resulting arrays are deterministic.
func (d *LeisureData) config() Config {
	var c Config
	c.HoursPerWeek = d.HoursPerWeek
	for s := LifeStage(0); s < numLifeStages; s++ {
		h := d.LifeStages[s.String()]
		c.Work[s] = h.Work
		c.Education[s] = h.Education
		c.Sleep[s] = h.Sleep
		c.Chores[s] = h.Chores
	}
	c.AccessFreeMinutes = d.AccessFreeMinutes
	c.AccessBudgetMinutes = d.AccessBudgetMinutes
	c.OvertimeWageRate = d.OvertimeWageRate
	c.NoveltyDecayBase = d.NoveltyDecayBase
	c.NoveltyDecayPerNovelty = d.NoveltyDecayPerNovelty
	c.FreshnessRecovery = d.FreshnessRecovery
	for k := EventKind(0); k < numEventKinds; k++ {
		c.EventCrowd[k] = d.EventCrowd[k.String()]
	}
	c.MatchThreshold = d.MatchThreshold
	for cat := 0; cat < NumCategories; cat++ {
		c.DefaultTaste[cat] = d.DefaultPopulationTaste[categoryKey(cat)]
	}
	return c
}

// Load reads and schema-validates data/leisure.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *LeisureAPI with its balance Config populated. correlationID is attached
// to every error this call (and the returned API's methods) construct
// (GR#1). Every failure is a registry-sourced *errs.E — never a silent
// default substitution, never a panic.
func Load(dir, correlationID string) (*LeisureAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[LeisureData, *LeisureData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrLeisureDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	cfg := d.config()
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	return New(cfg, 0, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then Loads it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*LeisureAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
