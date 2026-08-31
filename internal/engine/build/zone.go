package build

import (
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// ZoneType is one of §34's eight land types. The string value is the
// zone's stable catalogue key in data/buildings.json's "zones" array (a
// lowercase slug, matching the building catalogue's id convention) — the
// same value a consumer or a save file uses to name the zone, mirroring
// engine.market's CommodityType pattern (JSON identity == Go identity).
//
// The eight NAMES are §34's fixed vocabulary (spec constants, exactly
// like engine.logistics's requiredCommodities); the CONSTRUCTION ECONOMICS
// (materials bill, labour, lead time) are data, loaded from
// data/buildings.json — never a Go literal (GR#15, AC-8).
type ZoneType string

const (
	ZoneDwelling      ZoneType = "dwelling"
	ZoneShop          ZoneType = "shop"
	ZoneOffice        ZoneType = "office"
	ZoneEntertainment ZoneType = "entertainment"
	ZoneFarming       ZoneType = "farming"
	ZoneManufacturing ZoneType = "manufacturing"
	ZoneHeavyIndustry ZoneType = "heavy_industry"
	ZoneMining        ZoneType = "mining"
)

// requiredZoneTypes is the §34 eight-way catalogue in a fixed order —
// exactly the eight types Load requires present in data/buildings.json's
// "zones" array (AC-2). A slice, not a map, so iteration order is
// deterministic (GR#21).
var requiredZoneTypes = []ZoneType{
	ZoneDwelling, ZoneShop, ZoneOffice, ZoneEntertainment,
	ZoneFarming, ZoneManufacturing, ZoneHeavyIndustry, ZoneMining,
}

// zoneRecord is one zone type's loaded construction economics, decoded from
// a data.ZoneEntry and kept unexported — the only way a consumer reads it
// is through BuildAPI's exported queries.
type zoneRecord struct {
	name             string
	materials        int64 // construction-materials quantity (tonnes)
	labour           int64 // worker-days
	baseLeadTimeDays int64 // simulation days
}

// DemandReason is a §34 demand-bar cause code (US-2): why a zone's demand
// is unfilled. It is an open string enum, not a closed set — a future
// module (power network, labour market) may add a cause without a change
// to this type. The three §34-named causes are defined here so a consumer
// can match them exactly.
type DemandReason string

const (
	// ReasonNoLabour: demand unfilled for want of labour (§34).
	ReasonNoLabour DemandReason = "no-labour"
	// ReasonNoPower: demand unfilled for want of power (§34).
	ReasonNoPower DemandReason = "no-power"
	// ReasonNoFreightCapacity: demand unfilled for want of freight
	// capacity (§34).
	ReasonNoFreightCapacity DemandReason = "no-freight-capacity"
)

// demandReasonOrder is the fixed order DemandReason codes are reported in,
// so a DemandBar's Reasons slice is deterministic regardless of which
// combination of starved inputs produced it (GR#21).
var demandReasonOrder = []DemandReason{
	ReasonNoLabour, ReasonNoPower, ReasonNoFreightCapacity,
}

// DemandInput reports the current constraint signals feeding one zone's
// demand bar, populated by the composition root from the modules that own
// each signal (labour → engine.households/firms, power → engine.consumption,
// freight → engine.logistics). engine.build exposes the reason codes but
// does not itself compute the underlying network state (out of scope).
type DemandInput struct {
	// Unfilled is the magnitude of the zone's unfilled demand (a count).
	Unfilled int64
	// LabourStarved is true when labour is the binding constraint.
	LabourStarved bool
	// PowerStarved is true when power is the binding constraint.
	PowerStarved bool
	// FreightStarved is true when freight capacity is the binding constraint.
	FreightStarved bool
}

// DemandBar is a zone's self-explaining demand reading (AC-5): the unfilled
// magnitude alongside the reason codes explaining WHY it is unfilled, in
// deterministic order. A bare magnitude would be §34's "mute bar"; the
// Reasons slice is what makes the diagnosis actionable.
type DemandBar struct {
	Zone     ZoneType
	Unfilled int64
	Reasons  []DemandReason
}

// reasonsFor derives the ordered reason codes from a DemandInput. It maps
// the three starved flags onto the §34 reason codes in demandReasonOrder,
// so the same inputs always produce the same slice.
func reasonsFor(in DemandInput) []DemandReason {
	var out []DemandReason
	for _, r := range demandReasonOrder {
		switch r {
		case ReasonNoLabour:
			if in.LabourStarved {
				out = append(out, r)
			}
		case ReasonNoPower:
			if in.PowerStarved {
				out = append(out, r)
			}
		case ReasonNoFreightCapacity:
			if in.FreightStarved {
				out = append(out, r)
			}
		}
	}
	return out
}

// buildCatalogue maps the eight §34 zone types onto their loaded
// data.ZoneEntry records, validating completeness (every required type
// present, no unrecognised type) and the materials-bill shape (exactly the
// constructionMaterials commodity, non-negative) before returning. This is
// the consumer-level check foundation.data's generic schema validation
// cannot do (matching engine.logistics's requiredCommodities precedent):
// foundation.data validates each entry's shape, this function validates the
// SET is exactly §34's eight types and the bill is the one commodity
// Baseline One consumes. The returned error is a plain descriptive error;
// Load wraps it into the registry-sourced ErrZoneDataInvalid.
func buildCatalogue(zones []data.ZoneEntry) (map[ZoneType]zoneRecord, error) {
	if len(zones) == 0 {
		return nil, fmt.Errorf("zones array is empty; the eight §34 zone types must be present")
	}
	byID := make(map[string]data.ZoneEntry, len(zones))
	for _, z := range zones {
		byID[z.ID] = z
	}

	catalogue := make(map[ZoneType]zoneRecord, len(requiredZoneTypes))
	for _, zt := range requiredZoneTypes {
		z, ok := byID[string(zt)]
		if !ok {
			return nil, fmt.Errorf("missing required zone type %q", string(zt))
		}
		materials, err := constructionMaterialsFor(z.MaterialsBill)
		if err != nil {
			return nil, fmt.Errorf("zone %q: %w", string(zt), err)
		}
		catalogue[zt] = zoneRecord{
			name:             z.Name,
			materials:        materials,
			labour:           z.Labour,
			baseLeadTimeDays: z.BaseLeadTimeDays,
		}
	}

	// Reject any zone present in the data but NOT one of the eight §34
	// types (AC-11's "unrecognised zone-type string" — a data-authoring
	// typo like "indutry" must fail loudly, never silently load a dead zone).
	for id := range byID {
		recognised := false
		for _, zt := range requiredZoneTypes {
			if id == string(zt) {
				recognised = true
				break
			}
		}
		if !recognised {
			return nil, fmt.Errorf("unrecognised zone type %q", id)
		}
	}

	return catalogue, nil
}

// constructionMaterialsFor validates a zone's materials bill names exactly
// the constructionMaterials commodity (the only commodity Baseline One
// build consumes — a multi-commodity bill is a later-sprint extension, not
// silently ignored today) and returns its quantity. Commodity identity is
// read from market.ConstructionMaterials (GR#3 single source of truth), not
// a re-spelled string literal.
func constructionMaterialsFor(bill map[string]int64) (int64, error) {
	qty, ok := bill[string(market.ConstructionMaterials)]
	if !ok {
		return 0, fmt.Errorf("materials bill must include the constructionMaterials commodity")
	}
	if qty < 0 {
		return 0, fmt.Errorf("constructionMaterials quantity must be >= 0, got %d", qty)
	}
	if len(bill) != 1 {
		names := make([]string, 0, len(bill))
		for k := range bill {
			names = append(names, k)
		}
		sort.Strings(names)
		return 0, fmt.Errorf("unsupported materials-bill commodities %v (Baseline One consumes constructionMaterials only)", names)
	}
	return qty, nil
}

// industryZoneTypes are the §34 zone types classified as "industry" for
// FEAT-1972079927 inc2's builders'-merchant auto-placement trigger
// (Aaron's 2026-08-31 ruling): manufacturing, heavy industry, and mining.
// ZoneFarming is tracked separately by [BuildAPI.IndustryAndFarmsPresent].
var industryZoneTypes = []ZoneType{ZoneManufacturing, ZoneHeavyIndustry, ZoneMining}

// sortedZoneTypes returns the loaded zone types in ascending order — the
// deterministic order every exported method that ranges over the catalogue
// map uses (a Go map's iteration order is intentionally randomised, GR#21).
func sortedZoneTypes(m map[ZoneType]zoneRecord) []ZoneType {
	out := make([]ZoneType, 0, len(m))
	for zt := range m {
		out = append(out, zt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
