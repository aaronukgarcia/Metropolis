package uitest

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// These four tests are AC-10/AC-11 (UI-SPEC §5, M0-ENG §6 point 5):
// CI-runnable, hard-failing (t.Fatalf, never t.Log) latency assertions
// for the budgets doc.go transcribes. time.Now/time.Since appear ONLY in
// this file among this package's non-test sources (grep -rn
// "time\.Now|time\.Since" internal/harness/uitest/*.go, excluding
// _test.go, returns nothing — GR#21) — these tests are explicitly timing
// UI-SPEC §5's own wall-clock budgets, not gating correctness on wall
// time.

// TestLatencyKeystrokeEcho: keystroke -> echo must be < 10ms.
func TestLatencyKeystrokeEcho(t *testing.T) {
	echoCh := make(chan time.Time, 1)
	h := NewHarness(errs.NewCorrelationID(), func(msg uicore.InputMsg) {
		if msg.Kind == uicore.KeyInput {
			select {
			case echoCh <- time.Now():
			default:
			}
		}
	})
	defer h.Stop()

	start := time.Now()
	if err := h.SendKeys("b"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	select {
	case got := <-echoCh:
		if d := got.Sub(start); d > 10*time.Millisecond {
			t.Fatalf("keystroke echo latency = %v, want < 10ms (UI-SPEC §5)", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no echo observed within 1s")
	}
}

// TestLatencyScreenSwitch: switching a Harness's DrawFuncs (the
// headless equivalent of a real UI switching which screen is on top)
// through to a completed Render() must be < 30ms.
func TestLatencyScreenSwitch(t *testing.T) {
	h := NewHarness(errs.NewCorrelationID(), nil)
	defer h.Stop()
	if _, err := h.Render(); err != nil { // warm up
		t.Fatalf("Render (warm-up): %v", err)
	}

	start := time.Now()
	if err := h.SetDraws(func(back *uicore.Buffer, _ *uicore.ViewModels) {
		back.Set(0, 0, 'X', tcell.StyleDefault)
	}); err != nil {
		t.Fatalf("SetDraws: %v", err)
	}
	if _, err := h.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Fatalf("screen switch latency = %v, want < 30ms (UI-SPEC §5)", d)
	}
}

// TestLatencyDiffFlushTypical: a full-terminal diff flush with a small,
// "typical" changed region must be < 3ms.
func TestLatencyDiffFlushTypical(t *testing.T) {
	back := uicore.NewBuffer(uicore.MinCols, uicore.MinRows)
	front := uicore.NewBuffer(uicore.MinCols, uicore.MinRows)
	uicore.Flush(nopWriter{}, back, front) // prime front == back

	back.Set(5, 5, 'X', tcell.StyleDefault) // one small, typical change

	start := time.Now()
	uicore.Flush(nopWriter{}, back, front)
	if d := time.Since(start); d > 3*time.Millisecond {
		t.Fatalf("typical diff flush latency = %v, want < 3ms (UI-SPEC §5)", d)
	}
}

// TestLatencyDiffFlushWorst: a full-terminal diff flush covering every
// cell (the worst case UI-SPEC §5 names explicitly: "on resize", where
// the entire buffer necessarily differs) must be < 8ms.
func TestLatencyDiffFlushWorst(t *testing.T) {
	back := uicore.NewBuffer(uicore.MinCols, uicore.MinRows)
	front := uicore.NewBuffer(uicore.MinCols, uicore.MinRows)
	uicore.Flush(nopWriter{}, back, front) // prime front == back

	back.Fill('X', tcell.StyleDefault) // every cell changes: the resize/worst case

	start := time.Now()
	uicore.Flush(nopWriter{}, back, front)
	if d := time.Since(start); d > 8*time.Millisecond {
		t.Fatalf("worst-case diff flush latency = %v, want < 8ms (UI-SPEC §5)", d)
	}
}
