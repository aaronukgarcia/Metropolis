package citizens

import (
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// attack_bug663_round_test.go — the INDEPENDENT destructive round (GR#23,
// attacker != author) on BUG-663: originally DeathQueue.QueuedSnapshot
// replacing the per-citizen dq.IsQueued call in ColdShard.applyMonthly, the
// coldParamsLocked shard-streaming sampler refactor, and the
// SeedColdRecords duplicate-id guard.
//
// POST-REJECT REWORK (this file's tests (1) and (5) updated in place, GR#23
// -- the round's own tests are the acceptance bar, not a frozen artifact,
// per the rework brief's explicit "or adapt it to the new index" allowance
// for the benchmark): the round's own TestBUG663SnapshotCostScalesWithPendingQueue
// (below) proved QueuedSnapshot's O(pendingQueue) per-day-tick allocation
// cost 70x worse than pre-fix at 1M pending -- REJECTED. applyMonthly now
// takes the caller's own shard index and queries
// [DeathQueue.IsQueuedInShard] (O(1), no allocation, no cross-shard lock
// contention) instead of a map snapshot; QueuedSnapshot itself is UNCHANGED
// and still exists (BenchmarkBUG663QueuedSnapshot below still measures its
// real, unchanged cost) but is no longer on AdvanceDayTick's per-day-tick
// path. Tests (2)/(3)/(4) below are untouched by this rework -- they never
// called applyMonthly's snapshot parameter directly.
//
// The attack thesis these tests exist to settle:
//
//  1. THE STALENESS WINDOW. The snapshot is taken BEFORE the parallel
//     shard pass; Enqueue still mutates the live queue DURING the pass.
//     Can a citizen be hazard-selected while ALSO passing the stale
//     snapshot check, i.e. enqueued twice -> died twice?
//  2. DETERMINISM across worker counts under heavy mortality.
//  3. The sampler refactor's byte-identical claim, including the
//     reused-scratch-buffer aliasing hazard.
//  4. The duplicate-id guard: within-batch, cross-call, wire boundary.
//  5. The snapshot's own COST as the pending queue grows.

// --- (1) the staleness window -------------------------------------------

// TestBUG663DoubleEnqueueIsStructurallyImpossible answers the round's
// headline question directly, at the only layer where it can be answered
// unconditionally: DeathQueue.Enqueue itself.
//
// The snapshot check in applyMonthly is an OPTIMISATION, not the safety
// property — the terminal guard is Enqueue's own queued/realisedAt
// rejection under q.mu. This test constructs the worst case the stale
// snapshot can possibly produce (the SAME id drawn for mortality twice
// inside one applyMonthly call, which a stale snapshot cannot suppress
// because the snapshot predates the first enqueue) by placing a duplicate
// id in one shard's columns directly, bypassing SeedColdRecords' guard.
//
// Result, proven not asserted-by-inspection: the queue accepts the id
// exactly ONCE. The stale snapshot's ONLY observable consequence is that
// passTotals.selected is double-counted for that draw — a number
// registry.go's own comment documents as informational and explicitly
// forbids from feeding curMonthDeaths or the returned deaths count.
func TestBUG663DoubleEnqueueIsStructurallyImpossible(t *testing.T) {
	const month = int64(20000)
	const dupID = uint64(4242)

	shard := det.ShardForEntity(dupID)
	s := newColdShard(0)
	rec := mkGuaranteedDeathRecord(dupID, month)
	s.append(rec)
	s.append(rec) // the duplicate row SeedColdRecords now rejects
	if s.count() != 2 {
		t.Fatalf("fixture: shard count = %d, want 2", s.count())
	}

	dq := NewDeathQueue()
	tot := s.applyMonthly(1, month, ColdPassParams{MortalityMultiplier: 1}, nil, dq, shard, "attack")

	if got := dq.Len("attack"); got != 1 {
		t.Fatalf("DOUBLE ENQUEUE: queue length = %d, want 1 (a citizen must never be queued twice)", got)
	}
	if _, ok := dq.IsQueued(dupID, "attack"); !ok {
		t.Fatalf("fixture broken: guaranteed-death citizen %d was not queued at all", dupID)
	}
	// BUG-663 round 2 F2 fix: under the LIVE per-shard index (this rework),
	// there is no staleness window left to produce a double count at all --
	// the second duplicate row's IsQueuedInShard check runs AFTER the first
	// row's Enqueue has already landed (both inside this same applyMonthly
	// call, on the same goroutine), so the second row is filtered before
	// ever drawing the mortality hazard. selected must be exactly 1, never
	// 2 -- this assertion was a t.Logf (never fires) before the fix, which
	// made the test vacuous; it is now a real Fatalf pinning the corrected
	// value.
	if tot.selected != 1 {
		t.Fatalf("selected=%d, want 1 (the live index -- no snapshot staleness window -- must filter the duplicate row's own draw)", tot.selected)
	}
	realised := dq.Realise(100, month, "attack")
	if len(realised) != 1 {
		t.Fatalf("DOUBLE DEATH: Realise returned %d ids %v, want exactly 1", len(realised), realised)
	}
}

// TestBUG663NoDuplicateColdIDsUnderLiveTicks proves the invariant that
// makes the snapshot observationally identical to a live per-citizen
// IsQueued: every citizen id is resident in exactly one cold row, in
// exactly one shard, at all times — through months of real ticks
// including fertility births and death realisations. If this holds, an id
// is visited at most once per month by applyMonthly, so no id can reach
// the mortality draw twice inside one pass, so the snapshot's staleness
// can never be observed.
func TestBUG663NoDuplicateColdIDsUnderLiveTicks(t *testing.T) {
	api, err := NewCitizensAPI(90210, "attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = 14

	const month = int64(20000)
	recs := make([]ColdRecord, 0, 4000)
	for i := 1; i <= 3000; i++ {
		r := mkRecord(uint64(i), uint16(i%16))
		r.BirthMonth = month - 300 // fertile adults: births will fire
		r.Household = uint64(i / 2)
		if i%2 == 1 {
			r.Partner = uint64(i + 1)
		} else {
			r.Partner = uint64(i - 1)
		}
		recs = append(recs, r)
	}
	for i := 3001; i <= 3400; i++ { // guaranteed deaths: realisations will fire
		recs = append(recs, mkGuaranteedDeathRecord(uint64(i), month))
	}
	if err := api.SeedColdRecords(recs, "attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	checkUnique := func(when string) {
		seen := make(map[uint64]int, 4000)
		api.mu.Lock()
		defer api.mu.Unlock()
		for si, s := range api.cold {
			for row := 0; row < s.count(); row++ {
				id := s.ids[row]
				if prev, dup := seen[id]; dup {
					t.Fatalf("%s: DUPLICATE cold id %d (shards %d and %d) — the snapshot's whole safety argument fails", when, id, prev, si)
				}
				seen[id] = si
				if want := det.ShardForEntity(id); want != si {
					t.Fatalf("%s: id %d resident in shard %d, want %d (an id must belong to exactly ONE shard)", when, id, si, want)
				}
			}
		}
	}

	checkUnique("after seed")
	for m := 0; m < 24; m++ {
		if err := api.AdvanceMonth("attack"); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", m, err)
		}
		checkUnique(fmt.Sprintf("after month %d", m))
	}
	if api.TotalPopulation("attack") == 3400 {
		t.Fatal("fixture broken: population never changed across 24 months (no births and no deaths exercised)")
	}
}

// TestBUG663ConservationUnderParallelTicks: over a long parallel run with
// heavy mortality, total selections == realised + still-pending, no id is
// realised twice, and the living population equals seeded + births -
// realised. A stale-snapshot double-enqueue would break the first; a lost
// enqueue would break it the other way.
func TestBUG663ConservationUnderParallelTicks(t *testing.T) {
	api, err := NewCitizensAPI(31337, "attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = 20

	const month = int64(20000)
	const n = 2000
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		recs = append(recs, mkGuaranteedDeathRecord(uint64(i), month))
	}
	if err := api.SeedColdRecords(recs, "attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	for m := 0; m < 36; m++ {
		if err := api.AdvanceMonth("attack"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}

	dq := api.deathQueue
	dq.mu.Lock()
	pending := len(dq.pending)
	realised := append([]uint64(nil), dq.realisedIDs...)
	queuedSet := len(dq.queued)
	dq.mu.Unlock()

	if pending != queuedSet {
		t.Fatalf("queue internal drift: pending slice %d vs queued set %d", pending, queuedSet)
	}
	seen := make(map[uint64]bool, len(realised))
	for _, id := range realised {
		if seen[id] {
			t.Fatalf("DOUBLE REALISATION of citizen %d — a double death reached the population", id)
		}
		seen[id] = true
	}
	pop := api.TotalPopulation("attack")
	if pop+len(realised) != n {
		t.Fatalf("conservation broken: population %d + realised %d != seeded %d", pop, len(realised), n)
	}
	if len(realised) == 0 || pending == 0 {
		t.Fatalf("fixture broken: realised=%d pending=%d (need both non-zero to exercise the cross-month queue)", len(realised), pending)
	}
	t.Logf("36 months at workers=20: realised=%d pending=%d population=%d", len(realised), pending, pop)
}

// --- (2) determinism across worker counts --------------------------------

// TestBUG663HeavyMortalityDeterministicAcrossWorkerCounts strengthens
// TestLiveColdPassDeterministicAcrossWorkerCounts: a much larger cohort
// (deathwave-scale relative to its population), run TWICE at each of
// workers 1/4/20, must produce a byte-identical PopulationHash, the same
// population, and the same realised sequence and pending queue.
func TestBUG663HeavyMortalityDeterministicAcrossWorkerCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-worker-count month runs are too slow for -short")
	}
	const seed = uint64(0xBEEF663)
	const month = int64(20000)
	const n = 30000

	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		if i%3 == 0 {
			recs = append(recs, mkGuaranteedDeathRecord(uint64(i), month))
			continue
		}
		r := mkRecord(uint64(i), uint16(i%32))
		r.BirthMonth = month - int64(240+(i%600))
		r.Household = uint64(i / 2)
		if i%2 == 1 {
			r.Partner = uint64(i + 1)
		} else {
			r.Partner = uint64(i - 1)
		}
		recs = append(recs, r)
	}

	type outcome struct {
		hash     [32]byte
		pop      int
		realised []uint64
		pending  []uint64
	}
	run := func(workers int) outcome {
		api, err := NewCitizensAPI(seed, "attack")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		if err := api.SeedColdRecords(recs, "attack"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		for m := 0; m < 8; m++ {
			if err := api.AdvanceMonth("attack"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		dq := api.deathQueue
		dq.mu.Lock()
		out := outcome{
			hash:     api.PopulationHash("attack"),
			pop:      api.TotalPopulation("attack"),
			realised: append([]uint64(nil), dq.realisedIDs...),
		}
		for _, e := range dq.pending {
			out.pending = append(out.pending, e.citizenID)
		}
		dq.mu.Unlock()
		sort.Slice(out.pending, func(i, j int) bool { return out.pending[i] < out.pending[j] })
		return out
	}

	var ref outcome
	for _, workers := range []int{1, 4, 20} {
		for pass := 0; pass < 2; pass++ {
			got := run(workers)
			if workers == 1 && pass == 0 {
				ref = got
				if len(ref.realised) == 0 || len(ref.pending) == 0 {
					t.Fatalf("fixture broken: realised=%d pending=%d", len(ref.realised), len(ref.pending))
				}
				t.Logf("reference: pop=%d realised=%d pending=%d hash=%x", ref.pop, len(ref.realised), len(ref.pending), ref.hash[:8])
				continue
			}
			if got.hash != ref.hash {
				t.Fatalf("workers=%d pass=%d: PopulationHash %x != reference %x", workers, pass, got.hash[:8], ref.hash[:8])
			}
			if got.pop != ref.pop {
				t.Fatalf("workers=%d pass=%d: population %d != %d", workers, pass, got.pop, ref.pop)
			}
			if len(got.realised) != len(ref.realised) {
				t.Fatalf("workers=%d pass=%d: realised %d != %d", workers, pass, len(got.realised), len(ref.realised))
			}
			for i := range got.realised {
				if got.realised[i] != ref.realised[i] {
					t.Fatalf("workers=%d pass=%d: realised sequence diverges at %d: %d != %d", workers, pass, i, got.realised[i], ref.realised[i])
				}
			}
			if len(got.pending) != len(ref.pending) {
				t.Fatalf("workers=%d pass=%d: pending %d != %d", workers, pass, len(got.pending), len(ref.pending))
			}
			for i := range got.pending {
				if got.pending[i] != ref.pending[i] {
					t.Fatalf("workers=%d pass=%d: pending set diverges at %d", workers, pass, i)
				}
			}
		}
	}
}

// --- (3) the sampler refactor -------------------------------------------

// oracleBuildStratifiedSample is a SCRATCH COPY of BuildStratifiedSample
// exactly as it stood before BUG-663's stratifiedSampleBuilder refactor
// (git show HEAD:internal/engine/citizens/sampling.go). It is the
// independent oracle for the "byte-identical output" claim — the refactor
// is compared against this, never against itself.
func oracleBuildStratifiedSample(records []ColdRecord, month int64, seed uint64, minPerStratum int) *StratifiedSample {
	if minPerStratum < 0 {
		minPerStratum = 0
	}
	s := &StratifiedSample{
		counts:        make(map[Stratum]int),
		minPerStratum: minPerStratum,
		month:         month,
		seed:          seed,
	}
	byStratum := make(map[Stratum][]uint64)
	for _, r := range records {
		st := StratumOf(r, month)
		byStratum[st] = append(byStratum[st], r.ID)
	}
	strata := make([]Stratum, 0, len(byStratum))
	for st := range byStratum {
		strata = append(strata, st)
	}
	sort.Slice(strata, func(i, j int) bool {
		a, b := strata[i], strata[j]
		if a.District != b.District {
			return a.District < b.District
		}
		if a.Age != b.Age {
			return a.Age < b.Age
		}
		return a.Income < b.Income
	})
	selected := make(map[uint64]bool)
	var ordered []uint64
	add := func(id uint64, st Stratum) {
		if !selected[id] {
			selected[id] = true
			ordered = append(ordered, id)
			s.counts[st]++
		}
	}
	for _, st := range strata {
		ids := byStratum[st]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		covered := 0
		for i := 0; i < len(ids) && covered < minPerStratum; i++ {
			add(ids[i], st)
			covered++
		}
		for _, id := range ids {
			stream := det.NewStream(seed, id, month, "sample")
			if stream.Float64() < sampleFraction {
				add(id, st)
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	s.members = ordered
	return s
}

func sampleEqual(t *testing.T, what string, got, want *StratifiedSample) {
	t.Helper()
	g, w := got.Members(), want.Members()
	if len(g) != len(w) {
		t.Fatalf("%s: member count %d != oracle %d", what, len(g), len(w))
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("%s: member[%d] = %d != oracle %d", what, i, g[i], w[i])
		}
	}
	if len(got.counts) != len(want.counts) {
		t.Fatalf("%s: stratum count map size %d != oracle %d", what, len(got.counts), len(want.counts))
	}
	for st, c := range want.counts {
		if got.counts[st] != c {
			t.Fatalf("%s: counts[%+v] = %d != oracle %d", what, st, got.counts[st], c)
		}
	}
	if got.minPerStratum != want.minPerStratum || got.month != want.month || got.seed != want.seed {
		t.Fatalf("%s: rotation identity (min=%d month=%d seed=%d) != oracle (min=%d month=%d seed=%d)",
			what, got.minPerStratum, got.month, got.seed, want.minPerStratum, want.month, want.seed)
	}
	// The parameters actually consumed by the tick must match too.
	if DeriveColdPassParams(got) != DeriveColdPassParams(want) {
		t.Fatalf("%s: DeriveColdPassParams differs from oracle", what)
	}
}

func attackSampleRecords(n int, month int64, spread int) []ColdRecord {
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		r := mkRecord(uint64(i), uint16(i%maxInt1(spread)))
		r.BirthMonth = month - int64((i*7)%900)
		r.Wealth = int64((i * 1013) % 500000)
		recs = append(recs, r)
	}
	return recs
}

func maxInt1(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

// TestBUG663SamplerMatchesPreRefactorOracle: the refactored
// BuildStratifiedSample must equal the pre-refactor implementation across
// seeds, sizes (0, 1, many), months and coverage floors.
func TestBUG663SamplerMatchesPreRefactorOracle(t *testing.T) {
	for _, seed := range []uint64{0, 1, 559, 0xFFFFFFFFFFFFFFFF} {
		for _, n := range []int{0, 1, 2, 37, 1000} {
			for _, minPer := range []int{-1, 0, 1, 3} {
				for _, month := range []int64{0, 1, 20000} {
					recs := attackSampleRecords(n, month, 8)
					what := fmt.Sprintf("seed=%d n=%d min=%d month=%d", seed, n, minPer, month)
					sampleEqual(t, what,
						BuildStratifiedSample(recs, month, seed, minPer),
						oracleBuildStratifiedSample(recs, month, seed, minPer))
				}
			}
		}
	}
}

// TestBUG663SamplerStreamingEqualsFlatSlice: feeding the builder in
// arbitrary batches — including EMPTY batches (an empty shard), one-record
// batches (a shard with a single citizen) and a reversed partition — must
// produce exactly what one flat slice produces. This is the property
// coldParamsLocked's shard-at-a-time loop depends on.
func TestBUG663SamplerStreamingEqualsFlatSlice(t *testing.T) {
	const month = int64(20000)
	const seed = uint64(7)
	recs := attackSampleRecords(997, month, 5)
	want := oracleBuildStratifiedSample(recs, month, seed, 1)

	partitions := [][]int{
		{997},
		{0, 997, 0},
		{1, 1, 1, 994},
		{500, 0, 0, 497},
		{1, 2, 3, 4, 987},
	}
	for pi, sizes := range partitions {
		b := newStratifiedSampleBuilder(month, seed, 1)
		off := 0
		for _, sz := range sizes {
			b.addRecords(recs[off : off+sz])
			off += sz
		}
		if off != len(recs) {
			t.Fatalf("partition %d covers %d of %d records", pi, off, len(recs))
		}
		sampleEqual(t, fmt.Sprintf("partition %v", sizes), b.build(), want)
	}

	// Reverse order of the batches, and reverse the records themselves:
	// order-independence is the documented contract.
	rev := append([]ColdRecord(nil), recs...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	b := newStratifiedSampleBuilder(month, seed, 1)
	b.addRecords(rev[:400])
	b.addRecords(rev[400:])
	sampleEqual(t, "reversed input", b.build(), want)
}

// TestBUG663SamplerBuilderDoesNotAliasCallerBuffer is the specific hazard
// coldParamsLocked's REUSED scratch buffer creates: registry.go fills one
// []ColdRecord buffer, calls addRecords, then OVERWRITES that same buffer
// with the next shard's rows. If the builder retained any reference to the
// caller's slice (rather than extracting r.ID and StratumOf at add time),
// every shard but the last would be silently mis-stratified.
func TestBUG663SamplerBuilderDoesNotAliasCallerBuffer(t *testing.T) {
	const month = int64(20000)
	const seed = uint64(11)
	batchA := attackSampleRecords(300, month, 4)
	batchB := attackSampleRecords(200, month, 4)
	for i := range batchB { // distinct ids from batchA
		batchB[i].ID += 1_000_000
	}
	all := append(append([]ColdRecord(nil), batchA...), batchB...)
	want := oracleBuildStratifiedSample(all, month, seed, 1)

	// Emulate registry.go's reuse pattern exactly: ONE buffer, refilled.
	buf := make([]ColdRecord, 0, 300)
	b := newStratifiedSampleBuilder(month, seed, 1)
	buf = append(buf[:0], batchA...)
	b.addRecords(buf)
	// Poison the buffer between calls with records that must NOT appear.
	for i := range buf {
		buf[i] = ColdRecord{ID: 999_000_000 + uint64(i), District: 4095, Wealth: -1, BirthMonth: month}
	}
	buf = append(buf[:0], batchB...)
	b.addRecords(buf)
	// Poison again after the LAST add, before build().
	poison := make([]ColdRecord, len(buf))
	copy(poison, buf)
	for i := range buf {
		buf[i] = ColdRecord{ID: 888_000_000 + uint64(i), District: 4095, Wealth: -1, BirthMonth: month}
	}
	sampleEqual(t, "reused scratch buffer", b.build(), want)
}

// TestBUG663ColdParamsLockedMatchesFlatMaterialisation: the live
// coldParamsLocked (shard-streamed) must equal the old
// allColdRecordsLocked()-then-BuildStratifiedSample shape, over a
// population with empty shards, single-citizen shards, and full ones.
func TestBUG663ColdParamsLockedMatchesFlatMaterialisation(t *testing.T) {
	api, err := NewCitizensAPI(4242, "attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const month = int64(20000)
	// Sparse ids: many shards get 0 or 1 citizens.
	recs := make([]ColdRecord, 0, 700)
	for i := 1; i <= 500; i++ {
		r := mkRecord(uint64(i)*7919, uint16(i%12))
		r.BirthMonth = month - int64((i*11)%800)
		r.Wealth = int64((i * 977) % 400000)
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	got := api.coldParamsLocked("attack")
	flat := api.allColdRecordsLocked()
	api.mu.Unlock()

	want := DeriveColdPassParams(oracleBuildStratifiedSample(flat, month, api.seed, 1))
	if got != want {
		t.Fatalf("coldParamsLocked = %+v, oracle (flat materialisation) = %+v", got, want)
	}
	empties := 0
	for _, s := range api.cold {
		if s.count() == 0 {
			empties++
		}
	}
	if empties == 0 {
		t.Fatal("fixture broken: no empty shards, the empty-batch path was never exercised")
	}
}

// --- (4) the duplicate-id guard -----------------------------------------

func TestBUG663SeedColdRecordsDuplicateGuard(t *testing.T) {
	newAPI := func() *CitizensAPI {
		api, err := NewCitizensAPI(5, "attack")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		return api
	}

	t.Run("within one batch", func(t *testing.T) {
		api := newAPI()
		err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0), mkRecord(2, 0), mkRecord(1, 0)}, "attack")
		if err == nil {
			t.Fatal("duplicate id inside one batch was ACCEPTED")
		}
	})

	t.Run("across calls", func(t *testing.T) {
		api := newAPI()
		if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0)}, "attack"); err != nil {
			t.Fatalf("first seed: %v", err)
		}
		if err := api.SeedColdRecords([]ColdRecord{mkRecord(1, 0)}, "attack"); err == nil {
			t.Fatal("duplicate id across calls was ACCEPTED")
		}
	})

	t.Run("against a birth-created citizen", func(t *testing.T) {
		api := newAPI()
		if err := api.SeedColdRecords([]ColdRecord{mkRecord(77, 0)}, "attack"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := api.SeedColdRecords([]ColdRecord{mkRecord(77, 3)}, "attack"); err == nil {
			t.Fatal("re-seeding an existing id was ACCEPTED")
		}
	})

	// FINDING (non-blocking, pre-existing shape): the guard rejects
	// mid-loop, AFTER earlier records in the same batch have already been
	// appended — a rejected batch is PARTIALLY applied, exactly as the
	// pre-existing validateShardIndex rejection already was. Pinned here
	// so the behaviour is documented rather than discovered later.
	t.Run("rejection is partial, not atomic", func(t *testing.T) {
		api := newAPI()
		err := api.SeedColdRecords([]ColdRecord{mkRecord(11, 0), mkRecord(12, 0), mkRecord(11, 0), mkRecord(13, 0)}, "attack")
		if err == nil {
			t.Fatal("duplicate not rejected")
		}
		pop := api.TotalPopulation("attack")
		if pop != 2 {
			t.Logf("note: population after rejected batch = %d", pop)
		}
		if _, ok := api.coldRecord(13); ok {
			t.Fatal("a record AFTER the rejected one was applied — the loop did not stop")
		}
	})

	// The guard must not cost correctness for a legitimate large batch.
	t.Run("large clean batch still accepted", func(t *testing.T) {
		api := newAPI()
		recs := make([]ColdRecord, 0, 5000)
		for i := 1; i <= 5000; i++ {
			recs = append(recs, mkRecord(uint64(i), uint16(i%16)))
		}
		if err := api.SeedColdRecords(recs, "attack"); err != nil {
			t.Fatalf("clean batch rejected: %v", err)
		}
		if api.TotalPopulation("attack") != 5000 {
			t.Fatalf("population = %d, want 5000", api.TotalPopulation("attack"))
		}
	})
}

// TestBUG663WirePayloadDuplicateIDBoundary documents the LIMIT of the
// SeedColdRecords guard: the gob paging path (wireToColdShard) rebuilds
// the id->row index from the decoded ids column with NO duplicate check,
// so a wire payload carrying the same id twice yields a shard where the
// EARLIER row is permanently unreachable through rowOf — precisely the
// corruption the SeedColdRecords guard exists to prevent, on a path the
// guard does not cover. PageStore is built-but-not-wired today (BUG-664),
// so this is a documented follow-up, not a live defect; this test pins the
// behaviour so a future wiring cannot land it silently.
func TestBUG663WirePayloadDuplicateIDBoundary(t *testing.T) {
	base := newColdShard(0)
	base.append(mkRecord(5, 1))
	base.append(mkRecord(6, 2))
	w := base.toWire()
	// Forge a duplicate id in the wire payload.
	w.IDs = append([]uint64(nil), w.IDs...)
	w.IDs[1] = 5

	s := wireToColdShard(w)
	if s.count() != 2 {
		t.Fatalf("count = %d, want 2", s.count())
	}
	row := s.rowOf(5)
	if row != 1 {
		t.Fatalf("rowOf(5) = %d, want 1 (last-wins rebuild)", row)
	}
	// Row 0 is now unreachable by id: the boundary this test documents.
	if s.ids[0] != 5 || s.ids[1] != 5 {
		t.Fatalf("fixture broken: ids = %v", s.ids[:2])
	}
	t.Log("BOUNDARY (follow-up): wireToColdShard accepts a duplicate-id payload; rebuildIndexLocked is last-wins and row 0 becomes unreachable. Uncovered by the SeedColdRecords guard.")
}

// --- (5) the snapshot's own cost ----------------------------------------

// TestBUG663SnapshotCostScalesWithPendingQueue is the round's PERFORMANCE
// attack on the fix itself. QueuedSnapshot allocates and fills a map of
// EVERY pending id, once per day-tick — 30 times a month. The old code
// took an uncontended-per-shard mutex O(N) times but allocated nothing.
//
// The pending queue is not small in the shipped configuration:
// data/mortality.json's monthlyDeathBudget is 25 deaths/month while a
// realistic population selects far more than that per month, so the queue
// GROWS monotonically between emergency months (and, with no engine.season
// wired, forever). This test measures the per-tick cost at a fixed
// population with an empty queue versus a large pending queue, so the
// trade the fix makes is a measured number rather than an assumption.
//
// It is a MEASUREMENT, not a wall-clock gate (verification standards: no
// wall-clock bounds in CI) — it always passes and reports.
func TestBUG663SnapshotCostScalesWithPendingQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("timing measurement is too slow for -short")
	}
	const month = int64(20000)
	const n = 200_000

	measure := func(prefill int) time.Duration {
		api, err := NewCitizensAPI(99, "attack")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = runtime.NumCPU()
		recs := make([]ColdRecord, 0, n)
		for i := 1; i <= n; i++ {
			r := mkRecord(uint64(i), uint16(i%64))
			r.BirthMonth = month - 300
			r.Partner = 0
			r.Household = 0
			recs = append(recs, r)
		}
		if err := api.SeedColdRecords(recs, "attack"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		// Prefill the death queue with ids OUTSIDE the population so the
		// only variable is the snapshot's size.
		for i := 0; i < prefill; i++ {
			if err := api.deathQueue.Enqueue(uint64(50_000_000+i), month, "attack"); err != nil {
				t.Fatalf("prefill Enqueue: %v", err)
			}
		}
		if _, _, err := api.AdvanceDayTick("warmup"); err != nil {
			t.Fatalf("warmup: %v", err)
		}
		const ticks = 10
		start := time.Now()
		for i := 0; i < ticks; i++ {
			if _, _, err := api.AdvanceDayTick("measure"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			}
		}
		return time.Since(start) / ticks
	}

	for _, prefill := range []int{0, 100_000, 1_000_000} {
		d := measure(prefill)
		t.Logf("N=%d pendingQueue=%d: %.3f ms/day-tick", n, prefill, float64(d.Nanoseconds())/1e6)
	}
	// ROUND EVIDENCE (measured on this box, same fixture, by scratch-copying
	// coldpass.go/registry.go back to the pre-fix dq.IsQueued shape and
	// re-running this exact test — restored immediately after, GR#24):
	//
	//   pendingQueue        pre-fix (IsQueued)   post-fix (snapshot)
	//   0                   0.892 ms             0.204 ms   (4.4x BETTER)
	//   100,000             1.397 ms             3.150 ms   (2.3x WORSE)
	//   1,000,000           1.823 ms             127.903 ms (70x WORSE)
	//
	// The pre-fix cost is O(N/30) mutex acquisitions and is INDEPENDENT of
	// the queue length; the post-fix cost adds O(pendingQueue) map
	// construction PER DAY-TICK, 30 times a month. See
	// TestBUG663PendingQueueGrowsWithoutBound for why the queue does not
	// stay small.
}

// TestBUG663PendingQueueGrowsWithoutBound is the other half of the
// performance attack: the snapshot's cost only matters if the pending
// queue actually gets big. It does, structurally — data/mortality.json's
// monthlyDeathBudget is a FLAT 25 deaths/month while hazard SELECTIONS
// scale with population, and (with no engine.season wired, the default)
// no emergency month ever releases the backlog. So the queue grows by
// (selections - 25) every month, forever.
//
// This test measures the real selection rate on an ordinary demographic
// mix and reports the resulting growth, so the projection to 100M is a
// measured number rather than an assumption. Measurement only, no gate.
func TestBUG663PendingQueueGrowsWithoutBound(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-month growth measurement is too slow for -short")
	}
	const month = int64(20000)
	const n = 100_000

	api, err := NewCitizensAPI(2026, "attack")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = runtime.NumCPU()
	recs := make([]ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		r := mkRecord(uint64(i), uint16(i%64))
		// A spread of ordinary ages 0..95y, no guaranteed-death fixture.
		r.BirthMonth = month - int64((i*13)%1140)
		r.Household = 0
		r.Partner = 0
		recs = append(recs, r)
	}
	if err := api.SeedColdRecords(recs, "attack"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	const months = 12
	for m := 0; m < months; m++ {
		if err := api.AdvanceMonth("attack"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	pending := api.deathQueue.Len("attack")
	realised := len(api.deathQueue.RealisedSequence("attack"))
	selected := pending + realised
	perMonth := float64(selected) / months
	t.Logf("N=%d ordinary mix, %d months: selected=%d realised=%d pending=%d (%.1f selections/month, budget=%d/month)",
		n, months, selected, realised, pending, perMonth, api.mortalityCfg.MonthlyDeathBudget())
	if pending <= realised {
		t.Logf("note: queue did not outgrow the budget on this fixture")
	}
	// Projection, stated honestly: selections/citizen/month measured here,
	// scaled to the 100M finish line.
	perCitizen := perMonth / float64(n)
	t.Logf("projection: %.3g selections/citizen/month -> ~%.0f selections/month at 100M; queue grows by that minus 25 EVERY month",
		perCitizen, perCitizen*100e6)
}

// TestBUG663WorkerScalingEmptyQueue reproduces the author's headline
// qualitative claim independently: with an EMPTY death queue, does raising
// the worker count help or hurt? Pre-fix, every worker fought over
// DeathQueue.mu once per citizen and more workers made the tick WORSE.
//
// Measured on this box with this fixture (N=500k, empty queue), the same
// test run against a scratch-copied pre-fix coldpass.go/registry.go for
// the pre-fix column:
//
//	          workers=1     workers=20
//	pre-fix   2.027 ms      1.394 ms   (workers still HELP, 1.45x)
//	post-fix  1.861 ms      0.745 ms   (workers help 2.50x)
//
// So the fix's real empty-queue win is 1.394 -> 0.745 ms, 1.87x on the
// multi-worker path. The author's stronger claim (that pre-fix, 20 workers
// made the tick 2.9x WORSE than 1 worker) did NOT reproduce on this box
// with this fixture: pre-fix, more workers still helped. The DIRECTION of
// the fix is confirmed; the magnitude is smaller than reported.
//
// Measurement only, no gate (verification standards: no wall-clock bounds
// in CI). The queue is EMPTY here deliberately — that is the fix's best
// case; TestBUG663SnapshotCostScalesWithPendingQueue covers the rest of
// the operating range.
func TestBUG663WorkerScalingEmptyQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("timing measurement is too slow for -short")
	}
	const month = int64(20000)
	const n = 500_000

	measure := func(workers int) time.Duration {
		api, err := NewCitizensAPI(77, "attack")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		api.workers = workers
		batch := make([]ColdRecord, 0, 100_000)
		for i := 1; i <= n; i++ {
			r := mkRecord(uint64(i), uint16(i%64))
			r.BirthMonth = month - 300
			r.Household = 0
			r.Partner = 0
			batch = append(batch, r)
			if len(batch) == cap(batch) || i == n {
				if err := api.SeedColdRecords(batch, "attack"); err != nil {
					t.Fatalf("SeedColdRecords: %v", err)
				}
				batch = batch[:0]
			}
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		if _, _, err := api.AdvanceDayTick("warmup"); err != nil {
			t.Fatalf("warmup: %v", err)
		}
		const ticks = 15
		start := time.Now()
		for i := 0; i < ticks; i++ {
			if _, _, err := api.AdvanceDayTick("measure"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			}
		}
		return time.Since(start) / ticks
	}

	for _, w := range []int{1, runtime.NumCPU()} {
		d := measure(w)
		t.Logf("N=%d emptyQueue workers=%d: %.3f ms/day-tick", n, w, float64(d.Nanoseconds())/1e6)
	}
}

// TestBUG663NilSnapshotFailsOpen originally pinned a fail-OPEN hazard in the
// map-snapshot applyMonthly signature: alreadyQueued was a plain map, and a
// nil map lookup is a silent MISS, so a caller who forgot to pass the
// snapshot got "nobody is queued" for every already-queued citizen with no
// error and no panic (contained only by Enqueue's own terminal guard).
//
// POST-REJECT REWORK: the finding is closed STRUCTURALLY, not defensively.
// applyMonthly no longer takes a nullable map at all -- it takes the
// caller's own shard index (an int, always valid: every real call site
// derives it from det.ShardForEntity/runShardsParallel's own shard
// argument, never a caller-optional value) and queries the LIVE
// [DeathQueue.IsQueuedInShard] every time. There is no "forgotten snapshot"
// state left to fail open into: renamed to
// TestBUG663LiveIndexNeverFailsOpen to test the actual current contract --
// an already-queued citizen is NEVER re-drawn, on every call, with no
// snapshot step to skip.
func TestBUG663LiveIndexNeverFailsOpen(t *testing.T) {
	const month = int64(20000)
	const id = uint64(909)

	s := newColdShard(0)
	s.append(mkGuaranteedDeathRecord(id, month))
	dq := NewDeathQueue()
	if err := dq.Enqueue(id, month-1, "attack"); err != nil {
		t.Fatalf("pre-Enqueue: %v", err)
	}
	shard := det.ShardForEntity(id)

	// The live index already reports this citizen queued -- no re-draw,
	// every single call, since there is no snapshot to go stale or be
	// omitted.
	for i := 0; i < 3; i++ {
		got := s.applyMonthly(1, month, ColdPassParams{MortalityMultiplier: 1}, nil, dq, shard, "attack")
		if got.selected != 0 {
			t.Fatalf("call %d: selected=%d, want 0 (the citizen is already queued, and the live index never fails open)", i, got.selected)
		}
	}
	if dq.Len("attack") != 1 {
		t.Fatalf("queue length = %d, want 1 — Enqueue must still be the terminal guard", dq.Len("attack"))
	}
	if m, ok := dq.IsQueued(id, "attack"); !ok || m != month-1 {
		t.Fatalf("selection month = %d ok=%v, want %d/true — the original selection must not be overwritten", m, ok, month-1)
	}
}

// BenchmarkBUG663QueuedSnapshot measures the per-call ALLOCATION cost of
// the snapshot at realistic backlog sizes — the other half of the
// performance finding (the tick path calls this 30 times a month, and the
// garbage it produces is proportional to the pending queue, which
// §3.6 of the 100M proving plan already notes "budget-limited smoothing
// can make permanent").
func BenchmarkBUG663QueuedSnapshot(b *testing.B) {
	for _, q := range []int{0, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("pending=%d", q), func(b *testing.B) {
			dq := NewDeathQueue()
			for i := 0; i < q; i++ {
				if err := dq.Enqueue(uint64(i+1), 1, "bench"); err != nil {
					b.Fatalf("Enqueue: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = dq.QueuedSnapshot("bench")
			}
		})
	}
}

// BenchmarkBUG663IsQueuedInShard is the REWORK's own benchmark (adapting
// this file's benchmark to the new index, as the rework brief allows):
// measures the per-call cost of [DeathQueue.IsQueuedInShard] at the same
// backlog sizes BenchmarkBUG663QueuedSnapshot uses, called at the SAME
// shard the enqueued ids actually live in (so the lookup is a real,
// populated-map hit/miss, not an always-empty shard). Unlike
// QueuedSnapshot, this must show ZERO allocations and cost independent of
// the pending queue size -- that is the whole point of the rework.
func BenchmarkBUG663IsQueuedInShard(b *testing.B) {
	for _, q := range []int{0, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("pending=%d", q), func(b *testing.B) {
			dq := NewDeathQueue()
			for i := 0; i < q; i++ {
				if err := dq.Enqueue(uint64(i+1), 1, "bench"); err != nil {
					b.Fatalf("Enqueue: %v", err)
				}
			}
			probeShard := det.ShardForEntity(1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = dq.IsQueuedInShard(probeShard, 1, "bench")
			}
		})
	}
}
