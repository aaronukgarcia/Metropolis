package core

import (
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
)

// RenderTick is the UI render tick rate (UI-SPEC §1/§5: "10 Hz UI tick
// (plus immediate on input)").
const RenderTick = 100 * time.Millisecond

// RenderScreen is the subset of tcell.Screen that RenderLoop needs:
// ScreenWriter (diff.go) plus Size. tcell.Screen satisfies it as-is.
type RenderScreen interface {
	ScreenWriter
	Size() (int, int)
}

// DrawFunc draws one widget/pane's content into back, given the current
// front view-model snapshot. Draw callbacks must only write to back —
// never read or write front (Flush owns reconciling the two) and never
// touch the RenderScreen directly (M0-ENG §1.1: RenderLoop is the sole
// screen owner; a DrawFunc that reached for a screen reference would
// break that invariant, which is exactly why DrawFunc's signature has no
// screen parameter at all).
type DrawFunc func(back *Buffer, vm *ViewModels)

// stubDraw is what RenderLoop draws instead of the normal DrawFuncs when
// the terminal is below MinCols x MinRows (AC-6): a short message, not a
// crash and not a garbled partial layout.
func stubDraw(back *Buffer, _ *ViewModels) {
	msg := []rune("terminal too small - resize to at least 120x30")
	w, h := back.Size()
	y := h / 2
	for x := 0; x < w && x < len(msg); x++ {
		back.Set(x, y, msg[x], tcell.StyleDefault)
	}
}

// RenderLoop is T-RENDER: the sole goroutine that may call methods on a
// RenderScreen (AC-3). It owns the front/back Buffer pair, runs a 10Hz
// ticker plus immediate render-on-input, and — when the terminal is
// below the minimum size — substitutes stubDraw for the normal draw
// callbacks (AC-6).
//
// # Single-goroutine enforcement (AC-3, M0-ENG §1.1)
//
// renderOnce guards itself with an atomic CAS (owned). A second,
// concurrent call — which could only happen if a caller mistakenly
// invoked renderOnce/Run from more than one goroutine, or re-entrantly
// from within a DrawFunc — panics immediately with a message naming the
// violated rule, rather than silently interleaving tcell.Screen calls
// (which is the failure mode that only shows up as a real-terminal race,
// per this package's doc comment). The guard is always active (not
// behind a build tag): a single atomic CAS is cheap enough that gating
// it would only add complexity for no measurable benefit on the render
// path's budget (UI-SPEC §5).
type RenderLoop struct {
	screen RenderScreen
	front  *Buffer
	back   *Buffer
	draws  []DrawFunc
	views  *ViewStore

	renderNow chan struct{}
	owned     atomic.Bool

	lastStats FlushStats
	belowMin  bool
}

// NewRenderLoop constructs a RenderLoop over screen, sized to screen's
// current dimensions, reading view models from views and drawing with
// draws (invoked in order every render).
func NewRenderLoop(screen RenderScreen, views *ViewStore, draws ...DrawFunc) *RenderLoop {
	w, h := screen.Size()
	r := &RenderLoop{
		screen:    screen,
		front:     NewBuffer(w, h),
		back:      NewBuffer(w, h),
		draws:     draws,
		views:     views,
		renderNow: make(chan struct{}, 1),
		belowMin:  BelowMinimum(w, h),
	}
	return r
}

// TriggerRender requests an immediate render without waiting for the
// next tick (UI-SPEC §1: "render loop ... plus immediate on input").
// Non-blocking: if a render is already pending, this is a no-op rather
// than a queued second render (renders are idempotent snapshots of
// current state, so coalescing is correct, not lossy). Safe to call from
// any goroutine, including as an InputLoop.OnDelivered callback.
func (r *RenderLoop) TriggerRender() {
	select {
	case r.renderNow <- struct{}{}:
	default:
	}
}

// LastStats returns the FlushStats from the most recently completed
// render (zero value before the first render).
func (r *RenderLoop) LastStats() FlushStats { return r.lastStats }

// BelowMinimum reports whether the current terminal size is below
// MinCols x MinRows (AC-6).
func (r *RenderLoop) BelowMinimum() bool { return r.belowMin }

// Resize reallocates front/back to (w, h) and re-evaluates the
// below-minimum stub state. Safe to call before Run starts (initial
// sizing) or from within the render goroutine (e.g. in response to a
// translated ResizeInput); it is NOT safe to call concurrently with a
// render in progress from another goroutine — callers should route
// resize handling through the same goroutine that calls Run, e.g. by
// reacting to InputLoop's ResizeInput messages and calling Resize before
// TriggerRender.
func (r *RenderLoop) Resize(w, h int) {
	r.front.Resize(w, h)
	r.back.Resize(w, h)
	r.belowMin = BelowMinimum(w, h)
}

// Run drives T-RENDER: an initial render, then a loop alternating on a
// RenderTick ticker and TriggerRender's channel, until stop is closed.
// Intended to run in its own goroutine.
func (r *RenderLoop) Run(stop <-chan struct{}) {
	r.renderOnce()
	ticker := time.NewTicker(RenderTick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			r.renderOnce()
		case <-r.renderNow:
			r.renderOnce()
		}
	}
}

// renderOnce performs exactly one draw+flush cycle. See the ownership
// guard note on RenderLoop's doc comment.
func (r *RenderLoop) renderOnce() {
	if !r.owned.CompareAndSwap(false, true) {
		panic("ui/core: concurrent tcell screen access detected — T-RENDER must be the sole goroutine touching the screen (M0-ENG §1.1, UI-SPEC §1)")
	}
	defer r.owned.Store(false)

	w, h := r.screen.Size()
	if bw, bh := r.back.Size(); bw != w || bh != h {
		r.Resize(w, h)
	}

	vm := r.views.Front()
	if r.belowMin {
		stubDraw(r.back, vm)
	} else {
		for _, d := range r.draws {
			d(r.back, vm)
		}
	}

	r.lastStats = Flush(r.screen, r.back, r.front)
}
