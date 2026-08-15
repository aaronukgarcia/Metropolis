package chrome

import (
	"fmt"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestConcurrentApplyRenderAndAlert is AC-16: the delta/event-consuming
// goroutine (AddAlert/ApplyFiguresPatch) and the render/read path (Render/
// Alerts/Top/Figures) run concurrently with no data race. The goroutine
// hammer itself only asserts what any schedule guarantees — every distinct
// alert ID was ingested exactly once and is present at the end — the
// data-race check is `go test -race`, which this test exists to give a
// meaningful concurrent interleaving to.
func TestConcurrentApplyRenderAndAlert(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})
	c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 0, 1, 0, 0, "AA"))

	const n = 50
	var wg sync.WaitGroup

	// Writer goroutine: the T-VIEWS-side caller ingesting alerts and figures.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			a, err := NewAlert(
				fmt.Sprintf("a%d", i), "alert", Tier(i%3), i%2 == 0, drill("f1"), protocol.Tick(int64(i)))
			if err != nil {
				t.Errorf("NewAlert: %v", err)
				return
			}
			if err := c.AddAlert(a); err != nil {
				t.Errorf("AddAlert: %v", err)
				return
			}
			c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", i%30, i%4, int64(i), int64(i), "AA"))
		}
	}()

	// Reader goroutine: T-RENDER rendering and reading snapshots.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := core.NewBuffer(80, 10)
		rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
		for i := 0; i < n*2; i++ {
			c.Render(buf, rect)
			_ = c.Alerts()
			_, _ = c.Top()
			_ = c.Figures()
		}
	}()

	wg.Wait()

	if got := len(c.Alerts()); got != n {
		t.Fatalf("after concurrent ingest, stack has %d alerts, want %d", got, n)
	}
}
