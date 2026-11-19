package main

// BUG-1239: the tick driver's start-up signal. The driver sends its boot
// Resume asynchronously; Ready must close only once the engine has APPLIED it
// (the clock reads running), exactly once, and also when the Resume cannot be
// sent or the driver stops first, so a waiter never hangs.

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestBUG1239_DriverReady_FiresOnlyAfterTheResumeIsApplied(t *testing.T) {
	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1000000))
	var held protocol.Command
	var sent atomic.Int64
	var reads atomic.Int64
	d := newTickDriver(
		func() (enginecore.Clock, error) { reads.Add(1); return e.Clock() },
		// The "transport": records the Resume but does NOT hand it to the
		// engine, like a command still queued behind other work.
		func(cmd protocol.Command) error {
			if cmd.Kind == protocol.KindResume {
				held = cmd
				sent.Add(1)
			}
			return nil
		},
		1000000,
	)
	d.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// The driver has sent the Resume and has polled the (still paused) clock
	// several times: ready must not have fired. Counted, not timed.
	pollUntil(t, "the driver to poll the paused clock", func() bool { return sent.Load() == 1 && reads.Load() >= 5 })
	if isClosed(d.Ready()) {
		t.Fatal("Ready closed before the engine applied the Resume (the send is not the application)")
	}

	// The engine applies the Resume: now, and only now, Ready closes.
	if res := e.HandleCommand(held); !res.Accepted {
		t.Fatalf("Resume rejected: %+v", res.Error)
	}
	select {
	case <-d.Ready():
	case <-time.After(helperDeadline):
		t.Fatal("Ready never closed after the Resume was applied")
	}

	// Closed exactly once: a second markReady and the Run exit must not panic.
	d.markReady()
	cancel()
	<-done
	if !isClosed(d.Ready()) {
		t.Fatal("Ready reopened")
	}
}

// The interleaving the clock alone cannot see: the Resume is applied and the
// player pauses again before the driver's next read, tick still 0. The clock
// then reads paused at tick 0 for ever, exactly like a Resume never applied;
// only the engine's CommandResult for the Resume proves it was applied.
func TestBUG1239_DriverReady_FiresWhenResumeAppliedThenPausedBeforeAnyRead(t *testing.T) {
	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1000000))
	var reads atomic.Int64
	var d *tickDriver
	d = newTickDriver(
		func() (enginecore.Clock, error) { reads.Add(1); return e.Clock() },
		func(cmd protocol.Command) error {
			if cmd.Kind != protocol.KindResume {
				return nil
			}
			if res := e.HandleCommand(cmd); !res.Accepted {
				t.Errorf("Resume rejected: %+v", res.Error)
			}
			pause := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(),
				Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
			if res := e.HandleCommand(pause); !res.Accepted {
				t.Errorf("Pause rejected: %+v", res.Error)
			}
			// What the router does with the engine's result for the Resume.
			go d.ApplyResult(protocol.CommandResult{CorrelationID: cmd.CorrelationID, Accepted: true})
			return nil
		},
		1000000,
	)
	d.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	defer func() { cancel(); <-done }()

	select {
	case <-d.Ready():
	case <-time.After(helperDeadline):
		t.Fatal("Ready never closed although the Resume was applied (then paused before any clock read)")
	}
	// The driver is still running: Ready came from the result, not from Run exiting.
	select {
	case <-done:
		t.Fatal("Run returned; Ready fired from the exit path, not from the applied Resume")
	default:
	}
	if c, _ := e.Clock(); !c.Paused() || c.Tick() != 0 {
		t.Fatalf("test premise broken: clock paused=%v tick=%d", c.Paused(), c.Tick())
	}
}

// A result for some other command must not fire Ready.
func TestBUG1239_DriverReady_IgnoresResultsForOtherCommands(t *testing.T) {
	d := newTickDriver(func() (enginecore.Clock, error) { return enginecore.Clock{}, nil },
		func(protocol.Command) error { return nil }, 1000000)
	d.ApplyResult(protocol.CommandResult{CorrelationID: protocol.NewCorrelationID(), Accepted: true})
	if isClosed(d.Ready()) {
		t.Fatal("Ready closed by a result for an unrelated command")
	}
	d.ApplyResult(protocol.CommandResult{CorrelationID: d.ResumeCorrelationID(), Accepted: true})
	if !isClosed(d.Ready()) {
		t.Fatal("Ready not closed by the Resume's own result")
	}
}

func TestBUG1239_DriverReady_FiresWhenTheResumeCannotBeSent(t *testing.T) {
	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1000000))
	d := newTickDriver(e.Clock, func(protocol.Command) error { return errors.New("queue full") }, 1000000)
	d.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-d.Ready():
	case <-time.After(helperDeadline):
		t.Fatal("Ready never closed although the Resume failed to send")
	}
}

func TestBUG1239_DriverReady_FiresWhenRunStopsFirst(t *testing.T) {
	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1000000))
	d := newTickDriver(e.Clock, func(protocol.Command) error { return nil }, 1000000)
	d.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	cancel()
	<-done
	if !isClosed(d.Ready()) {
		t.Fatal("Ready still open after Run returned: a waiter would hang")
	}
}

// The helpers every frame test uses: boot to a confirmed paused clock, resume
// and pause again, each time with the clock AND the status view agreeing.
func TestBUG1239_Helpers_PauseResumeAreConfirmedOnClockAndView(t *testing.T) {
	w := bootPausedReady(t, "bug1239-helpers")
	for i := 0; i < 3; i++ {
		resumeAndConfirm(t, w)
		if c, _ := w.engine.Clock(); c.Paused() {
			t.Fatal("clock still paused after resumeAndConfirm")
		}
		if v, _ := w.statusBar.Snapshot(); v.Paused {
			t.Fatal("status view still paused after resumeAndConfirm")
		}
		pauseAndConfirm(t, w)
		if c, _ := w.engine.Clock(); !c.Paused() {
			t.Fatal("clock not paused after pauseAndConfirm")
		}
		if v, _ := w.statusBar.Snapshot(); !v.Paused {
			t.Fatal("status view not paused after pauseAndConfirm")
		}
	}
	advanceAndSettle(t, w, 30)
	if got := w.viewTicks.settledTick(); got < int64(w.engine.TicksCompleted()) {
		t.Fatalf("settledTick %d behind engine tick %d after advanceAndSettle", got, w.engine.TicksCompleted())
	}
}

// bootReady must not return until the status VIEW shows the boot Resume, not
// merely the clock. The seam holds the RUNNING status delta; bootReady has to
// stay blocked on it. The 500 ms look is a deliberate negative window: the
// engine's Resume is applied before the held delta is even published, so a
// bootReady that skipped the view wait returns within one driver poll (50 ms).
func TestBUG1239_BootReady_WaitsForTheStatusViewNotJustTheClock(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	viewApplyGate = func(view string, d protocol.Delta) {
		if view != "engine.status" {
			return
		}
		var v enginecore.EngineStatusView
		if json.Unmarshal(d.Patch, &v) != nil || v.Paused {
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	var relOnce atomic.Bool
	free := func() {
		if relOnce.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(func() { free(); viewApplyGate = nil })
	// The boot goroutine registers its own shutdown cleanup AFTER the one above,
	// so shutdown would run first and wait on the held router. A defer runs
	// before every cleanup, on every exit (t.Fatal included).
	defer free()

	// bootReady runs on its own goroutine, so it must report failure as a value:
	// t.Fatalf from a non-test goroutine does not stop the test. bootReadyErr
	// returns the error, and this goroutine's result is read below.
	type bootOutcome struct {
		w   *skeletonWiring
		err error
	}
	outcome := make(chan bootOutcome, 1)
	go func() {
		w, err := bootReadyErr(t, "bug1239-gate")
		outcome <- bootOutcome{w, err}
	}()

	select {
	case <-entered:
	case <-time.After(helperDeadline):
		t.Fatal("the running status delta never reached the view")
	}
	select {
	case <-outcome:
		free() // let the held router goroutine go, or shutdown would wait on it
		t.Fatal("bootReady returned while the status view still showed PAUSED")
	case <-time.After(500 * time.Millisecond):
	}
	free()
	select {
	case o := <-outcome:
		if o.err != nil {
			t.Fatalf("bootReady failed: %v", o.err)
		}
		if v, ok := o.w.statusBar.Snapshot(); !ok || v.Paused {
			t.Fatalf("bootReady returned but the view is not RUNNING: %+v ok=%v", v, ok)
		}
	case <-time.After(helperDeadline):
		t.Fatal("bootReady never returned after the status delta was released")
	}
}

// A refused boot Resume leaves the world paused: ApplyResult must say so (one
// line with the engine's registry error), still release the waiters, and an
// accepted Resume must record nothing.
func TestBUG1239_RefusedBootResume_IsRecorded(t *testing.T) {
	d := newTickDriver(func() (enginecore.Clock, error) { return enginecore.Clock{}, nil },
		func(protocol.Command) error { return nil }, 1000000)

	d.ApplyResult(protocol.CommandResult{CorrelationID: d.ResumeCorrelationID(), Accepted: true})
	if code, display, refused := d.BootResumeRefusal(); refused {
		t.Fatalf("an accepted Resume recorded a refusal: %q %q", code, display)
	}
	d2 := newTickDriver(func() (enginecore.Clock, error) { return enginecore.Clock{}, nil },
		func(protocol.Command) error { return nil }, 1000000)
	d2.ApplyResult(protocol.CommandResult{
		CorrelationID: d2.ResumeCorrelationID(),
		Error:         &protocol.ErrorRef{Code: "MET-X000", Display: "refused for the test"},
	})
	code, display, refused := d2.BootResumeRefusal()
	if !refused || code != "MET-X000" || display != "refused for the test" {
		t.Fatalf("a refused Resume must record the registry error, got %q %q refused=%v", code, display, refused)
	}
	if !isClosed(d2.Ready()) {
		t.Fatal("a refused Resume must still release Ready")
	}
}
