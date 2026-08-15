package dash_test

import (
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestConcurrentSaveAndRender is AC-13: the layout editor's save path
// running concurrently with a live-updating dashboard render must be
// race-free (run with -race). It is a deterministic-concurrency test —
// the dashboard's own mutex constructs the state, the goroutines just
// drive the two paths to completion, and the assertions check what the
// mutex guarantees under any schedule (a saved profile always parses,
// a render never panics), not a timing-dependent ordering.
func TestConcurrentSaveAndRender(t *testing.T) {
	l := dash.DefaultLayout("f1")
	d := dash.NewDashboard(l, &recordingNavigator{}, nil)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			data, err := d.Save()
			if err != nil {
				t.Errorf("Save: %v", err)
				return
			}
			// A saved profile must always be a valid, loadable layout
			// regardless of where the editor was in its mutation cycle.
			if _, err := dash.UnmarshalLayout(data); err != nil {
				t.Errorf("Save produced an unloadable profile: %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		rect := core.Rect{X: 0, Y: 0, W: 40, H: 30}
		for i := 0; i < 200; i++ {
			buf := core.NewBuffer(40, 30)
			d.Render(buf, rect, widgets.DefaultPalette, tcell.StyleDefault)
		}
	}()

	wg.Wait()
}
