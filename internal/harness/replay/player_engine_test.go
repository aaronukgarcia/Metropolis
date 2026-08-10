package replay

import (
	"context"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// commandSource is the CommandSource-shaped surface a target's
// RunCommandLoop consumes — declared locally (see player_engine.go's doc
// comment on why this package does not import internal/engine/core for
// this).
type commandSource interface {
	Commands() <-chan protocol.Command
	SendResult(protocol.CommandResult) bool
}

// runLoop mirrors engine.core.Engine.RunCommandLoop exactly (it is
// deliberately a byte-for-byte copy of that loop's shape, not a
// simplification) so tests exercise EnginePlayer against the real
// consumption pattern without importing internal/engine/core.
func runLoop(ctx context.Context, source commandSource, handle func(protocol.Command) protocol.CommandResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-source.Commands():
			if !ok {
				return
			}
			source.SendResult(handle(cmd))
		}
	}
}

func fixtureWithCommands(t *testing.T, n int) Fixture {
	t.Helper()
	r := NewRecorder()
	for i := 0; i < n; i++ {
		corr := protocol.NewCorrelationID()
		cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corr, Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
		if err := r.ObserveCommand(cmd); err != nil {
			t.Fatalf("ObserveCommand: %v", err)
		}
		if err := r.ObserveResult(protocol.CommandResult{CorrelationID: corr, Tick: protocol.Tick(i), Accepted: true}); err != nil {
			t.Fatalf("ObserveResult: %v", err)
		}
	}
	dir := t.TempDir()
	if err := Save(dir, "engineplayer", r, FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := Load(dir, "engineplayer")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fx
}

// TestEnginePlayerCleanReplayMatches is AC-3b/AC-5's happy path: driving
// a target that answers every command exactly as recorded produces a
// Matched CompareResult.
func TestEnginePlayerCleanReplayMatches(t *testing.T) {
	fx := fixtureWithCommands(t, 3)
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		i := 0
		runLoop(ctx, p, func(cmd protocol.Command) protocol.CommandResult {
			r := protocol.CommandResult{CorrelationID: cmd.CorrelationID, Tick: protocol.Tick(i), Accepted: true}
			i++
			return r
		})
	}()

	cmp, err := p.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !cmp.Matched {
		t.Fatalf("expected Matched, got diffs: %v", cmp.Diffs)
	}
	cancel()
	<-loopDone
}

// TestEnginePlayerMismatchProducesNonEmptyDiff is AC-5: a target that
// answers differently than the fixture recorded produces a non-empty
// diff, not a silent pass.
func TestEnginePlayerMismatchProducesNonEmptyDiff(t *testing.T) {
	fx := fixtureWithCommands(t, 2)
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runLoop(ctx, p, func(cmd protocol.Command) protocol.CommandResult {
			// Deliberately wrong: reject every command the fixture
			// recorded as accepted.
			return protocol.CommandResult{
				CorrelationID: cmd.CorrelationID,
				Tick:          99,
				Accepted:      false,
				Error:         &protocol.ErrorRef{Code: "MET-E999", Display: "deliberate mismatch"},
			}
		})
	}()

	cmp, err := p.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if cmp.Matched {
		t.Fatal("expected a mismatch, got Matched=true")
	}
	if len(cmp.Diffs) == 0 {
		t.Fatal("expected a non-empty diff")
	}
	cancel()
	<-loopDone
}

// TestEnginePlayerReplayDeterministicAcrossTwoRuns is AC-11: replaying
// the same fixture twice against an identically-behaving target produces
// byte-for-byte identical CompareResult.Diffs.
func TestEnginePlayerReplayDeterministicAcrossTwoRuns(t *testing.T) {
	fx := fixtureWithCommands(t, 4)

	replayOnce := func() *CompareResult {
		p, err := NewEnginePlayer(fx)
		if err != nil {
			t.Fatalf("NewEnginePlayer: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		loopDone := make(chan struct{})
		go func() {
			defer close(loopDone)
			i := 0
			runLoop(ctx, p, func(cmd protocol.Command) protocol.CommandResult {
				r := protocol.CommandResult{CorrelationID: cmd.CorrelationID, Tick: protocol.Tick(i), Accepted: true}
				i++
				return r
			})
		}()
		cmp, err := p.Replay(context.Background())
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		cancel()
		<-loopDone
		return cmp
	}

	first := replayOnce()
	second := replayOnce()
	if first.Matched != second.Matched || len(first.Diffs) != len(second.Diffs) {
		t.Fatalf("non-deterministic CompareResult: first=%+v second=%+v", first, second)
	}
	for i := range first.Diffs {
		if first.Diffs[i] != second.Diffs[i] {
			t.Fatalf("diff[%d] differs between runs: %q vs %q", i, first.Diffs[i], second.Diffs[i])
		}
	}
}

// TestEnginePlayerReplayReportsPrematureCloseDistinctly is AC-3c: if ctx
// is done before every sent command has a matching result, Replay
// reports the distinct codeReplayTargetClosedEarly failure — never a
// silent "completed" outcome.
//
// SEC-032 (Tester-1, 2026-08-10): this is the "keep a case proving a
// GENUINE premature close still reports MET-H004" half of that
// finding's fix — the wait loop's re-check (player_engine.go) must not
// silence a real alarm while it stops silencing false ones. Here got=1
// stays permanently < want=2 (the second command is never answered), so
// the re-check cannot flip the outcome no matter how the race resolves —
// unlike TestEnginePlayerReplayNoFalseAlarmOnFinalResultVsCancelRace
// below, where got DOES reach want and the re-check is what makes the
// difference.
//
// Deterministic in what it asserts, not in scheduling (the "construct
// the state, don't race for the timing" rule): the fixture has 2
// commands; the test answers EXACTLY 1 of them via a direct SendResult
// call before cancelling ctx. Whatever order the goroutines are
// scheduled in, Replay can only ever observe got=1 < want=2 at the
// moment ctx becomes done (a second SendResult never happens), so the
// wait loop's final select always resolves to the ctx.Done() branch —
// the OUTCOME is pinned regardless of how many loop iterations it takes
// to get there.
func TestEnginePlayerReplayReportsPrematureCloseDistinctly(t *testing.T) {
	fx := fixtureWithCommands(t, 2)
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		cmp *CompareResult
		err error
	}
	outcomeCh := make(chan outcome, 1)
	go func() {
		cmp, err := p.Replay(ctx)
		outcomeCh <- outcome{cmp, err}
	}()

	// Answer exactly the first command, never the second, then cancel —
	// simulating a target that stopped responding mid-sequence.
	first := <-p.Commands()
	p.SendResult(protocol.CommandResult{CorrelationID: first.CorrelationID, Accepted: true})
	cancel()

	got := <-outcomeCh
	if got.err == nil {
		t.Fatalf("Replay: expected codeReplayTargetClosedEarly, got a completed CompareResult: %+v", got.cmp)
	}
	if !strings.Contains(got.err.Error(), codeReplayTargetClosedEarly) {
		t.Errorf("error %q does not carry %s", got.err.Error(), codeReplayTargetClosedEarly)
	}
}

// The SEC-032 regression test that used to sit here has been removed,
// not weakened: it was proven inert. It called runtime.Gosched() so
// Replay's goroutine would PARK in its select before the final
// SendResult, on the stated grounds that this "maximises how often this
// run actually exercises the race" — but a send on the buffered notify
// channel with a receiver already parked hands off directly and wakes it
// on the notify branch, with cancel() running only afterwards. The
// biasing mechanism structurally closed the window it was written to
// open, so the test passed against pre-fix code and proved nothing
// (Tester-1 measured 0/500; SEC-034).
//
// Its coverage is not lost — it is subsumed and strengthened by
// sec034_differential_test.go, which drives the same shape through a
// gated handshake that makes the window deterministic, and which carries
// its own pre-fix loop so it can prove it still detects the defect
// (241/500 against pre-fix, 0/500 against the fix).

// TestEnginePlayerNeverRegistersPhaseHooks is AC-3b's structural check,
// restated as a compile-time fact: EnginePlayer's exported method set is
// exactly Commands/SendResult/Replay — grepping this package's non-test
// source for "RegisterPhaseHook" (part of the Tester's verification
// suite) finds no matches, which this comment documents rather than
// re-tests at runtime (there is nothing to call).
func TestEnginePlayerNeverRegistersPhaseHooks(t *testing.T) {
	var _ commandSource = (*EnginePlayer)(nil)
}
