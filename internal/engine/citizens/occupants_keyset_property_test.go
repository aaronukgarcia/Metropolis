package citizens

// FEAT-2326609842 r3 -- keyset-paging property test. A paging client steps
// afterID = the last id it received while deaths (official life-event path AND
// direct cold-pass-style removals that leave the index stale) and births are
// interleaved between its pages. Against a reference set of live ids kept at
// every step, no citizen that was live THROUGHOUT the walk may be skipped, no
// citizen may be listed twice, pages must ascend across the whole walk, and no
// id may be returned that was already dead when the page was read.

import (
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

const keysetPropertyRuns = 200

func TestOccupantsPage_KeysetProperty_NoSkipNoDuplicate(t *testing.T) {
	var totalPages, totalKills, totalBirths int
	for run := uint64(0); run < keysetPropertyRuns; run++ {
		rng := det.NewStream(0x5eed0000+run, 0, 0, "occ-keyset-prop")
		api, err := NewCitizensAPI(1, "kp")
		if err != nil {
			t.Fatal(err)
		}
		const home CellRef = 4242
		mk := func(id uint64) ColdRecord {
			r := ColdRecord{ID: id, HealthBand: HealthGood}
			r.Home = home
			return r
		}
		live := map[uint64]bool{}
		everUsed := map[uint64]bool{}
		throughout := map[uint64]bool{}
		var initial []ColdRecord
		n := 5 + int(rng.IntN(60))
		for len(initial) < n {
			id := uint64(1000 + rng.IntN(400))
			if everUsed[id] {
				continue
			}
			everUsed[id] = true
			live[id] = true
			throughout[id] = true
			initial = append(initial, mk(id))
		}
		if err := api.SeedColdRecords(initial, "kp"); err != nil {
			t.Fatal(err)
		}
		deadAt := map[uint64]bool{}
		kill := func() {
			if len(live) == 0 {
				return
			}
			ids := make([]uint64, 0, len(live))
			for id := range live {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
			id := ids[rng.IntN(int64(len(ids)))]
			if rng.IntN(2) == 0 {
				if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "kp", Kind: LifeEventDeath, CitizenID: id}); err != nil {
					t.Fatalf("run %d death %d: %v", run, id, err)
				}
			} else { // cold-pass style: index goes stale, no stamp bump
				s := api.shardAt(det.ShardForEntity(id))
				api.mu.Lock()
				s.removeAt(s.rowOf(id))
				api.mu.Unlock()
			}
			delete(live, id)
			deadAt[id] = true
			delete(throughout, id)
			totalKills++
		}
		birth := func() {
			for tries := 0; tries < 20; tries++ {
				id := uint64(1000 + rng.IntN(500))
				if everUsed[id] {
					continue
				}
				everUsed[id] = true
				if err := api.SeedColdRecords([]ColdRecord{mk(id)}, "kp"); err != nil {
					t.Fatalf("run %d birth %d: %v", run, id, err)
				}
				live[id] = true
				totalBirths++
				return
			}
		}
		seen := map[uint64]int{}
		var after, last uint64
		limit := 1 + int(rng.IntN(5))
		for guard := 0; ; guard++ {
			if guard > 1000 {
				t.Fatalf("run %d: paging did not terminate", run)
			}
			ids, _, more, err := api.OccupantsPage(OccupantsHome, uint32(home), after, limit, "kp")
			if err != nil {
				t.Fatal(err)
			}
			totalPages++
			for _, id := range ids {
				if seen[id]++; seen[id] > 1 {
					t.Fatalf("run %d: citizen %d listed twice", run, id)
				}
				if id <= last {
					t.Fatalf("run %d: not ascending: %d after %d", run, id, last)
				}
				if deadAt[id] {
					t.Fatalf("run %d: dead citizen %d returned", run, id)
				}
				last = id
			}
			if len(ids) > 0 {
				after = ids[len(ids)-1]
			}
			if !more || len(ids) == 0 {
				break
			}
			// Interleave churn between this client's pages.
			for k := int(rng.IntN(4)); k > 0; k-- {
				if rng.IntN(3) == 0 {
					birth()
				} else {
					kill()
				}
			}
		}
		// hasMore=false must mean nothing live remains beyond the cursor.
		for id := range live {
			if id > after && seen[id] == 0 {
				t.Fatalf("run %d: live citizen %d beyond the final cursor %d never listed (hasMore lied)", run, id, after)
			}
		}
		for id := range throughout {
			if seen[id] == 0 {
				t.Fatalf("run %d: citizen %d live throughout was SKIPPED", run, id)
			}
		}
	}
	t.Logf("keyset property: %d runs, %d pages, %d kills, %d births, skips 0, dups 0", keysetPropertyRuns, totalPages, totalKills, totalBirths)
}
