package protocol

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// ProtocolVersion is the wire version of this envelope schema, now
// DERIVED from the semver'd CurrentWireVersion (version.go, FEAT-1972079936
// Phase 0 inc1) rather than an independently hand-maintained string — see
// version.go's package doc for why a real major/minor pair replaced the
// old opaque string. Every Command still carries it verbatim; Validate
// below now accepts any minor at-or-below CurrentWireVersion's, for the
// same major, rather than exact string equality — see docs/design/
// protocol.md "Versioning & extension rules" for how v1.1 (additive)
// differs from a v2 (breaking) bump, now formalized by WireVersion.Major.

// Tick is simulation time: the monotonically increasing logistics
// day-tick / monthly-tick counter the engine's phase pipeline advances
// (GDD §3, M0-ENG §1.1). It is NEVER derived from the wall clock — this
// package must not call time.Now anywhere, and no field in this package
// may be populated from it. A Tick of 0 is the world's genesis tick.
type Tick int64

// CorrelationID identifies one causal chain: a Command and every Event,
// Delta, and CommandResult it causes. It is minted by the initiating
// side (typically the UI, for a player action) and propagates unchanged
// through engine events, deltas, logs, and error records so any failure
// is traceable end-to-end (GR#1; code.json conventions.errorHandling).
//
// It is a plain string, not a UUID type, because the protocol package
// must not assume or enforce a particular ID scheme on callers who mint
// their own (tests, replay fixtures) — NewCorrelationID is provided as
// the default generator, but any non-empty string is a valid
// CorrelationID as far as this package is concerned.
type CorrelationID string

// Validate reports whether c is usable as a Command's CorrelationID.
// Mandatory per GR#1 and code.json's errorHandling.correlation contract.
func (c CorrelationID) Validate() error {
	if c == "" {
		return ErrMissingCorrelationID
	}
	return nil
}

// ErrMissingCorrelationID is returned when a Command's CorrelationID is
// empty. Every Command must carry one — see CorrelationID.
var ErrMissingCorrelationID = errors.New("protocol: correlation ID is required and must not be empty")

// NewCorrelationID mints a random RFC 4122 version-4 UUID string using
// crypto/rand (never math/rand, never a time-seeded source — this keeps
// ID generation independent of wall-clock time, consistent with the rest
// of this package). It is a convenience for callers that don't already
// have a correlation ID scheme (CLI tools, tests, fixtures); the UI and
// engine are free to mint their own as long as it is non-empty.
func NewCorrelationID() CorrelationID {
	var b [16]byte
	// crypto/rand.Read on the package-level Reader never returns a short
	// read without an error on any platform Go supports; a non-nil error
	// here means the OS entropy source is broken, which is unrecoverable
	// for anything relying on unique IDs. Fall back to an all-zero UUID
	// tagged distinctly rather than panicking a library package.
	if _, err := rand.Read(b[:]); err != nil {
		return CorrelationID("00000000-0000-4000-8000-000000000000")
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return CorrelationID(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// Kind names a Command's or Event's payload variant. It is a plain
// string (not iota-based) so the wire format is stable and
// human-readable in logs and fixtures. See commands.go for the v1
// command Kind constants and the extension rule for adding new ones.
type Kind string

// CommandPayload is implemented by every typed command payload struct in
// commands.go. It exists so Command.Payload can hold any registered
// payload type while still being able to assert, at decode time, that
// the payload the caller constructed agrees with the envelope's Kind
// field (see Command.Validate).
type CommandPayload interface {
	// commandKind returns the Kind this payload type is registered
	// under. Unexported: only this package may implement CommandPayload,
	// which keeps the Kind<->payload mapping (commands.go's registry)
	// exhaustive and closed to outside packages — new command kinds are
	// added here, in commands.go, not by external implementers.
	commandKind() Kind
}

// Command is the versioned envelope for every message the UI (or a
// harness/tool acting as the UI) sends the engine. Field order is
// deliberate and part of the wire contract: see codec.go's "Deterministic
// marshalling" note — fixture hashing relies on struct field order for
// JSON object key order.
type Command struct {
	ProtocolVersion string         `json:"protocolVersion"`
	CorrelationID   CorrelationID  `json:"correlationId"`
	IssuedAtTick    Tick           `json:"issuedAtTick"`
	Kind            Kind           `json:"kind"`
	Payload         CommandPayload `json:"payload"`
}

// Validate checks that cmd is well-formed enough to enqueue: correct
// ProtocolVersion, non-empty CorrelationID, a non-nil Payload, and that
// Payload's registered Kind agrees with the envelope's Kind field. It
// does NOT validate payload-internal invariants (e.g. AdvanceTicksPayload.N
// > 0) — that is the engine's job once it decodes the command, using
// registry-sourced errors (GR#7); this package only guards the envelope
// contract itself.
func (cmd Command) Validate() error {
	got, err := ParseWireVersion(cmd.ProtocolVersion)
	if err != nil {
		return fmt.Errorf("%w: got %q: %v", ErrUnsupportedProtocolVersion, cmd.ProtocolVersion, err)
	}
	current := CurrentWireVersion
	// Major must match exactly (a breaking change — WireVersion's own doc
	// comment) with no window/shim to bridge it in this increment (that
	// is increments 2-3's job, AC-3/AC-4). Minor is additive-tolerant: an
	// OLDER minor than this build's current is fine (an older client just
	// never sent/reads the newer fields); a NEWER minor than this build
	// knows about is refused, since this build genuinely does not
	// understand what that minor may have added.
	if got.Major != current.Major || got.Minor > current.Minor {
		return fmt.Errorf("%w: got %q, want %q (major must match; minor must be <= current)", ErrUnsupportedProtocolVersion, cmd.ProtocolVersion, current.String())
	}
	if err := cmd.CorrelationID.Validate(); err != nil {
		return err
	}
	if cmd.Payload == nil {
		return fmt.Errorf("%w: kind %q", ErrNilPayload, cmd.Kind)
	}
	if got := cmd.Payload.commandKind(); got != cmd.Kind {
		return fmt.Errorf("%w: envelope kind %q, payload registered under %q", ErrKindPayloadMismatch, cmd.Kind, got)
	}
	return nil
}

// ErrUnsupportedProtocolVersion is returned by Command.Validate when a
// Command's ProtocolVersion is not the ProtocolVersion this build speaks.
var ErrUnsupportedProtocolVersion = errors.New("protocol: unsupported protocol version")

// ErrNilPayload is returned by Command.Validate when Payload is nil.
var ErrNilPayload = errors.New("protocol: command payload must not be nil")

// ErrKindPayloadMismatch is returned by Command.Validate when the
// envelope's Kind does not match the Kind the Payload type is registered
// under (commands.go's registry is the source of truth for that mapping).
var ErrKindPayloadMismatch = errors.New("protocol: command kind does not match payload type")

// ErrorRef is the registry-sourced error carried on a failed
// CommandResult. It is deliberately NOT a Go error value: the engine and
// UI are separate domains with no shared memory (M0-ENG §1.1), so the
// only thing that may cross the seam is data — a MET-xxxx code plus a
// display string, both of which the UI can show verbatim per GR#1
// ("every user-visible failure shows its registry code + correlation
// ID"). The receiving side, if it needs Go error semantics locally,
// reconstructs them from Code via its own error registry lookup — it
// never deserializes a Go error type off the wire.
type ErrorRef struct {
	// Code is a registry error code, format MET-<layer>NNN (data/errors.json).
	// Always present on a non-nil ErrorRef.
	Code string `json:"code"`
	// Display is the human-readable message for the F12 panel / status
	// line, already resolved from the registry (interpolated, no
	// placeholders like "{path}" left in) so the UI never has to know the
	// registry format.
	Display string `json:"display"`
}

// CommandResult is the direct acknowledgement of one Command: whether it
// was accepted, and if not, why. It echoes the causing Command's
// CorrelationID (mandatory — a CommandResult with no correlating command
// is meaningless) and the Tick at which the engine processed it.
//
// CommandResult answers "was my command accepted", not "what happened as
// a result" — a Command may be Accepted and still produce zero, one, or
// many Events and Deltas afterward as the engine simulates it forward.
type CommandResult struct {
	CorrelationID CorrelationID `json:"correlationId"`
	Tick          Tick          `json:"tick"`
	Accepted      bool          `json:"accepted"`
	// Error is non-nil iff Accepted is false. A CommandResult that
	// rejects a command MUST carry an ErrorRef (GR#7) — see
	// Command.Validate's sibling check in codec.go's result validation.
	Error *ErrorRef `json:"error,omitempty"`
}

// Validate checks CommandResult-level invariants: non-empty
// CorrelationID, and Error present iff Accepted is false.
func (r CommandResult) Validate() error {
	if err := r.CorrelationID.Validate(); err != nil {
		return err
	}
	if r.Accepted && r.Error != nil {
		return ErrAcceptedResultHasError
	}
	if !r.Accepted && r.Error == nil {
		return ErrRejectedResultMissingError
	}
	return nil
}

// ErrAcceptedResultHasError is returned when a CommandResult claims
// Accepted=true but also carries a non-nil Error — the two are mutually
// exclusive.
var ErrAcceptedResultHasError = errors.New("protocol: accepted command result must not carry an error")

// ErrRejectedResultMissingError is returned when a CommandResult claims
// Accepted=false but Error is nil — GR#7 requires every rejection name a
// registry error code.
var ErrRejectedResultMissingError = errors.New("protocol: rejected command result must carry a registry ErrorRef")
