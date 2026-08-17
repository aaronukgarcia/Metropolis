package coastal

// Registry-sourced error codes (GR#7) for engine.coastal, claimed in
// data/errors.json's ranges.reserved table under G3700-G3799 — the next
// free four-digit G-layer block after engine.social's G3600-G3699 (the E
// layer is fully exhausted and G3000-G3699 were all claimed by earlier
// engine modules, so engine.coastal opens G3700-G3799 under BUG-234's
// three-to-four-digit widening). Checked against this table AND
// `grep -rn "MET-G37" internal/ cmd/` before claiming, per BUG-008's lesson.
const (
	// ErrDataInvalid: data/coastal.json failed to load or validate.
	ErrDataInvalid = "MET-G3700"

	// ErrCopiedValue: a method was called on a struct-copied *CoastalAPI
	// (SEC-020 family, mirroring engine.comms/engine.services).
	ErrCopiedValue = "MET-G3701"

	// ErrUnknownCase: a pipeline-stage query referenced a case ID this
	// module never minted (AC-13). No zero-value stage is fabricated.
	ErrUnknownCase = "MET-G3702"

	// ErrInvalidPolicyRange: a policy coefficient was set outside [0,1]
	// (AC-13). Rejected, never silently clamped.
	ErrInvalidPolicyRange = "MET-G3703"

	// ErrInvalidShoreCell: an arrival was drawn against a cell the shore
	// source does not classify as shore (AC-14). No event is placed.
	ErrInvalidShoreCell = "MET-G3704"

	// ErrDependencyMissing: an outbound dependency required by an operation
	// is not wired (GR#17 — fail closed, never fabricate).
	ErrDependencyMissing = "MET-G3705"

	// ErrNonFinite: a NaN or ±Inf value reached a numeric boundary
	// (SEC-093 — rejected before any ordered range check).
	ErrNonFinite = "MET-G3706"

	// ErrOutOfRange: a finite numeric input fell outside its documented
	// domain (e.g. a negative month). Rejected, never silently clamped.
	ErrOutOfRange = "MET-G3707"

	// ErrInvalidEraTier: an era/milestone tier outside 0..13 reached the
	// frequency multiplier table (AC-3).
	ErrInvalidEraTier = "MET-G3708"

	// ErrInvalidSeasonIndex: a season index outside 0..3 reached the
	// frequency multiplier table (AC-3).
	ErrInvalidSeasonIndex = "MET-G3709"

	// ErrShoreNotWired: arrival generation ran with no shore source wired
	// (AC-14 — fail closed rather than inventing cells).
	ErrShoreNotWired = "MET-G3710"
)
