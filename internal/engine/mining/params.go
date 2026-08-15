package mining

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GR#15 data-file contract (AC-6): DepositParams is the
// validated, ordered view of data/deposits.json, and LoadDepositParams is
// this package's loader. Every tunable number the shuffle consumes —
// deposit counts (as selection weights), size/density-curve shape, per-type
// depth bands, co-location factors, and the coalfield generosity — comes
// from here, never from a Go literal in the shuffle (AC-6). The loader is
// self-contained (os.ReadFile + encoding/json + this file's Validate) so
// this package consumes no unregistered module edge (GR#20): it imports
// only internal/foundation/errs, the same universal primitive engine.world
// imports. Loading is all-or-nothing (AC-11): any missing/malformed/schema
// failure returns ErrDepositDataInvalid and no DepositParams — there is no
// partial map and no silent default substitution.

// ResourceClass is the broad family a resource belongs to, read from
// data/deposits.json's "class" field. It drives nothing on its own in the
// shuffle (placement rules key off DepositType and offshore flags); it is
// carried so a future balance or survey pass can classify without
// re-parsing the taxonomy.
type ResourceClass uint8

const (
	ClassOre ResourceClass = iota
	ClassHydrocarbon
	ClassCoal
	ClassFictional
)

// classByName maps a data/deposits.json "class" string to its ResourceClass.
var classByName = map[string]ResourceClass{
	"ore":         ClassOre,
	"hydrocarbon": ClassHydrocarbon,
	"coal":        ClassCoal,
	"fictional":   ClassFictional,
}

// maxDataMagnitude is the upper bound on any single float magnitude read
// from data/deposits.json (countWeight, curve min/max, co-location factors,
// coalfield generosity). It is an overflow guard, not a balance value: a
// magnitude far above it (e.g. ~1e308, a valid float64) overflows to
// +Inf/NaN once shuffle arithmetic multiplies or subtracts it, silently
// degenerating the draw (Finding 3). A hostile or corrupt data edit is
// therefore rejected at load time rather than let through to produce
// +Inf/NaN deposit attributes.
const maxDataMagnitude = 1e12

// ResourceParams is one resource's tunable column, in canonical enum
// order (see DepositParams.Resources).
type ResourceParams struct {
	Type        DepositType
	Class       ResourceClass
	CountWeight float64 // relative weight in per-cell type selection
	DepthMin    float64 // metres — inclusive floor of the realistic band
	DepthMax    float64 // metres — exclusive ceiling of the realistic band
	Offshore    bool    // may be placed on sea cells (gas/oil only today)
}

// CurveParams shapes one independently-sampled numeric field (AC-4's size
// and density curves).
type CurveParams struct {
	Shape float64
	Min   float64
	Max   float64
}

// CoLocationParams are the geology-aware bias strengths (AC-5), all
// read from data/deposits.json's "coLocation" object.
type CoLocationParams struct {
	ChalkUraniumFactor float64 // uranium weight multiplier in pure-chalk tiles
	CoalGasFactor      float64 // gas weight multiplier in coal-measures tiles
	CoalCoalFactor     float64 // coal weight multiplier in coal-measures tiles
}

// CoalfieldParams is the East Kent "don't be stingy" calibration (AC-7).
type CoalfieldParams struct {
	GenerosityMultiplier float64 // extra coal multiplier in coal measures
	CoverageFloor        float64 // minimum fraction of coal-measures tiles
	// that must carry at least one coal deposit (AC-7's checkable floor)
}

// DepositParams is the fully-validated, ordered deposit configuration.
type DepositParams struct {
	Version           int
	DepositRate       float64 // base probability a land cell holds any deposit
	OffshoreRate      float64 // base probability a sea cell holds any deposit
	Resources         []ResourceParams
	SizeCurve         CurveParams
	DensityCurve      CurveParams
	CoLocation        CoLocationParams
	EastKentCoalfield CoalfieldParams
}

// rawDepositData is the JSON wire shape of data/deposits.json, decoded
// only to be validated and folded into the ordered DepositParams above.
type rawDepositData struct {
	Version      int                    `json:"version"`
	DepositRate  float64                `json:"depositRate"`
	OffshoreRate float64                `json:"offshoreRate"`
	Resources    map[string]rawResource `json:"resources"`
	SizeCurve    rawCurve               `json:"sizeCurve"`
	DensityCurve rawCurve               `json:"densityCurve"`
	CoLocation   rawCoLocation          `json:"coLocation"`
	EastKent     rawCoalfield           `json:"eastKentCoalfield"`
}

type rawResource struct {
	Class       string  `json:"class"`
	CountWeight float64 `json:"countWeight"`
	DepthMin    float64 `json:"depthMin"`
	DepthMax    float64 `json:"depthMax"`
	Offshore    bool    `json:"offshore"`
}

type rawCurve struct {
	Shape float64 `json:"shape"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
}

type rawCoLocation struct {
	ChalkUraniumFactor float64 `json:"chalkUraniumFactor"`
	CoalGasFactor      float64 `json:"coalGasFactor"`
	CoalCoalFactor     float64 `json:"coalCoalFactor"`
}

type rawCoalfield struct {
	GenerosityMultiplier float64 `json:"generosityMultiplier"`
	CoverageFloor        float64 `json:"coverageFloor"`
}

// LoadDepositParams reads, decodes and validates data/deposits.json from
// path, returning the ordered DepositParams or ErrDepositDataInvalid
// (AC-11). Every failure is a registry-sourced *errs.E — never a panic,
// never a silent default substitution, never a partially-populated result.
func LoadDepositParams(path, correlationID string) (DepositParams, error) {
	var zero DepositParams
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrDepositDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawDepositData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrDepositDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildParams(raw, path, correlationID)
}

// buildParams folds the decoded raw data into an ordered, validated
// DepositParams. The canonical resource order is the DepositType enum
// order (Copper..Arcana), NOT the JSON object's key order (Go map
// iteration is randomised), so the shuffle's type-selection loop is
// deterministic (GR#21).
func buildParams(raw rawDepositData, path, correlationID string) (DepositParams, error) {
	var p DepositParams
	p.Version = raw.Version
	p.DepositRate = raw.DepositRate
	p.OffshoreRate = raw.OffshoreRate
	p.SizeCurve = CurveParams{Shape: raw.SizeCurve.Shape, Min: raw.SizeCurve.Min, Max: raw.SizeCurve.Max}
	p.DensityCurve = CurveParams{Shape: raw.DensityCurve.Shape, Min: raw.DensityCurve.Min, Max: raw.DensityCurve.Max}
	p.CoLocation = CoLocationParams{
		ChalkUraniumFactor: raw.CoLocation.ChalkUraniumFactor,
		CoalGasFactor:      raw.CoLocation.CoalGasFactor,
		CoalCoalFactor:     raw.CoLocation.CoalCoalFactor,
	}
	p.EastKentCoalfield = CoalfieldParams{
		GenerosityMultiplier: raw.EastKent.GenerosityMultiplier,
		CoverageFloor:        raw.EastKent.CoverageFloor,
	}

	fail := func(field, rule string) (DepositParams, error) {
		return DepositParams{}, errs.New(ErrDepositDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if raw.DepositRate < 0 || raw.DepositRate > 1 {
		return fail("depositRate", "must be in [0,1]")
	}
	if raw.OffshoreRate < 0 || raw.OffshoreRate > 1 {
		return fail("offshoreRate", "must be in [0,1]")
	}

	// Out-of-taxonomy keys are rejected before the ordered fold (AC-11's
	// own schema example names this failure): every resource key must
	// resolve to a known DepositType, and every DepositType must be present.
	byName := make(map[DepositType]rawResource, len(raw.Resources))
	for key, r := range raw.Resources {
		dt, ok := depositTypeByName(key)
		if !ok {
			return fail("resources."+key, "unknown resource key: not in the deposit taxonomy")
		}
		if _, dup := byName[dt]; dup {
			return fail("resources."+key, "duplicate resource entry (name aliases a known type)")
		}
		byName[dt] = r
	}

	offshoreCount := 0
	p.Resources = make([]ResourceParams, 0, len(byName))
	for dt := DepositCopper; dt <= DepositArcana; dt++ {
		r, ok := byName[dt]
		if !ok {
			return fail("resources."+dt.String(), "missing required resource entry")
		}
		class, ok := classByName[r.Class]
		if !ok {
			return fail("resources."+dt.String()+".class", "unknown class (want ore/hydrocarbon/coal/fictional)")
		}
		// AC-3 is enforced as a SCHEMA invariant here, not only as a runtime
		// chooseType filter (Finding 2): a data file that marks a metallic ore
		// offshore-capable would otherwise load clean and place ore on sea, so
		// it is rejected at load time — all-or-nothing, GR#15.
		if dt.IsMetal() && r.Offshore {
			return fail("resources."+dt.String()+".offshore", "metallic ores cannot be offshore-capable: ores are never placed on sea cells (AC-3)")
		}
		if r.CountWeight < 0 || r.CountWeight > maxDataMagnitude {
			return fail("resources."+dt.String()+".countWeight", "must be in [0, 1e12]")
		}
		if r.DepthMin < 0 {
			return fail("resources."+dt.String()+".depthMin", "must be >= 0")
		}
		if r.DepthMax <= r.DepthMin {
			return fail("resources."+dt.String()+".depthMax", "must be greater than depthMin (inverted band)")
		}
		if r.Offshore {
			offshoreCount++
		}
		p.Resources = append(p.Resources, ResourceParams{
			Type:        dt,
			Class:       class,
			CountWeight: r.CountWeight,
			DepthMin:    r.DepthMin,
			DepthMax:    r.DepthMax,
			Offshore:    r.Offshore,
		})
	}
	if offshoreCount == 0 {
		return fail("resources", "no resource is offshore-capable; sea cells would be permanently empty")
	}

	if raw.SizeCurve.Shape <= 0 {
		return fail("sizeCurve.shape", "must be > 0")
	}
	if raw.SizeCurve.Min < 0 || raw.SizeCurve.Min > maxDataMagnitude {
		return fail("sizeCurve.min", "must be in [0, 1e12]")
	}
	if raw.SizeCurve.Max > maxDataMagnitude {
		return fail("sizeCurve.max", "must be <= 1e12")
	}
	if raw.SizeCurve.Max <= raw.SizeCurve.Min {
		return fail("sizeCurve", "must have 0 <= min < max")
	}
	if raw.DensityCurve.Shape <= 0 {
		return fail("densityCurve.shape", "must be > 0")
	}
	if raw.DensityCurve.Min < 0 || raw.DensityCurve.Min > maxDataMagnitude {
		return fail("densityCurve.min", "must be in [0, 1e12]")
	}
	if raw.DensityCurve.Max > maxDataMagnitude {
		return fail("densityCurve.max", "must be <= 1e12")
	}
	if raw.DensityCurve.Max <= raw.DensityCurve.Min {
		return fail("densityCurve", "must have 0 <= min < max")
	}

	// The four geology-bias factors below each feed chooseType's
	// `w := countWeight * geologyFactor(...)` (shuffle.go). With only a
	// lower bound, a hostile magnitude like 1e308 survives validation and
	// overflows to +Inf the moment it is multiplied — the weight total then
	// goes +Inf/NaN and every draw falls through to the last candidate
	// (arcana), silently collapsing coal-measures/chalk geology into one
	// type (Finding 3's class). They get the same maxDataMagnitude upper
	// bound as countWeight and the curves, so no factor — and no product of
	// factors — exceeds 1e12 before the shuffle multiplies it again.
	if raw.CoLocation.ChalkUraniumFactor < 0 || raw.CoLocation.ChalkUraniumFactor > maxDataMagnitude {
		return fail("coLocation.chalkUraniumFactor", "must be in [0, 1e12]")
	}
	if raw.CoLocation.CoalGasFactor < 0 || raw.CoLocation.CoalGasFactor > maxDataMagnitude {
		return fail("coLocation.coalGasFactor", "must be in [0, 1e12]")
	}
	if raw.CoLocation.CoalCoalFactor < 0 || raw.CoLocation.CoalCoalFactor > maxDataMagnitude {
		return fail("coLocation.coalCoalFactor", "must be in [0, 1e12]")
	}
	if raw.EastKent.GenerosityMultiplier < 1 || raw.EastKent.GenerosityMultiplier > maxDataMagnitude {
		return fail("eastKentCoalfield.generosityMultiplier", "must be in [1, 1e12]")
	}
	if raw.EastKent.CoverageFloor < 0 || raw.EastKent.CoverageFloor > 1 {
		return fail("eastKentCoalfield.coverageFloor", "must be in [0,1]")
	}

	return p, nil
}

// depositTypeByName resolves a data/deposits.json resource key to its
// DepositType, iterating the enum's canonical String names (GR#15: the
// taxonomy is derived from the enum, not a second hand-maintained list).
func depositTypeByName(name string) (DepositType, bool) {
	for dt := DepositCopper; dt <= DepositArcana; dt++ {
		if dt.String() == name {
			return dt, true
		}
	}
	return 0, false
}

// DepthBand returns the data-sourced realistic depth band for dt
// (AC-2/AC-4): the [min, max) metres window a deposit of that type must
// fall inside. ok is false for an unrecognised type.
func (p DepositParams) DepthBand(dt DepositType) (min, max float64, ok bool) {
	for _, r := range p.Resources {
		if r.Type == dt {
			return r.DepthMin, r.DepthMax, true
		}
	}
	return 0, 0, false
}

// Resource returns the ResourceParams for dt, ok false if absent.
func (p DepositParams) Resource(dt DepositType) (ResourceParams, bool) {
	for _, r := range p.Resources {
		if r.Type == dt {
			return r, true
		}
	}
	return ResourceParams{}, false
}
