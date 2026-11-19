package main

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1239: viewTicks records, per bound view, the simulation tick of the last
// delta that view's receiver has APPLIED. The engine publishes every
// subscribed view each pump cycle, stamped with one tick, and the router
// applies deltas in order, so "every view has applied a delta stamped at
// least tick T" means every published view reflects the engine at tick T or
// later. Tests wait on that instead of guessing how long publishing takes.
//
// It is write-once at boot: wrap is called from bootCore's own goroutine, one
// call per view, while it primes the subscriptions. The wrapped functions are
// NOT only called after router.Run starts: during priming a forwarded delta
// reaches them on bootCore's goroutine, and afterwards on the router's. The map
// is never written after bootCore returns (so a reader needs no lock once it
// has the wiring); only the per-view atomics change at run time.
type viewTicks struct {
	byView map[string]*viewTickEntry
}

type viewTickEntry struct {
	tick    atomic.Int64
	applied atomic.Bool
}

// viewApplyGate is nil in the game. A test sets it (before booting, and never
// in a parallel test) to hold a view's delta before it is applied, so a wait
// on that view can be proven to wait.
var viewApplyGate func(view string, d protocol.Delta)

func newViewTicks() *viewTicks { return &viewTicks{byView: make(map[string]*viewTickEntry)} }

// wrap returns apply with the tick bookkeeping added AFTER the delta has been
// applied, so a recorded tick always means the view already shows it. Must be
// called only during boot (it writes the map).
func (v *viewTicks) wrap(view string, apply func(protocol.Delta)) func(protocol.Delta) {
	e := &viewTickEntry{}
	v.byView[view] = e
	return func(d protocol.Delta) {
		// Test seam: nil in the game; a test sets it to hold a view's delta.
		if gate := viewApplyGate; gate != nil {
			gate(view, d)
		}
		apply(d)
		e.tick.Store(int64(d.Tick))
		e.applied.Store(true)
	}
}

// settledTick is the lowest tick stamped on the last delta of every view that
// has received one (views the engine does not serve have none and are
// skipped). It is 0 when no view has applied anything.
func (v *viewTicks) settledTick() int64 {
	min := int64(-1)
	for _, e := range v.byView {
		if !e.applied.Load() {
			continue
		}
		if t := e.tick.Load(); min < 0 || t < min {
			min = t
		}
	}
	if min < 0 {
		return 0
	}
	return min
}
