package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// BUG-020: the engine command loop itself already distinguishes a
// premature Commands() closure from a clean ctx cancellation. StubEngine
// raised codePrematureCommandsClose (MET-P094); the real core.Engine's
// RunCommandLoop raises core.ErrPrematureCommandsClose (MET-E014) — the
// same shape, now fixed in engine.core itself (internal/engine/core/
// bug020-era coverage lives in engine/core's own tests). What remained
// silent was this package: bootCore's goroutine ran `_ = engine.Run(ctx)`,
// discarding the return value outright, so nothing here ever observed
// which of the two happened.
//
// The fix: skeletonWiring.engineRunErr (boot.go) captures the loop's
// return value, EngineRunErr() exposes it once shutdown() has returned,
// and run.go's logEngineShutdown reports it distinctly — silent on a
// clean ctx-cancellation exit, loud on anything else.
//
// These tests exercise skeletonWiring/logEngineShutdown directly (not
// through run(), which owns its own ctx/cancel internally and offers no
// seam to force a premature close) so both halves of the distinction are
// provable without relying on process-lifecycle timing.

// TestSkeletonWiring_CleanShutdown_EngineRunErrIsNil proves the ordinary
// path — cancel(); wg.Wait(); Close(), exactly what shutdown() does —
// leaves EngineRunErr() holding nil (core.Engine.RunCommandLoop returns
// nil on a clean ctx-cancelled shutdown, where StubEngine.Run returned
// ctx.Err()), and that logEngineShutdown stays silent for it: an
// intentional quit must never be reported as if something broke.
func TestSkeletonWiring_CleanShutdown_EngineRunErrIsNil(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("bug020-clean", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}

	w.shutdown()

	got := w.EngineRunErr()
	if got != nil {
		t.Fatalf("EngineRunErr() = %v, want nil (RunCommandLoop's clean ctx-cancellation return)", got)
	}

	var buf bytes.Buffer
	logEngineShutdown(&buf, got)
	if buf.Len() != 0 {
		t.Fatalf("logEngineShutdown wrote %q for a clean shutdown, want silence", buf.String())
	}
}

// TestSkeletonWiring_PrematureClose_EngineRunErrIsDistinguishable
// reproduces BUG-020's actual defect shape at this package's level: the
// transport closing before ctx is ever cancelled (an early Close(),
// exactly the "future bug, copied transport, early Close() elsewhere"
// scenario the BOW item names). Before this fix, boot.go's
// `_ = engine.Run(ctx)` meant nothing here could ever tell this apart
// from a clean exit. Now EngineRunErr() surfaces the registry-sourced
// codePrematureCommandsClose error, is NOT ctx.Canceled/DeadlineExceeded-
// shaped, and logEngineShutdown reports it on stderr instead of staying
// silent.
func TestSkeletonWiring_PrematureClose_EngineRunErrIsDistinguishable(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("bug020-premature", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}

	// The premature close: close the transport directly, WITHOUT going
	// through shutdown()/cancel() first — mirrors the BUG-020 premature-
	// close shape, exercised here through the real boot-time wiring
	// (core.Engine.RunCommandLoop) instead of a hand-built engine/transport
	// pair.
	if err := w.transport.Close(); err != nil {
		t.Fatalf("transport.Close: %v", err)
	}

	// Wait for the Run goroutine specifically (engineDone), NOT
	// shutdown()/wg.Wait() — cancelling ctx before Run's select has
	// observed the closed Commands() channel would reopen exactly the
	// cancel-vs-close race Run's own doc comment (SEC-026) warns about,
	// making this test flaky instead of deterministic.
	select {
	case <-w.engineDone:
	case <-time.After(2 * time.Second):
		t.Fatal("engine.Run did not return after the transport closed out from under it")
	}

	// Now safe to tear the rest of the wiring down (this cancels ctx,
	// which stops viewsLoop, and waits for both goroutines) —
	// engineRunErr was already settled, and happens-before-safe to read,
	// the instant engineDone closed above.
	w.shutdown()

	got := w.EngineRunErr()
	if got == nil {
		t.Fatal("EngineRunErr() = nil for a premature Commands() closure — indistinguishable from a clean exit, the exact defect BUG-020 exists to prevent")
	}
	if errors.Is(got, context.Canceled) || errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("EngineRunErr() = %v resolves to a ctx-cancellation shape for a premature close, want codePrematureCommandsClose", got)
	}

	var buf bytes.Buffer
	logEngineShutdown(&buf, got)
	if buf.Len() == 0 {
		t.Fatal("logEngineShutdown stayed silent for a premature Commands() closure — this is exactly the silent-discard BUG-020 reports (boot.go used to run `_ = engine.Run(ctx)`)")
	}
	if !strings.Contains(buf.String(), "engine loop exited abnormally") {
		t.Fatalf("logEngineShutdown output = %q, want it to flag the abnormal engine exit distinctly", buf.String())
	}
}
