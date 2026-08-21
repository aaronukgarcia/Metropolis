package main

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-211 increment 1 — INDEPENDENT DESTRUCTIVE ROUND (GR#23), routing
// half. Written by an attacker who did not write boot.go/run.go's
// FEAT-211 changes. Registry-internal attacks live in
// internal/ui/core/screen_registry_destructive_test.go.

// --- Axis 2: pending-sequence / key routing ----------------------------

// TestRouting_AbandonedSequenceIsAbortedOnSwitch is FINDING 1 of this
// round, INVERTED after the fix (ScreenRegistry.Activate now calls
// Abort() on the outgoing screen's grammar — internal/ui/core/
// screen_registry.go).
//
// As originally written this test asserted the DEFECT: a leader sequence
// half-typed on the services screen survived switching away, so after
// visiting another screen and coming back, a SINGLE "+" keystroke
// completed the abandoned "s f" prefix and sent a real funding-adjust
// command to the engine (the engine answered MET-G1202, "service
// clinic-1 was never registered"), even though on that visit the player
// had pressed exactly one key that is not bound to anything on its own.
// It now asserts the opposite: the prefix is gone the moment the player
// leaves, and the later "+" is as inert as it would be on a fresh boot.
//
// The control half (a fresh boot where "+" is the first key pressed on
// services) is retained and still matters: it proves the assertion below
// is meaningful — "+" alone was never bound, so "no funding command"
// after the round trip is the same observation the control makes, and the
// only variable between them is the abandoned prefix.
//
// Can-it-fail proof (2026-08-21): with the Abort call scratch-copied out
// of Activate, this test fails at "services grammar STILL pending after
// switching away" — and, with that assertion removed too, at the funding
// command actually firing with MET-G1202. Restored, it passes.
func TestRouting_AbandonedSequenceIsAbortedOnSwitch(t *testing.T) {
	// --- control: "+" alone, with no stale prefix, must do nothing ---
	ctl := bootForScreenTest(t)
	routeKeyInput(ctl, keyMsg(tcell.KeyF4, 0))
	routeKeyInput(ctl, keyMsg(tcell.KeyRune, '+'))
	// Barrier: a command sent AFTER any funding command would be, whose
	// result we await, so "no funding result by now" is a deterministic
	// observation rather than a sleep.
	barrier(t, ctl, "feat211-destructive-control")
	if got := ctl.servicesScreen.FundingRejectedReason(); got != "" {
		t.Fatalf("control: FundingRejectedReason() = %q after a bare '+', want empty (fixture assumption: '+' alone is not bound)", got)
	}

	// --- attack: half-type "s f", leave the screen, come back, press "+" ---
	w := bootForScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0)) // services
	routeKeyInput(w, keyMsg(tcell.KeyRune, 's'))
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'f'))
	if !w.keyGrammar.IsPending() {
		t.Fatal("services grammar not pending after 's','f' — fixture assumption broken")
	}
	routeKeyInput(w, keyMsg(tcell.KeyF1, 0)) // walk away to map
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() after F1 = %q, want %q", got, screenIDMap)
	}
	if w.keyGrammar.IsPending() {
		t.Fatal("services grammar is STILL pending after switching away — Activate must abort the outgoing screen's grammar (FEAT-211 inc1 destructive finding 1)")
	}
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0)) // and back to services
	routeKeyInput(w, keyMsg(tcell.KeyRune, '+'))

	// Same deterministic observation the control makes: everything queued
	// before this point has been adjudicated, so an empty reason now means
	// no funding command was ever sent, not "not yet".
	barrier(t, w, "feat211-destructive-abandoned")
	if reason := w.servicesScreen.FundingRejectedReason(); reason != "" {
		t.Fatalf("a single '+' after F4->F1->F4 sent a real funding command (engine said: %s) — the abandoned 's f' prefix survived the switch", reason)
	}
	// And the grammar is genuinely at root, not merely quiet: "+" was
	// NoSuchSequence, which resets to idle either way, so assert the
	// stronger property that the round trip left nothing behind at all.
	if w.keyGrammar.IsPending() {
		t.Fatal("services grammar left pending state behind after the round trip")
	}
}

// TestRouting_SelfSwitchAbortsPendingGrammar_FundingNotSent is r2's own
// finding F1, live end-to-end: F4, "s", "f", F4, "+" must send NOTHING.
// The original ruling on this diff (self-Activate is a harmless no-op)
// lost to two arguments r2 made in the independent round that first
// reviewed this fix: an F-key is a navigation intent regardless of
// whether it lands on a different screen, and the old behaviour was
// internally inconsistent — F1->F4 (switch away and back) cleared the
// pending prefix, but F4->F4 (self-switch) did not. ScreenRegistry.Activate
// now captures and aborts the outgoing grammar whenever a screen is
// already active (r.active >= 0), with no exception for id == the current
// screen — see screen_registry.go's Activate doc comment.
//
// Sibling of TestRouting_AbandonedSequenceIsAbortedOnSwitch above (same
// shape: half-type "s f", trigger a switch, press a bare "+", assert
// nothing fired), with the switch replaced by a self-switch.
//
// Can-it-fail proof (2026-08-21): with Activate's outgoing-grammar capture
// reverted to `if r.active >= 0 && r.active != idx` (the overturned
// carve-out, scratch-copied back in for this proof), this test fails at
// "services grammar is STILL pending after a self-switch F4" and then at
// the funding command actually firing with MET-G1202. Restored to the
// unconditional capture, it passes.
func TestRouting_SelfSwitchAbortsPendingGrammar_FundingNotSent(t *testing.T) {
	w := bootForScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0)) // services
	routeKeyInput(w, keyMsg(tcell.KeyRune, 's'))
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'f'))
	if !w.keyGrammar.IsPending() {
		t.Fatal("services grammar not pending after 's','f' — fixture assumption broken")
	}

	routeKeyInput(w, keyMsg(tcell.KeyF4, 0)) // SELF-switch: services -> services
	if got := w.screens.ActiveID(); got != screenIDServices {
		t.Fatalf("ActiveID() after a self-switch F4 = %q, want still %q", got, screenIDServices)
	}
	if w.keyGrammar.IsPending() {
		t.Fatal("services grammar is STILL pending after a self-switch F4 — Activate must abort the outgoing grammar even when the incoming screen is the same one (r2 finding F1)")
	}

	routeKeyInput(w, keyMsg(tcell.KeyRune, '+'))

	// Same deterministic barrier pattern as the switch-away test above:
	// everything queued before this point has been adjudicated, so an
	// empty reason now means no funding command was ever sent.
	barrier(t, w, "feat211-r2-f1-self-switch")
	if reason := w.servicesScreen.FundingRejectedReason(); reason != "" {
		t.Fatalf("a single '+' after F4->F4 (self-switch) sent a real funding command (engine said: %s) — the abandoned 's f' prefix survived a self-switch (r2 finding F1)", reason)
	}
	if w.keyGrammar.IsPending() {
		t.Fatal("services grammar left pending state behind after the self-switch round trip")
	}
}

// barrier sends one AdvanceTicks command and waits for its result, giving
// the tests above a deterministic "everything queued before this point
// has been adjudicated" point instead of a sleep.
func barrier(t *testing.T, w *skeletonWiring, corr string) {
	t.Helper()
	res := sendAndAwaitResult(t, w, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corr),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	})
	if !res.Accepted {
		t.Fatalf("barrier AdvanceTicks rejected: %+v", res.Error)
	}
}

// TestRouting_EscAbortsPendingSequenceThenQuits is FINDING 2, INVERTED
// after the fix (run.go's OnDelivered now routes Esc to the grammars
// instead of quitting whenever any grammar IsPending()).
//
// As originally written this asserted the DEFECT: FEAT-211 increment 1
// made leader sequences reachable from a real keyboard for the first
// time, but OnDelivered checked isQuitInput BEFORE routeKeyInput and
// returned — and isQuitInput claims Esc. keys.KeyGrammar treats "<Esc>"
// as its reserved, unconditional abort token (grammar.go's
// reservedTokens/computeFeed step 1) and refuses to let anything bind it,
// so the ONE documented way to cancel a mistyped mnemonic terminated the
// process instead, with the half-typed sequence still pending at exit.
//
// It now asserts both halves of the resolved behaviour, in one run, so
// neither can regress silently: the FIRST Esc (sequence pending) aborts
// and the game keeps running; the SECOND Esc (nothing pending) quits, so
// the fix did not simply take Esc away from the quit path.
//
// Can-it-fail proof (2026-08-21): with the isEscInput/anyGrammarPending
// branch scratch-copied out of OnDelivered, this test fails at "the run
// loop exited on the first Esc". Restored, it passes.
func TestRouting_EscAbortsPendingSequenceThenQuits(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat211-destructive-esc", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(120, 30)

	done := make(chan struct{})
	go func() {
		runInteractive(w, sim)
		close(done)
	}()

	sim.InjectKey(tcell.KeyF4, 0, tcell.ModNone)
	waitFor(t, func() bool { return w.screens.ActiveID() == screenIDServices }, "F4 to activate services")
	sim.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	waitFor(t, func() bool { return w.keyGrammar.IsPending() }, "'s' to start a pending sequence")

	// The player, having mistyped, presses Esc to cancel. This must abort
	// the sequence and NOT end the game.
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, func() bool { return !w.keyGrammar.IsPending() }, "Esc to abort the pending sequence")
	select {
	case <-done:
		t.Fatal("the run loop exited on the first Esc — Esc must abort a pending sequence, not quit (FEAT-211 inc1 destructive finding 2)")
	case <-time.After(250 * time.Millisecond):
		// Still running, as it must be.
	}

	// Nothing is pending now, so Esc reverts to its process-lifecycle
	// meaning: this must still be a working way to leave the game.
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractive did not exit on a second Esc with nothing pending — the abort fix must not cost Esc its quit meaning")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestRouting_DigitKeystrokeIsFedToBothGrammars is FINDING 3. Unlike
// findings 1/2/4 this one was a contract-honesty failure, not a live
// mis-fire: routeKeyInput's doc comment claimed "NEVER both for the same
// keystroke" and design §7(b) claims "exactly one call to Feed per
// keystroke", while any key chrome does not DISPATCH is in fact fed to
// both — and for a digit that is not inert, since BOTH grammars
// accumulate it as a count prefix.
//
// The fix was to the DOC, not the routing (see routeKeyInput's rewritten
// comment, which now states the double-feed plainly and records the
// layering decision UI-SPEC §3's 1/2/3 speed globals will need). So this
// test is NOT inverted — the behaviour it describes is now the
// deliberately documented contract, and this is what pins it.
//
// What DID change: the t.Skip that used to sit on the second assertion is
// now a t.Fatal. A skip would let a future single-consumer change land
// while routeKeyInput's doc still described the double-feed; a failure
// forces whoever makes that change to come here and update the contract
// in the same commit.
func TestRouting_DigitKeystrokeIsFedToBothGrammars(t *testing.T) {
	w := bootForScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0))
	if w.chromeGrammar.IsPending() || w.keyGrammar.IsPending() {
		t.Fatal("a grammar was already pending before any digit — fixture assumption broken")
	}

	routeKeyInput(w, keyMsg(tcell.KeyRune, '3'))

	if !w.keyGrammar.IsPending() {
		t.Fatal("services grammar did not take the count digit — fixture assumption broken")
	}
	if !w.chromeGrammar.IsPending() {
		t.Fatal("chromeGrammar no longer consumed the same digit: the routing changed to true single-consumer. That is the RIGHT direction (see routeKeyInput's \"layering decision\" section), but routeKeyInput's doc comment still documents the double-feed — update it and this test together")
	}
}

// TestRouting_ChromeCountPrefixSurvivesASwitchAndExpiresOnTheIdleTick
// pins the durable-junk half of the double-feed: digits typed before an
// F-key stay accumulated on chromeGrammar across the switch, because a
// global deliberately does not disturb pending state (grammar.go step 4).
// That part is unchanged and correct.
//
// What the round found alongside it — that nothing would EVER clear that
// prefix — is fixed: runIdleTimeouts (run.go) drives CheckIdleTimeout on
// the UI tick, so it expires 2s after the last keystroke. This test
// exercises routeKeyInput directly, with no render loop running, so it
// asserts only the survives-the-switch half; the expiry half is
// TestRouting_IdleTimeoutIsDrivenByTheUITick below, which runs the real
// loops.
func TestRouting_ChromeCountPrefixSurvivesASwitchAndExpiresOnTheIdleTick(t *testing.T) {
	w := bootForScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyRune, '7'))
	if !w.chromeGrammar.IsPending() {
		t.Fatal("chromeGrammar did not accumulate the digit — fixture assumption broken (see TestRouting_DigitKeystrokeIsFedToBothGrammars)")
	}
	routeKeyInput(w, keyMsg(tcell.KeyF2, 0))
	if got := w.screens.ActiveID(); got != screenIDFinance {
		t.Fatalf("ActiveID() = %q, want %q", got, screenIDFinance)
	}
	if !w.chromeGrammar.IsPending() {
		t.Fatal("chromeGrammar's count prefix was cleared by the switch — a global must not disturb pending state (grammar.go step 4); if that is now intended, update this test and grammar.go's contract together")
	}
}

// TestRouting_IdleTimeoutIsDrivenByTheUITick is FINDING 4, INVERTED
// after the fix (runIdleTimeouts, run.go). As originally written it
// asserted the DEFECT: ui.keys ships CheckIdleTimeout (AC-2d,
// DefaultIdleTimeout = 2s) precisely so a half-typed sequence
// self-cancels, and NOBODY in the live binary called it, so pending state
// was immortal — a sequence was still pending long after the timeout had
// passed, with the loops demonstrably alive and delivering input.
//
// It now asserts the opposite. Asserted behaviourally through the real
// runInteractive, never by grepping for a call site: the sleep is a LOWER
// bound on elapsed time before a state check (BUG-291/292's rule — this
// is not a wall-clock upper-bound performance assertion, and the check
// that follows it has no deadline of its own).
//
// Can-it-fail proof (2026-08-21): with the runIdleTimeouts goroutine's
// launch scratch-copied out of runInteractive, this test fails at "the
// sequence is STILL pending". Restored, it passes.
func TestRouting_IdleTimeoutIsDrivenByTheUITick(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat211-destructive-idle", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(120, 30)
	done := make(chan struct{})
	go func() {
		runInteractive(w, sim)
		close(done)
	}()
	defer func() {
		sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
		<-done
	}()

	sim.InjectKey(tcell.KeyF4, 0, tcell.ModNone)
	waitFor(t, func() bool { return w.screens.ActiveID() == screenIDServices }, "F4 to activate services")
	sim.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	waitFor(t, func() bool { return w.keyGrammar.IsPending() }, "'s' to start a pending sequence")

	// Deliberately longer than keys.DefaultIdleTimeout (2s). This is a
	// lower bound on elapsed time before a state check, not an upper-bound
	// performance assertion.
	time.Sleep(2500 * time.Millisecond)

	if w.keyGrammar.IsPending() {
		t.Fatal("the sequence is STILL pending >2.5s after the last key: nothing in cmd/metropolis is driving keys.KeyGrammar.CheckIdleTimeout (FEAT-211 inc1 destructive finding 4)")
	}

	// The loops must still be alive and routing — an "expired" sequence
	// proves nothing if the driver achieved it by the run loop dying.
	sim.InjectKey(tcell.KeyF2, 0, tcell.ModNone)
	waitFor(t, func() bool { return w.screens.ActiveID() == screenIDFinance }, "F2 to still switch screens after the idle abort")
}

// TestRouting_UnroutableInputKinds_AreSilentlyIgnored pins routeKeyInput's
// two fail-open guards: a non-key InputMsg and a KeyInput whose Raw is
// not a *tcell.EventKey are dropped without panic and without touching
// the active screen.
func TestRouting_UnroutableInputKinds_AreSilentlyIgnored(t *testing.T) {
	w := bootForScreenTest(t)
	routeKeyInput(w, core.InputMsg{Kind: core.MouseInput, Raw: tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone)})
	routeKeyInput(w, core.InputMsg{Kind: core.ResizeInput, Width: 80, Height: 24, Raw: tcell.NewEventResize(80, 24)})
	routeKeyInput(w, core.InputMsg{Kind: core.OtherInput, Raw: nil})
	// A KeyInput carrying no usable Raw event: silently dropped rather
	// than reconstructed from msg.Key/msg.Rune (which ARE populated).
	// Harmless today because InputLoop.translate always sets Raw, but it
	// means any future synthetic-key path must remember to set Raw or its
	// keys vanish with no diagnostic.
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyF2, Raw: nil})
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() = %q after unroutable input, want unchanged %q", got, screenIDMap)
	}
}

// TestRouting_RepeatKeyIsAlsoDoubleFed checks the '.' repeat token, the
// other grammar built-in that both grammars see: chromeGrammar's own
// lastDispatch is never set (globals do not record one, grammar.go step
// 4), so '.' falls through to the active screen's grammar and repeats
// ITS last dispatch. Documented here because it is the one double-fed
// built-in whose behaviour is actually correct — the attacker checked it
// rather than assuming.
func TestRouting_RepeatKeyIsAlsoDoubleFed(t *testing.T) {
	w := bootForScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0))
	// No dispatch has happened yet on either grammar: '.' must be inert,
	// not a panic and not a spurious funding command.
	routeKeyInput(w, keyMsg(tcell.KeyRune, '.'))
	barrier(t, w, "feat211-destructive-repeat")
	if got := w.servicesScreen.FundingRejectedReason(); got != "" {
		t.Fatalf("FundingRejectedReason() = %q after a bare '.', want empty", got)
	}
	if got := w.screens.ActiveID(); got != screenIDServices {
		t.Fatalf("ActiveID() = %q after '.', want %q", got, screenIDServices)
	}
}

// TestRouting_ActiveScreenDrawnWhileDeltasFlow_NoRace attacks the hazard
// this increment newly creates: before FEAT-211 the render loop drew ONLY
// mapScreen, so finance/services state was mutated by the router's
// delivery goroutine and read by nobody. financeDrawFunc/servicesDrawFunc
// now read that same state from T-RENDER every tick, while ApplyDelta/
// ApplyResult keep writing it from the router goroutine and the funding
// action writes it from T-INPUT — three goroutines over one Screen. Run
// under -race, this is the check that those screens' own mutexes actually
// cover every field the new draw closures touch.
func TestRouting_ActiveScreenDrawnWhileDeltasFlow_NoRace(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat211-destructive-drawrace", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(120, 30)
	done := make(chan struct{})
	go func() {
		runInteractive(w, sim)
		close(done)
	}()
	defer func() {
		sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
		<-done
	}()

	for i, id := range []core.ScreenID{screenIDFinance, screenIDServices} {
		key := tcell.KeyF2
		if id == screenIDServices {
			key = tcell.KeyF4
		}
		sim.InjectKey(key, 0, tcell.ModNone)
		waitFor(t, func() bool { return w.screens.ActiveID() == id }, "switch to "+string(id))
		// Deltas flow (engine -> router -> ApplyDelta) while T-RENDER is
		// drawing this very screen, and the funding key writes it from
		// T-INPUT at the same time.
		go func() {
			sim.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
			sim.InjectKey(tcell.KeyRune, 'f', tcell.ModNone)
			sim.InjectKey(tcell.KeyRune, '+', tcell.ModNone)
		}()
		barrier(t, w, "feat211-destructive-drawrace-"+strconvItoaLocal(i))
		barrier(t, w, "feat211-destructive-drawrace-b-"+strconvItoaLocal(i))
	}
}

func strconvItoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestRouting_SwitchStormWhileKeysFlow is the boot-level version of the
// registry's switching storm: F-keys and screen keys interleaved through
// the real routeKeyInput, then a barrier, asserting the wiring never
// panics, always lands on a registered screen, and leaves the engine
// pipeline healthy (router.PanicCount unchanged).
func TestRouting_SwitchStormWhileKeysFlow(t *testing.T) {
	w := bootForScreenTest(t)
	panicsBefore := w.router.PanicCount()
	keys := []core.InputMsg{
		keyMsg(tcell.KeyF1, 0), keyMsg(tcell.KeyRune, 's'), keyMsg(tcell.KeyF2, 0),
		keyMsg(tcell.KeyRune, 'f'), keyMsg(tcell.KeyF4, 0), keyMsg(tcell.KeyRune, '2'),
		keyMsg(tcell.KeyF4, 0), keyMsg(tcell.KeyRune, 'z'), keyMsg(tcell.KeyF3, 0),
	}
	for i := 0; i < 50; i++ {
		for _, k := range keys {
			routeKeyInput(w, k)
		}
	}
	barrier(t, w, "feat211-destructive-storm")
	switch id := w.screens.ActiveID(); id {
	case screenIDMap, screenIDFinance, screenIDServices:
	default:
		t.Fatalf("ActiveID() = %q after a key storm, want one of the three registered screens", id)
	}
	if got := w.router.PanicCount(); got != panicsBefore {
		t.Fatalf("router.PanicCount() = %d after a key storm, want unchanged %d", got, panicsBefore)
	}
}
