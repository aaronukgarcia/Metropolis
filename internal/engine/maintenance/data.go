package maintenance

import (
	"path/filepath"
	"regexp"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §20/§12 leave unquantified lives here, never as a Go literal.
const fileName = "maintenance.json"

// classKeyPattern is the positive character-class domain for a class key in
// data/maintenance.json: a lowercase slug (letters, digits, underscore,
// hyphen), starting with a letter. A class key becomes a map key and the
// class identity other modules resolve against, so a value outside this
// domain is rejected outright, never trimmed into a legal form (weakness
// pattern #4). This mirrors foundation/data's buildingIDPattern convention.
var classKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ClassData is the JSON shape of one "classes" entry.
type ClassData struct {
	EngineerDaysPerYear int64  `json:"engineerDaysPerYear"`
	LifetimeYears       int64  `json:"lifetimeYears"`
	Disclosure          string `json:"disclosure"`
}

// MaintenanceMeta is the data/maintenance.json meta block (AC-16): it states
// the balance-number regime and the unit of every figure, so a downstream
// consumer never guesses whether a value is engineer-days, years, or pounds.
type MaintenanceMeta struct {
	Note         string `json:"note"`
	RateUnit     string `json:"rateUnit"`
	LifetimeUnit string `json:"lifetimeUnit"`
	CostUnit     string `json:"costUnit"`
}

// MaintenanceData is the JSON shape of data/maintenance.json. The Classes
// map is only ever ACCESSED by key (never ranged), so JSON object-key order
// is irrelevant to determinism (GR#21).
type MaintenanceData struct {
	Version                      int                  `json:"version"`
	Meta                         MaintenanceMeta      `json:"meta"`
	CrewCostPerEngineerDay       int64                `json:"crewCostPerEngineerDay"`
	ContractorCostPerEngineerDay int64                `json:"contractorCostPerEngineerDay"`
	Classes                      map[string]ClassData `json:"classes"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. It checks field presence and key shape; the per-field
// domain checks (positive rate/lifetime/cost) run in Config.validate after
// conversion (the split mirrors foundation.data's "per-field vs semantic"
// division).
func (d *MaintenanceData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(d.Classes) == 0 {
		return &data.FieldError{Field: "classes", Rule: "required, must define at least two classes"}
	}
	for key, cd := range d.Classes {
		if !classKeyPattern.MatchString(key) {
			return &data.FieldError{Field: "classes." + key, Rule: "must match " + classKeyPattern.String() + " (an unrecognised class string)"}
		}
		if cd.EngineerDaysPerYear <= 0 {
			return &data.FieldError{Field: "classes." + key + ".engineerDaysPerYear", Rule: "required, must be a positive engineer-days/year rate"}
		}
		if cd.LifetimeYears <= 0 {
			return &data.FieldError{Field: "classes." + key + ".lifetimeYears", Rule: "required, must be a positive simulation-years lifetime"}
		}
		if cd.Disclosure == "" {
			return &data.FieldError{Field: "classes." + key + ".disclosure", Rule: "required, non-empty disclosure naming the value a balance-pass placeholder"}
		}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *MaintenanceData.
func (d *MaintenanceData) Validate() error { return d.validate() }

// config converts the decoded JSON shape into the runtime Config.
func (d *MaintenanceData) config() Config {
	c := Config{
		Classes:                      make(map[Class]ClassConfig, len(d.Classes)),
		CrewCostPerEngineerDay:       d.CrewCostPerEngineerDay,
		ContractorCostPerEngineerDay: d.ContractorCostPerEngineerDay,
	}
	for key, cd := range d.Classes {
		c.Classes[Class(key)] = ClassConfig{
			EngineerDaysPerYear: cd.EngineerDaysPerYear,
			LifetimeYears:       cd.LifetimeYears,
		}
	}
	return c
}

// Load reads and schema-validates data/maintenance.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *MaintenanceAPI with its balance Config populated. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every failure is a registry-sourced *errs.E — never a
// silent default substitution, never a panic (AC-12).
func Load(dir, correlationID string) (*MaintenanceAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[MaintenanceData, *MaintenanceData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrMaintenanceDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	cfg := d.config()
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	return New(cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then Loads it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*MaintenanceAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
