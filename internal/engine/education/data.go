package education

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §27 leaves unquantified lives here, never as a Go literal.
const fileName = "education.json"

// EducationData is the JSON shape of data/education.json. The
// EntryAgeMonths map is keyed by stage name (the same names Stage.String
// renders) so a balance edit can move a gate without touching code. The
// map is only ever ACCESSED by key (never ranged), so JSON object-key
// order is irrelevant to determinism (GR#21).
type EducationData struct {
	Version  int              `json:"version"`
	EntryAge map[string]int64 `json:"entryAgeMonths"`
	Baseline float64          `json:"baselineQuality"`
	Attain   float64          `json:"attainmentScale"`
	Research float64          `json:"researchPointsPerGraduate"`
	Halls    float64          `json:"hallsCapacity"`
	Dropout  float64          `json:"dropoutRate"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. It checks field presence and per-field domain; the
// cross-field age-monotonicity rule is enforced by Config.validate after
// conversion (the split mirrors foundation.data's "per-field vs semantic"
// division).
func (d *EducationData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	for _, s := range stageOrder {
		if _, ok := d.EntryAge[s.String()]; !ok {
			return &data.FieldError{Field: "entryAgeMonths." + s.String(), Rule: "required for every pipeline stage"}
		}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *EducationData (the generic data.Load's PT constraint).
func (d *EducationData) Validate() error { return d.validate() }

// config converts the decoded JSON shape into the runtime Config (AC-3's
// data-sourced age gates). The stage-name keys are looked up by stage in
// the fixed pipeline order, so the resulting array is deterministic.
func (d *EducationData) config() Config {
	var c Config
	for _, s := range stageOrder {
		c.EntryAgeMonths[s] = d.EntryAge[s.String()]
	}
	c.BaselineQuality = d.Baseline
	c.AttainmentScale = d.Attain
	c.ResearchPointsPerGraduate = d.Research
	c.HallsCapacity = d.Halls
	c.DropoutRate = d.Dropout
	return c
}

// Load reads and schema-validates data/education.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *EducationAPI with its balance Config populated. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every failure is a registry-sourced *errs.E — never a
// silent default substitution, never a panic.
func Load(dir, correlationID string) (*EducationAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[EducationData, *EducationData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrEducationDataInvalid, correlationID, err, map[string]any{
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
// and then [Load]s it — the convenience entry point for callers (boot
// wiring, tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*EducationAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
