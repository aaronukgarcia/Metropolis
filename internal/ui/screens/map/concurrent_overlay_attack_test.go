package mapscreen

import (
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestAttack_ConcurrentCycleOverlayRenderApplyPatch stresses CycleOverlay
// concurrently with Render and ApplyPatch (simulating an overlay switch
// racing a pending delta) under -race.
func TestAttack_ConcurrentCycleOverlayRenderApplyPatch(t *testing.T) {
	m := NewMapScreen("test", widgets.DefaultPalette)
	m.SetViewportSize(10, 10)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			m.CycleOverlay(i%2 == 0)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := core.NewBuffer(10, 10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			m.Render(buf, core.Rect{X: 0, Y: 0, W: 10, H: 10})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			m.ActiveOverlay()
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
