package airport

// Registry error codes for engine.airport (MOD-075). Range: G2800-G2899,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
// The G layer's blocks through G2600-G2699 were all claimed by the time this
// module landed (engine.citizens G000-G099 … feat.refinery G2600-G2699), and
// G2700-G2799 carries the in-flight engine.census work (which already raises
// MET-G2700-G2704 in its uncommitted source), so engine.airport opens
// G2800-G2899 — checked against data/errors.json's "ranges.reserved" table
// AND `grep -rn "MET-G28" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards against
// drift.
const (
	// ErrAirportDataInvalid: data/airport.json could not be loaded or failed
	// schema validation (missing file, malformed JSON, a tier missing its
	// key/name/disclosure, a non-positive runway/per-runway-rate/gate/
	// per-gate-rate/reach-multiplier/freight-apron/contour-radius/land-
	// footprint/reduced-throughput-percentage figure, a negative jobs/cost/
	// milestone figure, an unknown access tier or blight class, an out-of-
	// range reduced-throughput percentage, or a reach/access ladder that is
	// not monotonic non-decreasing with at least one strict reach increase).
	// Load is all-or-nothing: no partial ladder and no silent default
	// substitution (AC-11). Distinct from ErrAirportBuildRejected.
	ErrAirportDataInvalid = "MET-G2800"

	// ErrAirportBuildRejected: Build was refused — no permit authority wired
	// or permit not granted (AC-9, permit-gated via feat.facilitypermits),
	// below the tier's data-driven milestone gate, insufficient land footprint
	// (AC-9), a non-upgrade (downgrade or repeat) of an already-built airport,
	// no blight registrar wired or a noise contour the blight registrar refuses
	// to register (AC-7/AC-10), or an air-cargo command through an unwired
	// engine.freight seam / with a non-positive tonnage (AC-4). Never a panic, a
	// silent no-op, a downgrade
	// of activeTier, or a partially-created airport (AC-10).
	ErrAirportBuildRejected = "MET-G2801"

	// ErrUnknownAirport: a tier query, a Build, or a component query named an
	// airport-tier key that is not one of the loaded data/airport.json tiers,
	// or queried a component of an airport that has not been built yet.
	// Query- or build-time, never a silently-created zero-value tier or a
	// silently-zeroed component figure (AC-10).
	ErrUnknownAirport = "MET-G2802"

	// ErrAirportCopiedValue: an AirportAPI method was called on a struct-copied
	// *AirportAPI (SEC-020 family, mirroring engine.freight's ErrCopiedValue).
	// Always construct exactly one *AirportAPI via Load/LoadDefault and pass
	// its pointer everywhere.
	ErrAirportCopiedValue = "MET-G2803"
)
