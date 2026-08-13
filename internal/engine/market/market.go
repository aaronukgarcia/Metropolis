package market

import (
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileMarket is data/market.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir). Kept as a package-level
// constant (rather than inlining data.FileMarket at every call site)
// since existing tests (writeFixture) reference it directly.
//
// MOD-020 ruling 1 (2026-08-11): this package no longer owns a
// self-contained JSON-decode+validate loader. Load routes through
// foundation/data.LoadMarketFile (backed by the same generic Load[T]
// engine.season's LoadSeasonal uses) — see foundation/data/market.go's
// package doc comment for exactly which validation lives there versus
// here, and why.
const fileMarket = data.FileMarket

// CommodityType identifies one of the nine tradable commodities this
// package's registry holds (§6/§17.1/§2.2 — see doc.go's "The nine
// commodities" section and ASM-189 for why Gas is the ninth). The
// underlying string is also market.json's "commodities" map key, so a
// commodity's JSON identity and its Go identity are the same value —
// there is no separate name-mapping table to drift out of sync.
type CommodityType string

// The nine registered commodities (AC-2). This is the exhaustive list —
// go doc on this type shows exactly these nine cases, and
// allCommodities (below) is what Load's completeness check and every
// count-based test iterate over.
const (
	Water                 CommodityType = "water"
	Power                 CommodityType = "power"
	Gas                   CommodityType = "gas"
	FoodStaples           CommodityType = "foodStaples"
	FoodFresh             CommodityType = "foodFresh"
	Fuel                  CommodityType = "fuel"
	ConstructionMaterials CommodityType = "constructionMaterials"
	ConsumerGoods         CommodityType = "consumerGoods"
	Waste                 CommodityType = "waste"
)

// allCommodities is the complete, ordered set of the nine commodities
// this package's registry must contain (AC-2). Ordered (not a map) so
// any caller ranging over it gets a deterministic order (GR#21) —
// nothing in this package ever ranges over commodityRecords (a map)
// directly on a path whose result matters.
var allCommodities = []CommodityType{
	Water, Power, Gas, FoodStaples, FoodFresh, Fuel,
	ConstructionMaterials, ConsumerGoods, Waste,
}

// SupplyMode is §6's per-commodity import/local-production taxonomy
// (US-4, AC-3): whether a commodity can only be imported, can only be
// produced locally, or can be either.
type SupplyMode string

const (
	// ImportOnly: no local-production option exists at v1 (§6: Fuel —
	// "None early; synthetic late").
	ImportOnly SupplyMode = "importOnly"
	// LocallyProducible: reserved for a commodity with no import
	// channel at all. §6's table gives every commodity an import
	// channel, so no commodity currently uses this value — it exists
	// so a future commodity (or a later §6 revision) has somewhere to
	// go without a SupplyMode enum change.
	LocallyProducible SupplyMode = "locallyProducible"
	// Hybrid: both an import channel and a local-production option
	// exist (§6: Water, Power, Gas, FoodStaples, FoodFresh,
	// ConstructionMaterials, ConsumerGoods, Waste).
	Hybrid SupplyMode = "hybrid"
)

// Micropounds is money in the M0-ENG §1.2 fixed-point representation
// engine.finance (a registered consumer) uses: whole pounds ×
// 1,000,000, stored as int64 — never a float, so a price crossing the
// Market/finance module boundary never silently loses precision (US-5,
// AC-9).
type Micropounds int64

// AvailabilityResult is [MarketAPI.Availability]'s return value: the
// quantity a caller requested, the commodity's configured
// logistics-capacity ceiling, and the resulting bounded figure actually
// available — never a bare boolean (US-3, AC-5). Quantity units are
// commodity-specific (tonnes/slots per §8, or the per-commodity unit
// data/market.json's meta block documents — see AC-16).
type AvailabilityResult struct {
	Requested       int64
	CapacityCeiling int64
	Available       int64
}

// commodityRecord is one data/market.json "commodities" entry, decoded
// and validated at Load time. Unexported: the only way another package
// reaches a commodity's data is through MarketAPI's exported query
// methods (GR#20) — this struct, and MarketAPI's own commodities field
// below, are never part of this package's exported surface.
type commodityRecord struct {
	SupplyMode             SupplyMode `json:"supplyMode"`
	Unit                   string     `json:"unit"`
	ImportPriceMicropounds *int64     `json:"importPriceMicropounds,omitempty"`
	ExportPriceMicropounds *int64     `json:"exportPriceMicropounds,omitempty"`
	CapacityCeiling        *int64     `json:"capacityCeiling,omitempty"`
	Comment                string     `json:"comment,omitempty"`
}

// staticPricingMode is the only pricingMode value this item implements
// (I.3, AC-4). A future dynamic-pricing mode is a data-file value plus
// a new code path behind the SAME Price method signature — not a
// second parallel method — per this item's Out of scope section.
const staticPricingMode = "static"

// MarketAPI is the pure, stateless (after construction) commodity
// price/availability/supply-mode query contract — code.json's
// "engine.market" inbound interface (MarketAPI, "price/availability
// queries; dynamic-world future hook"). Every query method takes a
// [CommodityType] and returns the same value every time it is called
// with the same argument — no hidden mutable state, no side effects
// (AC-12).
//
// The zero value is not usable; construct via [Load] or [LoadDefault].
// A *MarketAPI is safe for concurrent use by multiple goroutines: its
// commodity map is populated once at construction and never mutated
// afterward (AC-14).
type MarketAPI struct {
	commodities   map[CommodityType]commodityRecord
	pricingMode   string
	correlationID string
}

// Load reads and validates data/market.json from dir (via
// foundation/data.LoadMarketFile — MOD-020 ruling 1) and checks that
// all nine commodities engine.market requires are present, returning a
// ready-to-query *MarketAPI. correlationID is attached to every error
// this call (and the returned MarketAPI's query methods) construct
// (GR#1). Every failure is a registry-sourced *errs.E — never a silent
// default substitution, never a panic (AC-11).
func Load(dir, correlationID string) (*MarketAPI, error) {
	mf, err := data.LoadMarketFile(dir, correlationID)
	if err != nil {
		// foundation/data.LoadMarketFile already returns a
		// registry-sourced *errs.E (MET-F6xx) for a missing file,
		// malformed JSON, or a generic per-record schema violation
		// (supplyMode, unit, capacityCeiling, non-negative prices —
		// see foundation/data/market.go's Validate). Re-wrap it under
		// this package's own ErrMarketDataInvalid so every Load-time
		// failure this package's callers see carries one consistent
		// MET-E6xx code, matching engine.season's LoadSeasonal wrap.
		// MET-E600's registered template has a "{cause}" placeholder
		// (BUG-099) — populate it from the wrapped error's own text so
		// the rendered message actually names the failure instead of
		// leaving the literal "{cause}" in the operator/log-visible text.
		return nil, errs.Wrap(ErrMarketDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	if mf.PricingMode != staticPricingMode {
		rule := "must be \"static\" (v1 supports no other mode)"
		// No underlying error exists for this New-based path (BUG-099) —
		// synthesize the {cause} template's substitution from the same
		// field/rule/got facts already being reported structurally.
		return nil, errs.New(ErrMarketDataInvalid, correlationID, map[string]any{
			"dir":   dir,
			"field": "pricingMode",
			"rule":  rule,
			"got":   mf.PricingMode,
			"cause": fmt.Sprintf("field %q: %s (got %q)", "pricingMode", rule, mf.PricingMode),
		})
	}

	// Validate (and insert) commodities in a deterministic (sorted)
	// order rather than ranging over mf.Commodities directly — Go map
	// iteration order is randomized per-run, so a market.json with
	// MULTIPLE entries simultaneously violating validateCommodityPricingXOR
	// would otherwise blame a different commodity on different runs
	// against the byte-identical file (GR#21, BUG-098).
	names := make([]string, 0, len(mf.Commodities))
	for name := range mf.Commodities {
		names = append(names, name)
	}
	sort.Strings(names)

	commodities := make(map[CommodityType]commodityRecord, len(mf.Commodities))
	for _, name := range names {
		rec := mf.Commodities[name]
		c := CommodityType(name)
		if err := validateCommodityPricingXOR(c, rec, correlationID, dir); err != nil {
			return nil, err
		}
		commodities[c] = commodityRecord{
			SupplyMode:             SupplyMode(rec.SupplyMode),
			Unit:                   rec.Unit,
			ImportPriceMicropounds: rec.ImportPriceMicropounds,
			ExportPriceMicropounds: rec.ExportPriceMicropounds,
			CapacityCeiling:        rec.CapacityCeiling,
			Comment:                rec.Comment,
		}
	}

	for _, c := range allCommodities {
		if _, ok := commodities[c]; !ok {
			return nil, errs.New(ErrMissingCommodity, correlationID, map[string]any{
				"commodity": string(c),
				"dir":       dir,
			})
		}
	}

	return &MarketAPI{
		commodities:   commodities,
		pricingMode:   mf.PricingMode,
		correlationID: correlationID,
	}, nil
}

// validateCommodityPricingXOR enforces the ONE per-entry rule
// foundation/data.MarketFile.Validate deliberately does not: Waste
// carries an export price and never an import price, and every other
// commodity carries an import price and never an export price (§6's
// negative-commodity distinction, US-6/AC-6). This rule needs to know
// WHICH commodity key is Waste — domain knowledge specific to
// engine.market, not a generic schema fact foundation/data's per-record
// validation has any notion of (MOD-020 ruling 1; see
// foundation/data/market.go's package doc for the full reasoning, which
// mirrors engine.season keeping its own curve-name-specific checks out
// of foundation/data too). Every other structural check (supplyMode
// enum, non-empty unit, non-negative capacity/prices, capacityCeiling
// required) already ran inside foundation/data.LoadMarketFile before
// Load ever reaches this function.
func validateCommodityPricingXOR(name CommodityType, rec data.CommodityRecord, correlationID, dir string) error {
	fail := func(field, rule string) error {
		// Same MET-E600 {cause} placeholder as Load's own two raise
		// sites (BUG-099) — this closure has no underlying error either,
		// so the cause text is synthesized from the field/rule this
		// validation failure already carries structurally.
		return errs.New(ErrMarketDataInvalid, correlationID, map[string]any{
			"dir":       dir,
			"commodity": string(name),
			"field":     field,
			"rule":      rule,
			"cause":     fmt.Sprintf("commodity %q, field %q: %s", name, field, rule),
		})
	}

	if name == Waste {
		if rec.ExportPriceMicropounds == nil {
			return fail("exportPriceMicropounds", "required for waste (the negative commodity, §6)")
		}
		if rec.ImportPriceMicropounds != nil {
			return fail("importPriceMicropounds", "must not be set for waste — waste is export-only (§6)")
		}
		return nil
	}

	if rec.ImportPriceMicropounds == nil {
		return fail("importPriceMicropounds", "required for every importable commodity")
	}
	if rec.ExportPriceMicropounds != nil {
		return fail("exportPriceMicropounds", "must not be set for a non-waste commodity")
	}
	return nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*MarketAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// lookup resolves c against the loaded registry, returning
// ErrUnknownCommodity (never a panic or a silently-returned zero value)
// for anything not one of the nine registered commodities — including
// every §10 service name (AC-7, AC-10).
func (m *MarketAPI) lookup(c CommodityType) (commodityRecord, error) {
	rec, ok := m.commodities[c]
	if !ok {
		return commodityRecord{}, errs.New(ErrUnknownCommodity, m.correlationID, map[string]any{
			"commodity": string(c),
		})
	}
	return rec, nil
}

// CommodityByName resolves a raw string against the registered
// commodity set. It exists specifically so a lookup-by-name (AC-7's
// check: every §10 service name must resolve to not-found) exercises
// the real registry rather than a Go compile-time constant comparison.
// Returns ErrUnknownCommodity for any name not one of the nine
// registered commodities, including every service name.
func (m *MarketAPI) CommodityByName(name string) (CommodityType, error) {
	c := CommodityType(name)
	if _, ok := m.commodities[c]; !ok {
		return "", errs.New(ErrUnknownCommodity, m.correlationID, map[string]any{
			"commodity": name,
		})
	}
	return c, nil
}

// SupplyMode returns c's §6 import/local-production classification
// (US-4, AC-3). Returns ErrUnknownCommodity for an unregistered
// commodity (AC-10).
func (m *MarketAPI) SupplyMode(c CommodityType) (SupplyMode, error) {
	rec, err := m.lookup(c)
	if err != nil {
		return "", err
	}
	return rec.SupplyMode, nil
}

// Price returns c's static v1 per-unit import price in Micropounds
// (I.3, US-2, AC-4, AC-9) — the ONE exported price-query method for
// importable commodities; a future dynamic-pricing mode is selected via
// data/market.json's pricingMode field, never a second parallel method.
// Two calls with the same commodity and no intervening Load return the
// identical value (AC-12).
//
// Price rejects Waste with ErrWasteNotImportable — waste is §6's
// negative commodity, priced only through [MarketAPI.ExportPrice]
// (US-6, AC-6) — and rejects any unregistered commodity with
// ErrUnknownCommodity (AC-10), never a silently-returned zero value a
// caller could mistake for a real price of zero.
func (m *MarketAPI) Price(c CommodityType) (Micropounds, error) {
	rec, err := m.lookup(c)
	if err != nil {
		return 0, err
	}
	if c == Waste {
		return 0, errs.New(ErrWasteNotImportable, m.correlationID, map[string]any{
			"commodity": string(c),
		})
	}
	// Load's validateCommodityPricingXOR guarantees ImportPriceMicropounds
	// is non-nil for every non-Waste commodity in a *MarketAPI that Load
	// returned successfully — but that guarantee is an invariant enforced
	// only at Load time, in prose here. A future edit to
	// validateCommodityPricingXOR (or any construction path that builds a
	// *MarketAPI bypassing Load entirely, e.g. a test helper) would
	// silently reintroduce a real nil-pointer panic on this dereference
	// with no test catching it except by accident (BOW MOD-020 ruling 2,
	// 2026-08-11 — GR#1: a panic in engine code is not a trap). Guard it
	// explicitly rather than trust the comment.
	if rec.ImportPriceMicropounds == nil {
		return 0, errs.New(ErrCommodityFieldMissing, m.correlationID, map[string]any{
			"commodity": string(c),
			"field":     "importPriceMicropounds",
		})
	}
	return Micropounds(*rec.ImportPriceMicropounds), nil
}

// ExportPrice returns Waste's §6 negative-commodity export cost in
// Micropounds — the price the city pays PER UNIT to have Waste taken
// away, never a positive per-unit-received price (US-6, AC-6, AC-9).
// ExportPrice rejects every non-Waste commodity with ErrNotExportable
// (the export-cost path exists only for the one negative commodity §6
// names) and rejects an unregistered commodity with ErrUnknownCommodity
// (AC-10).
func (m *MarketAPI) ExportPrice(c CommodityType) (Micropounds, error) {
	rec, err := m.lookup(c)
	if err != nil {
		return 0, err
	}
	if c != Waste {
		return 0, errs.New(ErrNotExportable, m.correlationID, map[string]any{
			"commodity": string(c),
		})
	}
	// Same unenforced-invariant class as Price's ImportPriceMicropounds
	// guard above (BOW MOD-020 ruling 2) — Load's validateCommodityPricingXOR
	// guarantees ExportPriceMicropounds is non-nil for Waste in a
	// *MarketAPI that Load returned successfully, but that guarantee is
	// not itself checked here without this guard.
	if rec.ExportPriceMicropounds == nil {
		return 0, errs.New(ErrCommodityFieldMissing, m.correlationID, map[string]any{
			"commodity": string(c),
			"field":     "exportPriceMicropounds",
		})
	}
	return Micropounds(*rec.ExportPriceMicropounds), nil
}

// Availability bounds requested (a quantity in c's own unit —
// data/market.json's "unit" field, per AC-16) by c's configured
// logistics-capacity ceiling (§8's junction/port/rail throughput,
// tonnes/slots per this item's own BOW description), returning an
// [AvailabilityResult] carrying the request, the ceiling, and the
// resulting bounded figure — never a bare boolean (US-3, AC-5). The
// live JIT queue/day-tick resolution of that capacity across a running
// simulation is engine.logistics's job (MOD-025, out of scope here);
// this method only exposes the capacity-bounded query.
//
// A negative requested quantity is clamped to zero Available rather
// than treated as a distinct error class — "how much can I get" for a
// nonsensical negative request has an unambiguous answer (zero) that
// does not need a new error code to express.
func (m *MarketAPI) Availability(c CommodityType, requested int64) (AvailabilityResult, error) {
	rec, err := m.lookup(c)
	if err != nil {
		return AvailabilityResult{}, err
	}
	// CapacityCeiling is required for every commodity (not just a
	// waste-vs-rest split like the price fields), so foundation/data's
	// generic MarketFile.Validate already rejects any record missing it
	// at Load time. Same reasoning as the two guards above still
	// applies: that is an invariant enforced at Load time, not something
	// this dereference itself checks, so guard it explicitly rather than
	// trust it (BOW MOD-020 ruling 2).
	if rec.CapacityCeiling == nil {
		return AvailabilityResult{}, errs.New(ErrCommodityFieldMissing, m.correlationID, map[string]any{
			"commodity": string(c),
			"field":     "capacityCeiling",
		})
	}

	ceiling := *rec.CapacityCeiling
	available := requested
	if requested < 0 {
		available = 0
	} else if requested > ceiling {
		available = ceiling
	}

	return AvailabilityResult{
		Requested:       requested,
		CapacityCeiling: ceiling,
		Available:       available,
	}, nil
}
