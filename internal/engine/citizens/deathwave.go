package citizens

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// deathwave.go implements FEAT-087 (mkey feat.deathwave) inc1: the
// death-queue smoothing CORE. It CONSUMES mortality.go's existing
// Gompertz-Makeham MortalityHazard/MortalityDeath (never re-derives the
// actuarial curve) and sits between a hazard-selected death and the moment
// a citizen is actually removed from the living population.
//
// # The problem this solves (US-1, AC-1)
//
// A same-birthMonth cohort riding the steep Gompertz slope can have
// MortalityDeath select a large fraction of the cohort in a single month.
// Realising every one of those deaths inline, the instant the hazard draw
// selects them, produces a one-month population cliff. DeathQueue instead
// separates SELECTION (Enqueue, driven by the existing hazard draw) from
// REALISATION (Realise, bounded by a monthly budget): a hazard-selected
// death is queued, not removed, and at most the budget's worth of the
// OLDEST queued entries are realised per (non-emergency) month. The
// remainder is retained, never lost (AC-2 — smoothing is delay, never a
// cull or a leak, §14/§19/GR#12).
//
// # inc1 scope (this file)
//
// AC-1 (enqueue/realise separation + bounded release), AC-2 (conservation:
// total realised == total selected, queue drains to empty), AC-3 (a queued
// citizen is the caller's responsibility to keep alive/ageing/aggregated
// until IsQueued reports it realised -- see IsQueued's doc), AC-4/AC-15
// (deterministic FIFO order, worker-count invariant), AC-5 (the budget is
// data.mortality.json-sourced, see mortalityconfig.go), AC-12/AC-13 (GR#7
// registry errors for a malformed budget file and for a not-queued/
// double-realise request), AC-14/AC-16/AC-17 (no shared RNG, no wall
// clock, race-clean under concurrent Enqueue/Realise).
//
// # inc2 (AC-6/AC-7/AC-8, built in weatheremergency.go, NOT this file)
//
// A declared weather emergency (consumed through the registered
// feat.deathwave -> engine.season edge) suspends the smoothing budget for
// a genuine non-smoothed major death event. Exactly as this file's doc
// anticipated, inc2 is a CALLER-SIDE wrapper ([EmergencyRealise] in
// weatheremergency.go), not a DeathQueue rewrite: it calls this file's own
// [DeathQueue.Realise] with a different (emergency) budget when a weather
// emergency is declared, and the ordinary budget otherwise. Realise itself
// is unchanged by inc2 -- it has no notion of "emergency", keeping AC-8's
// boundary mechanical (the hazard SELECTION path, Enqueue, is untouched
// either way).
//
// # inc3 (AC-9/AC-10/AC-11, built in THIS file below)
//
// The queryable, flagged (citizenId, deathMonth, emergencyFlag) handoff
// surface FEAT-088 drains ([RealisedDeath], [DeathQueue.RealisedDeaths]),
// and the injected funeral-throughput capacity FEAT-088 provides
// ([DrainCapacity], [DeathQueue.SetDrainCapacity]). Per ASM-580, the
// smoothing budget (AC-5, data-filed) and the drain capacity (AC-11,
// injected) are TWO INDEPENDENT knobs -- inc3 never derives one from the
// other. [DeathQueue.RealiseDrained] is the new entry point that tags each
// release with emergencyFlag; [DeathQueue.Realise] and [EmergencyRealise]
// (inc1/inc2) are UNCHANGED -- every inc1/inc1.5/inc2 test still exercises
// them directly, byte-for-byte, proving inc3 is purely additive. A
// DeathQueue that never has SetDrainCapacity called on it (the default,
// and every world with no FEAT-088 consumer wired) treats drain as
// unlimited, so RealiseDrained's release set/order is identical to
// EmergencyRealise's for the same (budget, emergency, month) inputs -- see
// RealiseDrained's own doc.
//
// BUG-484 (Aaron ruling, 2026-09-03): the min(budget, drain, queued) rule
// applies ONLY on the non-emergency release -- RealiseDrained realises
// min(ordinary budget, drain, queued) when emergency is false, but
// min(emergency budget, queued) alone (the drain is NOT consulted at all)
// when emergency is true. A declared weather emergency (AC-6) must
// produce a genuine, non-smoothed major death event regardless of the
// funeral fleet's throughput -- a small hearse fleet must never flatten
// the AC-6 major death event into a trickle. See RealiseDrained's own doc
// for the exact mechanics.

// deathQueueEntry is one pending, hazard-selected death awaiting bounded
// monthly realisation.
type deathQueueEntry struct {
	citizenID      uint64
	selectionMonth int64
}

// DeathQueue is FEAT-087 inc1's smoothing-queue core. All state is guarded
// by mu so a per-shard cold pass can Enqueue concurrently with a
// realisation pass reading/draining the queue (AC-17).
//
// Realisation order (AC-4/AC-15) is FIFO by (selectionMonth, citizenID) —
// a pure function of queue CONTENTS, recomputed by sorting at Realise
// time, never of Enqueue call order. This is what makes the sequence
// worker-count invariant: parallel cold-pass shards racing to Enqueue in
// any interleaving, at any worker count, still produce the identical
// realised sequence, because insertion order never influences the result.
type DeathQueue struct {
	mu sync.Mutex

	pending []deathQueueEntry
	queued  map[uint64]int64 // citizenID -> selectionMonth, while pending

	realisedIDs []uint64         // realisation order (AC-4)
	realisedAt  map[uint64]int64 // citizenID -> the month it was realised

	// drain is FEAT-088's OPTIONAL injected funeral-throughput capacity
	// (inc3, AC-11, ASM-580's second knob). nil (the NewDeathQueue default,
	// and every DeathQueue SetDrainCapacity is never called on) means
	// UNLIMITED -- RealiseDrained is then bounded by budget and queue
	// length alone, byte-identical to Realise/EmergencyRealise's existing
	// behaviour. Read/written only under mu.
	drain DrainCapacity

	// handoff is FEAT-088's ordered, flagged handoff stream (inc3, AC-9/
	// AC-10): one append-only RealisedDeath record per citizen released
	// through RealiseDrained, in release order (which IS realisedIDs'
	// order -- realiseLocked already sorts FIFO by (selectionMonth,
	// citizenID), AC-4). Realise/EmergencyRealise do NOT append here --
	// only RealiseDrained does, keeping inc1/inc1.5/inc2's existing
	// behaviour and tests completely untouched by inc3's addition.
	handoff []RealisedDeath

	// self is the SEC-020 copyguard (atomic.Pointer, mirroring
	// CitizensAPI.self / engine.world's World.self): stored exactly once,
	// at the end of NewDeathQueue, before the value is returned to any
	// caller. mu is a sync.Mutex VALUE while pending/queued/realisedIDs/
	// realisedAt are reference types a copy ALIASES, so an unrejected copy
	// would be a second, independent lock racing the original over the
	// same referents.
	self atomic.Pointer[DeathQueue]

	// negativeDrainWarned (BUG-483 F2, GR#17): true once RealiseDrained has
	// logged [ErrNegativeDrainCapacity] for this queue at least once. A
	// negative return from an injected [DrainCapacity] is a buggy FEAT-088
	// consumer, not a normal condition -- every occurrence would otherwise
	// be identical noise (a stuck-at-zero drain calls MonthlyDrainCapacity
	// every month), so this fires the registry warning exactly ONCE per
	// DeathQueue rather than flooding the log for as long as the consumer
	// stays broken. Read/written only under mu (alongside drain/handoff).
	negativeDrainWarned bool

	// shardIndex/shardMu (BUG-663 REWORK -- the destructive round's REJECT
	// on QueuedSnapshot's O(pendingQueue) per-day-tick allocation, remedy
	// (b)): a per-shard mirror of `queued`, sharded by
	// [det.ShardForEntity](citizenID) -- the SAME partition function
	// ColdShard's own shard index is drawn from (coldpass.go's numColdShards
	// shards), so a cold-pass shard's goroutine and this index's
	// corresponding slot always agree on which shard a citizen belongs to.
	//
	// This is what makes [IsQueuedInShard] O(1) with NO per-day-tick
	// snapshot and NO cross-shard lock contention: each of runShardsParallel's
	// goroutines is pinned to exactly one shard index for the whole call, so
	// it only ever locks shardMu[itsOwnShard] -- a different goroutine
	// processing a different shard locks a DIFFERENT shardMu entry, so two
	// concurrent shards never contend with each other here, and Enqueue
	// (called only for the small number of citizens actually hazard-selected
	// this tick) contends only with the OTHER accesses to that SAME
	// citizen's shard, never with the whole queue's global mu.
	//
	// shardIndex is the authority for [IsQueuedInShard] queries only --
	// [IsQueued] is UNCHANGED, still reading `queued` under q.mu, since its
	// callers (departure reconciliation, tests) are not the per-day-tick hot
	// path this rework targets. `queued`/`pending`/`realisedAt` (the
	// pre-existing fields, kept
	// unchanged in shape and locking so every existing accessor/test that
	// reaches into them under mu keeps working byte-for-byte) remain the
	// single source of truth for FIFO release order and conservation
	// counting, which Realise/RealiseDrained/RealiseByID need across ALL
	// shards at once and which run at most once per completed month (never
	// per day-tick) -- an O(pending) global sort there is the right cost,
	// paid 1/30th as often as the old per-day-tick snapshot was.
	// Enqueue/realiseLocked/RealiseByID keep both representations in
	// lockstep: shardMu is a LEAF LOCK, always acquired WITH or AFTER q.mu
	// whenever both are needed for one mutation -- Enqueue takes q.mu,
	// commits queued/pending, and only THEN (still holding q.mu) takes
	// shardMu to mirror the insert before releasing q.mu; realiseLocked and
	// RealiseByID do the identical thing on removal, both already called
	// with q.mu held. [IsQueuedInShard] is the one caller that takes shardMu
	// ALONE, with q.mu never touched at all -- that asymmetry (shardMu
	// sometimes alone, q.mu never alone when shardMu is also needed) is what
	// keeps a read never blocking on the once-a-month global mutation while
	// still making every WRITE atomic across both maps.
	//
	// CORRECTED (BUG-663 round 2 F1, blocking REJECT): an earlier version of
	// this comment claimed "q.mu is NEVER held while taking a shardMu",
	// describing Enqueue releasing q.mu BEFORE calling indexInsert. That was
	// false and exploitable: TestR2SplitLockWindowStress proved a concurrent
	// RealiseByID for the SAME citizenID could land inside that exact
	// released-mu-not-yet-shardMu window (delete from `queued`, indexRemove
	// a no-op since the insert had not happened yet), after which the
	// losing Enqueue's now-late indexInsert wrote a REALISED id into
	// shardIndex -- a permanent false-positive IsQueuedInShard for a dead
	// citizen. Latent under today's single caller (CitizensAPI.mu
	// serialises every command), but a live hazard once FEAT-088
	// deathservices or any other concurrent caller reaches this queue
	// directly. Fixed by moving indexInsert inside Enqueue's still-held
	// q.mu, matching realiseLocked/RealiseByID's existing nesting exactly.
	shardIndex [numColdShards]map[uint64]struct{}
	shardMu    [numColdShards]sync.Mutex
}

// indexInsert mirrors citizenID's queued membership into its shard's index
// (BUG-663 rework). Lazily allocates the shard's map on first use so a
// DeathQueue that never enqueues anything in a given shard costs nothing
// beyond one nil map slot for it.
//
// checkNotCopied here is REDUNDANT with the caller's own check (Enqueue
// already checks before touching q at all) -- kept anyway, matching this
// file's established double-check convention at this call shape (see
// budgetFor's own doc comment for the identical astgate-syntactic-blind-spot
// reasoning): astgate's SEC-049 field-access enumeration flags every
// receiver method on a candidate type (*DeathQueue) that never calls
// checkNotCopied itself, with no call-graph visibility into an
// already-guarded caller, so an unexported helper reached only from a
// guarded entry point still needs its own call to stay off the live-tree
// violation list rather than requiring a baseline-ratchet exception entry.
func (q *DeathQueue) indexInsert(citizenID uint64, correlationID string) {
	_ = q.checkNotCopied(correlationID, "indexInsert")
	shard := det.ShardForEntity(citizenID)
	q.shardMu[shard].Lock()
	if q.shardIndex[shard] == nil {
		q.shardIndex[shard] = make(map[uint64]struct{})
	}
	q.shardIndex[shard][citizenID] = struct{}{}
	q.shardMu[shard].Unlock()
}

// indexRemove clears citizenID from its shard's index (BUG-663 rework) --
// called at realisation (Realise/RealiseDrained/RealiseByID), mirroring the
// canonical `queued` map's own delete so shardIndex never reports a realised
// citizen as still pending. See indexInsert's doc comment for why the
// (redundant) checkNotCopied call is kept here too.
func (q *DeathQueue) indexRemove(citizenID uint64, correlationID string) {
	_ = q.checkNotCopied(correlationID, "indexRemove")
	shard := det.ShardForEntity(citizenID)
	q.shardMu[shard].Lock()
	if q.shardIndex[shard] != nil {
		delete(q.shardIndex[shard], citizenID)
	}
	q.shardMu[shard].Unlock()
}

// IsQueuedInShard is BUG-663's rework of the day-tick membership check
// (destructive round remedy (b)): reports whether citizenID has a pending,
// un-realised death, in O(1) under ONLY citizenID's own shard's lock -- no
// queue-wide snapshot, no global mu, no per-day-tick allocation.
//
// shard MUST be [det.ShardForEntity](citizenID) -- callers (coldpass.go's
// applyMonthly) already know their own shard index, since it is the same
// partition ColdShard is stored under, so this never re-derives it. Passing
// a mismatched shard is a caller bug (structurally unreachable through the
// one real call site, which always passes its own shard index for ids drawn
// from that exact shard's own columns) and simply looks up the wrong
// (possibly always-empty) shard rather than panicking -- consistent with
// every other read accessor in this file degrading rather than crashing.
func (q *DeathQueue) IsQueuedInShard(shard int, citizenID uint64, correlationID string) bool {
	_ = q.checkNotCopied(correlationID, "IsQueuedInShard")
	if shard < 0 || shard >= numColdShards {
		return false
	}
	q.shardMu[shard].Lock()
	defer q.shardMu[shard].Unlock()
	if q.shardIndex[shard] == nil {
		return false
	}
	_, ok := q.shardIndex[shard][citizenID]
	return ok
}

// NewDeathQueue constructs an empty death queue.
func NewDeathQueue() *DeathQueue {
	q := &DeathQueue{
		queued:     make(map[uint64]int64),
		realisedAt: make(map[uint64]int64),
	}
	q.self.Store(q)
	return q
}

// checkNotCopied rejects a method call on a struct copy of the *DeathQueue
// NewDeathQueue returned (SEC-020 family). Lock-free: a single
// atomic.Pointer.Load, safe to run before mu is ever touched.
func (q *DeathQueue) checkNotCopied(correlationID, method string) error {
	if q.self.Load() != q {
		return errs.New(ErrDeathQueueCopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// Enqueue records a hazard-selected death awaiting realisation (AC-1/
// AC-3). The caller is expected to have ALREADY drawn MortalityDeath (or
// equivalent) this month for citizenID before calling Enqueue — DeathQueue
// itself makes no hazard draw.
//
// A citizenID already pending, or already realised, is rejected
// (ErrCitizenAlreadyQueued) rather than silently accepted a second time: a
// queue entry is the single, terminal selection event (AC-3(b)) — the
// caller's per-citizen mortality draw must consult IsQueued first and skip
// a citizen that is already in (or has already left through) the queue,
// exactly as it must never re-draw a citizen already dead.
func (q *DeathQueue) Enqueue(citizenID uint64, selectionMonth int64, correlationID string) error {
	if err := q.checkNotCopied(correlationID, "Enqueue"); err != nil {
		return err
	}
	q.mu.Lock()
	if _, ok := q.queued[citizenID]; ok {
		q.mu.Unlock()
		return errs.New(ErrCitizenAlreadyQueued, correlationID, map[string]any{"citizenId": citizenID})
	}
	if _, ok := q.realisedAt[citizenID]; ok {
		q.mu.Unlock()
		return errs.New(ErrCitizenAlreadyQueued, correlationID, map[string]any{"citizenId": citizenID, "rule": "already realised"})
	}

	q.queued[citizenID] = selectionMonth
	q.pending = append(q.pending, deathQueueEntry{citizenID: citizenID, selectionMonth: selectionMonth})

	// BUG-663 round 2 F1 (BLOCKING REJECT, attacker's
	// TestR2SplitLockWindowStress): the index mirror MUST land BEFORE q.mu
	// is released, not after. Releasing q.mu first opened a window where a
	// concurrent RealiseByID for this SAME citizenID could run to
	// completion (delete from `queued`, indexRemove a no-op since the
	// insert had not landed yet) and then this Enqueue's now-late
	// indexInsert would write a REALISED id into shardIndex --
	// IsQueuedInShard true forever for a dead citizen. Nesting shardMu
	// INSIDE the still-held q.mu (uniform mu-then-shardMu order, exactly
	// like realiseLocked/RealiseByID below) makes the whole
	// queued/pending/shardIndex update one atomic unit with respect to any
	// other Enqueue/Realise*/RealiseByID call, which all take q.mu first
	// too -- see shardIndex's own doc comment for the corrected invariant.
	q.indexInsert(citizenID, correlationID)
	q.mu.Unlock()
	return nil
}

// IsQueued reports whether citizenID currently has a pending (not yet
// realised) death, and the month it was selected. AC-3(a): the CALLER uses
// this to decide that a queued citizen still counts in the living
// population, still ages, and still contributes to population aggregates
// until IsQueued reports false because the death was realised — DeathQueue
// does not itself model "alive" (it has no citizen store), so it is the
// caller's obligation to consult this before excluding a citizen from any
// living-population view.
func (q *DeathQueue) IsQueued(citizenID uint64, correlationID string) (int64, bool) {
	_ = q.checkNotCopied(correlationID, "IsQueued")
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.queued[citizenID]
	return m, ok
}

// QueuedSnapshot returns a POINT-IN-TIME copy of the set of citizen ids
// currently pending (Enqueued, not yet Realised).
//
// HISTORY (BUG-663): this was briefly the cold pass's per-day-tick
// membership check, replacing the [IsQueued] global-mutex bottleneck
// (coldpass.go's applyMonthly used to call IsQueued once per citizen,
// taking q.mu N/30 times per tick and serialising the shard parallelism
// runShardsParallel exists to provide). An independent destructive round
// REJECTED that fix: this method allocates and fills a map of every pending
// id, so calling it once per day-tick (30x/month) costs O(pendingQueue) —
// measured at 127.9ms/day-tick at 1M pending, 70x WORSE than the
// global-mutex approach it replaced (BenchmarkBUG663QueuedSnapshot pins the
// cost). registry.go's AdvanceDayTick no longer calls this method at all —
// it queries [DeathQueue.IsQueuedInShard] instead (O(1), no allocation, see
// that method's own doc and deathwave.go's shardIndex field comment for the
// per-shard-lock design that replaced this snapshot).
//
// QueuedSnapshot itself is UNCHANGED and kept for callers that genuinely
// want a whole-queue point-in-time copy (e.g. diagnostics, or a future
// consumer outside the per-day-tick hot path) — it is simply no longer on
// AdvanceDayTick's critical path. See [DeathQueue] and registry.go's
// AdvanceDayTick doc comment for the invariant (every citizen id belongs to
// exactly one shard, a shard is visited by applyMonthly at most once per
// day-tick, Realise/RealiseDrained runs sequentially after the parallel
// shard pass) that made a snapshot here observationally identical to a live
// per-citizen check in the first place.
func (q *DeathQueue) QueuedSnapshot(correlationID string) map[uint64]struct{} {
	_ = q.checkNotCopied(correlationID, "QueuedSnapshot")
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[uint64]struct{}, len(q.queued))
	for id := range q.queued {
		out[id] = struct{}{}
	}
	return out
}

// Len reports the current pending queue length.
func (q *DeathQueue) Len(correlationID string) int {
	_ = q.checkNotCopied(correlationID, "Len")
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// TotalRealised reports the lifetime count of realised deaths (AC-2's
// conservation check: totalRealised should equal totalSelected once the
// queue has fully drained).
func (q *DeathQueue) TotalRealised(correlationID string) int {
	_ = q.checkNotCopied(correlationID, "TotalRealised")
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.realisedIDs)
}

// RealisedSequence returns a copy of the realisation order so far — the
// deterministic total order FIFO by (selectionMonth, citizenID) (AC-4).
// Two runs with the same seed and command log produce a byte-identical
// (via reflect.DeepEqual/bytes-of-encoding) sequence; AC-15's
// worker-count-invariance test compares this across worker counts.
func (q *DeathQueue) RealisedSequence(correlationID string) []uint64 {
	_ = q.checkNotCopied(correlationID, "RealisedSequence")
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]uint64, len(q.realisedIDs))
	copy(out, q.realisedIDs)
	return out
}

// Realise releases up to `budget` (AC-5's data-file value; never negative,
// callers pass MortalityConfig.MonthlyDeathBudget()) of the OLDEST pending
// entries, FIFO by (selectionMonth, citizenID) (AC-4), and returns their
// citizen ids in that release order. The remainder — if the queue holds
// more than budget — stays pending, unmodified (AC-2: smoothing defers,
// never drops).
//
// budget <= 0 releases nothing (a non-positive budget must never panic or
// silently release everything — MortalityConfig.validate already rejects
// a non-positive configured value at load time, AC-12, so a caller only
// reaches budget<=0 via a programming error, which this defensively
// no-ops rather than corrupting the queue).
//
// See the file-level doc comment for how inc2 (emergency override) and
// inc3 (injected drain capacity, ASM-580's min(budget, drain, queued))
// extend this call without changing its shape.
func (q *DeathQueue) Realise(budget int, month int64, correlationID string) []uint64 {
	_ = q.checkNotCopied(correlationID, "Realise")
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.realiseLocked(budget, month, correlationID)
}

func (q *DeathQueue) realiseLocked(budget int, month int64, correlationID string) []uint64 {
	_ = q.checkNotCopied(correlationID, "realiseLocked")
	if budget <= 0 || len(q.pending) == 0 {
		return nil
	}

	sort.Slice(q.pending, func(i, j int) bool {
		a, b := q.pending[i], q.pending[j]
		if a.selectionMonth != b.selectionMonth {
			return a.selectionMonth < b.selectionMonth
		}
		return a.citizenID < b.citizenID
	})

	n := budget
	if n > len(q.pending) {
		n = len(q.pending)
	}

	released := q.pending[:n]
	out := make([]uint64, 0, n)
	for _, e := range released {
		delete(q.queued, e.citizenID)
		q.realisedAt[e.citizenID] = month
		q.realisedIDs = append(q.realisedIDs, e.citizenID)
		out = append(out, e.citizenID)
	}

	remaining := make([]deathQueueEntry, len(q.pending)-n)
	copy(remaining, q.pending[n:])
	q.pending = remaining

	// BUG-663 rework: this loop runs at most once per completed month (never
	// per day-tick) under q.mu, which is already held by the caller
	// (Realise/RealiseDrained) -- see shardIndex's doc comment for why
	// nesting a shardMu acquisition inside an already-held q.mu here is safe
	// (the reverse order, shardMu-then-mu, never happens anywhere in this
	// file). Deliberately released AFTER unlocking q.mu below is NOT an
	// option since realiseLocked itself does not own q.mu's lock/unlock (its
	// callers do) -- so the mirror happens here, still under mu, which is
	// fine: shardMu is a leaf lock, never itself the trigger for taking mu.
	for _, id := range out {
		q.indexRemove(id, correlationID)
	}

	return out
}

// RealiseByID force-realises exactly one specific queued citizen this
// month, bypassing the budget entirely — used where a caller (or a test
// exercising AC-13 directly) needs to realise or verify one targeted
// entry rather than draining the FIFO head. Returns:
//
//   - ErrCitizenNotQueued if citizenID has no pending entry and has not
//     already been realised — never fabricates a phantom death (AC-13).
//   - ErrDoubleRealisation if citizenID has already been realised — never
//     creates a second, duplicate death record for the same citizen
//     (AC-13).
func (q *DeathQueue) RealiseByID(citizenID uint64, month int64, correlationID string) error {
	if err := q.checkNotCopied(correlationID, "RealiseByID"); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.realisedAt[citizenID]; ok {
		return errs.New(ErrDoubleRealisation, correlationID, map[string]any{"citizenId": citizenID})
	}
	if _, ok := q.queued[citizenID]; !ok {
		return errs.New(ErrCitizenNotQueued, correlationID, map[string]any{"citizenId": citizenID})
	}

	idx := -1
	for i, e := range q.pending {
		if e.citizenID == citizenID {
			idx = i
			break
		}
	}
	// idx must be found: q.queued[citizenID] existed above, and
	// pending/queued are kept in lockstep by Enqueue/realiseLocked.
	q.pending = append(q.pending[:idx], q.pending[idx+1:]...)
	delete(q.queued, citizenID)
	q.realisedAt[citizenID] = month
	q.realisedIDs = append(q.realisedIDs, citizenID)
	// BUG-663 rework: mirror the removal into the per-shard index, same
	// nested-lock discipline as realiseLocked (still under the caller's
	// already-held q.mu; shardMu is always the leaf lock).
	q.indexRemove(citizenID, correlationID)
	return nil
}

// RealisedDeath is FEAT-087 inc3's ordered handoff record (AC-9): one
// realised death, carrying the minimum fields FEAT-088 (feat.deathservices)
// needs to drive funeral throughput (hearses, one body per trip) in FIFO
// order with no additional query back into engine.citizens.
type RealisedDeath struct {
	// CitizenID is the realised citizen's id.
	CitizenID uint64
	// DeathMonth is the simulation month realisation happened in (never the
	// SELECTION month -- AC-9's handoff is about the release, which is what
	// FEAT-088 schedules against).
	DeathMonth int64
	// EmergencyFlag is AC-10: true when this death was realised during a
	// declared weather emergency (weatheremergency.go's IsWeatherEmergency),
	// so FEAT-088 can switch to emergency dispensation (vans/trucks, 24x7)
	// without re-deriving the weather state itself.
	EmergencyFlag bool
}

// DrainCapacity is FEAT-088's injected funeral-throughput bound (AC-11,
// ASM-580's second, INDEPENDENT knob -- this package never derives it from
// its own smoothing budget, nor derives the budget from it). Implemented by
// the FEAT-088 consumer (e.g. hearse fleet size x trips/month) and wired
// in via [DeathQueue.SetDrainCapacity]/[CitizensAPI.SetDeathDrainCapacity].
type DrainCapacity interface {
	// MonthlyDrainCapacity returns the consumer's own throughput bound for
	// monthIndex. RealiseDrained treats a negative return as zero -- not as
	// "no bound" -- via budgetFor/realiseLocked's ordinary no-op on a
	// non-positive budget (no separate clamp exists); MET-G5405 is logged
	// once per DeathQueue the first time this occurs (see RealiseDrained's
	// doc).
	MonthlyDrainCapacity(monthIndex int64) int
}

// DrainCapacityFunc adapts a plain func to [DrainCapacity] (mirrors
// net/http's HandlerFunc precedent) for a consumer with no other state to
// carry.
type DrainCapacityFunc func(monthIndex int64) int

// MonthlyDrainCapacity implements [DrainCapacity].
func (f DrainCapacityFunc) MonthlyDrainCapacity(monthIndex int64) int {
	return f(monthIndex)
}

// SetDrainCapacity wires FEAT-088's injected drain capacity into q (AC-11).
// Passing nil restores the default UNLIMITED behaviour (RealiseDrained then
// bounded by budget and queue length alone) -- the state before any
// consumer is wired, and every world with no FEAT-088 consumer.
func (q *DeathQueue) SetDrainCapacity(d DrainCapacity, correlationID string) error {
	if err := q.checkNotCopied(correlationID, "SetDrainCapacity"); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.drain = d
	return nil
}

// RealisedDeaths returns a copy of the handoff stream so far (AC-9): every
// death RealiseDrained has released, in FIFO release order, each carrying
// (citizenId, deathMonth, emergencyFlag). Realise/EmergencyRealise releases
// (inc1/inc1.5/inc2, or a caller that never adopted RealiseDrained) do NOT
// appear here -- this is RealiseDrained's own stream, additive to (never a
// replacement for) [DeathQueue.RealisedSequence].
func (q *DeathQueue) RealisedDeaths(correlationID string) []RealisedDeath {
	_ = q.checkNotCopied(correlationID, "RealisedDeaths")
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]RealisedDeath, len(q.handoff))
	copy(out, q.handoff)
	return out
}

// DeathHandoffSince is BUG-483 F3's safety valve for the handoff stream's
// unbounded growth: [RealisedDeaths]/[DeathHandoff] always return the
// FULL cumulative stream (deliberate FEAT-087 semantics, pinned by
// TestAttackInc3_RealisedDeathsIsCumulativeNotDrainedOnRead -- AC-9 does
// not mandate drain-on-read, and this method does not change that
// default). At the 100M-citizen Option-B target that full-stream read
// grows for the life of the DeathQueue, which is fine for the
// FEAT-087-era tests but would be an unbounded per-poll payload for a
// FUTURE FEAT-088 consumer that simply wants "what's new since I last
// looked". DeathHandoffSince gives that consumer a page: everything
// STRICTLY AFTER index cursor in FIFO release order, i.e. handoff[cursor:]
// at the moment of the call.
//
// cursor is the count of records the caller has already consumed (0 on a
// consumer's very first call, then the RUNNING TOTAL of records it has
// received so far — e.g. cursor + len(previous result) — never an index
// into some other coordinate space). A negative cursor is treated as 0
// (never a panic or a negative-slice-bounds fault); a cursor at or past
// the current stream length returns an empty, non-nil slice (the consumer
// is simply caught up) rather than an error — being caught up is not a
// malformed request.
//
// This is a PURE READ over the same handoff slice DeathHandoff/
// RealisedDeaths already expose in full — DeathQueue itself never
// truncates handoff, never advances an internal cursor, and never treats
// any call to this method as an acknowledgement. Building an
// acknowledged-watermark truncation (if FEAT-088 ever needs one) is that
// future consumer's own job, coordinated with this package first per
// BUG-483's own text — not something this P3 follow-up forces today.
// FIFO order (AC-4) and the SEC-020 copyguard both apply exactly as they
// do to RealisedDeaths (read-only violations are logged, not fatal,
// mirroring every other read accessor in this file).
func (q *DeathQueue) DeathHandoffSince(cursor int, correlationID string) []RealisedDeath {
	_ = q.checkNotCopied(correlationID, "DeathHandoffSince")
	q.mu.Lock()
	defer q.mu.Unlock()
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(q.handoff) {
		return []RealisedDeath{}
	}
	out := make([]RealisedDeath, len(q.handoff)-cursor)
	copy(out, q.handoff[cursor:])
	return out
}

// budgetFor computes AC-6/AC-8's ordinary-or-emergency budget for one
// realisation call: cfg.MonthlyDeathBudget() unless emergency is true, in
// which case cfg.MonthlyEmergencyBudget() applies, with the 0 sentinel
// meaning "unbounded" (release the queue's entire current length, q.Len).
//
// This is the SINGLE SOURCE OF TRUTH for that rule (GR#3, BUG-483 F1) —
// [EmergencyRealise] (weatheremergency.go, inc2) and [DeathQueue.RealiseDrained]
// (this file, inc3) both delegate to it rather than each re-implementing
// the same three lines, so their documented byte-identical-budget claim is
// now STRUCTURAL (one function, one behaviour) rather than merely proven by
// attack_feat087_inc3_handoff_test.go's differential test — that test stays
// in place as a regression, now exercising two callers of one shared
// helper instead of two independent copies of the rule.
//
// SEC-020 copy-guard (astgate): budgetFor takes a candidate *DeathQueue
// parameter, so it checks first even though both of its current callers
// (EmergencyRealise, RealiseDrained) already check the SAME q before
// calling in -- astgate's syntactic, no-call-graph analysis cannot see
// that the check is already satisfied by the caller (the same
// already-guarded-reachable-path blind spot documented for
// EmergencyRealise's own belt-and-suspenders check). q.Len below performs
// its own internal check too; this one is deliberately redundant with it,
// matching the project's established double-check convention at this
// call shape.
func budgetFor(q *DeathQueue, cfg MortalityConfig, emergency bool, correlationID string) int {
	_ = q.checkNotCopied(correlationID, "budgetFor")
	budget := cfg.MonthlyDeathBudget()
	if emergency {
		budget = cfg.MonthlyEmergencyBudget()
		if budget <= 0 {
			budget = q.Len(correlationID)
		}
	}
	return budget
}

// RealiseDrained is FEAT-087 inc3's entry point (AC-9/AC-10/AC-11):
// combines the ordinary-or-emergency budget selection ([budgetFor] --
// shared, byte-for-byte, with [EmergencyRealise]; see budgetFor's doc and
// BUG-483 F1) with ASM-580's second, independent knob (the injected drain
// capacity, [DeathQueue.SetDrainCapacity]) and AC-9/AC-10's ordered,
// flagged handoff stream.
//
// BUG-484 (Aaron ruling, 2026-09-03): the injected drain capacity binds
// ONLY on the ordinary (non-emergency) path. When emergency is false,
// realisation this month is min(ordinary budget, injected drain, queued)
// exactly as ASM-580 words it -- a nil injected drain (the default) makes
// this call release the identical set/order of citizens [Realise] would
// for the same (budget, month) inputs. When emergency is true, the drain
// is IGNORED ENTIRELY -- a declared weather emergency (AC-6) must produce
// a genuine, non-smoothed major death event regardless of the funeral
// fleet's throughput; the emergency release is min(emergency budget,
// queued) alone, byte-identical to [EmergencyRealise] for the same (cfg,
// emergency, month) inputs no matter what drain capacity is wired. This is
// what makes RealiseDrained's "nil-drain path is differentially identical
// to EmergencyRealise" claim now ALSO hold with a real (non-nil, finite)
// drain wired, for the emergency case specifically -- wiring RealiseDrained
// into the live cold-pass realisation step is therefore a behavioural
// no-op, for BOTH the ordinary and emergency paths, for every world that
// has no FEAT-088 consumer wired yet (registry.go's AdvanceDayTick does
// exactly this).
//
// A negative [DrainCapacity.MonthlyDrainCapacity] return (a buggy FEAT-088
// consumer) is passed straight through as `effective` without an explicit
// clamp to 0 (BUG-483 F2), on the ordinary path only: [realiseLocked]
// already treats ANY budget/effective <= 0 as a no-op (see its doc), so a
// separate "if d < 0 { d = 0 }" step here would be dead code -- both a
// negative effective and a zero effective release nothing and leave the
// queue untouched, byte-for-byte. What was previously silent is the
// DIAGNOSABILITY of that state: the first time a negative drain is
// observed for this queue, a [ErrNegativeDrainCapacity] WARNING is logged
// (once per queue, not once per call, since a stuck-at-zero-or-negative
// consumer would otherwise call this every month) so a stuck death queue
// is visible in the log well before population anomalies would otherwise
// be the only symptom. The warning is logged only when the drain is
// actually consulted (the ordinary path) -- an emergency release never
// reads q.drain at all, so a negative drain sitting unconsulted during an
// emergency month is not itself a loggable event.
func (q *DeathQueue) RealiseDrained(cfg MortalityConfig, emergency bool, month int64, correlationID string) []RealisedDeath {
	if err := q.checkNotCopied(correlationID, "RealiseDrained"); err != nil {
		return nil
	}

	budget := budgetFor(q, cfg, emergency, correlationID)

	q.mu.Lock()
	defer q.mu.Unlock()

	effective := budget
	if !emergency && q.drain != nil {
		if d := q.drain.MonthlyDrainCapacity(month); d < effective {
			if d < 0 && !q.negativeDrainWarned {
				q.negativeDrainWarned = true
				_ = errs.New(ErrNegativeDrainCapacity, correlationID, map[string]any{"drain": d, "month": month})
			}
			effective = d
		}
	}

	ids := q.realiseLocked(effective, month, correlationID)
	out := make([]RealisedDeath, 0, len(ids))
	for _, id := range ids {
		rd := RealisedDeath{CitizenID: id, DeathMonth: month, EmergencyFlag: emergency}
		q.handoff = append(q.handoff, rd)
		out = append(out, rd)
	}
	return out
}

// DeathQueueEntrySnapshot is one FEAT-087 AC-20 pending (hazard-selected,
// not yet realised) death, captured for save/restore (BUG-483 F3). It is the
// wire-agnostic twin of the unexported deathQueueEntry — participant.go's
// wire type converts to/from this, never touching deathQueueEntry directly,
// so this file stays the single place that knows the private struct shape.
type DeathQueueEntrySnapshot struct {
	CitizenID      uint64
	SelectionMonth int64
}

// DeathQueueSnapshot is FEAT-087 AC-20's complete save/restore payload for
// one DeathQueue: every DATA field, captured together as one atomic unit
// under q.mu by [DeathQueue.Snapshot] and installed together by
// [DeathQueue.RestoreSnapshot].
//
// # Durable-vs-derived analysis (mirrors participant.go's citizens-wide one)
//
//	DURABLE -- carried here:
//	  - Pending: the queued-but-not-realised selections (AC-20's actual
//	    scope: BUG-483 F3, "a mid-queue save+exit+reload does not drop
//	    in-flight selections"). This is deathQueueEntry's DATA — citizenID
//	    and the month it was hazard-selected.
//	  - RealisedIDs: the FIFO realisation order so far (AC-4). Without this,
//	    a save/restore boundary would truncate [DeathQueue.RealisedSequence]
//	    at the restore point instead of the deterministic full history a
//	    never-saved run produces — the "identical realised sequence over 24
//	    more months" proof this AC's test evidence requires needs the PAST
//	    sequence carried across the boundary, not just the future one.
//	  - RealisedAt: per-citizen realisation month, the double-realisation
//	    guard Enqueue/RealiseByID consult (ErrCitizenAlreadyQueued/
//	    ErrDoubleRealisation) — dropping it on restore would let a citizen
//	    id that happens to be reused (or a stale re-departure command
//	    replayed against the restored engine) slip past a guard that held
//	    before the save.
//	  - Handoff: FEAT-088's ordered (citizenId, deathMonth, emergencyFlag)
//	    handoff stream ([DeathQueue.RealisedDeaths]/[DeathQueue.DeathHandoffSince]).
//	    Carried for the same reason RealisedIDs is: a future FEAT-088
//	    consumer's cursor is counted against the CUMULATIVE stream, and a
//	    save/restore silently truncating it out from under a live cursor
//	    would be exactly the kind of silent data loss AC-20 exists to close.
//	    (Pre-existing, inherited debt: this stream already has no
//	    acknowledged-truncation mechanism — see DeathHandoffSince's own doc
//	    — so it grows for the life of a save exactly as it already grows for
//	    the life of an un-saved run; AC-20 does not make that worse, it just
//	    stops it from being silently reset to empty at every restore.)
//
//	DERIVED / RUNTIME -- NOT carried, NOT serialized:
//	  - shardIndex/shardMu (BUG-663): a pure membership MIRROR of Pending,
//	    sharded by [det.ShardForEntity]. Serializing it would duplicate
//	    Pending's own data on the wire for no benefit, AND would be actively
//	    dangerous if the two ever drifted (a hand-edited save, or a future
//	    bug) — RestoreSnapshot instead REBUILDS it from the just-restored
//	    Pending under shardMu, mirroring [CitizensAPI]'s own
//	    ColdShard-index-rebuild precedent this AC's own brief calls out.
//	    Skipping this rebuild is the exact immortal-citizen hazard flagged
//	    on BUG-663's follow-up: IsQueuedInShard would report false for every
//	    restored pending citizen, the live cold pass
//	    (registry.go's applyMonthly) would then treat them as not-pending
//	    (already dead) forever, Enqueue would refuse to re-queue them
//	    (ErrCitizenAlreadyQueued: they are still in `queued`), and
//	    Realise/RealiseDrained never surfaces them again because the
//	    day-tick path never re-confirms membership through `queued` at
//	    all — the citizen is queued forever and realised never.
//	  - drain (DrainCapacity): an INJECTED FEAT-088 dependency (a hearse
//	    fleet size, a func closure), not gameplay data — exactly like
//	    citizens' own fertilityCfg, a load target is expected to have
//	    [DeathQueue.SetDrainCapacity] called again by whatever composed it,
//	    not to have a serialized interface value magically re-wired.
//	  - negativeDrainWarned: a one-time log-dedup latch (GR#17), not
//	    gameplay state — restoring false at worst re-logs one
//	    [ErrNegativeDrainCapacity] warning after a load, never a
//	    determinism or conservation hazard.
type DeathQueueSnapshot struct {
	Pending     []DeathQueueEntrySnapshot
	RealisedIDs []uint64
	RealisedAt  map[uint64]int64
	Handoff     []RealisedDeath
}

func (q *DeathQueue) Snapshot(correlationID string) DeathQueueSnapshot {
	_ = q.checkNotCopied(correlationID, "Snapshot")
	q.mu.Lock()
	defer q.mu.Unlock()

	out := DeathQueueSnapshot{
		Pending:     make([]DeathQueueEntrySnapshot, len(q.pending)),
		RealisedIDs: make([]uint64, len(q.realisedIDs)),
		RealisedAt:  make(map[uint64]int64, len(q.realisedAt)),
		Handoff:     make([]RealisedDeath, len(q.handoff)),
	}
	for i, e := range q.pending {
		out.Pending[i] = DeathQueueEntrySnapshot{CitizenID: e.citizenID, SelectionMonth: e.selectionMonth}
	}
	copy(out.RealisedIDs, q.realisedIDs)
	for k, v := range q.realisedAt {
		out.RealisedAt[k] = v
	}
	copy(out.Handoff, q.handoff)
	return out
}

// RestoreSnapshot replaces q's DATA fields with snap's contents (AC-20's
// load path) and REBUILDS shardIndex from the restored Pending set under
// shardMu — see [DeathQueueSnapshot]'s own doc for why shardIndex is
// derived rather than carried, and the immortal-citizen hazard of skipping
// this rebuild (BUG-663's follow-up finding, the mandatory part of this
// AC).
//
// RestoreSnapshot is meant to be called on a FRESH (or freshly reset)
// DeathQueue — exactly like every other *ForLoad collection reset in this
// package (participant.go's resetForLoad), it REPLACES q's data wholesale
// rather than merging with whatever was already queued; calling it on a
// queue that already has pending/realised entries clobbers them. Passing
// the zero-value DeathQueueSnapshot{} resets q to the same empty state
// NewDeathQueue constructs (GR#16: an old save with no "citizens.deathqueue"
// record decodes as this empty default, never a decode error).
func (q *DeathQueue) RestoreSnapshot(snap DeathQueueSnapshot, correlationID string) error {
	if err := q.checkNotCopied(correlationID, "RestoreSnapshot"); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	q.pending = make([]deathQueueEntry, len(snap.Pending))
	q.queued = make(map[uint64]int64, len(snap.Pending))
	for i, e := range snap.Pending {
		q.pending[i] = deathQueueEntry{citizenID: e.CitizenID, selectionMonth: e.SelectionMonth}
		q.queued[e.CitizenID] = e.SelectionMonth
	}

	q.realisedIDs = make([]uint64, len(snap.RealisedIDs))
	copy(q.realisedIDs, snap.RealisedIDs)
	q.realisedAt = make(map[uint64]int64, len(snap.RealisedAt))
	for k, v := range snap.RealisedAt {
		q.realisedAt[k] = v
	}
	q.handoff = make([]RealisedDeath, len(snap.Handoff))
	copy(q.handoff, snap.Handoff)
	q.negativeDrainWarned = false

	// BUG-663 r3 MANDATORY rebuild: shardIndex is a pure derived mirror of
	// `queued`/Pending (see DeathQueueSnapshot's doc) — it is never itself
	// on the wire, so every shard's index must be thrown away and re-derived
	// from the data that WAS just restored, exactly mirroring
	// ColdShard.rebuildIndexLocked's precedent. Skipping this step is the
	// silent-immortal-citizen defect this AC was written to close.
	// BUG (round REJECT, attacker's hammer test): the nil-out MUST take
	// shardMu[i] too, exactly like indexInsert/indexRemove do -- shardMu is
	// the lock IsQueuedInShard actually reads under (it never touches q.mu
	// at all, by this file's own leaf-lock design), so writing
	// q.shardIndex[i] = nil while holding only q.mu races a concurrent
	// IsQueuedInShard(i, ...) reading that same slot. RestoreSnapshot is
	// reached with q.mu already held here, but shardMu is the SEPARATE lock
	// this specific field is published under -- q.mu alone does not make
	// this write safe.
	for i := range q.shardIndex {
		q.shardMu[i].Lock()
		q.shardIndex[i] = nil
		q.shardMu[i].Unlock()
	}
	for _, e := range q.pending {
		// indexInsert takes its own shardMu[shard] internally -- consistent
		// with the nil-out above and with Enqueue's existing discipline.
		q.indexInsert(e.citizenID, correlationID)
	}
	return nil
}
