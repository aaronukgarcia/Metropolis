package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func mustCorrID() protocol.CorrelationID { return protocol.NewCorrelationID() }

// wantPlaceholderCode asserts that ref names this package's placeholder
// code, either directly (once a maintainer registers it in
// data/errors.json) or via the current MET-F003 "unregistered code"
// fallback (errors.go's doc comment; GR#7's degrade-loudly path) whose
// Display embeds the originally requested code. This keeps these tests
// correct both today (codes unregistered) and after registration
// (see /new-error) with no test changes required either way.
func wantPlaceholderCode(t *testing.T, ref *protocol.ErrorRef, code string) {
	t.Helper()
	if ref == nil {
		t.Fatalf("ErrorRef is nil, want code %s (or MET-F003 fallback naming it)", code)
	}
	if ref.Code == code {
		return
	}
	if ref.Code == "MET-F003" && strings.Contains(ref.Display, code) {
		return
	}
	t.Errorf("ErrorRef = %+v, want code %s (or MET-F003 fallback naming it)", ref, code)
}

func TestHandleCommand_AdvanceTicks(t *testing.T) {
	e := NewEngine()
	corrID := mustCorrID()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 3},
	}
	result := e.HandleCommand(cmd)
	if !result.Accepted {
		t.Fatalf("AdvanceTicks(3): rejected, error = %+v", result.Error)
	}
	if result.CorrelationID != corrID {
		t.Errorf("CorrelationID = %q, want %q", result.CorrelationID, corrID)
	}
	if clockOrFatal(t, e).Tick() != 3 {
		t.Errorf("Tick() = %d, want 3", clockOrFatal(t, e).Tick())
	}
}

func TestHandleCommand_AdvanceTicks_InvalidN(t *testing.T) {
	e := NewEngine()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: -5},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("AdvanceTicks(-5): accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidAdvanceTicks)
}

func TestHandleCommand_SetSpeed(t *testing.T) {
	e := NewEngine()
	valid := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 4},
	}
	result := e.HandleCommand(valid)
	if !result.Accepted {
		t.Fatalf("SetSpeed(4): rejected, error = %+v", result.Error)
	}
	if clockOrFatal(t, e).Speed() != Speed4x {
		t.Errorf("Speed() = %d, want %d", clockOrFatal(t, e).Speed(), Speed4x)
	}

	invalid := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 3},
	}
	result = e.HandleCommand(invalid)
	if result.Accepted {
		t.Fatal("SetSpeed(3): accepted, want rejected (3 is not a documented multiplier)")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidSpeed)
}

func TestHandleCommand_PauseResume_Idempotent(t *testing.T) {
	e := NewEngine()
	pause := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	for i := 0; i < 2; i++ {
		result := e.HandleCommand(pause)
		if !result.Accepted {
			t.Fatalf("Pause call %d: rejected, error = %+v", i, result.Error)
		}
	}
	if !clockOrFatal(t, e).Paused() {
		t.Error("Paused() = false after Pause commands, want true")
	}

	resume := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindResume, Payload: protocol.ResumePayload{}}
	for i := 0; i < 2; i++ {
		result := e.HandleCommand(resume)
		if !result.Accepted {
			t.Fatalf("Resume call %d: rejected, error = %+v", i, result.Error)
		}
	}
	if clockOrFatal(t, e).Paused() {
		t.Error("Paused() = true after Resume commands, want false")
	}
}

func TestHandleCommand_InspectEntity_Debug_PlaceholderAccepted(t *testing.T) {
	e := NewEngine()
	inspect := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindInspectEntity,
		Payload:         protocol.InspectEntityPayload{EntityRef: "citizen:1"},
	}
	if result := e.HandleCommand(inspect); !result.Accepted {
		t.Errorf("InspectEntity: rejected, error = %+v", result.Error)
	}

	debug := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: "noop"},
	}
	if result := e.HandleCommand(debug); !result.Accepted {
		t.Errorf("Debug: rejected, error = %+v", result.Error)
	}
}

func TestHandleCommand_InvalidEnvelope(t *testing.T) {
	e := NewEngine()
	// Missing CorrelationID fails protocol.Command.Validate.
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("HandleCommand with empty CorrelationID: accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidEnvelope)
}

// TestRunCommandLoop_RoundTripOverInProcTransport drives the command
// loop over a real protocol.InProcTransport end to end: send an
// AdvanceTicks command from the "UI side", read back the CommandResult
// from the "engine side", and confirm the correlation ID echoes.
func TestRunCommandLoop_RoundTripOverInProcTransport(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()

	e := NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() { loopDone <- e.RunCommandLoop(ctx, transport) }()

	corrID := mustCorrID()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 7},
	}
	if err := transport.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	select {
	case result := <-transport.Results():
		if !result.Accepted {
			t.Fatalf("result rejected: %+v", result.Error)
		}
		if result.CorrelationID != corrID {
			t.Errorf("CorrelationID = %q, want %q (echo)", result.CorrelationID, corrID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CommandResult")
	}

	if clockOrFatal(t, e).Tick() != 7 {
		t.Errorf("Tick() = %d, want 7", clockOrFatal(t, e).Tick())
	}

	// Clean shutdown: cancel, then join before Close (RunCommandLoop's
	// "Exit contract" doc comment) -- proves the round-trip path itself
	// also exits with a nil error on an ordinary ctx-cancelled stop.
	cancel()
	select {
	case err := <-loopDone:
		if err != nil {
			t.Errorf("RunCommandLoop returned %v on a clean ctx-cancelled shutdown, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunCommandLoop to return after cancel")
	}
}

// --- RunCommandLoop's exit contract (harness.headless AC-4/AC-5/AC-6,
// engine.headless.md; MOD-015 SEC-036) ---

// fakeCommandSource is a minimal CommandSource (commands.go) a test can
// close out from under RunCommandLoop without needing a real
// protocol.InProcTransport's Close() semantics (which close FOUR
// channels atomically and are guarded against struct-copy misuse in
// ways irrelevant to this exit-contract test) -- just the one channel
// RunCommandLoop actually ranges over.
type fakeCommandSource struct {
	ch chan protocol.Command

	mu      sync.Mutex
	results []protocol.CommandResult
}

func newFakeCommandSource(buf int) *fakeCommandSource {
	return &fakeCommandSource{ch: make(chan protocol.Command, buf)}
}

func (f *fakeCommandSource) Commands() <-chan protocol.Command { return f.ch }

func (f *fakeCommandSource) SendResult(r protocol.CommandResult) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, r)
	return true
}

// TestRunCommandLoop_CleanCtxCancel_ReturnsNil is the baseline clean-exit
// case (AC-4): ctx cancelled, the CommandSource's channel never closed at
// all. Demonstrated to fail against the pre-fix code: before this fix
// RunCommandLoop had no return value, so this test (which asserts on
// err) could not even compile against it -- the signature change IS the
// fix AC-4 requires (a caller-observable distinction did not exist
// before).
func TestRunCommandLoop_CleanCtxCancel_ReturnsNil(t *testing.T) {
	e := NewEngine()
	src := newFakeCommandSource(0)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- e.RunCommandLoop(ctx, src) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunCommandLoop = %v, want nil on a clean ctx-cancelled shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunCommandLoop to return")
	}
}

// TestRunCommandLoop_PrematureCommandsClose_ReturnsDistinctError is
// AC-4's load-bearing case: the CommandSource's Commands() channel closes
// WITHOUT ctx ever being cancelled -- the transport died out from under a
// caller that never told this loop to stop. Deterministic, not
// timing-based: ctx is a fresh context.Background() that is NEVER
// cancelled anywhere in this test, so there is no race to win -- the
// only way RunCommandLoop's select can proceed at all is via the
// Commands() branch observing ok==false, and the fix's whole point is
// that THAT branch, with ctx still live, must report an error rather
// than returning nil like the clean path does.
//
// Proof this can fail against the unfixed code: temporarily reverting
// the inner `select { case <-ctx.Done(): ... default: ... }` re-check in
// commands.go back to an unconditional `return nil` on ok==false (the
// exact shape BUG-020/SEC-026 already named as the defect this
// generalizes) makes this test fail, asserting a nil error where a
// distinct one is required -- verified by hand during this dispatch
// (see the dispatch report) rather than left as a permanent second code
// path in this file.
func TestRunCommandLoop_PrematureCommandsClose_ReturnsDistinctError(t *testing.T) {
	e := NewEngine()
	src := newFakeCommandSource(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // never called before the close below; kept only for cleanup

	done := make(chan error, 1)
	go func() { done <- e.RunCommandLoop(ctx, src) }()

	close(src.ch)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunCommandLoop = nil, want a distinct premature-close error")
		}
		var e2 *errs.E
		if !errsAs(err, &e2) {
			t.Fatalf("RunCommandLoop error = %v (%T), want a *errs.E", err, err)
		}
		wantPlaceholderCode(t, &protocol.ErrorRef{Code: e2.Code, Display: e2.Display()}, ErrPrematureCommandsClose)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunCommandLoop to return")
	}
}

// errsAs is a one-line errors.As wrapper local to this file purely to
// avoid importing "errors" for a single call site.
func errsAs(err error, target **errs.E) bool {
	e, ok := err.(*errs.E)
	if !ok {
		return false
	}
	*target = e
	return true
}

// TestRunCommandLoop_CtxDoneBeforeClose_TakesCleanPath is AC-5/AC-6's
// race case, constructed deterministically rather than raced for timing
// (dev-team-process.md's "construct the state, don't race for the
// timing" rule): cancel() is called, then close(src.ch) is called,
// STRICTLY AFTER cancel() returns -- both from this single test
// goroutine, in sequence, so ctx.Done() is guaranteed already closed by
// the time src.ch is closed. By the time RunCommandLoop's select next
// evaluates readiness, BOTH cases are ready simultaneously, and Go's
// select picks uniformly among ready cases -- it may take EITHER branch.
// The fix must make both outcomes converge on the same answer (nil, the
// clean path) regardless of which branch is chosen; this test asserts
// that convergence deterministically (the setup guarantees the race
// exists on every run, not just probabilistically), not the specific
// branch taken.
func TestRunCommandLoop_CtxDoneBeforeClose_TakesCleanPath(t *testing.T) {
	e := NewEngine()
	src := newFakeCommandSource(0)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- e.RunCommandLoop(ctx, src) }()

	cancel()
	close(src.ch)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunCommandLoop = %v, want nil (clean shutdown must win the ctx-done/close race)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunCommandLoop to return")
	}
}

func TestToErrorRef_RegistryError_PreservesCode(t *testing.T) {
	e := errs.New(ErrUnhandledCommandKind, errs.NewCorrelationID(), map[string]any{"kind": "test"})
	ref := toErrorRef(e)
	if ref == nil {
		t.Fatal("toErrorRef(*errs.E) returned nil")
	}
	if ref.Code != ErrUnhandledCommandKind {
		t.Errorf("toErrorRef(*errs.E).Code = %q, want %q", ref.Code, ErrUnhandledCommandKind)
	}
}

func TestToErrorRef_NonRegistryError_LabelsUnexpected(t *testing.T) {
	plain := errors.New("plain gameplay-handler error")
	ref := toErrorRef(plain)
	if ref == nil {
		t.Fatal("toErrorRef(non-*errs.E) returned nil")
	}
	// BUG-310: a non-*errs.E is an internal invariant break and must be
	// labelled the unexpected-error, NEVER the "unhandled command kind" the
	// pre-fix code wrongly attached to every defensive fallback.
	if ref.Code == ErrUnhandledCommandKind {
		t.Fatalf("toErrorRef(non-*errs.E).Code = %q; the BUG-310 mislabel regression is back", ref.Code)
	}
	wantPlaceholderCode(t, ref, ErrUnexpectedError)
}
