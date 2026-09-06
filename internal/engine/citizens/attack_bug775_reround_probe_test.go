package citizens

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-775 RE-ROUND (attacker "opus-reround-bug775") — independent residency
// probe over the NEW GatherInShardOrder sliding window. Samples the real
// resident-shard set from inside the walk (once per delivered id) and reports
// peak residency, peak pins, and shard-entry (load) events. The fix's claims
// under test: peak residency <= budget, and every shard loads at most once
// for a whole full-population walk (~256 entries total, not thousands).

func residentSet(c *CitizensAPI) (map[int]bool, int, int) {
	c.pagingMu.Lock()
	defer c.pagingMu.Unlock()
	set := make(map[int]bool, 64)
	pins := 0
	for i := range c.cold {
		if c.cold[i] != nil {
			set[i] = true
		}
		pins += int(c.shardPins[i])
	}
	return set, len(set), pins
}

func TestAttackBug775ReroundResidencyProbe(t *testing.T) {
	for _, budget := range []int{32, 64} {
		t.Run(fmt.Sprintf("budget%d", budget), func(t *testing.T) {
			const n = 20000
			api := pagedCity(t, n, budget, t.TempDir())
			ids := idsOf(n)
			// Warm to a genuinely paged steady state (unpinned walk).
			for _, id := range ids {
				api.CitizenAt(id, "probe-warm")
			}
			_, base, basePins := residentSet(api)
			if base > budget || basePins != 0 {
				t.Fatalf("precondition: resident=%d pins=%d budget=%d", base, basePins, budget)
			}

			prev, _, _ := residentSet(api)
			maxResident, maxPins := base, 0
			entries := 0
			entryCount := map[int]int{}
			delivered := 0
			shardOrder := []int{}
			lastShard := -1

			api.GatherInShardOrder(ids, "probe", func(id uint64, cit Citizen, ok bool) {
				delivered++
				s := det.ShardForEntity(id)
				if s != lastShard {
					shardOrder = append(shardOrder, s)
					lastShard = s
				}
				cur, cnt, pins := residentSet(api)
				if cnt > maxResident {
					maxResident = cnt
				}
				if pins > maxPins {
					maxPins = pins
				}
				for k := range cur {
					if !prev[k] {
						entries++
						entryCount[k]++
					}
				}
				prev = cur
			})

			_, after, afterPins := residentSet(api)
			reloaded := 0
			for _, v := range entryCount {
				if v > 1 {
					reloaded++
				}
			}
			ascending := true
			for i := 1; i < len(shardOrder); i++ {
				if shardOrder[i] <= shardOrder[i-1] {
					ascending = false
				}
			}
			t.Logf("budget=%d n=%d delivered=%d  PEAK resident=%d peak pins=%d | shard entries(loads)=%d distinct=%d reloaded>1=%d | distinct shard groups=%d ascending=%v | AFTER resident=%d pins=%d",
				budget, n, delivered, maxResident, maxPins, entries, len(entryCount), reloaded, len(shardOrder), ascending, after, afterPins)

			if delivered != n {
				t.Errorf("delivery gap: %d of %d ids delivered", delivered, n)
			}
			if maxResident > budget {
				t.Errorf("RESIDENCY BLOWN: peak %d resident shards vs budget %d", maxResident, budget)
			}
			if maxPins > budget {
				t.Errorf("PIN WINDOW BLOWN: peak %d pins vs budget %d", maxPins, budget)
			}
			if afterPins != 0 {
				t.Errorf("PIN LEAK: %d pins after walk", afterPins)
			}
			if entries > 2*numColdShards {
				t.Errorf("THRASHING: %d shard load events for a walk spanning %d shards", entries, len(entryCount))
			}
			if !ascending {
				t.Errorf("shard visitation not strictly ascending")
			}
		})
	}
}
