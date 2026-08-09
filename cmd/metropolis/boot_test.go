package main

import (
	"errors"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// waitFor polls cond every millisecond for up to 2 seconds, failing the
// test if it never becomes true — the same pattern internal/ui/core's own
// harness_test.go uses to synchronize against this item's background
// goroutines (StubEngine.Run, ui.core.ViewsLoop.Run) without a flaky fixed
// sleep.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}

// --- AC-2: module registry boots with every module stub/ok ---
// (this is also AC-3's fallback verification path: "the registry's own
// API directly," exercised here independent of whether ui.screen.debug
// has landed).

func TestRegisterSkeletonModules_AllStubHealthy(t *testing.T) {
	reg := registry.NewRegistry()
	if err := registerSkeletonModules(reg, "test-correlation"); err != nil {
		t.Fatalf("registerSkeletonModules: %v", err)
	}

	entries := reg.List()
	if len(entries) != len(skeletonModuleKeys) {
		t.Fatalf("registry has %d entries, want %d", len(entries), len(skeletonModuleKeys))
	}
	for _, e := range entries {
		if e.Status != registry.StatusStub {
			t.Errorf("module %s: status = %q, want %q", e.Key, e.Status, registry.StatusStub)
		}
		if e.Health != registry.HealthOK {
			t.Errorf("module %s: health = %q, want %q", e.Key, e.Health, registry.HealthOK)
		}
	}
}

// --- AC-7: a boot-time failure in a wired component is a clear,
// registry-sourced error --- (exercised here without needing a broken
// terminal: a colliding module key stands in for "a wired component
// fails to initialize").

func TestBootCore_DuplicateModuleRegistration_FailsCleanly(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register("harness.stub", nil, wiredModule{name: "harness.stub"}); err != nil {
		t.Fatalf("seeding collision: %v", err)
	}

	w, err := bootCore("test-correlation", reg)
	if err == nil {
		t.Fatal("bootCore: expected an error on duplicate module registration, got nil")
	}
	if w != nil {
		t.Fatal("bootCore: expected nil wiring on failure")
	}

	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("bootCore error %v is not a registry-sourced *errs.E", err)
	}
	if e.Code != codeBootFailure {
		t.Errorf("error code = %q, want %q", e.Code, codeBootFailure)
	}
	if e.CorrelationID != "test-correlation" {
		t.Errorf("error correlation ID = %q, want %q", e.CorrelationID, "test-correlation")
	}
}

// --- AC-1a / AC-5a: real protocol -> harness.stub -> ui.core ->
// ui.screen.map wiring, driven entirely through int.protocol's own API,
// nothing mocked ---

// wellKnownFolkestoneCells mirrors internal/ui/screens/map/map_test.go's
// own known-fixture assertions (this package deliberately does not import
// internal/engine/stub outside tests, so it re-derives these facts rather
// than reusing stub.GenerateFolkestone64 directly — see fixture.go for the
// source of truth: shore band at y=0, "Folkestone Harbour Arm" at (5,3),
// "Sandgate Road" at (8,5)).
func TestIntegration_SubscribeRendersFolkestone64(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("integration-render", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	// AC-1a: wait for the real int.protocol Subscribe MapScreen issued in
	// bootCore to land, via the real harness.stub -> ui.core.ViewsLoop
	// path — no simulated keystrokes, no fake transport.
	waitFor(t, func() bool { return len(w.viewStore.Front().Patches) > 0 })

	draw := mapDrawFunc(w.mapScreen)
	buf := core.NewBuffer(64, 64)
	draw(buf, w.viewStore.Front())

	waterColor := widgets.DefaultPalette.Color(widgets.TokenWater)
	wantStyle := tcell.StyleDefault.Background(waterColor)

	if got := buf.Get(10, 0); got.Rune != '~' || got.Style != wantStyle {
		t.Errorf("cell (10,0) = %+v, want shore rune '~' style %+v", got, wantStyle)
	}
	if got := buf.Get(5, 3); got.Rune != '#' {
		t.Errorf("cell (5,3) = %+v, want building rune '#' (Folkestone Harbour Arm)", got)
	}
	if got := buf.Get(8, 5); got.Rune != '+' {
		t.Errorf("cell (8,5) = %+v, want road rune '+' (Sandgate Road)", got)
	}
}

func TestIntegration_CommandsExerciseStubEngineEndToEnd(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("integration-commands", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	// Learn F1's real SubscriptionID from the ViewStore ui.core.ViewsLoop
	// publishes to (the sole entry — see mapDrawFunc's doc comment) rather
	// than adding a second, competing reader of transport.Deltas(), which
	// int.protocol documents as ViewsLoop's exclusive channel.
	var subID protocol.SubscriptionID
	waitFor(t, func() bool {
		for id := range w.viewStore.Front().Patches {
			subID = id
		}
		return subID != ""
	})

	// bootCore's own internal MapScreen.Subscribe call already produced
	// exactly one CommandResult that nothing has read yet — drain it
	// before this test starts correlating its own send()s 1:1 against
	// Results(), or the very first send() below would receive THIS
	// leftover result instead of its own (both happen to be Accepted, so
	// the mismatch would silently desynchronize every following
	// assertion instead of failing loudly).
	select {
	case <-w.transport.Results():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the boot Subscribe's CommandResult")
	}

	send := func(kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
		t.Helper()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(errs.NewCorrelationID()),
			Kind:            kind,
			Payload:         payload,
		}
		if err := w.transport.SendCommand(cmd); err != nil {
			t.Fatalf("SendCommand(%s): %v", kind, err)
		}
		select {
		case res := <-w.transport.Results():
			return res
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for a %s result", kind)
			return protocol.CommandResult{}
		}
	}

	beforeTick := w.stubEngine.Tick()

	if res := send(protocol.KindPause, protocol.PausePayload{}); !res.Accepted {
		t.Fatalf("Pause rejected: %+v", res.Error)
	}
	if res := send(protocol.KindSetSpeed, protocol.SetSpeedPayload{Speed: 2}); !res.Accepted {
		t.Fatalf("SetSpeed rejected: %+v", res.Error)
	}
	if res := send(protocol.KindResume, protocol.ResumePayload{}); !res.Accepted {
		t.Fatalf("Resume rejected: %+v", res.Error)
	}
	if got := w.stubEngine.Tick(); got != beforeTick {
		t.Fatalf("Pause/SetSpeed/Resume advanced the tick: got %d, want unchanged %d", got, beforeTick)
	}

	if res := send(protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5}); !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}
	if got, want := w.stubEngine.Tick(), beforeTick+5; got != want {
		t.Fatalf("Tick() after AdvanceTicks(5) = %d, want %d", got, want)
	}

	if res := send(protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: subID}); !res.Accepted {
		t.Fatalf("Unsubscribe(%s) rejected: %+v", subID, res.Error)
	}
}
