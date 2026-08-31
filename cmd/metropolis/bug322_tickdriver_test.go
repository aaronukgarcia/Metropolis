package main

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// BUG-322's regression suite: the interactive binary must actually advance
// time, the player must be able to stop and restart it, and none of that may
// leak a goroutine or race the render loop.
//
// EVERY assertion here counts TICKS. None asserts a wall-clock duration
// (BUG-031: a wall-clock ceiling turned main red on a correct fix). Where a
// test must wait, it waits for a TICK COUNT to reach a target with a generous
// deadline and fails only if the count never gets there — a slow machine
// makes these tests slower, never redder.

// testSecondsPerMonthAt1x is the pacing constant these tests boot with. One
// game month per real second at 1x means one tick every ~33 ms
// (DailyTicksPerMonth = 30), so a handful of ticks is observable in a few
// hundred milliseconds instead of the 16 seconds per tick the shipped
// data/pacing.json value (480) produces.
//
// This substitutes the DATA LOAD, not the pacing mechanism: production still
// reads data/pacing.json (GR#15). See bootSecondsPerMonthAt1x's doc comment.
const testSecondsPerMonthAt1x int64 = 1

// bootFastPaced boots the REAL composition root — real engine, real
// compose.Wire hook set, real transport, real router, real tick driver — at
// testSecondsPerMonthAt1x, and registers its shutdown.
//
// Nothing here injects a command. That is the whole point: the ticks these
// tests observe can only have come from the driver bootCore starts.
func bootFastPaced(t *testing.T) *skeletonWiring {
	t.Helper()
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return testSecondsPerMonthAt1x, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })

	w, err := bootCore("bug322-"+t.Name(), registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	return w
}

// waitForTicks blocks until the engine has completed at least want ticks, or
// fails after deadline. Returns the count observed.
//
// The deadline is a FAILURE bound ("time never moved"), never a performance
// assertion: no test here fails because ticks arrived too fast or too slow,
// only because they never arrived at all.
func waitForTicks(t *testing.T, w *skeletonWiring, want uint64, deadline time.Duration) uint64 {
	t.Helper()
	stop := time.Now().Add(deadline)
	for {
		if got := w.engine.TicksCompleted(); got >= want {
			return got
		}
		if time.Now().After(stop) {
			t.Fatalf("engine never reached %d ticks (stuck at %d) — the tick driver is not advancing time", want, w.engine.TicksCompleted())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestBUG322_RealBootPath_TimeAdvances is the headline regression: booting
// the interactive composition root and doing NOTHING must produce ticks.
//
// Before the fix this failed at tick 0 forever — bootCore wired a real engine
// with nine phase hooks and never sent a single AdvanceTicks.
func TestBUG322_RealBootPath_TimeAdvances(t *testing.T) {
	w := bootFastPaced(t)

	if got := w.engine.TicksCompleted(); got != 0 {
		t.Fatalf("precondition: engine should start at tick 0, got %d", got)
	}

	got := waitForTicks(t, w, 3, 10*time.Second)
	t.Logf("engine advanced to tick %d with no injected command", got)
}

// TestBUG322_BootsRunningNotPaused pins design decision 4: the binary boots
// RUNNING. A world that boots paused is indistinguishable from the bug.
func TestBUG322_BootsRunningNotPaused(t *testing.T) {
	w := bootFastPaced(t)
	waitForTicks(t, w, 1, 10*time.Second)

	c, err := w.engine.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if c.Paused() {
		t.Fatal("engine is still paused after boot — the driver's boot-time Resume did not take effect")
	}
}

// pressKey feeds one real tcell key event through the binary's own input
// router — the SAME routeKeyInput run.go's InputLoop callback calls — so this
// exercises the actual keybinding, not a direct call to the action behind it.
func pressKey(w *skeletonWiring, r rune) {
	ev := tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyRune, Rune: r, Raw: ev})
}

// waitForPaused waits until the engine clock's paused state matches want.
func waitForPaused(t *testing.T, w *skeletonWiring, want bool, deadline time.Duration) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for {
		c, err := w.engine.Clock()
		if err != nil {
			t.Fatalf("Clock: %v", err)
		}
		if c.Paused() == want {
			return
		}
		if time.Now().After(stop) {
			t.Fatalf("clock never reached Paused=%v", want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestBUG322_SpacePausesAndResumes proves the player can stop the clock and
// restart it, through the real Space binding.
//
// The "pause actually stopped it" assertion is a TICK COUNT comparison across
// a settling window, not a wall-clock claim: after the engine reports paused,
// the driver may still have one in-flight batch queued, so the count is
// sampled AFTER a settle, then again later, and the two must be identical.
func TestBUG322_SpacePausesAndResumes(t *testing.T) {
	w := bootFastPaced(t)
	waitForTicks(t, w, 2, 10*time.Second)

	pressKey(w, ' ')
	waitForPaused(t, w, true, 5*time.Second)

	// Let any command already in flight land, then take the baseline.
	time.Sleep(200 * time.Millisecond)
	frozen := w.engine.TicksCompleted()

	// At this pacing an unpaused driver produces ~30 ticks/second, so if
	// pause did not take, this window is worth ~15 ticks. Zero is the only
	// passing answer.
	time.Sleep(500 * time.Millisecond)
	if got := w.engine.TicksCompleted(); got != frozen {
		t.Fatalf("clock advanced while paused: %d -> %d", frozen, got)
	}

	pressKey(w, ' ')
	waitForPaused(t, w, false, 5*time.Second)
	waitForTicks(t, w, frozen+3, 10*time.Second)
}

// TestBUG322_SpeedKeysStepTheLadder proves ']' and '[' change speed through
// the real binding and the real SetSpeed command path, and that the ladder
// clamps at both ends instead of wrapping.
func TestBUG322_SpeedKeysStepTheLadder(t *testing.T) {
	w := bootFastPaced(t)

	waitForSpeed := func(want enginecore.Speed) {
		t.Helper()
		stop := time.Now().Add(5 * time.Second)
		for {
			c, err := w.engine.Clock()
			if err != nil {
				t.Fatalf("Clock: %v", err)
			}
			if c.Speed() == want {
				return
			}
			if time.Now().After(stop) {
				t.Fatalf("speed never reached %d (stuck at %d)", want, c.Speed())
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	waitForSpeed(enginecore.Speed1x)
	pressKey(w, ']')
	waitForSpeed(enginecore.Speed2x)
	pressKey(w, ']')
	waitForSpeed(enginecore.Speed4x)

	// Top of the ladder: another ']' must be a no-op, never a wrap to 1x.
	pressKey(w, ']')
	time.Sleep(200 * time.Millisecond)
	waitForSpeed(enginecore.Speed4x)

	pressKey(w, '[')
	waitForSpeed(enginecore.Speed2x)
	pressKey(w, '[')
	waitForSpeed(enginecore.Speed1x)
	pressKey(w, '[')
	time.Sleep(200 * time.Millisecond)
	waitForSpeed(enginecore.Speed1x)
}

// TestBUG322_StatusBarIsHonestBeforeAnyDelta pins the UN-PRIMED branch of
// Line(): before the first engine.status delta arrives, the bottom row must
// still say what it is and admit it is waiting — never render blank.
//
// This is the gap an independent destructive round proved was open (F1): with
// the un-primed branch blanked to "", the whole suite stayed green, because
// every other status-bar assertion here waits for a delta first. A blank
// bottom row is the exact "a frozen sim and a broken binary look identical"
// failure mode statusbar.go's own doc comment exists to prevent, so it gets
// its own assertion rather than relying on the primed path to imply it.
//
// No boot: a bare newStatusBar() IS the un-primed state.
func TestBUG322_StatusBarIsHonestBeforeAnyDelta(t *testing.T) {
	fresh := newStatusBar()

	if _, ok := fresh.Snapshot(); ok {
		t.Fatal("precondition: a fresh statusBar must report no snapshot yet")
	}

	line := fresh.Line()
	for _, want := range []string{"TICK", "MONTH", "SPEED", "waiting for engine.status", statusBarKeyHelp} {
		if !strings.Contains(line, want) {
			t.Fatalf("un-primed status bar is not honest: %q missing %q", line, want)
		}
	}
	// It must not claim a clock state it has never been told about.
	for _, forbidden := range []string{"RUNNING", "PAUSED"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("un-primed status bar claims %q with no engine.status delta: %q", forbidden, line)
		}
	}

	// And the honest text must actually reach the rendered bottom row, not
	// just the model — "nothing on screen" is the failure being pinned.
	buf := core.NewBuffer(120, 4)
	statusBarDraw(fresh)(buf, &core.ViewModels{})
	rendered := bufferRow(buf, 3)
	if !strings.Contains(rendered, "TICK") || !strings.Contains(rendered, "waiting for engine.status") {
		t.Fatalf("un-primed status row rendered blank/unhelpful: %q", rendered)
	}
}

// TestBUG322_StatusBarShowsTimeMoving proves the third arm of the bug is
// closed: the live engine.status figures reach a rendered surface, so a
// running sim no longer looks identical to a frozen one.
//
// It asserts on the rendered BUFFER, not just the model — the failure mode
// being fixed is "nothing on screen", so the test reads what would be on
// screen.
func TestBUG322_StatusBarShowsTimeMoving(t *testing.T) {
	w := bootFastPaced(t)
	waitForTicks(t, w, 2, 10*time.Second)

	// The status bar is fed by engine.status deltas, which the pump publishes
	// on each AdvanceTicks; wait for one carrying a non-zero tick.
	stop := time.Now().Add(10 * time.Second)
	for {
		v, ok := w.statusBar.Snapshot()
		if ok && v.Tick > 0 {
			break
		}
		if time.Now().After(stop) {
			t.Fatal("statusBar never received an engine.status delta with a non-zero tick — the view is not reaching the screen")
		}
		time.Sleep(2 * time.Millisecond)
	}

	buf := core.NewBuffer(120, 10)
	statusBarDraw(w.statusBar)(buf, &core.ViewModels{})
	rendered := bufferRow(buf, 9)

	for _, want := range []string{"TICK", "MONTH", "SPEED", "RUNNING", "[Space]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("status row %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "TICK 0 ") {
		t.Fatalf("status row still shows tick 0: %q", rendered)
	}
}

// TestBUG322_ComposedDrawIncludesTheStatusLine pins the WIRING, not just the
// widget: whatever runInteractive hands to the render loop must draw the
// status line over the active screen. Without this, deleting the overlay from
// composeDraw would leave every other test in this file green while the
// binary went back to showing nothing about the clock.
func TestBUG322_ComposedDrawIncludesTheStatusLine(t *testing.T) {
	w := bootFastPaced(t)
	waitForTicks(t, w, 2, 10*time.Second)

	buf := core.NewBuffer(120, 12)
	composeDraw(w)(buf, &core.ViewModels{})

	last := bufferRow(buf, 11)
	if !strings.Contains(last, "TICK") || !strings.Contains(last, "[Space]") {
		t.Fatalf("composed draw did not put the status line on the last row: %q", last)
	}
}

// bufferRow reads one row of a Buffer back as a string.
func bufferRow(b *core.Buffer, y int) string {
	w, _ := b.Size()
	var sb strings.Builder
	for x := 0; x < w; x++ {
		sb.WriteRune(b.Get(x, y).Rune)
	}
	return sb.String()
}

// TestBUG322_ShutdownLeaksNoGoroutine is the leak guard. The driver is a new
// long-lived goroutine in a binary that has already been bitten by shutdown
// races (BUG-020's premature-close, FEAT-208's unjoined pump), so it must
// provably return before the process does.
//
// It also implicitly covers "no send on a closed channel": shutdown() closes
// the transport only after wg.Wait(), and the driver sends on the transport,
// so a driver still alive at Close would be caught here first — and by
// -race in the same run.
func TestBUG322_ShutdownLeaksNoGoroutine(t *testing.T) {
	settle := func() int {
		// Goroutine counts need a moment to settle after any boot/teardown;
		// take the lowest reading over a short window rather than one sample.
		best := runtime.NumGoroutine()
		for i := 0; i < 50; i++ {
			time.Sleep(10 * time.Millisecond)
			if n := runtime.NumGoroutine(); n < best {
				best = n
			}
		}
		return best
	}

	before := settle()

	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return testSecondsPerMonthAt1x, nil }
	defer func() { bootSecondsPerMonthAt1x = prev }()

	w, err := bootCore("bug322-shutdown", registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	waitForTicks(t, w, 2, 10*time.Second)
	during := runtime.NumGoroutine()
	if during <= before {
		t.Fatalf("boot started no goroutines (%d -> %d) — this test is not observing what it claims to", before, during)
	}
	w.shutdown()

	after := settle()
	if after > before {
		t.Fatalf("goroutine leak across boot/shutdown: before=%d during=%d after=%d", before, during, after)
	}
}

// TestBUG322_RenderLoopAndDriverRunConcurrently is the -race target: the
// driver's goroutine and the render loop's draw path touch the same statusBar
// and the same engine clock at their own independent cadences. Run under
// -race -count=2 this is what proves the two do not share state unsafely.
func TestBUG322_RenderLoopAndDriverRunConcurrently(t *testing.T) {
	w := bootFastPaced(t)

	draw := statusBarDraw(w.statusBar)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Two concurrent "render loops" hammering the draw path while the driver
	// writes to the same statusBar from its own goroutine.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := core.NewBuffer(100, 8)
			for {
				select {
				case <-stop:
					return
				default:
				}
				draw(buf, &core.ViewModels{})
				pressKey(w, ']')
				pressKey(w, '[')
				// Yield so the tick-driver goroutine is not starved on a
				// limited-core CI runner under -race (BUG-464): these two
				// busy-spinners would otherwise peg every P and the wall-clock
				// tick driver would see zero ticks. The goroutines still hammer
				// the draw/keybinding path concurrently — they just let the
				// scheduler run the driver between iterations.
				runtime.Gosched()
			}
		}()
	}

	// Generous "time never moved" bound per waitForTicks's doc contract: only a
	// genuine hang should fire this, never scheduling contention on a loaded
	// -race runner. The spinners keep hammering until close(stop) below, so a
	// longer wait means MORE concurrent race coverage, not less.
	waitForTicks(t, w, 5, 120*time.Second)
	close(stop)
	wg.Wait()
}

// ── tickDriver unit tests ────────────────────────────────────────────────

// fakeClock is a hand-driven enginecore.Clock source for the driver's own
// unit tests. enginecore.Clock's pause/speed setters are unexported, so the
// snapshots are built through the package's real constructor plus the real
// command handlers on a bare Engine — no reflection, no shadow type.
type fakeClock struct {
	mu     sync.Mutex
	paused bool
	speed  enginecore.Speed
}

func (f *fakeClock) set(paused bool, speed enginecore.Speed) {
	f.mu.Lock()
	f.paused, f.speed = paused, speed
	f.mu.Unlock()
}

// read builds a real Clock in the requested state by driving a real Engine
// with real Pause/Resume/SetSpeed commands — the same path the keys use.
func (f *fakeClock) read(e *enginecore.Engine) (enginecore.Clock, error) {
	f.mu.Lock()
	paused, speed := f.paused, f.speed
	f.mu.Unlock()

	kind, payload := protocol.Kind(protocol.KindResume), protocol.CommandPayload(protocol.ResumePayload{})
	if paused {
		kind, payload = protocol.KindPause, protocol.PausePayload{}
	}
	e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            kind, Payload: payload,
	})
	e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: int(speed)},
	})
	return e.Clock()
}

// TestBUG322_Driver_EmitsNothingWhilePaused: a paused clock must produce zero
// AdvanceTicks commands, and paused wall time must not become catch-up debt
// that sprints on resume.
func TestBUG322_Driver_EmitsNothingWhilePaused(t *testing.T) {
	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1))
	fc := &fakeClock{paused: true, speed: enginecore.Speed1x}

	var advances, resumes atomic.Int64
	var requested atomic.Int64
	d := newTickDriver(
		func() (enginecore.Clock, error) { return fc.read(e) },
		func(cmd protocol.Command) error {
			switch cmd.Kind {
			case protocol.KindAdvanceTicks:
				advances.Add(1)
				requested.Add(cmd.Payload.(protocol.AdvanceTicksPayload).N)
			case protocol.KindResume:
				resumes.Add(1)
			}
			return nil
		},
		1,
	)
	d.pollInterval = 2 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()

	// Well over a tick's worth of real time (33 ms at this pacing) while paused.
	time.Sleep(300 * time.Millisecond)
	if got := advances.Load(); got != 0 {
		t.Fatalf("driver emitted %d AdvanceTicks while paused, want 0", got)
	}
	if got := resumes.Load(); got != 1 {
		t.Fatalf("driver sent %d Resume commands at start, want exactly 1", got)
	}

	// Unpause: ticks must start, and the FIRST batch must not be a sprint
	// through the 300 ms of paused time (that would be ~9 ticks at once).
	fc.set(false, enginecore.Speed1x)
	stop := time.Now().Add(5 * time.Second)
	for advances.Load() == 0 {
		if time.Now().After(stop) {
			t.Fatal("driver never resumed emitting after unpause")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := requested.Load(); got > 2 {
		t.Fatalf("first batch after resume asked for %d ticks — paused time became catch-up debt", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not return on ctx cancel")
	}
}

// TestBUG322_Accrue_CarriesTheRemainder is the deterministic, clock-free
// proof of catch-up rule 1: over a long run of out-of-phase wakes, the ticks
// asked for must equal the ticks DUE, exactly. Not "close to" — exactly.
//
// 20 ms wakes against a 33.333333 ms tick is the worst case for a naive
// implementation: every emission leaves ~13 ms behind. Carrying it yields
// floor(total/npt) ticks; discarding it yields one tick per two wakes, a
// third slow. That gap is what this asserts, with no timer involved and
// therefore no flakiness.
func TestBUG322_Accrue_CarriesTheRemainder(t *testing.T) {
	npt, ok := nanosPerTick(1, enginecore.Speed1x) // 33.333333 ms
	if !ok {
		t.Fatal("nanosPerTick(1, 1x) not ok")
	}
	const wakes = 300
	elapsed := (20 * time.Millisecond).Nanoseconds()

	var credit, total int64
	for i := 0; i < wakes; i++ {
		var n int64
		n, credit = accrue(credit, elapsed, npt)
		total += n
	}

	want := (int64(wakes) * elapsed) / npt
	if total != want {
		t.Fatalf("accrue asked for %d ticks over %d wakes, want exactly %d (drift of %d ticks means the sub-tick remainder is being discarded)",
			total, wakes, want, want-total)
	}
	if credit < 0 || credit >= npt {
		t.Fatalf("carried credit %d is out of range [0, %d)", credit, npt)
	}
}

// TestBUG322_Accrue_BoundsOneBatchButDefersTheRest is catch-up rule 2: a
// single command is capped, and the excess is CARRIED, not dropped. The
// second assertion is the one that matters — a cap that discarded the excess
// would satisfy the first and silently lose ticks.
func TestBUG322_Accrue_BoundsOneBatchButDefersTheRest(t *testing.T) {
	const npt = int64(time.Millisecond)
	// Two full batches' worth of elapsed time arriving in one wake.
	elapsed := 2 * maxTicksPerBatch * npt

	n, credit := accrue(0, elapsed, npt)
	if n != maxTicksPerBatch {
		t.Fatalf("first batch = %d, want the cap %d", n, maxTicksPerBatch)
	}
	if n > enginecore.MaxAdvanceTicksPerCall {
		t.Fatalf("batch %d exceeds the engine's own MaxAdvanceTicksPerCall %d", n, enginecore.MaxAdvanceTicksPerCall)
	}
	// The deferred half must still be there, and must come out on the next
	// wake even if no further time elapses.
	n2, _ := accrue(credit, 0, npt)
	if n2 != maxTicksPerBatch {
		t.Fatalf("deferred batch = %d, want %d — the capped excess was dropped, not carried", n2, maxTicksPerBatch)
	}
}

// TestBUG322_Accrue_ClampsTheBacklog is catch-up rule 3: an absurd elapsed
// time (a laptop resumed from sleep, a debugger breakpoint) must not leave
// the driver owing days of simulation. Credit is clamped, so the sim runs
// slower than the wall clock and catches up in bounded batches.
//
// The clamp is on WALL-CLOCK CREDIT, never on ticks: the assertion below is
// that the retained backlog is finite, not that any tick index was skipped —
// nothing here can skip one, since the engine numbers the ticks.
func TestBUG322_Accrue_ClampsTheBacklog(t *testing.T) {
	const npt = int64(time.Millisecond)
	hugely := (48 * time.Hour).Nanoseconds()

	n, credit := accrue(0, hugely, npt)
	if n != maxTicksPerBatch {
		t.Fatalf("batch after a 48h stall = %d, want the cap %d", n, maxTicksPerBatch)
	}
	if maxOwed := npt * maxBacklogTicks; credit > maxOwed {
		t.Fatalf("carried credit %d exceeds the backlog clamp %d — debt is unbounded and the driver will spiral", credit, maxOwed)
	}

	// Drain it: the backlog must be finite, i.e. it must run out.
	drained := int64(0)
	for i := 0; i < 1000 && credit > 0; i++ {
		var n int64
		n, credit = accrue(credit, 0, npt)
		drained += n
		if n == 0 {
			break
		}
	}
	if credit >= npt {
		t.Fatalf("backlog never drained: %d ns still owed after 1000 wakes", credit)
	}
	if drained > maxBacklogTicks {
		t.Fatalf("drained %d ticks of backlog, clamp allows at most %d", drained, maxBacklogTicks)
	}
}

// TestBUG322_Accrue_NonPositiveInterval: a non-positive npt must be refused,
// not divided by (GR#16 boundary discipline).
func TestBUG322_Accrue_NonPositiveInterval(t *testing.T) {
	for _, npt := range []int64{0, -1} {
		n, credit := accrue(500, 500, npt)
		if n != 0 || credit != 500 {
			t.Fatalf("accrue(500, 500, %d) = (%d, %d), want (0, 500)", npt, n, credit)
		}
	}
}

// TestBUG322_Driver_BatchIsBounded is the live counterpart to the accrue
// tests above: it proves the RUNNING driver actually reaches the catch-up
// path and that what comes out on the wire respects the cap.
func TestBUG322_Driver_BatchIsBounded(t *testing.T) {

	e := enginecore.NewEngine(enginecore.WithSecondsPerMonthAt1x(1))
	fc := &fakeClock{paused: false, speed: enginecore.Speed4x}

	var maxN atomic.Int64
	d := newTickDriver(
		func() (enginecore.Clock, error) { return fc.read(e) },
		func(cmd protocol.Command) error {
			if cmd.Kind == protocol.KindAdvanceTicks {
				n := cmd.Payload.(protocol.AdvanceTicksPayload).N
				for {
					cur := maxN.Load()
					if n <= cur || maxN.CompareAndSwap(cur, n) {
						break
					}
				}
			}
			return nil
		},
		1,
	)
	// A poll interval far LONGER than a tick (8.3 ms at 4x) forces every wake
	// to convert a large backlog — the catch-up path.
	d.pollInterval = 400 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done

	if maxN.Load() <= 1 {
		t.Fatalf("catch-up path never exercised (largest batch %d) — this test is not observing what it claims to", maxN.Load())
	}
	if maxN.Load() > maxTicksPerBatch {
		t.Fatalf("driver asked for %d ticks in one command, cap is %d", maxN.Load(), maxTicksPerBatch)
	}
	if maxTicksPerBatch > enginecore.MaxAdvanceTicksPerCall {
		t.Fatalf("maxTicksPerBatch %d exceeds the engine's own MaxAdvanceTicksPerCall %d", maxTicksPerBatch, enginecore.MaxAdvanceTicksPerCall)
	}
}

// TestBUG322_NanosPerTick covers the pacing arithmetic, including the shipped
// data/pacing.json value and the boundary cases GR#16 requires be rejected
// rather than silently producing garbage.
func TestBUG322_NanosPerTick(t *testing.T) {
	cases := []struct {
		name    string
		seconds int64
		speed   enginecore.Speed
		want    time.Duration
		wantOK  bool
	}{
		{"shipped default 480s at 1x", 480, enginecore.Speed1x, 16 * time.Second, true},
		{"shipped default 480s at 2x", 480, enginecore.Speed2x, 8 * time.Second, true},
		{"shipped default 480s at 4x", 480, enginecore.Speed4x, 4 * time.Second, true},
		{"debug 8x", 480, enginecore.Speed8xDebug, 2 * time.Second, true},
		// The exact case Clock.SecondsPerMonth's integer division would floor
		// to zero: 1 second per month at 4x. The driver must still produce a
		// real interval.
		{"sub-multiplier pacing", 1, enginecore.Speed4x, time.Second / 120, true},
		{"zero pacing rejected", 0, enginecore.Speed1x, 0, false},
		{"negative pacing rejected", -5, enginecore.Speed1x, 0, false},
		{"zero speed rejected", 480, 0, 0, false},
		{"negative speed rejected", 480, -1, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nanosPerTick(tc.seconds, tc.speed)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if time.Duration(got) != tc.want {
				t.Fatalf("nanosPerTick(%d, %d) = %v, want %v", tc.seconds, tc.speed, time.Duration(got), tc.want)
			}
		})
	}
}

// TestBUG322_StatusBarHelpMatchesBindings keeps the on-screen key prompt
// honest (GR#3): every key the status line advertises must actually be
// registered on the chrome grammar.
func TestBUG322_StatusBarHelpMatchesBindings(t *testing.T) {
	w := bootFastPaced(t)

	// Each advertised clock key must dispatch on the chrome global grammar.
	for _, r := range []rune{' ', ']', '['} {
		res := w.chromeGrammar.Feed(keys.KeyRune(r))
		if res.Status != keys.GlobalDispatched {
			t.Fatalf("status line advertises %q but the chrome grammar did not dispatch it (status=%v)", string(r), res.Status)
		}
	}
	for _, want := range []string{"[Space]", "]", "[", "F1/F2/F4", "[q] quit"} {
		if !strings.Contains(statusBarKeyHelp, want) {
			t.Fatalf("statusBarKeyHelp %q does not mention %q", statusBarKeyHelp, want)
		}
	}
}
