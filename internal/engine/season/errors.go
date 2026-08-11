package season

// Registry error codes for engine.season (MOD-027). Range: E500-E599,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master
// table — E000-E499 and E900-E999 were already claimed by engine.core,
// engine.detgate, feat.debugmode, engine.invariant, engine.world and
// feat.skeleton respectively; E500-E599 was the next free engine
// sub-range, checked against data/errors.json's "ranges.reserved" table
// AND `grep -rn "MET-E5" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current). Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against this ever drifting out of sync.
const (
	// ErrSeasonalDataInvalid: data/seasonal.json could not be loaded or
	// failed foundation.data's schema validation (missing file, malformed
	// JSON, a curve with other than 12 month points, a negative
	// multiplier). Wraps the underlying foundation.data error (itself
	// already registry-sourced under an F60x code) so callers see both
	// this package's own correlation ID and, via errors.Unwrap, the
	// original cause.
	ErrSeasonalDataInvalid = "MET-E500"

	// ErrMissingCurve: data/seasonal.json loaded and schema-validated
	// successfully but is missing one of the eight curves engine.season
	// requires (§9/§17) — a data-authoring omission distinct from a
	// schema violation, so foundation.data's generic loader cannot catch
	// it (it has no notion of which curve names this consumer needs).
	ErrMissingCurve = "MET-E501"

	// ErrNegativeMonthIndex: a curve function was queried with a month
	// index < 0 (before the world's epoch, Clock.Month()==0) — AC-13.
	ErrNegativeMonthIndex = "MET-E502"

	// ErrCurveLookupFailed: an internal invariant failure — a curve name
	// this package itself declared required (and Load already verified
	// present) was absent at query time. Should be unreachable in
	// practice (Load's completeness check runs once at construction and
	// the returned *SeasonAPI's curve map is never mutated after that),
	// but query methods return this registry-sourced error instead of
	// panicking or indexing out of bounds if it ever somehow is.
	ErrCurveLookupFailed = "MET-E503"

	// ErrIntakeGateShapeInvalid: data/seasonal.json's schoolIntakeGate
	// curve does not have exactly one calendar month at or above
	// schoolIntakeGateThreshold (BUG-059). §9/US-4 treat this curve as a
	// once-per-year boolean gate (education's stage-transition trigger),
	// not a continuous multiplier — zero qualifying months means intake
	// silently never fires, two-or-more means it silently double-fires,
	// and neither foundation/data.Seasonal.Validate (generic, curve-
	// name-agnostic) nor engine.season.Load's curve-presence check
	// catches either shape. Fails closed at Load time instead (GR#7).
	ErrIntakeGateShapeInvalid = "MET-E504"
)
