package census

// AC-15 (race safety): go test ./internal/ui/screens/census/... -race
// -count=2 must pass with no data race between the delta-applying
// goroutine and the render/accessor path.

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func TestConcurrentApplyDeltaAndRender_NoRace(t *testing.T) {
	s := New("corr-race")
	s.BindSubscription("sub-census-race")

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
			bands := *p.AgeBands
			bands[0] = int64(i)
			p.AgeBands = &bands
			raw := mustJSON(t, p)
			s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-census-race", Seq: uint64(i), Patch: raw})
			s.ApplyResult(protocol.CommandResult{CorrelationID: "corr-race", Accepted: i%2 == 0})
		}
	}()

	// Render/accessor goroutine: reads state and renders repeatedly, then
	// signals the delta goroutine to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		buf := core.NewBuffer(100, 40)
		style := widgets.DefaultPalette.Style(widgets.TokenMoney)
		for j := 0; j < 500; j++ {
			bands, have := s.AgeBandSeries()
			RenderAgeBandPyramid(buf, core.Rect{X: 0, Y: 0, W: 100, H: 8}, bands, have, widgets.DefaultPalette, style)
			sex, have := s.SexSeries()
			RenderSexSeries(buf, core.Rect{X: 0, Y: 8, W: 100, H: 4}, sex, have, widgets.DefaultPalette, style)
			tiers, have := s.EducationTierSeries()
			RenderEducationTierSeries(buf, core.Rect{X: 0, Y: 12, W: 100, H: 10}, tiers, have, style)
			bwc, have := s.BlueWhiteCollarSplit()
			RenderBlueWhiteCollar(buf, core.Rect{X: 0, Y: 22, W: 100, H: 4}, bwc, have, widgets.DefaultPalette, style)
			kpis, have := s.KPITiles()
			RenderKPITiles(buf, core.Rect{X: 0, Y: 26, W: 100, H: 9}, kpis, have, style)
			bio, haveBio := s.SelectedBio()
			RenderCitizenBio(buf, core.Rect{X: 0, Y: 35, W: 100, H: 5}, bio, haveBio, style)
			link, haveLink := s.EducationCrimeLinkageView()
			RenderEducationCrimeLinkage(buf, core.Rect{X: 0, Y: 39, W: 100, H: 1}, link, haveLink, style)
			_ = s.Stale()
			s.SetStale(j%2 == 0)
			_, _ = s.KPISource(KPIKeyHomeless)
			_ = DrillTargets(kpis, bio, haveBio)
		}
	}()

	wg.Wait()
}
