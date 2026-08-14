package consumption

// Registry error codes for engine.consumption (MOD-021). Range: G300-G399,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully claimed, and the G layer's earlier
// blocks belong to engine.citizens (G000-G099), engine.projections
// (G100-G199), and engine.finance (G200-G299); G300-G399 was the next
// free engine sub-range, checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G3" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always
// current. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrConsumptionDataInvalid: data/consumption.json could not be loaded
	// or failed foundation.data's schema validation (missing file, malformed
	// JSON, negative coefficient, empty class unit). Wraps the underlying
	// foundation.data error (already registry-sourced under an F6xx code).
	ErrConsumptionDataInvalid = "MET-G300"

	// ErrUnresolvedConsumptionRef: a consumptionRef key (from
	// data/buildings.json) does not resolve to any class in
	// data/consumption.json's loaded classes map — AC-13. Fails loudly at
	// reference-resolution time; never a silent zero-demand default.
	ErrUnresolvedConsumptionRef = "MET-G301"

	// ErrNoSource: a network with zero registered sources was asked to solve
	// a tick — AC-14. A loud diagnostic, never a silent 100%-shortfall answer
	// indistinguishable from a partial, expected shortfall.
	ErrNoSource = "MET-G302"

	// ErrInvalidOccupancy: a class-demand or residential-demand query was
	// given a negative or non-finite (NaN/Inf) occupancy/throughput/
	// population, which would produce a negative or NaN demand figure
	// (GR#1/GR#16: a silent-correctness trap, not a number).
	ErrInvalidOccupancy = "MET-G303"

	// ErrMarketDataInvalid: data/market.json could not be loaded for utility
	// billing (AC-20) — wraps engine.market's own Load error.
	ErrMarketDataInvalid = "MET-G304"

	// ErrSeasonDataInvalid: data/seasonal.json could not be loaded for the
	// seasonal-modifier layer (AC-11) — wraps engine.season's own Load error.
	ErrSeasonDataInvalid = "MET-G305"

	// ErrInvalidDemand: a solve was handed a negative or non-finite demand
	// figure, which would silently poison the conserved
	// Delivered + ShortfallTotal == Demand accounting (GR#1/GR#16).
	ErrInvalidDemand = "MET-G306"

	// ErrNoSolve: a Shortfall query was made before any solve had run on
	// the network.
	ErrNoSolve = "MET-G307"

	// ErrUnknownEntity: a Shortfall query named an entity that was not part
	// of the network's most recent solve.
	ErrUnknownEntity = "MET-G308"

	// ErrInvalidSource: a network source was given a negative or non-finite
	// capacity, which would produce a negative delivery (units destroyed) in
	// the conserved solve (GR#1/GR#16).
	ErrInvalidSource = "MET-G309"

	// ErrInvalidEdge: a network edge was given a negative or non-finite
	// length, which would produce a negative loss fraction (GR#1/GR#16).
	ErrInvalidEdge = "MET-G310"

	// ErrInvalidAquiferYield: an aquifer was constructed with a negative or
	// non-finite sustainable yield, which would produce a negative draw
	// (GR#1/GR#16).
	ErrInvalidAquiferYield = "MET-G311"

	// ErrInvalidStorage: a network storage node was given a negative or
	// non-finite capacity (GR#1/GR#16).
	ErrInvalidStorage = "MET-G312"

	// ErrInvalidAbstraction: AquiferYield.Abstract was asked to abstract a
	// negative or non-finite amount (GR#1/GR#16) — the mutation counterpart
	// of NewAquiferYield's constructor validation.
	ErrInvalidAbstraction = "MET-G313"

	// ErrDemandOverflow: a class/residential demand computation overflowed to
	// a non-finite value after coefficient × occupancy (and the seasonal
	// layer) (GR#1/GR#16) — a public demand query must never return +Inf/NaN.
	ErrDemandOverflow = "MET-G314"

	// ErrSolveOverflow: the daily-tick solve's supply/loss accumulation
	// overflowed to a non-finite value (GR#1/GR#16) — a degenerate demand/
	// survival configuration must be rejected, never masked as a NaN delivery.
	ErrSolveOverflow = "MET-G315"
)
