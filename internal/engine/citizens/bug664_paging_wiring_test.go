package citizens

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnableDiskPagingRejectsInvalidBudget (BUG-664 GR#1): EnableDiskPaging
// must reject maxResident < 1 rather than accept a budget that leaves
// shardAt's eviction discipline nothing safe to keep resident.
func TestEnableDiskPagingRejectsInvalidBudget(t *testing.T) {
	api, err := NewCitizensAPI(1, "bug664")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := api.EnableDiskPaging(t.TempDir(), 0, "bug664"); err == nil {
		t.Fatal("EnableDiskPaging(dir, 0) should be rejected")
	}
	if err := api.EnableDiskPaging(t.TempDir(), -1, "bug664"); err == nil {
		t.Fatal("EnableDiskPaging(dir, -1) should be rejected")
	}
}

// TestPageStoreWiredIntoCitizensAPI (BUG-664 — "citizens PageStore ... has
// NO call site outside its own tests"): PageStore must be a REAL production
// call site once EnableDiskPaging is active, not only paging_test.go's own
// direct unit test.
//
// Wiring-proof idiom (mirrors the compose/bug689 edge wire-test): this
// asserts on OBSERVABLE PRODUCTION SIDE EFFECTS of the wiring, not on
// internal plumbing, so it reddens if shardAt is ever reverted to a bare
// `c.cold[shard]` read (or otherwise stops calling PageStore):
//   - Seeding citizens across more distinct shards than the residency
//     budget allows MUST leave real .page files on disk (proof
//     PageStore.Store was actually invoked from CitizensAPI code).
//   - Every seeded citizen MUST still be reachable, byte-identical,
//     through the ordinary CitizensAPI query surface regardless of
//     whether their shard is currently resident or was paged out (proof
//     PageStore.Load is actually invoked to rehydrate on demand).
func TestPageStoreWiredIntoCitizensAPI(t *testing.T) {
	dir := t.TempDir()
	api, err := NewCitizensAPI(42, "bug664")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// A tight budget (4 of numColdShards=256) forces heavy eviction
	// traffic from touching only a handful of distinct shards.
	if err := api.EnableDiskPaging(dir, 4, "bug664"); err != nil {
		t.Fatalf("EnableDiskPaging: %v", err)
	}

	// One citizen per call, spread across many distinct shard indices —
	// det.ShardForEntity hashes sequential ids across the 256 shards, so
	// ten sequential ids reliably span well beyond the 4-shard budget.
	ids := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	seeded := make(map[uint64]ColdRecord, len(ids))
	for _, id := range ids {
		rec := mkRecord(id, uint16(id%8))
		if err := api.SeedColdRecords([]ColdRecord{rec}, "bug664"); err != nil {
			t.Fatalf("SeedColdRecords(%d): %v", id, err)
		}
		seeded[id] = rec
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	pageFiles := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".page" {
			pageFiles++
		}
	}
	if pageFiles == 0 {
		t.Fatal("EnableDiskPaging wired in but zero .page files were written — PageStore.Store was never actually called from CitizensAPI production code")
	}

	api.mu.RLock()
	resident := 0
	for _, s := range api.cold {
		if s != nil {
			resident++
		}
	}
	api.mu.RUnlock()
	if resident > 4 {
		t.Fatalf("resident shard count = %d, want <= the 4-shard budget after seeding %d distinct shards", resident, len(ids))
	}

	// Every seeded citizen must still be reachable and byte-identical,
	// whether their shard is currently resident or was paged out and must
	// be transparently reloaded by coldRecord -> shardAt.
	for _, id := range ids {
		rec, ok := api.coldRecord(id)
		if !ok {
			t.Fatalf("citizen %d unreachable after paging eviction — shardAt failed to rehydrate its shard", id)
		}
		want := seeded[id]
		if rec != want {
			t.Fatalf("citizen %d record diverged after a page-out/reload round trip: got %+v, want %+v", id, rec, want)
		}
	}
}

// TestPagingDoesNotChangePopulationHash (BUG-664 behaviour parity, AC-17
// invariance style): the SAME population, ticked the SAME number of
// months, produces a BYTE-IDENTICAL PopulationHash whether paging is
// disabled (every shard permanently resident — the pre-BUG-664 default) or
// enabled with a tight residency budget forcing constant eviction/reload
// churn. Paging is purely a memory-residency mechanism; it must never
// change simulated outcomes.
func TestPagingDoesNotChangePopulationHash(t *testing.T) {
	build := func(pageDir string, maxResident int) *CitizensAPI {
		api, err := NewCitizensAPI(777, "bug664")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		recs := make([]ColdRecord, 0, 40)
		for i := 0; i < 40; i++ {
			r := mkRecord(uint64(i+1), uint16(i%8))
			r.BirthMonth = -300 // adults, so the monthly pass has real work to do
			recs = append(recs, r)
		}
		if err := api.SeedColdRecords(recs, "bug664"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		if pageDir != "" {
			if err := api.EnableDiskPaging(pageDir, maxResident, "bug664"); err != nil {
				t.Fatalf("EnableDiskPaging: %v", err)
			}
		}
		for m := 0; m < 3; m++ {
			if err := api.AdvanceMonth("bug664"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api
	}

	plain := build("", 0)
	paged := build(t.TempDir(), 4)

	h1 := plain.PopulationHash("bug664")
	h2 := paged.PopulationHash("bug664")
	if h1 != h2 {
		t.Fatalf("PopulationHash diverged: paging=off %x vs paging=on(budget=4) %x — paging changed simulated outcomes", h1, h2)
	}
}
