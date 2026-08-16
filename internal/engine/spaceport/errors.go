package spaceport

// Registry error codes for engine.spaceport (MOD-076). Range: G3000-G3099,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) is fully exhausted by eleven earlier engine
// modules, and the G layer's blocks through G2900-G2999 were all claimed
// before this module landed (engine.citizens G000-G099 … engine.news
// G2300-G2399, engine.accelerator G2400-G2499, feat.pharmacampus
// G2500-G2599, feat.refinery G2600-G2699, engine.census G2700-G2799,
// engine.airport G2800-G2899, engine.worklife G2900-G2999), so
// engine.spaceport opens G3000-G3099 under BUG-234's 2026-08-14
// three-to-four-digit code-format widening. Checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G30" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current — no prior MET-G30xx code
// existed either place. Every code below IS registered in data/errors.json
// with real severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrSpaceportDataInvalid: data/spaceport.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, a non-positive
	// version, an empty catalogue anchor, or a numeric value missing its
	// unit/disclosure field — AC-15). Load-time.
	ErrSpaceportDataInvalid = "MET-G3000"

	// ErrCatalogueAnchorUnresolved: the spaceport's catalogue anchor does
	// not resolve to exactly one data/buildings.json entry (zero or more
	// than one match). AC-1's "exactly one catalogue anchor" — a silent
	// second launch-site entry is a GR#3 violation, so the ambiguity is a
	// load-time rejection, never a silent default.
	ErrCatalogueAnchorUnresolved = "MET-G3001"

	// ErrUnknownFacilityKey: a build command named a facility key outside
	// the spaceport's taxonomy (anything other than the resolved catalogue
	// anchor). Rejected before any state is touched (AC-10).
	ErrUnknownFacilityKey = "MET-G3002"

	// ErrExpertGateUnmet: the shared §8 expert gate was not met — the
	// education output was below data/spaceport.json's expert threshold.
	// Money, development points, and milestones cannot substitute (AC-3).
	ErrExpertGateUnmet = "MET-G3003"

	// ErrPermitMissing: the facility permit gate (FEAT-053) did not hold a
	// valid permit for the spaceport. Delegated, not reimplemented (AC-8).
	ErrPermitMissing = "MET-G3004"

	// ErrLaunchUnbuilt: a launch was requested against an unbuilt or
	// incomplete facility. Launches never fire before the build completes
	// (AC-4/AC-10).
	ErrLaunchUnbuilt = "MET-G3005"

	// ErrAlreadyBuilt: a build command was issued for a spaceport that is
	// already building or built. One spaceport each (§MP); never a silent
	// double-build.
	ErrAlreadyBuilt = "MET-G3006"

	// ErrCopiedValue: a SpaceportAPI method was called on a struct-copied
	// value (SEC-020 family). A copy would alias the mutex/maps across two
	// values, so the call is rejected before the lock is touched.
	ErrCopiedValue = "MET-G3007"

	// ErrDependencyMissing: an operation that needs a wired seam (the
	// education gate, permit gate, decommission liability, FDI draw, or
	// tourism draw) was invoked before that seam was wired.
	ErrDependencyMissing = "MET-G3008"

	// ErrInvalidMonth: Tick was called with a negative month index.
	ErrInvalidMonth = "MET-G3009"

	// ErrInvalidSite: a build command carried negative site coordinates for
	// the exclusion-contour centre.
	ErrInvalidSite = "MET-G3010"
)
