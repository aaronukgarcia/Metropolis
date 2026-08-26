package data

import (
	"encoding/json"
	"sort"
	"strconv"
)

// This file defines the typed struct for each of the §24 config files.
// Consumption and Seasonal carry the fields spec-stated in §17.1/§17.2
// and §17.1's seasonal modifiers respectively. Modes and Policies are
// intentionally minimal versioned skeletons — their full schemas firm
// up alongside the engine module that owns each domain (engine.market/
// engine.roads for Modes, engine.policies for Policies) — see
// foundation.data.md's Out of scope section. Each skeleton's container
// field is present and typed so a seed fixture is genuinely
// schema-conformant, but is expected to be empty until its owning
// module populates it. The richer types (Buildings, UnlockTrees,
// NamingCorpus, ExternalWorld) live in their own files — buildings.go,
// unlock_trees.go, naming_corpus.go, external_world.go — matching the
// split each owning module's acceptance item introduced.

// --- consumption.json (§17) ---------------------------------------------

// Consumption is the §17 resource-consumption coefficient table: the
// per-person residential baseline (§17.1) plus per-occupied-unit
// coefficients by building class (§17.2). No utility number is ever
// hardcoded in Go — this struct is the sole source engine.consumption
// (and any validator checking a consumption expectation, per GR#15)
// must read.
type Consumption struct {
	Version int `json:"version"`

	// Residential is the §17.1 per-person-per-day baseline, before
	// seasonal modifiers (applied via Seasonal).
	Residential ResidentialBaseline `json:"residential"`

	// Classes maps a building class key (e.g. "school", "hospital",
	// "office") to its §17.2 per-occupied-unit coefficients.
	Classes map[string]ClassCoefficients `json:"classes"`
}

// ResidentialBaseline is §17.1's per-person-per-day baseline.
type ResidentialBaseline struct {
	WaterLitresPerPersonPerDay      float64 `json:"waterLitresPerPersonPerDay"`
	ElectricityKWhPerPersonPerDay   float64 `json:"electricityKWhPerPersonPerDay"`
	GasKWhPerPersonPerDay           float64 `json:"gasKWhPerPersonPerDay"`
	FoodStaplesKgPerPersonPerDay    float64 `json:"foodStaplesKgPerPersonPerDay"`
	FoodFreshKgPerPersonPerDay      float64 `json:"foodFreshKgPerPersonPerDay"`
	HouseholdWasteKgPerPersonPerDay float64 `json:"householdWasteKgPerPersonPerDay"`
	WastewaterFractionOfWater       float64 `json:"wastewaterFractionOfWater"` // §17: "~95% rule"
}

// ClassCoefficients is §17.2's per-occupied-unit row: unit is the
// occupancy/throughput measure the coefficients scale by (e.g.
// "pupil", "bed", "worker", "m2Sales/10"), matching the catalogue's
// building class definition (owned by data.catalogue / FEAT-010).
type ClassCoefficients struct {
	Unit    string  `json:"unit"`
	WaterL  float64 `json:"waterL"`
	ElecKWh float64 `json:"elecKWh"`
	GasKWh  float64 `json:"gasKWh"`
	WasteKg float64 `json:"wasteKg"`
}

// Validate implements Validator.
func (c *Consumption) Validate() error {
	if err := requireVersion(c.Version); err != nil {
		return err
	}
	r := c.Residential
	if err := requireNonNegative("residential.waterLitresPerPersonPerDay", r.WaterLitresPerPersonPerDay); err != nil {
		return err
	}
	if err := requireNonNegative("residential.electricityKWhPerPersonPerDay", r.ElectricityKWhPerPersonPerDay); err != nil {
		return err
	}
	if err := requireNonNegative("residential.gasKWhPerPersonPerDay", r.GasKWhPerPersonPerDay); err != nil {
		return err
	}
	if err := requireNonNegative("residential.foodStaplesKgPerPersonPerDay", r.FoodStaplesKgPerPersonPerDay); err != nil {
		return err
	}
	if err := requireNonNegative("residential.foodFreshKgPerPersonPerDay", r.FoodFreshKgPerPersonPerDay); err != nil {
		return err
	}
	if err := requireNonNegative("residential.householdWasteKgPerPersonPerDay", r.HouseholdWasteKgPerPersonPerDay); err != nil {
		return err
	}
	if r.WastewaterFractionOfWater < 0 || r.WastewaterFractionOfWater > 1 {
		return fieldErr("residential.wastewaterFractionOfWater", "must be in [0,1]")
	}
	// Iterate class keys in a deterministic (sorted) order rather than
	// ranging over the map directly (Go map iteration order is randomised
	// per-run) so that, given the SAME malformed consumption.json with
	// multiple violating classes, the FIRST violation returned - and
	// therefore which class the MET-F604 error blames - is stable across
	// runs and across POOL-SIM worker counts. BUG-098 class; same fix
	// market.go/logistics.go/refuse.go/tax_instruments.go already carry.
	keys := make([]string, 0, len(c.Classes))
	for key := range c.Classes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cls := c.Classes[key]
		if err := requireNonEmptyString("classes["+key+"].unit", cls.Unit); err != nil {
			return err
		}
		if err := requireNonNegative("classes["+key+"].waterL", cls.WaterL); err != nil {
			return err
		}
		if err := requireNonNegative("classes["+key+"].elecKWh", cls.ElecKWh); err != nil {
			return err
		}
		if err := requireNonNegative("classes["+key+"].gasKWh", cls.GasKWh); err != nil {
			return err
		}
		if err := requireNonNegative("classes["+key+"].wasteKg", cls.WasteKg); err != nil {
			return err
		}
	}
	return nil
}

// --- seasonal.json (§17.1's seasonal modifiers, §9) ----------------------

// Seasonal holds named monthly-multiplier curves applied on top of
// Consumption's baseline rates (e.g. "gas" ×2.2 Jan / ×0.2 Jul,
// "waterSummerPeak" +25%, "electricityWinter" +15% — the three
// spec-stated curves in §17.1). Curves is keyed by curve name so new
// curves (e.g. per-class seasonal profiles for farms/quarries, §17.2)
// can be added without a struct change.
type Seasonal struct {
	Version int                     `json:"version"`
	Curves  map[string]MonthlyCurve `json:"curves"`
	// Meta is data/seasonal.json's documentation block (month-index
	// convention, curve inventory prose). Declared explicitly — never
	// consumed — because the BUG-281 r2 strict loader rejects undeclared
	// fields and only strips $-prefixed keys at the top level.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// MonthlyCurve is exactly 12 multipliers, index 0 = January.
type MonthlyCurve struct {
	Comment     string    `json:"comment,omitempty"`
	Multipliers []float64 `json:"multipliers"`
}

// Validate implements Validator.
func (s *Seasonal) Validate() error {
	if err := requireVersion(s.Version); err != nil {
		return err
	}
	// Iterate curve names in a deterministic (sorted) order rather than
	// ranging over the map directly (Go map iteration order is randomised
	// per-run) so the first violation reported for a multi-curve malformed
	// file is stable across runs - BUG-098 class, same fix as Consumption
	// above and market.go's precedent.
	names := make([]string, 0, len(s.Curves))
	for name := range s.Curves {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		curve := s.Curves[name]
		if err := requireLen("curves["+name+"].multipliers", curve.Multipliers, 12); err != nil {
			return err
		}
		for i, m := range curve.Multipliers {
			if m < 0 {
				return fieldErr(monthField(name, i), "must be >= 0")
			}
		}
	}
	return nil
}

func monthField(curve string, month int) string {
	return "curves[" + curve + "].multipliers[" + itoa(month) + "]"
}

// --- modes.json (§19.1) — skeleton ---------------------------------------

// Modes is a minimal versioned skeleton for the §19.1 transport mode
// table (walk/bicycle/motorbike/car/taxi/minibus/bus/tram/metro/heavy
// rail/ferry/air: speed, capacity, road/track load, unlock tier). The
// per-mode fields below firm up alongside engine.roads/engine.market's
// nested-logit mode-choice implementation — this seed only proves the
// container round-trips and validates.
//
// TODO(engine.roads/engine.market, §19.1): replace Entries' minimal
// shape with the full per-mode schema (speedKmh, capacityPerUnit,
// roadLoadPCU, unlockTier, ...) once that module lands.
type Modes struct {
	Version int         `json:"version"`
	Entries []ModeEntry `json:"entries"`
}

// ModeEntry is a placeholder row; Key is the only field currently
// enforced (must be non-empty when present).
type ModeEntry struct {
	Key string `json:"key"`
}

// Validate implements Validator.
func (m *Modes) Validate() error {
	if err := requireVersion(m.Version); err != nil {
		return err
	}
	for i, e := range m.Entries {
		if err := requireNonEmptyString("entries["+itoa(i)+"].key", e.Key); err != nil {
			return err
		}
	}
	return nil
}

// --- buildings.json (catalogue) -------------------------------------------
//
// Buildings/BuildingEntry (the full FEAT-010/data.catalogue schema —
// unlock gate, cost/capacity, consumptionRef, blightClass,
// appealProfile, sourcePack, supplement tagging) now live in
// buildings.go, per this file's own former TODO. Kept as a separate
// file because FEAT-010 owns it exclusively (see that item's file
// ownership note) while this file remains MOD-006's shared skeleton
// set for the other seven §24 files.

// --- unlock_trees.json (§22) — in unlock_trees.go --------------------------

// UnlockTrees/UnlockTree/UnlockNode (the full §22 Development-Point
// progression-tree schema — twelve per-category trees, each covering all
// thirteen §4 milestone tiers, with kind/DP-cost/prereq edges) now live
// in unlock_trees.go, per this file's own former TODO. Kept as a
// separate file because engine.unlocks owns the unlock economy (§22),
// matching the buildings.go split for FEAT-010/data.catalogue.

// --- naming_corpus.json (§20) — in naming_corpus.go -----------------------

// NamingCorpus/RoadSuffixes (the full §20 deterministic auto-naming
// corpus schema — Kentish road place-name list, per-road-class suffix
// table, and the file's notes) now live in naming_corpus.go, per this
// file's own former TODO. Kept as a separate file because engine.roads
// owns the naming domain (§20), matching the buildings.go split for
// FEAT-010/data.catalogue.

// --- external_world.json (§21) — in external_world.go ----------------------

// ExternalWorld/ExternalProfile (the full §21 off-map job-pool schema —
// the three named pools with era-scaled capacity curves, int64 wages,
// and transport gating) now live in external_world.go, per this file's
// own former TODO. Kept as a separate file because engine.extcommute
// owns the off-map commuting domain (§21), matching the buildings.go
// split for FEAT-010/data.catalogue.

// --- policies.json (§45 policy library) — skeleton ------------------------

// Policies is a minimal versioned skeleton for the policy library
// referenced in the master doc's movement/layout/economy/social policy
// sections (highlights listed inline; "full list in policies.json").
//
// TODO(engine.policies): replace Entries with the full policy schema
// (category, cost, effect coefficients, conflicting-bundle warnings, ...).
type Policies struct {
	Version int           `json:"version"`
	Entries []PolicyEntry `json:"entries"`
	// Meta is data/policies.json's documentation block (spec refs,
	// category inventory, disclosures). engine.policies decodes it with
	// its own richer schema; this skeleton declares it opaquely so the
	// BUG-281 r2 strict loader (which only strips top-level $-prefixed
	// keys) accepts the real file.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// PolicyEntry is a placeholder row; Key is the only field currently
// enforced.
type PolicyEntry struct {
	Key string `json:"key"`
}

// Validate implements Validator.
func (p *Policies) Validate() error {
	if err := requireVersion(p.Version); err != nil {
		return err
	}
	for i, e := range p.Entries {
		if err := requireNonEmptyString("entries["+itoa(i)+"].key", e.Key); err != nil {
			return err
		}
	}
	return nil
}

// itoa is a small alias used throughout this file's field-path messages.
func itoa(i int) string {
	return strconv.Itoa(i)
}
