package main

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

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

// --- AC-1a / AC-5a: real protocol -> engine.core (via compose) ->
// ui.core -> ui.screen.map wiring, driven entirely through int.protocol's
// own API, nothing mocked ---

// TestIntegration_RealEngineBootsAndServesEngineStatus proves the FEAT-082
// flip: bootCore now constructs a real *enginecore.Engine wired by the
// composition root, not harness.stub.StubEngine. The boot-time MapScreen
// Subscribe ("f1.viewport") is now REJECTED by the real engine — v1 serves
// only "engine.status" — which is the honest baseline-one state, and the
// engine.status view IS served.
func TestIntegration_RealEngineBootsAndServesEngineStatus(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("integration-real-engine", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	if w.engine == nil {
		t.Fatal("bootCore constructed a nil real engine — the FEAT-082 flip did not take")
	}

	// FEAT-208 increment 2: w.router (not this test) now owns
	// w.transport.Results()/Deltas() post-boot (router_testutil_test.go's
	// own doc comment) — the boot-time MapScreen Subscribe's own
	// CommandResult is no longer observable via a raw channel read here
	// (router drains and RouteMiss-logs it, since nothing registered a
	// handler for mapScreen's correlationID; that gating is unchanged
	// from before this increment — see boot.go's mapScreen.Subscribe
	// comment). The "f1.viewport" rejection claim this test makes is
	// instead proven by issuing the same Subscribe ourselves, through
	// router.RegisterResultHandler — an equivalent, race-free proof of
	// the same real-engine behaviour (v1 serves only registered views,
	// and "f1.viewport" is still not one of them this increment).
	send := func(kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
		t.Helper()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(errs.NewCorrelationID()),
			Kind:            kind,
			Payload:         payload,
		}
		return sendAndAwaitResult(t, w, cmd)
	}

	if res := send(protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"}); res.Accepted {
		t.Fatalf("f1.viewport Subscribe was Accepted, want REJECTED by the real engine (not a registered view this increment)")
	}
	if res := send(protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"}); !res.Accepted {
		t.Fatalf("engine.status Subscribe rejected by the real engine: %+v", res.Error)
	}
}

func TestIntegration_CommandsExerciseRealEngineEndToEnd(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("integration-commands", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	// FEAT-208 increment 2: w.router now owns w.transport.Results() post-
	// boot (router_testutil_test.go's doc comment) — no leftover-result
	// drain is needed or possible any more; each send() below registers
	// its own CorrelationID with w.router before sending, so router
	// routes each CommandResult back to exactly the call that issued it,
	// regardless of whatever else (e.g. the boot-time MapScreen Subscribe)
	// router is also routing/RouteMiss-logging concurrently.
	send := func(kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
		t.Helper()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(errs.NewCorrelationID()),
			Kind:            kind,
			Payload:         payload,
		}
		return sendAndAwaitResult(t, w, cmd)
	}

	beforeTick := w.engine.TicksCompleted()

	if res := send(protocol.KindPause, protocol.PausePayload{}); !res.Accepted {
		t.Fatalf("Pause rejected: %+v", res.Error)
	}
	if res := send(protocol.KindSetSpeed, protocol.SetSpeedPayload{Speed: 2}); !res.Accepted {
		t.Fatalf("SetSpeed rejected: %+v", res.Error)
	}
	if res := send(protocol.KindResume, protocol.ResumePayload{}); !res.Accepted {
		t.Fatalf("Resume rejected: %+v", res.Error)
	}
	if got := w.engine.TicksCompleted(); got != beforeTick {
		t.Fatalf("Pause/SetSpeed/Resume advanced the tick: got %d, want unchanged %d", got, beforeTick)
	}

	if res := send(protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5}); !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}
	if got, want := w.engine.TicksCompleted(), beforeTick+5; got != want {
		t.Fatalf("TicksCompleted() after AdvanceTicks(5) = %d, want %d", got, want)
	}
}
