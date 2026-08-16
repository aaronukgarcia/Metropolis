package education

// Registry error codes for engine.education (MOD-041). Range: G1800-G1899,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) is fully exhausted by earlier engine modules, and
// the G layer's three-digit namespace plus the four-digit blocks through
// G1700-G1799 were all claimed before this module landed (engine.citizens
// G000-G099 … engine.firms G1400-G1499, engine.crime G1500-G1599,
// engine.farming G1600-G1699, engine.rail G1700-G1799), so engine.education
// opens G1800-G1899 under BUG-234's 2026-08-14 code-format widening (three
// digits → three-or-four). Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G18" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always current
// — no prior MET-G18xx code existed either place. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against this ever drifting out of sync.
const (
	// ErrEducationDataInvalid: data/education.json could not be loaded or
	// failed this package's schema validation (missing file, malformed
	// JSON, a non-positive entry-age gate, an out-of-domain attainment
	// scale, a negative research-points/halls figure). Load-time.
	ErrEducationDataInvalid = "MET-G1800"

	// ErrStageNotRegistered: a stage-funding command or a stage query was
	// issued for a Stage value that has not been registered as a service
	// instance (RegisterStages must run first). AC-12 — never a silently
	// zero-valued enrolment/funding answer for a stage that does not exist.
	ErrStageNotRegistered = "MET-G1801"

	// ErrInvalidCitizenState: an enrolment or transition was requested for
	// a citizen with no valid age/stage-history state (unknown citizen id,
	// negative age). AC-12 — never a silently-created zero-value enrolment
	// record for a citizen whose state cannot be resolved.
	ErrInvalidCitizenState = "MET-G1802"

	// ErrForkMismatch: an ApplyFork command whose three branch counts do
	// not sum exactly to the secondary cohort's full eligible size. AC-13 —
	// the conservation identity is enforced at the write boundary, not only
	// checked after the fact, so a shortfall/overcount can never silently
	// vanish a pupil.
	ErrForkMismatch = "MET-G1803"

	// ErrSlowFusePayloadMissing: a stage-funding command (AC-9) whose
	// principal effect lands more than five game-years out — which, per
	// §27's own text, is every education funding decision — was submitted
	// without an attached projected-consequence payload. This module's own
	// pre-submission check (A5/US-5) rejects it before it ever reaches
	// engine.projections' Slow-Fuse gate.
	ErrSlowFusePayloadMissing = "MET-G1804"

	// ErrInvalidFunding: SetStageFunding was called with a level outside
	// the valid [0,1] range. Rejected rather than silently clamped.
	ErrInvalidFunding = "MET-G1805"

	// ErrCopiedValue: an EducationAPI method was called on a struct-copied
	// value (SEC-020 family). A copied *EducationAPI would alias its
	// mutex/maps across two values, so the copy guard rejects the call.
	ErrCopiedValue = "MET-G1806"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (citizens, services, season, traffic, projections) was invoked before
	// that dependency was wired. Fails loudly rather than no-op-ing.
	ErrDependencyMissing = "MET-G1807"

	// ErrInvalidDepartureReason: RemovePupil was called with a
	// DepartureReason outside the three documented values (deceased,
	// emigrated, dropped out). Rejected at the write boundary BEFORE any
	// mutation, so the live enrolled count and the independent ledger
	// replay can never diverge because a departure term went unrecorded
	// (AC-10).
	ErrInvalidDepartureReason = "MET-G1808"

	// ErrInvalidFuseYears: SetStageFunding was called with a non-finite
	// (NaN/±Inf) or non-positive FuseYears tag. A degenerate tag must never
	// reach the Slow-Fuse threshold comparison (where NaN > threshold is
	// false and would read as "under threshold"), mirroring
	// engine.projections' own finite-tag guard (AC-9).
	ErrInvalidFuseYears = "MET-G1809"

	// ErrInvalidSeries: a ProjectedConsequence.Series carried a non-finite
	// (NaN/±Inf) value. Such a value would make projectedDelta (last-first)
	// non-finite and that delta is swallowed into EnqueueDecision's queued
	// step, poisoning Curve(attainmentCurveKey) with NaN/±Inf forever. The
	// series is finite-checked at the write boundary, before the projection
	// is enqueued (Destructive-MOD041-r2 DEFECT 2).
	ErrInvalidSeries = "MET-G1810"
)
