package farming

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the farm-type catalogue loader and resolver: it reads the
// five agricultural facility categories from data/farmtypes.json, validates
// every per-type figure all-or-nothing, and folds them into the ordered
// catalogue. The type model it folds into lives in types.go.
//
// The load-bearing rule (AC-2): each farm type is a distinct modelled
// facility — its own footprint, soil-quality band, terrain/slope preference,
// BDI term, stocking density (livestock only) and typed chain output. A
// single shared row keyed by a type string is the anti-pattern this file's
// tests are written to fail. Regime (conventional vs organic) is orthogonal
// to type: it is engine.farming's regime bundle applied to a type, not a
// sixth facility type.
//
// (AC-4): every per-type figure loads from data/farmtypes.json; none is a Go
// numeric literal in this file — a balance pass edits the data file, never
// this package. Loading is all-or-nothing (AC-8): any missing / malformed /
// schema failure returns a registry-sourced error (the MET- code declared in
// errors.go) and no catalogue — there is no partial map and no silent default
// substitution.

// FarmTypeCatalogue is the resolved, validated, ordered farm-type catalogue.
// types is held in canonical FarmType enum order (not JSON map-iteration
// order), so Resolve/Types are deterministic (AC-9).
type FarmTypeCatalogue struct {
	types []FarmTypeParams
}

// Resolve returns the distinct parameter set for typeKey, or
// ErrUnknownFarmType. Never a panic, never a silent default substitution.
// The returned FarmTypeParams is a deep copy (see clone): its Variants/Stocking
// slices and each non-nil Variant.Chain pointer are cloned, so a caller editing
// the result cannot corrupt the catalogue's shared state (GR#3) and concurrent
// callers editing their own results cannot race on shared backing arrays
// (GR#21).
func (c FarmTypeCatalogue) Resolve(typeKey string) (FarmTypeParams, error) {
	for _, p := range c.types {
		if p.Name == typeKey {
			return p.clone(), nil
		}
	}
	return FarmTypeParams{}, errs.New(ErrUnknownFarmType, errs.NewCorrelationID(), map[string]any{"typeKey": typeKey})
}

// Types returns the five resolved parameter sets in canonical FarmType enum
// order — the deterministic order AC-9 relies on. Each returned parameter set
// is a deep copy (see clone), so editing a result never aliases the
// catalogue's internal state (GR#3/GR#21).
func (c FarmTypeCatalogue) Types() []FarmTypeParams {
	out := make([]FarmTypeParams, len(c.types))
	for i, p := range c.types {
		out[i] = p.clone()
	}
	return out
}

// clone returns a deep copy of p. Variants and Stocking are re-sliced into
// fresh backing arrays and each non-nil Variant.Chain pointer is re-allocated,
// so the returned parameter set aliases none of the catalogue's internal state.
// This is the boundary Resolve/Types enforce so a caller editing a "local"
// field cannot corrupt the SSOT for later calls (GR#3) and two goroutines
// editing their own results cannot race on the shared arrays (GR#21) — the
// same copy-returning-accessor contract as foundation.registry.List().
func (p FarmTypeParams) clone() FarmTypeParams {
	p.Variants = append([]Variant(nil), p.Variants...)
	for i := range p.Variants {
		if p.Variants[i].Chain != nil {
			cc := *p.Variants[i].Chain
			p.Variants[i].Chain = &cc
		}
	}
	p.Stocking = append([]StockingDensity(nil), p.Stocking...)
	return p
}

// LoadFarmTypes reads, decodes and validates data/farmtypes.json from path,
// returning the ordered FarmTypeCatalogue or ErrFarmTypeDataInvalid (AC-8).
// Every failure is a registry-sourced *errs.E — never a panic, never a
// silent default substitution, never a partially-populated result. The
// loader is self-contained (os.ReadFile + encoding/json + buildCatalogue)
// so this package consumes no unregistered module edge: it imports only
// internal/foundation/errs.
func LoadFarmTypes(path, correlationID string) (FarmTypeCatalogue, error) {
	var zero FarmTypeCatalogue
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrFarmTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"field": "file",
			"rule":  "must exist and be readable",
			"cause": err.Error(),
		})
	}

	var raw rawFarmTypesData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrFarmTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"field": "JSON",
			"rule":  "must be valid JSON",
			"cause": err.Error(),
		})
	}

	return buildCatalogue(raw, path, correlationID)
}

// buildCatalogue folds the decoded raw data into an ordered, validated
// FarmTypeCatalogue. The canonical type order is the FarmType enum order
// (Arable..Vineyard), NOT the JSON object's key order, so resolution and
// Types() are deterministic (AC-9).
func buildCatalogue(raw rawFarmTypesData, path, correlationID string) (FarmTypeCatalogue, error) {
	fail := func(field, rule string) error {
		return errs.New(ErrFarmTypeDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}

	if raw.Version <= 0 {
		return FarmTypeCatalogue{}, fail("version", "required, must be a positive integer")
	}

	// Out-of-taxonomy keys are rejected before the ordered fold: every
	// "types" key must resolve to a known FarmType, and every FarmType must
	// be present exactly once.
	byName := make(map[FarmType]rawFarmType, len(raw.Types))
	for key, rt := range raw.Types {
		ft, ok := farmTypeByName(key)
		if !ok {
			return FarmTypeCatalogue{}, fail("types."+key, "unknown farm-type key: not one of the five facility categories")
		}
		if _, dup := byName[ft]; dup {
			return FarmTypeCatalogue{}, fail("types."+key, "duplicate farm-type entry (name aliases a known type)")
		}
		byName[ft] = rt
	}

	cat := FarmTypeCatalogue{types: make([]FarmTypeParams, 0, len(byName))}
	for ft := FarmTypeArable; ft <= FarmTypeVineyard; ft++ {
		rt, ok := byName[ft]
		if !ok {
			return FarmTypeCatalogue{}, fail("types."+ft.String(), "missing required farm-type entry")
		}
		p, err := buildParams(ft, rt, fail)
		if err != nil {
			return FarmTypeCatalogue{}, err
		}
		cat.types = append(cat.types, p)
	}
	return cat, nil
}

// buildParams validates and folds one raw type into its resolved parameter
// set. Every per-type figure is read from the raw data, never from a Go
// literal.
func buildParams(ft FarmType, rt rawFarmType, fail func(string, string) error) (FarmTypeParams, error) {
	var p FarmTypeParams
	p.Type = ft
	p.Name = ft.String()
	p.Footprint = rt.Footprint
	p.BDITerm = rt.BDITerm

	if rt.Footprint <= 0 {
		return p, fail("types."+ft.String()+".footprintCells", "required, must be a positive cell count")
	}

	sb, ok := soilBandByName(rt.SoilBand)
	if !ok {
		return p, fail("types."+ft.String()+".soilBand", "unknown soil band (want chalkDownland/pasture/chalkSlope/fertileLoam)")
	}
	p.SoilBand = sb

	tp, ok := terrainByName(rt.Terrain)
	if !ok {
		return p, fail("types."+ft.String()+".terrain", "unknown terrain preference (want openDownland/grazingDownland/chalkSlope/shelteredValley)")
	}
	p.Terrain = tp

	dest, ok := chainDestByName(rt.Chain.Destination)
	if !ok {
		return p, fail("types."+ft.String()+".chain.destination", "unknown chain destination (want mill/dairy/abattoir/packhouse/winery)")
	}
	if rt.Chain.Commodity == "" {
		return p, fail("types."+ft.String()+".chain.commodity", "required, must name the chain-output commodity")
	}
	p.Chain = ChainOutput{Commodity: rt.Chain.Commodity, Destination: dest}

	if len(rt.Variants) == 0 {
		return p, fail("types."+ft.String()+".variants", "required, must name at least one crop/livestock variant")
	}
	p.Variants = make([]Variant, 0, len(rt.Variants))
	for _, rv := range rt.Variants {
		if rv.Name == "" {
			return p, fail("types."+ft.String()+".variants", "each variant requires a non-empty name")
		}
		v := Variant{Name: rv.Name}
		if rv.Chain != nil {
			vd, ok := chainDestByName(rv.Chain.Destination)
			if !ok {
				return p, fail("types."+ft.String()+".variants."+rv.Name+".chain.destination", "unknown chain destination (want mill/dairy/abattoir/packhouse/winery)")
			}
			if rv.Chain.Commodity == "" {
				return p, fail("types."+ft.String()+".variants."+rv.Name+".chain.commodity", "required when a variant chain override is present")
			}
			co := ChainOutput{Commodity: rv.Chain.Commodity, Destination: vd}
			v.Chain = &co
		}
		p.Variants = append(p.Variants, v)
	}

	// Stocking density is livestock-only: livestock requires it (per-variant,
	// head per cell), every other type forbids it — the field is
	// livestock-typed, not a shared zero on every type.
	if ft == FarmTypeLivestock {
		if len(rt.Stocking) == 0 {
			return p, fail("types."+ft.String()+".stocking", "required for livestock: per-variant stocking density (head per cell)")
		}
		p.Stocking = make([]StockingDensity, 0, len(rt.Stocking))
		seen := make(map[string]bool, len(rt.Stocking))
		for _, rs := range rt.Stocking {
			if rs.Variant == "" {
				return p, fail("types."+ft.String()+".stocking", "each stocking entry requires a non-empty variant name")
			}
			if seen[rs.Variant] {
				return p, fail("types."+ft.String()+".stocking."+rs.Variant, "duplicate stocking entry")
			}
			seen[rs.Variant] = true
			if rs.HeadPerCell < 0 {
				return p, fail("types."+ft.String()+".stocking."+rs.Variant+".headPerCell", "must be >= 0 (head per cell)")
			}
			p.Stocking = append(p.Stocking, StockingDensity(rs))
		}
	} else if len(rt.Stocking) != 0 {
		return p, fail("types."+ft.String()+".stocking", "stocking density is a livestock-only parameter; a non-livestock type must not carry it")
	}

	return p, nil
}

// farmTypeByName resolves a data/farmtypes.json "types" key to its FarmType,
// iterating the enum's canonical String names (the taxonomy is derived from
// the enum, not a second hand-maintained list).
func farmTypeByName(name string) (FarmType, bool) {
	for ft := FarmTypeArable; ft <= FarmTypeVineyard; ft++ {
		if ft.String() == name {
			return ft, true
		}
	}
	return 0, false
}

func soilBandByName(name string) (SoilBand, bool) {
	for s := SoilBandChalkDownland; s <= SoilBandFertileLoam; s++ {
		if s.String() == name {
			return s, true
		}
	}
	return 0, false
}

func terrainByName(name string) (TerrainPreference, bool) {
	for t := TerrainOpenDownland; t <= TerrainShelteredValley; t++ {
		if t.String() == name {
			return t, true
		}
	}
	return 0, false
}

func chainDestByName(name string) (ChainDestination, bool) {
	for d := ChainMill; d <= ChainWinery; d++ {
		if d.String() == name {
			return d, true
		}
	}
	return 0, false
}
