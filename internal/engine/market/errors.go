package market

// Registry error codes for engine.market (MOD-020). Range: E600-E699,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master
// table — E000-E599 and E900-E999 were already claimed by engine.core,
// engine.detgate, feat.debugmode, engine.invariant, engine.world,
// engine.season and feat.skeleton respectively; E600-E699 was the next
// free engine sub-range, checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-E6" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-E6xx code existed either place). Every code
// below IS registered in data/errors.json with real severity/module/
// message/remedy fields (GR#7); the internal/foundation/errs
// source-scan test guards against this ever drifting out of sync.
const (
	// ErrMarketDataInvalid: data/market.json could not be loaded or
	// failed this package's schema validation (missing file, malformed
	// JSON, a commodity missing a required price/capacity field, a
	// negative price or capacity, an unrecognised supply-mode string).
	// Load-time, distinct from ErrMissingCommodity below (AC-11).
	ErrMarketDataInvalid = "MET-E600"

	// ErrMissingCommodity: data/market.json loaded and schema-validated
	// successfully but is missing one of the nine commodities
	// engine.market requires (§6/§17/§2.2) — a data-authoring omission
	// distinct from a schema violation, so this package's generic
	// per-entry validation cannot catch it (it has no notion of which
	// commodity keys this consumer needs). Load-time (AC-11).
	ErrMissingCommodity = "MET-E601"

	// ErrUnknownCommodity: Price/ExportPrice/Availability/SupplyMode
	// (or CommodityByName) was queried with a commodity ID/name that is
	// not one of the nine registered commodities — including every §10
	// service name (AC-7), which is a deliberate not-found result, not
	// a special case. Query-time, never a panic or a silent
	// zero-value a caller could mistake for a real price of zero
	// (AC-10).
	ErrUnknownCommodity = "MET-E602"

	// ErrWasteNotImportable: Price(Waste) was called. Waste is §6's
	// "negative commodity" — the city pays to EXPORT it, never pays a
	// supplier to receive it — so the import-price path rejects it and
	// directs the caller to ExportPrice instead of silently returning a
	// positive per-unit-received price (US-6, AC-6).
	ErrWasteNotImportable = "MET-E603"

	// ErrNotExportable: ExportPrice was called on any commodity other
	// than Waste. The export-cost path exists only for the one
	// negative commodity §6 names; every other commodity is
	// import-priced only (US-6, AC-6, the symmetric half of
	// ErrWasteNotImportable).
	ErrNotExportable = "MET-E604"

	// ErrCommodityFieldMissing: a commodity record resolved by lookup is
	// missing a price/capacity pointer field (importPriceMicropounds,
	// exportPriceMicropounds, or capacityCeiling) that Load's validation
	// should have guaranteed present before this *MarketAPI was ever
	// returned. Should be unreachable for any *MarketAPI Load returned
	// successfully — Load's foundation/data.MarketFile.Validate and this
	// package's own validateCommodityPricingXOR both run before Load
	// returns, so this only fires if a future edit to either weakens
	// that guarantee, or a construction path bypasses Load entirely
	// (e.g. a test helper building a *MarketAPI by hand). Query-time,
	// added specifically so that scenario is a registry-sourced error
	// rather than a nil-pointer panic (BOW MOD-020 ruling 2, 2026-08-11
	// — GR#1: a panic in engine code is not a trap).
	ErrCommodityFieldMissing = "MET-E605"
)
