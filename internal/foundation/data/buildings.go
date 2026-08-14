package data

import (
	"fmt"
	"regexp"
)

// This file is FEAT-010 / data.catalogue's own: the full building &
// object catalogue schema (Part IV + Supplements 1-3 of
// docs/METROPOLIS-MASTER-v2.1.md), loaded from data/buildings.json.
// See docs/design/buildings-schema.md for the human field reference and
// docs/planning/acceptance/data.catalogue.md for the acceptance
// criteria this file and buildings_test.go build to.

// buildingIDPattern is the positive character-class domain for
// BuildingEntry.ID (AC-12b): a single lowercase slug — starts with a
// letter, then lowercase letters/digits/underscore/dot/hyphen, 3-64
// characters total. No path separators, no whitespace, no leading
// digit — id becomes a foreign key from unlock_trees.json, save files,
// and future engine lookups (a map key), so a value outside this
// domain is rejected outright, never trimmed/normalised into a legal
// form (weakness pattern #4). This exact regex is a BA judgment call
// (ASM-082, logged against foundation.data/data.catalogue at BA time,
// per data.catalogue.md's Escalations) — this loader adopts it as-is.
var buildingIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)

// consumptionRefPattern is the positive character-class domain for
// BuildingEntry.ConsumptionRef (AC-12b): a camelCase identifier
// matching data/consumption.json's Classes map key convention (e.g.
// "elderCareHome", "stationRailMetro") — starts with a letter, then
// letters/digits only, 2-64 characters. Distinct from buildingIDPattern
// because consumption.json's existing class keys are camelCase, not
// the catalogue's own lowercase-slug id convention; both are still
// positively-stated domains, never "any non-empty string".
var consumptionRefPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{1,63}$`)

// appealTagPattern is the positive character-class domain for each tag
// inside BuildingEntry.AppealProfile: a lowercase slug word.
var appealTagPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// knownBlightClasses is the documented blightClass enum (AC-7). "none"
// is the default for every entry the spec does not explicitly assign a
// blight class to.
var knownBlightClasses = map[string]bool{
	"none": true, "low": true, "medium": true, "high": true, "max": true,
}

// knownSourcePacks is the documented sourcePack enum (AC-9): the six
// non-cosmetic §23 expansion-content-equivalent groups (the seventh,
// purely cosmetic regional set, is explicitly skipped per §23's own
// table, so it has no corresponding tag here).
var knownSourcePacks = map[string]bool{
	"waterfront": true, "shoreline": true, "high-rise": true,
	"retail": true, "rail": true, "office": true,
}

// knownSupplements is the documented supplement enum (AC-3).
var knownSupplements = map[string]bool{
	"": true, "S1": true, "S2": true, "S3": true,
}

// milestonePattern matches §4's milestone ladder tiers M1-M13.
var milestonePattern = regexp.MustCompile(`^M(1[0-3]|[1-9])$`)

// Buildings is the full building & object catalogue (FEAT-010): every
// object from Part IV's fifteen sections (R, E, W, H, ED, F-P, G, PK,
// L, T, PT, HS, C-I, LM, CM-WF) plus Catalogue Supplement / Supplement
// 2 / Supplement 3, each carrying its unlock gate, cost/capacity, a
// consumption-coefficient-class reference (never a raw utility number
// — §17), blight class where the spec assigns one, appeal profile
// where the spec assigns one (housing typologies, §21), and a
// §23-pack tag where applicable.
type Buildings struct {
	Version int             `json:"version"`
	Entries []BuildingEntry `json:"entries"`
	// Zones is the §34 eight-way zone catalogue (engine.build / MOD-026),
	// carried in the same buildings.json file as the building catalogue so
	// construction-cost rebalancing is one data edit (GR#15). Absent (empty)
	// for a catalogue that does not carry zone data. See zones.go.
	Zones []ZoneEntry `json:"zones,omitempty"`
	// ZoneMeta is the zone-catalogue meta block (engine.build / MOD-026) —
	// see zones.go. Zero-valued when the file carries no zone meta.
	ZoneMeta ZoneMeta `json:"meta,omitempty"`
}

// UnlockGate is one entry's Part IV "Unlock" column, structured rather
// than left as an opaque string (AC-4): Milestone is the parsed M1-M13
// tier (empty when the spec's gate text isn't a clean "Mn"-shaped
// milestone — e.g. "with sources", "first 100 deaths", an upgrade
// path — in which case Conditional carries the verbatim gate text and
// Raw is always the untouched original for traceability either way).
type UnlockGate struct {
	Raw              string `json:"raw"`
	Milestone        string `json:"milestone,omitempty"`
	DevelopmentPoint bool   `json:"developmentPoint,omitempty"`
	Achievement      bool   `json:"achievement,omitempty"`
	University       bool   `json:"university,omitempty"`
	Funds            bool   `json:"funds,omitempty"`
	Research         bool   `json:"research,omitempty"`
	Policy           bool   `json:"policy,omitempty"`
	Conditional      string `json:"conditional,omitempty"`
}

// BuildingEntry is one catalogue object.
type BuildingEntry struct {
	// ID is the stable, globally-unique foreign key other files
	// (unlock_trees.json, save files) and future engine lookups use.
	// Domain: buildingIDPattern (AC-12b).
	ID   string `json:"id"`
	Name string `json:"name"`

	// CatalogueSection is the Part IV/supplement section code this
	// entry belongs to (e.g. "R", "E", "HS", or a supplement group
	// code such as "MP", "SEC", "SUP2", "SUP3" — see
	// docs/design/buildings-schema.md for the full list).
	CatalogueSection string `json:"catalogueSection"`

	// Supplement distinguishes a Catalogue Supplement / Supplement 2 /
	// Supplement 3 entry from a base Part IV entry (AC-3): "", "S1",
	// "S2", or "S3".
	Supplement string `json:"supplement,omitempty"`
	// SupplementCategory names the supplement's own named category
	// (e.g. "mega-projects", "security-justice", "mining") for
	// supplement entries; empty for base Part IV entries.
	SupplementCategory string `json:"supplementCategory,omitempty"`

	// SourcePack tags this entry as fulfilling one of §23's six
	// non-cosmetic 'Blue'-pack-equivalent groups (AC-9). Empty when not
	// applicable. Domain: knownSourcePacks.
	SourcePack string `json:"sourcePack,omitempty"`

	Unlock UnlockGate `json:"unlock"`

	// CostRaw/CapacityRaw are the spec table's literal Cost/Cost-per-km
	// and Output-Cap/Density text, preserved verbatim (GR#15: this file
	// IS the transcribed source of truth) rather than force-parsed into
	// a single numeric field — Part IV's own tables mix flat costs,
	// per-km rates, ranges, and non-numeric costs ("opex", "zoning",
	// "reserved") that don't share one numeric shape. May be empty for
	// the handful of supplement flat-list entries the spec itself gives
	// no cost for (see notes + the logged ASM- assumptions).
	CostRaw     string `json:"costRaw,omitempty"`
	CapacityRaw string `json:"capacityRaw,omitempty"`

	// ConsumptionRef is a key into data/consumption.json's Classes map
	// (§17: the catalogue never hard-codes a utility number). Empty for
	// entries that are not utility-relevant occupants (§17.2) — most
	// notably HS housing entries, which draw against Consumption's
	// Residential per-person baseline instead of a Classes entry, and
	// the E/W sections' own utility-infrastructure objects (they
	// produce/store/distribute utilities rather than consuming
	// against a class coefficient). Domain: consumptionRefPattern;
	// existence against a loaded Consumption is checked separately by
	// [ValidateConsumptionRefs] (AC-12), not by Validate() itself,
	// since Validate() has no access to another file's content.
	ConsumptionRef string `json:"consumptionRef,omitempty"`

	// BlightClass is the §32-style qualitative blight rating. Domain:
	// knownBlightClasses. Defaults to "none".
	BlightClass string `json:"blightClass"`

	// AppealProfile is the §21 "profile sketch" structured as tags.
	// Required non-empty for every HS-section entry (AC-8); empty for
	// everything else unless the spec explicitly gives one (e.g.
	// Supplement 3's social-housing/married-quarters typologies).
	AppealProfile []string `json:"appealProfile,omitempty"`

	Notes string `json:"notes,omitempty"`
}

// Validate implements Validator. Errors are returned for the first
// violation found while walking Entries in file order (already
// deterministic — a JSON array's order is preserved by decoding, so no
// additional sort is needed for AC-13's stable-ordering requirement).
func (b *Buildings) Validate() error {
	if err := requireVersion(b.Version); err != nil {
		return err
	}

	seenIDs := make(map[string]int, len(b.Entries))

	for i, e := range b.Entries {
		prefix := fmt.Sprintf("entries[%d]", i)
		idPrefix := prefix
		if e.ID != "" {
			idPrefix = fmt.Sprintf("%s(id=%s)", prefix, e.ID)
		}

		if err := requireNonEmptyString(prefix+".id", e.ID); err != nil {
			return err
		}
		if !buildingIDPattern.MatchString(e.ID) {
			return fieldErr(idPrefix+".id", fmt.Sprintf("must match %s, got %q", buildingIDPattern.String(), e.ID))
		}
		if first, dup := seenIDs[e.ID]; dup {
			return fieldErr(idPrefix+".id", fmt.Sprintf("duplicate id (first seen at entries[%d])", first))
		}
		seenIDs[e.ID] = i

		if err := requireNonEmptyString(idPrefix+".name", e.Name); err != nil {
			return err
		}
		if err := requireNonEmptyString(idPrefix+".catalogueSection", e.CatalogueSection); err != nil {
			return err
		}

		if !knownSupplements[e.Supplement] {
			return fieldErr(idPrefix+".supplement", fmt.Sprintf("must be one of \"\", \"S1\", \"S2\", \"S3\", got %q", e.Supplement))
		}
		if e.Supplement != "" && e.SupplementCategory == "" {
			return fieldErr(idPrefix+".supplementCategory", "required when supplement is set")
		}

		if e.SourcePack != "" && !knownSourcePacks[e.SourcePack] {
			return fieldErr(idPrefix+".sourcePack", fmt.Sprintf("must be one of the six §23 pack tags, got %q", e.SourcePack))
		}

		if err := requireNonEmptyString(idPrefix+".unlock.raw", e.Unlock.Raw); err != nil {
			return err
		}
		if e.Unlock.Milestone != "" && !milestonePattern.MatchString(e.Unlock.Milestone) {
			return fieldErr(idPrefix+".unlock.milestone", fmt.Sprintf("must be M1-M13, got %q", e.Unlock.Milestone))
		}

		if e.ConsumptionRef != "" && !consumptionRefPattern.MatchString(e.ConsumptionRef) {
			return fieldErr(idPrefix+".consumptionRef", fmt.Sprintf("must match %s, got %q", consumptionRefPattern.String(), e.ConsumptionRef))
		}

		if !knownBlightClasses[e.BlightClass] {
			return fieldErr(idPrefix+".blightClass", fmt.Sprintf("must be one of none/low/medium/high/max, got %q", e.BlightClass))
		}

		if e.CatalogueSection == "HS" && len(e.AppealProfile) == 0 {
			return fieldErr(idPrefix+".appealProfile", "required non-empty for every HS (Housing Typology) entry")
		}
		for j, tag := range e.AppealProfile {
			if !appealTagPattern.MatchString(tag) {
				return fieldErr(fmt.Sprintf("%s.appealProfile[%d]", idPrefix, j), fmt.Sprintf("must match %s, got %q", appealTagPattern.String(), tag))
			}
		}
	}

	if err := b.validateZones(); err != nil {
		return err
	}

	return nil
}

// ValidateConsumptionRefs cross-checks every non-empty ConsumptionRef
// in b against c's loaded Classes map (AC-12): a consumptionRef that
// doesn't resolve is a load-time rejection, never a silent gap an
// engine module discovers only when it tries to look the coefficient
// up at runtime. Callers load b and c independently (they are separate
// files) and call this after both succeed — see [LoadBuildingsCatalogue].
func ValidateConsumptionRefs(b *Buildings, c *Consumption) error {
	for i, e := range b.Entries {
		if e.ConsumptionRef == "" {
			continue
		}
		if _, ok := c.Classes[e.ConsumptionRef]; !ok {
			return fieldErr(
				fmt.Sprintf("entries[%d](id=%s).consumptionRef", i, e.ID),
				fmt.Sprintf("references consumption class %q, which is not present in consumption.json", e.ConsumptionRef),
			)
		}
	}
	return nil
}
