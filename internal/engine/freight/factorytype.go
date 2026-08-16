package freight

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is feat.factorytypes (FEAT-105): the factory-unit catalogue —
// every manufacturing type as a DISTINCT modelled facility, each with its
// own footprint, input-output pair, jobs, utility draw and blight class.
// It is the GR#15 data-file contract for data/factorytypes.json: every
// per-type figure loads from that file, never from a Go literal in this
// package. The loader is self-contained (os.ReadFile + encoding/json +
// buildFactoryTypes), the same pattern freight's own LoadConfig and
// engine.mining's LoadDepositParams use, so this feature consumes no
// unregistered module edge (GR#20).
//
// # The load-bearing rule (a type is a facility, not a label)
//
// A factory type is NOT a generic factory row with a discriminator field
// and defaulted parameters. Each of the eight types resolves to a
// FactoryTypeParams whose footprint, input-output, jobs, utility draw and
// blight class are all real, typed, per-type values. The anti-pattern this
// file exists to prevent is a shared default parameter set stamped with a
// type label — the resolve surface below returns a distinct struct per
// type, and FactoryType is a closed enum (mirroring engine.mining's
// DepositType), not a free-form discriminator field.
//
// # Single source of truth against data/freight.json (GR#3)
//
// A factory type that corresponds to a freight chain stage carries ONLY a
// stageRef into data/freight.json and re-exports that stage's
// input-output/jobs/power/water/blight by reference, through one code path.
// The resolver rejects a stageRef entry that also carries an inline copy
// (buildFactoryTypes), and resolves the stage's values from the SAME loaded
// freight config (resolveFactoryTypes) — there is no second, driftable
// copy. Footprint (cells) is facility-level and lives here for every type,
// because the chain stages carry no footprint. See doc.go for the spec
// cross-references and the balance-number regime.

// fileFactoryTypes is data/factorytypes.json's filename, relative to the
// resolved data directory (see data.ResolveDataDir).
const fileFactoryTypes = "factorytypes.json"

// FactoryType identifies one of the eight modelled factory types. It is a
// closed enum (uint8 + String), mirroring engine.mining's DepositType: the
// canonical String form is the exact key used in data/factorytypes.json's
// "factoryTypes" object, so the enum, the data file and the loader stay in
// lock-step from one name table. The resolved parameter set is a real
// per-type struct, never a generic row labelled by this value.
type FactoryType uint8

const (
	FactoryAssembler          FactoryType = iota // light manufacturing: fabrication → machinery
	FactorySteelMill                             // heavy: §33 steel chain anchor
	FactoryElectronics                           // light: §46 semiconductor fab
	FactoryChemicalsConverter                    // heavy: §46 chemicals complex
	FactoryFoodProcessing                        // light processing: §33 food chain
	FactoryTextiles                              // light manufacturing
	FactoryCement                                // moderate: §33 construction chain
	FactoryGlass                                 // moderate: construction/consumer goods
)

// String returns the canonical lowercase key, identical to the matching
// data/factorytypes.json factoryTypes key.
func (f FactoryType) String() string {
	switch f {
	case FactoryAssembler:
		return "assembler"
	case FactorySteelMill:
		return "steelMill"
	case FactoryElectronics:
		return "electronics"
	case FactoryChemicalsConverter:
		return "chemicalsConverter"
	case FactoryFoodProcessing:
		return "foodProcessing"
	case FactoryTextiles:
		return "textiles"
	case FactoryCement:
		return "cement"
	case FactoryGlass:
		return "glass"
	default:
		return "unknown"
	}
}

// allFactoryTypes is the canonical ordered manifest of the eight types
// (GR#15: the expected type set is derived from this manifest, never a
// second hand-maintained list). Ordering is the resolve order, so
// FactoryTypes is deterministic regardless of data-file key order (GR#21).
var allFactoryTypes = []FactoryType{
	FactoryAssembler,
	FactorySteelMill,
	FactoryElectronics,
	FactoryChemicalsConverter,
	FactoryFoodProcessing,
	FactoryTextiles,
	FactoryCement,
	FactoryGlass,
}

// factoryTypeByName resolves a data/factorytypes.json key to its FactoryType,
// iterating the manifest's canonical names (GR#15: the taxonomy is derived
// from the enum, not a second hand-maintained list).
func factoryTypeByName(name string) (FactoryType, bool) {
	for _, ft := range allFactoryTypes {
		if ft.String() == name {
			return ft, true
		}
	}
	return 0, false
}

// BlightClass is the blight severity a facility emits, a documented enum
// matching the freight chain stages' blightClass ordinal (see doc.go for
// the zoning cross-reference). Heavy-industry types carry a higher class
// than light-manufacturing types, giving the Manufacturing-vs-Heavy-
// Industry distinction mechanical teeth.
type BlightClass int

const (
	BlightNone     BlightClass = iota // open land / farms — no industrial blight
	BlightLow                         // light processing (bakery, packhouse, textiles)
	BlightLight                       // light industry (assembler, quarry, machinery)
	BlightModerate                    // moderate industry (cement, glass, plastics)
	BlightHeavy                       // heavy industry (steel, chemicals, mines)
	BlightSevere                      // severe (reserved)
)

// validBlightClass reports whether b is one of the documented enum values.
func validBlightClass(b BlightClass) bool {
	return b >= BlightNone && b <= BlightSevere
}

// FactoryTypeParams is the resolved parameter set of ONE factory type: its
// footprint, input-output pair, jobs, utility draw and blight class are all
// named, typed, per-type fields — never a discriminator field substituting
// for the parameters.
type FactoryTypeParams struct {
	Key               FactoryType
	Name              string
	FootprintCells    int64
	StageRef          StageID // the chain stage owning this type's io/jobs/utility/blight; "" when none
	Inputs            []StageInput
	Outputs           []StageOutput
	Jobs              int64
	PowerKWhPerDay    int64
	WaterLitresPerDay int64
	BlightClass       BlightClass
}

// factoryTypeDef is the validated per-type form folded out of
// data/factorytypes.json, ready to resolve against the freight chain-stage
// config.
type factoryTypeDef struct {
	name              string
	footprintCells    int64
	stageRef          StageID
	inputs            []StageInput
	outputs           []StageOutput
	jobs              int64
	powerKWhPerDay    int64
	waterLitresPerDay int64
	blightClass       BlightClass
}

// rawFactoryTypeData is data/factorytypes.json's JSON wire shape, decoded
// only to be validated and folded into the per-type defs above.
type rawFactoryTypeData struct {
	Version      int                       `json:"version"`
	FactoryTypes map[string]rawFactoryType `json:"factoryTypes"`
}

// rawFactoryType is one factoryTypes entry. jobs/power/water/blight are
// pointers so an absent field is distinguishable from a present zero —
// buildFactoryTypes needs that to reject a stageRef entry that also carries
// an inline copy of the stage values (the AC-5 drift risk).
type rawFactoryType struct {
	Name              string  `json:"name"`
	FootprintCells    int64   `json:"footprintCells"`
	StageRef          string  `json:"stageRef"`
	Inputs            []rawIO `json:"inputs"`
	Outputs           []rawIO `json:"outputs"`
	Jobs              *int64  `json:"jobs"`
	PowerKWhPerDay    *int64  `json:"powerKWhPerDay"`
	WaterLitresPerDay *int64  `json:"waterLitresPerDay"`
	BlightClass       *int    `json:"blightClass"`
	Disclosure        string  `json:"disclosure"`
}

// LoadFactoryTypes reads, decodes and validates data/factorytypes.json from
// path, returning the folded per-type defs or ErrFactoryTypeDataInvalid.
// Every failure is a registry-sourced *errs.E — never a panic, never a
// silent default substitution, never a partially-populated map (GR#7).
func LoadFactoryTypes(path, correlationID string) (map[FactoryType]factoryTypeDef, error) {
	var zero map[FactoryType]factoryTypeDef
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrFactoryTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawFactoryTypeData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrFactoryTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildFactoryTypes(raw, path, correlationID)
}

// buildFactoryTypes folds the decoded raw data into an ordered, validated
// per-type def map keyed by FactoryType. It enforces the manifest (exactly
// the eight types), the per-type schema (footprint > 0, non-empty
// disclosure, non-zero input-output, non-negative jobs/power/water, a
// documented blight enum), and the stageRef XOR inline rule that keeps the
// freight chain stages the single source of truth.
func buildFactoryTypes(raw rawFactoryTypeData, path, correlationID string) (map[FactoryType]factoryTypeDef, error) {
	fail := func(field, rule string) (map[FactoryType]factoryTypeDef, error) {
		return nil, errs.New(ErrFactoryTypeDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
			"cause": field + ": " + rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	// Every data key must resolve to a known type (reject unknown keys
	// before the ordered fold below — the same engine.mining pattern).
	for key := range raw.FactoryTypes {
		if _, ok := factoryTypeByName(key); !ok {
			return fail("factoryTypes."+key, "unknown factory type key (not in the eight-type manifest)")
		}
	}
	if len(raw.FactoryTypes) != len(allFactoryTypes) {
		return fail("factoryTypes", "must contain exactly the eight manifest factory types (no missing, no extra)")
	}

	defs := make(map[FactoryType]factoryTypeDef, len(allFactoryTypes))
	for _, ft := range allFactoryTypes {
		r := raw.FactoryTypes[ft.String()]
		if r.Name == "" {
			return fail("factoryTypes."+ft.String()+".name", "required, must be a non-empty name")
		}
		if r.FootprintCells <= 0 {
			return fail("factoryTypes."+ft.String()+".footprintCells", "must be > 0")
		}
		if r.Disclosure == "" {
			return fail("factoryTypes."+ft.String()+".disclosure", "required, non-empty placeholder disclosure naming it pending the balance pass")
		}

		hasStageRef := r.StageRef != ""
		hasInline := r.Inputs != nil || r.Outputs != nil || r.Jobs != nil ||
			r.PowerKWhPerDay != nil || r.WaterLitresPerDay != nil || r.BlightClass != nil
		if hasStageRef && hasInline {
			// The AC-5 drift risk: a stageRef entry that ALSO carries a
			// second copy of the stage's io/jobs/power/water/blight would
			// re-declare the number in two files. Reject it at load time.
			return fail("factoryTypes."+ft.String(), "stageRef and inline io/jobs/power/water/blight are mutually exclusive (single source of truth)")
		}

		def := factoryTypeDef{name: r.Name, footprintCells: r.FootprintCells}
		if hasStageRef {
			def.stageRef = StageID(r.StageRef)
			defs[ft] = def
			continue
		}

		if !hasInline {
			return fail("factoryTypes."+ft.String(), "must carry either a stageRef or inline io/jobs/power/water/blight")
		}
		if len(r.Inputs) == 0 {
			return fail("factoryTypes."+ft.String()+".inputs", "must have at least one input (non-zero input-output pair)")
		}
		if len(r.Outputs) == 0 {
			return fail("factoryTypes."+ft.String()+".outputs", "must have at least one output")
		}
		if r.Jobs == nil || *r.Jobs <= 0 {
			return fail("factoryTypes."+ft.String()+".jobs", "must be present and > 0")
		}
		if r.PowerKWhPerDay == nil || *r.PowerKWhPerDay < 0 {
			return fail("factoryTypes."+ft.String()+".powerKWhPerDay", "must be present and >= 0")
		}
		if r.WaterLitresPerDay == nil || *r.WaterLitresPerDay < 0 {
			return fail("factoryTypes."+ft.String()+".waterLitresPerDay", "must be present and >= 0")
		}
		if r.BlightClass == nil || !validBlightClass(BlightClass(*r.BlightClass)) {
			return fail("factoryTypes."+ft.String()+".blightClass", "must be present and a documented enum value")
		}

		inputs := make([]StageInput, 0, len(r.Inputs))
		for _, in := range r.Inputs {
			if in.Commodity == "" {
				return fail("factoryTypes."+ft.String()+".inputs.commodity", "must be a non-empty freight commodity")
			}
			if in.TonnesPerDay <= 0 {
				return fail("factoryTypes."+ft.String()+".inputs."+in.Commodity+".tonnesPerDay", "must be > 0")
			}
			inputs = append(inputs, StageInput{Commodity: Commodity(in.Commodity), TonnesPerDay: in.TonnesPerDay})
		}
		outputs := make([]StageOutput, 0, len(r.Outputs))
		for _, out := range r.Outputs {
			if out.Commodity == "" {
				return fail("factoryTypes."+ft.String()+".outputs.commodity", "must be a non-empty freight commodity")
			}
			if out.TonnesPerDay <= 0 {
				return fail("factoryTypes."+ft.String()+".outputs."+out.Commodity+".tonnesPerDay", "must be > 0")
			}
			outputs = append(outputs, StageOutput{Commodity: Commodity(out.Commodity), TonnesPerDay: out.TonnesPerDay})
		}

		def.inputs = inputs
		def.outputs = outputs
		def.jobs = *r.Jobs
		def.powerKWhPerDay = *r.PowerKWhPerDay
		def.waterLitresPerDay = *r.WaterLitresPerDay
		def.blightClass = BlightClass(*r.BlightClass)
		defs[ft] = def
	}
	return defs, nil
}

// resolveFactoryTypes folds the validated per-type defs against the loaded
// freight chain-stage config (cfg), returning the resolved parameter set for
// every type in manifest order. A stageRef type re-exports its stage's
// io/jobs/power/water/blight by reference (one code path); an inline type's
// commodities are checked against the freight commodity registry here, where
// cfg is in scope. A dangling stageRef or an unregistered commodity is
// ErrFactoryTypeDataInvalid — never a silently-substituted default.
func resolveFactoryTypes(cfg config, defs map[FactoryType]factoryTypeDef, correlationID string) ([]FactoryTypeParams, error) {
	stageByID := make(map[StageID]stageConfig, len(cfg.stageConfigs))
	for _, sc := range cfg.stageConfigs {
		stageByID[sc.ID] = sc
	}

	out := make([]FactoryTypeParams, 0, len(allFactoryTypes))
	for _, ft := range allFactoryTypes {
		def := defs[ft]
		p := FactoryTypeParams{
			Key:            ft,
			Name:           def.name,
			FootprintCells: def.footprintCells,
			StageRef:       def.stageRef,
		}
		if def.stageRef != "" {
			sc, ok := stageByID[def.stageRef]
			if !ok {
				return nil, errs.New(ErrFactoryTypeDataInvalid, correlationID, map[string]any{
					"field": "factoryTypes." + ft.String() + ".stageRef",
					"rule":  "dangling stage reference",
					"stage": string(def.stageRef),
					"cause": "factoryTypes." + ft.String() + ".stageRef: dangling stage reference to " + string(def.stageRef),
				})
			}
			p.Inputs = append([]StageInput(nil), sc.Inputs...)
			p.Outputs = append([]StageOutput(nil), sc.Outputs...)
			p.Jobs = sc.Jobs
			p.PowerKWhPerDay = sc.PowerKWhPerDay
			p.WaterLitresPerDay = sc.WaterLitresPerDay
			p.BlightClass = BlightClass(sc.BlightClass)
		} else {
			for _, in := range def.inputs {
				if _, ok := cfg.commodities[in.Commodity]; !ok {
					return nil, errs.New(ErrFactoryTypeDataInvalid, correlationID, map[string]any{
						"field":     "factoryTypes." + ft.String() + ".inputs." + string(in.Commodity),
						"rule":      "references an unregistered freight commodity",
						"commodity": string(in.Commodity),
						"cause":     "factoryTypes." + ft.String() + ".inputs." + string(in.Commodity) + ": references an unregistered freight commodity",
					})
				}
			}
			for _, out := range def.outputs {
				if _, ok := cfg.commodities[out.Commodity]; !ok {
					return nil, errs.New(ErrFactoryTypeDataInvalid, correlationID, map[string]any{
						"field":     "factoryTypes." + ft.String() + ".outputs." + string(out.Commodity),
						"rule":      "references an unregistered freight commodity",
						"commodity": string(out.Commodity),
						"cause":     "factoryTypes." + ft.String() + ".outputs." + string(out.Commodity) + ": references an unregistered freight commodity",
					})
				}
			}
			p.Inputs = append([]StageInput(nil), def.inputs...)
			p.Outputs = append([]StageOutput(nil), def.outputs...)
			p.Jobs = def.jobs
			p.PowerKWhPerDay = def.powerKWhPerDay
			p.WaterLitresPerDay = def.waterLitresPerDay
			p.BlightClass = def.blightClass
		}
		out = append(out, p)
	}
	return out, nil
}

// LoadFactoryTypeCatalogue loads data/factorytypes.json from path and
// attaches the resolved catalogue to f, resolving stageRef entries against
// f's already-loaded freight chain-stage config (AC-5 single source of
// truth). It is an explicit, separately-invoked step — the same shape
// feat.containerport uses for its own data file — so core freight [Load]
// does NOT require the feature's data file. Hard-fails on a missing or
// malformed file (ErrFactoryTypeDataInvalid), never a silent empty or
// default-substituted catalogue. A second call re-resolves from path and
// overwrites the prior catalogue.
func (f *FreightAPI) LoadFactoryTypeCatalogue(path string) error {
	if err := f.checkNotCopied("LoadFactoryTypeCatalogue"); err != nil {
		return err
	}
	defs, err := LoadFactoryTypes(path, f.correlationID)
	if err != nil {
		return err
	}
	resolved, err := resolveFactoryTypes(f.cfg, defs, f.correlationID)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.factoryTypes = resolved
	f.factoryTypeByKey = make(map[FactoryType]FactoryTypeParams, len(resolved))
	for _, ft := range resolved {
		f.factoryTypeByKey[ft.Key] = ft
	}
	return nil
}

// FactoryType resolves one factory type to its distinct parameter set, or
// ErrUnknownFactoryType for a key that is not one of the eight modelled
// types — never a silently-created zero-value set (AC-8).
func (f *FreightAPI) FactoryType(key FactoryType) (FactoryTypeParams, error) {
	if err := f.checkNotCopied("FactoryType"); err != nil {
		return FactoryTypeParams{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.factoryTypeByKey[key]
	if !ok {
		return FactoryTypeParams{}, errs.New(ErrUnknownFactoryType, f.correlationID, map[string]any{
			"type": key.String(),
		})
	}
	return snapshotFactoryType(p), nil
}

// FactoryTypes returns every factory type in manifest order (deterministic,
// GR#21), each a deep copy so callers cannot mutate the catalogue.
func (f *FreightAPI) FactoryTypes() []FactoryTypeParams {
	if err := f.checkNotCopied("FactoryTypes"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]FactoryTypeParams, 0, len(f.factoryTypes))
	for _, p := range f.factoryTypes {
		out = append(out, snapshotFactoryType(p))
	}
	return out
}

// snapshotFactoryType returns a deep copy of p's input/output slices, so a
// returned parameter set never aliases the catalogue's stored state.
func snapshotFactoryType(p FactoryTypeParams) FactoryTypeParams {
	p.Inputs = append([]StageInput(nil), p.Inputs...)
	p.Outputs = append([]StageOutput(nil), p.Outputs...)
	return p
}
