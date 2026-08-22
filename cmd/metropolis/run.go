package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// run is main's testable body: parse args, boot, dispatch to the
// interactive or headless-seam path, and return a process exit code
// (0 success, 1 a registry-sourced boot failure per AC-7, 2 a flag-parse
// error). main() itself is just os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) —
// every other case here is exercised directly in tests without needing a
// real terminal or a subprocess.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("metropolis", flag.ContinueOnError)
	fs.SetOutput(stderr)
	headlessMode := fs.Bool("headless", false, "run engine.core headlessly (harness.headless, MOD-015): no UI attached, requires -seed/-months/-out")
	version := fs.Bool("version", false, "print build identity and exit")
	hf := registerHeadlessFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *version {
		_, _ = fmt.Fprintln(stdout, "metropolis", buildinfo.String())
		return 0
	}

	// AC-6 (feat.skeleton): -headless dispatches into harness.headless
	// (MOD-015) directly — this binary does no engine/transport/registry
	// boot work of its own beyond what runHeadless does via that package.
	if *headlessMode {
		return runHeadless(fs, hf, stdout, stderr)
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
		logEngineShutdown(stderr, w.EngineRunErr())
		printBootError(stderr, err)
		return 1
	}

	runInteractive(w, screen)
	w.shutdown()
	logEngineShutdown(stderr, w.EngineRunErr())
	return 0
}

// logEngineShutdown observes core.Engine.RunCommandLoop's return value
// (BUG-020) and reports it distinctly from a clean, intentional shutdown,
// rather than silently discarding it the way `_ = engine.Run(ctx)` used
// to. Callers must call this only after w.shutdown() has returned — see
// skeletonWiring.engineRunErr's doc comment (boot.go) for why reading the
// value any earlier races the Run goroutine.
//
// A clean shutdown resolves to nil (RunCommandLoop returns nil on ctx
// cancellation — the normal cancel(); wg.Wait(); Close() path) and is
// deliberately NOT logged: logging it would just be noise on every
// ordinary exit. Anything else — in practice, core.ErrPrematureCommandsClose
// (MET-E014, internal/engine/core/errors.go) — means Commands() closed out
// from under the loop while ctx was still live, which BUG-020's own doc
// comment says never happens under today's cancel-before-close wiring, so
// seeing it here means that invariant broke; it must be visible on
// stderr, not swallowed.
func logEngineShutdown(stderr io.Writer, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	var e *errs.E
	if errors.As(err, &e) {
		_, _ = fmt.Fprintln(stderr, "metropolis: engine loop exited abnormally:", e.Display())
		return
	}
	_, _ = fmt.Fprintln(stderr, "metropolis: engine loop exited abnormally:", err)
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
// blocks until a quit key is observed (isQuitInput — but see OnDelivered
// below for why Esc defers to a pending grammar sequence first) or the
// screen itself finalizes, then tears the screen down. It also runs the
// UI-tick idle-timeout driver (runIdleTimeouts) for the same lifetime.
//
// w's engine/protocol/views
// goroutines are left running for the caller to shut down afterward (run
// calls w.shutdown() once this returns) — this function owns only the
// screen-dependent half of the skeleton (AC-3's single-goroutine tcell
// ownership rule: RenderLoop is that one goroutine).
//
// FEAT-211 increment 1: RenderLoop no longer draws the hardcoded
// mapDrawFunc(w.mapScreen) literal — it draws through w.screens
// (core.ScreenRegistry), one indirection closure that calls
// w.screens.ActiveDraw() fresh every tick (design §7(c): "RenderLoop's
// draw loop changes from ranging over all DrawFuncs to calling exactly
// the active one"). Passing exactly ONE DrawFunc into NewRenderLoop
// achieves that without touching render.go at all — render.go's
// renderOnce already calls each of r.draws exactly once per tick; with
// only this one indirection entry, that is already "call exactly the
// active screen's Draw," sourced dynamically from whichever screen
// w.screens.Activate last selected (a pointer swap, design §7(d) — no
// re-subscribe, no reconstruction, so this closure's own cost per tick
// never grows with how many screens are registered).
// composeDraw builds the ONE core.DrawFunc runInteractive hands to
// NewRenderLoop: the active screen draws first, then BUG-322's status line is
// overlaid on the last row.
//
// A named function rather than a closure literal inside runInteractive
// purely so it is testable: runInteractive itself needs a live tcell.Screen
// and blocks until a quit key, so a status bar wired only in there could be
// deleted without a single test going red — which is precisely the
// "nothing on screen looks the same as nothing running" failure BUG-322 is
// about. See TestBUG322_ComposedDrawIncludesTheStatusLine.
//
// Order is the contract: screen first, clock chrome on top, so the overlay
// always wins the last row no matter what the active screen drew there.
func composeDraw(w *skeletonWiring) core.DrawFunc {
	statusDraw := statusBarDraw(w.statusBar)
	return func(back *core.Buffer, vm *core.ViewModels) {
		w.screens.ActiveDraw()(back, vm)
		statusDraw(back, vm)
	}
}

func runInteractive(w *skeletonWiring, screen tcell.Screen) {
	inputLoop := core.NewInputLoop(screen, 32)
	renderLoop := core.NewRenderLoop(screen, w.viewStore, composeDraw(w))

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	inputLoop.OnDelivered(func(msg core.InputMsg) {
		renderLoop.TriggerRender()
		// Esc is BOTH this binary's process-lifecycle quit key and
		// keys.KeyGrammar's reserved, unconditional abort token
		// (grammar.go's reservedTokens/computeFeed step 1 — Register and
		// RegisterGlobal refuse to bind it precisely so it is always
		// available to cancel). Before FEAT-211 increment 1 that clash was
		// invisible, because no leader sequence was reachable from a real
		// keyboard at all; making services' "s f +" reachable made Esc's
		// documented "cancel what I mistyped" behaviour unreachable
		// instead — isQuitInput is checked first, so Esc killed the
		// process with the half-typed sequence still pending (FEAT-211
		// increment 1 destructive round, finding 2).
		//
		// Resolution: abort wins while anything is pending, quit wins
		// otherwise. This is the ordering a player already expects from
		// every modal editor — the first Esc backs out of what you are in
		// the middle of, the next one leaves — and it costs the quit path
		// nothing, because a pending sequence is by definition a state the
		// player entered deliberately one keystroke ago. Ctrl+C and 'q'
		// are untouched: neither is a grammar token, so neither has an
		// abort meaning to compete with, and Ctrl+C in particular must
		// stay an unconditional kill.
		if isEscInput(msg) && anyGrammarPending(w) {
			routeKeyInput(w, msg)
			return
		}
		if isQuitInput(msg) {
			closeStop()
			return
		}
		routeKeyInput(w, msg)
	})

	// renderDone/inputDone are tracked separately (not one shared
	// WaitGroup) so shutdown ordering is exact: RenderLoop must have
	// fully returned from its last renderOnce before screen.Fini() runs
	// (AC-3's single-goroutine tcell rule — Fini itself touches the
	// screen, so it may never race a still-in-flight render), and
	// InputLoop's blocked PollEvent is only unblocked BY Fini, so it must
	// be waited on after.
	var renderDone, inputDone, idleDone sync.WaitGroup
	renderDone.Add(1)
	go func() { defer renderDone.Done(); renderLoop.Run(stop) }()
	inputDone.Add(1)
	go func() { defer inputDone.Done(); inputLoop.Run(stop) }()
	idleDone.Add(1)
	go func() { defer idleDone.Done(); runIdleTimeouts(w, renderLoop, stop) }()

	<-stop
	renderDone.Wait()
	screen.Fini() // unblocks InputLoop's PollEvent so its goroutine exits
	inputDone.Wait()
	idleDone.Wait()
}

// runIdleTimeouts drives keys.KeyGrammar.CheckIdleTimeout on the UI tick
// (FEAT-211 increment 1 destructive round, finding 4). ui.keys ships a
// leader-sequence inactivity timeout (AC-2d, keys.DefaultIdleTimeout =
// 2s) that is explicitly poll-driven — "never a blocking sleep or its own
// timer goroutine (T-INPUT's non-blocking contract)", CheckIdleTimeout's
// own doc comment, "a caller drives this from whatever tick it already
// has". Before this, NOTHING in the live binary called it, so the timeout
// was dead code and a half-typed prefix was immortal: the player's only
// escape was to complete or mis-complete the sequence.
//
// It ticks at core.RenderTick — the UI's own 10 Hz cadence (UI-SPEC §1) —
// so the abort is observed within one frame of expiring, and requests a
// render whenever it actually cancels something, since the which-key HUD
// that will read Continuations() must not keep showing a prefix that no
// longer exists.
//
// Why a goroutine on the render tick rather than inside the draw closure
// runInteractive passes to NewRenderLoop, which would be the render tick
// literally: core.DrawFunc's contract forbids exactly that ("Draw
// callbacks must only write to back", render.go). Smuggling grammar
// mutation into a draw callback would trade one silent-failure defect for
// a violated invariant in the single-goroutine screen owner, so this
// takes the same 10 Hz from its own goroutine instead. Cost is a ticker
// and, per tick, at most two mutex-guarded time comparisons; the
// grammars' own locks make it safe alongside T-INPUT's Feed calls.
//
// Only chromeGrammar and the ACTIVE screen's grammar are polled — not
// every registered screen's — because an inactive screen can no longer
// hold pending state: ScreenRegistry.Activate aborts the outgoing
// screen's grammar on every switch (finding 1's fix, screen_registry.go).
// Those two fixes are complements, not alternatives: the abort covers
// "the player left", this covers "the player stopped".
func runIdleTimeouts(w *skeletonWiring, renderLoop *core.RenderLoop, stop <-chan struct{}) {
	ticker := time.NewTicker(core.RenderTick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			aborted := false
			if w.chromeGrammar != nil && w.chromeGrammar.CheckIdleTimeout() {
				aborted = true
			}
			if g := w.screens.ActiveGrammar(); g != nil && g.CheckIdleTimeout() {
				aborted = true
			}
			if aborted {
				renderLoop.TriggerRender()
			}
		}
	}
}

// anyGrammarPending reports whether ANY grammar this binary feeds is
// mid-sequence (a leader prefix or a count prefix). Both are consulted,
// in the same order routeKeyInput feeds them, because both can hold
// pending state: chromeGrammar accumulates count digits too (see
// routeKeyInput's doc comment on the double-feed), so "Esc should abort,
// not quit" must be true for a prefix pending on either one.
func anyGrammarPending(w *skeletonWiring) bool {
	if w.chromeGrammar != nil && w.chromeGrammar.IsPending() {
		return true
	}
	if g := w.screens.ActiveGrammar(); g != nil && g.IsPending() {
		return true
	}
	return false
}

// isEscInput reports whether msg is the Escape key specifically —
// isQuitInput's Esc arm, split out so runInteractive can ask "is this the
// grammar's abort token?" separately from "is this a quit key?". See
// OnDelivered's own comment for why the two must be distinguished.
func isEscInput(msg core.InputMsg) bool {
	return msg.Kind == core.KeyInput && msg.Key == tcell.KeyEscape
}

// routeKeyInput is FEAT-211 increment 1's input routing (design §7(b)):
// sequential, first-dispatcher-wins — chrome/global grammar first, the
// ACTIVE screen's own grammar second.
//
// # What "single consumer" does and does not mean here (corrected)
//
// This comment previously claimed the two grammars NEVER both see the
// same keystroke, and design §7(b) says "exactly one Feed per keystroke".
// That is not what this function does, and the destructive round on this
// increment was right to call it out (finding 3). What is actually true:
//
//   - At most one grammar DISPATCHES an action for a keystroke. If chrome
//     dispatches (GlobalDispatched/Dispatched) routing stops there, so no
//     key can fire a chrome global and a screen action at once. That is
//     the guarantee that matters for correctness, and it holds.
//   - Every other key IS fed to both. Feed is not a pure query — a digit
//     at idle accumulates a count prefix (grammar.go step 5), so one "3"
//     leaves chromeGrammar AND the active screen's grammar pending.
//
// Today that is harmless: chromeGrammar has only F-key globals, no leader
// paths and no count-consuming action, so its accumulated digits can
// never resolve to anything — and they are no longer immortal: nothing on
// chromeGrammar can consume its own count prefix, so runIdleTimeouts is
// what clears it, 2s after the last keystroke.
//
// # The layering decision UI-SPEC §3's speed globals will need
//
// It stops being harmless the moment chromeGrammar gains a global that
// takes a count. UI-SPEC §3's planned 1/2/3 speed keys are exactly that:
// with them registered as chrome globals, "3" would set speed AND leave a
// count prefix on the screen grammar, and "1 2" typed as a count for a
// screen action would change speed twice on the way.
//
// The decision, recorded here for whoever builds them (this increment
// deliberately does NOT build the speed globals): make the fall-through
// conditional on the chrome grammar having no interest in the key, rather
// than on it having failed to dispatch. Concretely, feed the screen
// grammar only when chrome's FeedResult is NoSuchSequence or NoOp — the
// two statuses that mean "chrome consumed nothing and retained nothing" —
// and stop on Pending as well as on the two dispatch statuses. That makes
// the "exactly one Feed per keystroke" claim in §7(b) true as written,
// and it is a two-line change here; it is not made now only because with
// no leader paths and no counted globals on chromeGrammar, Pending there
// is unreachable except for digits, and changing the digit path today
// would silently take count prefixes away from screen actions that
// already use them (services' "3 s f +"). Whoever lands the speed globals
// must make both changes in the same commit.
//
// This is the ui.keys AC-20/AC-21 seam
// (keys.FromTcellEvent, "the ONE conversion point between tcell and this
// package's own grammar state") applied here exactly once per real
// keystroke; it constructs no protocol.Command itself (that remains each
// screen's own Action.Run closure's job, e.g.
// servicesscreen.RegisterFundingAdjustKeys' countAdjust).
//
// Only core.KeyInput messages carrying a real *tcell.EventKey (msg.Raw)
// are routed at all — mouse/resize/other InputMsg kinds have no key
// grammar concept and are silently not fed (not an error: OtherInput/
// MouseInput/ResizeInput were never routable to begin with, mirroring
// isQuitInput's own Kind guard immediately below).
//
// Step 1: feed w.chromeGrammar (F1/F2/F4's switch globals, boot.go). A
// GlobalDispatched or Dispatched result means SOMETHING fired for this
// keystroke already (a global always fires standalone in this grammar —
// no leader paths are ever Register()ed on it, only RegisterGlobal — so
// Dispatched is defensive, never actually reachable today) — stop here:
// the active screen's grammar must not ALSO act on a key chrome already
// acted on.
//
// Step 2: otherwise (Pending — e.g. an accumulating count-prefix digit
// that never resolves to any real chrome action, since chromeGrammar has
// no registered leader paths — NoSuchSequence, Aborted, or NoOp), feed
// w.screens.ActiveGrammar() if the currently active screen registered
// one (nil is legal — see ScreenEntry's own doc comment; map/finance
// register no Grammar in this increment, only services does). Note this
// is a SECOND Feed of the same key, with the state consequences the
// section above spells out; Aborted in particular is deliberately on this
// list, so one Esc clears a pending prefix on both grammars at once.
func routeKeyInput(w *skeletonWiring, msg core.InputMsg) {
	if msg.Kind != core.KeyInput {
		return
	}
	ev, ok := msg.Raw.(*tcell.EventKey)
	if !ok {
		return
	}
	k := keys.FromTcellEvent(ev)

	if res := w.chromeGrammar.Feed(k); res.Status == keys.GlobalDispatched || res.Status == keys.Dispatched {
		return
	}

	if g := w.screens.ActiveGrammar(); g != nil {
		g.Feed(k)
	}
}

// isQuitInput recognises the walking skeleton's process-lifecycle quit
// keys: Ctrl+C, Esc, or 'q'. Esc's answer here is unconditional by
// design — this predicate answers "is this a quit key", not "should we
// quit right now"; the pending-sequence override lives at the one call
// site that has the grammars to ask (runInteractive's OnDelivered), so
// this stays a pure function of the message and its table test stays
// meaningful. This is NOT the AC-5b key-input-to-Command
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
