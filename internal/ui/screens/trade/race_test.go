package trade

// SF-9 (race safety): go test ./internal/ui/screens/trade/... -race
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
	s.BindSubscription("sub-trade")

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
			contracts := *p.Contracts
			contracts[0].PricePerUnitMicropounds = int64(i) * 1_000_000
			p.Contracts = &contracts
			raw, err := json.Marshal(p)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-trade", Seq: uint64(i), Patch: raw})
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
			contracts, have := s.Contracts()
			RenderContracts(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, contracts, have, style)
			junctions, have := s.Junctions()
			RenderJunctions(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, junctions, have, style)
			warehouse, have := s.Warehouse()
			RenderWarehouse(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, warehouse, have, style)
			port, have := s.Port()
			RenderPort(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, port, have, style)
			balance, have := s.Balance()
			RenderBalance(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, balance, have, style)
			safety, have := s.Safety()
			RenderSafety(buf, core.Rect{X: 0, Y: 0, W: 90, H: 6}, safety, have, style)
			_, _ = s.CancellationPenalty("c-1")
			_ = s.Stale()
			s.SetStale(j%2 == 0)
		}
	}()

	wg.Wait()
}
