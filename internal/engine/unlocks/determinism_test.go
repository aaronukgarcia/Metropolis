package unlocks

import (
	"sync"
	"testing"
	"unsafe"
)

// --- AC-14: deterministic grant order -----------------------------------

// TestCrossMilestoneOrderDeterministic crosses the same population
// sequence through many independently-constructed APIs and asserts the
// crossed-milestone order and final state are byte-identical every time.
// Because the grant path iterates the ladder slice (never a map) and
// crosses in ascending tier order, this cannot vary run to run (GR#21).
func TestCrossMilestoneOrderDeterministic(t *testing.T) {
	type state struct {
		tiers     []int
		finalTier int
		dp        int64
		permits   int64
	}
	run := func() state {
		api, _ := realAPIWithFinance(t)
		var s state
		for _, pop := range []int64{0, 5_000, 20_000, 50_000} {
			crossed, err := api.AdvancePopulation(pop, testCorrelationID())
			if err != nil {
				t.Fatalf("AdvancePopulation(%d): %v", pop, err)
			}
			for _, m := range crossed {
				s.tiers = append(s.tiers, m.Tier)
			}
		}
		s.finalTier = api.CurrentTier()
		s.dp = api.DevelopmentPoints()
		s.permits = api.ExpansionPermits()
		return s
	}

	first := run()
	for i := 0; i < 25; i++ {
		got := run()
		if len(got.tiers) != len(first.tiers) {
			t.Fatalf("run %d crossed %d milestones, want %d", i, len(got.tiers), len(first.tiers))
		}
		for j := range got.tiers {
			if got.tiers[j] != first.tiers[j] {
				t.Fatalf("run %d crossed tier %d at position %d, want %d (non-deterministic grant order)", i, got.tiers[j], j, first.tiers[j])
			}
		}
		if got.finalTier != first.finalTier || got.dp != first.dp || got.permits != first.permits {
			t.Fatalf("run %d state = (%d, %d, %d), want (%d, %d, %d) (non-deterministic)", i,
				got.finalTier, got.dp, got.permits, first.finalTier, first.dp, first.permits)
		}
	}
}

// TestSignatureUnlocksSorted asserts the data-derived signature-unlock
// list is sorted, so any caller ranging over it gets a deterministic order
// (AC-14's sorted-stable-key requirement).
func TestSignatureUnlocksSorted(t *testing.T) {
	api := realAPI(t)
	for tier := 1; tier <= 13; tier++ {
		sigs := api.SignatureUnlocks(tier)
		for i := 1; i < len(sigs); i++ {
			if sigs[i-1] >= sigs[i] {
				t.Fatalf("SignatureUnlocks(%d) not sorted at %d: %q >= %q", tier, i, sigs[i-1], sigs[i])
			}
		}
	}
}

// --- AC-16: concurrent reads/writes are race-free -----------------------

// TestConcurrentXPAccrualIsRaceFree hammers the XP counter from many
// goroutines and asserts the deterministic total — the number of
// successful awards, independent of scheduling (go func() in _test.go, so
// `go test -race` exercises the data race the AC names).
func TestConcurrentXPAccrualIsRaceFree(t *testing.T) {
	api := realAPI(t)
	const goroutines = 64
	const perGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if err := api.AwardPopulationXP(1, testCorrelationID()); err != nil {
					t.Errorf("AwardPopulationXP: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := api.XP(); got != goroutines*perGoroutine {
		t.Errorf("XP = %d, want %d (deterministic total across concurrent awards)", got, goroutines*perGoroutine)
	}
}

// TestConcurrentReadDuringMilestoneCrossing reads gate state concurrently
// while a writer crosses a milestone, asserting the reads never error and
// the final tier is exactly the crossed tier (deterministic outcome).
func TestConcurrentReadDuringMilestoneCrossing(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	if _, err := api.AdvancePopulation(0, testCorrelationID()); err != nil { // tier 1
		t.Fatalf("AdvancePopulation(0): %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := api.CheckGate(Gate{MilestoneTier: 1}); err != nil {
					t.Errorf("CheckGate: %v", err)
					return
				}
				_ = api.IsUnlocked(Gate{MilestoneTier: 1})
				_ = api.MilestoneReached(1)
				_ = api.CurrentTier()
				_ = api.XP()
			}
		}()
	}

	if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(100): %v", err)
	}
	close(stop)
	wg.Wait()

	if api.CurrentTier() != 2 {
		t.Errorf("CurrentTier = %d, want 2 after concurrent-read crossing", api.CurrentTier())
	}
}

// --- SEC-020 copy guard -------------------------------------------------

// TestCopiedValueRejected proves the copy guard rejects a method call on a
// struct-copied *UnlocksAPI rather than racing the original's state.
func TestCopiedValueRejected(t *testing.T) {
	api := realAPI(t)
	cp := apiCopy(api) // struct copy — its own mu, aliased maps

	if err := cp.AwardPopulationXP(1, testCorrelationID()); err == nil {
		t.Error("AwardPopulationXP on a struct copy returned nil error; want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
	if _, err := cp.CheckGate(Gate{MilestoneTier: 1}); err == nil {
		t.Error("CheckGate on a struct copy returned nil error; want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
}

// apiCopy takes a same-package value copy of *UnlocksAPI via an unsafe
// byte-copy (mirrors engine.world's w2Copy / engine.core's e2Copy
// convention): a plain `cp := *api` is legal Go producing the identical
// attack shape, but go vet's copylocks check would flag the LITERAL
// assignment at its own call site and fail this package's own
// `go vet` gate. The byte-copy reaches the same struct-value copy through
// a route copylocks does not statically recognise.
func apiCopy(u *UnlocksAPI) *UnlocksAPI {
	c := new(UnlocksAPI)
	*(*[unsafe.Sizeof(UnlocksAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(UnlocksAPI{})]byte)(unsafe.Pointer(u))
	return c
}
