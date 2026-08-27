package refuse

// Registry error codes for engine.refuse (MOD-039). Range: G1900-G1999,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The E layer (E000-E999) is fully exhausted, and the G layer's earlier
// blocks belong to engine.citizens (G000-G099), engine.projections
// (G100-G199), engine.finance (G200-G299), engine.consumption (G300-G399),
// engine.logistics (G400-G499), engine.build (G500-G599), engine.households
// (G600-G699), engine.attract (G700-G799), feat.compositionroot (G800-G899),
// engine.unlocks (G900-G999), engine.freight (G1000-G1099), engine.spiral
// (G1100-G1199), engine.services (G1200-G1299), engine.tax (G1300-G1399),
// engine.firms (G1400-G1499), engine.crime (G1500-G1599), engine.farming
// (G1600-G1699), engine.rail (G1700-G1799), and engine.education
// (G1800-G1899); G1900-G1999 was the next free engine sub-range, checked
// against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G19" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against drift.
const (
	// ErrRefuseDataInvalid: data/refuse.json could not be loaded or failed
	// foundation.data's schema validation (missing file, malformed JSON, a
	// non-positive bin capacity, a negative waste rate, a stream-mix
	// fraction outside [0,1], a contamination penalty outside [0,1], a
	// compost conversion ratio outside (0,1], a negative vermin/incineration
	// rate, a funding threshold outside [0,1], or a non-positive truck
	// capacity/crews-per-truck). Wraps the underlying foundation.data error
	// (itself already registry-sourced) so callers see this package's own
	// code AND, via errors.Unwrap, the original cause.
	ErrRefuseDataInvalid = "MET-G1900"

	// ErrUnknownLandUse: a bin-stock query or cell registration named a
	// land-use type this package does not recognise (not residential/
	// commercial/industrial), or a cell that was never registered and so has
	// no land-use type. Query-time, never a silently-created zero-value
	// bin-stock entry (AC-13).
	ErrUnknownLandUse = "MET-G1901"

	// ErrUnknownDepot: a round was scheduled (or a strike set) against a
	// depot ID that was never registered. Query-time, never a
	// silently-created zero-value round (AC-13).
	ErrUnknownDepot = "MET-G1902"

	// ErrInvalidOverride: OverrideRound (or AutoOptimise/ClearOverride/Round)
	// was called on a round that is unknown or has already completed, or
	// ScheduleRound was called to re-schedule an existing round (in flight or
	// completed). Rejected rather than silently overriding a finished round's
	// history (AC-14).
	ErrInvalidOverride = "MET-G1903"

	// ErrInvalidContamination: SetContamination was called with a level
	// outside [0,1] (negative or over 100%). Rejected with this typed error,
	// never silently clamped (AC-14).
	ErrInvalidContamination = "MET-G1904"

	// ErrInvalidFunding: SetFunding was called with a level outside [0,1].
	// Rejected at this package's boundary before delegating to
	// engine.services (never silently clamped — AC-14).
	ErrInvalidFunding = "MET-G1905"

	// ErrDisposalSiteUnavailable: general (or food) waste was routed to a
	// disposal site that is unknown, full, reclaimed, or not the right kind
	// for that stream (a compost site cannot take general waste; only a
	// landfill can be reclaimed); or a Register* disposal-site call named a
	// siteID that is already registered (rejected rather than silently
	// replacing the site and destroying its durable state — AC-8/AC-9/AC-10/
	// AC-11, Destructive-MOD039 r4). Rejected rather than silently dropping
	// the tonnage or the existing site (AC-8).
	ErrDisposalSiteUnavailable = "MET-G1906"

	// ErrCopiedValue: a RefuseAPI method was called on a struct-copied
	// *RefuseAPI (SEC-020-class) — a copy gets its own independently zeroed
	// mu but ALIASES the original's cells/rounds/sites maps and keeps the
	// original's self pointer, so the copy is rejected before mu is ever
	// touched, mirroring engine.services' ServicesAPI (MET-G1208) and
	// engine.logistics' LogisticsAPI (MET-G405).
	ErrCopiedValue = "MET-G1907"

	// ErrDependencyNotWired: a method that drives the registered outbound
	// edges (Wire, RunRound, SetFunding, RouteGeneralToSite, …) was called
	// before Wire injected engine.logistics/engine.services. The composition
	// root wires these before the first tick; an unwired call is rejected
	// rather than silently treating a nil dependency as "empty".
	ErrDependencyNotWired = "MET-G1908"

	// ErrUnknownStream: a bin-stock query or accounting method was called
	// with a Stream value that is not one of the three registered waste
	// streams (general, recycling, food). Query-time, never a silently-created
	// zero-value stream entry (AC-13).
	ErrUnknownStream = "MET-G1909"
)
