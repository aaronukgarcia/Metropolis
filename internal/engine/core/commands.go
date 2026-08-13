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
type DeltaSink interface {
	SendDelta(protocol.Delta) bool
}

// HandleCommand processes one Command synchronously and returns the
// CommandResult to send back to the caller. It never blocks on
// anything slower than an Engine method call or a phase-pipeline run
// (AdvanceTicks) — there is no I/O and no wall-clock wait anywhere in
// this file.
func (e *Engine) HandleCommand(cmd protocol.Command) protocol.CommandResult {
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
	default:
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
}

func (e *Engine) accept(cmd protocol.Command) protocol.CommandResult {
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          e.clockTickForResult(),
		Accepted:      true,
	}
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
	wrapped := errs.Wrap(ErrUnhandledCommandKind, "", err, map[string]any{"cause": err.Error()})
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
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
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
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
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
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
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
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
}

func (e *Engine) handleSubscribe(cmd protocol.Command, correlationID string) protocol.CommandResult {
	payload, ok := cmd.Payload.(protocol.SubscribePayload)
	if !ok {
		return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, map[string]any{"kind": string(cmd.Kind)}))
	}
	if _, err := e.subs.Subscribe(payload.ViewName, payload.Params, cmd.CorrelationID, correlationID); err != nil {
		return e.reject(cmd, err)
	}
	result := e.accept(cmd)
	// The subscription's first delta is pushed asynchronously off this
	// call path — see subscribe.go's SubscriptionServer.publishInitial
	// and AC-7's "not inline in phase execution" requirement, which
	// this generalises to "not inline in command handling" either.
	e.signalSubscriptionPump()
	return result
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
// engine.status deltas (subscribe.go) in response to signals from
// signalSubscriptionPump, off the command/tick path (AC-7). It returns
// immediately; the goroutine runs until ctx is done. Safe to call at
// most once per Engine (a second call would start a second, redundant
// pump) — NewEngine does not start one automatically, since not every
// caller (e.g. a headless harness driving AdvanceTicks with no live
// UI) needs one.
func (e *Engine) StartSubscriptionPump(ctx context.Context, sink DeltaSink) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.deltaSignal:
				e.subs.PublishEngineStatus(sink, e.EngineStatusView())
			}
		}
	}()
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
