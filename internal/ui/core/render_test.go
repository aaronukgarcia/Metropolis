package core

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func newSimScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("SimulationScreen.Init: %v", err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)
	return sim
}

func TestRenderLoop_DrawsAndFlushesViaSimulationScreen(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()

	var drawCalls int
	draw := func(back *Buffer, vm *ViewModels) {
		drawCalls++
		back.Set(0, 0, 'H', tcell.StyleDefault)
	}

	r := NewRenderLoop(sim, views, draw)
	r.renderOnce()

	if drawCalls != 1 {
		t.Fatalf("draw called %d times, want 1", drawCalls)
	}
	ch, _, _, _ := sim.GetContent(0, 0)
	if ch != 'H' {
		t.Fatalf("simulated screen cell (0,0) = %q, want 'H'", ch)
	}
	stats := r.LastStats()
	if stats.CellsChanged == 0 {
		t.Fatal("expected at least one changed cell on first render")
	}
}

// TestRenderLoop_SingleGoroutineGuard is AC-3's runtime assertion check:
// a second, concurrent call to renderOnce while one is already in
// progress must panic rather than silently interleave tcell.Screen
// calls.
func TestRenderLoop_SingleGoroutineGuard(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()

	release := make(chan struct{})
	entered := make(chan struct{})
	blockingDraw := func(back *Buffer, vm *ViewModels) {
		close(entered)
		<-release
	}

	r := NewRenderLoop(sim, views, blockingDraw)

	go r.renderOnce()
	<-entered // the first renderOnce is now inside the guarded section

	func() {
		defer func() {
			rec := recover()
			if rec == nil {
				t.Fatal("expected a panic from a concurrent renderOnce call")
			}
			msg, ok := rec.(string)
			if !ok || !strings.Contains(msg, "concurrent tcell screen access") {
				t.Fatalf("panic value = %v, want a message about concurrent screen access", rec)
			}
		}()
		r.renderOnce()
	}()

	close(release)
}

func TestRenderLoop_BelowMinimumCollapsesToStub(t *testing.T) {
	sim := newSimScreen(t, 60, 15) // below MinCols/MinRows
	views := NewViewStore()

	var normalDrawCalled bool
	normalDraw := func(back *Buffer, vm *ViewModels) { normalDrawCalled = true }

	r := NewRenderLoop(sim, views, normalDraw)
	if !r.BelowMinimum() {
		t.Fatal("expected BelowMinimum() true for a 60x15 screen")
	}

	r.renderOnce()

	if normalDrawCalled {
		t.Fatal("normal DrawFunc must not run when below minimum size (AC-6: collapse to stub instead)")
	}
	stats := r.LastStats()
	if stats.CellsChanged == 0 {
		t.Fatal("expected the stub message to have produced a non-empty flush, not a crash or blank screen")
	}
}

func TestRenderLoop_ResizeReflowsBuffers(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()
	r := NewRenderLoop(sim, views)

	if r.BelowMinimum() {
		t.Fatal("120x30 should not be below minimum")
	}

	sim.SetSize(60, 15)
	r.renderOnce() // renderOnce detects the size change and calls Resize

	if !r.BelowMinimum() {
		t.Fatal("after resizing the screen to 60x15, RenderLoop should report BelowMinimum")
	}
	w, h := r.back.Size()
	if w != 60 || h != 15 {
		t.Fatalf("back buffer size = %dx%d, want 60x15", w, h)
	}
}

// TestConcurrentRenderAndViews_NoTornReads drives T-RENDER and T-VIEWS
// simultaneously against a shared ViewStore (AC-4) and is meant to be
// run with -race (AC-11): a torn read of the published ViewModels
// pointer, or any unsynchronized map access, would be flagged by the
// race detector.
func TestConcurrentRenderAndViews_NoTornReads(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()
	tr := newTestTransport()
	viewsLoop := NewViewsLoop(tr, views, "corr-concurrent")

	var readCount atomic.Int64
	draw := func(back *Buffer, vm *ViewModels) {
		// Read every field of the snapshot; the race detector catches any
		// unsynchronized concurrent write underneath a read here.
		for k, v := range vm.Patches {
			_ = k
			_ = v
		}
		_ = vm.AnyStale()
		readCount.Add(1)
	}
	renderLoop := NewRenderLoop(sim, views, draw)

	stop := make(chan struct{})
	renderDone := make(chan struct{})
	viewsDone := make(chan struct{})
	go func() { renderLoop.Run(stop); close(renderDone) }()
	go func() { viewsLoop.Run(stop); close(viewsDone) }()

	sub := protocol.SubscriptionID("sub-race")
	for i := 1; i <= 50; i++ {
		tr.SendDelta(protocol.Delta{
			SubscriptionID: sub,
			Tick:           protocol.Tick(i),
			Seq:            uint64(i),
			Patch:          json.RawMessage(`{"n":` + strconv.Itoa(i) + `}`),
		})
		if i%5 == 0 {
			renderLoop.TriggerRender()
		}
	}

	waitForCondition(t, func() bool { return readCount.Load() > 0 })
	time.Sleep(20 * time.Millisecond)

	close(stop)
	<-renderDone
	<-viewsDone
}
