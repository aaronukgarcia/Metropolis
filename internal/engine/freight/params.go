package freight

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GR#15 data-file contract: config is the validated,
// ordered view of data/freight.json, and LoadConfig is this package's
// loader. Every tunable number the freight module consumes — port
// berths/crane-rate/hours, customs throughput, modal bulk caps, storage
// capacities, the freight-commodity taxonomy and its market/storage
// mapping, and the five production-chain families' per-stage rates/jobs/
// power/water/blight — comes from here, never from a Go literal in this
// package (GR#15). The loader is self-contained (os.ReadFile +
// encoding/json + validateConfig) so this package consumes no
// unregistered module edge for its own data file (GR#20), the same
// pattern engine.mining's LoadDepositParams uses. Loading is
// all-or-nothing: any missing/malformed/schema failure returns
// ErrFreightDataInvalid and no config — there is no partial map and no
// silent default substitution.

// Unit is a freight commodity's measure. The tonnes-conservation identity
// (AC-10) covers only UnitTonne commodities; UnitKWh/UnitLitre are tracked
// for chain completeness and pricing but excluded from that identity.
type Unit string

const (
	UnitTonne Unit = "tonne"
	UnitKWh   Unit = "kWh"
	UnitLitre Unit = "L"
)

// StorageClass is the storage-type matching class (AC-6): each of the four
// documented storage-site types accepts exactly one class, and a freight
// commodity carries the class of site it may occupy.
type StorageClass string

const (
	StorageGeneral StorageClass = "general" // quayside stacks — any commodity
	StorageGrain   StorageClass = "grain"   // silos
	StorageFuel    StorageClass = "fuel"    // tank farm
	StorageFresh   StorageClass = "fresh"   // cold store (fresh/fish)
)

// Commodity is a freight-internal commodity identifier (chalk, cement,
// grain, steel, ...) — a finer taxonomy than engine.market's nine
// commodities, each mapped to one of those nine for pricing/availability.
type Commodity string

// commodityConfig is one data/freight.json "commodities" entry, folded
// into a validated form keyed by Commodity.
type commodityConfig struct {
	Market        market.CommodityType // the engine.market commodity priced/availability-bounded through (AC-8)
	StorageClass  StorageClass
	Unit          Unit
	UnitsPerTonne int64 // market units per freight tonne (1000 for kg/L-priced, 1 for tonne-priced)
}

// Mode is a freight transport mode (AC-7): road, rail, sea.
type Mode string

const (
	ModeRoad Mode = "road"
	ModeRail Mode = "rail"
	ModeSea  Mode = "sea"
)

// modalCap is one data/freight.json "modalCaps" entry.
type modalCap struct {
	MaxTonnesPerMovement int64
	MinTonnesPerMovement int64 // sea only; zero for road/rail
	LeadTimeTicks        int64
}

// siteConfig is one data/freight.json "storage" entry.
type siteConfig struct {
	Type           SiteType
	CommodityClass StorageClass
	CapacityTonnes int64
}

// SiteType is one of the four documented storage-site types (AC-6).
type SiteType string

const (
	SiteQuayside  SiteType = "quayside"  // general cargo stacks
	SiteSilo      SiteType = "silo"      // grain
	SiteTankFarm  SiteType = "tankFarm"  // fuel
	SiteColdStore SiteType = "coldStore" // fresh/fish
)

// allSiteTypes is the ordered set of the four documented site types,
// iterated in fixed order for determinism (GR#21).
var allSiteTypes = []SiteType{SiteQuayside, SiteSilo, SiteTankFarm, SiteColdStore}

// StageInput is one per-day input commodity rate for a chain stage (AC-5).
type StageInput struct {
	Commodity    Commodity
	TonnesPerDay int64
}

// StageOutput is one per-day output commodity rate for a chain stage (AC-5).
type StageOutput struct {
	Commodity    Commodity
	TonnesPerDay int64
}

// stageConfig is one data/freight.json chain "stages" entry.
type stageConfig struct {
	ID                StageID
	Name              string
	Family            ChainFamily
	Inputs            []StageInput
	Outputs           []StageOutput
	Jobs              int64
	PowerKWhPerDay    int64
	WaterLitresPerDay int64
	BlightClass       int
}

// StageID identifies one chain stage. It is derived from data (the stage's
// "id" field), never a hardcoded enum — stages are data, per AC-4's
// "chains from data" pattern.
type StageID string

// ChainFamily is one of the five documented production-chain families
// (AC-4).
type ChainFamily string

const (
	FamilyConstruction   ChainFamily = "construction"
	FamilySteelMachinery ChainFamily = "steelAndMachinery"
	FamilyFood           ChainFamily = "food"
	FamilyConsumerGoods  ChainFamily = "consumerGoods"
	FamilyEnergy         ChainFamily = "energy"
)

// allChainFamilies is the ordered set of the five documented families.
var allChainFamilies = []ChainFamily{
	FamilyConstruction, FamilySteelMachinery, FamilyFood, FamilyConsumerGoods, FamilyEnergy,
}

// config is the fully-validated, ordered view of data/freight.json.
type config struct {
	Port struct {
		Berths                      int64
		CraneRateTonnesPerHour      int64
		OperatingHoursPerDay        int64
		CustomsCapacityTonnesPerDay int64
	}
	ModalCaps map[Mode]modalCap
	Sites     map[SiteType]siteConfig
	// commodities is keyed by Commodity; stageConfigs preserves the
	// data-file stage order (already topological within each family).
	commodities  map[Commodity]commodityConfig
	stageConfigs []stageConfig
	// canonicalSite maps a commodity's storage class to the site that
	// holds it (AC-6's matching), for the tick's auto-routing of leftover
	// production to storage.
	canonicalSite map[StorageClass]SiteType
}

// rawFreightData is data/freight.json's JSON wire shape, decoded only to
// be validated and folded into the ordered config above.
type rawFreightData struct {
	Version     int                     `json:"version"`
	Port        rawPort                 `json:"port"`
	ModalCaps   map[string]rawModal     `json:"modalCaps"`
	Storage     []rawSite               `json:"storage"`
	Commodities map[string]rawCommodity `json:"commodities"`
	Chains      map[string]rawChain     `json:"chains"`
}

type rawPort struct {
	Berths                      int64 `json:"berths"`
	CraneRateTonnesPerHour      int64 `json:"craneRateTonnesPerHour"`
	OperatingHoursPerDay        int64 `json:"operatingHoursPerDay"`
	CustomsCapacityTonnesPerDay int64 `json:"customsCapacityTonnesPerDay"`
}

type rawModal struct {
	MaxTonnesPerMovement int64 `json:"maxTonnesPerMovement"`
	MinTonnesPerMovement int64 `json:"minTonnesPerMovement"`
	LeadTimeTicks        int64 `json:"leadTimeTicks"`
}

type rawSite struct {
	Type           string `json:"type"`
	CommodityClass string `json:"commodityClass"`
	CapacityTonnes int64  `json:"capacityTonnes"`
}

type rawCommodity struct {
	Market        string `json:"market"`
	StorageClass  string `json:"storageClass"`
	Unit          string `json:"unit"`
	UnitsPerTonne int64  `json:"unitsPerTonne"`
}

type rawChain struct {
	Stages []rawStage `json:"stages"`
}

type rawStage struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Inputs            []rawIO `json:"inputs"`
	Outputs           []rawIO `json:"outputs"`
	Jobs              int64   `json:"jobs"`
	PowerKWhPerDay    int64   `json:"powerKWhPerDay"`
	WaterLitresPerDay int64   `json:"waterLitresPerDay"`
	BlightClass       int     `json:"blightClass"`
}

type rawIO struct {
	Commodity    string `json:"commodity"`
	TonnesPerDay int64  `json:"tonnesPerDay"`
}

// LoadConfig reads, decodes and validates data/freight.json from path,
// returning the ordered config or ErrFreightDataInvalid. Every failure is
// a registry-sourced *errs.E — never a panic, never a silent default.
func LoadConfig(path, correlationID string) (config, error) {
	var zero config
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrFreightDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawFreightData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrFreightDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildConfig(raw, path, correlationID)
}

func buildConfig(raw rawFreightData, path, correlationID string) (config, error) {
	fail := func(field, rule string) (config, error) {
		return config{}, errs.New(ErrFreightDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}

	var c config
	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Port (AC-2/AC-3). Berths may legitimately be zero ("port not yet
	// built" — PortCapacity then errors with ErrNoBerthsConfigured), but
	// crane rate and operating hours must be positive whenever any berth
	// exists; customs throughput must be positive whenever any berth exists
	// too, so a built port always has a customs capacity to saturate.
	c.Port.Berths = raw.Port.Berths
	c.Port.CraneRateTonnesPerHour = raw.Port.CraneRateTonnesPerHour
	c.Port.OperatingHoursPerDay = raw.Port.OperatingHoursPerDay
	c.Port.CustomsCapacityTonnesPerDay = raw.Port.CustomsCapacityTonnesPerDay
	if raw.Port.Berths < 0 {
		return fail("port.berths", "must be >= 0 (0 = port not yet built)")
	}
	if raw.Port.Berths > 0 {
		if raw.Port.CraneRateTonnesPerHour <= 0 {
			return fail("port.craneRateTonnesPerHour", "must be > 0 while berths > 0")
		}
		if raw.Port.OperatingHoursPerDay <= 0 {
			return fail("port.operatingHoursPerDay", "must be > 0 while berths > 0")
		}
		if raw.Port.CustomsCapacityTonnesPerDay <= 0 {
			return fail("port.customsCapacityTonnesPerDay", "must be > 0 while berths > 0")
		}
	}

	// Modal caps (AC-7/AC-13). Road/rail/sea are all required; the sea mode
	// additionally carries a minimum bulk size (§33: 3kt coaster).
	c.ModalCaps = make(map[Mode]modalCap, 3)
	for _, mode := range []Mode{ModeRoad, ModeRail, ModeSea} {
		r, ok := raw.ModalCaps[string(mode)]
		if !ok {
			return fail("modalCaps."+string(mode), "required modal cap entry missing")
		}
		if r.MaxTonnesPerMovement <= 0 {
			return fail("modalCaps."+string(mode)+".maxTonnesPerMovement", "must be > 0")
		}
		if r.MinTonnesPerMovement < 0 {
			return fail("modalCaps."+string(mode)+".minTonnesPerMovement", "must be >= 0")
		}
		if r.MinTonnesPerMovement > r.MaxTonnesPerMovement {
			return fail("modalCaps."+string(mode)+".minTonnesPerMovement", "must be <= maxTonnesPerMovement")
		}
		if r.LeadTimeTicks <= 0 {
			return fail("modalCaps."+string(mode)+".leadTimeTicks", "must be > 0")
		}
		c.ModalCaps[mode] = modalCap(r)
	}

	// Storage sites (AC-6): all four documented types required, each with a
	// distinct capacity and a commodity class matching its type.
	c.Sites = make(map[SiteType]siteConfig, len(allSiteTypes))
	byType := make(map[SiteType]rawSite, len(raw.Storage))
	for _, s := range raw.Storage {
		st := SiteType(s.Type)
		known := false
		for _, kt := range allSiteTypes {
			if st == kt {
				known = true
				break
			}
		}
		if !known {
			return fail("storage.type", "unknown site type (want quayside/silo/tankFarm/coldStore)")
		}
		if _, dup := byType[st]; dup {
			return fail("storage.type", "duplicate site entry for type "+string(st))
		}
		byType[st] = s
	}
	for _, st := range allSiteTypes {
		s, ok := byType[st]
		if !ok {
			return fail("storage."+string(st), "required storage site missing")
		}
		cls := StorageClass(s.CommodityClass)
		if err := siteClassMatches(st, cls); err != "" {
			return fail("storage."+string(st)+".commodityClass", err)
		}
		if s.CapacityTonnes <= 0 {
			return fail("storage."+string(st)+".capacityTonnes", "must be > 0")
		}
		c.Sites[st] = siteConfig{Type: st, CommodityClass: cls, CapacityTonnes: s.CapacityTonnes}
	}

	// Commodity taxonomy (GR#15 single source for market/storage mapping).
	c.commodities = make(map[Commodity]commodityConfig, len(raw.Commodities))
	c.canonicalSite = make(map[StorageClass]SiteType, len(allSiteTypes))
	for _, st := range allSiteTypes {
		c.canonicalSite[c.Sites[st].CommodityClass] = st
	}
	// The general class is accepted by the quayside site; grain/fuel/fresh
	// each have exactly one site. canonicalSite covers all four classes.
	commodityNames := sortedKeys(raw.Commodities)
	for _, name := range commodityNames {
		rc := raw.Commodities[name]
		mkt := market.CommodityType(rc.Market)
		if !isKnownMarketCommodity(mkt) {
			return fail("commodities."+name+".market", "unknown engine.market commodity key")
		}
		cls := StorageClass(rc.StorageClass)
		if _, ok := c.canonicalSite[cls]; !ok {
			return fail("commodities."+name+".storageClass", "unknown storage class (want general/grain/fuel/fresh)")
		}
		unit := Unit(rc.Unit)
		if unit != UnitTonne && unit != UnitKWh && unit != UnitLitre {
			return fail("commodities."+name+".unit", "unknown unit (want tonne/kWh/L)")
		}
		if rc.UnitsPerTonne <= 0 {
			return fail("commodities."+name+".unitsPerTonne", "must be > 0")
		}
		c.commodities[Commodity(name)] = commodityConfig{
			Market:        mkt,
			StorageClass:  cls,
			Unit:          unit,
			UnitsPerTonne: rc.UnitsPerTonne,
		}
	}

	// Chains (AC-4): all five families required, stages already ordered;
	// every referenced commodity must be registered above.
	rawFamilies := raw.Chains
	for _, family := range allChainFamilies {
		rc, ok := rawFamilies[string(family)]
		if !ok {
			return fail("chains."+string(family), "required chain family missing")
		}
		if len(rc.Stages) == 0 {
			return fail("chains."+string(family)+".stages", "family must have at least one stage")
		}
		seenStage := map[StageID]bool{}
		for i, rs := range rc.Stages {
			field := "chains." + string(family) + ".stages[" + itoa(i) + "]"
			if rs.ID == "" {
				return fail(field+".id", "required, must be a non-empty stage id")
			}
			if seenStage[StageID(rs.ID)] {
				return fail(field+".id", "duplicate stage id within family")
			}
			seenStage[StageID(rs.ID)] = true
			if rs.Name == "" {
				return fail(field+".name", "required, must be a non-empty stage name")
			}
			if rs.Jobs < 0 {
				return fail(field+".jobs", "must be >= 0")
			}
			if rs.PowerKWhPerDay < 0 {
				return fail(field+".powerKWhPerDay", "must be >= 0")
			}
			if rs.WaterLitresPerDay < 0 {
				return fail(field+".waterLitresPerDay", "must be >= 0")
			}
			if rs.BlightClass < 0 || rs.BlightClass > 5 {
				return fail(field+".blightClass", "must be in [0,5]")
			}
			inputs := make([]StageInput, 0, len(rs.Inputs))
			seenInputs := make(map[Commodity]bool, len(rs.Inputs))
			for _, in := range rs.Inputs {
				if _, ok := c.commodities[Commodity(in.Commodity)]; !ok {
					return fail(field+".inputs."+in.Commodity, "references an unregistered freight commodity")
				}
				// SEC-086: a stage listing the same input commodity twice would
				// let the tick over-draw the pool negative and book the deficit
				// as a negative StorageDelta — a silent tonnage leak the
				// conservation identity cannot see. Reject it at load time.
				if seenInputs[Commodity(in.Commodity)] {
					return fail(field+".inputs."+in.Commodity, "duplicate input commodity within a stage")
				}
				seenInputs[Commodity(in.Commodity)] = true
				if in.TonnesPerDay <= 0 {
					return fail(field+".inputs."+in.Commodity+".tonnesPerDay", "must be > 0")
				}
				inputs = append(inputs, StageInput{Commodity: Commodity(in.Commodity), TonnesPerDay: in.TonnesPerDay})
			}
			outputs := make([]StageOutput, 0, len(rs.Outputs))
			for _, out := range rs.Outputs {
				if _, ok := c.commodities[Commodity(out.Commodity)]; !ok {
					return fail(field+".outputs."+out.Commodity, "references an unregistered freight commodity")
				}
				if out.TonnesPerDay <= 0 {
					return fail(field+".outputs."+out.Commodity+".tonnesPerDay", "must be > 0")
				}
				outputs = append(outputs, StageOutput{Commodity: Commodity(out.Commodity), TonnesPerDay: out.TonnesPerDay})
			}
			if len(outputs) == 0 {
				return fail(field+".outputs", "stage must have at least one output")
			}
			c.stageConfigs = append(c.stageConfigs, stageConfig{
				ID:                StageID(rs.ID),
				Name:              rs.Name,
				Family:            family,
				Inputs:            inputs,
				Outputs:           outputs,
				Jobs:              rs.Jobs,
				PowerKWhPerDay:    rs.PowerKWhPerDay,
				WaterLitresPerDay: rs.WaterLitresPerDay,
				BlightClass:       rs.BlightClass,
			})
		}
	}

	return c, nil
}

// siteClassMatches returns "" if the site type and commodity class pair is
// the documented §33 pairing, else a rule string for the failure message.
func siteClassMatches(st SiteType, cls StorageClass) string {
	want := map[SiteType]StorageClass{
		SiteQuayside:  StorageGeneral,
		SiteSilo:      StorageGrain,
		SiteTankFarm:  StorageFuel,
		SiteColdStore: StorageFresh,
	}
	if want[st] != cls {
		return "site type " + string(st) + " must have commodityClass " + string(want[st])
	}
	return ""
}

// isKnownMarketCommodity reports whether m is one of engine.market's nine
// registered commodities (the single source of truth for market identities,
// GR#3 — engine.market's own exported constants, not a re-spelled list).
func isKnownMarketCommodity(m market.CommodityType) bool {
	switch m {
	case market.Water, market.Power, market.Gas, market.FoodStaples, market.FoodFresh,
		market.Fuel, market.ConstructionMaterials, market.ConsumerGoods, market.Waste:
		return true
	}
	return false
}

// sortedKeys returns m's keys in ascending order so map-driven validation
// is deterministic (GR#21 — Go map iteration order is randomized).
func sortedKeys(m map[string]rawCommodity) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
