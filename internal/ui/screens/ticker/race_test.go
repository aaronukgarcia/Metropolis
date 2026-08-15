package ticker

// SF-9 (race safety): go test ./internal/ui/screens/ticker/... -race
// -count=1 must pass with no data race between the delta-applying
// goroutine and the render/accessor/search path.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// marshalNoT marshals v to JSON, reporting a failure via t.Errorf (safe
// from any goroutine, unlike t.Fatalf) — mirrors ui.screen.demo's
// mustMarshalNoT.
func marshalNoT(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Errorf("json.Marshal: %v", err)
		return nil
	}
	return b
}

func TestConcurrentApplyDeltaAndRender_NoRace(t *testing.T) {
	s := New("corr-race")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.BindSubscription(ViewBulletin, "sub-bull")
	s.BindSubscription(ViewAnnual, "sub-ann")
	s.BindSubscription(ViewArchive, "sub-arch")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Delta-applying goroutine: applies deltas continuously until stop is
	// closed by the render goroutine below.
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
				SubscriptionID: "sub-tick",
				Seq:            uint64(i),
				Patch: marshalNoT(t, wireTickerPatch{
					SchemaVersion: 1,
					Events:        []wireStory{{EventID: "evt-1", Tick: int64(i), Name: "Pent Lane", Text: "queue clears"}},
				}),
			})
			s.ApplyDelta(protocol.Delta{
				SubscriptionID: "sub-arch",
				Seq:            uint64(i),
				Patch: marshalNoT(t, wireArchivePatch{
					SchemaVersion: 1,
					Stories:       []wireStory{{EventID: "evt-1", Tick: int64(i), Name: "Pent Lane", Text: "queue clears"}},
				}),
			})
		}
	}()

	// Render/accessor/search goroutine: reads state, renders, and searches
	// repeatedly, then signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(60, 6)
		rect := core.Rect{X: 0, Y: 0, W: 60, H: 6}
		for j := 0; j < 500; j++ {
			events, have := s.Ticker()
			RenderTicker(buf, rect, events, s.ScrollStep(), have, tcell.StyleDefault)
			archive, haveArchive := s.Archive()
			RenderArchive(buf, rect, archive, haveArchive, s.SearchActive(), s.ArchiveStalled(), s.SearchMatchedCount(), tcell.StyleDefault)
			_ = s.Stale(ViewTicker)
			s.SetStale(ViewTicker, j%2 == 0)
			s.SearchStories("pent")
			_, _ = s.NextMatch()
			s.AdvanceScroll(1)
		}
	}()

	wg.Wait()
}
