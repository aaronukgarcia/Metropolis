package citizens

import (
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// skipUnlessPerfCurves is the shared opt-in gate for this file's costly
// multi-round/multi-month/multi-worker perf attacks (F4, round 2 advisory
// finding): CI's build-test-vet job runs plain `go test ./... -race
// -count=1` with NO -short flag (confirmed against .github/workflows/ci.yml,
// same finding tickperf_test.go's TestAdvanceDayTickCurveParallel already
// documents), so a testing.Short() guard is dead code there and these tests
// would otherwise run at full scale on every push (measured: ~34.7s
// combined under -race). Matches tickperf_test.go's own
// METRO_PERF_CURVES=1 opt-in pattern exactly.
func skipUnlessPerfCurves(t *testing.T) {
	t.Helper()
	if os.Getenv("METRO_PERF_CURVES") == "" {
		t.Skip("opt-in perf curve: set METRO_PERF_CURVES=1 to run (CI's go test ./... -race carries no -short flag, so testing.Short() cannot gate this)")
	}
}

// attack_bug663_r2_test.go -- ROUND 2 independent destructive attack on the
// BUG-663 REWORK (per-shard membership index on DeathQueue replacing the
// REJECTED QueuedSnapshot). Attacker != author, and != round 1's attacker.
//
// The thesis under attack: shardIndex is a MIRROR of `queued`, maintained by
// three call sites (Enqueue/realiseLocked/RealiseByID) with a deliberately
// SPLIT lock discipline -- Enqueue commits to `queued` under q.mu, RELEASES
// q.mu, and only then takes shardMu. If the mirror can ever diverge from the
// canonical map, coldpass.go's applyMonthly either (a) re-draws an
// already-queued citizen (index says false, queue says true) -- Enqueue then
// fails, the error is discarded, and tot.selected++ still fires, permanently
// inflating selections above realisations and breaking AC-2 conservation --
// or (b) refuses to ever draw a citizen who is not queued (index says true,
// queue says false) -- an immortal citizen.

// indexMembership dumps the whole shardIndex as one flat set, under the
// shard locks, so a test can compare it against `queued`. Test-only oracle:
// deliberately NOT a production accessor (production code must never iterate
// these maps -- GR#21).
func indexMembership(q *DeathQueue) map[uint64]struct{} {
	out := make(map[uint64]struct{})
	for s := 0; s < numColdShards; s++ {
		q.shardMu[s].Lock()
		for id := range q.shardIndex[s] {
			if got := det.ShardForEntity(id); got != s {
				panic("index slot disagrees with det.ShardForEntity")
			}
			out[id] = struct{}{}
		}
		q.shardMu[s].Unlock()
	}
	return out
}

func assertIndexMirrorsQueue(t *testing.T, q *DeathQueue, when string) {
	t.Helper()
	idx := indexMembership(q)
	q.mu.Lock()
	queued := make(map[uint64]struct{}, len(q.queued))
	for id := range q.queued {
		queued[id] = struct{}{}
	}
	q.mu.Unlock()

	for id := range queued {
		if _, ok := idx[id]; !ok {
			t.Fatalf("%s: DIVERGENCE (queued but NOT indexed): citizen %d — applyMonthly would re-draw an already-queued citizen, inflating selections forever", when, id)
		}
	}
	for id := range idx {
		if _, ok := queued[id]; !ok {
			t.Fatalf("%s: DIVERGENCE (indexed but NOT queued): citizen %d — a false-positive IsQueuedInShard makes this citizen immortal", when, id)
		}
	}
}

// --- A1: the split-lock window, hammered ---------------------------------

// TestR2IndexQueueDivergenceUnderRace hammers the exact interleaving the
// rework's own doc comment waves away: Enqueue releases q.mu BEFORE taking
// shardMu, so a concurrent RealiseByID for the SAME citizen can commit its
// own indexRemove (a no-op, the insert has not happened yet) and let the
// losing Enqueue then insert a REALISED id into the index.
//
// Run with -race. The assertion is not "no data race" (there is none, both
// maps are locked) — it is index==queue membership after full quiesce.
func TestR2IndexQueueDivergenceUnderRace(t *testing.T) {
	skipUnlessPerfCurves(t)
	const rounds = 4000
	dq := NewDeathQueue()

	for r := 0; r < rounds; r++ {
		id := uint64(r + 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = dq.Enqueue(id, 1, "r2")
		}()
		go func() {
			defer wg.Done()
			// Spin briefly so the two land in the same window more often
			// than a cold start would allow.
			for i := 0; i < 8; i++ {
				if err := dq.RealiseByID(id, 2, "r2"); err == nil {
					return
				}
				runtime.Gosched()
			}
		}()
		wg.Wait()
	}
	assertIndexMirrorsQueue(t, dq, "after concurrent Enqueue/RealiseByID hammer")
}

// --- A2: the live oracle over real ticks ----------------------------------

// TestR2IndexOracleAcrossRealMonths runs the REAL tick path (multi-worker,
// heavy mortality, births, realisations, budget backlog) and re-proves,
// after EVERY month, that the per-shard index and the canonical queued map
// agree exactly, and that IsQueuedInShard agrees with IsQueued for every
// live citizen. A one-entry drift anywhere is caught with the id named.
func TestR2IndexOracleAcrossRealMonths(t *testing.T) {
	skipUnlessPerfCurves(t)
	api, err := NewCitizensAPI(0xA77AC4, "r2")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = 20

	const month = int64(20000)
	const n = 3000
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		if i%3 == 0 {
			recs = append(recs, mkGuaranteedDeathRecord(uint64(i), month))
			continue
		}
		r := mkRecord(uint64(i), uint16(i%64))
		r.BirthMonth = month - int64((i*17)%1140)
		r.Household = uint64(i / 2)
		if i%2 == 1 {
			r.Partner = uint64(i + 1)
		} else {
			r.Partner = uint64(i - 1)
		}
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "r2"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	dq := api.deathQueue
	for m := 0; m < 24; m++ {
		if err := api.AdvanceMonth("r2"); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", m, err)
		}
		assertIndexMirrorsQueue(t, dq, "after month")

		// Cross-check the two membership accessors agree for every id the
		// queue knows about, through the PUBLIC shard-addressed API.
		dq.mu.Lock()
		ids := make([]uint64, 0, len(dq.queued))
		for id := range dq.queued {
			ids = append(ids, id)
		}
		dq.mu.Unlock()
		for _, id := range ids {
			if !dq.IsQueuedInShard(det.ShardForEntity(id), id, "r2") {
				t.Fatalf("month %d: IsQueuedInShard=false for queued citizen %d (fail-open: it would be re-drawn)", m, id)
			}
		}
	}
	if dq.Len("r2") == 0 || len(dq.RealisedSequence("r2")) == 0 {
		t.Fatalf("fixture broken: pending=%d realised=%d, need both non-zero", dq.Len("r2"), len(dq.RealisedSequence("r2")))
	}
	t.Logf("24 months workers=20: pending=%d realised=%d pop=%d", dq.Len("r2"), len(dq.RealisedSequence("r2")), api.TotalPopulation("r2"))
}

// --- A3: the departure-while-pending dequeue (FEAT-087) -------------------

// TestR2DepartureClearsShardIndex hunts the specific hazard the rework's
// three indexRemove call sites could have missed: a citizen who is QUEUED
// and then leaves by a NON-death route (emigration -> LifeEventDeath's
// generic departure path, registry.go's reconciliation). If that path
// removed the citizen from `queued` without mirroring into shardIndex, the
// id would answer IsQueuedInShard=true forever — a permanent false positive.
func TestR2DepartureClearsShardIndex(t *testing.T) {
	api, err := NewCitizensAPI(4242, "r2")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const month = int64(20000)
	const id = uint64(7777)
	if err := api.SeedColdRecords([]ColdRecord{mkGuaranteedDeathRecord(id, month)}, "r2"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	dq := api.deathQueue
	if err := dq.Enqueue(id, month, "r2"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	shard := det.ShardForEntity(id)
	if !dq.IsQueuedInShard(shard, id, "r2") {
		t.Fatalf("fixture: citizen %d not indexed after Enqueue", id)
	}

	if err := api.ApplyLifeEventCommand(LifeEventCommand{
		Kind: LifeEventDeath, CitizenID: id, CorrelationID: "r2",
	}); err != nil {
		t.Fatalf("departure: %v", err)
	}

	if dq.IsQueuedInShard(shard, id, "r2") {
		t.Fatalf("STALE INDEX: departed citizen %d still reports IsQueuedInShard=true — permanent false positive", id)
	}
	assertIndexMirrorsQueue(t, dq, "after departure-while-pending")
}

// --- A4: budgeted realisation clears the index ----------------------------

// TestR2RealiseClearsIndexExactly proves realiseLocked's mirror removes
// EXACTLY the released ids and no others, across several partial budget
// releases (the shipped configuration realises far fewer than it selects, so
// the partial-release path is the normal one, not an edge case).
func TestR2RealiseClearsIndexExactly(t *testing.T) {
	dq := NewDeathQueue()
	const total = 500
	for i := 1; i <= total; i++ {
		if err := dq.Enqueue(uint64(i), 1, "r2"); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	assertIndexMirrorsQueue(t, dq, "after 500 enqueues")

	released := 0
	for b := 0; b < 5; b++ {
		out := dq.Realise(37, int64(2+b), "r2")
		released += len(out)
		for _, id := range out {
			if dq.IsQueuedInShard(det.ShardForEntity(id), id, "r2") {
				t.Fatalf("realised citizen %d still indexed — a dead citizen blocks nothing but leaks a permanent true", id)
			}
		}
		assertIndexMirrorsQueue(t, dq, "after partial realise")
	}
	if released != 5*37 {
		t.Fatalf("released %d, want %d", released, 5*37)
	}
	if got := len(indexMembership(dq)); got != total-released {
		t.Fatalf("index size %d, want %d", got, total-released)
	}
}

// --- A5: determinism (GR#21) ---------------------------------------------

// TestR2PopulationHashInvariantAcrossWorkers re-proves round 1's worker
// invariance against the NEW index. A map-backed membership structure is
// exactly where an order leak hides; this pins that none reaches an outcome.
func TestR2PopulationHashInvariantAcrossWorkers(t *testing.T) {
	skipUnlessPerfCurves(t)
	run := func(workers int) ([32]byte, int, []uint64) {
		api, err := NewCitizensAPI(0xD37E4, "r2")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		const month = int64(20000)
		const n = 8000
		recs := make([]ColdRecord, 0, n)
		for i := 1; i <= n; i++ {
			if i%4 == 0 {
				recs = append(recs, mkGuaranteedDeathRecord(uint64(i), month))
				continue
			}
			r := mkRecord(uint64(i), uint16(i%64))
			r.BirthMonth = month - int64((i*23)%1140)
			r.Household = uint64(i / 2)
			recs = append(recs, r)
		}
		if err := api.SeedColdRecords(recs, "r2"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		for m := 0; m < 8; m++ {
			if err := api.AdvanceMonth("r2"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("r2"), api.TotalPopulation("r2"), api.deathQueue.RealisedSequence("r2")
	}

	wantHash, wantPop, wantSeq := run(1)
	for _, w := range []int{1, 4, 20} {
		for rep := 0; rep < 2; rep++ {
			h, pop, seq := run(w)
			if h != wantHash {
				t.Fatalf("workers=%d rep=%d: PopulationHash %x != %x (map-order leak)", w, rep, h, wantHash)
			}
			if pop != wantPop {
				t.Fatalf("workers=%d rep=%d: population %d != %d", w, rep, pop, wantPop)
			}
			if len(seq) != len(wantSeq) {
				t.Fatalf("workers=%d rep=%d: realised %d != %d", w, rep, len(seq), len(wantSeq))
			}
			for i := range seq {
				if seq[i] != wantSeq[i] {
					t.Fatalf("workers=%d rep=%d: realised[%d]=%d != %d (FIFO order leaked)", w, rep, i, seq[i], wantSeq[i])
				}
			}
		}
	}
	t.Logf("hash %x stable at workers 1/4/20 x2, pop=%d realised=%d", wantHash, wantPop, len(wantSeq))
}

// --- A6: the shard-mismatch fail-open contract ----------------------------

// TestR2IsQueuedInShardWrongShardFailsOpen pins the DOCUMENTED degrade
// behaviour of a mismatched shard argument so it can never change silently:
// it returns false (fail-OPEN, the citizen gets re-drawn), it does not
// panic, and out-of-range values are bounded. This is the residual of round
// 1's nil-map finding: the nullable map is gone, but a WRONG int is still a
// silent miss rather than an error.
func TestR2IsQueuedInShardWrongShardFailsOpen(t *testing.T) {
	dq := NewDeathQueue()
	const id = uint64(31415)
	if err := dq.Enqueue(id, 1, "r2"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	right := det.ShardForEntity(id)
	if !dq.IsQueuedInShard(right, id, "r2") {
		t.Fatalf("correct shard %d reports not queued", right)
	}
	wrong := (right + 1) % numColdShards
	if dq.IsQueuedInShard(wrong, id, "r2") {
		t.Fatalf("wrong shard %d reported queued — the partition is not what it claims", wrong)
	}
	for _, s := range []int{-1, -1 << 40, numColdShards, 1 << 40} {
		if dq.IsQueuedInShard(s, id, "r2") {
			t.Fatalf("out-of-range shard %d reported queued", s)
		}
	}
}
