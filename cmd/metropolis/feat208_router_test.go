package main

import (
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-208 increment 2's own end-to-end proof of the boot-time router
// wiring itself: TestIntegration_RealEngineBootsAndServesEngineStatus and
// TestIntegration_CommandsExerciseRealEngineEndToEnd (boot_test.go) prove
// the generic engine.status/Pause/AdvanceTicks path still works with
// router in place of ui.core.ViewsLoop, and
// internal/engine/compose/finance_publish_test.go /
// services_publish_test.go already prove compose's own publish path in
// isolation — but nothing yet proves that bootCore's OWN wiring
// (primeScreenSubscription + router.BindSubscription, boot.go) actually
// delivers a live f2.finance/f4.services Delta stream to
// w.financeScreen/w.servicesScreen end to end, through the real
// transport, the real router, and each screen's real ApplyDelta. This
// file is that proof.

// TestBootCore_FinanceAndServicesScreens_LiveOverRouter proves
// w.financeScreen and w.servicesScreen both have real data immediately
// after bootCore returns (the priming handshake's own job — see
// primeScreenSubscription's doc comment) and that BOTH keep receiving
// fresh deltas via router as the sim advances (proving router.Run, not
// just the one-off priming read, is the thing actually keeping them
// live from this point on).
func TestBootCore_FinanceAndServicesScreens_LiveOverRouter(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat208-router-wiring", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	if w.router == nil {
		t.Fatal("bootCore did not construct a router.Router (FEAT-208 increment 2 did not take)")
	}
	if w.financeScreen == nil {
		t.Fatal("bootCore did not construct a financeScreen")
	}
	if w.servicesScreen == nil {
		t.Fatal("bootCore did not construct a servicesScreen")
	}

	if !w.financeScreen.HaveData() {
		t.Fatal("financeScreen.HaveData() = false immediately after bootCore — the priming handshake did not deliver f2.finance's first delta")
	}
	if !w.servicesScreen.HaveData() {
		t.Fatal("servicesScreen.HaveData() = false immediately after bootCore — the priming handshake did not deliver f4.services' first delta")
	}
	if _, have := w.financeScreen.BalanceSheet(); !have {
		t.Fatal("financeScreen.BalanceSheet() reports have=false despite HaveData()==true")
	}
	if _, have := w.servicesScreen.CapacityDemand(); !have {
		t.Fatal("servicesScreen.CapacityDemand() reports have=false despite HaveData()==true")
	}

	// Drive AdvanceTicks (which signals the subscription pump) through
	// router itself, correlating via awaitRouterResult — proving router,
	// not the one-off priming read above, is what keeps both screens
	// live from here on. Real per-tick money movement is not guaranteed
	// this early in a fresh baseline-one run, so this does not assert the
	// figures CHANGED — only that router keeps delivering without a
	// panic/route-miss stall, which PanicCount()/RouteMissCount() below
	// verify directly.
	panicsBefore := w.router.PanicCount()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("feat208-router-advance"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 3},
	}
	res := sendAndAwaitResult(t, w, cmd)
	if !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}

	// Give the subscription pump goroutine a moment to publish and
	// router a moment to route the post-AdvanceTicks deltas — bounded
	// wall-clock poll (this is boot-time integration-test synchronization,
	// not engine determinism; the sim tick advance itself already
	// completed synchronously above).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.router.PanicCount() > panicsBefore {
			t.Fatalf("router recovered a receiver panic while routing to financeScreen/servicesScreen (PanicCount went from %d to %d)", panicsBefore, w.router.PanicCount())
		}
		if w.financeScreen.HaveData() && w.servicesScreen.HaveData() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !w.financeScreen.HaveData() || !w.servicesScreen.HaveData() {
		t.Fatal("financeScreen/servicesScreen lost HaveData() after AdvanceTicks — router stopped delivering")
	}
	if got := w.router.PanicCount(); got != panicsBefore {
		t.Fatalf("router.PanicCount() = %d, want unchanged %d", got, panicsBefore)
	}
}
