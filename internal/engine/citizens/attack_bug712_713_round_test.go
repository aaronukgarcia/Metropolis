package citizens

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ===========================================================================
// Independent destructive round against BUG-712 / BUG-713 (attacker:
// opus-round-bug713, NOT the author). Every test here is an ATTACK on the
// landed fix, not a restatement of the author's own coverage.
// ===========================================================================

// TestAttackBug713ForgetRaceUnderConcurrentShardAt hammers the exact window
// the round brief named: evictOverBudgetLocked now does Store -> Forget ->
// cold[victim] = nil as three separate steps (Store and Forget each take
// PageStore.mu independently), so if any of those steps ran outside
// pagingMu a concurrent shardAt on the SAME shard could observe a moment
// where the shard is in neither cold[], nor PageStore.resident, nor yet
// durable -- a vanished citizen. 8 goroutines read every citizen while the
// residency budget forces continuous eviction; every read must find its
// citizen with the seeded district intact.
func TestAttackBug713ForgetRaceUnderConcurrentShardAt(t *testing.T) {
	const n = 400
	api := pagedAPI(t, 0xF0F0, n, 1, t.TempDir(), 2)

	var wg sync.WaitGroup
	errCh := make(chan string, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for pass := 0; pass < 6; pass++ {
				for i := 1; i <= n; i++ {
					id := uint64(((i + g*37 + pass) % n) + 1)
					rec, ok := api.coldRecord(id)
					if !ok {
						select {
						case errCh <- "citizen vanished mid-eviction":
						default:
						}
						return
					}
					if rec.ID != id {
						select {
						case errCh <- "wrong record returned":
						default:
						}
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatalf("concurrent shardAt hit the Store/Forget/nil-out window: %s", msg)
	}
}

// TestAttackBug713CorruptPageLosesCitizensSilently is a CHARACTERISATION
// test for the round's finding F1: PageStore.Load returns (nil, false, nil)
// -- an ordinary "never persisted" MISS, indistinguishable from a genuinely
// absent shard -- when the page file EXISTS but fails to gob-decode.
// loadShardLocked then substitutes a fresh EMPTY shard, so every citizen in
// that shard is silently deleted from the city: TotalPopulation drops,
// PopulationHash changes, and NOTHING is written to the error registry
// (contrast the world-seed mismatch on the very next line of Load, which
// DOES raise MET-G012). This test pins today's behaviour so the gap is
// visible in-tree; it is NOT an endorsement.
func TestAttackBug713CorruptPageLosesCitizensSilently(t *testing.T) {
	dir := t.TempDir()
	const n = 200
	api := pagedAPI(t, 0xC0FFEE, n, 1, dir, 2)

	before := api.TotalPopulation("attack-bug713")
	if before != n {
		t.Fatalf("setup: population %d, want %d", before, n)
	}
	hashBefore := api.PopulationHash("attack-bug713")

	// Find an evicted shard that has a page file on disk and corrupt it.
	victim := -1
	api.pagingMu.Lock()
	for i := range api.cold {
		if api.cold[i] == nil {
			if _, err := os.Stat(api.pages.pathFor(i)); err == nil {
				victim = i
				break
			}
		}
	}
	api.pagingMu.Unlock()
	if victim < 0 {
		t.Skip("no evicted-with-page-file shard available in this configuration")
	}
	if err := os.WriteFile(api.pages.pathFor(victim), []byte("garbage-not-gob"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	after := api.TotalPopulation("attack-bug713")
	hashAfter := api.PopulationHash("attack-bug713")
	lost := before - after
	t.Logf("FINDING F1: corrupting ONE page file silently deleted %d of %d citizens "+
		"(population %d -> %d, PopulationHash changed=%v) with no registry error recorded",
		lost, before, before, after, hashBefore != hashAfter)
	if lost <= 0 {
		t.Fatalf("expected the corrupt page to cost citizens (characterisation of F1); "+
			"population went %d -> %d. If this now holds population, the fail-closed "+
			"gap has been fixed and this characterisation test should be replaced by a "+
			"real conservation assertion", before, after)
	}
}

// TestAttackBug713ForeignShardFileAdoptedUnderMatchingSeed attacked the
// round's item (4): what ELSE did Load validate besides the world seed?
// Pre-fix answer: nothing -- a page file belonging to shard A, present at
// shard B's path, was adopted wholesale as long as the world stamp matched,
// corrupting det.ShardForEntity's invariant that a citizen ID always
// resolves to exactly one shard. FIXED in this same round: coldShardWire
// now also stamps its OWN shard index (F2), verified alongside the world
// seed, so this exact attack shape is refused rather than silently adopted.
// Updated (not left "as-is") because a characterization test whose whole
// assertion is "the vulnerability exists" necessarily flips once the
// vulnerability it names is closed -- keeping the old assertions here would
// mean this test could never be green again after a correct F2 fix.
func TestAttackBug713ForeignShardFileAdoptedUnderMatchingSeed(t *testing.T) {
	dir := t.TempDir()
	const seed = uint64(0x5EED)
	ps := NewPageStore(dir, 4, seed)

	shardA := det.ShardForEntity(1)
	shardB := (shardA + 1) % numColdShards

	sa := newColdShard(0)
	sa.append(mkRecord(1, 0))
	if err := ps.Store(shardA, sa); err != nil {
		t.Fatalf("store A: %v", err)
	}
	ps.Forget(shardA)

	// Copy shard A's page file over shard B's path (a plausible operator
	// mistake, a partial restore, or a rename in the page dir).
	data, err := os.ReadFile(ps.pathFor(shardA))
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if err := os.WriteFile(ps.pathFor(shardB), data, 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}

	got, ok, err := ps.Load(shardB, "attack-foreign-shard")
	if err == nil {
		t.Fatalf("F2 FIX REGRESSION: Load(%d, ...) of a page file stamped shard=%d "+
			"succeeded with no error (ok=%v got=%+v) -- the matching world stamp is "+
			"no longer sufficient cover; ShardIndex/ShardIndexStamped must also be "+
			"checked", shardB, shardA, ok, got)
	}
	if ok || got != nil {
		t.Fatalf("Load returned ok=%v got=%+v alongside a non-nil error -- a shard-"+
			"index mismatch must never hand back a shard", ok, got)
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrPageShardIndexMismatch {
		t.Fatalf("expected ErrPageShardIndexMismatch, got: %#v", err)
	}
	t.Logf("F2 CLOSED: citizen 1 (det.ShardForEntity = %d) was REFUSED adoption into "+
		"shard %d from a mis-placed page file with a matching world stamp -- "+
		"coldShardWire's ShardIndex stamp (added this round) caught what the world "+
		"seed check alone could not", shardA, shardB)
}

// TestAttackBug713SameSeedTwoCitiesAliasPageFiles attacks the round's item
// (3): worldSeed is a WORLD seed, not a city/lineage identity. Two cities
// created with the same seed (a "Start Over" then "New Game" with the same
// seed -- the exact BUG-687 shape) produce PageStores whose stamps are
// IDENTICAL, so city B happily adopts city A's page files if they share a
// directory. The stamp closes the different-seed case only.
func TestAttackBug713SameSeedTwoCitiesAliasPageFiles(t *testing.T) {
	dir := t.TempDir()
	const seed = uint64(0x1234)

	cityA := NewPageStore(dir, 4, seed)
	sa := newColdShard(0)
	sa.append(mkRecord(99, 0))
	if err := cityA.Store(5, sa); err != nil {
		t.Fatalf("store A: %v", err)
	}

	// A brand new city, same seed, same page directory (no lineage id is
	// available inside this package -- see EnableDiskPaging's own comment).
	cityB := NewPageStore(dir, 4, seed)
	got, ok, err := cityB.Load(5, "attack-same-seed")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if !ok || got == nil || got.rowOf(99) < 0 {
		t.Fatalf("expected city B to adopt city A's page (ok=%v)", ok)
	}
	t.Logf("FINDING F3: a second city with the SAME world seed adopted the first " +
		"city's page file. The BUG-713 P3 stamp distinguishes worlds, not lineages; " +
		"the page directory must therefore be per-lineage at the compose/persist " +
		"layer. Today EnableDiskPaging has NO production caller at all, so this is " +
		"latent, not live")
}

// TestAttackBug713ZeroStampBypassesTheCheck attacks the round's item (4)
// legacy carve-out: a page file whose stamped seed is 0 is accepted by ANY
// world. That is the documented compatibility rule, but it also means the
// stamp is trivially defeatable by anything that writes a zero -- including
// a truncated/rewritten file that still decodes.
func TestAttackBug713ZeroStampBypassesTheCheck(t *testing.T) {
	dir := t.TempDir()
	writer := NewPageStore(dir, 4, 0) // unstamped, i.e. WorldSeed == 0 on disk
	s := newColdShard(0)
	s.append(mkRecord(7, 0))
	if err := writer.Store(11, s); err != nil {
		t.Fatalf("store: %v", err)
	}
	for _, seed := range []uint64{1, 0xFFFF_FFFF_FFFF_FFFF, 0xAAAA} {
		reader := NewPageStore(dir, 4, seed)
		got, ok, err := reader.Load(11, "attack-zero-stamp")
		if err != nil || !ok || got == nil {
			t.Fatalf("seed %d: legacy zero-stamped page should be accepted, got ok=%v err=%v", seed, ok, err)
		}
	}
	t.Log("FINDING F4 (informational): the zero stamp is a universal skeleton key. " +
		"Accepted as the documented legacy carve-out, but it means the identity " +
		"check cannot be relied on as a security boundary, only as a mistake-catcher")
}

// TestAttackBug713ForgetIsIdempotentAndSafe: Forget on a never-resident
// shard, twice on the same shard, and on a shard with a live disk file must
// never panic, never touch disk, and never make a subsequent Load fail.
func TestAttackBug713ForgetIsIdempotentAndSafe(t *testing.T) {
	dir := t.TempDir()
	ps := NewPageStore(dir, 4, 0x77)
	ps.Forget(0) // never resident
	s := newColdShard(0)
	s.append(mkRecord(5, 0))
	if err := ps.Store(9, s); err != nil {
		t.Fatalf("store: %v", err)
	}
	ps.Forget(9)
	ps.Forget(9) // idempotent
	if ps.ResidentCount() != 0 {
		t.Fatalf("resident count after Forget = %d, want 0", ps.ResidentCount())
	}
	got, ok, err := ps.Load(9, "attack-forget")
	if err != nil || !ok || got == nil || got.rowOf(5) < 0 {
		t.Fatalf("Forget must never destroy the on-disk copy: ok=%v err=%v", ok, err)
	}
	if ps.ResidentCount() != 1 {
		t.Fatalf("after a real disk Load resident count = %d, want 1", ps.ResidentCount())
	}
}

// TestAttackBug713PageStoreOrderStaysConsistentAfterForget: forgetLocked
// splices p.order by value. If a shard index ever appeared twice in order
// (makeResidentLocked only appends when NOT already resident, so it should
// not) the splice would leave a dangling entry and the LRU victim choice
// would drift. Hammer Store/Forget/Load cycles and assert order and
// resident stay in lockstep.
func TestAttackBug713PageStoreOrderStaysConsistentAfterForget(t *testing.T) {
	dir := t.TempDir()
	ps := NewPageStore(dir, 3, 0x99)
	for round := 0; round < 40; round++ {
		for shard := 0; shard < 6; shard++ {
			s := newColdShard(0)
			s.append(mkRecord(uint64(shard+1), 0))
			if err := ps.Store(shard, s); err != nil {
				t.Fatalf("store: %v", err)
			}
			if round%2 == 0 {
				ps.Forget(shard)
			}
			if _, _, err := ps.Load(shard, "attack-order"); err != nil {
				t.Fatalf("load: %v", err)
			}
		}
		ps.mu.Lock()
		nRes, nOrd := len(ps.resident), len(ps.order)
		seen := map[int]bool{}
		dup := false
		for _, v := range ps.order {
			if seen[v] {
				dup = true
			}
			seen[v] = true
		}
		ps.mu.Unlock()
		if nRes != nOrd || dup {
			t.Fatalf("round %d: resident=%d order=%d dup=%v — LRU bookkeeping drifted", round, nRes, nOrd, dup)
		}
	}
}
