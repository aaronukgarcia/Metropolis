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
		Tick:          protocol.Tick(e.Clock().Tick()),
		Accepted:      true,
	}
}

func (e *Engine) reject(cmd protocol.Command, err error) protocol.CommandResult {
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          protocol.Tick(e.Clock().Tick()),
		Accepted:      false,
		Error:         toErrorRef(err),
	}
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
	e.mu.Lock()
	e.clock.setSpeed(speed)
	e.mu.Unlock()
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
}

// handlePause pauses the clock. Idempotent (per PausePayload's doc
// comment): pausing an already-paused world is a no-op Accept.
func (e *Engine) handlePause(cmd protocol.Command) protocol.CommandResult {
	e.mu.Lock()
	e.clock.setPaused(true)
	e.mu.Unlock()
	result := e.accept(cmd)
	e.signalSubscriptionPump()
	return result
}

// handleResume resumes at the previously set speed. Idempotent.
func (e *Engine) handleResume(cmd protocol.Command) protocol.CommandResult {
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
// T-ENGINE). It returns when ctx is done or t's Commands channel is
// closed (protocol.InProcTransport.Close was called). Blocks the
// calling goroutine, so callers run it as `go
// engine.RunCommandLoop(ctx, transport)`.
func (e *Engine) RunCommandLoop(ctx context.Context, t CommandSource) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-t.Commands():
			if !ok {
				return
			}
			t.SendResult(e.HandleCommand(cmd))
		}
	}
}
