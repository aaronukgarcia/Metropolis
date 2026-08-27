package build

// Registry error codes for engine.build (MOD-026). Range: G500-G599,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed, and the G layer's earlier blocks belong to engine.citizens
// (G000-G099), engine.projections (G100-G199), engine.finance (G200-G299),
// engine.consumption (G300-G399), and engine.logistics (G400-G499);
// G500-G599 is the next free engine sub-range, checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G5" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrZoneDataInvalid: data/buildings.json's "zones" array could not be
	// loaded, failed foundation.data's schema validation (missing materials
	// bill, negative lead time/labour/materials), names an unrecognised
	// zone type, or is missing one of the eight §34 zone types this module
	// requires (AC-11). Wraps the underlying foundation.data error (itself
	// already registry-sourced) so callers see this package's own code AND,
	// via errors.Unwrap, the original cause. Never a silent default
	// substitution of a malformed catalogue entry.
	ErrZoneDataInvalid = "MET-G500"

	// ErrUnknownZoneType: a SubmitZoneCommand/SubmitBuildCommand/Demand
	// query named a zone-type string that is not one of the eight §34 zone
	// types (AC-10). Rejected loudly, never a panic or a silently-accepted
	// no-op a caller could mistake for success.
	ErrUnknownZoneType = "MET-G501"

	// ErrCellOutOfBounds: a command/query referenced a tile outside the
	// 30x30 expansion extent or a local cell outside the 200x200 tile grid
	// (AC-10). Rejected before any state is touched.
	ErrCellOutOfBounds = "MET-G502"

	// ErrCellNotOwned: the ownership gate (AC-3) — a zone/build/demolish
	// command against a cell whose tile is not owned by the issuing owner.
	// Rejected at command-acceptance time with no zone/queue mutation.
	ErrCellNotOwned = "MET-G503"

	// ErrNoStructure: SubmitDemolishCommand against a cell with no
	// completed structure to demolish (AC-10). Never a silent no-op.
	ErrNoStructure = "MET-G504"

	// ErrCopiedValue: a BuildAPI method was called on a struct-copied
	// value, not the one Load/LoadDefault constructed (SEC-020-class,
	// mirroring engine.finance's MET-G204 and engine.logistics's MET-G405).
	ErrCopiedValue = "MET-G505"

	// ErrInvalidSeasonalMultiplier: engine.season's
	// ConstructionSpeedMultiplier returned a NaN/Inf/non-positive value,
	// which would make effective lead time undefined or infinite (GR#16).
	ErrInvalidSeasonalMultiplier = "MET-G506"

	// ErrInvalidMonth: a negative month index was passed to SubmitBuildCommand
	// or Tick. Month indices are absolute and non-negative (0 = genesis).
	ErrInvalidMonth = "MET-G507"

	// ErrDependencyMissing: an operation was invoked before its required
	// dependency (world/season/logistics) was wired via the Set* setters.
	// Never a silent no-op (GR#1/GR#17).
	ErrDependencyMissing = "MET-G508"

	// ErrInvalidDistrict: SetDistrict was given an invalid district parameter
	// (empty string). District identifiers are required and must be non-empty.
	ErrInvalidDistrict = "MET-G509"
)
