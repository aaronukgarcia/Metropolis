package policies

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// policiesFile is data/policies.json's top-level schema. It loads through
// foundation/data's generic Load[T] (the shared duplicate-key /
// malformed-JSON / version-field machinery — GR#3) rather than a
// hand-rolled decoder. The top-level $comment / meta blocks carry the
// documentation and the data-declared interaction/PreviewDrift numbers
// (AC-18, ASM-286, GR#15).
type policiesFile struct {
	Version int           `json:"version"`
	Meta    policiesMeta  `json:"meta"`
	Entries []policyEntry `json:"entries"`
}

// policiesMeta is the meta block's schema: the §52 categories, the
// data-declared combination rule (AC-10), the ASM-286 PreviewDrift numbers
// (tolerance + quarterly cadence, GR#15 — read from here, never a Go
// literal), and the ASM-284/ASM-288 disclosure.
type policiesMeta struct {
	SpecRef      string       `json:"specRef"`
	Categories   []string     `json:"categories"`
	Combination  string       `json:"combination"`
	PreviewDrift previewDrift `json:"previewDrift"`
	// Disclosures is the ASM-284/ASM-288 placeholder-provenance prose.
	// Declared explicitly — never consumed — because the BUG-281 r2 strict
	// loader rejects undeclared fields at any depth (only $-prefixed
	// TOP-LEVEL keys are stripped, and this block is nested).
	Disclosures json.RawMessage `json:"disclosures,omitempty"`
}

// previewDrift is ASM-286's two stored numbers: the relative divergence
// tolerance and the checkpoint cadence in months.
type previewDrift struct {
	Tolerance        float64 `json:"tolerance"`
	CheckpointMonths int64   `json:"checkpointMonths"`
}

// policyEntry is one library row.
type policyEntry struct {
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Category      string    `json:"category"`
	Scope         string    `json:"scope"`
	Mechanism     mechanism `json:"mechanism"`
	Cost          costEntry `json:"cost"`
	ConflictsWith []string  `json:"conflictsWith"`
	Disclosure    string    `json:"disclosure"`
}

// mechanism is the data-declared coefficient-move list (AC-2).
type mechanism struct {
	Coefficients []coefficientEntry `json:"coefficients"`
}

// coefficientEntry is one (coefficientKey, delta) pair plus the optional
// data-declared tax routing.
type coefficientEntry struct {
	Key           string  `json:"key"`
	Delta         float64 `json:"delta"`
	TaxInstrument string  `json:"taxInstrument,omitempty"`
	TaxMode       string  `json:"taxMode,omitempty"`
}

// costEntry is a policy's declared cost contract (AC-19).
type costEntry struct {
	EnactmentMicroPounds   int64 `json:"enactmentMicroPounds"`
	OpexMonthlyMicroPounds int64 `json:"opexMonthlyMicroPounds"`
}

// combinationMultiplicative is the sole supported meta.combination rule.
// It is the data-declared compounding rule (AC-10): combined delta =
// ∏(1+delta_i) − 1, which is non-additive (two deltas a,b combine to
// a+b+ab, never the naive sum).
const combinationMultiplicative = "multiplicative"

// LoadPoliciesFile reads and validates data/policies.json from dir via
// foundation/data's generic Load[T] and converts it to the runtime library.
// Every failure is a registry-sourced *errs.E wrapped under
// ErrPoliciesDataInvalid, never a silent default and never a panic.
func loadPoliciesFile(dir, correlationID string) (policiesFile, error) {
	f, err := data.Load[policiesFile, *policiesFile](filepath.Join(dir, data.FilePolicies), correlationID)
	if err != nil {
		return policiesFile{}, errs.Wrap(ErrPoliciesDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return f, nil
}

// fieldErr builds a *data.FieldError naming the offending field and rule,
// so foundation/data's generic Load renders a precise registry-sourced
// message (GR#7/GR#15) — mirrors engine.worklife's identical helper.
func fieldErr(field, rule string) error {
	return &data.FieldError{Field: field, Rule: rule}
}

// Validate implements data.Validator. It enforces the module-specific
// schema rules foundation/data's generic loader has no notion of:
//
//   - a positive version;
//   - a non-empty meta.combination naming the single supported rule
//     (multiplicative);
//   - a positive ASM-286 drift tolerance and a positive checkpoint cadence;
//   - every entry carries a non-empty key/name/category/disclosure and one
//     of the three documented scope kinds;
//   - every coefficient carries a non-empty key and a finite delta;
//   - a coefficient carrying taxInstrument must also carry the sole
//     supported taxMode (districtMultiplier), and vice-versa;
//   - conflictsWith entries are plain strings (referenced keys are checked
//     for resolvability at load time, in buildLibrary, not here).
//
// Field-level failures are returned as *data.FieldError.
func (p *policiesFile) Validate() error {
	if p.Version <= 0 {
		return fieldErr("version", "required, must be a positive integer")
	}
	if p.Meta.Combination == "" {
		return fieldErr("meta.combination", "required, must be non-empty")
	}
	if p.Meta.Combination != combinationMultiplicative {
		return fieldErr("meta.combination", fmt.Sprintf("unsupported combination rule %q (want %q)", p.Meta.Combination, combinationMultiplicative))
	}
	if !num.IsFinite(p.Meta.PreviewDrift.Tolerance) || p.Meta.PreviewDrift.Tolerance <= 0 {
		return fieldErr("meta.previewDrift.tolerance", "must be finite and > 0")
	}
	if p.Meta.PreviewDrift.CheckpointMonths <= 0 {
		return fieldErr("meta.previewDrift.checkpointMonths", "must be a positive integer")
	}
	if len(p.Meta.Categories) == 0 {
		return fieldErr("meta.categories", "required, must be non-empty")
	}

	seen := make(map[string]bool, len(p.Entries))
	for i, e := range p.Entries {
		prefix := fmt.Sprintf("entries[%d]", i)
		if e.Key == "" {
			return fieldErr(prefix+".key", "required, must be non-empty")
		}
		if seen[e.Key] {
			return fieldErr(prefix+".key", fmt.Sprintf("duplicate policy key %q", e.Key))
		}
		seen[e.Key] = true
		if e.Name == "" {
			return fieldErr(prefix+".name", "required, must be non-empty")
		}
		if e.Category == "" {
			return fieldErr(prefix+".category", "required, must be non-empty")
		}
		switch e.Scope {
		case string(ScopeCitywide), string(ScopeDistrict), string(ScopeRoad):
		default:
			return fieldErr(prefix+".scope", fmt.Sprintf("unsupported scope %q (want citywide/district/road)", e.Scope))
		}
		if e.Disclosure == "" {
			return fieldErr(prefix+".disclosure", "required, must be a non-empty disclosure comment")
		}
		if len(e.Mechanism.Coefficients) == 0 {
			return fieldErr(prefix+".mechanism.coefficients", "required, must declare at least one coefficient move")
		}
		if e.Cost.EnactmentMicroPounds < 0 || e.Cost.OpexMonthlyMicroPounds < 0 {
			return fieldErr(prefix+".cost", "enactment and opex micro-pounds must be >= 0")
		}
		for j, c := range e.Mechanism.Coefficients {
			cprefix := fmt.Sprintf("%s.mechanism.coefficients[%d]", prefix, j)
			if c.Key == "" {
				return fieldErr(cprefix+".key", "required, must be non-empty")
			}
			if !num.IsFinite(c.Delta) {
				return fieldErr(cprefix+".delta", "must be finite (not NaN/±Inf)")
			}
			if (c.TaxInstrument == "") != (c.TaxMode == "") {
				return fieldErr(cprefix, "taxInstrument and taxMode must be declared together or both omitted")
			}
			if c.TaxMode != "" && c.TaxMode != taxMoveDistrictMultiplier {
				return fieldErr(cprefix+".taxMode", fmt.Sprintf("unsupported tax mode %q (want %q)", c.TaxMode, taxMoveDistrictMultiplier))
			}
		}
	}
	return nil
}

// buildLibrary converts the validated file into the runtime library map,
// cross-checking every conflictsWith reference resolves to a real policy
// key (GR#15/GR#7 — a dangling conflict reference is a load-time error, not
// a silently-ignored one). Returns the library and the meta block.
func (p *policiesFile) buildLibrary(correlationID string) (map[PolicyID]*policyDef, policiesMeta, error) {
	byKey := make(map[PolicyID]bool, len(p.Entries))
	for _, e := range p.Entries {
		byKey[PolicyID(e.Key)] = true
	}

	lib := make(map[PolicyID]*policyDef, len(p.Entries))
	for _, e := range p.Entries {
		mech := make([]CoefficientDelta, 0, len(e.Mechanism.Coefficients))
		for _, c := range e.Mechanism.Coefficients {
			cd := CoefficientDelta{Key: c.Key, Delta: c.Delta}
			if c.TaxInstrument != "" {
				cd.Tax = &TaxMove{Instrument: c.TaxInstrument, Mode: c.TaxMode}
			}
			mech = append(mech, cd)
		}
		sort.Slice(mech, func(i, j int) bool { return mech[i].Key < mech[j].Key })

		conflicts := make([]PolicyID, 0, len(e.ConflictsWith))
		for _, ck := range e.ConflictsWith {
			if !byKey[PolicyID(ck)] {
				return nil, policiesMeta{}, errs.New(ErrPoliciesDataInvalid, correlationID, map[string]any{
					"field":    "entries.conflictsWith",
					"policy":   e.Key,
					"conflict": ck,
				})
			}
			conflicts = append(conflicts, PolicyID(ck))
		}
		sort.Slice(conflicts, func(i, j int) bool { return conflicts[i] < conflicts[j] })

		lib[PolicyID(e.Key)] = &policyDef{
			ID:        PolicyID(e.Key),
			Name:      e.Name,
			Category:  e.Category,
			Scope:     ScopeKind(e.Scope),
			Mechanism: mech,
			Cost: CostDef{
				EnactmentMicroPounds:   e.Cost.EnactmentMicroPounds,
				OpexMonthlyMicroPounds: e.Cost.OpexMonthlyMicroPounds,
			},
			Conflicts:  conflicts,
			Disclosure: e.Disclosure,
		}
	}
	return lib, p.Meta, nil
}
