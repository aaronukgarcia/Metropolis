package core

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// countingWriter is a ScreenWriter test double that never touches a real
// or simulated terminal, so Flush's own behaviour is isolated from
// tcell's internals (AC-1, AC-7).
type countingWriter struct {
	setCalls  int
	showCalls int
	clearHit  bool // never set — see the type's method set; kept only to
	// document that ScreenWriter has no Clear/Fill method to hit.
}

func (w *countingWriter) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	w.setCalls++
}
func (w *countingWriter) Show() { w.showCalls++ }

func TestFlush_OnlyChangedCellsProportional(t *testing.T) {
	const w, h = 40, 10
	front := NewBuffer(w, h)
	back := NewBuffer(w, h)

	cw := &countingWriter{}
	// First flush: back and front are identical (both blank) -> nothing
	// to write.
	stats := Flush(cw, back, front)
	if stats.CellsChanged != 0 || cw.setCalls != 0 {
		t.Fatalf("identical buffers: got %d changed / %d SetContent calls, want 0/0", stats.CellsChanged, cw.setCalls)
	}
	if cw.showCalls != 0 {
		t.Fatalf("Show should not be called when nothing changed, got %d calls", cw.showCalls)
	}

	// Change a handful of cells in back only.
	changed := []struct{ x, y int }{{1, 1}, {2, 1}, {3, 1}, {10, 5}}
	for _, c := range changed {
		back.Set(c.x, c.y, 'X', tcell.StyleDefault)
	}

	stats = Flush(cw, back, front)
	if stats.CellsChanged != len(changed) {
		t.Fatalf("CellsChanged = %d, want %d", stats.CellsChanged, len(changed))
	}
	if cw.setCalls != len(changed) {
		t.Fatalf("SetContent called %d times, want %d (proportional to changed region, not %d)", cw.setCalls, len(changed), w*h)
	}
	// {1,1},{2,1},{3,1} form one contiguous run; {10,5} is a second run.
	if stats.Runs != 2 {
		t.Fatalf("Runs = %d, want 2", stats.Runs)
	}
	if cw.showCalls != 1 {
		t.Fatalf("Show called %d times, want 1", cw.showCalls)
	}

	// A third flush with no further changes should again be a no-op.
	cw2 := &countingWriter{}
	stats = Flush(cw2, back, front)
	if stats.CellsChanged != 0 || cw2.setCalls != 0 || cw2.showCalls != 0 {
		t.Fatalf("post-sync flush should be a no-op, got stats=%+v setCalls=%d showCalls=%d", stats, cw2.setCalls, cw2.showCalls)
	}
}

// TestFlush_NoClearInvariant asserts, at the type level, that Flush
// cannot call Clear or Fill on the screen: ScreenWriter simply has no
// such method, so nothing calling through that interface can ever issue
// one. This test exercises Flush across a full-buffer change (the worst
// case, e.g. a resize) and confirms every emitted write is a targeted
// SetContent call, never a clear-and-redraw.
func TestFlush_NoClearInvariant(t *testing.T) {
	const w, h = 20, 5
	front := NewBuffer(w, h)
	back := NewBuffer(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			back.Set(x, y, 'Z', tcell.StyleDefault)
		}
	}
	cw := &countingWriter{}
	stats := Flush(cw, back, front)
	if stats.CellsChanged != w*h {
		t.Fatalf("CellsChanged = %d, want %d (full-buffer change)", stats.CellsChanged, w*h)
	}
	if cw.setCalls != w*h {
		t.Fatalf("SetContent calls = %d, want %d", cw.setCalls, w*h)
	}
	if cw.showCalls != 1 {
		t.Fatalf("Show calls = %d, want 1", cw.showCalls)
	}
	// No Clear/Fill call is even expressible through ScreenWriter, so
	// clearHit can never become true — asserted for documentation.
	if cw.clearHit {
		t.Fatal("impossible: ScreenWriter has no Clear/Fill method")
	}
}

// nopWriter performs no work at all, so BenchmarkFlush_ZeroAlloc
// measures only this package's own allocation behaviour, not tcell's.
type nopWriter struct{}

func (nopWriter) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {}
func (nopWriter) Show()                                                                  {}

func BenchmarkFlush_ZeroAlloc(b *testing.B) {
	const w, h = 200, 60 // UI-SPEC §1's reference "~12,000-cell screen"
	front := NewBuffer(w, h)
	back := NewBuffer(w, h)
	nw := nopWriter{}

	// Steady-state: a small, stable set of cells toggles each round,
	// matching UI-SPEC §1's "a typical sim-tick update touches a few
	// hundred cells."
	style := tcell.StyleDefault
	toggle := byte('A')

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for y := 0; y < 10; y++ {
			for x := 0; x < 30; x++ {
				back.Set(x, y, rune(toggle), style)
			}
		}
		toggle++
		Flush(nw, back, front)
	}
}
