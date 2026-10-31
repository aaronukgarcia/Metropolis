package main

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	debugscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
)

// Destructive round r2 (FEAT-2326609837, GR#23) pin for the r1 BUG-602
// advisory: with debug mode off, F12's Draw path must draw the notice and
// perform NO Collect (Collect reads the clock and memstats every frame).
// The runtime source is Collect's only clock/memstats read, so a counting
// runtime source counts Collect calls. The flag is flipped on mid-test to
// prove the counter is live (a vacuous zero would otherwise pass).
func TestWireTUI_Attack_DebugOffDrawNeverCollects(t *testing.T) {
	var on bool
	collects := 0
	ds := debugscreen.NewScreen(nil, errs.NewCorrelationID(),
		debugscreen.WithDebugFlag(func() bool { return on }),
		debugscreen.WithRuntimeSource(func() debugscreen.RuntimeMetrics {
			collects++
			return debugscreen.RuntimeMetrics{}
		}))
	draw := debugDrawFunc(ds)

	for i := 0; i < 3; i++ {
		buf := core.NewBuffer(twW, twH)
		draw(buf, nil)
		if !bufferContainsString(buf, twW, twH, "debug mode is off") {
			t.Fatalf("frame %d with debug off: notice not drawn", i)
		}
	}
	if collects != 0 {
		t.Fatalf("debug off: Draw called Collect %d times over 3 frames, want 0 (BUG-602 class)", collects)
	}

	on = true
	draw(core.NewBuffer(twW, twH), nil)
	if collects != 1 {
		t.Fatalf("debug on: Draw called Collect %d times, want 1 (counter seam is dead, the off-path check above is vacuous)", collects)
	}
}
