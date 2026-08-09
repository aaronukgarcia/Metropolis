package detgate

// Placeholder registry error codes for detgate (FEAT-004).
//
// data/errors.json's "reserved" table does not yet carve out a
// sub-range for internal/engine/detgate specifically (checked via
// `grep MET-E data/errors.json` before picking this range, same
// discipline internal/engine/core/errors.go documents). engine.core has
// already informally claimed MET-E000-E099 for itself; this package
// claims the next free block, MET-E100-E199, mirroring that pattern. A
// maintainer should add the "E100-E199: reserved for engine.detgate"
// line to data/errors.json's reserved table in the same change that
// registers these codes for real (see /new-error).
//
// None of the codes below are registered in data/errors.json yet. Per
// GR#7, this is not a silent failure: errs.New/errs.Wrap detects an
// unregistered code at construction time and falls back to the
// always-available MET-F003 "unregistered error code" wrapper, so every
// error path below already fails loudly today (code + correlation ID +
// a note that the code isn't registered) and will pick up its real
// registry entry the moment someone lands it in data/errors.json — no
// call site here will need to change.
const (
	// ErrInvalidMonths: RunGate was called with months <= 0.
	ErrInvalidMonths = "MET-E100"

	// ErrTooFewRuns: RunGate was called with fewer than two RunSpecs —
	// a determinism gate that only ever runs once can never detect a
	// mismatch (AC-9's "a gate that can't fail is no gate", generalised
	// to construction time).
	ErrTooFewRuns = "MET-E101"

	// ErrCommandRejected: a real protocol.Command sent down the gate's
	// AdvanceTicks command path was rejected (SendCommand error) or came
	// back Accepted=false — an infrastructure failure distinct from a
	// hash mismatch (which is not an error; see GateReport.Verdict).
	ErrCommandRejected = "MET-E102"

	// ErrSnapshotEncodeFailed: json.Marshal of the serialize.Header
	// returned by Engine.Snapshot failed. Defensive: Header's fields are
	// all plain JSON-safe types, so this should be unreachable in
	// practice.
	ErrSnapshotEncodeFailed = "MET-E103"
)
