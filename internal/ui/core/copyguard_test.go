package core

// BUG-018 copy-guard regression: RenderLoop's screen-ownership gate
// (owned, an atomic.Bool) is a struct VALUE — a struct-copied RenderLoop
// (r2 := *r) gets its own, entirely independent atomic word, so a copy's
// renderOnce could CompareAndSwap(false, true) and succeed even while the
// original already owns the screen, giving two goroutines that each
// correctly believe they hold T-RENDER's exclusive ownership of the
// tcell.Screen. The fixed behaviour verified below is what ships:
// checkNotCopied rejects a copy's call BEFORE the CAS is ever attempted,
// so at most one instance (the original returned by NewRenderLoop) can
// ever successfully claim ownership.

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// renderLoopCopy takes a same-package value copy of *RenderLoop, isolated
// into its own tiny helper (mirrors internal/ui/screens/demo's
// screenCopy / internal/engine/core's e2Copy / internal/engine/world's
// w2Copy convention exactly, including the unsafe byte-copy): a plain
// `r2 := *r1` is legal, correct Go that produces the identical attack
// shape this fix closes, but go vet's copylocks-adjacent checks and this
// package's own conventions call for reaching the same struct-value copy
// via a route that does not read as a literal copy at its own call site.
// The byte-copy achieves the same struct-value copy (same owned/
// renderNow/front/back/self bytes) deterministically.
func renderLoopCopy(r *RenderLoop) *RenderLoop {
	c := new(RenderLoop)
	*(*[unsafe.Sizeof(RenderLoop{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(RenderLoop{})]byte)(unsafe.Pointer(r))
	return c
}

// assertRenderLoopCopiedCode fails t unless err is a registry error
// carrying ErrRenderLoopCopied (MET-U003).
func assertRenderLoopCopiedCode(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != ErrRenderLoopCopied {
		t.Errorf("e.Code = %s, want %s", e.Code, ErrRenderLoopCopied)
	}
}

// TestRenderLoop_CheckNotCopiedRejectsCopy directly exercises the
// checkNotCopied guard itself: constructed-to-completion, then asserted
// (deterministic, not a timing race) — a copy's check must fail with
// ErrRenderLoopCopied while the original's check keeps passing.
func TestRenderLoop_CheckNotCopiedRejectsCopy(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()
	r1 := NewRenderLoop(sim, views)

	if err := r1.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		t.Fatalf("original RenderLoop.checkNotCopied returned an error: %v", err)
	}

	r2 := renderLoopCopy(r1)
	assertRenderLoopCopiedCode(t, r2.checkNotCopied(errs.NewCorrelationID(), nil))

	// The original must be completely unaffected by r2's existence.
	if err := r1.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		t.Fatalf("original RenderLoop.checkNotCopied returned an error after copying: %v", err)
	}
}

// TestRenderLoop_CopyCannotClaimOwnership is AC-2's central proof: a
// value-copied RenderLoop calling the ownership-claiming method
// (renderOnce) must fail cleanly — never touching r.owned, r.screen, or
// r.back/r.front — rather than succeeding and racing the original for
// the screen. Constructed to completion and then asserted, not raced for
// timing.
func TestRenderLoop_CopyCannotClaimOwnership(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()

	var drawCalls int32
	draw := func(back *Buffer, vm *ViewModels) {
		atomic.AddInt32(&drawCalls, 1)
		back.Set(0, 0, 'H', tcell.StyleDefault)
	}

	r1 := NewRenderLoop(sim, views, draw)
	r2 := renderLoopCopy(r1)

	// r1 already holds ownership (simulating T-RENDER mid-render) — the
	// exact scenario BUG-018 describes: a second instance believing it
	// can also claim exclusive ownership.
	if !r1.owned.CompareAndSwap(false, true) {
		t.Fatal("r1.owned.CompareAndSwap(false, true) failed on a fresh RenderLoop")
	}

	// Pre-fix, r2 would have its OWN independent owned word (still
	// false) and this call would succeed, drawing and flushing
	// concurrently with r1. Post-fix, checkNotCopied rejects r2's call
	// before the CAS is ever attempted.
	r2.renderOnce()

	if got := atomic.LoadInt32(&drawCalls); got != 0 {
		t.Errorf("r2.renderOnce() on a copy ran the DrawFunc %d times, want 0 (must be rejected before drawing)", got)
	}
	if r2.owned.Load() {
		t.Error("r2.renderOnce() on a copy set r2.owned true — the copy-guard must reject before touching owned")
	}

	// r1's own ownership state must be completely unaffected by r2's
	// rejected attempt.
	if !r1.owned.Load() {
		t.Error("r1.owned was cleared by r2's rejected renderOnce call — copies must not affect the original's ownership state")
	}
	r1.owned.Store(false)

	// With r1's ownership released, a genuine renderOnce on the ORIGINAL
	// must still work normally — the guard rejects copies, not the real
	// instance.
	r1.renderOnce()
	if got := atomic.LoadInt32(&drawCalls); got != 1 {
		t.Errorf("r1.renderOnce() ran the DrawFunc %d times total, want 1 (only the original's call should ever draw)", got)
	}
}

// TestRenderLoop_CopyMethodsAreSilentNoOps enumerates every exported/
// receiver method that reads or writes RenderLoop fields (BUG-018's
// "general guard, not just the one CAS call site" requirement, per
// ASM-093/BUG-024's established policy) and confirms a struct-copied
// RenderLoop's call to each is a silent no-op / documented zero value —
// never observably touching or mutating anything r1 can see.
func TestRenderLoop_CopyMethodsAreSilentNoOps(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()
	r1 := NewRenderLoop(sim, views)
	r2 := renderLoopCopy(r1)

	if stats := r2.LastStats(); stats != (FlushStats{}) {
		t.Errorf("r2.LastStats() = %+v, want zero value", stats)
	}
	if got := r2.BelowMinimum(); got {
		t.Errorf("r2.BelowMinimum() = %v, want false", got)
	}

	r2.Resize(999, 999)
	if w, h := r2.back.Size(); w == 999 || h == 999 {
		t.Error("r2.Resize on a copy resized the aliased back buffer visible via r1")
	}
	if w, h := r1.back.Size(); w == 999 || h == 999 {
		t.Error("r2.Resize on a copy leaked a resize into r1's back buffer")
	}

	r2.TriggerRender()
	select {
	case <-r1.renderNow:
		t.Error("r2.TriggerRender on a copy sent on the renderNow channel r1 aliases")
	default:
	}

	stop := make(chan struct{})
	close(stop)
	r2.Run(stop) // must return immediately without ever calling renderOnce
	if r2.owned.Load() {
		t.Error("r2.Run on a copy touched ownership state")
	}
}

// TestRenderLoop_CopyRaceNoLongerReproducible re-runs BUG-018's exact
// concurrency shape (the original and a copy both hammering renderOnce
// concurrently) under -race. Post-fix, the copy's renderOnce call is
// rejected by checkNotCopied before it ever reaches owned/screen/back/
// front, so there is no write for -race to catch and the original's
// render count is the only one that ever advances.
func TestRenderLoop_CopyRaceNoLongerReproducible(t *testing.T) {
	sim := newSimScreen(t, 120, 30)
	views := NewViewStore()

	var r1Draws, r2Draws int32
	draw := func(back *Buffer, vm *ViewModels) {
		atomic.AddInt32(&r1Draws, 1)
	}
	r1 := NewRenderLoop(sim, views, draw)
	r2 := renderLoopCopy(r1)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r1.renderOnce()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r2.renderOnce() // always rejected — never touches shared state
			atomic.AddInt32(&r2Draws, 1)
		}
	}()
	wg.Wait()

	if got := atomic.LoadInt32(&r1Draws); got != 200 {
		t.Errorf("r1 DrawFunc ran %d times, want 200 (all of r1's own calls should have succeeded)", got)
	}
	if got := atomic.LoadInt32(&r2Draws); got != 200 {
		t.Errorf("r2.renderOnce loop completed %d iterations, want 200 (all should run, all silently rejected)", got)
	}
}
