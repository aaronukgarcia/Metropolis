package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	buildscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/build"
)

// Destructive round r1 (FEAT-2326609837, GR#23) attack tests for the F3
// build wiring. Kept as permanent regressions.

// TestWireTUI_Attack_B4_RealTransportRejectionReachesScreen closes the
// builder's disclosed gap (pack AC-B3 second mutation): the 'b' key on the
// ACTIVE F3 screen sends BuildOn through the real transport, the engine
// adjudicates it asynchronously, and the CommandResult must come back
// through the router to build.Screen.ApplyResult via the
// RegisterResultHandler in boot_screens.go's sendBuild. With that
// registration dropped the result is a router route-miss and the screen
// shows no rejection, so this test goes red. The map cursor at boot sits on
// a cell the city does not own, so the engine rejects the build.
//
// Ordering: the transport and router are FIFO, so once the barrier's
// AdvanceTicks result has been routed, the earlier build result has been
// routed too. No wall-clock wait.
func TestWireTUI_Attack_B4_RealTransportRejectionReachesScreen(t *testing.T) {
	w := bootForScreenTest(t)
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	if cat, _ := w.extra.build.Catalogue(); len(cat) == 0 {
		t.Fatal("f3.build catalogue empty at boot; nothing to build")
	}
	if got := w.extra.build.RejectedReason(); got != "" {
		t.Fatalf("RejectedReason() = %q before any build, want empty", got)
	}
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
	barrier(t, w, "wire-tui-attack-b4-barrier")

	reason := w.extra.build.RejectedReason()
	if reason == "" {
		t.Fatal("pressing b on F3 for an unowned cell: the engine's rejection never reached the screen (result handler not registered, or BuildOn not sent)")
	}
	// The engine's Display text can exceed the 120-column test screen (it
	// carries the correlation ID) and the status row clips, so assert on
	// the drawn prefix only.
	head := "REJECTED: " + reason
	if len(head) > 60 {
		head = head[:60]
	}
	if !bufferContainsString(drawActive(w), twW, twH, head) {
		t.Errorf("rejection %q reached the screen but %q was not drawn", reason, head)
	}
	if n := w.router.PendingResultCount(); n != 0 {
		t.Errorf("router.PendingResultCount() = %d after the build result was routed, want 0", n)
	}
}

// TestWireTUI_Attack_D3_NoKeyTheftOrSilentShadow: (1) a second F3/F10/F12
// registration on the chrome grammar is REFUSED, not silently shadowed;
// (2) with the map active, 'b' is the map's own build key and never fires
// F3's grammar (F3's rejection state stays empty, the map stays active).
func TestWireTUI_Attack_D3_NoKeyTheftOrSilentShadow(t *testing.T) {
	w := bootForScreenTest(t)
	for _, k := range []string{"F1", "F2", "F3", "F4", "F10", "F12"} {
		err := w.chromeGrammar.RegisterGlobal(keys.Key{Special: k}, keys.Action{Name: "attacker " + k, Run: func(keys.ActionArgs) {}})
		if err == nil {
			t.Errorf("a second RegisterGlobal(%s) on the chrome grammar was accepted: a conflicting bind would silently shadow the screen switch", k)
		}
	}
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() at boot = %q, want %q", got, screenIDMap)
	}
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
	barrier(t, w, "wire-tui-attack-d3-barrier")
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Errorf("'b' on the map switched the screen to %q", got)
	}
	if got := w.extra.build.RejectedReason(); got != "" {
		t.Errorf("'b' on the MAP reached F3's grammar (F3 RejectedReason = %q)", got)
	}
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	pressF(t, w, tcell.KeyF1, screenIDMap)
}

// TestWireTUI_Attack_StaleAndNoDataNeverDrawnAsLive: F3's draw path must mark
// a stale subscription on the header, and a screen with no data must say
// so and draw no catalogue names (never old or fabricated numbers as live).
func TestWireTUI_Attack_StaleAndNoDataNeverDrawnAsLive(t *testing.T) {
	w := bootForScreenTest(t)
	cat, _ := w.extra.build.Catalogue()
	if len(cat) == 0 {
		t.Fatal("f3.build catalogue empty at boot")
	}
	buf := core.NewBuffer(twW, twH)
	buildDrawFunc(w.extra, func() bool { return true })(buf, nil)
	if !bufferContainsString(buf, twW, twH, "F3 Land & Construction  (stale)") {
		t.Error("stale f3.build subscription drawn without the (stale) marker")
	}
	if !w.extra.build.Stale() {
		t.Error("draw did not push the router's stale verdict into the screen")
	}
	buf = core.NewBuffer(twW, twH)
	buildDrawFunc(w.extra, func() bool { return false })(buf, nil)
	if bufferContainsString(buf, twW, twH, "(stale)") {
		t.Error("(stale) marker persisted after the subscription recovered")
	}

	fresh := &extraScreens{build: buildscreen.New(errs.NewCorrelationID())}
	buf = core.NewBuffer(twW, twH)
	buildDrawFunc(fresh, func() bool { return false })(buf, nil)
	if !bufferContainsString(buf, twW, twH, "no data") {
		t.Error("F3 with no f3.build data did not say so")
	}
	if bufferContainsString(buf, twW, twH, cat[0].Name) {
		t.Errorf("F3 with no data drew catalogue entry %q", cat[0].Name)
	}
}
