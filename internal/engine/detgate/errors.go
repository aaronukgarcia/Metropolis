package detgate

// Registry error codes for detgate (FEAT-004, module key
// "engine.detgate"). Range: E100-E199, declared in data/errors.json's
// "ranges.reserved" table. Every code below IS registered there with
// real severity/module/message/remedy fields (GR#7; closed under
// BUG-008 — this exact range was the one that collided: feat.debugmode
// was mistakenly registered against E100-E199 on 2026-08-09 because
// this package's claim existed only in this comment, not in the
// registry, so a registry-only search found the range "free". See
// data/errors.json's "E100-E199" reserved-range entry for the full
// incident note.). The internal/foundation/errs source-scan test now
// guards against this recurring, for this range and every other.
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
