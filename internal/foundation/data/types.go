package data

import "strconv"

// This file defines the typed struct for each of the §24 config files.
// Consumption and Seasonal carry the fields spec-stated in §17.1/§17.2
// and §17.1's seasonal modifiers respectively. Modes, Buildings,
// UnlockTrees, NamingCorpus, ExternalWorld, and Policies are
// intentionally minimal versioned skeletons — their full schemas firm
// up alongside the engine module that owns each domain (engine.market/
// engine.roads for Modes, FEAT-010 for Buildings' full catalogue,
// MOD-021's consumption coefficients already covered here, engine.
// unlocks for UnlockTrees, engine.world's naming system for
// NamingCorpus, engine.extcommute/engine.world for ExternalWorld, and
// engine.policies for Policies) — see foundation.data.md's Out of scope
// section. Each skeleton's container field is present and typed so a
// seed fixture is genuinely schema-conformant, but is expected to be
// empty until its owning module populates it.

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
	for key, cls := range c.Classes {
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
	for name, curve := range s.Curves {
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

// --- buildings.json (catalogue) — skeleton -------------------------------

// Buildings is a minimal versioned skeleton for the building catalogue
// (owned in full by FEAT-010/data.catalogue — this package only
// guarantees the file loads, validates, and hot-reloads; the real
// catalogue content is that module's own deliverable).
//
// TODO(FEAT-010/data.catalogue): replace Entries with the full
// building-class schema (tier, cost, footprint, blight class,
// consumption class key linking back to Consumption.Classes, ...).
type Buildings struct {
	Version int             `json:"version"`
	Entries []BuildingEntry `json:"entries"`
}

// BuildingEntry is a placeholder row; Key is the only field currently
// enforced.
type BuildingEntry struct {
	Key string `json:"key"`
}

// Validate implements Validator.
func (b *Buildings) Validate() error {
	if err := requireVersion(b.Version); err != nil {
		return err
	}
	for i, e := range b.Entries {
		if err := requireNonEmptyString("entries["+itoa(i)+"].key", e.Key); err != nil {
			return err
		}
	}
	return nil
}

// --- unlock_trees.json (§22) — skeleton -----------------------------------

// UnlockTrees is a minimal versioned skeleton for the §22 per-category
// Development Point progression trees (Roads, Electricity, Water & Gas,
// Health & Deathcare, Education, Fire, Police, Garbage, Parks & Rec,
// Transport, Communications, Welfare).
//
// TODO(engine.unlocks, §22): replace Trees with the full per-category
// tree schema (nodes, DP costs, prerequisite edges, unlocked building/
// ability keys, ...).
type UnlockTrees struct {
	Version int         `json:"version"`
	Trees   []TreeEntry `json:"trees"`
}

// TreeEntry is a placeholder row; Category is the only field currently
// enforced.
type TreeEntry struct {
	Category string `json:"category"`
}

// Validate implements Validator.
func (u *UnlockTrees) Validate() error {
	if err := requireVersion(u.Version); err != nil {
		return err
	}
	for i, e := range u.Trees {
		if err := requireNonEmptyString("trees["+itoa(i)+"].category", e.Category); err != nil {
			return err
		}
	}
	return nil
}

// --- naming_corpus.json (§20) — skeleton ----------------------------------

// NamingCorpus is a minimal versioned skeleton for §20's deterministic
// auto-naming word lists (Kentish road-name corpus, class suffixes,
// civic-building toponym fallbacks, district toponyms, transit letter/
// colour pools). Categories maps a corpus category key (e.g.
// "roadNamesKentish", "roadSuffixes") to its word list.
//
// TODO(engine.world's naming system, §20): firm up the full set of
// category keys required by the deterministic seed+id naming algorithm.
type NamingCorpus struct {
	Version    int                 `json:"version"`
	Categories map[string][]string `json:"categories"`
}

// Validate implements Validator.
func (n *NamingCorpus) Validate() error {
	if err := requireVersion(n.Version); err != nil {
		return err
	}
	for cat, words := range n.Categories {
		for i, w := range words {
			if w == "" {
				return fieldErr("categories["+cat+"]["+itoa(i)+"]", "must be non-empty")
			}
		}
	}
	return nil
}

// --- external_world.json (§21, §30) — skeleton ----------------------------

// ExternalWorld is a minimal versioned skeleton for off-map conditions:
// external job pools/wages (§21 out-commuting/in-commuting), and world
// condition profiles feeding §30's coastal-arrival frequency.
//
// TODO(engine.extcommute/engine.world, §21/§30): replace Profiles with
// the full schema (job pool capacity/wage by destination, arrival-
// frequency world-condition weights, ...).
type ExternalWorld struct {
	Version  int                    `json:"version"`
	Profiles []ExternalProfileEntry `json:"profiles"`
}

// ExternalProfileEntry is a placeholder row; Key is the only field
// currently enforced.
type ExternalProfileEntry struct {
	Key string `json:"key"`
}

// Validate implements Validator.
func (e *ExternalWorld) Validate() error {
	if err := requireVersion(e.Version); err != nil {
		return err
	}
	for i, p := range e.Profiles {
		if err := requireNonEmptyString("profiles["+itoa(i)+"].key", p.Key); err != nil {
			return err
		}
	}
	return nil
}

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
