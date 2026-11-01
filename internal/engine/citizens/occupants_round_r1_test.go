package citizens

// FEAT-2326609842 round r1 (opus-round-r1-passport) attacker probes against
// the occupant index: scrape economics at 1M, once-per-change rebuild, paging
// hygiene under a stale (mid-month death) index, and paged-store rebuild cost.

import (
	"os"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

func roundR1Seed(t *testing.T, api *CitizensAPI, n int) {
	t.Helper()
	const batch = 100000
	recs := make([]ColdRecord, 0, batch)
	for id := 1; id <= n; id++ {
		r := ColdRecord{ID: uint64(id), HealthBand: HealthGood}
		r.Home = CellRef(id%20000 + 1)
		r.Workplace = uint32(id%5000 + 1)
		recs = append(recs, r)
		if len(recs) == batch {
			if err := api.SeedColdRecords(recs, "r1"); err != nil {
				t.Fatal(err)
			}
			recs = recs[:0]
		}
	}
	if len(recs) > 0 {
		if err := api.SeedColdRecords(recs, "r1"); err != nil {
			t.Fatal(err)
		}
	}
}

// (2) scrape economics: 1M citizens; the index builds once, then every
// query is O(page) and the index pointer never changes while the stamp holds.
func TestRoundR1_OccupantIndex_1M_OncePerChange(t *testing.T) {
	if os.Getenv("METRO_R1_1M") == "" {
		t.Skip("set METRO_R1_1M=1 (1M-citizen scrape probe)")
	}
	api, err := NewCitizensAPI(7, "r1")
	if err != nil {
		t.Fatal(err)
	}
	roundR1Seed(t, api, 1_000_000)
	t0 := time.Now()
	ids, total, _, err := api.OccupantsPage(OccupantsHome, 42, 0, 50, "r1")
	build := time.Since(t0)
	const wantFirstPage = 50
	if err != nil || total != wantFirstPage || len(ids) != wantFirstPage {
		t.Fatalf("first page: n=%d total=%d err=%v", len(ids), total, err)
	}
	first := api.occ
	t1 := time.Now()
	const q = 20000
	for i := 0; i < q; i++ {
		k := uint32(i%20000 + 1)
		if _, _, _, err := api.OccupantsPage(OccupantsHome, k, 0, 50, "r1"); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := api.OccupantsPage(OccupantsWorkplace, uint32(i%5000+1), 0, 50, "r1"); err != nil {
			t.Fatal(err)
		}
	}
	per := time.Since(t1) / (2 * q)
	if api.occ != first {
		t.Fatal("index rebuilt with no stamp change (per-query rebuild)")
	}
	t.Logf("1M: first page (index build) %v; steady-state %v/query over %d queries", build, per, 2*q)
	if per > build/20 {
		t.Errorf("steady-state query %v is not O(page) vs build %v", per, build)
	}
	// Full-population enumeration exposure through paging: sum over all keys.
	exposed := 0
	for k := uint32(1); k <= 20000; k++ {
		for after := uint64(0); ; {
			got, _, more, _ := api.OccupantsPage(OccupantsHome, k, after, 50, "r1")
			exposed += len(got)
			if !more || len(got) == 0 {
				break
			}
			after = got[len(got)-1]
		}
	}
	t.Logf("whole-population enumeration via home paging: %d ids in %d keys (bounded per request, NOT per client)", exposed, 20000)
	// A day tick inside the month: rebuilds at most once per tick.
	rebuilds := 0
	prev := api.occ
	for d := 0; d < 3; d++ {
		if _, _, err := api.AdvanceDayTick("r1"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			if _, _, _, err := api.OccupantsPage(OccupantsHome, 42, 0, 50, "r1"); err != nil {
				t.Fatal(err)
			}
			if api.occ != prev {
				rebuilds++
				prev = api.occ
			}
		}
	}
	t.Logf("rebuilds across 3 day ticks x 5 queries: %d", rebuilds)
	if rebuilds > 3 {
		t.Errorf("index rebuilt %d times for 3 changes", rebuilds)
	}
}

// Paging hygiene: a client paging by offset += len(entries) (the natural
// reading of HasMore = offset+len < total in compose.occupantPage) after a
// mid-month death must not see the same citizen twice.
func TestRoundR1_OccupantPaging_NoDuplicateAfterStaleDeath(t *testing.T) {
	api, err := NewCitizensAPI(1, "r1")
	if err != nil {
		t.Fatal(err)
	}
	var recs []ColdRecord
	for id := uint64(1000); id < 1005; id++ {
		r := ColdRecord{ID: id}
		r.Home = 4242
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := api.OccupantsPage(OccupantsHome, 4242, 0, 2, "r1"); err != nil {
		t.Fatal(err)
	}
	s := api.shardAt(det.ShardForEntity(1001))
	api.mu.Lock()
	s.removeAt(s.rowOf(1001))
	api.mu.Unlock()
	seen := map[uint64]int{}
	after := uint64(0)
	for guard := 0; guard < 10; guard++ {
		ids, _, hasMore, err := api.OccupantsPage(OccupantsHome, 4242, after, 2, "r1")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			seen[id]++
		}
		if !hasMore || len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
	}
	for id, c := range seen {
		if c > 1 {
			t.Errorf("citizen %d listed %d times paging by keyset (afterID = last id) after a stale death", id, c)
		}
	}
	if len(seen) != 4 {
		t.Errorf("want the 4 live residents, got %v", seen)
	}
}
