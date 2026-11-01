package citizens

// FEAT-2326609842 round r3 (opus-round-r3-passport) adversarial keyset pager,
// independent of the builder's property test: births BEHIND and AHEAD of the
// cursor, the cursor id itself dying (official and cold-pass paths), a citizen
// re-homed between pages, and HasMore exactness when the probed citizen (or a
// run of stale entries) dies.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

func r3Seed(t *testing.T, api *CitizensAPI, home CellRef, ids ...uint64) {
	t.Helper()
	recs := make([]ColdRecord, 0, len(ids))
	for _, id := range ids {
		r := ColdRecord{ID: id, HealthBand: HealthGood}
		r.Home = home
		r.Workplace = 9
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "r3"); err != nil {
		t.Fatal(err)
	}
}

func r3ColdKill(api *CitizensAPI, id uint64) {
	s := api.shardAt(det.ShardForEntity(id))
	api.mu.Lock()
	s.removeAt(s.rowOf(id))
	api.mu.Unlock()
}

func r3Page(t *testing.T, api *CitizensAPI, home CellRef, after uint64, limit int) ([]uint64, bool) {
	t.Helper()
	ids, _, more, err := api.OccupantsPage(OccupantsHome, uint32(home), after, limit, "r3")
	if err != nil {
		t.Fatal(err)
	}
	return ids, more
}

// walk continues a keyset walk from after to the end, asserting ascending and
// no duplicate against the ids already seen.
func r3Walk(t *testing.T, api *CitizensAPI, home CellRef, after uint64, limit int, seen map[uint64]int, last *uint64) {
	t.Helper()
	for guard := 0; guard < 1000; guard++ {
		ids, more := r3Page(t, api, home, after, limit)
		for _, id := range ids {
			if seen[id]++; seen[id] > 1 {
				t.Fatalf("citizen %d listed twice", id)
			}
			if id <= *last {
				t.Fatalf("not ascending: %d after %d", id, *last)
			}
			*last = id
		}
		if len(ids) > 0 {
			after = ids[len(ids)-1]
		}
		if !more || len(ids) == 0 {
			return
		}
	}
	t.Fatal("walk did not terminate")
}

// Births behind the cursor are NOT listed in this walk (the honest keyset
// contract: a walk lists the citizens live throughout plus any born ahead of
// the cursor); births ahead ARE listed; the stream stays ascending, no dup.
func TestRoundR3_BirthsBehindAndAheadOfCursor(t *testing.T) {
	api, err := NewCitizensAPI(1, "r3")
	if err != nil {
		t.Fatal(err)
	}
	const home CellRef = 3131
	r3Seed(t, api, home, 1000, 1001, 1002, 1003, 1004, 1005)
	seen := map[uint64]int{}
	var last uint64
	ids, _ := r3Page(t, api, home, 0, 3)
	for _, id := range ids {
		seen[id]++
		last = id
	}
	// Born between pages: one far behind, one just behind, one ahead, one at
	// the very top of the fertility namespace.
	r3Seed(t, api, home, 500, 1001+1000, 999)
	r3Seed(t, api, home, uint64(1)<<63)
	r3Walk(t, api, home, last, 3, seen, &last)
	for _, id := range []uint64{1000, 1001, 1002, 1003, 1004, 1005, 2001, uint64(1) << 63} {
		if seen[id] != 1 {
			t.Errorf("citizen %d listed %d times, want 1 (seen %v)", id, seen[id], seen)
		}
	}
	if seen[500] != 0 || seen[999] != 0 {
		t.Errorf("born-behind-cursor citizens listed late in an ascending walk: %v", seen)
	}
	// A fresh walk lists them.
	fresh := map[uint64]int{}
	var fl uint64
	r3Walk(t, api, home, 0, 4, fresh, &fl)
	if fresh[500] != 1 || fresh[999] != 1 || len(fresh) != 10 {
		t.Errorf("fresh walk = %v, want all 10", fresh)
	}
}

// The cursor id itself dies between pages, by both death paths: the next page
// resumes at the next live id, nothing skipped or repeated.
func TestRoundR3_CursorIDDies(t *testing.T) {
	for _, official := range []bool{true, false} {
		api, err := NewCitizensAPI(1, "r3")
		if err != nil {
			t.Fatal(err)
		}
		const home CellRef = 77
		r3Seed(t, api, home, 10, 20, 30, 40, 50)
		seen := map[uint64]int{}
		var last uint64
		ids, _ := r3Page(t, api, home, 0, 2) // [10 20]
		for _, id := range ids {
			seen[id]++
			last = id
		}
		if official {
			if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "r3", Kind: LifeEventDeath, CitizenID: 20}); err != nil {
				t.Fatal(err)
			}
		} else {
			r3ColdKill(api, 20)
		}
		r3Walk(t, api, home, last, 2, seen, &last)
		for _, id := range []uint64{30, 40, 50} {
			if seen[id] != 1 {
				t.Errorf("official=%v: %d listed %d times (%v)", official, id, seen[id], seen)
			}
		}
	}
}

// A citizen re-homed between pages (simulated: removed and re-seeded under a
// different home with the same id; nothing in the engine moves a citizen
// today) is dropped from the old home's walk and appears in the new home's.
func TestRoundR3_RehomedBetweenPages(t *testing.T) {
	api, err := NewCitizensAPI(1, "r3")
	if err != nil {
		t.Fatal(err)
	}
	const a, b CellRef = 11, 12
	r3Seed(t, api, a, 1, 2, 3, 4, 5, 6)
	seen := map[uint64]int{}
	var last uint64
	ids, _ := r3Page(t, api, a, 0, 2)
	for _, id := range ids {
		seen[id]++
		last = id
	}
	r3ColdKill(api, 4) // leaves the index stale for home a
	r3Seed(t, api, b, 4)
	r3Walk(t, api, a, last, 2, seen, &last)
	if seen[4] != 0 {
		t.Errorf("re-homed citizen 4 still listed in its old home: %v", seen)
	}
	for _, id := range []uint64{1, 2, 3, 5, 6} {
		if seen[id] != 1 {
			t.Errorf("%d listed %d times (%v)", id, seen[id], seen)
		}
	}
	got, more := r3Page(t, api, b, 0, 5)
	if len(got) != 1 || got[0] != 4 || more {
		t.Errorf("new home b = %v more=%v, want [4] false", got, more)
	}
}

// HasMore exactness under churn.
func TestRoundR3_HasMoreExactness(t *testing.T) {
	api, err := NewCitizensAPI(1, "r3")
	if err != nil {
		t.Fatal(err)
	}
	const home CellRef = 99
	r3Seed(t, api, home, 1, 2, 3, 4)
	// Exactly limit remain: HasMore false on the final full page.
	ids, more := r3Page(t, api, home, 2, 2)
	if len(ids) != 2 || more {
		t.Errorf("final full page %v more=%v, want 2 ids, false", ids, more)
	}
	// A run of stale entries after the page: the probe compacts through all of
	// them and reports the truth.
	r3Seed(t, api, home, 5, 6, 7, 8)
	r3Page(t, api, home, 0, 1) // build the index with 1..8
	for _, id := range []uint64{3, 4, 5, 6, 7} {
		r3ColdKill(api, id)
	}
	ids, more = r3Page(t, api, home, 0, 2) // [1 2], probe walks 3..7 dead, finds 8
	if len(ids) != 2 || !more {
		t.Errorf("page before a stale run %v more=%v, want [1 2] true", ids, more)
	}
	// The probed citizen (8) dies between pages: HasMore=true followed by an
	// EMPTY page, the one sanctioned way, and then HasMore false.
	r3ColdKill(api, 8)
	ids, more = r3Page(t, api, home, 2, 2)
	if len(ids) != 0 || more {
		t.Errorf("after probed death %v more=%v, want [] false", ids, more)
	}
	// All dead beyond the cursor: HasMore false, never true with nothing live.
	r3ColdKill(api, 2)
	ids, more = r3Page(t, api, home, 0, 1)
	if len(ids) != 1 || ids[0] != 1 || more {
		t.Errorf("only 1 live: %v more=%v, want [1] false", ids, more)
	}
}
