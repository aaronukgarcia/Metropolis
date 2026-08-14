package logistics

// Registry error codes for engine.logistics (MOD-025). Range: G400-G499,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully claimed, and the G layer's earlier
// blocks belong to engine.citizens (G000-G099), engine.projections
// (G100-G199), engine.finance (G200-G299), and engine.consumption
// (G300-G399); G400-G499 was the next free engine sub-range, checked
// against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G4" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrLogisticsDataInvalid: data/logistics.json (or, at Load time, its
	// engine.market dependency's data/market.json) could not be loaded or
	// failed foundation.data's schema validation (missing file, malformed
	// JSON, a negative throughput/shelf-life/holding-cost, a shortfall
	// factor outside (0,1], a defaultBufferPolicy naming an absent
	// policy). Wraps the underlying foundation.data or engine.market
	// error (itself already registry-sourced) so callers see this
	// package's own code AND, via errors.Unwrap, the original cause.
	ErrLogisticsDataInvalid = "MET-G400"

	// ErrMissingCommodity: data/logistics.json loaded and schema-validated
	// successfully but is missing one of the nine §6 commodities this
	// package's baseline loop requires — a data-authoring omission
	// distinct from a schema violation, so foundation.data's generic
	// loader cannot catch it (it has no notion of which commodity keys
	// this consumer needs).
	ErrMissingCommodity = "MET-G401"

	// ErrUnknownCommodity: a query/draw/order was made for a commodity ID
	// that is not one of this package's registered commodities. Query-time,
	// never a panic or a silently-created zero-value stock entry (AC-13).
	ErrUnknownCommodity = "MET-G402"

	// ErrUnknownDistrict: a stateful operation (Stock/Draw/Restock/
	// SetBufferPolicy/OrderSize) named a district that has never been
	// Provisioned. The coarse stub models districts as opaque delivery
	// targets (no junction geometry — deferred with AC-4), so "unknown
	// district" is this item's analogue of AC-13's "unregistered junction
	// ID": rejected loudly, never a silently-created zero-value entry.
	ErrUnknownDistrict = "MET-G403"

	// ErrInvalidBufferPolicy: SetBufferPolicy was called with a policy
	// string that names no entry in data/logistics.json's bufferPolicies
	// map. Query-time, never a silent fallback to a default tier.
	ErrInvalidBufferPolicy = "MET-G404"

	// ErrCopiedValue: a LogisticsAPI method was called on a struct-copied
	// *LogisticsAPI (SEC-020-class) — a copy gets its own independently
	// zeroed mu but ALIASES the original's stocks/subs/cfg maps and keeps
	// the original's self pointer, so the copy is rejected before mu is
	// ever touched, mirroring engine.finance's FinanceAPI (MET-G204) and
	// engine.world's World (MET-E406). Always construct exactly one
	// *LogisticsAPI via Load/LoadDefault and pass its pointer everywhere,
	// never a dereferenced copy.
	ErrCopiedValue = "MET-G405"
)
