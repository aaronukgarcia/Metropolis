package demo

// SF-9 (race safety): go test ./internal/ui/screens/demo/... -race
// -count=1 must pass with no data race between the delta-applying
// goroutine and the render/accessor path.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestConcurrentApplyDeltaAndRender_NoRace(t *testing.T) {
	s := New("corr-race")
	s.BindSubscription(ViewPopulation, "sub-pop")
	s.BindSubscription(ViewLeisure, "sub-lei")
	s.BindSubscription(ViewHousing, "sub-hou")
	s.BindSubscription(ViewCommute, "sub-com")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Delta-applying goroutine: applies deltas continuously until stop
	// is closed by the render goroutine below.
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
			s.ApplyDelta(protocol.Delta{
				SubscriptionID: "sub-com",
				Seq:            uint64(i),
				Patch:          mustMarshalNoT(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: i, InCommuters: i}),
			})
			s.ApplyDelta(protocol.Delta{
				SubscriptionID: "sub-hou",
				Seq:            uint64(i),
				Patch: mustMarshalNoT(t, wireHousingPatch{SchemaVersion: 1, Full: true, Typologies: []wireTypology{
					{Typology: "Terrace", Demand: i, Stock: i},
				}}),
			})
		}
	}()

	// Render/accessor goroutine: reads state and renders repeatedly,
	// then signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(40, 4)
		rect := core.Rect{X: 0, Y: 0, W: 40, H: 4}
		for j := 0; j < 500; j++ {
			commute, _ := s.Commute()
			RenderCommuteLeak(buf, rect, commute, tcell.StyleDefault)
			typologies, _ := s.Typologies()
			RenderTypologies(buf, rect, typologies, tcell.StyleDefault)
			_ = s.Stale(ViewCommute)
			s.SetStale(ViewCommute, j%2 == 0)
		}
	}()

	wg.Wait()
}

// mustMarshalNoT marshals v to JSON, reporting a failure via t.Errorf
// (safe from any goroutine, unlike t.Fatalf) rather than panicking —
// these are fixed, known-marshalable wire structs, so a failure here
// would indicate a real bug worth reporting, not an expected condition.
func mustMarshalNoT(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Errorf("json.Marshal: %v", err)
		return nil
	}
	return b
}
