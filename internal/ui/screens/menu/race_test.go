package menu

// SF-9 (race safety): go test ./internal/ui/screens/menu/... -race
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
	s.BindSubscription(ViewSession, "sub-session")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Delta-applying goroutine: applies f10.session deltas continuously
	// until stop is closed by the render goroutine below.
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
				SubscriptionID: "sub-session",
				Seq:            uint64(i),
				Patch: mustJSONNoT(t, wireSessionPatch{
					SchemaVersion: 1, WorldSeed: int64(i), Tick: int64(i), GameMonth: int64(i % 12), Paused: i%2 == 0, Speed: 1,
				}),
			})
		}
	}()

	// Render/accessor goroutine: reads session state and renders
	// repeatedly, then signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(80, 2)
		rect := core.Rect{X: 0, Y: 0, W: 80, H: 2}
		for j := 0; j < 500; j++ {
			session, _ := s.Session()
			RenderSession(buf, rect, session, tcell.StyleDefault)
			_ = s.SaveEntries()
			s.SetStale(ViewSession, j%2 == 0)
			_ = s.Stale(ViewSession)
		}
	}()

	wg.Wait()
}

// mustJSONNoT marshals v to JSON, reporting a failure via t.Errorf (safe
// from any goroutine, unlike t.Fatalf) — mirrors ui.screen.demo's
// mustMarshalNoT.
func mustJSONNoT(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Errorf("json.Marshal: %v", err)
		return nil
	}
	return b
}
