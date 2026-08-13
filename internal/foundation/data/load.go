package data

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Filenames for the §24 config set (relative to the resolved data
// directory — see ResolveDataDir).
const (
	FileConsumption   = "consumption.json"
	FileModes         = "modes.json"
	FileBuildings     = "buildings.json"
	FileUnlockTrees   = "unlock_trees.json"
	FileNamingCorpus  = "naming_corpus.json"
	FileSeasonal      = "seasonal.json"
	FileExternalWorld = "external_world.json"
	FilePolicies      = "policies.json"
)

// Load reads, JSON-decodes, and schema-validates one config file at
// path into a freshly zero-valued T, via T's pointer-receiver
// Validator implementation (PT). It is the shared implementation
// behind every per-file loader (LoadConsumption, LoadModes, ...) and
// behind Store's reload path.
//
// Every failure returns a registry-sourced *errs.E (never a panic):
//   - the file does not exist / cannot be read: CodeFileNotFound
//   - the file is not well-formed JSON (syntax error): CodeMalformedJSON
//   - a decoded field has the wrong JSON type: CodeSchemaInvalid, field
//     named from the underlying json.UnmarshalTypeError
//   - the decoded struct's "version" field is missing/zero: CodeMissingVersion
//   - any other Validate() failure: CodeSchemaInvalid, field+rule named
func Load[T any, PT interface {
	*T
	Validator
}](path, correlationID string) (T, error) {
	var zero T

	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(CodeFileNotFound, correlationID, err, map[string]any{
			"path": path,
		})
	}

	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			field := ute.Field
			if field == "" {
				field = "(root)"
			}
			return zero, errs.Wrap(CodeSchemaInvalid, correlationID, err, map[string]any{
				"path":  path,
				"field": field,
				"rule":  "type mismatch, want " + ute.Type.String(),
			})
		}
		// MET-F602's registered template has a "{cause}" placeholder
		// (BUG-099, shared with MET-E600) — populate it from the JSON
		// decode error's own text so the rendered message actually names
		// the syntax failure instead of leaving the literal "{cause}" in
		// the operator/log-visible text.
		return zero, errs.Wrap(CodeMalformedJSON, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	pv := PT(&v)
	if err := pv.Validate(); err != nil {
		code := CodeSchemaInvalid
		ctx := map[string]any{"path": path}
		var fe *FieldError
		if errors.As(err, &fe) {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
			if fe.Field == "version" {
				code = CodeMissingVersion
			}
		}
		return zero, errs.Wrap(code, correlationID, err, ctx)
	}

	return v, nil
}

// LoadConsumption loads and validates consumption.json from dir.
func LoadConsumption(dir, correlationID string) (Consumption, error) {
	return Load[Consumption, *Consumption](filepath.Join(dir, FileConsumption), correlationID)
}

// LoadModes loads and validates modes.json from dir.
func LoadModes(dir, correlationID string) (Modes, error) {
	return Load[Modes, *Modes](filepath.Join(dir, FileModes), correlationID)
}

// LoadBuildings loads and schema-validates buildings.json from dir. It
// does not cross-check consumptionRef against consumption.json — use
// [LoadBuildingsCatalogue] when that check is wanted (LoadAll performs
// it itself, see below).
func LoadBuildings(dir, correlationID string) (Buildings, error) {
	return Load[Buildings, *Buildings](filepath.Join(dir, FileBuildings), correlationID)
}

// LoadBuildingsCatalogue loads both buildings.json and consumption.json
// from dir and cross-checks every buildings.json entry's consumptionRef
// against consumption.json's Classes map (FEAT-010/data.catalogue
// AC-12), returning a registry-sourced MET-F607 error naming the
// offending entry and field if any reference is dangling. This is the
// canonical way for a caller that only wants the catalogue (rather than
// the full LoadAll aggregate) to get the same cross-file guarantee
// LoadAll provides.
func LoadBuildingsCatalogue(dir, correlationID string) (Buildings, error) {
	b, err := LoadBuildings(dir, correlationID)
	if err != nil {
		return Buildings{}, err
	}
	c, err := LoadConsumption(dir, correlationID)
	if err != nil {
		return Buildings{}, err
	}
	if verr := ValidateConsumptionRefs(&b, &c); verr != nil {
		var fe *FieldError
		ctx := map[string]any{"path": filepath.Join(dir, FileBuildings)}
		if ok := asFieldError(verr, &fe); ok {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
		}
		return Buildings{}, errs.Wrap(CodeBuildingDanglingConsumptionRef, correlationID, verr, ctx)
	}
	return b, nil
}

// asFieldError is a tiny errors.As wrapper kept local to this file so
// load.go's imports stay unchanged (errors.As is already imported here).
func asFieldError(err error, target **FieldError) bool {
	return errors.As(err, target)
}

// LoadUnlockTrees loads and validates unlock_trees.json from dir.
func LoadUnlockTrees(dir, correlationID string) (UnlockTrees, error) {
	return Load[UnlockTrees, *UnlockTrees](filepath.Join(dir, FileUnlockTrees), correlationID)
}

// LoadNamingCorpus loads and validates naming_corpus.json from dir.
func LoadNamingCorpus(dir, correlationID string) (NamingCorpus, error) {
	return Load[NamingCorpus, *NamingCorpus](filepath.Join(dir, FileNamingCorpus), correlationID)
}

// LoadSeasonal loads and validates seasonal.json from dir.
func LoadSeasonal(dir, correlationID string) (Seasonal, error) {
	return Load[Seasonal, *Seasonal](filepath.Join(dir, FileSeasonal), correlationID)
}

// LoadExternalWorld loads and validates external_world.json from dir.
func LoadExternalWorld(dir, correlationID string) (ExternalWorld, error) {
	return Load[ExternalWorld, *ExternalWorld](filepath.Join(dir, FileExternalWorld), correlationID)
}

// LoadPolicies loads and validates policies.json from dir.
func LoadPolicies(dir, correlationID string) (Policies, error) {
	return Load[Policies, *Policies](filepath.Join(dir, FilePolicies), correlationID)
}

// LoadMarketFile loads and validates market.json from dir (MOD-020
// ruling 1 — see market.go's package-level doc comment). Not part of
// the eight-file §24 set LoadAll aggregates; engine.market.Load calls
// this directly.
func LoadMarketFile(dir, correlationID string) (MarketFile, error) {
	return Load[MarketFile, *MarketFile](filepath.Join(dir, FileMarket), correlationID)
}

// Config aggregates all eight §24 files loaded by LoadAll. errors.json
// is deliberately excluded (see the package doc and
// foundation.data.md's Out of scope section) — foundation.errors owns
// that loader independently.
type Config struct {
	Consumption   Consumption
	Modes         Modes
	Buildings     Buildings
	UnlockTrees   UnlockTrees
	NamingCorpus  NamingCorpus
	Seasonal      Seasonal
	ExternalWorld ExternalWorld
	Policies      Policies
}

// LoadAll loads and validates all eight §24 files from dir into one
// aggregate Config, for callers (like engine.core's boot sequence)
// that want everything up front. It fails on the first error
// encountered (in the order above), returning that file's
// registry-sourced error unchanged.
func LoadAll(dir, correlationID string) (*Config, error) {
	var c Config
	var err error

	if c.Consumption, err = LoadConsumption(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Modes, err = LoadModes(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Buildings, err = LoadBuildings(dir, correlationID); err != nil {
		return nil, err
	}
	if verr := ValidateConsumptionRefs(&c.Buildings, &c.Consumption); verr != nil {
		var fe *FieldError
		ctx := map[string]any{"path": filepath.Join(dir, FileBuildings)}
		if ok := asFieldError(verr, &fe); ok {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
		}
		return nil, errs.Wrap(CodeBuildingDanglingConsumptionRef, correlationID, verr, ctx)
	}
	if c.UnlockTrees, err = LoadUnlockTrees(dir, correlationID); err != nil {
		return nil, err
	}
	if c.NamingCorpus, err = LoadNamingCorpus(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Seasonal, err = LoadSeasonal(dir, correlationID); err != nil {
		return nil, err
	}
	if c.ExternalWorld, err = LoadExternalWorld(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Policies, err = LoadPolicies(dir, correlationID); err != nil {
		return nil, err
	}

	return &c, nil
}
