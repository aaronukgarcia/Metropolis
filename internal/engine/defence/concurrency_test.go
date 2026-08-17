package defence

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestConcurrentBids_DeterministicLedgerTotal hammers BidForGrant from many
// goroutines while readers poll the mandate/grants surface, then asserts the
// one invariant every schedule must preserve: each Won bid credited exactly
// one award, so the treasury money stock equals won × award (AC-15's -race
// safety plus a schedule-independent conservation assertion — constructed
// state, never raced for timing).
func TestConcurrentBids_DeterministicLedgerTotal(t *testing.T) {
	d, f, _ := newWiredDefence(t, 99)
	cfg := validConfig()
	award := finance.Money(cfg.GrantPots[0].AwardMicropounds)

	const writers = 12
	const perWriter = 25
	var won int64

	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				r, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 100_000_000, Month: int64(i)})
				if err != nil {
					errCh <- err
					return
				}
				if r.Won {
					atomic.AddInt64(&won, 1)
				}
			}
		}(g)
	}
	// Concurrent read-only traffic exercises the RWMutex read path.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d.PendingMandates(100_000)
				d.FormulaSupport(1_000_000)
				d.ReputationPenalty()
				d.WageBillFactor()
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent bid error: %v", err)
	}

	wonCount := atomic.LoadInt64(&won)
	if wonCount == 0 {
		t.Fatal("no bids won — deterministic seed should produce at least one win")
	}
	want := finance.Money(wonCount) * award
	if got := f.TotalMoneyInCirculation(); got != want {
		t.Fatalf("treasury money stock = %d, want %d (won %d × award %d)", int64(got), int64(want), wonCount, int64(award))
	}
}
