package proj

// SF-9 (race safety): go test ./internal/ui/screens/proj/... -race
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
	s.BindSubscription("sub-proj")

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
			raw, err := json.Marshal(wirePatch{
				SchemaVersion: 1,
				HorizonMonths: 72,
				Curves: []wireCurve{
					{Key: "water.demand", Status: "available", History: []float64{float64(i)}, Projection: []float64{float64(i + 1)}},
				},
				RateOutlook: &wireRateOutlook{Status: "available", Projection: []float64{float64(i), float64(i) + 0.5}},
			})
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-proj", Seq: uint64(i), Patch: raw})
		}
	}()

	// Render/accessor goroutine: reads state and renders repeatedly, then
	// signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(40, 6)
		rect := core.Rect{X: 0, Y: 0, W: 40, H: 6}
		for j := 0; j < 500; j++ {
			curves, _ := s.Curves()
			for _, c := range curves {
				RenderCurve(buf, rect, c, widgets.DefaultPalette)
			}
			rate, ok, _ := s.RateOutlook()
			if ok {
				RenderRateOutlook(buf, rect, rate, widgets.DefaultPalette)
			}
			_, _ = s.HorizonMonths()
			_ = s.Stale()
			s.SetStale(j%2 == 0)
		}
	}()

	wg.Wait()
}
