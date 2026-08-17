package build

// SF-9 (race safety): go test ./internal/ui/screens/build/... -race
// -count=1 must pass with no data race between the delta-applying
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
	s.BindSubscription("sub-build")

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
			queue := *p.Queue
			queue[0].LeadTimeRemaining = int64(i)
			p.Queue = &queue
			raw, err := json.Marshal(p)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-build", Seq: uint64(i), Patch: raw})
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
			zones, have := s.Zones()
			RenderZones(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, zones, have, style)
			queue, have := s.Queue()
			RenderQueue(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, queue, have, style)
			catalogue, have := s.Catalogue()
			RenderCatalogue(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, catalogue, have, style)
			price, have := s.LandPrice()
			RenderLandPrice(buf, core.Rect{X: 0, Y: 0, W: 90, H: 2}, price, have, style)
			dem, have := s.Demolition()
			RenderDemolition(buf, core.Rect{X: 0, Y: 0, W: 90, H: 2}, dem, have, style)
			_, _ = s.DemolishCost(protocol.CellRef{X: 2, Y: 3})
			_ = s.Stale()
			s.SetStale(j%2 == 0)
		}
	}()

	wg.Wait()
}
