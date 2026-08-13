package core

import (
	"sync"
	"testing"
)

// BUG-016: seal() gained an atomic fast path (sealedFast) so that every
// AdvanceTicks call after the first, for the life of an Engine, resolves
// without ever acquiring e.mu. These tests prove the fast path did not
// reopen anything SEC-003/SEC-016 closed.
//
// TestBUG016_ConcurrentSeal_Race is the "sealer-vs-sealer" race: many
// goroutines call seal() concurrently on a fresh, unsealed Engine, so
// some of them race for the FIRST call (which takes the slow, mu-guarded
// path and does the real Store) while the rest may observe sealedFast
// already true and take the fast, lock-free path — all in the same
// timing window, which is exactly the case double-checked locking must
// get right. Run under -race: any missing synchronization between the
// sealedFast.Store in the slow path and the sealedFast.Load in the fast
// path would show up here as a data race on e.sealed itself (the slow
// path also reads/writes it), not merely as a logic bug.
func TestBUG016_ConcurrentSeal_Race(t *testing.T) {
	e := NewEngine(WithPoolSize(4))

	const goroutines = 200
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			errCh <- e.seal("bug016-race")
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("seal() under concurrent race: unexpected error %v (want nil every time — seal is idempotent)", err)
		}
	}

	if !e.sealedFast.Load() {
		t.Fatalf("sealedFast = false after concurrent seal() calls, want true")
	}
	e.mu.Lock()
	sealed := e.sealed
	e.mu.Unlock()
	if !sealed {
		t.Fatalf("sealed = false after concurrent seal() calls, want true")
	}
}

// TestBUG016_ConcurrentAdvanceTicks_FastPathHammer drives the SAME
// pattern as SEC-003's concurrent stress test (sec003_poc_test.go) but
// specifically targets the fast path added here: many goroutines call
// AdvanceTicks concurrently against a single, already-sealed Engine, so
// the overwhelming majority of seal() calls they trigger take the
// lock-free fast path. Asserts every call either succeeds or fails
// cleanly (never a race, never a partial/corrupt tick) and that
// tickCounter — the lock-free, independent observation point
// (engine.go's tickCounter field doc) — ends up exactly consistent with
// the number of calls that reported success, proving the fast path did
// not let any tick silently skip or double-apply its effects.
//
// AdvanceTicks is documented as a command-level API, not one designed
// for genuinely concurrent multi-goroutine callers (there is no
// production call site that does this) — this test exists to prove the
// FAST PATH specifically does not corrupt state if it ever were called
// that way, matching the level of scrutiny SEC-003 already applied to
// seal()'s mutex-only predecessor, not to bless concurrent AdvanceTicks
// as a supported usage pattern.
func TestBUG016_ConcurrentAdvanceTicks_FastPathHammer(t *testing.T) {
	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("seed RegisterPhaseHook: %v", err)
	}

	const goroutines = 50
	const callsEach = 100
	var wg sync.WaitGroup
	successCount := make([]int, goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < callsEach; i++ {
				if err := e.AdvanceTicks("bug016-hammer", 1); err != nil {
					t.Errorf("AdvanceTicks: unexpected error %v", err)
					return
				}
				successCount[g]++
			}
		}()
	}
	wg.Wait()

	wantTicks := uint64(0)
	for _, c := range successCount {
		wantTicks += uint64(c)
	}
	if got := e.tickCounter.Load(); got != wantTicks {
		t.Fatalf("tickCounter = %d after concurrent AdvanceTicks hammer, want %d (successful calls)", got, wantTicks)
	}
}
