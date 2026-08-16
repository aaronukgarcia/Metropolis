package worklife

// Registry error codes for engine.worklife (MOD-080). Range: G2900-G2999,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The G layer (engine second block) was claimed through G2800-G2899
// (engine.airport) by the time this package landed, so engine.worklife
// opens G2900-G2999 under BUG-234's three-to-four-digit code-format
// widening. Checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G29" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G29xx code existed either place. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against this ever drifting out of sync.
const (
	// ErrDataInvalid: data/worklife.json could not be loaded or failed this
	// package's schema validation (a missing file, malformed JSON, a
	// non-positive hours/day, a negative shift-rotation length, an
	// unrecognised pattern-kind string, or a rotation set that does not
	// contiguously tile the coverage span). Load-time (AC-15/GR#15) —
	// never a silent default substitution, never a silently-clamped value.
	ErrDataInvalid = "MET-G2900"

	// ErrUnknownPattern: a query referenced a pattern kind that is not one
	// of the three documented kinds (core-hours / shift / any-time) carried
	// in the loaded data. Rejected rather than silently treated as a
	// "worker who is never at work" (AC-14).
	ErrUnknownPattern = "MET-G2901"

	// ErrInvalidHours: an hours input was out of domain (negative, or more
	// than 24 hours in a day). Rejected rather than silently clamped and
	// folded into a coverage/hours figure (AC-14).
	ErrInvalidHours = "MET-G2902"

	// ErrUnknownPolicy: reserved for the by-ID working-week-policy lookup (a
	// future WorkScheduleAPI surface that resolves a policy reference by
	// name). The current active-policy read routes through the PoliciesAPI
	// seam, which propagates its own registry-sourced unknown-policy error,
	// so this code is not yet raised (mirroring engine.wellbeing's reserved
	// ErrUnknownCitizen). AC-14's "unknown working-week policy reference"
	// case is covered by that propagation path.
	ErrUnknownPolicy = "MET-G2903"

	// ErrCopiedValue: a WorkScheduleAPI method was called on a
	// struct-copied value (SEC-020 family). A copied *WorkScheduleAPI
	// would alias its mutex/source fields across two values, so the copy
	// guard rejects the call rather than let a torn read/write pass.
	ErrCopiedValue = "MET-G2904"

	// ErrDependencyMissing: a query that must push through a wired seam
	// (wellbeing) was called before that seam was wired (AC-12's push path).
	ErrDependencyMissing = "MET-G2905"

	// ErrNonFiniteInput: a wage coefficient or wellbeing weight crossing a
	// module boundary was NaN or ±Inf, or a computed boundary value was
	// non-finite. Rejected at the boundary rather than propagated (SEC-093).
	ErrNonFiniteInput = "MET-G2906"
)
