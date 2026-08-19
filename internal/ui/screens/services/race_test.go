package services

// SF-9 (race safety): go test ./internal/ui/screens/services/... -race
// -count=2 must pass with no data race between the delta-applying
// goroutine and the render/accessor path.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func TestConcurrentApplyDeltaAndRender_NoRace(t *testing.T) {
	s := New("corr-race")
	s.BindSubscription("sub-services")

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
			cd := *p.CapacityDemand
			cd[0].DemandUnits = float64(i)
			p.CapacityDemand = &cd
			raw, err := json.Marshal(p)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-services", Seq: uint64(i), Patch: raw})
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
		style := widgets.DefaultPalette.Style(widgets.TokenMoney)
		for j := 0; j < 500; j++ {
			sliders, have := s.Sliders()
			RenderSliders(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, sliders, s.FundingRejectedReason(), have, style)
			cd, have := s.CapacityDemand()
			RenderCapacityDemand(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, cd, have, widgets.DefaultPalette, style)
			rt, have := s.ResponseTimes()
			RenderResponseTimes(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, rt, have, style)
			wl, have := s.WaitingLists()
			RenderWaitingLists(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, wl, have, style)
			pie, have := s.PublicServicePie()
			RenderPublicServicePie(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, pie, have, style)
			_ = s.Stale()
			s.SetStale(j%2 == 0)
			_, _ = s.Sliders()
		}
	}()

	wg.Wait()
}
