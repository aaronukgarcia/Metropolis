package data

import (
	"fmt"
	"sort"
)

// This file is MOD-026 / engine.build's own slice of the buildings.json
// schema: the §34 eight-way zone catalogue and its construction
// economics, loaded from data/buildings.json's top-level "zones" array.
// Kept in a separate file from buildings.go (which FEAT-010/data.catalogue
// owns exclusively) for the same reason buildings.go is itself separate
// from types.go — the two catalogue halves are owned by different
// modules, and this split makes each module's field set independently
// auditable.

// ZoneMeta is the zone-catalogue meta block carried in data/buildings.json's
// top-level "meta" object. It is read (not just documentation) for the one
// numeric placeholder the engine needs beyond the zone entries themselves:
// the construction-labour application rate. The remaining meta fields
// (materialsBillUnit / labourUnit / leadTimeUnit / specRefs) are human
// documentation (AC-16) and deliberately not typed here — they document
// units of measure for data editors, not values the engine consumes.
type ZoneMeta struct {
	// LabourPerTick is the placeholder §13-F3 labour gate: how many
	// worker-days of a build order's labour requirement are satisfied per
	// build-queue tick, pending the real labour market (engine.households/
	// engine.firms — out of scope for MOD-026). Positive when a zone
	// catalogue is present; the engine rejects a non-positive value rather
	// than silently substituting a default.
	LabourPerTick int64 `json:"labourPerTick"`
}

// ZoneEntry is one §34 zone type's construction economics: its identity,
// its construction materials bill (commodity name → quantity, the "bill
// of materials"), its labour requirement, and its base lead time.
// engine.build (MOD-026) consumes this via [Buildings.Zones] — rebalancing
// construction costs is a data edit in data/buildings.json, never a Go
// literal (GR#15).
type ZoneEntry struct {
	// ID is the stable zone-type key (e.g. "dwelling", "heavyIndustry").
	// Domain: buildingIDPattern (the same lowercase-slug domain the
	// building catalogue uses, reused rather than reinvented — GR#3).
	ID string `json:"id"`
	// Name is the display name transcribed from §34 (e.g. "Heavy Industry").
	Name string `json:"name"`
	// MaterialsBill maps a §6 commodity name (at Baseline One depth,
	// exactly "constructionMaterials") to the quantity of that commodity
	// one build order for this zone type requires, in the commodity's own
	// unit (tonnes — data/buildings.json's "meta" block documents the
	// unit-of-measure convention so a downstream consumer never has to
	// guess whether the figure is tonnes, units, or days). The map shape
	// keeps the schema future-proof for multi-commodity bills; the
	// commodity names themselves are engine.build's vocabulary, so this
	// generic validator only enforces presence and non-negativity.
	MaterialsBill map[string]int64 `json:"materialsBill"`
	// Labour is the construction labour requirement in worker-days.
	Labour int64 `json:"labour"`
	// BaseLeadTimeDays is the base construction lead time in simulation
	// days (before §9's seasonal construction-speed multiplier is applied).
	BaseLeadTimeDays int64 `json:"baseLeadTimeDays"`
}

// sortedZoneCommodityNames returns the materials-bill commodity names in
// ascending order, so a validation or engine loop that ranges over the
// bill iterates deterministically rather than in Go map order (GR#21).
func sortedZoneCommodityNames(m map[string]int64) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// validateZones runs the generic (schema-level) validation for the
// "zones" array. It checks each entry's shape — slug id (unique), non-empty
// name, a non-empty materials bill whose every quantity is non-negative,
// a non-negative labour requirement, and a non-negative base lead time.
//
// It deliberately does NOT check §34's eight-type completeness or which
// commodity names are legal — that is engine.build's consumer-level
// concern (matching engine.logistics's requiredCommodities precedent),
// because this package has no notion of engine.build's vocabulary. An
// ABSENT zones array (len == 0) is valid here: not every buildings.json
// consumer needs the zone catalogue, so requiring it would break
// FEAT-010's existing building-catalogue fixtures.
func (b *Buildings) validateZones() error {
	if len(b.Zones) > 0 && b.ZoneMeta.LabourPerTick <= 0 {
		return fieldErr("meta.labourPerTick", fmt.Sprintf("must be > 0 when a zone catalogue is present, got %d", b.ZoneMeta.LabourPerTick))
	}
	seen := make(map[string]int, len(b.Zones))
	for i, z := range b.Zones {
		prefix := fmt.Sprintf("zones[%d]", i)
		idPrefix := prefix
		if z.ID != "" {
			idPrefix = fmt.Sprintf("%s(id=%s)", prefix, z.ID)
		}

		if err := requireNonEmptyString(prefix+".id", z.ID); err != nil {
			return err
		}
		if !buildingIDPattern.MatchString(z.ID) {
			return fieldErr(idPrefix+".id", fmt.Sprintf("must match %s, got %q", buildingIDPattern.String(), z.ID))
		}
		if first, dup := seen[z.ID]; dup {
			return fieldErr(idPrefix+".id", fmt.Sprintf("duplicate id (first seen at zones[%d])", first))
		}
		seen[z.ID] = i

		if err := requireNonEmptyString(idPrefix+".name", z.Name); err != nil {
			return err
		}
		if len(z.MaterialsBill) == 0 {
			return fieldErr(idPrefix+".materialsBill", "required non-empty: a zone type must declare its construction materials bill")
		}
		for _, commodity := range sortedZoneCommodityNames(z.MaterialsBill) {
			if z.MaterialsBill[commodity] < 0 {
				return fieldErr(fmt.Sprintf("%s.materialsBill[%s]", idPrefix, commodity),
					fmt.Sprintf("must be >= 0, got %d", z.MaterialsBill[commodity]))
			}
		}
		if z.Labour < 0 {
			return fieldErr(idPrefix+".labour", fmt.Sprintf("must be >= 0, got %d", z.Labour))
		}
		if z.BaseLeadTimeDays < 0 {
			return fieldErr(idPrefix+".baseLeadTimeDays", fmt.Sprintf("must be >= 0, got %d", z.BaseLeadTimeDays))
		}
	}
	return nil
}
