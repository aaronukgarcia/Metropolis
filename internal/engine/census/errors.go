package census

// Registry error codes for engine.census (MOD-078).
//
// engine.census owns the G2700-G2799 sub-range (data/errors.json
// ranges.reserved). The E layer (E000-E999) is fully exhausted and the G
// layer's blocks through G2600-G2699 were claimed by earlier engine
// modules (engine.citizens G000 … engine.wellbeing G2200, engine.news
// G2300, engine.accelerator G2400, engine.fdi G2500, feat.refinery G2600),
// so engine.census opens G2700-G2799 under BUG-234's three-to-four-digit
// code-format widening. Checked against this table AND
// `grep -rn "MET-G27" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G27xx code existed either place.
const (
	// ErrCopiedValue: a *CensusAPI method was called on a struct-copied
	// value, not the one New/Load constructed (SEC-020 family). A copy
	// gets its own, independently-zeroed mu but ALIASES the original's
	// tracked/history maps and source pointers.
	ErrCopiedValue = "MET-G2700"

	// ErrUnknownObject: a bio/check-in query was made for a GUID no
	// tracked object carries (AC-21). No zero-value bio or check-in
	// record is returned — the caller gets this registry-sourced error
	// instead of a fabricated empty figure.
	ErrUnknownObject = "MET-G2701"

	// ErrUnknownKey: a KPI/statistic/aggregate key was queried that the
	// census does not expose (AC-21, AC-20's Source resolution). No
	// zero-value aggregate is returned — the caller cannot mistake a
	// fabricated "0" for a real empty figure.
	ErrUnknownKey = "MET-G2702"

	// ErrCensusDataInvalid: data/census.json could not be loaded or
	// failed schema validation (AC-22). The module never falls back to a
	// partial config or silently substitutes a default for a malformed
	// parameter — a data-authoring bug is surfaced, not masked.
	ErrCensusDataInvalid = "MET-G2703"

	// ErrDependencyMissing: an observation/query operation requires a
	// source (one of the seven consumed modules' interfaces) that has not
	// been wired. The census fails closed rather than silently reporting
	// a zero aggregate from an absent source (GR#1).
	ErrDependencyMissing = "MET-G2704"

	// ErrInvalidStageKind: an education source returned a stage kind
	// outside the census's [0,7] stage domain. The snapshot boundary
	// rejects it (SEC-126) rather than indexing a fixed [numStages]int64
	// array out of range and panicking the observation pipeline.
	ErrInvalidStageKind = "MET-G2705"

	// ErrInvalidGUID: TrackObject was called with a GUID that fails
	// parseGUID, or with a kind/lifeSpan that contradicts the GUID's
	// prefix (SEC-127). The GUID is rejected rather than stored unvalidated
	// where it could never round-trip through the bio surfaces.
	ErrInvalidGUID = "MET-G2706"

	// ErrNonFiniteSource: a consumed source returned a NaN/±Inf float where
	// the census stores a finite figure (SEC-130). Rejected at the snapshot
	// boundary so a non-finite value can never silently disable the
	// regulator watchdog (NaN > threshold is false — GR#17).
	ErrNonFiniteSource = "MET-G2707"
)
