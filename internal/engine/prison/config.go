package prison

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// filePrison is data/prison.json's filename, relative to the resolved data
// directory (see data.ResolveDataDir).
const filePrison = "prison.json"

// Category is a §43 prison-security category. The three registered
// categories form the open→standard→high-security ladder (AC-3); the
// underlying string is also data/prison.json's "categories" element, so a
// category's JSON identity and Go identity are the same value.
type Category string

// The three registered categories (AC-3/AC-10). Load's completeness check
// validates that data/prison.json declares exactly this set, so an
// unregistered category is rejected at query time rather than silently
// created.
const (
	CategoryOpen         Category = "open"
	CategoryStandard     Category = "standard"
	CategoryHighSecurity Category = "highSecurity"
)

// OffenceClass is the §43 offence-severity class an admission carries. The
// reoffending base rate is keyed by (OffenceClass, AgeBand) from
// data/prison.json (GR#15).
type OffenceClass string

const (
	OffenceMinor   OffenceClass = "minor"
	OffenceSerious OffenceClass = "serious"
	OffenceViolent OffenceClass = "violent"
)

// AgeBand distinguishes the adult pipeline from the §43 youth-offending
// pipeline (AC-6) — a distinct, cheaper pipeline with its own prevention
// synergy, not merely a cheaper cell.
type AgeBand string

const (
	AgeYouth AgeBand = "youth"
	AgeAdult AgeBand = "adult"
)

// RegimeLine identifies one of the three independently-funded regime
// programmes (AC-4): education-in-prison, work programmes, and addiction
// treatment. Each is a distinct funding line with its own attributable
// effect on reoffending — never a single blended "prison quality" scalar.
type RegimeLine string

const (
	RegimeEducation          RegimeLine = "education"
	RegimeWork               RegimeLine = "work"
	RegimeAddictionTreatment RegimeLine = "addictionTreatment"
)

// ReentryKind identifies one of the three independently-sourced re-entry
// support sub-inputs (AC-5): probation capacity, ex-offender employment
// scheme uptake, and housing-on-release status.
type ReentryKind string

const (
	ReentryProbation  ReentryKind = "probationCapacity"
	ReentryEmployment ReentryKind = "employmentUptake"
	ReentryHousing    ReentryKind = "housingOnRelease"
)

// config is data/prison.json decoded and validated (GR#15). Unexported —
// the only way another package reaches a prison balance figure is through
// PrisonAPI's exported surface (GR#20).
type config struct {
	Version int    `json:"version"`
	SpecRef string `json:"specRef"`
	// Note is the file's placeholder/units documentation prose. Declared
	// explicitly — never consumed — because the BUG-281 r2 strict loader
	// rejects undeclared fields and only strips $-prefixed top-level keys.
	Note string `json:"note,omitempty"`

	Categories              []string                      `json:"categories"`
	BaseRates               map[string]map[string]float64 `json:"baseRates"`
	CategoryMismatchPenalty float64                       `json:"categoryMismatchPenalty"`
	Regime                  map[string]regimeLine         `json:"regime"`
	Reentry                 map[string]reentryLine        `json:"reentry"`
	Overcrowding            overcrowdingConfig            `json:"overcrowding"`
	Youth                   youthConfig                   `json:"youth"`
	AdultCostPerOffender    map[string]int64              `json:"adultCostPerOffender"`
	FuseYears               fuseYearsConfig               `json:"fuseYears"`
}

// regimeLine is one data/prison.json "regime" entry (AC-4).
type regimeLine struct {
	MaxEffect  float64 `json:"maxEffect"`
	CostForMax int64   `json:"costForMax"`
}

// reentryLine is one data/prison.json "reentry" entry (AC-5).
type reentryLine struct {
	MaxEffect float64 `json:"maxEffect"`
}

type overcrowdingConfig struct {
	DegradeMax float64 `json:"degradeMax"`
}

type youthConfig struct {
	CostMultiplier float64 `json:"costMultiplier"`
}

type fuseYearsConfig struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Validate implements foundation/data.Validator for config. It runs
// schema-level checks foundation.data's generic per-file loader cannot:
// which category names / offence classes / regime lines this package
// requires, and that every rate is a well-formed [0,1] figure. Returns a
// *data.FieldError naming the offending field and rule (never a bare
// "validation failed" string).
func (c *config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(c.Categories) != 3 {
		return &data.FieldError{Field: "categories", Rule: "must declare exactly open, standard, highSecurity"}
	}
	for _, cat := range []Category{CategoryOpen, CategoryStandard, CategoryHighSecurity} {
		if !containsString(c.Categories, string(cat)) {
			return &data.FieldError{Field: "categories", Rule: "missing category " + string(cat)}
		}
	}

	for _, age := range []AgeBand{AgeYouth, AgeAdult} {
		rates, ok := c.BaseRates[string(age)]
		if !ok {
			return &data.FieldError{Field: "baseRates", Rule: "missing age band " + string(age)}
		}
		for _, off := range []OffenceClass{OffenceMinor, OffenceSerious, OffenceViolent} {
			r, ok := rates[string(off)]
			if !ok {
				return &data.FieldError{Field: "baseRates", Rule: "missing " + string(age) + "." + string(off)}
			}
			if r < 0 || r > 1 {
				return &data.FieldError{Field: "baseRates", Rule: string(age) + "." + string(off) + " must be in [0,1]"}
			}
		}
	}

	if c.CategoryMismatchPenalty < 0 {
		return &data.FieldError{Field: "categoryMismatchPenalty", Rule: "must be >= 0"}
	}

	for _, line := range []RegimeLine{RegimeEducation, RegimeWork, RegimeAddictionTreatment} {
		rl, ok := c.Regime[string(line)]
		if !ok {
			return &data.FieldError{Field: "regime", Rule: "missing line " + string(line)}
		}
		if rl.MaxEffect < 0 || rl.MaxEffect > 1 {
			return &data.FieldError{Field: "regime", Rule: string(line) + ".maxEffect must be in [0,1]"}
		}
		if rl.CostForMax <= 0 {
			return &data.FieldError{Field: "regime", Rule: string(line) + ".costForMax must be > 0"}
		}
	}

	for _, kind := range []ReentryKind{ReentryProbation, ReentryEmployment, ReentryHousing} {
		rl, ok := c.Reentry[string(kind)]
		if !ok {
			return &data.FieldError{Field: "reentry", Rule: "missing kind " + string(kind)}
		}
		if rl.MaxEffect < 0 || rl.MaxEffect > 1 {
			return &data.FieldError{Field: "reentry", Rule: string(kind) + ".maxEffect must be in [0,1]"}
		}
	}

	if c.Overcrowding.DegradeMax < 0 || c.Overcrowding.DegradeMax > 1 {
		return &data.FieldError{Field: "overcrowding.degradeMax", Rule: "must be in [0,1]"}
	}
	if c.Youth.CostMultiplier <= 0 || c.Youth.CostMultiplier > 1 {
		return &data.FieldError{Field: "youth.costMultiplier", Rule: "must be in (0,1] so youth is cheaper than adult"}
	}
	for _, off := range []OffenceClass{OffenceMinor, OffenceSerious, OffenceViolent} {
		if c.AdultCostPerOffender[string(off)] <= 0 {
			return &data.FieldError{Field: "adultCostPerOffender", Rule: "missing or non-positive cost for " + string(off)}
		}
	}
	if c.FuseYears.Min < 5 || c.FuseYears.Max > 15 || c.FuseYears.Min > c.FuseYears.Max {
		return &data.FieldError{Field: "fuseYears", Rule: "must be a subrange of [5,15] with min <= max"}
	}
	return nil
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// loadConfig reads and validates data/prison.json from dir via
// foundation/data's generic Load[T]. Every failure is a registry-sourced
// *errs.E wrapped under this package's own ErrPrisonDataInvalid — never a
// silent default substitution, never a panic (GR#7, GR#15).
func loadConfig(dir, correlationID string) (config, error) {
	cfg, err := data.Load[config, *config](filepath.Join(dir, filePrison), correlationID)
	if err != nil {
		return config{}, errs.Wrap(ErrPrisonDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return cfg, nil
}
