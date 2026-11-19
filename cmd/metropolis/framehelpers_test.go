package main

// BUG-1239: the ONE set of helpers every frame test of the real terminal game
// uses to boot it, to put the clock in a known state, to wait for the views to
// catch up, and to read a frame without racing the render loop. Tests used to
// GUESS when asynchronous start-up and publishing had finished (a Space pressed
// before the tick driver's boot Resume was applied, a frame read before the
// views had caught up with a month close, a screen read while the render loop
// drew it) and failed on slow or loaded machines. Every wait here is on a real
// signal or on the exact condition the test needs, bounded by a deadline.
//
//   - start-up: skeletonWiring.driverReady closes when the engine has APPLIED
//     the driver's boot Resume (tickdriver.go).
//   - settled: skeletonWiring.viewTicks.settledTick() is the lowest tick that
//     every bound view has applied (viewticks.go); the engine publishes every
//     view each cycle stamped with one tick, so >= T means every view shows the
//     engine at tick T or later.
//   - frames: composeDraw into a fresh buffer on the test goroutine, or, for
//     tests of the real render loop, lockedSim, whose Show and row reads share
//     one lock so a read never overlaps the draw (tcell's GetContents returns
//     the live front buffer, not a copy).

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// helperDeadline bounds every wait: a FAILURE bound ("it never happened"),
// never a performance claim.
//
// It is a variable only so the regression test of the barrier can give itself
// a short bound (restored with t.Cleanup); the game never touches it.
var helperDeadline = 90 * time.Second

// pollUntilErr is pollUntil without a test: it returns an error naming the
// condition, so a non-test goroutine can report the failure to the test
// goroutine instead of calling t.Fatalf itself.
func pollUntilErr(what string, cond func() bool) error {
	return pollUntilWithin(helperDeadline, what, cond)
}

// pollUntilWithin is pollUntilErr with an explicit time budget, for a caller
// that has already spent part of one overall deadline.
func pollUntilWithin(budget time.Duration, what string, cond func() bool) error {
	stop := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(stop) {
			return fmt.Errorf("timed out after %v waiting for: %s", budget, what)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// waitDriverReady blocks until the tick driver's boot Resume has been applied
// (w.driverReady), bounded by helperDeadline. A wiring without a tick driver
// has nothing to wait for.
func waitDriverReady(t *testing.T, w *skeletonWiring) {
	t.Helper()
	if w.driverReady == nil {
		return
	}
	select {
	case <-w.driverReady:
	case <-time.After(helperDeadline):
		t.Fatalf("the tick driver's boot Resume was never applied within %v", helperDeadline)
	}
}

// pollUntil is the ONE place the frame tests sleep: a short poll interval
// inside a deadline-bounded wait for a condition. what names the condition in
// the failure.
func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if err := pollUntilErr(what, cond); err != nil {
		t.Fatal(err)
	}
}

// bootReady boots the real game at the slow test pace (a month is a million
// seconds, so the running clock never produces a tick by itself during a
// test) and returns only once the engine has applied the tick driver's boot
// Resume. The clock then reads running; use pauseAndConfirm to stop it.
func bootReady(t *testing.T, slug string) *skeletonWiring {
	t.Helper()
	w, err := bootReadyErr(t, slug)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// bootReadyErr is bootReady reporting failure as an error rather than through
// t.Fatalf, so it may run on a goroutine other than the test's (t is used only
// for Cleanup, which is safe from any goroutine).
func bootReadyErr(t *testing.T, slug string) (*skeletonWiring, error) {
	t.Helper()
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return 1000000, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })
	w, err := bootCore(slug, registry.NewRegistry())
	if err != nil {
		return nil, fmt.Errorf("bootCore: %w", err)
	}
	t.Cleanup(w.shutdown)
	select {
	case <-w.driverReady:
	case <-time.After(helperDeadline):
		return nil, fmt.Errorf("the tick driver's boot Resume was never applied within %v", helperDeadline)
	}
	// The status view starts out showing the clock's initial PAUSED state and
	// catches up with the Resume late (measured: 700 ms under -race). Wait for
	// it to show RUNNING so the view and the clock agree before anything is
	// confirmed: otherwise a later "view shows paused" would be satisfied by
	// that stale initial state. Deltas arrive in order, so once the view has
	// shown RUNNING, a later PAUSED can only come from a later pause.
	if err := pollUntilErr("the status view to show the boot Resume", func() bool {
		v, ok := w.statusBar.Snapshot()
		return ok && !v.Paused
	}); err != nil {
		return nil, err
	}
	return w, nil
}

// bootPausedReady is bootReady followed by pauseAndConfirm.
func bootPausedReady(t *testing.T, slug string) *skeletonWiring {
	t.Helper()
	w := bootReady(t, slug)
	pauseAndConfirm(t, w)
	return w
}

// setPausedAndConfirm presses Space only if the clock is not already in the
// wanted state, then waits for the clock AND the published status view to
// show it, and for every view to have caught up with the engine's tick.
func setPausedAndConfirm(t *testing.T, w *skeletonWiring, wantPaused bool) {
	t.Helper()
	c, err := w.engine.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if c.Paused() != wantPaused {
		pressKey(w, ' ')
	}
	pollUntil(t, "the clock to read the wanted pause state", func() bool {
		c, err := w.engine.Clock()
		return err == nil && c.Paused() == wantPaused
	})
	pollUntil(t, "the status view to show the wanted pause state", func() bool {
		v, ok := w.statusBar.Snapshot()
		return ok && v.Paused == wantPaused
	})
	settleViews(t, w)
}

func pauseAndConfirm(t *testing.T, w *skeletonWiring)  { t.Helper(); setPausedAndConfirm(t, w, true) }
func resumeAndConfirm(t *testing.T, w *skeletonWiring) { t.Helper(); setPausedAndConfirm(t, w, false) }

// settleViews waits until every bound view has applied a delta stamped at or
// after the engine's current tick.
func settleViews(t *testing.T, w *skeletonWiring) {
	t.Helper()
	want := int64(w.engine.TicksCompleted())
	pollUntil(t, "every view to catch up with the engine tick", func() bool {
		return w.viewTicks.settledTick() >= want
	})
}

// advanceAndSettle advances n ticks, waits for the engine to finish them, then
// for every view to have applied that tick.
func advanceAndSettle(t *testing.T, w *skeletonWiring, n int64) {
	t.Helper()
	want := w.engine.TicksCompleted() + uint64(n)
	if err := w.transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	pollUntil(t, "the engine to complete the advanced ticks", func() bool { return w.engine.TicksCompleted() >= want })
	pollUntil(t, "every view to apply the advanced tick", func() bool { return w.viewTicks.settledTick() >= int64(want) })
}

// barrierSlice is how long commandBarrier waits for one attempt's result before
// it registers a fresh receiver and sends again. A variable only so the
// barrier's regression test can shorten it (restored with t.Cleanup).
var barrierSlice = 250 * time.Millisecond

// barrierSentHook is nil in every test but the barrier's regression test,
// which uses it to learn that an attempt has been sent.
var barrierSentHook func()

// commandBarrier returns once the engine has handled everything sent before
// it (commands and their results are in order). It NEVER changes game state:
// it re-asserts the state the clock has at that instant (Pause if paused,
// Resume if running), which the engine accepts as an idempotent no-op
// (TestBUG1239_CommandBarrier_LeavesTheClockAlone proves the clock's paused
// flag, speed and tick are untouched). Do not use it in a test whose subject
// is the pause state itself: wait on the clock and the status view there.
//
// PRECONDITION: it is neutral only once start-up has finished (the tick
// driver's Ready, see waitDriverReady) and while no other goroutine is
// changing the paused state. The barrier reads the clock and THEN sends the
// matching command; if the boot Resume is still queued, it can read "paused",
// send Pause after that Resume and leave the world paused.
//
// The router drops a result receiver once the router has seen a tick more than
// its pending-result TTL ahead of the tick it registered at, which a lagging
// router on a running clock can do before the result arrives. So each attempt
// waits only barrierSlice for its result, then registers a fresh receiver and
// sends again; a result of ANY attempt suffices (results are in order, so a
// later attempt's result implies every earlier command was handled). The whole
// wait is bounded by helperDeadline and fails naming the barrier.
func commandBarrier(t *testing.T, w *skeletonWiring, corr string) {
	t.Helper()
	deadline := time.Now().Add(helperDeadline) // ONE deadline for the whole barrier
	got := make(chan protocol.CommandResult, 1)
	receiver := resultReceiverFunc(func(r protocol.CommandResult) {
		select { // never block the router
		case got <- r:
		default:
		}
	})
	for attempt := 0; ; attempt++ {
		id := protocol.CorrelationID(corr)
		if attempt > 0 {
			id = protocol.NewCorrelationID()
		}
		c, err := w.engine.Clock()
		if err != nil {
			t.Fatalf("command barrier: Clock: %v", err)
		}
		cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: id}
		if c.Paused() {
			cmd.Kind, cmd.Payload = protocol.KindPause, protocol.PausePayload{}
		} else {
			cmd.Kind, cmd.Payload = protocol.KindResume, protocol.ResumePayload{}
		}
		w.router.RegisterResultHandler(id, receiver)
		// A test that has just flooded the command queue finds it full: retry
		// the send until the engine has drained enough room.
		if err := pollUntilWithin(time.Until(deadline), "room in the command queue for the barrier", func() bool {
			return w.transport.SendCommand(cmd) == nil
		}); err != nil {
			t.Fatal(err)
		}
		if hook := barrierSentHook; hook != nil {
			hook()
		}
		select {
		case res := <-got:
			if !res.Accepted {
				t.Fatalf("command barrier rejected: %+v", res.Error)
			}
			return
		case <-time.After(barrierSlice):
			if time.Now().After(deadline) {
				t.Fatalf("command barrier: no result for %d attempts within %v (the engine never answered the barrier command)", attempt+1, helperDeadline)
			}
		}
	}
}

// waitFrame polls the synchronously composed plFrame until pred holds, for
// asserting an effect that reaches the screen through a delta-fed view.
func waitFrame(t *testing.T, w *skeletonWiring, what string, pred func(frame string) bool) string {
	t.Helper()
	var f string
	pollUntil(t, what, func() bool { f = plFrame(w); return pred(f) })
	return f
}

// lockedSim is a simulation screen whose Show and Sync hold one lock that its
// row reads also hold, so a test reading the screen never overlaps the render
// loop drawing it.
type lockedSim struct {
	tcell.SimulationScreen
	mu sync.Mutex
}

func newLockedSim(t *testing.T, cols, rows int) *lockedSim {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(cols, rows)
	return &lockedSim{SimulationScreen: sim}
}

func (l *lockedSim) Show() { l.mu.Lock(); defer l.mu.Unlock(); l.SimulationScreen.Show() }
func (l *lockedSim) Sync() { l.mu.Lock(); defer l.mu.Unlock(); l.SimulationScreen.Sync() }

// rows returns the first cols cells of each of rows screen rows as text.
func (l *lockedSim) rows(cols, rows int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cells, cw, _ := l.GetContents()
	out := make([]string, rows)
	for y := 0; y < rows; y++ {
		var sb strings.Builder
		for x := 0; x < cols; x++ {
			c := cells[y*cw+x]
			if len(c.Runes) == 0 || c.Runes[0] == 0 {
				sb.WriteRune(' ')
			} else {
				sb.WriteRune(c.Runes[0])
			}
		}
		out[y] = sb.String()
	}
	return out
}

// framedLoop runs a REAL render loop on a lockedSim and counts the frames it
// starts, so a test can wait for "a frame that began after now has been
// drawn" instead of sleeping.
type framedLoop struct {
	sim    *lockedSim
	rl     *core.RenderLoop
	starts atomic.Int64
	stop   chan struct{}
	done   chan struct{}
}

// startFramedLoop starts the loop over screen (a lockedSim, or a wrapper that
// embeds one) with the game's composed draw.
func startFramedLoop(t *testing.T, w *skeletonWiring, sim *lockedSim, screen core.RenderScreen) *framedLoop {
	t.Helper()
	f := &framedLoop{sim: sim, stop: make(chan struct{}), done: make(chan struct{})}
	draw := composeDraw(w)
	f.rl = core.NewRenderLoop(screen, w.viewStore, func(b *core.Buffer, vm *core.ViewModels) {
		f.starts.Add(1)
		draw(b, vm)
	})
	go func() { f.rl.Run(f.stop); close(f.done) }()
	return f
}

// framesAfterNow returns once the loop has drawn n complete frames that began
// after the call: frame k is complete when frame k+1 starts, so it waits for
// n+1 starts, asking for a render each poll.
func (f *framedLoop) framesAfterNow(t *testing.T, n int64) {
	t.Helper()
	target := f.starts.Load() + n + 1
	pollUntil(t, "the render loop to draw the wanted frames", func() bool {
		f.rl.TriggerRender()
		return f.starts.Load() >= target
	})
}

func (f *framedLoop) close() {
	close(f.stop)
	select {
	case <-f.done:
	case <-time.After(helperDeadline):
		panic(fmt.Sprintf("framedLoop.close: the render loop did not stop within %v of its stop signal", helperDeadline))
	}
	f.sim.Fini()
}
