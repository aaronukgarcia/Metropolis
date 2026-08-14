package finance

import (
	"sync"
	"testing"
)

// TestConcurrentPostingNoRace (AC-16) posts to the ledger concurrently
// from multiple goroutines (run under -race) and asserts the final totals
// are correct and race-free.
func TestConcurrentPostingNoRace(t *testing.T) {
	f := NewFinanceAPI("ac16")
	seedTreasury(t, f, gbp(1_000_000))

	const workers = 8
	const perWorker = 100

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := f.PostWages(gbp(1)); err != nil {
					t.Errorf("PostWages: %v", err)
				}
				if _, err := f.SettleOpex(gbp(1)); err != nil {
					t.Errorf("SettleOpex: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	// Each worker posts 100 wages (internal, neutral) + 100 opex (£1 out
	// each): the money stock falls by £800 from the £1,000,000 seed.
	if want := gbp(1_000_000) - gbp(800); f.TotalMoneyInCirculation() != want {
		t.Fatalf("total money = %d, want %d", f.TotalMoneyInCirculation(), want)
	}
	if got, want := f.TotalMoneyInCirculation(), f.RecomputeMoneyStock(); got != want {
		t.Fatalf("running total %d != recompute %d after concurrent posting", got, want)
	}
}
