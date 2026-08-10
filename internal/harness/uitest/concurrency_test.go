package uitest

import (
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestConcurrentKeyInjectionAndDeltaConsumption is AC-12: the harness's
// key-injection path (T-INPUT, driven here from an explicit test
// goroutine) and its delta-consumption path (T-VIEWS, an attached
// fixture's ViewsLoop goroutine) run concurrently and must be race-clean
// under that split, mirroring UI-SPEC §1's real T-INPUT/T-VIEWS
// topology. Run with -race (the mandatory baseline for this item).
func TestConcurrentKeyInjectionAndDeltaConsumption(t *testing.T) {
	fx := buildFixture(t, 6)

	h := NewHarness(errs.NewCorrelationID(), func(uicore.InputMsg) {}, countDraw)
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			if err := h.SendKeys("b"); err != nil {
				t.Errorf("SendKeys: %v", err)
				return
			}
		}
	}()

	if err := h.AwaitDeltas(6, 2*time.Second); err != nil {
		t.Fatalf("AwaitDeltas: %v", err)
	}
	wg.Wait()

	if _, err := h.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := h.Capture(); err != nil {
		t.Fatalf("Capture: %v", err)
	}
}
