package main

// Round r1 (BUG-1239), hostile reviewer, now the regression test of its
// finding: commandBarrier registered its result receiver at the ROUTER's
// current tick, and the router prunes a registration more than 64 ticks behind
// the newest tick it has seen. In a test whose clock is RUNNING
// (bootFastPaced) a router that lags the engine (slow runner, -race) could see
// a delta more than 64 ticks newer before the barrier's own result: the
// registration was pruned, the result was a route miss, and the old barrier
// waited on t.Context(), which never ends while the test body is blocked, so
// the whole package hung until -timeout.
//
// This forces the lag: the router is held on one delta (viewApplyGate) while
// the clock runs on for 100 ticks, then the barrier is sent and the router let
// go as soon as the barrier's first attempt is on the wire. The fixed barrier
// re-registers and re-sends each barrierSlice, so it returns; the test bounds
// itself with a short helperDeadline so a regression fails in seconds with the
// barrier's own message instead of hanging.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type r1Hold struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func TestRoundR1_BUG1239_BarrierSurvivesAPrunedRegistration(t *testing.T) {
	prevDeadline, prevSlice := helperDeadline, barrierSlice
	helperDeadline, barrierSlice = 30*time.Second, 50*time.Millisecond
	t.Cleanup(func() { helperDeadline, barrierSlice = prevDeadline, prevSlice })

	var cur atomic.Pointer[r1Hold]
	viewApplyGate = func(string, protocol.Delta) {
		if h := cur.Load(); h != nil && h.armed.CompareAndSwap(true, false) {
			close(h.entered)
			<-h.release
		}
	}
	t.Cleanup(func() { viewApplyGate = nil; barrierSentHook = nil })
	h := &r1Hold{entered: make(chan struct{}), release: make(chan struct{})}
	cur.Store(h)
	w := bootFastPaced(t)
	// Release the held router on EVERY exit. Registered after bootFastPaced's own
	// shutdown cleanup, so (cleanups run last-in first-out) it runs BEFORE the
	// shutdown, which would otherwise wait on the held router forever.
	var freeOnce sync.Once
	free := func() { freeOnce.Do(func() { close(h.release) }) }
	t.Cleanup(free)

	pollUntil(t, "the clock to run", func() bool { return w.engine.TicksCompleted() >= 2 })
	h.armed.Store(true)
	select {
	case <-h.entered:
	case <-time.After(helperDeadline):
		t.Fatal("the router was never held on a delta")
	}
	held := w.engine.TicksCompleted()
	pollUntil(t, "the clock to run 100 ticks past the held router", func() bool {
		return w.engine.TicksCompleted() >= held+100
	})
	// Let the router go once the barrier's first attempt has been sent: the
	// router then works through 100+ newer deltas, which prunes that
	// attempt's registration.
	var once atomic.Bool
	barrierSentHook = func() {
		if once.CompareAndSwap(false, true) {
			free()
		}
	}
	commandBarrier(t, w, string(protocol.NewCorrelationID()))
	barrierSentHook = nil
	if !once.Load() {
		t.Fatal("the barrier returned without sending")
	}
	t.Log("barrier returned after the router was held past the prune window")
}

// TestBUG1239_CommandBarrier_LeavesTheClockAlone: the barrier is a no-op for
// the game. On a running clock and on a paused clock, the paused flag, the
// speed and the tick are the same after it as before.
func TestBUG1239_CommandBarrier_LeavesTheClockAlone(t *testing.T) {
	w := bootReady(t, "bug1239-barrier-noop")
	read := func() (bool, int, uint64) {
		c, err := w.engine.Clock()
		if err != nil {
			t.Fatalf("Clock: %v", err)
		}
		return c.Paused(), int(c.Speed()), w.engine.TicksCompleted()
	}
	for _, wantPaused := range []bool{false, true} {
		setPausedAndConfirm(t, w, wantPaused)
		p0, s0, k0 := read()
		for i := 0; i < 3; i++ {
			commandBarrier(t, w, string(protocol.NewCorrelationID()))
		}
		p1, s1, k1 := read()
		if p0 != wantPaused || p1 != p0 || s1 != s0 || k1 != k0 {
			t.Fatalf("barrier changed the clock: paused %v->%v speed %d->%d tick %d->%d (wanted paused=%v)", p0, p1, s0, s1, k0, k1, wantPaused)
		}
	}
}
