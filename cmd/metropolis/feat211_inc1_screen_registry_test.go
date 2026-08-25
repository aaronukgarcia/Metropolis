package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// FEAT-211 increment 1's own proof set: the ScreenRegistry + F-key
// switching that makes the built screens reachable. See
// E:\git\metropolis-status\active-screen-design.md for the design this
// implements and screen_registry_test.go (internal/ui/core) for the
// registry's own unit-level proofs (registration order, dup/unknown-ID
// rejection, copy-guard, allocation-constant Activate). This file proves
// the WIRING — boot.go's registry construction and run.go's routing —
// actually behaves as designed, through bootCore's real components.

// keyMsg builds a core.InputMsg for key/r exactly as InputLoop's own
// translate() would from a real *tcell.EventKey — used by the routing
// tests below to drive routeKeyInput directly, without needing a real
// SimulationScreen render loop for tests that only care about routing
// decisions, not rendered output.
func keyMsg(key tcell.Key, r rune) core.InputMsg {
	ev := tcell.NewEventKey(key, r, tcell.ModNone)
	return core.InputMsg{Kind: core.KeyInput, Key: key, Rune: r, Raw: ev}
}

func bootForScreenTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("feat211-inc1-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	if w.screens == nil {
		t.Fatal("bootCore did not construct w.screens (FEAT-211 increment 1's ScreenRegistry did not take)")
	}
	if w.chromeGrammar == nil {
		t.Fatal("bootCore did not construct w.chromeGrammar")
	}
	return w
}

// --- boot-time registry wiring sanity ---

func TestBootCore_ScreenRegistry_MapIsInitiallyActive(t *testing.T) {
	w := bootForScreenTest(t)
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() immediately after bootCore = %q, want %q (map must stay the pre-FEAT-211 baseline default)", got, screenIDMap)
	}
	got := w.screens.RegisteredIDs()
	want := []core.ScreenID{screenIDMap, screenIDFinance, screenIDServices, screenIDCensus, screenIDProjections, screenIDDistricts, screenIDTrade}
	if len(got) != len(want) {
		t.Fatalf("RegisteredIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RegisteredIDs()[%d] = %q, want %q (registration order)", i, got[i], want[i])
		}
	}
}

// --- switch-changes-draw ---

func TestScreenSwitch_F2_ChangesActiveDraw(t *testing.T) {
	w := bootForScreenTest(t)

	back := core.NewBuffer(120, 30)
	vm := &core.ViewModels{}

	// Before switching: map is active, drawing through mapDrawFunc.
	w.screens.ActiveDraw()(back, vm)
	beforeText := bufferText(back)
	if strings.Contains(beforeText, "PROFIT & LOSS") {
		t.Fatal("map screen's draw output already contains finance's own header text before any switch — test fixture assumption broken")
	}

	routeKeyInput(w, keyMsg(tcell.KeyF2, 0))
	if got := w.screens.ActiveID(); got != screenIDFinance {
		t.Fatalf("ActiveID() after F2 = %q, want %q", got, screenIDFinance)
	}

	back2 := core.NewBuffer(120, 30)
	w.screens.ActiveDraw()(back2, vm)
	afterText := bufferText(back2)
	if !strings.Contains(afterText, "PROFIT & LOSS") {
		t.Fatalf("finance screen's draw output after F2 switch does not contain its own header text; got:\n%s", afterText)
	}
}

// bufferText flattens a core.Buffer's cells into rows of runes, purely
// for substring assertions in tests (never for anything a real DrawFunc
// or RenderLoop relies on).
func bufferText(b *core.Buffer) string {
	w, h := b.Size()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := b.Get(x, y)
			if c.Rune == 0 {
				sb.WriteRune(' ')
				continue
			}
			sb.WriteRune(c.Rune)
		}
		sb.WriteRune('\n')
	}
	return sb.String()
}

// --- wrong-key-does-not-switch ---

func TestScreenSwitch_UnregisteredKey_DoesNotSwitch(t *testing.T) {
	w := bootForScreenTest(t)
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("precondition: ActiveID() = %q, want %q", got, screenIDMap)
	}

	// 'x' is not a chrome global (only F1/F2/F4 are) and not part of any
	// leader path on chromeGrammar (which registers no leader paths at
	// all — only globals) — it must not move the active screen.
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'x'))
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() after an unregistered key = %q, want unchanged %q", got, screenIDMap)
	}

	// F3/F9..F12 are real tcell specials but none are registered chrome
	// globals (F1/F2/F4/F5/F6/F7/F8 are) — same must hold for the rest.
	for _, k := range []tcell.Key{tcell.KeyF3, tcell.KeyF9, tcell.KeyF10, tcell.KeyF11} {
		routeKeyInput(w, keyMsg(k, 0))
		if got := w.screens.ActiveID(); got != screenIDMap {
			t.Fatalf("ActiveID() after unregistered %v = %q, want unchanged %q", k, got, screenIDMap)
		}
	}
}

// --- inactive-screen-still-receives-deltas (the warm-state claim) ---

func TestScreenSwitch_InactiveScreen_StillReceivesDeltas(t *testing.T) {
	w := bootForScreenTest(t)

	// map stays active throughout — finance/services are never switched
	// to in this test at all.
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("precondition: ActiveID() = %q, want %q", got, screenIDMap)
	}
	if !w.financeScreen.HaveData() {
		t.Fatal("financeScreen.HaveData() = false before advancing — priming should have delivered its first delta already")
	}
	if !w.servicesScreen.HaveData() {
		t.Fatal("servicesScreen.HaveData() = false before advancing")
	}

	panicsBefore := w.router.PanicCount()

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("feat211-inc1-inactive-warm"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 3},
	}
	res := sendAndAwaitResult(t, w, cmd)
	if !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}

	// The screens registered but NOT active (finance, services) must
	// still have live data — router.BindSubscription's delta delivery is
	// independent of which screen ScreenRegistry currently draws (design
	// §4/§7(d)) — proven here by observing no panic/route-miss occurred
	// while map stayed active the whole time.
	if !w.financeScreen.HaveData() {
		t.Error("financeScreen.HaveData() = false after AdvanceTicks while map (not finance) was the active screen — warm-state delta delivery broke")
	}
	if !w.servicesScreen.HaveData() {
		t.Error("servicesScreen.HaveData() = false after AdvanceTicks while map (not services) was the active screen")
	}
	if got := w.router.PanicCount(); got != panicsBefore {
		t.Errorf("router.PanicCount() = %d, want unchanged %d (no panic/stall routing to an inactive screen's still-bound subscription — mirrors feat208_router_test.go's own PanicCount-only proof)", got, panicsBefore)
	}
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() at test end = %q, want still %q (this test never switched)", got, screenIDMap)
	}
}

// --- single-consumer: no double-feed of the same keystroke ---

// TestScreenSwitch_GlobalKeyIsNotDispatchedByTheActiveScreenGrammar
// proves routeKeyInput's step-2-only-on-non-dispatch rule (design §7(b)):
// a keystroke a chrome global DISPATCHED must not also reach the active
// screen's grammar as a token.
//
// # Why this probe, and not the obvious one (REWRITTEN AGAIN 2026-08-21)
//
// This test's residual-pending-state probe has now broken TWICE for the
// same underlying reason. First (an earlier rewrite, same day) it used a
// switch-AWAY key (F2) and asserted the services grammar's pending "s"
// survived it — that was asserting a defect FEAT-211 inc1's independent
// round found (finding 1), fixed by making Activate abort the outgoing
// screen's grammar on any real switch. The rewrite that followed used a
// SELF-switch (F4 while already on services) as the probe instead,
// reasoning that Activate treated re-activating the active screen as a
// no-op and so would never abort it — making "pending survived" a clean
// signal that the F4 token itself never reached the grammar.
//
// r2's own round (finding F1, on THIS diff) proved that reasoning wrong
// too: the self-switch no-op was itself the defect (F4, "s", "f", F4, "+"
// fired a real funding command), so Activate now aborts unconditionally
// whenever a screen is already active — self-switch included. Both
// "double-fed" and "correctly aborted" now read as IsPending() == false
// after the second F4, so residual pending state can no longer
// distinguish them AT ALL, for either kind of switch.
//
// A first attempt at fixing THIS test registered a spy one token past the
// pending "s" prefix (path "s","F4") reasoning that, if the F4 keystroke
// were double-fed, it would dispatch there. That did NOT work, for two
// compounding reasons found by scratch-copy testing it directly: (1)
// Activate's own Abort() call runs SYNCHRONOUSLY, inside the SAME
// chromeGrammar.Feed call, as a side effect of the F4 global's own
// Action.Run (fKeyGlobal -> w.screens.Activate) — so by the time any
// hypothetical second Feed of F4 could reach w.keyGrammar, the pending "s"
// prefix the spy depended on is already gone, Abort or no Abort; and (2)
// the literal registered token was wrong anyway — Key.Token() wraps a
// named special in angle brackets ("<F4>", key.go), not the bare "F4" a
// naive path literal suggests, so even without (1) the spy would sit on a
// trie node Feed(F4) could never reach. A masked, mistokened probe is
// worse than no probe, so this is not that test.
//
// The version that actually isolates the claim: the spy is registered at
// ROOT, using the SAME Token() the real F4 keystroke resolves to
// (fkeyToken below, computed the same way FromTcellEvent does — never
// hand-typed, so this can't drift from Feed's own tokenisation again), and
// pressed on the VERY FIRST F4 of the test, while chromeGrammar is
// otherwise idle (no pending sequence anywhere for Abort to have any
// opinion about, so Abort is a true no-op here either way). If
// routeKeyInput ever fell through to w.screens.ActiveGrammar().Feed(k) for
// a key chromeGrammar already GlobalDispatched, THIS is what would fire,
// with nothing else able to explain it. Whether Activate additionally
// aborts a pending "s" prefix on a self-switch is a SEPARATE claim,
// already pinned in internal/ui/core
// (TestScreenRegistry_Activate_AbortsOutgoingScreensPendingGrammar) and by
// TestRouting_SelfSwitchAbortsPendingGrammar_FundingNotSent below; this
// test's only job is proving the keystroke itself is single-consumer.
func TestScreenSwitch_GlobalKeyIsNotDispatchedByTheActiveScreenGrammar(t *testing.T) {
	w := bootForScreenTest(t)

	fkeyToken := keys.FromTcellEvent(tcell.NewEventKey(tcell.KeyF4, 0, tcell.ModNone)).Token()
	var spyCalls int
	if err := w.keyGrammar.Register([]string{fkeyToken}, keys.Action{
		Name: "double-feed-probe",
		Run:  func(keys.ActionArgs) { spyCalls++ },
	}); err != nil {
		t.Fatalf("registering the double-feed spy on w.keyGrammar at %q: %v", fkeyToken, err)
	}

	// First F4: idle -> services. No pending prefix exists yet anywhere,
	// so Abort() (called on map's nil Grammar — a no-op — and then never
	// again, since services was never active before) cannot explain
	// anything either way. A double-fed F4 would dispatch the root-level
	// spy right here.
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0))
	if got := w.screens.ActiveID(); got != screenIDServices {
		t.Fatalf("ActiveID() after F4 = %q, want %q", got, screenIDServices)
	}
	if spyCalls != 0 {
		t.Fatalf("the root-level \"F4\" spy dispatched %d time(s) on the very first F4 — the keystroke itself reached w.keyGrammar.Feed (routeKeyInput must stop after chromeGrammar's GlobalDispatched status and never fall through to w.screens.ActiveGrammar() for a key chromeGrammar already dispatched)", spyCalls)
	}

	routeKeyInput(w, keyMsg(tcell.KeyRune, 's'))
	if !w.keyGrammar.IsPending() {
		t.Fatal("w.keyGrammar.IsPending() = false after feeding 's' — the leader path 's f +/-' did not start pending as expected; test fixture assumption broken")
	}

	// Second F4: a chrome global that resolves to the screen ALREADY
	// active. It must dispatch on chromeGrammar and stop there. Activate's
	// own Abort() now also clears the pending "s" as a documented, SEPARATE
	// effect (r2 F1) — IsPending() == false here is expected, and proves
	// nothing about double-feed on its own (see this test's own doc
	// comment for why); the spy is the only claim about the keystroke
	// itself, and a spy at root sees "F4" regardless of whether "s" was
	// pending or already aborted by the time it would have been fed.
	routeKeyInput(w, keyMsg(tcell.KeyF4, 0))
	if got := w.screens.ActiveID(); got != screenIDServices {
		t.Fatalf("ActiveID() after a second F4 = %q, want %q (a self-switch must stay put)", got, screenIDServices)
	}
	if w.keyGrammar.IsPending() {
		t.Fatal("w.keyGrammar.IsPending() = true after a self-switch F4, want false: Activate must abort the pending prefix on a self-switch too (r2 finding F1, 2026-08-21)")
	}
	if spyCalls != 0 {
		t.Fatalf("the root-level \"F4\" spy dispatched %d time(s) — the F4 keystroke itself reached w.keyGrammar.Feed on the self-switch press (routeKeyInput must stop after chromeGrammar's GlobalDispatched status and never fall through to w.screens.ActiveGrammar())", spyCalls)
	}

	// Sanity the other direction: a FRESH leader sequence must still
	// complete normally on this same grammar afterwards — proving the
	// grammar is healthy, not merely quiet, and that the spy registration
	// itself does not interfere with the real funding path.
	routeKeyInput(w, keyMsg(tcell.KeyRune, 's'))
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'f'))
	routeKeyInput(w, keyMsg(tcell.KeyRune, '+'))
	if w.keyGrammar.IsPending() {
		t.Fatal("w.keyGrammar.IsPending() = true after completing 's f +' — the sequence should have dispatched and returned to idle")
	}
	if spyCalls != 0 {
		t.Fatalf("the root-level \"F4\" spy dispatched %d time(s) during the follow-up 's f +' sequence — fixture leaked state", spyCalls)
	}
}

// --- the F4 end-to-end proof: real keystrokes -> switch -> funding
// adjust -> engine -> result -> ApplyResult, through runInteractive's
// REAL InputLoop/RenderLoop plumbing (not routeKeyInput called directly,
// unlike the routing-focused tests above) ---

func TestE2E_RealKeystrokes_SwitchToF4_FundingAdjust_RoundTripsThroughRealRunLoop(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat211-inc1-e2e", reg)
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

	// Precondition: map is active, services has no funding rejection
	// recorded yet.
	if got := w.servicesScreen.FundingRejectedReason(); got != "" {
		t.Fatalf("FundingRejectedReason() before any input = %q, want empty", got)
	}

	// F4 -> switch to services, via a REAL injected tcell key event
	// through InputLoop, exactly as a real terminal would deliver it.
	sim.InjectKey(tcell.KeyF4, 0, tcell.ModNone)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && w.screens.ActiveID() != screenIDServices {
		time.Sleep(10 * time.Millisecond)
	}
	if got := w.screens.ActiveID(); got != screenIDServices {
		t.Fatalf("ActiveID() after injecting F4 = %q, want %q (the switch never took over InputLoop)", got, screenIDServices)
	}

	// "s" "f" "+" -> the funding-increase mnemonic path
	// (feat208PilotFundingKeyPath, boot.go), now reachable because
	// services is the active screen and its grammar is registry.ActiveGrammar().
	for _, r := range []rune{'s', 'f', '+'} {
		sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}

	deadline = time.Now().Add(2 * time.Second)
	var reason string
	for time.Now().Before(deadline) {
		reason = w.servicesScreen.FundingRejectedReason()
		if reason != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reason == "" {
		t.Fatal("FundingRejectedReason() still empty after real injected keystrokes (F4, s, f, +) — the funding-adjust key never round-tripped through the real InputLoop -> routeKeyInput -> registry.ActiveGrammar -> SetFunding -> transport -> engine -> router -> ApplyResult path")
	}
	const wantCode = "MET-G1202" // services.ErrServiceNotRegistered — proves the engine itself adjudicated it
	if !strings.Contains(reason, wantCode) {
		t.Fatalf("FundingRejectedReason() = %q, want it to contain %q", reason, wantCode)
	}
	t.Logf("real keystroke round trip confirmed: %s", reason)

	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractive did not return after an injected Escape key")
	}
}
