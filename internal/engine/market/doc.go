// Package market is the needs-and-commodities module (MOD-020): a
// single MarketAPI for price/availability/supply-mode queries over the
// nine tradable commodities §6 (plus §17/§2.2's gas — see ASM-189 in
// docs/planning/acceptance/engine.market.md) names, loaded from
// data/market.json.
//
// Module key: engine.market (see code.json)
// Spec refs:  §6 (Needs & Commodities: the import/local-production/
// hybrid taxonomy, the eight-row commodity table); I.3 (Fixed decisions:
// "static import prices v1 behind a market interface"); §17.1/§2.2 (gas
// as a fourth, separately-networked utility with its own off-map
// pipeline/LNG import channel); M0-ENG §1.2 (money is int64
// micro-pounds — engine.finance, a registered consumer, carries this
// representation, and Market's prices must feed it without a lossy
// conversion).
//
// # The nine commodities
//
// Every commodity below is a [CommodityType] constant, backed by a
// named entry in data/market.json's "commodities" map (never a Go
// literal for its price/capacity/supply-mode value — GR#15):
//
//  1. Water                  — hybrid (boreholes/reservoir/desalination
//     local options, §6).
//  2. Power                  — hybrid (diesel/wind/solar/gas-CCGT/
//     nuclear local options, §6).
//  3. Gas                    — hybrid (§17.1/§2.2: a fourth,
//     separately-networked utility with its own off-map pipeline/LNG
//     import channel, structurally identical to Power's Sellindge
//     connection — ASM-189).
//  4. FoodStaples ("food-staples") — hybrid (farm plots/vertical farms
//     local options, §6).
//  5. FoodFresh ("food-fresh")     — hybrid (market gardens/fishing
//     local options, §6).
//  6. Fuel                   — import-only v1 ("None early; synthetic
//     late", §6).
//  7. ConstructionMaterials  — hybrid (quarry/timber/cement plant local
//     options, §6).
//  8. ConsumerGoods          — hybrid (light-to-heavy industry chains,
//     §6).
//  9. Waste                  — hybrid local disposal options (landfill/
//     incinerator/recycling), but priced as a distinct EXPORT cost, not
//     an import price — see "Waste" below.
//
// # Waste is a negative commodity (US-6, AC-6)
//
// §6 states Waste is a "negative commodity": the city pays to export it
// rather than paying a supplier to receive it, the reverse of every
// other commodity's "pay to receive" semantics. This package makes that
// a real, distinct code path rather than a sign-convention footnote
// (ASM-190): [MarketAPI.Price] rejects Waste with [ErrWasteNotImportable]
// (directing the caller to [MarketAPI.ExportPrice] instead), and
// [MarketAPI.ExportPrice] rejects every non-Waste commodity with
// [ErrNotExportable] — the two paths are structurally exclusive, never a
// single method whose sign a caller must remember to flip.
//
// # Static v1 price behind a config-flip seam (I.3, US-2, AC-4)
//
// [MarketAPI.Price] is the ONE exported per-commodity import-price
// query method — there is no separate "StaticPrice"/"DynamicPrice"
// pair. At v1 the value it returns is a static figure read from
// data/market.json's "importPriceMicropounds" field
// (data/market.json's top-level "pricingMode" field is the documented
// seam: only "static" is implemented by this item, per I.3 and this
// item's own Out of scope section — a future dynamic-pricing mode
// selects via that same field, without Price's signature or call sites
// changing).
//
// # Services are structurally excluded (US-7, AC-7)
//
// §10's non-tradable services (education, healthcare, elder care, fire,
// police, deathcare, leisure, transport) never appear in this package's
// commodity registry — [CommodityByName] returns [ErrUnknownCommodity]
// for every service name, the same not-found result an unrecognised
// string gets, never a valid [CommodityType].
//
// # Logistics-capacity-bounded availability (US-3, AC-5)
//
// [MarketAPI.Availability] returns an [AvailabilityResult] carrying the
// requested quantity, the commodity's configured import-capacity
// ceiling (data/market.json's "capacityCeiling" field, tonnes/slots per
// §8's junction/port/rail throughput), and the resulting bounded
// quantity actually available — never a bare boolean. The live JIT
// queue/day-tick resolution of that capacity across a running
// simulation belongs to engine.logistics (MOD-025, open, out of scope
// here — ASM-191); this package only exposes the capacity-bounded
// figure as data.
//
// # Loading and errors (GR#7, GR#15)
//
// [Load] reads data/market.json via foundation/data.LoadMarketFile —
// the same generic, Validator-interface-driven Load[T] every other §24
// config file (including engine.season's seasonal.json) routes through
// (MOD-020 ruling 1, 2026-08-11; see foundation/data/market.go's
// package doc for the exact split between what that shared loader
// validates and what this package's own validateCommodityPricingXOR
// validates afterward) — and checks that all nine commodities this
// package requires are present. Every failure returns a
// registry-sourced *errs.E (MET-E6xx, this package's claimed
// sub-range — see errors.go), never a silent default-substitution and
// never a panic. Every dereference of a commodity record's optional
// price/capacity pointer field is guarded at the call site
// (ErrCommodityFieldMissing, MOD-020 ruling 2) rather than trusting
// Load's validation by convention alone.
package market
