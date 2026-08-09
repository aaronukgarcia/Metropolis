package core

// Registry error codes for engine.core (MOD-012). Range: E000-E099,
// declared in data/errors.json's "ranges.reserved" table. Every code
// below IS registered there with real severity/module/message/remedy
// fields (GR#7; closed under BUG-008) — see that file's "E000-E099"
// reserved-range entry and its "codes" section. The
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync again, and against another module's range
// accidentally overlapping this one (BUG-008's root cause).
const (
	// ErrInvalidAdvanceTicks: AdvanceTicksPayload.N was <= 0 or exceeded
	// MaxAdvanceTicksPerCall (AC-11: rejected, never silently clamped).
	ErrInvalidAdvanceTicks = "MET-E000"

	// ErrPhaseHookFailed: a registered PhaseHook's RunShard returned an
	// error for at least one shard during a phase; the phase's barrier
	// still ran for whatever effects were emitted, but the tick's
	// remaining phases were aborted (AC-10).
	ErrPhaseHookFailed = "MET-E001"

	// ErrInvalidSpeed: SetSpeedPayload.Speed was not one of the
	// documented multipliers (1, 2, 4; 8 reserved for feat.debugmode).
	ErrInvalidSpeed = "MET-E002"

	// ErrInvalidViewName: SubscribePayload.ViewName failed
	// protocol.ValidateViewName's naming-scheme check.
	ErrInvalidViewName = "MET-E003"

	// ErrUnknownView: SubscribePayload.ViewName was well-formed but
	// names a view T-SUBSCR does not (yet) serve. v1 serves exactly one
	// view: "engine.status".
	ErrUnknownView = "MET-E004"

	// ErrUnknownSubscription: UnsubscribePayload.SubscriptionID does not
	// name a live subscription.
	ErrUnknownSubscription = "MET-E005"

	// ErrSnapshotFailed: Snapshot's marshalling or shard-write step
	// failed (e.g. the caller-supplied io.Writer returned an error).
	ErrSnapshotFailed = "MET-E006"

	// ErrNilPhaseHook: RegisterPhaseHook was called with a nil hook.
	ErrNilPhaseHook = "MET-E007"

	// ErrUnknownPhase: RegisterPhaseHook was called with a PhaseKind not
	// present in DailyPhaseOrder or MonthlyPhaseOrder.
	ErrUnknownPhase = "MET-E008"

	// ErrInvalidEnvelope: HandleCommand received a Command that fails
	// protocol.Command.Validate (wrong ProtocolVersion, empty
	// CorrelationID, nil/mismatched Payload). Defensive: protocol's
	// InProcTransport.SendCommand already validates before enqueueing,
	// so this should only be reachable if a caller invokes
	// HandleCommand directly with a hand-built Command that skipped
	// that check (e.g. a test, or a future transport that forgets to).
	ErrInvalidEnvelope = "MET-E010"

	// ErrUnhandledCommandKind: HandleCommand received a Command whose
	// Kind is not one of the eight v1 command kinds this package
	// handles. Defensive: protocol.Command.Validate already closes the
	// Kind<->payload mapping via commandRegistry, so this should be
	// unreachable in practice, but a closed switch with an explicit
	// default (rather than a silent no-op) is cheap insurance against a
	// future protocol Kind landing here unhandled.
	ErrUnhandledCommandKind = "MET-E009"
)
