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

	// ErrEngineSealed: RegisterPhaseHook was called after the Engine
	// sealed its hook set (SEC-003 — see Engine.sealed's doc comment).
	// Sealing happens the first time AdvanceTicks runs; registration is
	// boot-only, and after the seal it is rejected rather than silently
	// accepted-but-ignored or left to race runPhase's unsynchronized read.
	ErrEngineSealed = "MET-E011"

	// ErrEngineCopied: RegisterPhaseHook or AdvanceTicks was called on an
	// Engine value that is not the one NewEngine constructed — i.e. a
	// struct copy (SEC-014: `e2 := *e` is legal, unsafe-free, reflect-
	// free Go, and defeats mu/sealed's per-instance safety because the
	// copy gets its OWN mu and sealed but ALIASES the original's hooks
	// map). See Engine.self's doc comment.
	ErrEngineCopied = "MET-E012"

	// ErrSubscriptionServerCopied: Subscribe, Unsubscribe, or
	// PublishEngineStatus was called on a SubscriptionServer value that
	// is not the one NewSubscriptionServer constructed — i.e. a struct
	// copy (SEC-019: same class as SEC-014/SEC-016 on Engine — `s2 := *s`
	// is legal, unsafe-free, reflect-free Go, and defeats mu's per-
	// instance safety because the copy gets its OWN mu but ALIASES the
	// original's subs map, a reference type). See
	// SubscriptionServer.self's doc comment.
	ErrSubscriptionServerCopied = "MET-E013"

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

	// ErrPrematureCommandsClose: RunCommandLoop observed its
	// CommandSource's Commands() channel close WITHOUT ctx already being
	// done — the transport went away for some reason other than the
	// shutdown the caller told the loop about (engine.headless.md AC-4,
	// the third instance of the BUG-020/MET-H004 premature-close shape,
	// now fixed in engine.core itself). Never returned on a clean
	// ctx-cancelled shutdown (RunCommandLoop returns nil for that case).
	ErrPrematureCommandsClose = "MET-E014"

	// ErrSpeed8xGateNotConfigured: checkSpeed8xAllowed's default-deny
	// branch — a SetSpeed(Speed8xDebug) command reached engine.core with
	// no Speed8xGate wired at all (WithSpeed8xGate never called). This is
	// deliberately distinct from ErrInvalidSpeed (MET-E002): the speed
	// VALUE is valid (8x is a documented multiplier once feat.debugmode
	// is wired), the failure is that nothing wired the gate that would
	// authorise it. BUG-011: reused MET-E002 for this from BUG-009 until
	// BUG-008 (the registry rewrite this stopgap was deliberately
	// avoiding colliding with) landed and stabilised data/errors.json.
	ErrSpeed8xGateNotConfigured = "MET-E015"
)
