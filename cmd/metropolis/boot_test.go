package main

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"strings"
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

// --- BUG-335: MET-E900 boot-failure message renders the real cause, not a
// literal {cause} ---
//
// The MET-E900 template is "metropolis failed to boot: {component} failed
// to initialize: {cause}", but every boot-time errs.Wrap call site supplied
// only "component", never "cause" — so {cause} rendered literally on exactly
// the boot-failure paths that matter (map/finance/services prime, compose,
// pump, registry). The wrap ctx was the wrong side; the template is right.

// TestBootFailureRendersCauseNotLiteral triggers the cheapest real boot
// failure (a colliding module registration, the AC-7 path) and asserts the
// rendered Display() carries the underlying registry error verbatim with no
// literal "{cause}" or "{component}" surviving — rendered, not read.
func TestBootFailureRendersCauseNotLiteral(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register("harness.stub", nil, wiredModule{name: "harness.stub"}); err != nil {
		t.Fatalf("seeding collision: %v", err)
	}

	w, err := bootCore("boot-render-cause", reg)
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

	display := e.Display()
	if strings.Contains(display, "{cause}") {
		t.Fatalf("Display() = %q renders {cause} literally; want the real reason", display)
	}
	if strings.Contains(display, "{component}") {
		t.Fatalf("Display() = %q renders {component} literally; want the component name", display)
	}
	if e.Wrapped == nil {
		t.Fatal("boot failure must wrap the underlying registry error")
	}
	if !strings.Contains(display, e.Wrapped.Error()) {
		t.Fatalf("Display() = %q does not contain the wrapped cause %q", display, e.Wrapped.Error())
	}
}

// TestBootFailureWrapSitesAllSupplyCause is a STRUCTURAL gate over boot.go's
// own source: every errs.Wrap(codeBootFailure, ...) call site must carry both
// the "component" and "cause" keys in its ctx map, so the MET-E900 template
// ("metropolis failed to boot: {component} failed to initialize: {cause}")
// renders the real failure reason instead of a literal {cause}.
//
// This is deliberately a source-structure check, not a runtime drive. The
// non-registry wrap sites (engine.compose, StartSubscriptionPump, the
// Subscribe/RegisterFundingAdjustKeys prime paths, the ScreenRegistry/
// RegisterGlobal wiring) can only fail on states a test cannot reach through
// bootCore — an already-composed engine, a struct-copied engine, a broken
// transport — so a runtime proof would be a fabricated harness (the original
// TestBootFailureWrapComponentsRenderCause built its own inline ctx and
// tested errs.Wrap, not boot.go). What CAN be checked honestly is that every
// site supplies the keys: remove "cause" from ANY boot-failure site and this
// test fails RED. The registry site's rendered-output proof lives in
// TestBootFailureRendersCauseNotLiteral above, which drives a real bootCore
// failure end to end.
//
// The site count is deliberately NOT asserted (GR#15): the invariant is that
// EVERY errs.Wrap(codeBootFailure, ...) site supplies the keys, whatever the
// count is. A hardcoded count would silently go stale as bootCore grows new
// failure paths (as it did when FEAT-211's ScreenRegistry.Register and
// RegisterGlobal wraps landed on main).
func TestBootFailureWrapSitesAllSupplyCause(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatalf("read boot.go: %v", err)
	}

	// Every site is a flat ctx map literal with no nested braces:
	//   errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
	//       "component": "...",
	//       "cause":     err.Error(),
	//   })
	// (the foundation.registry site additionally carries "key"; the
	// chromeGrammar loop site passes w.correlationID rather than the
	// function-local correlationID). A negated brace class cleanly captures
	// the map through its "})" close.
	wrapSite := regexp.MustCompile(`errs\.Wrap\(codeBootFailure, (?:correlationID|w\.correlationID), err, map\[string\]any\{[^}]*\}\)`)

	sites := wrapSite.FindAll(src, -1)
	if len(sites) == 0 {
		t.Fatal("no errs.Wrap(codeBootFailure, ...) sites found in boot.go — the regex has gone stale")
	}
	for i, site := range sites {
		if !bytes.Contains(site, []byte(`"component":`)) {
			t.Errorf("wrap site %d lacks a \"component\" key:\n%s", i, site)
		}
		if !bytes.Contains(site, []byte(`"cause":`)) {
			t.Errorf("wrap site %d lacks a \"cause\" key (MET-E900 would render literal {cause}):\n%s", i, site)
		}
		if !bytes.Contains(site, []byte(`err.Error()`)) {
			t.Errorf("wrap site %d does not source its cause from err.Error():\n%s", i, site)
		}
	}
}

// --- AC-1a / AC-5a: real protocol -> engine.core (via compose) ->
// ui.core -> ui.screen.map wiring, driven entirely through int.protocol's
// own API, nothing mocked ---

// TestIntegration_RealEngineBootsAndServesEngineStatus proves the FEAT-082
// flip: bootCore now constructs a real *enginecore.Engine wired by the
// composition root, not harness.stub.StubEngine, and that engine serves
// its registered views.
//
// BUG-323 INVERTED HALF OF THIS TEST. It previously asserted that the
// boot-time MapScreen Subscribe ("f1.viewport") was REJECTED, recording
// as "the honest baseline-one state" the very defect BUG-323 was later
// raised against: f1.viewport had no registered view, so F1 — the
// DEFAULT screen at boot — rendered entirely blank. compose's
// viewRegistrationOrder now registers it (internal/engine/compose/
// viewport_publish.go), so the same Subscribe must be ACCEPTED. The
// assertion is kept, with its sense flipped, rather than deleted: it is
// the cheapest possible tripwire for the registration being removed
// again.
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
	// own doc comment), so the boot-time MapScreen Subscribe's own
	// CommandResult is not observable via a raw channel read here. The
	// view-serving claim is instead proven by issuing the same Subscribes
	// ourselves, through router.RegisterResultHandler — an equivalent,
	// race-free proof of the same real-engine behaviour.
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

	if res := send(protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"}); !res.Accepted {
		t.Fatalf("f1.viewport Subscribe was REJECTED by the real engine (%+v) — compose no longer registers the map's view, so F1 renders blank at boot (BUG-323)", res.Error)
	}
	// A name nothing registers must still be rejected — otherwise the
	// assertion above would pass for an engine that accepts everything.
	if res := send(protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f9.not-a-view"}); res.Accepted {
		t.Fatalf("an unregistered view name was Accepted — the registered-view lookup is not gating anything")
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
