package districts

// AC-12 (race safety): go test ./internal/ui/screens/districts/... -race
// -count=2 must pass with no data race between the delta-applying
// goroutine and the render/accessor path.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestConcurrentApplyDeltaAndRender_NoRace(t *testing.T) {
	s := New("corr-race")
	s.BindSubscription("sub-districts")
	s.SetSelectedDistrict("harbour")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Delta-applying goroutine: applies full patches continuously until
	// stop is closed by the render goroutine below.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			p := fullPatch()
			ts := *p.TaxSettings
			ts[0].Multiplier = float64(i % 10)
			p.TaxSettings = &ts
			raw, err := json.Marshal(p)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-districts", Seq: uint64(i), Patch: raw})
			s.ApplyResult(protocol.CommandResult{CorrelationID: "corr-race", Accepted: i%2 == 0})
		}
	}()

	// Render/accessor goroutine: reads state and renders repeatedly, then
	// signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(90, 6)
		for j := 0; j < 500; j++ {
			settings, have := s.TaxSettings()
			RenderTaxSettings(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, settings, s.SelectedDistrict(), s.TaxRejectedReason(), have, testStyle)
			RenderBlockedFeature(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, "POLICY LIBRARY", testStyle)
			_ = s.Stale()
			s.SetStale(j%2 == 0)
			_, _ = s.Districts()
			_, _ = s.TaxSettings()
			s.SetSelectedDistrict("harbour")
		}
	}()

	wg.Wait()
}
