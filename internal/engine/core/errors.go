package core

// Placeholder registry error codes for engine.core (MOD-012).
//
// data/errors.json's "ranges.layers" section reserves the single letter
// "E" for "engine — internal/engine/* (simulation modules)" but, as of
// this writing, its "ranges.reserved" table (which subdivides F000-F599
// and U000-U099 into per-module blocks) has no entry carving out a
// sub-range for engine.core specifically — no other engine.* module has
// claimed E000-E099 either (checked via `grep MET-E data/errors.json`
// before picking this range). This package claims MET-E000-MET-E099 as
// engine.core's block, mirroring foundation.det's F200-F299 pattern
// (see internal/foundation/det/errors.go); a maintainer should add the
// "E000-E099: reserved for engine.core" line to data/errors.json's
// reserved table in the same change that registers these codes for
// real (see /new-error).
//
// None of the codes below are registered in data/errors.json yet. This
// is not a silent failure (GR#7): errs.New/errs.Wrap detects an
// unregistered code at construction time and falls back to the
// always-available MET-F003 "unregistered error code" wrapper, so every
// error path below already fails loudly today (code + correlation ID
// + a note that the code isn't registered) and will pick up its real
// registry entry (message/remedy/severity) the moment someone lands it
// in data/errors.json — no call site here will need to change.
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
