package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// headlessSeamMessage is what -headless prints today (AC-6, supplementary
// — not Sprint-1-gate-blocking per this item's acceptance doc). It exists
// as a distinct constant, checked in run_test.go, so the "seam, not a
// harness" framing has one authoritative source of truth (GR#3) rather
// than a string duplicated between the flag help text and the printed
// message.
const headlessSeamMessage = "metropolis: -headless requested; harness.headless (MOD-015) is not built yet " +
	"(its dependency MOD-012 is done, so it is buildable, just not yet built) " +
	"— this is a flag-dispatch seam only (AC-6), not a working headless harness. " +
	"Run without -headless for the interactive walking skeleton."

// run is main's testable body: parse args, boot, dispatch to the
// interactive or headless-seam path, and return a process exit code
// (0 success, 1 a registry-sourced boot failure per AC-7, 2 a flag-parse
// error). main() itself is just os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) —
// every other case here is exercised directly in tests without needing a
// real terminal or a subprocess.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("metropolis", flag.ContinueOnError)
	fs.SetOutput(stderr)
	headless := fs.Bool("headless", false, "flag-dispatch seam for harness.headless (MOD-015, not yet built); prints a message and exits 0 (AC-6)")
	version := fs.Bool("version", false, "print build identity and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *version {
		_, _ = fmt.Fprintln(stdout, "metropolis", buildinfo.String())
		return 0
	}

	// AC-6's flag-dispatch seam: checked before any wiring boots, so a
	// -headless run today does no engine/transport/registry work at all
	// (nothing to shut down) — exactly what "seam, not a harness" means.
	// Once harness.headless (MOD-015) lands, this becomes
	// `if *headless { return runHeadless(...) }`, calling into that
	// module rather than printing a message.
	if *headless {
		_, _ = fmt.Fprintln(stdout, headlessSeamMessage)
		return 0
	}

	correlationID := errs.NewCorrelationID()

	w, err := bootCore(correlationID, newBootRegistry())
	if err != nil {
		printBootError(stderr, err)
		return 1
	}

	screen, err := core.NewScreen(correlationID)
	if err != nil {
		// AC-7: a boot-time failure after some components already started
		// must still exit non-zero without ever rendering a partial/blank
		// screen — shut the already-started engine/views goroutines down
		// cleanly rather than leaking them, then fail loudly.
		w.shutdown()
		printBootError(stderr, err)
		return 1
	}

	runInteractive(w, screen)
	w.shutdown()
	return 0
}

// printBootError renders a boot failure the GR#1 way: if it's a
// registry-sourced *errs.E (every boot failure in this package is, per
// bootCore/core.NewScreen), print its Display() one-liner (code + message
// + correlation ID); otherwise (defensive — should be unreachable given
// GR#7) fall back to a plain error line rather than losing the failure
// entirely.
func printBootError(stderr io.Writer, err error) {
	var e *errs.E
	if errors.As(err, &e) {
		_, _ = fmt.Fprintln(stderr, e.Display())
		return
	}
	_, _ = fmt.Fprintln(stderr, "metropolis: boot failed:", err)
}

// runInteractive wires ui.core's InputLoop/RenderLoop over screen and
// blocks until a quit key is observed (isQuitInput) or the screen itself
// finalizes, then tears the screen down. w's engine/protocol/views
// goroutines are left running for the caller to shut down afterward (run
// calls w.shutdown() once this returns) — this function owns only the
// screen-dependent half of the skeleton (AC-3's single-goroutine tcell
// ownership rule: RenderLoop is that one goroutine).
func runInteractive(w *skeletonWiring, screen tcell.Screen) {
	inputLoop := core.NewInputLoop(screen, 32)
	renderLoop := core.NewRenderLoop(screen, w.viewStore, mapDrawFunc(w.mapScreen))

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	inputLoop.OnDelivered(func(msg core.InputMsg) {
		renderLoop.TriggerRender()
		if isQuitInput(msg) {
			closeStop()
		}
	})

	// renderDone/inputDone are tracked separately (not one shared
	// WaitGroup) so shutdown ordering is exact: RenderLoop must have
	// fully returned from its last renderOnce before screen.Fini() runs
	// (AC-3's single-goroutine tcell rule — Fini itself touches the
	// screen, so it may never race a still-in-flight render), and
	// InputLoop's blocked PollEvent is only unblocked BY Fini, so it must
	// be waited on after.
	var renderDone, inputDone sync.WaitGroup
	renderDone.Add(1)
	go func() { defer renderDone.Done(); renderLoop.Run(stop) }()
	inputDone.Add(1)
	go func() { defer inputDone.Done(); inputLoop.Run(stop) }()

	<-stop
	renderDone.Wait()
	screen.Fini() // unblocks InputLoop's PollEvent so its goroutine exits
	inputDone.Wait()
}

// isQuitInput recognises the walking skeleton's process-lifecycle quit
// keys: Ctrl+C, Esc, or 'q'. This is NOT the AC-5b key-input-to-Command
// translation this item's acceptance doc explicitly bans ("no ad hoc key
// handling inside cmd/metropolis... that bypasses ui.keys") — it never
// constructs, validates, or sends a protocol.Command; it only decides
// whether to stop the render/input loops and let this process exit, the
// same "how do I close this TUI" concern every tcell program needs
// regardless of what game/protocol it speaks. Binding an actual in-game
// key (pause, pan, inspect, ...) to a Command remains ui.keys' (MOD-011)
// job alone, untouched here.
func isQuitInput(msg core.InputMsg) bool {
	if msg.Kind != core.KeyInput {
		return false
	}
	return msg.Key == tcell.KeyCtrlC || msg.Key == tcell.KeyEscape || msg.Rune == 'q'
}
