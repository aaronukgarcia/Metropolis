package citizens

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-664 RE-ROUND (GR#23) — independent attacker "opus-reround-bug664".
//
// Round 1 REJECTED on a concurrent-eviction P0 (a worker's shardAt evicted a
// SIBLING worker's in-flight shard). The rework answers it with an
// acquireShard/releaseShard pin pair used ONLY inside AdvanceDayTick's
// runShardsParallel closure, on the claim that every OTHER shardAt call site
// is strictly sequential. This file attacks that claim from five directions:
//
//   R1 — the sequentiality audit: RLock query paths hammered CONCURRENTLY
//        with a write-locked AdvanceDayTick, and concurrently with EACH
//        OTHER (two RLock readers CAN overlap; only Lock excludes RLock).
//   R2 — pin hygiene: double release, release-without-acquire, panic inside
//        the pinned closure.
//   R3 — budget creep: workers >> budget sustained over many ticks; does
//        residency return to budget or ratchet?
//   R4 — the O(1) container/list LRU rewrite: is the EVICTION SEQUENCE
//        itself byte-stable across runs, and is pageElem (a Go map) ever
//        able to drive order (GR#21)?
//   R5 — the sequential stale-pointer class the pin fix does NOT cover:
//        a real fertility city (reciprocal couples + seeded households, so
//        births actually fire) under a budget of 1, where applyFertilityLocked
//        holds `s` across coldRecord/birthChildLocked's own shardAt calls.

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// coupleCity builds a city of `couples` reciprocal, household-registered,
// child-bearing-age couples spread over many shards. Unlike round 1's
// pagedAPI (which never calls SeedHouseholds, so ValidateCitizen rejects
// every fertility birth and the fertility path never actually WRITES), this
// city produces real births — the only shape that exercises
// birthChildLocked's cross-shard append + mutateColdLocked while
// applyFertilityLocked is holding a shard pointer of its own.
func coupleCity(t *testing.T, seed uint64, couples, workers int, pageDir string, maxResident int) *CitizensAPI {
	t.Helper()
	api, err := NewCitizensAPI(seed, "bug664-reround")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = workers

	recs := make([]ColdRecord, 0, couples*2)
	for k := 0; k < couples; k++ {
		a := uint64(2*k + 1)
		b := uint64(2*k + 2)
		hh := uint64(k + 1)
		for _, pair := range [][2]uint64{{a, b}, {b, a}} {
			r := mkRecord(pair[0], uint16(pair[0]%64))
			r.BirthMonth = -300 - int64(pair[0]%60) // adults, 25-30 at genesis
			r.Household = hh
			r.Partner = pair[1]
			r.ChildCount = 0
			recs = append(recs, r)
		}
	}
	if err := api.SeedColdRecords(recs, "bug664-reround"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.SeedHouseholds(recs, "bug664-reround"); err != nil {
		t.Fatalf("SeedHouseholds: %v", err)
	}
	if pageDir != "" {
		if err := api.EnableDiskPaging(pageDir, maxResident, "bug664-reround"); err != nil {
			t.Fatalf("EnableDiskPaging: %v", err)
		}
	}
	return api
}

// residentSnapshot reports (citizens-layer resident count, the actual number
// of non-nil cold slots, the pageList order, total pin count). Read under
// pagingMu exactly like production does.
func residentSnapshot(c *CitizensAPI) (counted, actual int, order []int, pins int) {
	c.pagingMu.Lock()
	defer c.pagingMu.Unlock()
	counted = c.residentCount
	for i := range c.cold {
		if c.cold[i] != nil {
			actual++
		}
		pins += int(c.shardPins[i])
	}
	if c.pageList != nil {
		for e := c.pageList.Front(); e != nil; e = e.Next() {
			order = append(order, e.Value.(int))
		}
	}
	return
}

// ---------------------------------------------------------------------------
// R1 — THE SEQUENTIALITY AUDIT
// ---------------------------------------------------------------------------

// TestAttackBug664ReroundQueriesDuringAdvance is the direct assault on the
// rework's central claim: "the other 17 shardAt sites are all strictly
// sequential — either under c.mu.Lock, or on RLock query paths that never
// overlap a write-locked AdvanceDayTick".
//
// Every one of these query surfaces is a PUBLIC API a UI / serializer /
// deathservices caller can hit at any moment, and every one of them calls
// the UNPINNED shardAt, which mutates c.cold (installing a reloaded shard,
// nil-ing an evicted one) and calls PageStore.Store — a full columnar read
// of the victim shard. If AdvanceDayTick's fan-out window is NOT fully
// covered by mu.Lock (a release, a downgrade, or any shard work escaping the
// critical section), these hammers reproduce round 1's P0 exactly.
func TestAttackBug664ReroundQueriesDuringAdvance(t *testing.T) {
	if runtime.NumCPU() < 4 {
		t.Skip("needs >= 4 CPUs to interleave readers with the fan-out")
	}
	api := coupleCity(t, 0x664A1, 1200, 16, t.TempDir(), 2)

	ids := make([]uint64, 0, 64)
	for i := uint64(1); i <= 64; i++ {
		ids = append(ids, i)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Six concurrent readers, each on a DIFFERENT public RLock surface that
	// reaches shardAt.
	readers := []func(){
		func() { api.PopulationHash("reader-hash") },
		func() { api.TotalPopulation("reader-pop") },
		func() {
			for _, id := range ids {
				api.CitizenAt(id, "reader-citizen")
			}
		},
		func() {
			for _, id := range ids {
				api.FidelityOf(id, "reader-fid")
				api.HouseholdOf(id, "reader-hh")
			}
		},
		func() { api.BuildSample("reader-sample") },
		func() { api.ColdParams("reader-params") },
	}
	for _, r := range readers {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				fn()
			}
		}(r)
	}
	// Also hammer the serializer's own snapshot surface (snapshotHead walks
	// ALL 256 shards under RLock; snapshotColdShard walks one).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := api.snapshotHead(); err != nil {
				return
			}
			for s := 0; s < 8; s++ {
				api.snapshotColdShard(s)
			}
		}
	}()

	for m := 0; m < 2; m++ {
		if err := api.AdvanceMonth("reround-advance"); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("survived: pop=%d", api.TotalPopulation("reround-advance"))
}

// TestAttackBug664ReroundConcurrentReadersOnly isolates the OTHER half of the
// sequentiality claim, the half the rework's doc comment never mentions: two
// RLock holders are NOT mutually exclusive. Two concurrent PopulationHash /
// TotalPopulation / snapshotColdShard calls both run shardAt, and shardAt
// under a tight budget EVICTS — so reader A's shard can be Store()'d and
// nil-ed by reader B while reader A is still walking it. Nothing pins on
// these paths at all.
func TestAttackBug664ReroundConcurrentReadersOnly(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("needs >= 2 CPUs")
	}
	api := coupleCity(t, 0x6645C, 900, 1, t.TempDir(), 1)
	want := api.TotalPopulation("reround-readers")
	wantHash := api.PopulationHash("reround-readers")

	var wg sync.WaitGroup
	errsCh := make(chan string, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				if got := api.TotalPopulation("reround-readers"); got != want {
					errsCh <- fmt.Sprintf("goroutine %d: TotalPopulation=%d want %d", g, got, want)
				}
				if got := api.PopulationHash("reround-readers"); got != wantHash {
					errsCh <- fmt.Sprintf("goroutine %d: PopulationHash %x want %x", g, got[:8], wantHash[:8])
				}
				for s := 0; s < 16; s++ {
					api.snapshotColdShard(s)
				}
			}
		}(g)
	}
	wg.Wait()
	close(errsCh)
	for m := range errsCh {
		t.Error(m)
	}
	if _, actual, _, pins := residentSnapshot(api); pins != 0 {
		t.Errorf("after read-only hammering, %d pins still held (readers never pin, so this must be 0); resident=%d", pins, actual)
	}
}

// ---------------------------------------------------------------------------
// R2 — PIN HYGIENE
// ---------------------------------------------------------------------------

// TestAttackBug664ReroundPinHygiene attacks the pin bookkeeping directly:
// double release, release with no acquire, and (the interesting one) a panic
// raised while a pin is held — the case where a leaked pin would permanently
// wedge a shard resident and silently defeat the residency budget forever.
func TestAttackBug664ReroundPinHygiene(t *testing.T) {
	api := coupleCity(t, 0x664B2, 400, 1, t.TempDir(), 2)

	// (a) release without acquire must not underflow into a negative count
	// (a negative pin reads as "never pinned" and would reopen round 1's P0).
	api.releaseShard(3)
	api.releaseShard(3)
	api.pagingMu.Lock()
	if api.shardPins[3] < 0 {
		api.pagingMu.Unlock()
		t.Fatalf("releaseShard underflowed shardPins[3] to %d", api.shardPins[3])
	}
	api.pagingMu.Unlock()

	// (b) double release after a single acquire.
	api.acquireShard(7)
	api.releaseShard(7)
	api.releaseShard(7)
	api.pagingMu.Lock()
	if api.shardPins[7] != 0 {
		api.pagingMu.Unlock()
		t.Fatalf("double release left shardPins[7]=%d, want 0", api.shardPins[7])
	}
	api.pagingMu.Unlock()

	// (c) panic while pinned: a deferred release must still fire during
	// unwinding, otherwise a single panicking worker leaks a pin for the
	// lifetime of the process.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
		}()
		defer api.releaseShard(11)
		_ = api.acquireShard(11)
		panic("simulated worker panic mid-pin")
	}()
	api.pagingMu.Lock()
	leaked := api.shardPins[11]
	api.pagingMu.Unlock()
	if leaked != 0 {
		t.Errorf("panic mid-pin leaked shardPins[11]=%d — a leaked pin wedges that shard resident forever", leaked)
	}

	// (d) nested acquire of the SAME shard (refcount, not a boolean flag):
	// two acquires need two releases.
	api.acquireShard(19)
	api.acquireShard(19)
	api.releaseShard(19)
	api.pagingMu.Lock()
	if api.shardPins[19] != 1 {
		api.pagingMu.Unlock()
		t.Fatalf("nested acquire is not refcounted: shardPins[19]=%d after 2 acquires + 1 release, want 1", api.shardPins[19])
	}
	api.pagingMu.Unlock()
	api.releaseShard(19)

	if _, _, _, pins := residentSnapshot(api); pins != 0 {
		t.Errorf("pins outstanding after full acquire/release balance: %d", pins)
	}
}

// ---------------------------------------------------------------------------
// R3 — BUDGET CREEP
// ---------------------------------------------------------------------------

// TestAttackBug664ReroundBudgetCreep measures whether the documented
// "all candidates pinned ⇒ temporarily exceed budget" policy is genuinely
// TEMPORARY (bounded by c.workers, corrected every tick by releaseShard's
// opportunistic re-evict) or a RATCHET that grows residency without bound —
// which would make the whole residency ceiling a lie, i.e. BUG-664 not
// actually fixed.
func TestAttackBug664ReroundBudgetCreep(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("needs >= 2 CPUs")
	}
	const workers, budget, ticks = 16, 2, 50
	api := coupleCity(t, 0x664C3, 1500, workers, t.TempDir(), budget)

	peak := 0
	var series []int
	for i := 0; i < ticks; i++ {
		if _, _, err := api.AdvanceDayTick("creep"); err != nil {
			t.Fatalf("AdvanceDayTick %d: %v", i, err)
		}
		counted, actual, _, pins := residentSnapshot(api)
		if counted != actual {
			t.Fatalf("tick %d: residentCount bookkeeping drift — counted=%d, actual non-nil cold slots=%d", i, counted, actual)
		}
		if pins != 0 {
			t.Fatalf("tick %d: %d pins still held after AdvanceDayTick returned — a pin leaked out of runShardsParallel", i, pins)
		}
		if actual > peak {
			peak = actual
		}
		series = append(series, actual)
	}
	t.Logf("residency across %d ticks at workers=%d budget=%d: peak=%d, final=%d, series(first 12)=%v",
		ticks, workers, budget, peak, series[len(series)-1], series[:12])

	// The documented bound is "at most one pinned shard per live worker", so
	// residency must never exceed budget + workers, and — since no pins are
	// held between ticks — must be back AT budget by the time each tick
	// returns.
	if peak > budget+workers {
		t.Errorf("residency peaked at %d, above the documented bound budget(%d)+workers(%d)=%d", peak, budget, workers, budget+workers)
	}
	for i, v := range series {
		if v > budget {
			t.Errorf("tick %d ended with %d shards resident, budget is %d — residency does not return to budget between ticks (RATCHET)", i, v, budget)
			break
		}
	}
	// A monotonic climb is the ratchet signature even if it stays under the bound.
	if series[len(series)-1] > series[0] && series[len(series)-1] > budget {
		t.Errorf("residency ratcheted from %d to %d across %d ticks", series[0], series[len(series)-1], ticks)
	}
}

// ---------------------------------------------------------------------------
// R4 — LRU ORDER DETERMINISM (GR#21)
// ---------------------------------------------------------------------------

// evictionTrace drives a FIXED sequence of shardAt calls and returns both the
// resulting LRU order and the exact set/sequence of shards that ended up
// paged out — the observable projection of the eviction ORDER. If pageElem
// (a Go map) ever leaked into the ordering decision, this varies run to run
// under Go's randomised map iteration.
func evictionTrace(t *testing.T, seed uint64) (order []int, pagedOut []int, hash [32]byte) {
	t.Helper()
	api := coupleCity(t, seed, 600, 1, t.TempDir(), 3)
	// A deterministic, deliberately non-monotonic access pattern so a
	// correct LRU produces a non-trivial order.
	pattern := []int{0, 5, 9, 5, 2, 0, 17, 9, 33, 2, 5, 41, 17, 0, 63, 9}
	for _, s := range pattern {
		_ = api.shardAt(s)
	}
	counted, _, order, _ := residentSnapshot(api)
	_ = counted
	seen := map[int]bool{}
	for _, s := range pattern {
		if seen[s] {
			continue
		}
		seen[s] = true
		api.pagingMu.Lock()
		if api.cold[s] == nil {
			pagedOut = append(pagedOut, s)
		}
		api.pagingMu.Unlock()
	}
	hash = api.PopulationHash("evict-trace")
	return
}

func TestAttackBug664ReroundEvictionOrderDeterministic(t *testing.T) {
	wantOrder, wantPaged, wantHash := evictionTrace(t, 0x664D4)
	t.Logf("LRU order after fixed access pattern: %v; paged out: %v", wantOrder, wantPaged)
	if len(wantOrder) == 0 {
		t.Fatal("no LRU order recorded — the trace never exercised paging")
	}
	if len(wantPaged) == 0 {
		t.Fatal("nothing was ever evicted — the trace is vacuous, it proves nothing about eviction order")
	}
	for i := 0; i < 8; i++ {
		gotOrder, gotPaged, gotHash := evictionTrace(t, 0x664D4)
		if fmt.Sprint(gotOrder) != fmt.Sprint(wantOrder) {
			t.Fatalf("run %d: LRU order %v != %v — eviction order is not deterministic (GR#21)", i, gotOrder, wantOrder)
		}
		if fmt.Sprint(gotPaged) != fmt.Sprint(wantPaged) {
			t.Fatalf("run %d: paged-out set %v != %v — eviction VICTIM CHOICE varies run to run (GR#21)", i, gotPaged, wantPaged)
		}
		if gotHash != wantHash {
			t.Fatalf("run %d: PopulationHash %x != %x", i, gotHash[:8], wantHash[:8])
		}
	}
}

// TestAttackBug664ReroundLRUIsRealLRU proves the container/list rewrite still
// implements LEAST-RECENTLY-USED semantics, not merely "some deterministic
// order". A rewrite that is deterministic but evicts the WRONG shard is a
// silent performance regression that no determinism test can catch.
func TestAttackBug664ReroundLRUIsRealLRU(t *testing.T) {
	api := coupleCity(t, 0x664E5, 600, 1, t.TempDir(), 2)
	// Touch 10, then 20; residency is now {10,20} with 10 least-recent.
	_ = api.shardAt(10)
	_ = api.shardAt(20)
	// Re-touch 10, making 20 the least-recent.
	_ = api.shardAt(10)
	// Bring in 30: the LRU victim must be 20, NOT 10.
	_ = api.shardAt(30)
	api.pagingMu.Lock()
	c10, c20, c30 := api.cold[10] != nil, api.cold[20] != nil, api.cold[30] != nil
	api.pagingMu.Unlock()
	if !c10 || c20 || !c30 {
		t.Errorf("LRU violated: resident 10=%v 20=%v 30=%v; want 10=true 20=false 30=true (20 was least recently used)", c10, c20, c30)
	}
}

// ---------------------------------------------------------------------------
// R5 — SEQUENTIAL STALE-POINTER CLASS + FULL PARITY WITH REAL BIRTHS
// ---------------------------------------------------------------------------

// TestAttackBug664ReroundFertilityParity is the parity test round 1's own
// version could not be: round 1's pagedAPI never calls SeedHouseholds, so
// ValidateCitizen's householdExists guard REJECTED every fertility birth and
// birthChildLocked's writes were never exercised under paging at all.
//
// With real households, applyFertilityLocked holds `s := c.shardAt(shard)`
// across coldRecord(partner), householdChildCountLocked→birthMonthOfLocked→
// coldRecord, personalityOfLocked→coldRecord and birthChildLocked's own
// shardAt(childShard).append + mutateColdLocked(parent) — every one of which
// can evict `s` out from under the loop at a tight budget. If any write ever
// lands on a detached shard object, population and PopulationHash diverge
// from the paging-off reference.
func TestAttackBug664ReroundFertilityParity(t *testing.T) {
	const couples = 700
	run := func(workers, budget, ticks int) ([32]byte, int, int, int) {
		dir := ""
		if budget > 0 {
			dir = t.TempDir()
		}
		api := coupleCity(t, 0x6646D, couples, workers, dir, budget)
		for i := 0; i < ticks; i++ {
			if _, _, err := api.AdvanceDayTick("fertility-parity"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			}
		}
		b, d := api.VitalEvents("fertility-parity")
		return api.PopulationHash("fertility-parity"), api.TotalPopulation("fertility-parity"), b, d
	}
	const ticks = 65 // > 2 calendar months, so VitalEvents is populated

	wantHash, wantPop, wantB, wantD := run(1, 0, ticks)
	t.Logf("reference (paging OFF, workers=1): pop=%d births=%d deaths=%d hash=%x", wantPop, wantB, wantD, wantHash[:8])
	if wantB == 0 {
		t.Fatal("VACUOUS: the reference city produced ZERO births — this test would not exercise birthChildLocked under paging at all")
	}

	for _, tc := range []struct{ workers, budget int }{
		{1, 1}, {1, 2}, {1, 8}, {4, 1}, {4, 2}, {16, 2}, {16, 256},
	} {
		attempts := 1
		if tc.workers > 1 {
			attempts = 3
		}
		seen := map[[32]byte]int{}
		for a := 0; a < attempts; a++ {
			gotHash, gotPop, gotB, gotD := run(tc.workers, tc.budget, ticks)
			seen[gotHash]++
			if gotPop != wantPop || gotB != wantB || gotD != wantD {
				t.Errorf("workers=%d budget=%d attempt %d: pop=%d births=%d deaths=%d; want pop=%d births=%d deaths=%d",
					tc.workers, tc.budget, a, gotPop, gotB, gotD, wantPop, wantB, wantD)
			}
			if gotHash != wantHash {
				t.Errorf("workers=%d budget=%d attempt %d: PopulationHash %x, want %x — paging changed simulated outcomes with real births in play",
					tc.workers, tc.budget, a, gotHash[:8], wantHash[:8])
			}
		}
		if len(seen) > 1 {
			t.Errorf("workers=%d budget=%d: %d DISTINCT PopulationHashes across %d identical runs (GR#21)", tc.workers, tc.budget, len(seen), attempts)
		}
	}
}

// TestAttackBug664ReroundStalePointerDuplicateShard is the white-box version
// of the same hazard, made explicit. PageStore keeps its OWN resident cache
// keyed by shard, so a Load can return the SAME pointer that was Stored —
// which MASKS a stale-pointer defect whenever the page store still happens to
// cache the object. This test forces the page store to drop its cache (by
// churning enough other shards through it) and then proves that a shard
// reloaded purely from DISK is data-identical to the detached object the
// caller was still holding. If any mutation had landed on the detached
// object after its eviction Store, the two diverge.
func TestAttackBug664ReroundStalePointerDuplicateShard(t *testing.T) {
	dir := t.TempDir()
	api := coupleCity(t, 0x664F6, 800, 1, dir, 1)

	const target = 5
	held := api.shardAt(target) // caller keeps this pointer, unpinned
	if held == nil {
		t.Fatal("shardAt returned nil")
	}
	before := held.count()

	// Churn other shards so `target` is evicted from BOTH the citizens layer
	// and PageStore's own resident cache.
	for s := 0; s < 64; s++ {
		if s == target {
			continue
		}
		_ = api.shardAt(s)
	}
	api.pagingMu.Lock()
	detached := api.cold[target] == nil
	api.pagingMu.Unlock()
	if !detached {
		t.Fatalf("target shard was never evicted — test is vacuous")
	}

	// Reload it: with the page store's cache churned, this must come off disk
	// and be a DIFFERENT object.
	reloaded := api.shardAt(target)
	if reloaded == nil {
		t.Fatal("reload returned nil")
	}
	if reloaded.count() != before {
		t.Fatalf("reloaded shard has %d rows, the detached object had %d — the disk round-trip is LOSSY", reloaded.count(), before)
	}
	if reloaded == held {
		t.Logf("NOTE: PageStore's own resident cache returned the SAME pointer — aliasing masks disk round-trip defects here")
	} else {
		t.Logf("reload produced a distinct object (true disk round-trip): held=%p reloaded=%p", held, reloaded)
		// Every column must survive; rowOf must work (the index is derived,
		// not serialized — BUG-666's rebuildIndexLocked).
		for i := 0; i < reloaded.count(); i++ {
			a, b := held.recordAt(i), reloaded.recordAt(i)
			if a != b {
				t.Fatalf("row %d diverged across the disk round-trip:\n held=%+v\n disk=%+v", i, a, b)
			}
			if got := reloaded.rowOf(a.ID); got != i {
				t.Fatalf("rowOf(%d)=%d after reload, want %d — the derived id index was not rebuilt", a.ID, got, i)
			}
		}
	}
}

// TestAttackBug664ReroundResetForLoadMidPin constructs the hostile case the
// rework's resetForLoad comment claims to have closed: a load arriving while
// pins are outstanding. If seedPageBookkeepingLocked did NOT zero shardPins,
// a pin minted against the PREVIOUS city would permanently block eviction of
// that shard index in the loaded one.
func TestAttackBug664ReroundResetForLoadMidPin(t *testing.T) {
	api := coupleCity(t, 0x66407, 500, 1, t.TempDir(), 2)

	// Mint pins WITHOUT releasing them (the "leaked across a load" shape).
	for _, s := range []int{1, 2, 3, 4, 5} {
		api.acquireShard(s)
	}
	if _, _, _, pins := residentSnapshot(api); pins != 5 {
		t.Fatalf("setup: expected 5 outstanding pins, got %d", pins)
	}

	if err := api.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	counted, actual, _, pins := residentSnapshot(api)
	if pins != 0 {
		t.Fatalf("resetForLoad left %d STALE pins from the previous city — those shard indices can never be evicted in the loaded city", pins)
	}
	if counted != actual {
		t.Fatalf("resetForLoad left residentCount=%d but %d non-nil cold slots", counted, actual)
	}

	// And residency must actually shrink again to the budget on first use.
	for s := 0; s < 32; s++ {
		_ = api.shardAt(s)
	}
	_, actual, _, _ = residentSnapshot(api)
	if actual > api.maxResidentShards {
		t.Errorf("after reset + 32 accesses, %d shards resident, budget %d", actual, api.maxResidentShards)
	}
}

// TestAttackBug664ReroundSaveRestoreUnderPagingWithBirths hammers the full
// serializer round trip on a city that has REAL births in it, into a load
// target that also has paging enabled at a tight budget — so applyLoadRecord
// streams 256 shards' worth of records through the paging seam, evicting and
// reloading constantly while the load is still in progress.
func TestAttackBug664ReroundSaveRestoreUnderPagingWithBirths(t *testing.T) {
	const srcSeed = 0x6644B // PopulationHash mixes c.seed, which a load deliberately does NOT restore (resetForLoad: seed is config) -- every load target must therefore share the source seed or the comparison is vacuous
	src := coupleCity(t, srcSeed, 600, 1, t.TempDir(), 2)
	for i := 0; i < 40; i++ {
		if _, _, err := src.AdvanceDayTick("save-round"); err != nil {
			t.Fatalf("AdvanceDayTick: %v", err)
		}
	}
	wantHash := src.PopulationHash("save-round")
	wantPop := src.TotalPopulation("save-round")

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
	if len(recs) == 0 {
		t.Fatal("save produced no records")
	}

	// Restore into a matrix of load targets. The CONTROLS (paging OFF) are
	// mandatory: if a non-paged target also fails to reproduce the source
	// hash, the defect is in the save/restore participant, NOT in paging, and
	// this test must not be allowed to attribute it to BUG-664.
	type target struct {
		name    string
		prepop  int
		budget  int // 0 = paging off
		control bool
	}
	targets := []target{
		{name: "CONTROL fresh, paging OFF", prepop: 0, budget: 0, control: true},
		{name: "CONTROL pre-populated, paging OFF", prepop: 300, budget: 0, control: true},
		{name: "paged budget=1, pre-populated", prepop: 300, budget: 1},
		{name: "paged budget=2, pre-populated", prepop: 300, budget: 2},
		{name: "paged budget=256, pre-populated", prepop: 300, budget: 256},
		{name: "paged budget=1, fresh", prepop: 0, budget: 1},
	}
	for _, tg := range targets {
		var dst *CitizensAPI
		if tg.prepop > 0 {
			dir := ""
			if tg.budget > 0 {
				dir = t.TempDir()
			}
			dst = coupleCity(t, srcSeed, tg.prepop, 1, dir, tg.budget)
		} else {
			var err error
			dst, err = NewCitizensAPI(srcSeed, "save-round")
			if err != nil {
				t.Fatalf("NewCitizensAPI: %v", err)
			}
			if tg.budget > 0 {
				if err := dst.EnableDiskPaging(t.TempDir(), tg.budget, "save-round"); err != nil {
					t.Fatalf("EnableDiskPaging: %v", err)
				}
			}
		}
		h := NewSaveParticipant(dst).Handler()
		for _, r := range recs {
			if err := h(r); err != nil {
				t.Fatalf("%s: Handler(%s): %v", tg.name, r.Kind, err)
			}
		}
		gotPop := dst.TotalPopulation("save-round")
		gotHash := dst.PopulationHash("save-round")
		t.Logf("%-36s -> pop=%d hash=%x", tg.name, gotPop, gotHash[:8])
		if gotPop != wantPop {
			t.Errorf("%s: restored population %d, want %d", tg.name, gotPop, wantPop)
		}
		if gotHash != wantHash {
			if tg.control {
				t.Errorf("%s: CONTROL FAILED — hash %x != source %x. The save/restore round trip is not hash-stable even WITHOUT paging; nothing below can be attributed to BUG-664",
					tg.name, gotHash[:8], wantHash[:8])
			} else {
				t.Errorf("%s: restored PopulationHash %x, want %x", tg.name, gotHash[:8], wantHash[:8])
			}
		}
		if counted, actual, _, pins := residentSnapshot(dst); tg.budget > 0 && (counted != actual || pins != 0) {
			t.Errorf("%s: after load residentCount=%d actual=%d pins=%d", tg.name, counted, actual, pins)
		}
	}
}

// TestAttackBug664ReroundEnableDiskPagingOnDirtyDir points a fresh city's page
// store at a directory that already contains ANOTHER city's page files (the
// same-dir reuse a real save-slot layout makes easy). Nothing in the page
// file identifies its city, so a paged-out shard could be reloaded as the
// WRONG city's citizens.
func TestAttackBug664ReroundEnableDiskPagingOnDirtyDir(t *testing.T) {
	dir := t.TempDir()
	a := coupleCity(t, 0x66418, 600, 1, dir, 1)
	for s := 0; s < 64; s++ {
		_ = a.shardAt(s) // force pages to disk
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("no page files written — test vacuous")
	}
	t.Logf("%d page files left on disk by city A", len(entries))

	// City B: DIFFERENT population, SAME page dir.
	b := coupleCity(t, 0x66429, 200, 1, dir, 1)
	wantPop := 400
	if got := b.TotalPopulation("dirty"); got != wantPop {
		t.Fatalf("setup: city B pop %d, want %d", got, wantPop)
	}
	for s := 0; s < 64; s++ {
		_ = b.shardAt(s)
	}
	if got := b.TotalPopulation("dirty"); got != wantPop {
		t.Errorf("city B population became %d (want %d) after paging over city A's leftover page files — CROSS-CITY PAGE RESURRECTION", got, wantPop)
	}
	// And the leftover files must not be readable as B's shards.
	if _, err := os.Stat(filepath.Join(dir, "shard-000.page")); err != nil {
		t.Logf("shard-000.page absent: %v", err)
	}
}

// TestAttackBug664ReroundAcquireOutOfRange documents the bounds contract on
// the pinned accessor. det.ShardForEntity is always in range, but the
// accessor is package-internal and a future caller could pass a raw index.
func TestAttackBug664ReroundAcquireOutOfRange(t *testing.T) {
	api := coupleCity(t, 0x6643A, 100, 1, t.TempDir(), 2)
	for _, idx := range []int{-1, numColdShards} {
		func(idx int) {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("acquireShard(%d) panics (bounds enforced by the array, no silent corruption): %v", idx, r)
				} else {
					t.Errorf("acquireShard(%d) did NOT panic — an out-of-range shard index is silently accepted", idx)
				}
			}()
			_ = api.acquireShard(idx)
		}(idx)
	}
	// Sanity: the registry is still usable afterwards.
	if api.TotalPopulation("oob") != 200 {
		t.Error("registry corrupted by the out-of-range probes")
	}
	_ = errs.NewCorrelationID()
	_ = det.ShardForEntity(1)
}
