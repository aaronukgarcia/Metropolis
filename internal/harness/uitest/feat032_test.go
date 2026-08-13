package uitest

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/engine/stub"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// This file discharges FEAT-032 — FEAT-006's AC-1b, deferred at Sprint 1
// close pending ui.harness (MOD-014) and harness.replay (MOD-013), both
// now done. AC-1b's own wording: "literal scripted terminal key
// sequences, translated by ui.keys' key grammar and replayed against a
// recorded delta-stream fixture, reproduce the same result" as AC-1a
// (feat.skeleton's protocol-driven render of Folkestone-64's initial F1
// view, cmd/metropolis/boot_test.go's TestIntegration_SubscribeRendersFolkestone64).
//
// This is the layer directly above internal/ui/keys/feat006_test.go
// (FEAT-033's key-grammar-to-Command leg): that file proves a real
// tcell.EventKey reaches a real protocol.Command via KeyGrammar.
// FeedTcellEvent, standing up its own Action.Run closure by hand. This
// file proves the same key-grammar leg AGAIN, but this time the
// tcell.EventKey itself originates from uitest's own scripted-key DSL
// (SendKeys("p"), parsed by keyscript.go and delivered through a real
// Harness's real T-INPUT goroutine — chanEventSource -> InputLoop ->
// OnDelivered), and the resulting render is asserted against a
// cell-buffer snapshot of Folkestone-64, fed by a REAL recorded
// harness.replay fixture (not a synthetic {"n":i} payload like
// testutil_test.go's buildFixture) — recorded from a real StubEngine +
// MapScreen.Subscribe pair, exactly the same call ui.screen.map's own
// Subscribe method makes in cmd/metropolis's bootCore.
//
// Nothing in this test hand-builds a protocol.Command literal to fake
// the keystroke leg, and nothing hand-builds a Folkestone-64 patch to
// fake the render leg — both are the real, already-accepted
// implementations, driven end to end.

// buildFolkestone64BootFixture records ONE real Delta — the exact
// full-viewport snapshot handleSubscribe emits (internal/engine/stub/
// engine.go) — from a real StubEngine wired to a real
// protocol.InProcTransport, subscribed via ui.screen.map's own
// MapScreen.Subscribe (never a hand-rolled Subscribe Command literal),
// and returns it round-tripped through a real harness.replay Save/Load
// cycle, exactly like a genuine recording (never an in-memory-only
// Recorder standing in for one). This is "Folkestone-64's boot
// sequence" made concrete on the render side: the one Delta a freshly
// booted feat.skeleton binary emits before any simulated ticks occur,
// the same Delta cmd/metropolis/boot_test.go's
// TestIntegration_SubscribeRendersFolkestone64 asserts against directly.
func buildFolkestone64BootFixture(t *testing.T) (replay.Fixture, protocol.SubscriptionID) {
	t.Helper()

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()

	engine, err := stub.NewStubEngine(transport)
	if err != nil {
		t.Fatalf("NewStubEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		_ = engine.Run(ctx)
	}()

	ms := mapscreen.NewMapScreen("feat032-fixture-build", widgets.DefaultPalette)
	if err := ms.Subscribe(transport.SendCommand); err != nil {
		cancel()
		<-engineDone
		t.Fatalf("MapScreen.Subscribe: %v", err)
	}

	select {
	case res := <-transport.Results():
		if !res.Accepted {
			cancel()
			<-engineDone
			t.Fatalf("Subscribe rejected: %+v", res.Error)
		}
	case <-time.After(2 * time.Second):
		cancel()
		<-engineDone
		t.Fatal("timed out waiting for Subscribe's CommandResult")
	}

	var delta protocol.Delta
	select {
	case delta = <-transport.Deltas():
	case <-time.After(2 * time.Second):
		cancel()
		<-engineDone
		t.Fatal("timed out waiting for Folkestone-64's initial viewport Delta")
	}

	cancel()
	<-engineDone

	rec := replay.NewRecorder()
	if err := rec.ObserveDelta(delta); err != nil {
		t.Fatalf("ObserveDelta: %v", err)
	}

	dir := t.TempDir()
	meta := replay.FixtureMeta{WorldSeed: int64(stub.FixtureSeed), AppVersion: "feat032-test"}
	if err := replay.Save(dir, "folkestone64-boot", rec, meta); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := replay.Load(dir, "folkestone64-boot")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fx, delta.SubscriptionID
}

// mapScreenDraw adapts a *mapscreen.MapScreen into a uicore.DrawFunc,
// mirroring cmd/metropolis/boot.go's own (unexported) mapDrawFunc: apply
// the ViewStore's front ViewModels patch for the SPECIFIC live
// subscription under test (subID — the real Folkestone-64 subscription's
// ID, captured by buildFolkestone64BootFixture from the recorded Delta),
// propagate staleness, then render.
//
// This must key on subID rather than ranging over vm.Patches and taking
// whatever entry map iteration hands it first: by the time Render() runs,
// vm.Patches also carries fixturePlayback's synthetic barrier Delta
// (transport.go's barrierSubID) — a second, unrelated entry the pump
// goroutine appends once the fixture's real deltas are exhausted, purely
// as AwaitDeltas' non-timing completion signal. Go's map iteration order
// is randomized, so an unkeyed `for id, patch := range vm.Patches { ...;
// break }` picks the barrier's empty/synthetic patch roughly as often as
// the real one, applying a blank patch and rendering an empty buffer —
// this was FEAT-032's Destructive-attacker finding (~20% flake under
// `-race -count=20`). Keying on the exact subID sidesteps map iteration
// order entirely: there is exactly one correct entry, and it is always
// the one looked up, never "whichever one came first this run."
func mapScreenDraw(ms *mapscreen.MapScreen, subID protocol.SubscriptionID) uicore.DrawFunc {
	return func(back *uicore.Buffer, vm *uicore.ViewModels) {
		if patch, ok := vm.Patches[subID]; ok {
			ms.ApplyPatch(patch)
			ms.SetStale(vm.Stale[subID])
		}
		w, h := back.Size()
		ms.Render(back, uicore.Rect{X: 0, Y: 0, W: w, H: h})
	}
}

// TestFeat032BootKeySequenceRendersFolkestone64Snapshot is FEAT-006
// AC-1b: a scripted "p" keystroke (the same walking-skeleton boot key
// FEAT-033's feat006_test.go established) is driven through uitest's
// real DSL/dispatcher into a real ui.keys.KeyGrammar, which sends a real
// protocol.Command on a real protocol.Transport — proving the keystroke
// leg genuinely reaches int.protocol, exactly like AC-5a/feat006_test.go
// but this time originating from Harness.SendKeys rather than a
// hand-built tcell.EventKey. In parallel, the SAME Harness has a real
// recorded harness.replay fixture of Folkestone-64's boot Delta attached
// as its render-side source (AC-3), and the resulting rendered cell
// buffer is asserted, cell by cell, against the exact same three
// fixture facts cmd/metropolis/boot_test.go's
// TestIntegration_SubscribeRendersFolkestone64 (AC-1a) asserts — proving
// AC-1b's own claim: the keystroke-driven leg "reproduces the same
// result" as the protocol-driven leg — plus a full golden snapshot
// comparison via AssertSnapshot (ui.harness's own built-in mechanism),
// closing FEAT-006 AC-1b.
func TestFeat032BootKeySequenceRendersFolkestone64Snapshot(t *testing.T) {
	fx, folkestoneSubID := buildFolkestone64BootFixture(t)

	// A dedicated, real protocol.Transport for the keystroke leg — kept
	// separate from the fixture (AttachFixture is a read-only Delta/
	// Result/Event source, not a full duplex Transport a Command can be
	// sent back into) — exactly the same separation feat006_test.go uses.
	cmdTransport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = cmdTransport.Close() }()

	corrID := errs.NewCorrelationID()
	grammar := keys.NewKeyGrammar(nil, 0, 0, corrID)

	sendPause := func(keys.ActionArgs) {
		_ = cmdTransport.SendCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			Kind:            protocol.KindPause,
			Payload:         protocol.PausePayload{},
			CorrelationID:   protocol.CorrelationID(corrID),
		})
	}
	// "p" is the walking skeleton's boot key sequence — the same binding
	// internal/ui/keys/feat006_test.go registers for its own
	// TestFeat006RealKeyEventReachesRealCommand.
	if err := grammar.Register([]string{"p"}, keys.Action{Name: "pause", Run: sendPause}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ms := mapscreen.NewMapScreen("feat032-render", widgets.DefaultPalette)

	// onKey is the real dispatcher wiring: every InputMsg uitest's real
	// T-INPUT goroutine delivers is fed, via its real Raw *tcell.EventKey,
	// into the real KeyGrammar — the same translation
	// internal/ui/keys/feat006_test.go exercises by hand, now driven by
	// uitest's own scripted-key pipeline instead.
	onKey := func(msg uicore.InputMsg) {
		if msg.Kind != uicore.KeyInput {
			return
		}
		ev, ok := msg.Raw.(*tcell.EventKey)
		if !ok {
			return
		}
		grammar.FeedTcellEvent(ev)
	}

	h := NewHarness(corrID, onKey, mapScreenDraw(ms, folkestoneSubID))
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}
	// RunScript = SendKeys("p") (parsed by uitest's real DSL, keyscript.go)
	// then AwaitDeltas(1, ...) for the fixture's single recorded boot
	// Delta to land in the ViewStore, driven by a real completion signal
	// (AwaitDeltas' barrier), never a blind sleep (GR#21).
	if err := h.RunScript("p", 1, 2*time.Second); err != nil {
		t.Fatalf("RunScript(\"p\"): %v", err)
	}

	// The scripted "p" keystroke's Command genuinely reached the real
	// Transport via the real KeyGrammar dispatch — the keystroke leg is
	// not a no-op decoration on top of a fixture that would have rendered
	// the same way regardless.
	select {
	case cmd := <-cmdTransport.Commands():
		if cmd.Kind != protocol.KindPause {
			t.Fatalf("Kind = %v, want KindPause", cmd.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scripted \"p\" keystroke never reached the real Transport as a Command")
	}

	if _, err := h.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Same three fixture facts cmd/metropolis/boot_test.go's AC-1a test
	// asserts (map_test.go's own known-fixture cells): shore band at
	// y=0, "Folkestone Harbour Arm" building at (5,3), "Sandgate Road"
	// road at (8,5) — proving the keystroke-driven leg reproduces AC-1a's
	// result (AC-1b's own required claim), not merely "some content
	// rendered."
	rows := splitCaptureLines(t, got)
	if got, want := runeAt(rows, 10, 0), '~'; got != want {
		t.Errorf("cell (10,0) = %q, want %q (shore)", got, want)
	}
	if got, want := runeAt(rows, 5, 3), '#'; got != want {
		t.Errorf("cell (5,3) = %q, want %q (Folkestone Harbour Arm)", got, want)
	}
	if got, want := runeAt(rows, 8, 5), '+'; got != want {
		t.Errorf("cell (8,5) = %q, want %q (Sandgate Road)", got, want)
	}

	// AC-4/AC-5/AC-14: the full cell-buffer snapshot assertion via
	// ui.harness's own built-in golden mechanism, human-diffable in a PR.
	// First run (or an intentional change) is captured with:
	//   go test ./internal/harness/uitest/... -run TestFeat032BootKeySequenceRendersFolkestone64Snapshot -update
	AssertSnapshot(t, got)
}

// splitCaptureLines/runeAt are tiny helpers over Capture()'s plain-text
// grid (renderBufferText's format: one line per row, one rune per cell,
// '\n'-terminated) so this test's cell-level assertions read the same
// buffer AssertSnapshot compares, rather than re-deriving cell contents
// from ms/vm directly.
func splitCaptureLines(t *testing.T, s string) []string {
	t.Helper()
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return lines
}

func runeAt(rows []string, x, y int) rune {
	if y < 0 || y >= len(rows) {
		return 0
	}
	row := []rune(rows[y])
	if x < 0 || x >= len(row) {
		return 0
	}
	return row[x]
}
