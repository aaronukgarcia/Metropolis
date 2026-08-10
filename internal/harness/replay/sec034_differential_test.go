package replay

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SEC-034 (Bill, 2026-08-10) — the SEC-032 regression test could not
// fail against the bug it guards, so it proved nothing. This file is the
// replacement: a DIFFERENTIAL test that carries its own "before" and
// "after" wait loops and asserts they behave differently.
//
// Why differential rather than "revert and re-run": internal/harness is
// entirely untracked (git ls-files internal/harness/ is empty), so there
// is no committed pre-fix revision to check out. Tester-1's attempt to
// revert therefore ran against a hand-reconstruction and measured 0/500
// false alarms on supposedly-buggy code — the baseline-selection trap in
// its most acute form: not the wrong reference, but no reference at all.
// Keeping the "before" loop HERE, in the test, removes the dependency on
// git history entirely and keeps the comparison honest for good.
//
// What was actually wrong with the old test: it called runtime.Gosched()
// specifically so Replay's goroutine would reach its select and PARK
// before the final SendResult, and its comment claimed this "maximises
// how often this run actually exercises the race". It does the exact
// opposite. A send on the buffered notify channel with a receiver
// already parked hands off directly and wakes that receiver on the
// notify branch there and then — cancel() runs afterwards and ctx.Done()
// is not yet ready when the select resolves. The biasing mechanism
// structurally CLOSED the window it was written to open, which is why
// the test passed against pre-fix code.
//
// The real window is narrow and specific: the waiter must be BETWEEN its
// top-of-loop completion check and its select when the final result and
// the cancellation both become ready. Only then does the select see two
// ready cases and pick uniformly.
//
// That gap is a handful of instructions wide, which is why every attempt
// to hit it by racing has failed — the old test's, and this file's own
// first draft, both measured 0/500 against the pre-fix loop. It is not
// reachable by luck and no amount of extra iterations changes that. So
// this file does not race for it: waitLoop takes a hook at exactly that
// point and the driver blocks the waiter there until the final result
// and the cancellation have BOTH landed, then releases it into a select
// whose two cases are guaranteed ready. Construct the state, do not race
// for the timing.
//
// The test asserts both halves — that the pre-fix loop misreports and
// the fixed one does not — so it fails if the fix regresses AND fails if
// the driver ever goes inert. That second half is the property the
// original SEC-032 test was missing, and the whole reason it could pass
// while proving nothing.

// feedCommands replicates the send-and-close half of Replay that runs
// before the wait loop. The copies below MUST do this: cmdCh is filled
// by Replay itself, not by the constructor, so a wait loop that skips it
// leaves the driver's `<-p.Commands()` blocked forever. (Found the hard
// way — the first draft of this file omitted it and hung.)
func feedCommands(p *EnginePlayer) {
	for _, cmd := range p.commands {
		p.cmdCh <- cmd
	}
	close(p.cmdCh)
}

// waitLoop is Replay's wait loop, reproduced here in both its pre-fix
// and post-fix forms so the two can be driven through identical states
// and compared. recheck selects which: false is the loop as it stood
// BEFORE SEC-032 (the ctx.Done() branch reports a premature close
// without re-checking whether the work had in fact finished), true is
// the fix under test.
//
// beforeSelect is the hook that makes this deterministic. It runs at the
// one point that matters — after the top-of-loop completion check has
// read got, before the select evaluates its cases — which is precisely
// the gap the defect lives in and precisely the gap that is unreachable
// by luck. Two earlier drivers (the old test's, and this file's first
// attempt) both measured 0/500 against the pre-fix loop for exactly that
// reason: the gap is a handful of instructions wide, so the driver
// always won the race and the waiter's next check simply found the work
// complete. Blocking here instead of hoping to hit the gap is the same
// principle the process doc already states for concurrency tests —
// construct the attack STATE, do not race for the timing.
//
// The hook is passed to BOTH forms identically, so the comparison stays
// apples-to-apples: the fixed loop has to report zero false alarms with
// the window forced fully open, not merely with it closed.
func waitLoop(p *EnginePlayer, ctx context.Context, recheck bool, beforeSelect func(got int)) error {
	feedCommands(p)
	want := len(p.commands)
	for {
		p.mu.Lock()
		got := len(p.results)
		p.mu.Unlock()
		if got >= want {
			return nil
		}
		if beforeSelect != nil {
			beforeSelect(got)
		}
		select {
		case <-p.notify:
		case <-ctx.Done():
			if recheck {
				p.mu.Lock()
				done := len(p.results)
				p.mu.Unlock()
				if done >= want {
					return nil
				}
			}
			return errs.New(codeReplayTargetClosedEarly, errs.NewCorrelationID(), map[string]any{
				"sent": want, "answered": got,
			})
		}
	}
}

// runFinalResultVsCancelWindow drives one iteration of the exact shape
// SEC-032 describes — every command answered, in order, with the last
// result and the cancellation both landing before the waiter evaluates
// its select. Because every result is sent before cancel(), guaranteed
// by program order in this single goroutine, the replay has genuinely
// completed: ANY error returned is by definition a false alarm and never
// a real premature close.
//
// The sequence is deterministic, not hopeful:
//
//	waiter parks in select on its first pass    (got=0, want=2)
//	SendResult #1 hands off, waking it          (got=1)
//	waiter loops, reaches the hook, BLOCKS on gate
//	SendResult #2 completes the results         (got=2, notify token queued)
//	cancel() makes ctx.Done() ready
//	close(gate) releases the waiter INTO the select
//
// At that last step both cases are ready, so Go picks uniformly between
// them — which is the whole defect. The pre-fix loop must therefore
// misreport roughly half the time, and the fixed loop never.
//
// Two commands, not one: with a single command the waiter parks on its
// first check and the only SendResult hands off directly, so the loop is
// never re-entered and the window never exists. That is the inert shape
// the original SEC-032 test had.
//
// The fixture is built ONCE per test and reused: it is immutable after
// construction, and rebuilding it costs a temp dir plus a Save/Load
// round-trip that at 500 iterations dominates everything else. Only the
// EnginePlayer must be fresh each iteration.
func runFinalResultVsCancelWindow(t *testing.T, fx Fixture, recheck bool) bool {
	t.Helper()

	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}
	recorded, err := fx.Results()
	if err != nil || len(recorded) != 2 {
		t.Fatalf("fx.Results: %v (len=%d)", err, len(recorded))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := len(recorded)
	reached := make(chan struct{}) // waiter -> driver: "parked at the hook"
	gate := make(chan struct{})    // driver -> waiter: "state is staged, go"
	errCh := make(chan error, 1)
	go func() {
		errCh <- waitLoop(p, ctx, recheck, func(got int) {
			// Only the final pass matters; earlier ones have nothing to
			// synchronise.
			if got == want-1 {
				close(reached)
				<-gate
			}
		})
	}()

	first := <-p.Commands()
	second := <-p.Commands()

	p.SendResult(protocol.CommandResult{CorrelationID: first.CorrelationID, Accepted: recorded[0].Accepted, Tick: recorded[0].Tick})

	// Wait for the waiter to actually BE at the hook before staging the
	// rest. Without this handshake the driver races ahead, the final
	// result lands while the waiter is still between passes, and the
	// waiter's next top-of-loop check finds the work already complete and
	// returns without ever reaching the select — the window closes and
	// the driver goes inert. That is not a hypothetical: it is what the
	// first gated draft of this file did, and it measured 0/500 against
	// the pre-fix loop, exactly like the ungated attempts before it.
	<-reached

	p.SendResult(protocol.CommandResult{CorrelationID: second.CorrelationID, Accepted: recorded[1].Accepted, Tick: recorded[1].Tick})
	cancel()
	// Both select cases are now ready. Release the waiter into them.
	close(gate)

	return <-errCh != nil
}

// TestSEC034_PreFixLoopStillReportsFalseAlarms is the half the old test
// was missing: proof that this driver can actually detect the defect. If
// this ever stops finding false alarms in the pre-fix loop, the driver
// has gone inert (a scheduler or runtime change closing the window, say)
// and the companion test below is no longer evidence of anything — which
// is exactly the failure mode SEC-034 was raised for.
func TestSEC034_PreFixLoopStillReportsFalseAlarms(t *testing.T) {
	const iterations = 500
	fx := fixtureWithCommands(t, 2)
	falseAlarms := 0
	for i := 0; i < iterations; i++ {
		if runFinalResultVsCancelWindow(t, fx, false) {
			falseAlarms++
		}
	}
	t.Logf("pre-fix loop: %d/%d false premature-close reports", falseAlarms, iterations)
	if falseAlarms == 0 {
		t.Fatalf("driver is inert: the pre-fix loop produced 0/%d false alarms, so a pass from the fixed loop proves nothing (SEC-034)", iterations)
	}
}

// TestSEC034_FixedLoopReportsNoFalseAlarms is the regression proper,
// meaningful only because the test above proves the driver bites.
func TestSEC034_FixedLoopReportsNoFalseAlarms(t *testing.T) {
	const iterations = 500
	fx := fixtureWithCommands(t, 2)
	falseAlarms := 0
	for i := 0; i < iterations; i++ {
		if runFinalResultVsCancelWindow(t, fx, true) {
			falseAlarms++
		}
	}
	if falseAlarms != 0 {
		t.Fatalf("SEC-032 regression: %d/%d false premature-close alarms on a replay that always completes (want 0)", falseAlarms, iterations)
	}
}

// TestSEC034_RealReplayLoopMatchesTheFixedCopy guards the one weakness
// a differential test of this shape has: waitLoop above is a COPY of
// Replay's wait loop, and a copy can drift away from the original until
// it is testing something the production code no longer does.
//
// The copy cannot be gated from outside without a hook in production
// code, and adding a test-only hook to a shipping method to make a test
// pass is a worse trade than this check. So instead of re-running the
// window against Replay — which, being ungated, is exactly as inert as
// the two drivers that already measured 0/500 and would prove nothing —
// this asserts the structural property the differential depends on: that
// Replay's ctx.Done() branch still re-checks the completion condition
// before raising the alarm. Read from source, so it cannot silently
// diverge from what actually ships.
//
// SEC-041 (Tester-1, 2026-08-10): this used to be a strings.Index
// SUBSTRING-ORDER check ("len(p.results)" appears before
// "codeReplayTargetClosedEarly" somewhere in the branch's text), which
// a mutation that deletes the gating `if` while leaving `len(p.results)`
// textually present (e.g. as a now-dead assignment) still satisfies —
// Tester-1 built exactly that probe and it passed the old check. Fixed
// by parsing player_engine.go with go/ast (sec041_ast_guard_test.go)
// and requiring an actual `if` statement, linked by variable identity
// to a len(...results...) assignment, whose body unconditionally exits,
// positioned before the raise — not just two substrings in the right
// order. TestSEC041_ASTGateCatchesGateRemovedMutant proves the
// replacement actually fails against that exact mutation; this test is
// the "and it still accepts the real thing" half.
func TestSEC034_RealReplayLoopMatchesTheFixedCopy(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "player_engine.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse player_engine.go: %v", err)
	}
	fn := findMethodDecl(file, "EnginePlayer", "Replay")
	if fn == nil {
		t.Fatalf("could not find func (*EnginePlayer) Replay — the loop this file mirrors has been restructured, so the differential above no longer describes production code")
	}
	if err := checkReplayCtxDoneGuard(fn); err != nil {
		t.Fatalf("%v", err)
	}
}

// TestSEC034_GenuinePrematureCloseStillReported is the check that
// matters more than the false-alarm count: a "fix" that simply stopped
// raising MET-H004 would score a perfect zero on every test above. Here
// the replay genuinely does NOT complete — one command of two is
// answered before cancellation — and the alarm must still fire.
func TestSEC034_GenuinePrematureCloseStillReported(t *testing.T) {
	fx := fixtureWithCommands(t, 2)
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}
	recorded, err := fx.Results()
	if err != nil {
		t.Fatalf("fx.Results: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Replay(ctx)
		errCh <- err
	}()

	first := <-p.Commands()
	<-p.Commands()
	p.SendResult(protocol.CommandResult{CorrelationID: first.CorrelationID, Accepted: recorded[0].Accepted, Tick: recorded[0].Tick})
	// Second command deliberately never answered — this IS a premature
	// close and must be reported as one.
	cancel()

	got := <-errCh
	if !errors.Is(got, &errs.E{Code: codeReplayTargetClosedEarly}) {
		t.Fatalf("genuine premature close: err = %v, want codeReplayTargetClosedEarly (MET-H004)", got)
	}
}
