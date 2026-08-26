package mining

import (
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the mine-type catalogue (feat.minetypes): the six extraction
// types, each modelled as a DISTINCT facility with its own footprint,
// output, blight class, jobs and depth band, loaded from
// data/minetypes.json. It shares the internal/engine/mining package with
// engine.mining and feat.resourcedeposits and has no inbound contract of
// its own — it surfaces through engine.mining's eventual MiningAPI.
//
// # The load-bearing distinct-facility rule (AC-2)
//
// A mine type is NOT a generic "mine" row whose name is stamped on one
// shared parameter set. Every type resolves to its own MineTypeParams value
// whose footprint/output/blight/jobs/depth fields are populated from that
// type's own data file entry. The type key is the LOOKUP key into the
// catalogue — it is deliberately NOT a field of MineTypeParams, so no
// caller can fall back to a shared default behind a cosmetic label.
//
// # Balance-number regime (GR#15)
//
// Every per-type figure is a placeholder read from data/minetypes.json at
// load time, never a Go numeric literal here. A balance pass edits the data
// file, never this package.
//
// # The compatibility gates (AC-5)
//
// A bulk type (chalk, sand & gravel, brickworks clay, ragstone, offshore
// aggregate) declares a geologyClass gate against engine.world's revealed
// geology (chalk/clay/gravel/deep_coal). A deposit-backed type (deep coal)
// additionally declares a depositClass gate against the feat.resourcedeposits
// DepositType taxonomy (the "coal" entry). The two gates are SEPARATE
// fields — geology and deposits are different mechanisms, and a deep coal
// mine can never silently consume a chalk-geology bulk cell.

// BlightClass is the general-blight ordinal a mine type registers against
// the blight model (engine.mining's BlightAPI, AC-1). It is a
// junior-invented four-value ordinal until engine.mining's BlightAPI names
// its own scale (logged as an assumption); the per-type value is a
// placeholder read from data/minetypes.json (GR#15).
type BlightClass uint8

const (
	BlightLow BlightClass = iota
	BlightModerate
	BlightHigh
	BlightSevere
)

// String returns the canonical data-file name of the blight class. It is
// the single name table blightClassByName derives from (GR#3).
func (b BlightClass) String() string {
	switch b {
	case BlightLow:
		return "low"
	case BlightModerate:
		return "moderate"
	case BlightHigh:
		return "high"
	case BlightSevere:
		return "severe"
	default:
		return "unrecognised"
	}
}

// blightClassByName resolves a data/minetypes.json "blightClass" string to
// its BlightClass ordinal, iterating the enum's canonical String names.
func blightClassByName(name string) (BlightClass, bool) {
	for b := BlightLow; b <= BlightSevere; b++ {
		if b.String() == name {
			return b, true
		}
	}
	return 0, false
}

// geologyGateKinds is the set of geology formations a mine type may gate on
// — the four revealed-geology pockets engine.world authors. The gate
// strings in data/minetypes.json are the canonical names this slice derives
// via GeologyKind.String(), so the data file and engine.world stay in
// lock-step from world's own name table (GR#3 — no mining-local geology
// name copy).
var geologyGateKinds = []world.GeologyKind{
	world.GeologyChalk,
	world.GeologyClay,
	world.GeologyGravel,
	world.GeologyDeepCoal,
}

// geologyGateByName resolves a geology-class gate string to a
// world.GeologyKind, using world's own canonical String names.
func geologyGateByName(name string) (world.GeologyKind, bool) {
	for _, g := range geologyGateKinds {
		if g.String() == name {
			return g, true
		}
	}
	return 0, false
}

// MineTypeParams is the resolved, data-sourced parameter set for one mine
// type (AC-1). Footprint, output, blight class, jobs and depth band are all
// named, typed fields on this value — the type key is the lookup key, never
// a field of this set (the distinct-facility rule). GeologyClass and
// DepositClass are the two compatibility gates (AC-5); SpoilTipFootprint
// and SubsidenceRadius are the deep-coal-specific risk parameters, separate
// from the shared blight field (AC-6).
type MineTypeParams struct {
	Footprint         int     // cells
	OutputCommodity   string  // canonical commodity name (chalk/aggregate/brick_clay/ragstone/coal/marine_aggregate)
	OutputRate        float64 // t/day — the base a feat.extraction tier multiplies
	BlightClass       BlightClass
	Jobs              int
	DepthMin          float64 // metres — inclusive floor of the extraction band
	DepthMax          float64 // metres — exclusive ceiling of the extraction band
	GeologyClass      string  // geology gate for bulk types ("" when not geology-gated)
	DepositClass      string  // deposit gate for deposit-backed types ("" when not deposit-backed)
	SpoilTipFootprint int     // cells — deep coal only, 0 for every other type
	SubsidenceRadius  float64 // metres — deep coal only, 0 for every other type
}

// DepthBand returns the data-sourced extraction depth band [min, max) in
// metres (AC-5).
func (p MineTypeParams) DepthBand() (min, max float64) { return p.DepthMin, p.DepthMax }

// GeologyGate returns the geology class this type is gated on, "" when the
// type is deposit-backed only (AC-5).
func (p MineTypeParams) GeologyGate() string { return p.GeologyClass }

// DepositGate returns the deposit class this type is gated on, "" when the
// type is geology-gated bulk (AC-5). Deep coal declares "coal" here.
func (p MineTypeParams) DepositGate() string { return p.DepositClass }

// Subsidence returns the deep-coal spoil-tip footprint (cells) and the
// subsidence-risk radius (metres), both 0 for every non-deep-coal type
// (AC-6). These are SEPARATE from the blight-class field: a deep coal mine
// is not merely "high blight" — subsidence is its own risk flag, carried
// per-type, never folded into the shared blight ordinal.
func (p MineTypeParams) Subsidence() (spoilTipFootprint int, radius float64) {
	return p.SpoilTipFootprint, p.SubsidenceRadius
}

// MineType pairs a type key with its resolved parameter set (AC-1/AC-3).
// The key lives here, on the catalogue entry, NOT on MineTypeParams — the
// parameter set carries no type discriminator of its own.
type MineType struct {
	Key    string
	Params MineTypeParams
}

// Catalogue is the immutable, fully-validated mine-type catalogue loaded
// from data/minetypes.json (AC-7: all-or-nothing — a load that fails
// validation yields no partial catalogue). Resolution is a pure function of
// (data file, key); the canonical key order is the data file's declared
// array order, so no map-iteration order is ever exposed (AC-8).
type Catalogue struct {
	ordered []MineType
	byKey   map[string]MineTypeParams
}

// Resolve returns the parameter set for the named mine type. An unknown key
// yields ErrUnknownMineType — never a panic, never a silent
// default-substituted parameter set (AC-7).
func (c Catalogue) Resolve(key string) (MineTypeParams, error) {
	p, ok := c.byKey[key]
	if !ok {
		return MineTypeParams{}, errs.New(ErrUnknownMineType, errs.NewCorrelationID(), map[string]any{"key": key})
	}
	return p, nil
}

// Keys returns the catalogue's type keys in canonical (data-file) order.
// The returned slice is a copy — callers may mutate it freely.
func (c Catalogue) Keys() []string {
	out := make([]string, len(c.ordered))
	for i, mt := range c.ordered {
		out[i] = mt.Key
	}
	return out
}

// All returns the catalogue's entries in canonical order. The returned
// slice is a copy.
func (c Catalogue) All() []MineType {
	out := make([]MineType, len(c.ordered))
	copy(out, c.ordered)
	return out
}

// Len reports the number of mine types in the catalogue.
func (c Catalogue) Len() int { return len(c.ordered) }

// rawMineType is the JSON wire shape of one data/minetypes.json type entry,
// decoded only to be validated and folded into MineTypeParams. The
// disclosure/meta/$comment documentation blocks are deliberately absent
// here: they are author-facing commentary, not simulation parameters, and
// encoding/json skips them.
type rawMineType struct {
	Key               string  `json:"key"`
	Name              string  `json:"name"`
	Footprint         int     `json:"footprint"`
	OutputCommodity   string  `json:"outputCommodity"`
	OutputRate        float64 `json:"outputRate"`
	BlightClass       string  `json:"blightClass"`
	Jobs              int     `json:"jobs"`
	DepthMin          float64 `json:"depthMin"`
	DepthMax          float64 `json:"depthMax"`
	GeologyClass      string  `json:"geologyClass"`
	DepositClass      string  `json:"depositClass"`
	SpoilTipFootprint int     `json:"spoilTipFootprint"`
	SubsidenceRadius  float64 `json:"subsidenceRadius"`
}

type rawMineTypeData struct {
	Version int           `json:"version"`
	Types   []rawMineType `json:"types"`
}

// LoadMineTypes reads, decodes and validates data/minetypes.json from path,
// returning the ordered Catalogue or ErrMineTypeDataInvalid (AC-7, the MET-
// code declared in errors.go). Every failure is a registry-sourced *errs.E —
// never a panic, never a silent default substitution, never a
// partially-populated result. Wrong JSON types are rejected by typed
// decoding (GR#16).
func LoadMineTypes(path, correlationID string) (Catalogue, error) {
	var zero Catalogue
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrMineTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawMineTypeData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrMineTypeDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildCatalogue(raw, path, correlationID)
}

// buildCatalogue folds the decoded raw data into an ordered, validated
// Catalogue. The canonical order is the data file's array order (not a Go
// map), so Keys/All are deterministic (GR#21).
func buildCatalogue(raw rawMineTypeData, path, correlationID string) (Catalogue, error) {
	fail := func(field, rule string) (Catalogue, error) {
		return Catalogue{}, miningMineTypeInvalid(correlationID, field, rule)
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if len(raw.Types) == 0 {
		return fail("types", "at least one mine type is required")
	}

	ordered := make([]MineType, 0, len(raw.Types))
	byKey := make(map[string]MineTypeParams, len(raw.Types))
	for i, rt := range raw.Types {
		field := func(s string) string { return fmt.Sprintf("types[%d].%s", i, s) }

		if rt.Key == "" {
			return fail(field("key"), "required, must be a non-empty type key")
		}
		if _, dup := byKey[rt.Key]; dup {
			return fail(field("key"), "duplicate type key")
		}
		if rt.Footprint <= 0 {
			return fail(field("footprint"), "required, must be a positive cell count")
		}
		if rt.OutputCommodity == "" {
			return fail(field("outputCommodity"), "required, must name the output commodity")
		}
		// OutputRate is bounded to maxDataMagnitude (SEC-219): it is one factor
		// of the site-capacity product outputRate × capacityDays, so an
		// unbounded finite value overflows the product to +Inf and makes the
		// site inexhaustible.
		if !validFloat(rt.OutputRate, 0, maxDataMagnitude) || rt.OutputRate <= 0 {
			return fail(field("outputRate"), "required, must be a positive finite t/day rate at most 1e12")
		}
		blight, ok := blightClassByName(rt.BlightClass)
		if !ok {
			return fail(field("blightClass"), "unknown blight class (want low/moderate/high/severe)")
		}
		if rt.Jobs < 0 {
			return fail(field("jobs"), "must be >= 0 (negative jobs headcount)")
		}
		if rt.DepthMin < 0 || math.IsNaN(rt.DepthMin) || math.IsInf(rt.DepthMin, 0) {
			return fail(field("depthMin"), "must be a finite, non-negative metres value")
		}
		if rt.DepthMax <= rt.DepthMin || math.IsNaN(rt.DepthMax) || math.IsInf(rt.DepthMax, 0) {
			return fail(field("depthMax"), "must be a finite metres value greater than depthMin (inverted band)")
		}

		// Compatibility gates: canonicalise each declared class against the
		// owning enum's own name table, and require at least one gate (AC-5).
		geologyClass := ""
		if rt.GeologyClass != "" {
			g, ok := geologyGateByName(rt.GeologyClass)
			if !ok {
				return fail(field("geologyClass"), "dangling geology class ref (want chalk/clay/gravel/deep_coal)")
			}
			geologyClass = g.String()
		}
		depositClass := ""
		if rt.DepositClass != "" {
			dt, ok := depositTypeByName(rt.DepositClass)
			if !ok {
				return fail(field("depositClass"), "dangling deposit class ref: not in the deposit taxonomy")
			}
			depositClass = dt.String()
		}
		if geologyClass == "" && depositClass == "" {
			return fail(field("geologyClass|depositClass"), "a mine type must declare a sourcing gate (geologyClass and/or depositClass)")
		}
		if rt.SpoilTipFootprint < 0 {
			return fail(field("spoilTipFootprint"), "must be >= 0 cells")
		}
		if rt.SubsidenceRadius < 0 || math.IsNaN(rt.SubsidenceRadius) || math.IsInf(rt.SubsidenceRadius, 0) {
			return fail(field("subsidenceRadius"), "must be a finite, non-negative metres value")
		}

		p := MineTypeParams{
			Footprint:         rt.Footprint,
			OutputCommodity:   rt.OutputCommodity,
			OutputRate:        rt.OutputRate,
			BlightClass:       blight,
			Jobs:              rt.Jobs,
			DepthMin:          rt.DepthMin,
			DepthMax:          rt.DepthMax,
			GeologyClass:      geologyClass,
			DepositClass:      depositClass,
			SpoilTipFootprint: rt.SpoilTipFootprint,
			SubsidenceRadius:  rt.SubsidenceRadius,
		}
		ordered = append(ordered, MineType{Key: rt.Key, Params: p})
		byKey[rt.Key] = p
	}

	return Catalogue{ordered: ordered, byKey: byKey}, nil
}
