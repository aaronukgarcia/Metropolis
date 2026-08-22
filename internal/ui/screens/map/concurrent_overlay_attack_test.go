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

	// Round D6: the same screen must also tolerate concurrent unknown-
	// terrain reporting. The earlier concurrent tests rendered a screen
	// with NO snapshot, so reportUnknown never fired and the
	// logUnknownTerrainOnce dedupe write (which used to be unsynchronised
	// "single-goroutine" state) was never exercised — that is exactly how
	// the race hid. This goroutine drives a grid full of an unrecognised
	// surface so the seen-write runs concurrently with the other Render.
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.ApplyPatch(unknownTerrainPatchRaw(t, "marsh", 10))
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

// TestAttack_ConcurrentLogUnknownTerrainOnce_NoRace is the deterministic
// BUG-317 round-D6 detector. The Render-level concurrent tests can't
// reliably catch the unsynchronised dedupe write: seen[terrain] is set
// true exactly ONCE per distinct surface, so a timing-based test (two
// goroutines racing a 100ms Render loop) rarely overlaps the single write
// with another goroutine's map read — the race exists but the window is
// microscopic. This test removes the timing lottery: a barrier releases N
// goroutines simultaneously, each hammering logUnknownTerrainOnce with
// the SAME map and the same key, so every iteration is a check-then-act
// on shared state. With the mutex, green under -race; with the mutex
// removed (the D6 mutation), the unsynchronised read/write on seen fires
// RED deterministically.
func TestAttack_ConcurrentLogUnknownTerrainOnce_NoRace(t *testing.T) {
	seen := make(map[string]bool)
	var mu sync.Mutex
	start := make(chan struct{})

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 500; j++ {
				logUnknownTerrainOnce(seen, &mu, "corr-race", "marsh")
			}
		}()
	}
	close(start)
	wg.Wait()

	// The dedupe must still hold after concurrent access: exactly one of
	// the 8*500 calls recorded the surface.
	if got := len(seen); got != 1 {
		t.Fatalf("seen has %d entries after concurrent logUnknownTerrainOnce, want exactly 1 (dedupe held under concurrency)", got)
	}
}
