package citizens

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-664 INDEPENDENT DESTRUCTIVE ROUND (GR#23) — attacker "opus-round-bug664".
//
// The estate wires PageStore in behind a shardAt(shard) choke point guarded
// by an INDEPENDENT pagingMu. The whole attack surface is the LIFETIME of the
// *ColdShard pointer shardAt hands back: shardAt releases pagingMu before the
// caller has finished using the shard, so nothing stops a concurrent shardAt
// call on a DIFFERENT shard from choosing that same shard as its eviction
// victim and Store()-ing (i.e. reading every column of) a shard another
// goroutine is concurrently mutating.
//
// The author's own parity test never reached this: NewCitizensAPI defaults
// workers=1, so runShardsParallel ran a single goroutine and every shardAt
// call was serialised by accident.

// pagedAPI builds a CitizensAPI with n citizens spread across many shards,
// the given worker count, and (if pageDir != "") paging enabled at the given
// residency budget.
func pagedAPI(t *testing.T, seed uint64, n, workers int, pageDir string, maxResident int) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(seed, "bug664-attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = workers
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		r := mkRecord(uint64(i), uint16(i%64))
		r.BirthMonth = -int64((i*17)%1100) - 240 // adults: real monthly work
		r.Household = uint64(i / 2)
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "bug664-attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if pageDir != "" {
		if err := api.EnableDiskPaging(pageDir, maxResident, "bug664-attack"); err != nil {
			t.Fatalf("EnableDiskPaging: %v", err)
		}
	}
	return api
}

// distinctShards reports how many of the 256 shards the ids 1..n land in.
func distinctShards(n int) int {
	seen := map[int]bool{}
	for i := 1; i <= n; i++ {
		seen[det.ShardForEntity(uint64(i))] = true
	}
	return len(seen)
}

// ---------------------------------------------------------------------------
// A1 — THE HAMMER: concurrent shardAt under a tight residency budget.
// ---------------------------------------------------------------------------

// TestAttackBug664ConcurrentShardAtEvictsShardInUse is the pointer-lifetime
// hammer. runShardsParallel fans the monthly cold pass out over `workers`
// goroutines, each of which calls shardAt(shard) and then mutates the
// returned *ColdShard for the whole duration of applyMonthly. With a tight
// budget, a SECOND worker's shardAt call evicts the FIRST worker's shard
// while it is still in use: evictOverBudgetLocked only ever protects `keep`,
// the shard the CALLING goroutine just touched, and has no knowledge at all
// of the shards other goroutines are mid-flight on.
//
// Under -race this reports a write/read data race between applyMonthly's
// column writes and PageStore.Store's gob encode of the same columns. Without
// -race it manifests as silent citizen loss / resurrection (A2 below).
func TestAttackBug664ConcurrentShardAtEvictsShardInUse(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("needs >= 2 CPUs to interleave workers")
	}
	const n = 3000
	api := pagedAPI(t, 0xB664A, n, 16, t.TempDir(), 2)
	t.Logf("population %d spread over %d distinct shards, budget=2, workers=%d",
		n, distinctShards(n), api.workers)

	for m := 0; m < 2; m++ {
		if err := api.AdvanceMonth("bug664-attack"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	t.Logf("survived %d citizens", api.TotalPopulation("bug664-attack"))
}

// ---------------------------------------------------------------------------
// A2 — the same defect WITHOUT the race detector: outcome divergence.
// ---------------------------------------------------------------------------

// TestAttackBug664ConcurrentPagingDivergesFromSerial pins the OBSERVABLE
// consequence of A1's race for anyone running the suite without -race: the
// same seed, same population and same number of ticks must produce the same
// PopulationHash whether the cold pass ran on 1 worker or 16, exactly as
// AC-17's worker-count invariance requires — and paging must not change that.
//
// A lost update through an evicted-mid-use shard breaks it: the eviction
// Stores a shard's state, a later shardAt Loads the DISK copy, and every
// mutation the in-flight goroutine made after the Store is silently gone.
func TestAttackBug664ConcurrentPagingDivergesFromSerial(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("needs >= 2 CPUs")
	}
	const n = 2000
	run := func(workers, budget int) ([32]byte, int) {
		dir := ""
		if budget > 0 {
			dir = t.TempDir()
		}
		api := pagedAPI(t, 0xB664B, n, workers, dir, budget)
		for m := 0; m < 2; m++ {
			if err := api.AdvanceMonth("bug664-attack"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("bug664-attack"), api.TotalPopulation("bug664-attack")
	}

	wantHash, wantPop := run(1, 0) // paging OFF, serial — the reference
	t.Logf("reference (paging off, workers=1): pop=%d hash=%x", wantPop, wantHash[:8])

	// CONTROL first: worker-count invariance WITHOUT paging (AC-17). If this
	// fails, the divergence below is not paging's fault and the whole test is
	// meaningless — so it is asserted as a hard precondition.
	for _, w := range []int{4, 16} {
		got, pop := run(w, 0)
		if got != wantHash || pop != wantPop {
			t.Fatalf("CONTROL BROKEN: paging OFF, workers=%d gives pop=%d hash=%x, want pop=%d hash=%x — worker invariance is already broken on trunk, this test cannot attribute anything to paging",
				w, pop, got[:8], wantPop, wantHash[:8])
		}
	}
	t.Log("control OK: with paging off, PopulationHash is worker-count invariant")

	for _, tc := range []struct {
		workers, budget int
	}{
		{1, 2}, {1, 4}, {1, 256},
		{4, 2}, {16, 2}, {16, 4}, {16, 256},
	} {
		// The concurrent-paging defect is TIMING dependent (it is a data
		// race), so a single sample is a flaky detector — one observed clean
		// run does NOT mean the config is correct. Repeat the concurrent
		// configs and fail on ANY divergence across attempts; also report the
		// distinct-hash count, since >1 distinct outcome for one config is by
		// itself a GR#21 determinism violation.
		attempts := 1
		if tc.workers > 1 && tc.budget < numColdShards {
			attempts = 5
		}
		seen := map[[32]byte]int{}
		for a := 0; a < attempts; a++ {
			got, pop := run(tc.workers, tc.budget)
			seen[got]++
			if pop != wantPop {
				t.Errorf("workers=%d budget=%d attempt %d: population %d, want %d — paging LOST or RESURRECTED citizens",
					tc.workers, tc.budget, a, pop, wantPop)
			}
			if got != wantHash {
				t.Errorf("workers=%d budget=%d attempt %d: PopulationHash %x, want %x — paging changed simulated outcomes",
					tc.workers, tc.budget, a, got[:8], wantHash[:8])
			}
		}
		if len(seen) > 1 {
			t.Errorf("workers=%d budget=%d: %d DISTINCT PopulationHashes across %d identical runs — the same seed produces different cities (GR#21)",
				tc.workers, tc.budget, len(seen), attempts)
		}
	}
}

// ---------------------------------------------------------------------------
// A3 — crash consistency: a failing Store must never lose citizens.
// ---------------------------------------------------------------------------

// TestAttackBug664StoreFailureDoesNotLoseCitizens points the page directory
// at a path that cannot be written (a regular FILE where a directory is
// expected, so MkdirAll/WriteFile both fail). evictOverBudgetLocked claims to
// keep a shard resident rather than evict on a failed persist; this proves it
// and, more importantly, proves no citizen disappears.
func TestAttackBug664StoreFailureDoesNotLoseCitizens(t *testing.T) {
	base := t.TempDir()
	blocked := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// The page dir is a CHILD of a regular file — MkdirAll must fail.
	pageDir := filepath.Join(blocked, "pages")

	const n = 800

	// CONTROL: the same seed/population with paging OFF. A month of the cold
	// pass legitimately kills some citizens (mortality), so "missing" is only
	// a defect where it differs from the no-paging baseline.
	ctrl := pagedAPI(t, 0xB664C, n, 1, "", 0)
	if err := ctrl.AdvanceMonth("bug664-attack"); err != nil {
		t.Fatalf("control AdvanceMonth: %v", err)
	}
	wantAlive := map[uint64]ColdRecord{}
	for i := 1; i <= n; i++ {
		if r, ok := ctrl.coldRecord(uint64(i)); ok {
			wantAlive[uint64(i)] = r
		}
	}
	t.Logf("control (paging off): %d/%d citizens alive after one month", len(wantAlive), n)

	api := pagedAPI(t, 0xB664C, n, 1, pageDir, 2)
	if err := api.AdvanceMonth("bug664-attack"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	// Every citizen the control kept must still be individually reachable and
	// identical: a failed Store that nonetheless nilled the slot, or a Load
	// that fell back to a fresh empty shard, would make them vanish or reset.
	missing, diverged := 0, 0
	for id, want := range wantAlive {
		got, ok := api.coldRecord(id)
		if !ok {
			missing++
			continue
		}
		if got != want {
			diverged++
		}
	}
	if missing > 0 || diverged > 0 {
		t.Errorf("with an UNWRITABLE page dir: %d citizens unreachable and %d diverged vs the paging-off control (%d alive) — a failed Store discarded or corrupted resident data",
			missing, diverged, len(wantAlive))
	}
}

// ---------------------------------------------------------------------------
// A4 — serialization interplay: save/restore with shards paged out.
// ---------------------------------------------------------------------------

// TestAttackBug664SaveRestoreWithShardsPagedOut proves state COMPLETENESS
// across the save boundary when paging is enabled and most shards are on
// disk at save time. participant.go's five call sites route through shardAt,
// so the save walk must force-load each paged-out shard rather than emitting
// an empty or stale one.
func TestAttackBug664SaveRestoreWithShardsPagedOut(t *testing.T) {
	const n = 1200
	src := pagedAPI(t, 0xB664D, n, 1, t.TempDir(), 2)

	// Touch a spread of shards so most of them are genuinely evicted.
	for i := 1; i <= n; i++ {
		_, _ = src.coldRecord(uint64(i))
	}
	src.pagingMu.Lock()
	resident := 0
	for _, s := range src.cold {
		if s != nil {
			resident++
		}
	}
	src.pagingMu.Unlock()
	if resident > 2 {
		t.Fatalf("resident=%d, want <= budget 2 — precondition (most shards paged out) not met", resident)
	}
	t.Logf("saving with %d/%d shards resident", resident, numColdShards)

	// Stream the save.
	var recs []serialize.Record
	pull := NewSaveParticipant(src).Source()
	for {
		rec, ok, err := pull()
		if err != nil {
			t.Fatalf("Source pull: %v", err)
		}
		if !ok {
			break
		}
		recs = append(recs, rec)
	}

	dst, err := NewCitizensAPI(999, "bug664-attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	h := NewSaveParticipant(dst).Handler()
	for _, rec := range recs {
		if err := h(rec); err != nil {
			t.Fatalf("Handler(%s): %v", rec.Kind, err)
		}
	}

	if got := dst.TotalPopulation("bug664-attack"); got != n {
		t.Errorf("restored population %d, want %d — the save walk did not force-load paged-out shards", got, n)
	}
	for i := 1; i <= n; i++ {
		want, okS := src.coldRecord(uint64(i))
		got, okD := dst.coldRecord(uint64(i))
		if !okS {
			t.Fatalf("source lost citizen %d before the save", i)
		}
		if !okD {
			t.Fatalf("citizen %d missing after restore from a paged-out save", i)
		}
		if got != want {
			t.Fatalf("citizen %d diverged across the paged save boundary:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// A5 — coverage probe: is the fresh-Store-before-evict actually load-bearing?
// ---------------------------------------------------------------------------

// TestAttackBug664EvictionPersistsInPlaceMutations targets the estate's own
// stated correctness claim: eviction must Store the shard's CURRENT
// in-memory state, because rows are mutated in place through the pointer
// shardAt handed out. Mutate a shard through a shardAt pointer, then force
// it out by touching enough other shards, then read it back.
//
// SERIAL by construction, so it isolates the persist-freshness contract from
// A1's concurrency defect.
func TestAttackBug664EvictionPersistsInPlaceMutations(t *testing.T) {
	const n = 600
	api := pagedAPI(t, 0xB664E, n, 1, t.TempDir(), 2)

	// Mutate citizen 1's wealth in place through the shardAt pointer, the
	// same way applyMonthly/mutateColdLocked do.
	const target = uint64(1)
	shard := det.ShardForEntity(target)
	api.mu.Lock()
	s := api.shardAt(shard)
	row := s.rowOf(target)
	if row < 0 {
		api.mu.Unlock()
		t.Fatalf("citizen %d not found in shard %d", target, shard)
	}
	s.wealth[row] = 123456789
	api.mu.Unlock()

	// Force the shard out by touching many others.
	for i := 2; i <= n; i++ {
		_, _ = api.coldRecord(uint64(i))
	}

	rec, ok := api.coldRecord(target)
	if !ok {
		t.Fatalf("citizen %d unreachable after eviction", target)
	}
	if rec.Wealth != 123456789 {
		t.Errorf("wealth = %d after a page-out/reload round trip, want 123456789 — the eviction persisted a STALE copy, losing an in-place mutation", rec.Wealth)
	}
}

// ---------------------------------------------------------------------------
// A6 — load into a paging-enabled target over a PREVIOUS city's page dir.
// ---------------------------------------------------------------------------

// TestAttackBug664LoadIntoPagingTargetOverStalePages checks the one path
// resetForLoad's pageOrder reseed exists to protect: a load target that
// already has paging enabled over a directory full of a DIFFERENT city's
// .page files. If any shard is ever Loaded from disk without having been
// re-Stored after the reset, the OLD city's citizens are resurrected into
// the new one.
func TestAttackBug664LoadIntoPagingTargetOverStalePages(t *testing.T) {
	dir := t.TempDir()

	// City A: 900 citizens, ids 1..900, paged into dir.
	old := pagedAPI(t, 0xB664F, 900, 1, dir, 2)
	for i := 1; i <= 900; i++ {
		_, _ = old.coldRecord(uint64(i))
	}

	// City B: a DISJOINT id range, saved.
	src, err := NewCitizensAPI(0xB6650, "bug664-attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	recs := make([]ColdRecord, 0, 300)
	wantIDs := map[uint64]bool{}
	for i := 5000; i < 5300; i++ {
		recs = append(recs, mkRecord(uint64(i), uint16(i%64)))
		wantIDs[uint64(i)] = true
	}
	if err := src.SeedColdRecords(recs, "bug664-attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	var saved []serialize.Record
	pull := NewSaveParticipant(src).Source()
	for {
		rec, ok, err := pull()
		if err != nil {
			t.Fatalf("Source: %v", err)
		}
		if !ok {
			break
		}
		saved = append(saved, rec)
	}

	// Load city B into the SAME api that is paging over city A's dir.
	h := NewSaveParticipant(old).Handler()
	for _, rec := range saved {
		if err := h(rec); err != nil {
			t.Fatalf("Handler(%s): %v", rec.Kind, err)
		}
	}

	if got := old.TotalPopulation("bug664-attack"); got != len(wantIDs) {
		t.Errorf("post-load population %d, want %d — stale pages from the previous city leaked into the loaded one", got, len(wantIDs))
	}
	for i := 1; i <= 900; i++ {
		if _, ok := old.coldRecord(uint64(i)); ok {
			t.Fatalf("citizen %d from the PREVIOUS city is still reachable after a full load — a stale .page was rehydrated", i)
		}
	}
	for id := range wantIDs {
		if _, ok := old.coldRecord(id); !ok {
			t.Fatalf("loaded citizen %d missing", id)
		}
	}
}
