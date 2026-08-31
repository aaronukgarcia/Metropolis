package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// BUG-362 blocker #1's own proof set: "no keyboard path to build anything"
// — cmd/metropolis had zero KindBuild/KindZone/KindBuy references reachable
// from routeKeyInput. This file proves the fix in two layers, mirroring
// feat211_inc1_screen_registry_test.go's own split between routing-level
// and command-shape assertions:
//
//  1. buyCommandAt/buildCommandAt (pure, no engine) are proven to build the
//     exact protocol.Command envelope a keypress at grid cell (x, y)
//     produces — this is the piece a routing-level test alone cannot see,
//     because routeKeyInput's real action sends through a live transport
//     with no observable return value.
//  2. That EXACT command shape is proven ACCEPTED by a REAL, fully wired
//     composition (w.engine.HandleCommand — the same synchronous entry
//     point ui/router's transport-draining goroutine calls for a command
//     arriving over the real channel; see enginecore.Engine.HandleCommand's
//     own doc comment: "processes one Command synchronously"), so this is
//     not a mock of the engine's accept/reject decision — it is the
//     decision.
//  3. The map screen's own KeyGrammar (w.mapGrammar) is proven to actually
//     recognise 'y'/'b'/arrow keys AND to be the grammar routeKeyInput
//     reaches when Map is the active screen (registered as ScreenEntry's
//     Grammar field, screenIDMap being FEAT-211's default active screen).
//     Arrow-key cursor movement is asserted end-to-end through
//     mapScreen.CursorPos(), a real, previously-unbound method.

// --- layer 1: pure command-shape proofs ---

func TestBuyCommandAt_BuildsCorrectCellRef(t *testing.T) {
	cmd := buyCommandAt(7, 3)
	if cmd.Kind != protocol.KindBuy {
		t.Fatalf("Kind = %s, want %s", cmd.Kind, protocol.KindBuy)
	}
	p, ok := cmd.Payload.(protocol.BuyPayload)
	if !ok {
		t.Fatalf("Payload = %T, want protocol.BuyPayload", cmd.Payload)
	}
	if p.Cell != (protocol.CellRef{X: 7, Y: 3}) {
		t.Fatalf("Cell = %+v, want {X:7 Y:3}", p.Cell)
	}
	if cmd.CorrelationID == "" {
		t.Fatal("CorrelationID is empty — every command must carry one (GR#1)")
	}
	if cmd.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("ProtocolVersion = %v, want %v", cmd.ProtocolVersion, protocol.ProtocolVersion)
	}
}

func TestBuyCommandAt_FreshCorrelationIDPerCall(t *testing.T) {
	a := buyCommandAt(0, 0)
	b := buyCommandAt(0, 0)
	if a.CorrelationID == b.CorrelationID {
		t.Fatalf("two calls minted the SAME CorrelationID (%s) — a repeated keypress would collide on router's one-registration-per-CorrelationID contract (the exact BUG-387-adjacent class FEAT-208's own doc comment warns about)", a.CorrelationID)
	}
}

func TestBuildCommandAt_BuildsCorrectCellRefAndZone(t *testing.T) {
	cmd := buildCommandAt(2, 9, mapBuildDefaultZone)
	if cmd.Kind != protocol.KindBuild {
		t.Fatalf("Kind = %s, want %s", cmd.Kind, protocol.KindBuild)
	}
	p, ok := cmd.Payload.(protocol.BuildPayload)
	if !ok {
		t.Fatalf("Payload = %T, want protocol.BuildPayload", cmd.Payload)
	}
	if p.Cell != (protocol.CellRef{X: 2, Y: 9}) {
		t.Fatalf("Cell = %+v, want {X:2 Y:9}", p.Cell)
	}
	if p.BuildingType != mapBuildDefaultZone {
		t.Fatalf("BuildingType = %q, want %q", p.BuildingType, mapBuildDefaultZone)
	}
}

// --- layer 2: the real engine accepts the exact command shape a keypress sends ---

func TestBuyThenBuild_AtSameCell_AcceptedByRealEngine(t *testing.T) {
	w := bootForScreenTest(t)

	// A fresh cell this test alone owns — (0,0) is always in-bounds
	// (cellFromRef maps every grid x/y in [0, TileSizeCells) onto
	// Baseline One's single start tile, compose.go's cellFromRef) and
	// bootForScreenTest gives every test its own engine, so no other
	// test's Buy can have claimed it first.
	buy := w.engine.HandleCommand(buyCommandAt(0, 0))
	if !buy.Accepted {
		msg := "<nil>"
		if buy.Error != nil {
			msg = buy.Error.Display
		}
		t.Fatalf("Buy at (0,0) rejected: %s — the exact command 'y' sends at the default cursor position must be accepted by a fresh composition, or the keyboard build path is not actually reachable end to end", msg)
	}

	build := w.engine.HandleCommand(buildCommandAt(0, 0, mapBuildDefaultZone))
	if !build.Accepted {
		msg := "<nil>"
		if build.Error != nil {
			msg = build.Error.Display
		}
		t.Fatalf("Build(%q) at (0,0) rejected after a successful Buy: %s — the exact command 'b' sends must be accepted once the cell is owned", mapBuildDefaultZone, msg)
	}
}

func TestBuild_BeforeBuy_RejectedByRealEngine(t *testing.T) {
	// Negative control (mutation proof for the test above): building an
	// UNOWNED cell must be rejected — proves TestBuyThenBuild's Accepted
	// really does depend on the prior Buy, not on Build always accepting.
	w := bootForScreenTest(t)
	build := w.engine.HandleCommand(buildCommandAt(5, 5, mapBuildDefaultZone))
	if build.Accepted {
		t.Fatal("Build on an unowned cell was accepted — engine.build's ownership check (requireOwned) is not actually being reached, which would make Buy-before-Build a UI-only fiction")
	}
}

func TestBuildCommandAt_UnknownZone_RejectedByRealEngine(t *testing.T) {
	// Negative control for mapBuildDefaultZone itself: an unrecognised
	// zone slug must be rejected even on an owned cell, proving the
	// engine — not the client — is the authority on the catalogue
	// (ui/screens/build/commands.go's own documented contract, which this
	// keyboard path deliberately does not duplicate client-side).
	w := bootForScreenTest(t)
	buy := w.engine.HandleCommand(buyCommandAt(1, 1))
	if !buy.Accepted {
		t.Fatalf("precondition failed: Buy at (1,1) rejected (%v)", buy.Error)
	}
	build := w.engine.HandleCommand(buildCommandAt(1, 1, "not-a-real-zone"))
	if build.Accepted {
		t.Fatal("Build with an unknown zone slug was accepted — engine.build.ErrUnknownZoneType is not being enforced")
	}
}

// --- layer 3: the map screen's grammar actually recognises these keys, wired as the active screen's grammar ---

func TestMapGrammar_RegisteredAsActiveScreenGrammar(t *testing.T) {
	w := bootForScreenTest(t)
	if w.mapGrammar == nil {
		t.Fatal("bootCore did not construct w.mapGrammar (BUG-362 blocker #1's keyboard build path)")
	}
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("ActiveID() = %q, want %q", got, screenIDMap)
	}
	if g := w.screens.ActiveGrammar(); g != w.mapGrammar {
		t.Fatal("w.mapGrammar is not the Grammar registered against screenIDMap — routeKeyInput's w.screens.ActiveGrammar() feed will never reach it")
	}
}

func TestMapGrammar_RecognisesBuildKeys(t *testing.T) {
	w := bootForScreenTest(t)
	for _, r := range []rune{'y', 'b'} {
		res := w.mapGrammar.Feed(keys.KeyRune(r))
		if res.Status != keys.GlobalDispatched {
			t.Errorf("Feed(%q) = %v, want GlobalDispatched — this key must be registered as a global action on the map grammar", string(r), res.Status)
		}
	}
}

func TestMapGrammar_ArrowKeys_MoveCursor(t *testing.T) {
	w := bootForScreenTest(t)

	// A real render sets the viewport size every frame (mapDrawFunc calls
	// Render, which calls SetViewportSize) before any cursor position can
	// mean anything — clampCursorLocked clamps to (0,0) while viewportW/H
	// are still their zero value, so this test's own MoveCursor calls
	// would silently no-op without it (this is not the bug under test;
	// it is this test's own precondition).
	w.mapScreen.SetViewportSize(40, 20)

	// Start the cursor away from the edges so every direction has room
	// to move without clamping masking a wiring failure as a coincidental
	// pass.
	w.mapScreen.MoveCursor(5, 5)
	x0, y0 := w.mapScreen.CursorPos()

	cases := []struct {
		key    tcell.Key
		wantDX int
		wantDY int
	}{
		{tcell.KeyRight, 1, 0},
		{tcell.KeyLeft, -1, 0},
		{tcell.KeyDown, 0, 1},
		{tcell.KeyUp, 0, -1},
	}
	for _, c := range cases {
		before := [2]int{x0, y0}
		x0, y0 = w.mapScreen.CursorPos()
		before[0], before[1] = x0, y0
		routeKeyInput(w, keyMsg(c.key, 0))
		x1, y1 := w.mapScreen.CursorPos()
		if x1 != before[0]+c.wantDX || y1 != before[1]+c.wantDY {
			t.Fatalf("%v: CursorPos() moved (%d,%d)->(%d,%d), want delta (%d,%d)", c.key, before[0], before[1], x1, y1, c.wantDX, c.wantDY)
		}
	}
}
