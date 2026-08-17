package farming

// This file is the farm-type catalogue's type model (FEAT-104): the five
// §31 facility categories as a typed enum, the per-type parameter set each
// resolves to, and the raw JSON wire shapes the loader folds. The loader and
// resolver themselves live in farmtypes.go; this file holds only the named,
// typed fields — footprint, soil band, terrain, BDI term, stocking density
// and chain output — so a balance pass edits data/farmtypes.json, never this
// package's type declarations.

// FarmType is one of the five agricultural facility categories. The
// canonical string form (String) is the exact key used in
// data/farmtypes.json's "types" object, so the enum, the data file and the
// loader stay in lock-step from one name table.
type FarmType uint8

const (
	FarmTypeArable       FarmType = iota // wheat/barley/rapeseed/potatoes
	FarmTypeLivestock                    // dairy/beef/sheep/pigs/poultry
	FarmTypeOrchard                      // apples/cherries/soft fruit
	FarmTypeMarketGarden                 // field veg/poly-tunnel salads/hops
	FarmTypeVineyard                     // vines (chalk slopes)
)

// String returns the canonical lowercase name, identical to the matching
// data/farmtypes.json "types" key.
func (t FarmType) String() string {
	switch t {
	case FarmTypeArable:
		return "arable"
	case FarmTypeLivestock:
		return "livestock"
	case FarmTypeOrchard:
		return "orchard"
	case FarmTypeMarketGarden:
		return "marketGarden"
	case FarmTypeVineyard:
		return "vineyard"
	default:
		return "unknown"
	}
}

// SoilBand is the per-type soil-quality requirement band. It is a DISTINCT
// field from the BDI term — a chalk-slope-loving vine must not sit on heavy
// clay unnoticed, so soil band and BDI term are two fields, never one folded
// "environmental score".
type SoilBand uint8

const (
	SoilBandChalkDownland SoilBand = iota // chalk-compatible downland (arable)
	SoilBandPasture                       // grazing pasture (livestock)
	SoilBandChalkSlope                    // chalk slopes (orchard/vineyard)
	SoilBandFertileLoam                   // fertile loam (market garden)
)

// String returns the canonical soil-band name, identical to the matching
// data/farmtypes.json value.
func (s SoilBand) String() string {
	switch s {
	case SoilBandChalkDownland:
		return "chalkDownland"
	case SoilBandPasture:
		return "pasture"
	case SoilBandChalkSlope:
		return "chalkSlope"
	case SoilBandFertileLoam:
		return "fertileLoam"
	default:
		return "unknown"
	}
}

// TerrainPreference is the per-type terrain/slope preference: where the spec
// names a chalk-slope requirement (vines/orchard) it is a placement
// consequence, not flavour text.
type TerrainPreference uint8

const (
	TerrainOpenDownland TerrainPreference = iota
	TerrainGrazingDownland
	TerrainChalkSlope
	TerrainShelteredValley
)

// String returns the canonical terrain-preference name, identical to the
// matching data/farmtypes.json value.
func (t TerrainPreference) String() string {
	switch t {
	case TerrainOpenDownland:
		return "openDownland"
	case TerrainGrazingDownland:
		return "grazingDownland"
	case TerrainChalkSlope:
		return "chalkSlope"
	case TerrainShelteredValley:
		return "shelteredValley"
	default:
		return "unknown"
	}
}

// ChainDestination is the downstream chain a farm type hands off to: the five
// named destinations (mill, dairy, abattoir, packhouse, winery).
type ChainDestination uint8

const (
	ChainMill ChainDestination = iota
	ChainDairy
	ChainAbattoir
	ChainPackhouse
	ChainWinery
)

// String returns the canonical destination name, identical to the matching
// data/farmtypes.json value.
func (c ChainDestination) String() string {
	switch c {
	case ChainMill:
		return "mill"
	case ChainDairy:
		return "dairy"
	case ChainAbattoir:
		return "abattoir"
	case ChainPackhouse:
		return "packhouse"
	case ChainWinery:
		return "winery"
	default:
		return "unknown"
	}
}

// ChainOutput is the typed chain handoff: a commodity name plus the
// downstream destination chain it routes to. It is not a single
// undifferentiated "farm output" figure — the destination is part of the
// type's parameter set.
type ChainOutput struct {
	Commodity   string
	Destination ChainDestination
}

// StockingDensity is the livestock-only per-variant stocking parameter:
// head per cell for one named animal variant. Non-livestock types carry no
// stocking field at all (the field is livestock-typed, not a shared zero).
type StockingDensity struct {
	Variant     string
	HeadPerCell float64
}

// Variant is one named crop/livestock within a category: a variant, not its
// own top-level type. Chain, when non-nil, overrides the type-level chain
// for this variant (dairy → milk → dairy plant, distinct from the livestock
// type's livestock → abattoir handoff).
type Variant struct {
	Name  string
	Chain *ChainOutput
}

// FarmTypeParams is the resolved parameter set for one farm type. Footprint,
// soil band, BDI term, stocking density and chain output are all named,
// typed fields — there is no string discriminator field standing in for the
// parameters themselves. Stocking is non-nil ONLY for livestock.
type FarmTypeParams struct {
	Type      FarmType
	Name      string
	Footprint int // cells
	SoilBand  SoilBand
	Terrain   TerrainPreference
	BDITerm   float64 // additive per-type BDI contribution (AC-5)
	Chain     ChainOutput
	Variants  []Variant
	Stocking  []StockingDensity // non-nil only for livestock (AC-6)
}

// StockingFor returns the stocking density for a named livestock variant.
// ok is false when the type carries no stocking table or the variant is not
// one of the livestock entries.
func (p FarmTypeParams) StockingFor(variant string) (StockingDensity, bool) {
	for _, s := range p.Stocking {
		if s.Variant == variant {
			return s, true
		}
	}
	return StockingDensity{}, false
}

// ChainFor returns the effective chain output for a named variant: the
// variant's own override when present, otherwise the type-level chain.
func (p FarmTypeParams) ChainFor(variant string) ChainOutput {
	for _, v := range p.Variants {
		if v.Name == variant && v.Chain != nil {
			return *v.Chain
		}
	}
	return p.Chain
}

// rawFarmTypesData is the JSON wire shape of data/farmtypes.json, decoded
// only to be validated and folded into the ordered catalogue in farmtypes.go.
type rawFarmTypesData struct {
	Version int                    `json:"version"`
	Types   map[string]rawFarmType `json:"types"`
}

type rawFarmType struct {
	Footprint int           `json:"footprintCells"`
	SoilBand  string        `json:"soilBand"`
	Terrain   string        `json:"terrain"`
	BDITerm   float64       `json:"bdiTerm"`
	Chain     rawChain      `json:"chain"`
	Variants  []rawVariant  `json:"variants"`
	Stocking  []rawStocking `json:"stocking"`
}

type rawChain struct {
	Commodity   string `json:"commodity"`
	Destination string `json:"destination"`
}

type rawVariant struct {
	Name  string    `json:"name"`
	Chain *rawChain `json:"chain"`
}

type rawStocking struct {
	Variant     string  `json:"variant"`
	HeadPerCell float64 `json:"headPerCell"`
}
