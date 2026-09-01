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
// other. [DeathQueue.RealiseDrained] is the new entry point that takes
// min(budget, drain, queued) and tags each release with emergencyFlag;
// [DeathQueue.Realise] and [EmergencyRealise] (inc1/inc2) are UNCHANGED --
// every inc1/inc1.5/inc2 test still exercises them directly, byte-for-byte,
// proving inc3 is purely additive. A DeathQueue that never has
// SetDrainCapacity called on it (the default, and every world with no
// FEAT-088 consumer wired) treats drain as unlimited, so RealiseDrained's
// release set/order is identical to EmergencyRealise's for the same
// (budget, emergency, month) inputs -- see RealiseDrained's own doc.

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
	// monthIndex. RealiseDrained clamps this to be non-negative before
	// using it (a negative return is treated as zero, never as "no bound"
	// -- see RealiseDrained's doc).
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

// RealiseDrained is FEAT-087 inc3's entry point (AC-9/AC-10/AC-11):
// combines the ordinary-or-emergency budget selection (mirroring
// EmergencyRealise's own budget logic exactly, so EmergencyRealise itself
// stays untouched byte-for-byte -- see the file doc) with ASM-580's second,
// independent knob (the injected drain capacity, [DeathQueue.SetDrainCapacity])
// and AC-9/AC-10's ordered, flagged handoff stream. Realisation this month
// is min(ordinary-or-emergency budget, injected drain, queued) (ASM-580);
// a nil injected drain (the default) makes this call release the identical
// set/order of citizens EmergencyRealise would for the same
// (cfg, emergency, month) inputs -- wiring RealiseDrained into the live
// cold-pass realisation step is therefore a behavioural no-op for every
// world that has no FEAT-088 consumer wired yet (registry.go's
// AdvanceDayTick does exactly this).
func (q *DeathQueue) RealiseDrained(cfg MortalityConfig, emergency bool, month int64, correlationID string) []RealisedDeath {
	if err := q.checkNotCopied(correlationID, "RealiseDrained"); err != nil {
		return nil
	}

	budget := cfg.MonthlyDeathBudget()
	if emergency {
		budget = cfg.MonthlyEmergencyBudget()
		if budget <= 0 {
			budget = q.Len(correlationID)
		}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	effective := budget
	if q.drain != nil {
		if d := q.drain.MonthlyDrainCapacity(month); d < effective {
			if d < 0 {
				d = 0
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
