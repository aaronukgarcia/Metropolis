package citizens

import (
	"sort"
	"sync"
	"sync/atomic"

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
// # Deferred to later increments (NOT built here)
//
//   - inc2 (AC-6/AC-7/AC-8): a declared weather emergency (via
//     engine.season) suspending the smoothing budget for a genuine
//     non-smoothed major death event. Realise's signature below already
//     shapes for this: an inc2 caller adds an `emergency bool` (or
//     equivalent) argument that, when true, bypasses the budget clamp
//     entirely -- a caller-side wrapper, not a DeathQueue rewrite.
//   - inc3 (AC-9/AC-10/AC-11): the queryable, flagged
//     (citizenId, deathMonth, emergencyFlag) handoff surface FEAT-088
//     drains, and replacing Realise's fixed test-only drain assumption
//     with FEAT-088's injected funeral-throughput capacity. ASM-580's rule
//     (the smoothing budget and the funeral drain rate are two
//     INDEPENDENT knobs, min(budget, drain, queued) -- never one derived
//     from the other) is why Realise is written to take budget alone in
//     inc1: inc3 adds a second capacity argument and takes the min,
//     without needing to touch Enqueue, the FIFO ordering, or the
//     conservation guarantee at all.

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

	// self is the SEC-020 copyguard (atomic.Pointer, mirroring
	// CitizensAPI.self / engine.world's World.self): stored exactly once,
	// at the end of NewDeathQueue, before the value is returned to any
	// caller. mu is a sync.Mutex VALUE while pending/queued/realisedIDs/
	// realisedAt are reference types a copy ALIASES, so an unrejected copy
	// would be a second, independent lock racing the original over the
	// same referents.
	self atomic.Pointer[DeathQueue]
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
	defer q.mu.Unlock()

	if _, ok := q.queued[citizenID]; ok {
		return errs.New(ErrCitizenAlreadyQueued, correlationID, map[string]any{"citizenId": citizenID})
	}
	if _, ok := q.realisedAt[citizenID]; ok {
		return errs.New(ErrCitizenAlreadyQueued, correlationID, map[string]any{"citizenId": citizenID, "rule": "already realised"})
	}

	q.queued[citizenID] = selectionMonth
	q.pending = append(q.pending, deathQueueEntry{citizenID: citizenID, selectionMonth: selectionMonth})
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
	return nil
}
