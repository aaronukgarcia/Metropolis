package core

import (
	"context"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is engine.core's command loop (M0-ENG §1.1's T-ENGINE
// consuming a Transport): HandleCommand decodes and dispatches one
// protocol.Command to the matching Engine behaviour and returns the
// CommandResult to send back; RunCommandLoop drives that repeatedly
// off a live Transport until told to stop.
//
// Every rejection is built via errs.New/errs.Wrap with one of this
// package's MET-E0xx placeholder codes (errors.go) — GR#7, AC-10/AC-11.
// HandleCommand itself never panics: an unexpected payload type or
// Kind is a rejected CommandResult, never a crash (mirrors
// foundation.registry's "standard ok-idiom" philosophy).

// DeltaSink is the minimal push surface T-SUBSCR (subscribe.go) needs
// from a transport. *protocol.InProcTransport satisfies it today via
// SendDelta; a future gRPC-backed Transport implementation would too.
//
// CONTRACT (independent round r2/r3, FEAT-208 increment 1 — this
// contract previously lived only on protocol.Transport's own doc
// comment, not here on the interface Publish actually depends on; r2's
// attack proved that gap was real, not academic):
//
//   - SendDelta MUST NOT block indefinitely. subscribe.go's Publish
//     calls SendDelta while holding SubscriptionServer.publishMu for
//     the duration of the whole delivery pass (never s.mu — see
//     publishMu's own doc comment for the R3 two-mutex split this
//     replaces r1's "hold s.mu across SendDelta" fix with) — a
//     SendDelta implementation that blocks unboundedly stalls every
//     subsequent Publish call (they queue on publishMu), though it can
//     no longer stall Subscribe/Unsubscribe/RegisterView (those only
//     ever take s.mu). *protocol.InProcTransport satisfies this via its
//     documented evict-oldest, non-blocking send.
//   - SendDelta MUST NOT call back into Publish (directly, or
//     transitively through anything that itself calls Publish) on the
//     same goroutine. Go's sync.Mutex is not reentrant: a SendDelta
//     call that re-entered Publish would self-deadlock permanently on
//     publishMu, since nothing else can ever unlock a mutex for the
//     goroutine that already holds it. Calling back into
//     Subscribe/Unsubscribe/RegisterView from within SendDelta IS safe
//     (they take only s.mu, which the calling goroutine is not holding
//     at that point) — the prohibition is specifically and only against
//     re-entering Publish itself.
//
// Nothing in this interface's method signature enforces either rule —
// both are attacker-verified conventions
// (internal/engine/core/feat208_r2_destructive_test.go originally
// proved the pre-R3 hazard; feat208_destructive_test.go's R3-era
// regression tests now prove the fixed behaviour holds and that the one
// remaining prohibition is honestly documented, not silently assumed).
type DeltaSink interface {
	SendDelta(protocol.Delta) bool
}

// CommandJournaler is the minimal write surface accept() (below) needs to
// record an accepted command into the replay journal. Aaron's
// engine-owns-journal DD (2026-08-31, FEAT-1972079852 inc3, interview
// transcript on the BOW item): "commands over the protocol are journaled
// Go-side (harness.replay estate), the TS journal applies to mock/offline
// mode only." The edge engine.core -> harness.replay is registered in
// code.json (docs/planning/proposals/protocol-journal-edge-2026-08-31.md,
// landed 7b68d10) so this call is GR#25-legal.
//
// *replay.Recorder (internal/harness/replay/record.go) satisfies this
// interface exactly via its own ObserveCommand method (record.go:100) — no
// adapter needed. engine.core still defines its own minimal interface
// rather than importing harness/replay's concrete type directly, mirroring
// DeltaSink's decoupling shape above (the same "define the seam you need,
// let the concrete implementation satisfy it structurally" convention this
// package already uses for DeltaSink/GameplayCommandHandler/Speed8xGate).
//
// DURABILITY GAP (flagged for follow-up, not faked here): ASM-470 already
// noted harness.replay.Recorder buffers records in memory only and loses
// them on crash — wiring this seam does not change that. A Recorder wired
// via WithCommandJournaler/SetCommandJournaler records durably only as far
// as whatever later calls Recorder.Records()/Save persists it; this seam's
// job is only to ensure ObserveCommand is CALLED for every accepted
// command, not to make the Recorder itself crash-durable.
type CommandJournaler interface {
	ObserveCommand(cmd protocol.Command) error
}

// GameplayCommandHandler is the injected seam HandleCommand consults for
// the gameplay-intent commands (Buy, Zone, Build, Demolish — the build
// screen's vocabulary; SetFunding — F4's funding-slider vocabulary, added
// FEAT-208 increment 3 — internal/protocol/commands.go). engine.core
// neither owns nor imports the modules that adjudicate those commands
// (engine.build, engine.finance, engine.world); the composition root
// (internal/engine/compose) — the one package permitted to know all
// concrete modules (GR#20) — injects a handler that maps each command to
// its owning module's command surface. The seam mirrors Speed8xGate's
// "inject a func, deny by default" shape exactly.
//
// A nil return means the command was accepted; a non-nil error means it
// was rejected and is surfaced on the CommandResult (via e.reject, so the
// error's registry code reaches the caller). Unset (nil handler) means
// deny-by-default: the four gameplay kinds are rejected with
// ErrUnhandledCommandKind, never silently accepted.
type GameplayCommandHandler func(cmd protocol.Command) error

// WithGameplayCommandHandler installs the gameplay-command handler
// handleGameplay consults before accepting KindBuy/KindZone/KindBuild/
// KindDemolish. Unset (the default an Engine boots with — e.g. a bare
// NewEngine() in a test, or a caller that forgot to wire the composition
// root) means deny-by-default: those kinds are refused, never silently
// permitted. See GameplayCommandHandler's doc comment for why the handler
// lives at the composition root rather than here.
func WithGameplayCommandHandler(h GameplayCommandHandler) Option {
	return func(e *Engine) { e.gameplayHandler = h }
}

// SetGameplayCommandHandler installs the gameplay-command handler on an
// already-constructed Engine. It exists for the composition root, which
// receives a *core.Engine (not an Option list) and must route the four
// gameplay kinds to the build/world modules it composes — the handler is a
// closure over those modules, so it cannot be built at NewEngine time the
// way Speed8xGate (a plain method value) can. Same boot-time-only
// discipline as RegisterPhaseHook: must be called before the first
// AdvanceTicks (before the engine seals), rejected with ErrEngineSealed
// afterward, never silently ignored.
func (e *Engine) SetGameplayCommandHandler(h GameplayCommandHandler) error {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	if e.sealed {
		return errs.New(ErrEngineSealed, errs.NewCorrelationID(), map[string]any{"handler": "gameplay"})
	}
	e.gameplayHandler = h
	return nil
}

// SetCommandJournaler installs the journaling seam on an already-constructed
// Engine, mirroring SetGameplayCommandHandler exactly: the composition root
// receives a *core.Engine (not an Option list) and wires a real
// *replay.Recorder once it exists — which, like the gameplay handler,
// cannot be built at NewEngine time. Same boot-time-only discipline as
// SetGameplayCommandHandler/RegisterPhaseHook: must be called before the
// first AdvanceTicks (before the engine seals), rejected with
// ErrEngineSealed afterward, never silently ignored.
func (e *Engine) SetCommandJournaler(j CommandJournaler) error {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	if e.sealed {
		return errs.New(ErrEngineSealed, errs.NewCorrelationID(), map[string]any{"handler": "journaler"})
	}
	e.journaler = j
	return nil
}

// HandleCommand processes one Command synchronously and returns the
// CommandResult to send back to the caller. It never blocks on
// anything slower than an Engine method call or a phase-pipeline run
// (AdvanceTicks) — there is no I/O and no wall-clock wait anywhere in
// this file.
//
// BUG-472 r2 finding #1 (single source of truth, GR#3): this is now the
// ONE place that calls signalSubscriptionPump, for every return path —
// success, rejection, AND the persist-halt short-circuit below. r1 had
// each accepting handler call signalSubscriptionPump itself, which left
// handleGameplay/handleInspectEntity/handleDebug (3 of 9 dispatch
// targets) and the halt-check return silently un-signalled: once halted,
// EVERY later command is refused at the top-of-function check before any
// handler runs, so if that check itself never signals, NO command of any
// Kind can ever wake the pump again and an already-subscribed client is
// never told the sim paused. Moving the signal here, wrapping
// dispatchCommand unconditionally, closes that structurally — a future
// Kind added to dispatchCommand's switch cannot repeat the omission,
// because it does not need to call signalSubscriptionPump itself at all.
// The extra signal on paths that used to skip it (e.g. handleUnsubscribe,
// the copy-check short-circuit) is harmless: signalSubscriptionPump is a
// cheap, non-blocking, coalescing send (see its own doc comment) — waking
// the pump one extra time for a rejection nobody needed to observe costs
// one recompute, not correctness.
func (e *Engine) HandleCommand(cmd protocol.Command) protocol.CommandResult {
	// SEC-018/astgate: identity-checked directly here too, not only inside
	// dispatchCommand — a copy must never reach the unconditional
	// signalSubscriptionPump() call below (e.deltaSignal is a copied
	// channel HEADER aliasing the same underlying channel as the
	// original, so signalling from a copy would still wake the REAL
	// pump — harmless in effect, but this checks identity explicitly
	// rather than relying on that being merely benign).
	if err := e.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return protocol.CommandResult{
			CorrelationID: cmd.CorrelationID,
			Tick:          0,
			Accepted:      false,
			Error:         toErrorRef(err),
		}
	}
	result := e.dispatchCommand(cmd)
	e.signalSubscriptionPump()
	return result
}

// dispatchCommand is HandleCommand's actual decode/dispatch body, split
// out so HandleCommand itself can wrap every return path with exactly one
// signalSubscriptionPump call (see HandleCommand's doc comment above).
func (e *Engine) dispatchCommand(cmd protocol.Command) protocol.CommandResult {
	// SEC-018: identity-checked BEFORE anything else in this function,
	// including cmd.Validate()'s own rejection path — reject() calls
	// Clock() (now itself guarded, see engine.go), but building the
	// CommandResult directly here means a copied Engine never even
	// reaches Clock() for ANY command, valid or not. This is the single
	// choke point for every command-based entry into e.mu (handleSetSpeed/
	// handlePause/handleResume, each of which ALSO carries its own
	// pre-lock check below — defence in depth, not the only line, same as
	// SEC-016's RegisterPhaseHook/seal pattern).
	if err := e.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return protocol.CommandResult{
			CorrelationID: cmd.CorrelationID,
			Tick:          0,
			Accepted:      false,
			Error:         toErrorRef(err),
		}
	}
	// BUG-472 (Aaron ruling 2026-09-01, "HALT + SURFACE"): checked BEFORE
	// cmd.Validate() and BEFORE dispatch, so a persist-halted Engine
	// refuses EVERY command kind — including a malformed one that would
	// otherwise have been rejected with ErrInvalidEnvelope — with the halt
	// error instead. This preserves the STRUCTURAL gate property (one
	// check before the switch, covering every Kind including a future one
	// and the default branch) that r1's independent round verified: no
	// handler can ever be reached once halted, by construction, not by
	// convention. See persistHaltState's doc comment for why this is a
	// lock-free atomic.Pointer read (safe on every command, any goroutine)
	// and persistHaltResult's doc comment for why this branch never calls
	// errs.New/Wrap itself (that already happened exactly once, at the
	// moment the ORIGINAL append failed).
	if e.persistHalt.Load() != nil {
		return e.persistHaltResult(cmd)
	}
	if err := cmd.Validate(); err != nil {
		return e.reject(cmd, errs.New(ErrInvalidEnvelope, "", map[string]any{"cause": err.Error()}))
	}
	correlationID := string(cmd.CorrelationID)

	switch cmd.Kind {
	case protocol.KindAdvanceTicks:
		return e.handleAdvanceTicks(cmd, correlationID)
	case protocol.KindSetSpeed:
		return e.handleSetSpeed(cmd, correlationID)
	case protocol.KindPause:
		return e.handlePause(cmd)
	case protocol.KindResume:
		return e.handleResume(cmd)
	case protocol.KindSubscribe:
		return e.handleSubscribe(cmd, correlationID)
	case protocol.KindUnsubscribe:
		return e.handleUnsubscribe(cmd, correlationID)
	case protocol.KindInspectEntity:
		return e.handleInspectEntity(cmd)
	case protocol.KindDebug:
		return e.handleDebug(cmd)
	case protocol.KindBuy, protocol.KindZone, protocol.KindBuild, protocol.KindDemolish, protocol.KindSetFunding:
		return e.handleGameplay(cmd, correlationID)
	default:
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
}

// handleGameplay dispatches the gameplay-intent commands (Buy, Zone,
// Build, Demolish, SetFunding) to the injected GameplayCommandHandler. It
// is the deny-by-default counterpart to handleSetSpeed's
// checkSpeed8xAllowed: with no handler wired (the bare-NewEngine case)
// every gameplay kind is rejected with ErrUnhandledCommandKind rather
// than silently accepted,
// and with a handler wired, the handler's error (nil or registry-sourced)
// decides accept/reject. engine.core never adjudicates the gameplay
// itself — that is engine.build/engine.finance/engine.world's job, reached
// only through the composition root's handler (GR#20).
func (e *Engine) handleGameplay(cmd protocol.Command, correlationID string) protocol.CommandResult {
	if e.gameplayHandler == nil {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	if err := e.gameplayHandler(cmd); err != nil {
		return e.reject(cmd, err)
	}
	return e.accept(cmd)
}

// accept is the single choke point every accepted-command path in this
// file shares (handleGameplay, handleAdvanceTicks, handleSetSpeed,
// handlePause, handleResume, handleSubscribe, handleUnsubscribe,
// handleInspectEntity, handleDebug) — journalAccepted is called from here,
// not duplicated into each handler, so no handler can forget to journal
// (lead-default ruling #1, FEAT-1972079852 inc3: "journal inside accept(),
// not per-handler — simpler, ensures consistency"). BUG-472: a durable
// append failure observed here turns this into a REJECTED CommandResult
// instead (Aaron's "HALT + SURFACE" ruling) — see journalAccepted's doc
// comment for the full policy.
func (e *Engine) accept(cmd protocol.Command) protocol.CommandResult {
	if haltResult, halted := e.journalAccepted(cmd); halted {
		return haltResult
	}
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          e.clockTickForResult(),
		Accepted:      true,
	}
}

// persistHaltState is the ORIGINAL durable-persist failure's identity,
// latched exactly once by latchPersistHalt (below) into e.persistHalt and
// read by every subsequent HandleCommand call (the halt check in
// dispatchCommand) and by EngineStatusView (subscribe.go). Immutable after
// construction — every field is filled in BEFORE the CompareAndSwap that
// publishes it, so a Load() that observes a non-nil *persistHaltState
// never needs its own synchronization to read these fields safely.
//
// display is precomputed (captured from the ONE errs.New call
// latchPersistHalt makes, at the moment the ORIGINAL failure is first
// observed) rather than recomputed on every later read. This matters for
// two reasons: (1) errs.New/Wrap unconditionally LOG every call (GR#1/
// GR#7's own registry sink), so recomputing it per rejected command while
// halted — which could be arbitrarily many, e.g. a client hammering
// retries against a paused sim — would flood the log with duplicate
// entries describing the exact same fault; (2) EngineStatusView is read
// far more often than any real command is rejected (every subscription
// pump wake), so it must never construct a registry error itself. Both
// readers use these three plain fields instead. Mirrors BUG-480's
// dirtyLogged latch (persistjournal.go), which solves the identical
// "don't re-log an already-established, permanent condition on every
// subsequent cadence boundary" problem for the sibling dirty flag.
type persistHaltState struct {
	code          string // always ErrSimulationPersistHalted (MET-E023) — kept as data, not re-derived
	correlationID string // the ORIGINAL failed AppendJournal's correlation ID — NEVER the correlation ID of whichever later command happens to be rejected
	display       string // precomputed (*errs.E).Display() — "[MET-E023] ... (correlation: <original-id>)", the exact copy-paste-able string Aaron's Q100011 ruling requires
}

// journalAccepted records cmd into the replay journal via the configured
// CommandJournaler, if one is wired (WithCommandJournaler/
// SetCommandJournaler), applying BUG-472's "HALT + SURFACE" policy (Aaron
// ruling 2026-09-01) on a durable-append failure. Returns (zero-value,
// false) when cmd should be accepted normally (no journaler configured, or
// the append succeeded); returns (haltResult, true) when the append failed
// and the CALLER (accept()) must return haltResult to its own caller
// INSTEAD of building an Accepted result.
//
// # Policy history: this supersedes MET-E021's old swallow-and-continue
//
// Before BUG-472, a journal-write failure here was logged (MET-E021) and
// otherwise ignored: the command stayed Accepted and the tick kept
// running, on the theory that a lost journal FRAME (a replay-fidelity
// gap) is a lesser fault than aborting a live tick. The 2026-08-31 inc2
// destructive round proved that theory wrong for durable persistence
// specifically: a store that keeps failing SILENTLY diverges the live
// digest from anything a later restore can reconstruct, with no signal to
// the caller that it happened. Aaron's ruling: HALT the composition and
// SURFACE the fault the moment it is first observed, rather than letting
// the divergence compound silently.
//
// # Why the FAILING command itself is still "rejected", even though its
// own side effects already ran
//
// journalAccepted only runs from accept() (see accept()'s own doc comment
// above this type), which is only reached AFTER a command's real effects
// have already been applied — handleGameplay calls e.gameplayHandler(cmd)
// (which mutates build/finance/world state) BEFORE calling accept() at
// all; AdvanceTicks/SetSpeed/Pause/Resume/etc. have likewise already
// mutated e.clock/e.subs by the time their own handler reaches accept().
// There is no earlier point in this synchronous call at which "will this
// be accepted" is yet decided, so there is no way to journal-then-decide.
//
// Aaron's ruling explicitly chose REJECTED over inventing an
// accepted-but-not-durable wire slot (protocol.CommandResult.Validate
// already forbids Error alongside Accepted=true, and the ruling text says
// a new slot is "NOT wanted"). That means the wire response the client
// sees for THIS specific command can legitimately disagree with what the
// live engine just did internally — a known, documented, accepted
// consequence of a durable-persistence fault this severe: the whole point
// of halting immediately afterward is that no FURTHER command can build on
// top of that now only-locally-applied effect, and the only supported
// recovery (a fresh process restored via RestoreLatestSnapshotOrGenesis,
// BUG-480) replays only what the journal durably has, which by
// construction never includes the dropped command either.
//
// # Every later command while halted
//
// e.persistHalt is latched (CompareAndSwap, exactly one winner) BEFORE
// this function returns, so dispatchCommand's own halt check
// (commands.go) refuses every subsequent command before its handler ever
// runs — no further side effects are ever applied once halted. Reused via
// e.persistHaltResult (never a second errs.New call) for every one of
// those later rejections, keyed by the SAME persistHaltState this function
// established, so every rejection while halted — no matter how much
// later, or which later command triggered it — carries the ORIGINAL
// failed append's code and correlation ID (Aaron's Q100011 ruling), never
// a fresh one.
//
// nil e.journaler (the default for a bare NewEngine(), and for every
// Engine in this package's own tests that does not call
// WithCommandJournaler/SetCommandJournaler) is a documented no-op —
// mirrors WithPhaseObserver's optional-hook shape, not
// gameplayHandler/speed8xGate's deny-by-default shape: journaling absence
// is not a security gate, so there is nothing to deny or halt over.
func (e *Engine) journalAccepted(cmd protocol.Command) (haltResult protocol.CommandResult, halted bool) {
	if e.journaler == nil {
		return protocol.CommandResult{}, false
	}
	if err := e.journaler.ObserveCommand(cmd); err != nil {
		e.latchPersistHalt(cmd, err)
		return e.persistHaltResult(cmd), true
	}
	return protocol.CommandResult{}, false
}

// latchPersistHalt is called exactly at the moment a durable
// CommandJournaler.ObserveCommand append fails. It ALWAYS constructs the
// two registry errors describing this specific failure (MET-E021, kept
// for the log-level detail the original swallow policy already provided;
// MET-E023, the new halt code) — both are real, distinct, genuine
// failures worth their own log line even if several goroutines race this
// call concurrently for what turns out to be the SAME underlying store
// outage (each one truly did fail its own append). What is latched into
// e.persistHalt, however, is exactly ONE of those constructions: the
// first to win the CompareAndSwap(nil, ...) race becomes the process's
// permanent persistHaltState — every later reader (dispatchCommand's
// check, EngineStatusView) sees that SAME winner's code/correlationID/
// display, never whichever goroutine happened to call this function last.
//
// This is a genuine sync/atomic CompareAndSwap on e.persistHalt (an
// atomic.Pointer[persistHaltState], engine.go), not merely tested as if it
// were one — BUG-472 r1's round found this distinction under-verified
// (mutating the CAS to a plain Store did not redden the same-Kind
// concurrency test, only a MIXED-Kind race); see
// TestAttackBUG472_MixedKindsRacingTheLatch (attack_bug472_surface_test.go)
// for the regression that specifically catches a future CAS→Store
// regression.
//
// # Un-pause decision -- CONFIRMED permanent (Aaron 2026-09-01, Q100022)
//
// See ErrSimulationPersistHalted's doc comment (errors.go): permanent for
// the process lifetime, recovery only via a fresh process restore; the
// paired write-then-verify save requirement is FEAT-2326609714.
func (e *Engine) latchPersistHalt(cmd protocol.Command, appendErr error) {
	wrapped := errs.Wrap(ErrJournalWriteFailed, string(cmd.CorrelationID), appendErr, map[string]any{
		"kind": string(cmd.Kind),
	})
	haltE := errs.New(ErrSimulationPersistHalted, wrapped.CorrelationID, map[string]any{
		"originalCode": wrapped.Code,
		"kind":         string(cmd.Kind),
	})
	e.persistHalt.CompareAndSwap(nil, &persistHaltState{
		code:          haltE.Code,
		correlationID: haltE.CorrelationID,
		display:       haltE.Display(),
	})
}

// persistHaltResult builds the rejected CommandResult for cmd from the
// CURRENTLY latched e.persistHalt state. It deliberately never calls
// errs.New/Wrap itself (see persistHaltState's doc comment for why:
// avoiding a duplicate registry-log entry, and avoiding any registry
// lookup at all, for every command rejected while already halted) — every
// field it needs was already computed once, by latchPersistHalt, at the
// moment the ORIGINAL failure was first observed.
//
// Callers: dispatchCommand's halt check (this cmd is a LATER, unrelated
// command arriving after the process was already halted) and
// journalAccepted (this cmd IS the one whose own append just failed and
// triggered the halt). Both read e.persistHalt.Load() AFTER it is
// guaranteed non-nil — dispatchCommand checks Load()!=nil itself before
// calling this; journalAccepted calls latchPersistHalt (unconditionally
// CompareAndSwap-ing a non-nil value) immediately before this, so the
// Load() below can never observe nil in practice. Guarded anyway (GR#1:
// never assume a call that could return zero values cannot); the fallback
// degrades to reject()'s normal errs.New-backed path rather than
// panicking or returning a malformed nil-Error CommandResult, which
// protocol.CommandResult.Validate would reject as
// ErrRejectedResultMissingError.
func (e *Engine) persistHaltResult(cmd protocol.Command) protocol.CommandResult {
	state := e.persistHalt.Load()
	if state == nil {
		// Unreachable via either real call site (see doc comment above) —
		// defensive fallback only, so this can never construct a wire
		// CommandResult with Accepted=false and Error=nil. originalCode is
		// supplied as "unknown" (never omitted) so this still renders
		// without a literal {originalCode} token — the errs render-gate
		// scans every call site statically, independent of reachability.
		return e.reject(cmd, errs.New(ErrSimulationPersistHalted, string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind), "originalCode": "unknown"}))
	}
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          e.clockTickForResult(),
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: state.code, Display: state.display},
	}
}

// PersistHalted reports whether BUG-472's persist-halt has latched for
// this Engine and, if so, the registry code and ORIGINAL correlation ID of
// the durable-persist failure that caused it (Aaron's Q100011 ruling: the
// caller/client layer needs the ACTUAL code+correlation, not a generic
// message). ok is false, with both strings empty, for an Engine that has
// never halted — the default for a bare NewEngine() and for every Engine
// until its first ObserveCommand failure, if any.
//
// This is the direct, synchronous counterpart to EngineStatusView's
// PersistHalted/PersistHaltError fields (subscribe.go) — that pair
// reaches a subscribed client proactively, over the existing delta-push
// path, with no further command required; this method is for a caller
// (metroserve's tick driver, a test, a health check) that already holds
// *Engine and wants a synchronous, allocation-free check without going
// through the subscription machinery at all. Both read the SAME
// underlying e.persistHalt — no second source of truth (GR#3).
func (e *Engine) PersistHalted() (code, correlationID string, ok bool) {
	// SEC-018/astgate: guarded directly like Clock()/HookCount() — this is
	// a real exported entry point external callers (metroserve's tick
	// driver) hold a bare *Engine and call directly, not merely reachable
	// through an already-guarded internal path.
	if err := e.checkNotCopied("", nil); err != nil {
		return "", "", false
	}
	state := e.persistHalt.Load()
	if state == nil {
		return "", "", false
	}
	return state.code, state.correlationID, true
}

func (e *Engine) reject(cmd protocol.Command, err error) protocol.CommandResult {
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          e.clockTickForResult(),
		Accepted:      false,
		Error:         toErrorRef(err),
	}
}

// clockTickForResult returns the current tick for a CommandResult, or 0
// if Clock() reports e is a struct-copied Engine (SEC-018). In practice
// this error branch is unreachable via HandleCommand's own dispatch:
// HandleCommand's entry-point identity check and every individual
// handler's own pre-lock check already reject a copy before accept()/
// reject() is ever called here. Handled anyway because Clock() itself
// is guarded and returns an error rather than hanging — consistent with
// never treating any single check as the only line of defence (SEC-016).
func (e *Engine) clockTickForResult() protocol.Tick {
	c, err := e.Clock()
	if err != nil {
		return 0
	}
	return protocol.Tick(c.Tick())
}

// toErrorRef converts any error into a protocol.ErrorRef. Every error
// this package's own methods return is always constructed via
// errs.New/errs.Wrap (a *errs.E), so the type assertion below normally
// succeeds; the fallback branch is defensive-only (GR#1: never panic on
// an unexpected type) and itself degrades loudly through the registry
// error path rather than silently.
func toErrorRef(err error) *protocol.ErrorRef {
	if e, ok := err.(*errs.E); ok {
		return &protocol.ErrorRef{Code: e.Code, Display: e.Display()}
	}
	wrapped := errs.Wrap(ErrUnhandledCommandKind, "", err, map[string]any{"kind": "unknown"})
	return &protocol.ErrorRef{Code: wrapped.Code, Display: wrapped.Display()}
}

func (e *Engine) handleAdvanceTicks(cmd protocol.Command, correlationID string) protocol.CommandResult {
	payload, ok := cmd.Payload.(protocol.AdvanceTicksPayload)
	if !ok {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	if err := e.AdvanceTicks(correlationID, payload.N); err != nil {
		return e.reject(cmd, err)
	}
	// BUG-472 r2 finding #1: signalSubscriptionPump is now called exactly
	// once, by HandleCommand itself, after every dispatchCommand return —
	// see HandleCommand's doc comment. Handlers no longer call it here.
	return e.accept(cmd)
}

func (e *Engine) handleSetSpeed(cmd protocol.Command, correlationID string) protocol.CommandResult {
	payload, ok := cmd.Payload.(protocol.SetSpeedPayload)
	if !ok {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	speed := Speed(payload.Speed)
	if !ValidSpeed(speed) {
		return e.reject(cmd, errs.New(ErrInvalidSpeed, correlationID, map[string]any{"speed": payload.Speed}))
	}
	if speed == Speed8xDebug {
		if err := e.checkSpeed8xAllowed(correlationID); err != nil {
			return e.reject(cmd, err)
		}
	}
	// SEC-018: identity-checked BEFORE e.mu — one of eight e.mu.Lock()
	// sites in this package's non-test files. Also caught by
	// HandleCommand's own entry-point check (defence in depth, not the
	// only line), but guarded here directly too so this method is safe
	// even if ever called by a future non-HandleCommand path.
	if err := e.checkNotCopied(correlationID, map[string]any{"kind": string(cmd.Kind)}); err != nil {
		return e.reject(cmd, err)
	}
	e.mu.Lock()
	e.clock.setSpeed(speed)
	e.mu.Unlock()
	// BUG-472 r2 finding #1: see handleAdvanceTicks's identical note above.
	return e.accept(cmd)
}

// checkSpeed8xAllowed is BUG-009's enforcement point: Speed8xDebug is
// reserved for feat.debugmode (clock.go's Speed8xDebug doc comment) and
// must never be reachable with debug off. engine.core does not import
// feat.debugmode to check this (see Speed8xGate's doc comment,
// engine.go) — it calls whatever gate a caller injected via
// WithSpeed8xGate, or refuses by default if none was.
//
// The default-deny branch returns ErrSpeed8xGateNotConfigured
// (MET-E015), a dedicated registry code distinct from ErrInvalidSpeed
// (MET-E002): Speed8xDebug's VALUE is valid (it is a documented
// multiplier once a debug gate has accepted it) — the failure here is
// that no gate was wired to authorise it at all, a genuinely different
// triage case from an out-of-range speed value. BUG-011 (closing
// ASM-012): this used to reuse MET-E002 with an unregistered
// "reason: no_gate_configured" context field as a deliberate stopgap
// while BUG-008 was concurrently rewriting the whole error registry;
// that justification expired once BUG-008 landed and data/errors.json
// stabilised. See ASM-* in the BUG-009 dispatch report for the original
// reasoning this superseded.
func (e *Engine) checkSpeed8xAllowed(correlationID string) error {
	if e.speed8xGate == nil {
		return errs.New(ErrSpeed8xGateNotConfigured, correlationID, map[string]any{
			"speed": int(Speed8xDebug),
		})
	}
	return e.speed8xGate(correlationID)
}

// handlePause pauses the clock. Idempotent (per PausePayload's doc
// comment): pausing an already-paused world is a no-op Accept.
func (e *Engine) handlePause(cmd protocol.Command) protocol.CommandResult {
	// SEC-018: identity-checked BEFORE e.mu — one of eight e.mu.Lock()
	// sites in this package's non-test files. Also caught by
	// HandleCommand's own entry-point check (defence in depth, not the
	// only line), but guarded here directly too so this method is safe
	// even if ever called by a future non-HandleCommand path.
	if err := e.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return e.reject(cmd, err)
	}
	e.mu.Lock()
	e.clock.setPaused(true)
	e.mu.Unlock()
	// BUG-472 r2 finding #1: see handleAdvanceTicks's identical note above.
	return e.accept(cmd)
}

// handleResume resumes at the previously set speed. Idempotent.
func (e *Engine) handleResume(cmd protocol.Command) protocol.CommandResult {
	// SEC-018: see handlePause's identical note above.
	if err := e.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return e.reject(cmd, err)
	}
	e.mu.Lock()
	e.clock.setPaused(false)
	e.mu.Unlock()
	// BUG-472 r2 finding #1: see handleAdvanceTicks's identical note above.
	return e.accept(cmd)
}

func (e *Engine) handleSubscribe(cmd protocol.Command, correlationID string) protocol.CommandResult {
	payload, ok := cmd.Payload.(protocol.SubscribePayload)
	if !ok {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	if _, err := e.subs.Subscribe(payload.ViewName, payload.Params, cmd.CorrelationID, correlationID); err != nil {
		return e.reject(cmd, err)
	}
	// The subscription's first delta is pushed asynchronously off this
	// call path — see subscribe.go's SubscriptionServer.publishInitial
	// and AC-7's "not inline in phase execution" requirement, which this
	// generalises to "not inline in command handling" either.
	// BUG-472 r2 finding #1: see handleAdvanceTicks's identical note above.
	return e.accept(cmd)
}

func (e *Engine) handleUnsubscribe(cmd protocol.Command, correlationID string) protocol.CommandResult {
	payload, ok := cmd.Payload.(protocol.UnsubscribePayload)
	if !ok {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	if err := e.subs.Unsubscribe(payload.SubscriptionID, correlationID); err != nil {
		return e.reject(cmd, err)
	}
	return e.accept(cmd)
}

// handleInspectEntity returns a placeholder accepted result.
// TODO(engine.world / engine.citizens, InspectEntity): resolve
// EntityRef to a real detail payload once an inspectable entity model
// exists; out of scope for engine.core (see the acceptance doc's "Out
// of scope" — engine.core only orchestrates, it doesn't own entities).
func (e *Engine) handleInspectEntity(cmd protocol.Command) protocol.CommandResult {
	return e.accept(cmd)
}

// handleDebug returns a placeholder accepted result.
// TODO(feat.debugmode, Debug op dispatch): route DebugPayload.Op to
// real F12 panel operations (module toggle, force-snapshot, ...) once
// feat.debugmode lands; out of scope for engine.core today.
func (e *Engine) handleDebug(cmd protocol.Command) protocol.CommandResult {
	return e.accept(cmd)
}

// signalSubscriptionPump wakes the subscription pump goroutine (if one
// is running via StartSubscriptionPump) to recompute and push the
// current engine.status view. The send is non-blocking and coalescing
// (a size-1 buffered channel, dropped if already full) — multiple
// commands processed in quick succession collapse into a single
// recompute, and the compute/push itself always happens on the pump's
// own goroutine, never here (AC-7).
func (e *Engine) signalSubscriptionPump() {
	select {
	case e.deltaSignal <- struct{}{}:
	default:
	}
}

// StartSubscriptionPump starts the goroutine that computes and pushes
// engine.status (and, once compose.Wire has registered more views via
// RegisterView, every other registered view's) deltas (subscribe.go) in
// response to signals from signalSubscriptionPump, off the command/tick
// path (AC-7). It returns immediately; the returned done channel is
// closed exactly once, when the goroutine actually exits (ctx.Done()
// observed) — callers join on it at shutdown (F2, independent round r1:
// previously nothing tracked or waited for this goroutine at all; see
// cmd/metropolis/boot.go's shutdown() and internal/harness/headless's
// Run() shutdown closure, both of which now select on it before closing
// their transport, mirroring skeletonWiring.engineDone's identical
// close-then-select join idiom for RunCommandLoop's own goroutine).
//
// Mechanically restricted to at most once per Engine (F1a, independent
// round r1, FEAT-208 increment 1): previously this was a documented-only
// contract ("safe to call at most once per Engine") with no enforcement
// — a second call started a SECOND, concurrently-running pump goroutine,
// both reading the same e.deltaSignal and both able to call
// SubscriptionServer.Publish concurrently, which is exactly the
// precondition the independent round's attack test exploited to
// reproduce out-of-order delivery (see subscribe.go's Publish doc
// comment). A second call now returns ErrSubscriptionPumpAlreadyStarted
// and starts no goroutine at all — never a silent second pump. NewEngine
// does not start one automatically, since not every caller (e.g. a
// headless harness driving AdvanceTicks with no live UI) needs one.
//
// BUG-019: identity-checked BEFORE the started flag is even touched —
// one of this package's e.mu.Lock()-adjacent entry points missed by
// SEC-018's enumeration (that pass covered every direct e.mu.Lock() call
// site; this one only touches e.mu transitively via EngineStatusView(),
// which already guards itself). Without this check, a struct-copied
// Engine (e2 := *e) calling e2.StartSubscriptionPump would start a live
// goroutine reading e2.deltaSignal — a copied channel HEADER aliasing
// the same underlying channel as the original — and call
// e2.subs.Publish. Because e2.subs is the SAME POINTER as e.subs (not
// itself a copy), Publish's own checkNotCopied guard would never fire,
// so the failure mode is not a crash or a hang but silently WRONG DATA:
// published engine.status deltas built from e2.EngineStatusView()'s
// degrade-to-zero path (a copy's Clock() is itself guarded and returns a
// zeroed Clock). Rejecting the copy here, before the goroutine is ever
// started, closes that off at the same entry point SEC-016/SEC-018
// established for every other guarded method on this type. Note this
// also means a copy's e2.pumpStarted CompareAndSwap is never reached —
// e2.pumpStarted is itself a copy of e's own atomic.Bool value, so even
// if this check were skipped, a copy attempting to "start the pump
// twice" would not collide with the original's own started-flag; the
// identity check is rejected first regardless, for the data-corruption
// reason above, not because of pumpStarted's copy semantics.
func (e *Engine) StartSubscriptionPump(ctx context.Context, sink DeltaSink) (done <-chan struct{}, err error) {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil, err
	}
	if !e.pumpStarted.CompareAndSwap(false, true) {
		return nil, errs.New(ErrSubscriptionPumpAlreadyStarted, errs.NewCorrelationID(), nil)
	}
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.deltaSignal:
				e.subs.Publish(sink, e.pumpTick())
			}
		}
	}()
	return doneCh, nil
}

// pumpTick reads the engine's current Tick for the subscription pump's
// Publish call (commands.go's StartSubscriptionPump) — replaces the old
// PublishEngineStatus(sink, e.EngineStatusView()) call, which derived
// Tick from the "engine.status" view's own Tick field specifically; now
// that Publish serves N registered views uniformly, the pump reads Tick
// directly off the engine's own clock once per cycle (§4 of the design:
// "Publish reads Tick once per cycle ... never a per-delta wall-clock or
// independently-advancing counter"). Degrades to Tick(0) on the same
// unreachable-in-practice copied-Engine path EngineStatusView's own
// Clock() failure branch already documents — never the wall clock (GR#21).
func (e *Engine) pumpTick() protocol.Tick {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	// Direct, explicit guard against e.subs too (SEC-019) — this method
	// is called from the same pump-goroutine closure that calls
	// e.subs.Publish, so astgate's syntactic scan reaches it through
	// that field chain; not because pumpTick itself touches e.subs.
	// Mirrors engineStatusViewPatch's identical defensive pattern
	// (subscribe.go).
	if err := e.subs.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	c, err := e.Clock()
	if err != nil {
		return 0
	}
	return protocol.Tick(c.Tick())
}

// CommandSource is the minimal pull surface RunCommandLoop needs from a
// transport's engine-facing side (satisfied by
// *protocol.InProcTransport's Commands()+SendResult today; a future
// gRPC-backed Transport implementation would too).
type CommandSource interface {
	Commands() <-chan protocol.Command
	SendResult(protocol.CommandResult) bool
}

// RunCommandLoop consumes commands from t, one at a time, calling
// HandleCommand for each and pushing the resulting CommandResult back
// via t.SendResult — the command loop deliverable (M0-ENG §1.1's
// T-ENGINE). Blocks the calling goroutine, so callers run it as `go
// engine.RunCommandLoop(ctx, transport)` and observe the returned error
// once the goroutine exits (AC-7, engine.headless.md).
//
// # Exit contract (harness.headless AC-4/AC-5/AC-6 — read this before
// wiring a caller; mirrors StubEngine.Run's "Exit contract" doc comment,
// internal/engine/stub/engine.go)
//
// ctx cancellation and t.Commands() closing are NOT two equivalent,
// interchangeable ways to stop this loop. Only ctx cancellation is the
// clean shutdown signal: RunCommandLoop returns nil. A Commands()
// closure observed WITHOUT ctx already being done is reported as a
// distinct, registry-sourced error (ErrPrematureCommandsClose,
// MET-E014) — it means the transport went away for some reason other
// than the shutdown the caller told this loop about, and for a headless
// run (no operator watching a screen — engine.headless.md's whole
// premise) that must never be allowed to look like a clean exit, the
// same way BUG-020 (harness.stub) and MET-H004
// (harness.replay.EnginePlayer) were fixed for their own callers. This
// is the third instance of that shape, now fixed in engine.core itself.
//
// The caller-side contract this implies: cancel ctx, WAIT for this
// goroutine to actually return (join it), and only THEN close the
// transport — see StubEngine.Run's doc comment for the full "cancel();
// Close()" without a join is NOT safe" argument, which applies here
// unchanged. internal/engine/detgate's RunGate is the one documented
// exception: it already controls cancel()/Close() ordering
// deterministically with no other goroutine able to close Commands()
// first, so it does not need to observe this return value (see
// gate.go's runOnce and engine.headless.md's AC-7 "Out of scope" note).
//
// AC-6: this implementation is the plain two-branch select mirroring
// StubEngine.Run directly (re-checking ctx.Done() AFTER observing
// ok==false on the Commands() receive, never before) — not a
// wait/notify loop — so it needs no additional "re-check at the alarm"
// pattern beyond that single re-check; see AC-6's text for why a
// wait/notify shape would need one and this shape does not.
func (e *Engine) RunCommandLoop(ctx context.Context, t CommandSource) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case cmd, ok := <-t.Commands():
			if !ok {
				// AC-4/AC-5: re-check ctx.Done() here, AFTER observing
				// ok==false, is what tells the two triggers apart — see the
				// "Exit contract" section above. If ctx is already done,
				// this is the clean shutdown path (nil); if it is not,
				// something closed the transport out from under a caller
				// that never told this loop to stop, and that is reported,
				// never silently swallowed.
				select {
				case <-ctx.Done():
					return nil
				default:
					return errs.New(ErrPrematureCommandsClose, errs.NewCorrelationID(), nil)
				}
			}
			t.SendResult(e.HandleCommand(cmd))
		}
	}
}
