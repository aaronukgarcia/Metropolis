package leisure

// Registry error codes for engine.leisure (MOD-055). Range: G2100-G2199,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table).
//
// The E layer (E000-E999) is fully exhausted by eleven earlier engine
// modules, and the G layer's blocks through G2000-G2099 were all claimed
// before this module landed (engine.citizens G000-G099 … engine.refuse
// G1900-G1999, engine.rail G1700-G1799, feat.containerport G2000-G2099),
// so engine.leisure opens G2100-G2199 under BUG-234's 2026-08-14
// code-format widening (three digits → three-or-four). Checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G21" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current — no prior MET-G21xx code
// existed either place. Every code below IS registered in data/errors.json
// with real severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrLeisureDataInvalid: data/leisure.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// non-positive hoursPerWeek, a missing life-stage/event-crowd/default-
	// taste entry, an out-of-domain access-time or decay figure). Load-time.
	ErrLeisureDataInvalid = "MET-G2100"

	// ErrUnknownCitizen: a patronage/unmet-demand/leisure-fit query was issued
	// for a citizen ID that has no record in engine.citizens. AC-11 — never a
	// silently-returned zero-value patronage record.
	ErrUnknownCitizen = "MET-G2101"

	// ErrUnknownDistrict: an unmet-demand/venue-mix query named a non-zero
	// district that has no registered venues. AC-11.
	ErrUnknownDistrict = "MET-G2102"

	// ErrMalformedEvent: an event was scheduled with a malformed or missing
	// date or venue reference. AC-12 — rejected at schedule time, never a
	// silently-dropped event that leaves a UI-promised payoff ungenerated.
	ErrMalformedEvent = "MET-G2103"

	// ErrUnknownVenue: a freshness/visit/refurbish/schedule operation named a
	// venue ID that was never registered.
	ErrUnknownVenue = "MET-G2104"

	// ErrInvalidVenue: OpenVenue was called with a zero ID, a category outside
	// the seven going-out categories, or a non-positive capacity.
	ErrInvalidVenue = "MET-G2105"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (citizens, traffic, wellbeing) was invoked before that dependency was
	// wired. Fails loudly rather than fabricating a figure.
	ErrDependencyMissing = "MET-G2106"

	// ErrCopiedValue: a LeisureAPI method was called on a struct-copied value
	// (SEC-020 family).
	ErrCopiedValue = "MET-G2107"

	// ErrInvalidTasteDistribution: LeisureFitAggregate/SetPopulationTaste was
	// called with a non-finite, negative, or zero-sum distribution (AC-9).
	ErrInvalidTasteDistribution = "MET-G2108"

	// ErrInvalidInput: a numeric input (e.g. overtime hours) was non-finite or
	// negative.
	ErrInvalidInput = "MET-G2109"
)
