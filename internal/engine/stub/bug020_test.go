package stub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-020 (found by a Destructive agent during wave 1): Run returned a
// bare nil the instant s.transport.Commands() reported ok=false —
// indistinguishable from the clean, intentional-quit exit (ctx
// cancellation), even though a premature closure is a very different
// event (cmd/metropolis/boot.go discards Run's return entirely: `_ =
// engine.Run(ctx)`). Nothing observed a premature exit; only
// shutdown()'s wg.Wait() inspects completion, and only on an
// intentional quit.
//
// Latent today: boot.go's shutdown ordering (cancel(); wg.Wait();
// Close()) always cancels ctx before the transport closes, so this
// window never opens under current wiring. But if Commands() ever
// closed early — a future bug, a copied transport handed to
// NewStubEngine, an early Close() somewhere else — the engine loop
// would vanish silently.
//
// The fix (Run, engine.go): a Commands() closure observed while ctx is
// STILL LIVE (not Done) now returns codePrematureCommandsClose, a
// registry-sourced error (codes.go/data/errors.json), instead of nil.
// ctx cancellation — including the case where ctx happens to be done by
// the time the closure is observed — still resolves to a clean
// ctx.Err(), unchanged from before this fix.
//
// This test reproduces the premature-close window deterministically:
// close the transport directly (never cancelling ctx), which is exactly
// "an early Close()" from Run's Task 2 brief. No timing hammer, no
// probabilistic race — Close() is synchronous and Commands() is
// observably closed the instant it returns.
func TestStubEngine_Run_PrematureCommandsClose_ReturnsRegistryError(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
	eng, err := NewStubEngine(tr)
	if err != nil {
		t.Fatalf("NewStubEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ctx is deliberately NEVER cancelled before Run observes the close below

	runDone := make(chan error, 1)
	go func() { runDone <- eng.Run(ctx) }()

	// The premature close: the transport closes (so Commands() reports
	// ok=false) with ctx still fully live. This is BUG-020's "an early
	// Close()" scenario, reproduced directly rather than waited for.
	if err := tr.Close(); err != nil {
		t.Fatalf("tr.Close(): %v", err)
	}

	select {
	case got := <-runDone:
		if got == nil {
			t.Fatal("Run returned nil on a premature Commands() closure with ctx still live — indistinguishable from a clean ctx.Err() exit, the exact BUG-020 defect")
		}
		if !errors.Is(got, &errs.E{Code: codePrematureCommandsClose}) {
			t.Fatalf("Run() error = %v, want codePrematureCommandsClose (%s)", got, codePrematureCommandsClose)
		}
		if errors.Is(got, context.Canceled) || errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, must NOT resolve to a ctx-cancellation-shaped error for a premature close", got)
		}
	case <-time.After(testTimeout):
		t.Fatal("Run did not return after the transport closed out from under it")
	}
}

// TestStubEngine_Run_CtxCancel_ReturnsCleanNil proves the OTHER half of
// BUG-020's fix did not regress: an intentional ctx cancellation (the
// normal shutdown path, cancel() before the transport is ever touched)
// still returns a clean, non-error exit — Run must not start reporting
// every shutdown as an error just because it now distinguishes the
// premature case.
func TestStubEngine_Run_CtxCancel_ReturnsCleanNil(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
	t.Cleanup(func() { _ = tr.Close() })

	eng, err := NewStubEngine(tr)
	if err != nil {
		t.Fatalf("NewStubEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan error, 1)
	go func() { runDone <- eng.Run(ctx) }()

	// Intentional shutdown: cancel ctx FIRST (mirrors boot.go's
	// cancel(); wg.Wait(); Close() ordering — the transport is not
	// closed here at all, isolating this test to ctx cancellation
	// alone).
	cancel()

	select {
	case got := <-runDone:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("Run() error after ctx cancellation = %v, want context.Canceled (ctx.Err())", got)
		}
		if errors.Is(got, &errs.E{Code: codePrematureCommandsClose}) {
			t.Fatal("Run() reported codePrematureCommandsClose for an intentional ctx cancellation — the two exit paths must stay distinguishable in both directions")
		}
	case <-time.After(testTimeout):
		t.Fatal("Run did not return after ctx was cancelled")
	}
}
