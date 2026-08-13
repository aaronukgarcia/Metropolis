package data

import (
	"encoding/json"
	"sort"
)

// This file defines data/market.json's typed schema (engine.market,
// MOD-020), routed through the SAME generic [Load] every other §24 file
// uses — matching Seasonal's split rather than a self-contained
// engine.market loader.
//
// BOW MOD-020 ruling 1 (2026-08-11, lead Bob): market originally wrote
// its own JSON-decode + validate loader, justified as "market.json's
// shape is engine.market-specific". That reasoning does not distinguish
// market from Seasonal — Seasonal's month-indexed curve validation is
// no simpler than market's cross-field rules, and Seasonal already
// routes through this package's generic Load[T]. Two data-loading
// modules built days apart establishing two different patterns (with a
// third module free to pick a third) is the actual problem being fixed
// here, not a claim that either loader is technically better.
//
// The split below follows Seasonal's OWN precedent exactly, not just
// its destination: MarketFile.Validate covers every check that applies
// uniformly to a commodity record regardless of which commodity it is
// (a recognised supplyMode, a non-empty unit, a non-negative
// capacityCeiling that must be present, and — if present at all — a
// non-negative importPriceMicropounds/exportPriceMicropounds). It
// deliberately does NOT enforce "waste carries exportPriceMicropounds
// and never importPriceMicropounds; every other commodity is the
// reverse" — that rule requires knowing WHICH commodity key is the §6
// negative commodity, which is engine.market domain knowledge this
// package has no notion of, exactly the same reasoning that keeps
// Seasonal's requiredCurves list and its schoolIntakeGate "exactly one
// qualifying month" shape check (see engine/season/season.go's
// validateSchoolIntakeGateShape doc comment) OUT of this package and in
// engine.season's own Load instead. engine.market.Load performs that
// waste-specific XOR check itself, the same way engine.season.Load
// performs its own curve-name-specific checks after calling
// LoadSeasonal. This IS the answer to "did the shared loader express
// everything market needed": generic per-record schema, yes; the one
// commodity-identity-specific cross-field rule, no — by the same
// design boundary Seasonal already drew, not a new one invented here.

// FileMarket is data/market.json's filename, relative to the resolved
// data directory (see ResolveDataDir). Not part of the original §24
// config set FileConsumption..FilePolicies enumerate — market.json is
// engine.market's own file, added here per MOD-020 ruling 1 rather than
// growing that constant block's doc comment, which is written
// specifically about the eight files LoadAll aggregates.
const FileMarket = "market.json"

// CommodityRecord is one data/market.json "commodities" entry: the
// per-commodity supply-mode tag, unit, optional import/export prices
// (int64 micro-pounds, M0-ENG §1.2 — never a float), and required
// logistics-capacity ceiling. Price fields are pointers so "absent" and
// "present but zero" are distinguishable, matching engine.market's own
// former commodityRecord shape (see internal/engine/market/market.go).
type CommodityRecord struct {
	SupplyMode             string `json:"supplyMode"`
	Unit                   string `json:"unit"`
	ImportPriceMicropounds *int64 `json:"importPriceMicropounds,omitempty"`
	ExportPriceMicropounds *int64 `json:"exportPriceMicropounds,omitempty"`
	CapacityCeiling        *int64 `json:"capacityCeiling,omitempty"`
	Comment                string `json:"comment,omitempty"`
}

// MarketFile is data/market.json's top-level schema (§6/I.3). Commodities
// is keyed by the raw commodity string (engine.market's CommodityType
// underlying type) — this package does not import engine.market's
// CommodityType (that would be the reverse of the intended dependency
// direction, foundation -> engine), so the key stays a plain string
// here; engine.market.Load re-types each key into its own CommodityType
// after a successful Load.
type MarketFile struct {
	Version     int                        `json:"version"`
	PricingMode string                     `json:"pricingMode"`
	Meta        json.RawMessage            `json:"meta,omitempty"`
	Commodities map[string]CommodityRecord `json:"commodities"`
}

// Validate implements Validator. See this file's package-level doc
// comment for exactly which checks live here versus in
// engine.market.Load, and why.
func (m *MarketFile) Validate() error {
	if err := requireVersion(m.Version); err != nil {
		return err
	}
	// Iterate commodity keys in a deterministic (sorted) order rather
	// than ranging over the map directly (Go map iteration order is
	// randomized per-run) so that, given the SAME malformed
	// market.json with multiple violating entries, the FIRST violation
	// returned — and therefore which commodity a caller's error blames
	// — is identical on every run (GR#21, BUG-098).
	names := make([]string, 0, len(m.Commodities))
	for name := range m.Commodities {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rec := m.Commodities[name]
		switch rec.SupplyMode {
		case "importOnly", "locallyProducible", "hybrid":
		default:
			return fieldErr("commodities["+name+"].supplyMode",
				"must be one of importOnly, locallyProducible, hybrid; got \""+rec.SupplyMode+"\"")
		}
		if err := requireNonEmptyString("commodities["+name+"].unit", rec.Unit); err != nil {
			return err
		}
		if rec.CapacityCeiling == nil {
			return fieldErr("commodities["+name+"].capacityCeiling", "required")
		}
		if *rec.CapacityCeiling < 0 {
			return fieldErr("commodities["+name+"].capacityCeiling", "must be >= 0")
		}
		if rec.ImportPriceMicropounds != nil && *rec.ImportPriceMicropounds < 0 {
			return fieldErr("commodities["+name+"].importPriceMicropounds", "must be >= 0")
		}
		if rec.ExportPriceMicropounds != nil && *rec.ExportPriceMicropounds < 0 {
			return fieldErr("commodities["+name+"].exportPriceMicropounds", "must be >= 0")
		}
	}
	return nil
}
