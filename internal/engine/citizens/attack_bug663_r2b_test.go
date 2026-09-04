package citizens

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// TestR2SplitLockWindowStress: much wider concurrency than A1 -- many
// in-flight Enqueue/RealiseByID pairs at once, so the scheduler actually
// preempts inside Enqueue's q.mu-released-but-shardMu-not-yet-taken window.
func TestR2SplitLockWindowStress(t *testing.T) {
	const batches = 400
	const perBatch = 64
	dq := NewDeathQueue()
	next := uint64(1)
	diverged := 0
	for b := 0; b < batches; b++ {
		ids := make([]uint64, perBatch)
		for i := range ids {
			ids[i] = next
			next++
		}
		var wg sync.WaitGroup
		for _, id := range ids {
			id := id
			wg.Add(2)
			go func() { defer wg.Done(); _ = dq.Enqueue(id, 1, "r2") }()
			go func() {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					if err := dq.RealiseByID(id, 2, "r2"); err == nil {
						return
					}
				}
			}()
		}
		wg.Wait()
		for _, id := range ids {
			inIdx := dq.IsQueuedInShard(det.ShardForEntity(id), id, "r2")
			_, inQ := dq.IsQueued(id, "r2")
			if inIdx != inQ {
				diverged++
				if diverged <= 5 {
					t.Logf("DIVERGENCE id=%d index=%v queued=%v", id, inIdx, inQ)
				}
			}
		}
	}
	if diverged > 0 {
		t.Fatalf("index/queue diverged on %d of %d citizens under concurrent Enqueue/RealiseByID", diverged, batches*perBatch)
	}
}
