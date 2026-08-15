package freight

// Registry error codes for engine.freight (MOD-047). Range: G1000-G1099,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully claimed, and the G layer's three-digit
// blocks are all claimed by engine.citizens (G000-G099), engine.projections
// (G100-G199), engine.finance (G200-G299), engine.consumption (G300-G399),
// engine.logistics (G400-G499), engine.build (G500-G599), engine.households
// (G600-G699), engine.attract (G700-G799), feat.compositionroot (G800-G899)
// and engine.unlocks (G900-G999); G1000-G1099 is the next free engine
// sub-range — the first FOUR-DIGIT block, per BUG-234's widening of the code
// format from three to three-or-four digits (checked against
// data/errors.json's "ranges.reserved" table AND `grep -rn "MET-G1" internal/
// cmd/` before claiming, per BUG-008's lesson that the table alone is not
// always current — no prior MET-G1xxx code existed either place). Every code
// below IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrFreightDataInvalid: data/freight.json (or, at Load time, its
	// engine.market/engine.logistics dependency's data file) could not be
	// loaded or failed validation (missing file, malformed JSON, a
	// non-positive berth/crane/hour/customs figure, an unknown
	// storage-class or market-commodity mapping, a chain stage referencing
	// an unregistered commodity, an input/output rate <= 0, a negative
	// jobs/power/water draw, an out-of-range blight class).
	ErrFreightDataInvalid = "MET-G1000"

	// ErrNoBerthsConfigured: PortCapacity (or a movement/import needing the
	// port's physical throughput) was queried while the loaded port config
	// carries zero berths — the port is not yet built, so there is no
	// throughput to compute (AC-12). Never a silently-returned zero figure
	// a caller could mistake for a real capacity of zero.
	ErrNoBerthsConfigured = "MET-G1001"

	// ErrUnknownCommodity: a stage/movement/storage/trade query named a
	// freight commodity ID that is not one of the loaded data/freight.json
	// commodities. Query-time, never a panic or a silently-created
	// zero-value entry (AC-12).
	ErrUnknownCommodity = "MET-G1002"

	// ErrUnknownStage: a chain query named a stage ID that is not one of
	// the loaded data/freight.json chain stages. Query-time, never a
	// silently-created zero-value stage (AC-12).
	ErrUnknownStage = "MET-G1003"

	// ErrUnknownStorageSite: a storage query/movement named a storage-site
	// ID that is not one of the four documented site types. Query-time,
	// never a silently-created zero-value site (AC-12).
	ErrUnknownStorageSite = "MET-G1004"

	// ErrStorageTypeMismatch: a commodity was routed to a storage site
	// whose commodity class does not match the commodity's storage class
	// (e.g. grain to a tank farm, fuel to a silo) without an explicit
	// override. Rejected loudly rather than silently accepted (AC-6).
	ErrStorageTypeMismatch = "MET-G1005"

	// ErrModalCapExceeded: a movement/import/export command declared a
	// tonnage above its mode's documented per-movement bulk cap (road 25t,
	// rail 1,000t, sea 40kt) — or below the sea minimum (3kt). Rejected,
	// never silently clamped to the cap (AC-13).
	ErrModalCapExceeded = "MET-G1006"

	// ErrNegativeTonnage: a movement/import/export/store command declared a
	// negative tonnage. Rejected, never silently ignored (AC-13).
	ErrNegativeTonnage = "MET-G1007"

	// ErrCopiedValue: a FreightAPI method was called on a struct-copied
	// *FreightAPI (SEC-020-class) — a copy gets its own independently
	// zeroed mu but ALIASES the original's maps/slices and keeps the
	// original's self pointer, so the copy is rejected before mu is ever
	// touched, mirroring engine.finance's FinanceAPI and engine.logistics'
	// LogisticsAPI. Always construct exactly one *FreightAPI via
	// Load/LoadDefault and pass its pointer everywhere.
	ErrCopiedValue = "MET-G1008"
)
