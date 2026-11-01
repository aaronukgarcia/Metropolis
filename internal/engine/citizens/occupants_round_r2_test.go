package citizens

// FEAT-2326609842 round r2 (opus-round-r2-passport) attacker probes against
// the r2 in-place-compacting occupant paging: (a) a SINGLE client paging by
// offset += len(entries) skips a live resident when a citizen it has already
// read dies through the official life-event path between its pages (the
// rebuild drops the dead entry before the client's offset, shifting every
// later position left by one); (b) the same skip via a second client's
// compaction (the disclosed case); (c) -race hammer: concurrent pagers +
// concurrent deaths + passport reads.

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

func r2SeedHome(t *testing.T, api *CitizensAPI, ref CellRef, from, to uint64) {
	t.Helper()
	var recs []ColdRecord
	for id := from; id < to; id++ {
		r := ColdRecord{ID: id, HealthBand: HealthGood}
		r.Home = ref
		r.Workplace = 17
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "r2"); err != nil {
		t.Fatal(err)
	}
}

// pageAll pages with the documented client contract and returns ids seen.
// FLIPPED for the r3 keyset protocol: the cursor is the afterID (the last id
// received), not an offset; hasMore replaces the "off >= total" stop test.
func r2Page(t *testing.T, api *CitizensAPI, ref uint32, after uint64, limit int) ([]uint64, bool) {
	t.Helper()
	ids, _, more, err := api.OccupantsPage(OccupantsHome, ref, after, limit, "r2")
	if err != nil {
		t.Fatal(err)
	}
	return ids, more
}

// (a) single client, official death path, no second client: SKIP.
func TestRoundR2_OccupantPaging_SingleClientSkipAfterOfficialDeath(t *testing.T) {
	api, err := NewCitizensAPI(1, "r2")
	if err != nil {
		t.Fatal(err)
	}
	r2SeedHome(t, api, 4242, 1000, 1005)
	seen := map[uint64]bool{}
	ids, _ := r2Page(t, api, 4242, 0, 2) // [1000 1001]
	for _, id := range ids {
		seen[id] = true
	}
	// A citizen this client ALREADY read dies between its pages.
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "r2", Kind: LifeEventDeath, CitizenID: 1000}); err != nil {
		t.Fatalf("death: %v", err)
	}
	after := ids[len(ids)-1]
	for guard := 0; guard < 10; guard++ {
		ids, more := r2Page(t, api, 4242, after, 2)
		for _, id := range ids {
			seen[id] = true
		}
		if !more || len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
	}
	for id := uint64(1001); id < 1005; id++ {
		if !seen[id] {
			t.Errorf("LIVE resident %d never listed (skipped) -- seen %v", id, seen)
		}
	}
}

// (b) the disclosed case: client B's compaction shifts client A.
func TestRoundR2_OccupantPaging_SecondClientCompactionSkip(t *testing.T) {
	api, err := NewCitizensAPI(1, "r2")
	if err != nil {
		t.Fatal(err)
	}
	r2SeedHome(t, api, 4242, 1000, 1005)
	seen := map[uint64]bool{}
	ids, _ := r2Page(t, api, 4242, 0, 2) // A reads [1000 1001]
	for _, id := range ids {
		seen[id] = true
	}
	// Mid-month cold-pass style death (no stamp bump), found by client B.
	s := api.shardAt(det.ShardForEntity(1000))
	api.mu.Lock()
	s.removeAt(s.rowOf(1000))
	api.mu.Unlock()
	r2Page(t, api, 4242, 0, 2) // B compacts 1000 out
	after := ids[len(ids)-1]
	for guard := 0; guard < 10; guard++ {
		ids, more := r2Page(t, api, 4242, after, 2)
		for _, id := range ids {
			seen[id] = true
		}
		if !more || len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
	}
	for id := uint64(1001); id < 1005; id++ {
		if !seen[id] {
			t.Errorf("LIVE resident %d skipped after another client's compaction -- seen %v", id, seen)
		}
	}
}

// (c) -race: concurrent pagers, concurrent official deaths, passport reads.
// Asserts no race, no panic, and no dead id is ever returned after its death
// completed before the page began.
func TestRoundR2_OccupantPaging_RaceHammer(t *testing.T) {
	api, err := NewCitizensAPI(1, "r2")
	if err != nil {
		t.Fatal(err)
	}
	r2SeedHome(t, api, 77, 1, 2001)
	var wg sync.WaitGroup
	var deadMu sync.Mutex
	dead := map[uint64]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := uint64(1); id <= 2000; id += 3 {
			if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "r2", Kind: LifeEventDeath, CitizenID: id}); err != nil {
				t.Errorf("death %d: %v", id, err)
				return
			}
			deadMu.Lock()
			dead[id] = true
			deadMu.Unlock()
		}
	}()
	for p := 0; p < 6; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for round := 0; round < 40; round++ {
				deadMu.Lock()
				snap := make(map[uint64]bool, len(dead))
				for k := range dead {
					snap[k] = true
				}
				deadMu.Unlock()
				for after := uint64(0); ; {
					ids, _, more, err := api.OccupantsPage(OccupantsHome, 77, after, 50, "r2")
					if err != nil {
						t.Error(err)
						return
					}
					for _, id := range ids {
						if snap[id] {
							t.Errorf("dead %d listed", id)
							return
						}
						api.PassportRecord(id, "r2")
					}
					if !more || len(ids) == 0 {
						break
					}
					after = ids[len(ids)-1]
				}
			}
		}(p)
	}
	wg.Wait()
}
