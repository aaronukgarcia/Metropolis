package main

import (
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	debugscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
)

// wire-tui-screens (docs/planning/acceptance/wire-tui-screens.md): F3 build,
// F10 menu, F12 debug reachable from the running binary's key router.

const twW, twH = 120, 40

func drawActive(w *skeletonWiring) *core.Buffer {
	buf := core.NewBuffer(twW, twH)
	w.screens.ActiveDraw()(buf, nil)
	return buf
}

func pressF(t *testing.T, w *skeletonWiring, k tcell.Key, want core.ScreenID) {
	t.Helper()
	routeKeyInput(w, keyMsg(k, 0))
	if got := w.screens.ActiveID(); got != want {
		t.Fatalf("after %v ActiveID() = %q, want %q", k, got, want)
	}
}

// AC-B1 / AC-M1 / AC-D1 / AC-D3: each F-key reaches its screen, the header
// is drawn, and F1/F2/F4 still reach theirs before AND after.
func TestWireTUI_AC_B1_M1_D1_D3_FKeysReachScreens(t *testing.T) {
	w := bootForScreenTest(t)
	pressF(t, w, tcell.KeyF2, screenIDFinance)
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	if !bufferContainsString(drawActive(w), twW, twH, "F3 Land & Construction") {
		t.Error("AC-B1: F3 header not drawn")
	}
	pressF(t, w, tcell.KeyF10, screenIDMenu)
	if !bufferContainsString(drawActive(w), twW, twH, "F10 Menu & saves") {
		t.Error("AC-M1: F10 header not drawn")
	}
	pressF(t, w, tcell.KeyF12, screenIDDebug)
	if !bufferContainsString(drawActive(w), twW, twH, "F12 Debug") {
		t.Error("AC-D1: F12 header not drawn")
	}
	// AC-D3: existing bindings unchanged.
	pressF(t, w, tcell.KeyF1, screenIDMap)
	pressF(t, w, tcell.KeyF2, screenIDFinance)
	pressF(t, w, tcell.KeyF4, screenIDServices)
}

// AC-B2: the booted binary's F3 shows the REAL engine's zones/catalogue
// (primed through the real f3.build view, not hand-fed).
func TestWireTUI_AC_B2_BuildScreenShowsEngineState(t *testing.T) {
	w := bootForScreenTest(t)
	zones, haveZ := w.extra.build.Zones()
	cat, haveC := w.extra.build.Catalogue()
	if !w.extra.build.HaveData() || !haveZ || !haveC || len(zones) == 0 || len(cat) == 0 {
		t.Fatalf("f3.build not primed from the engine: zones=%d catalogue=%d", len(zones), len(cat))
	}
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	if !bufferContainsString(drawActive(w), twW, twH, cat[0].Name) {
		t.Errorf("catalogue entry %q from the engine not drawn on F3", cat[0].Name)
	}
}

// AC-B3/B4: the 'b' key on F3 sends a build through the router with its
// result handler registered; a build on an unowned cell is rejected by the
// real engine and the engine's reason is shown on screen. (The accepted
// path's queue evidence lives in compose's
// TestBuildView_AC_B2_B3_QueuedBuildVisible; the ledger half is
// descoped against BUG-1142.)
func TestWireTUI_AC_B4_RejectionReasonShown(t *testing.T) {
	w := bootForScreenTest(t)
	cat, _ := w.extra.build.Catalogue()
	if len(cat) == 0 {
		t.Fatal("no catalogue")
	}
	send := func(cmd protocol.Command) error {
		cmd.CorrelationID = protocol.CorrelationID(errs.NewCorrelationID())
		res := w.engine.HandleCommand(cmd)
		w.extra.build.ApplyResult(res)
		return nil
	}
	if err := w.extra.build.BuildOn(send, protocol.CellRef{X: 9, Y: 9}, cat[0].ID); err != nil {
		t.Fatalf("BuildOn: %v", err)
	}
	reason := w.extra.build.RejectedReason()
	if reason == "" {
		t.Fatal("AC-B4: unowned-cell build left no rejection reason on the screen")
	}
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	if !bufferContainsString(drawActive(w), twW, twH, "REJECTED: ") {
		t.Errorf("AC-B4: rejection %q not drawn", reason)
	}
}

// AC-M2: the F10 screen lists the real save slots, refreshed on activation.
func TestWireTUI_AC_M2_MenuListsRealSaves(t *testing.T) {
	root := t.TempDir()
	t.Setenv(saveDirEnv, root)
	for i, name := range []string{"alpha-town", "beta-town"} {
		dir := filepath.Join(root, name)
		if err := serialize.CreateBundleDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := serialize.WriteHeader(dir, serialize.NewHeader(int64(100+i), 10, 1, "t")); err != nil {
			t.Fatal(err)
		}
	}
	w := bootForScreenTest(t)
	pressF(t, w, tcell.KeyF10, screenIDMenu)
	buf := drawActive(w)
	for _, name := range []string{"alpha-town", "beta-town"} {
		if !bufferContainsString(buf, twW, twH, name) {
			t.Errorf("AC-M2: save %q not shown on F10", name)
		}
	}
	// A third save added later appears on the next activation.
	dir := filepath.Join(root, "gamma-town")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := serialize.WriteHeader(dir, serialize.NewHeader(7, 10, 1, "t")); err != nil {
		t.Fatal(err)
	}
	pressF(t, w, tcell.KeyF1, screenIDMap)
	pressF(t, w, tcell.KeyF10, screenIDMenu)
	if !bufferContainsString(drawActive(w), twW, twH, "gamma-town") {
		t.Error("AC-M2: third save not shown after re-activation")
	}
}

// AC-D2: with debug on, F12 draws the build identity and the live error
// tail; a newly emitted error appears. (Debug mode is permanently off in
// the stock binary today, FEAT-035, so the flag is injected here.)
func TestWireTUI_AC_D2_DebugShowsBuildIdentityAndErrorTail(t *testing.T) {
	ds := debugscreen.NewScreen(nil, errs.NewCorrelationID(), debugscreen.WithDebugFlag(func() bool { return true }))
	draw := debugDrawFunc(ds)
	_ = errs.New("MET-U200", errs.NewCorrelationID(), map[string]any{"cause": "wire-tui-d2-marker"})
	buf := core.NewBuffer(twW, twH)
	draw(buf, nil)
	if !bufferContainsString(buf, twW, twH, "MET-U200") {
		t.Error("AC-D2: emitted error not in the F12 error tail")
	}
	if !bufferContainsString(buf, twW, twH, "go1") {
		t.Error("AC-D2: toolchain build identity not drawn")
	}
}

// F12 with debug off (the stock binary): header + honest notice, no panes.
func TestWireTUI_DebugOffShowsNotice(t *testing.T) {
	w := bootForScreenTest(t)
	pressF(t, w, tcell.KeyF12, screenIDDebug)
	if !bufferContainsString(drawActive(w), twW, twH, "debug mode is off") {
		t.Error("F12 with debug off did not say so")
	}
}
