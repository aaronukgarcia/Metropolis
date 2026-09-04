package citizens

import (
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// DaysPerMonth is the fixed number of logistics day-ticks per calendar
// month (M0-ENG §3's two-layer clock: 30 day-ticks advance one month).
// A schema constant, never derived.
const DaysPerMonth = 30

// numColdShards is the fixed cold-store shard count — foundation/det's
// 256 id-hash shards (M0-ENG §1.2 point 1). Constant forever, never
// derived from core count or world size.
const numColdShards = det.NumShards

// ColdPassSchedule returns the fixed, deterministic set of cold shard
// indices to process on day-tick d (0..DaysPerMonth-1) of a calendar
// month (A2, AC-6). It is a pure, seed-independent function of the day
// index: shard i is assigned to day i*DaysPerMonth/numColdShards. Because
// 256 = 30×8 + 16, sixteen day-ticks schedule 9 shards and fourteen
// schedule 8; across the full 30-day-tick month every shard is scheduled
// exactly once (AC-6/AC-7). There is no random jitter — the amortisation
// changes *when within the month* a shard advances, never *how many times*.
func ColdPassSchedule(day int) []int {
	if day < 0 {
		day = 0
	}
	day %= DaysPerMonth
	out := make([]int, 0, numColdShards/DaysPerMonth+1)
	for shard := 0; shard < numColdShards; shard++ {
		if shard*DaysPerMonth/numColdShards == day {
			out = append(out, shard)
		}
	}
	return out
}

// ColdPassParams is the set of batch parameters the cold pass consumes,
// measured from the rotating stratified sample (AC-8) — never hardcoded
// constants. Every field is derived from the sample's actual composition
// in DeriveColdPassParams; the cold pass has no other source of rates.
type ColdPassParams struct {
	// MortalityMultiplier scales the base Gompertz-Makeham hazard to the
	// sample's measured health/access composition.
	MortalityMultiplier float64
	// EducationTransitionRate is the per-month probability a citizen
	// advances an education stage, measured from the sample.
	EducationTransitionRate float64
	// JobMatchRate is the per-month probability a citizen changes
	// employment, measured from the sample.
	JobMatchRate float64
	// HealthDrift is the signed per-month health-band drift, measured from
	// the sample.
	HealthDrift int32
	// SatisfactionDrift is the signed per-month satisfaction drift from
	// district-level service coverage, measured from the sample.
	SatisfactionDrift int32
	// LowConfidence is true when a stratum had too few members and the
	// estimate had to borrow from the nearest comparable stratum (AC-14's
	// documented fallback) — surfaced so a consumer can discount the
	// estimate rather than treat it as silently trustworthy.
	LowConfidence bool
}

// passTotals aggregates one monthly cold pass's effects. The zero value is
// the identity (MergeInOrder-style combine: field-wise addition).
type passTotals struct {
	// selected counts hazard-selected deaths ENQUEUED this call (FEAT-087
	// inc1.5) -- informational only. It is NOT a population change: a
	// selected death stays resident (still ages, still counts, ASM-581)
	// until DeathQueue.Realise actually removes it. AdvanceDayTick's
	// returned/conserved deaths count comes from Realise's own return, NOT
	// from this field.
	selected int
	updated  int
}

// add folds one shard's totals into the accumulator (in shard order).
func (t passTotals) add(o passTotals) passTotals {
	return passTotals{selected: t.selected + o.selected, updated: t.updated + o.updated}
}

// runShardsParallel runs fn over every shard in shards, spread across
// `workers` goroutines stealing shard indices from a shared queue, and
// returns one result per shard indexed by shard (so the caller sums in
// ascending shard order — never completion order). This is the same
// shard-stealing + ordered-merge discipline as foundation/det.RunPhase,
// applied to a scheduled SUBSET of shards (the amortised 1/30 slice)
// rather than the whole 256, which is why it does not call RunPhase
// directly. Because each shard's fn writes only its own results[shard]
// slot, the result is byte-identical at any worker count (AC-17).
func runShardsParallel(workers int, shards []int, fn func(shard int) passTotals) []passTotals {
	if workers < 1 {
		workers = 1
	}
	results := make([]passTotals, numColdShards)

	queue := make(chan int)
	go func() {
		for _, s := range shards {
			queue <- s
		}
		close(queue)
	}()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range queue {
				results[s] = fn(s)
			}
		}()
	}
	wg.Wait()
	return results
}

// coldDeath is one REALISED cold-pass mortality, snapshotted at removal
// time so the caller can route the death through the same
// household-dissolution path LifeEventDeath uses (BUG-369): the citizen's
// household and partner ids must be read BEFORE removeAt overwrites the row
// via swap-with-last. Built at REALISATION (registry.go's AdvanceDayTick),
// never at selection/enqueue time (FEAT-087 inc1.5) -- the household/
// partner snapshot must be current as of removal, not stale from the
// (possibly much earlier) month the citizen was selected in.
type coldDeath struct {
	citizenID   uint64
	householdID uint64
	partnerID   uint64
}

// applyMonthly advances every cold citizen in the shard exactly once for
// the given month (AC-7): aging (implicit — age is derived from
// birthMonth), per-person Gompertz-Makeham mortality (AC-11),
// education-stage transition, statistical job matching, health drift, and
// satisfaction drift (AC-8 — every rate comes from params, none
// hardcoded). It is a pure function of (seed, the shard's own columns,
// month, params): no shared RNG, no wall clock, no cross-shard reads
// (AC-15/AC-17). isHot, when non-nil, identifies citizens currently
// elevated to HOT/WARM.
//
// BUG-270: an elevated citizen still lives in the cold store (the single
// source of truth) and is advanced by THIS pass on their shard's one
// scheduled day-tick per month -- there is no separate "daily path", and the
// former blanket isHot skip meant an elevated citizen was never drawn for
// mortality and so could never die. The MORTALITY draw is now taken for
// every citizen regardless of tier: it is keyed (seed, id, month,
// "mortality"), so the outcome is identical whether the citizen is hot or
// cold, and it is taken exactly ONCE per citizen per month (their shard is
// scheduled on exactly one day-tick), never double-counted. The elevated
// death is finished on the sequential post-pass in AdvanceDayTick, which
// deletes the dead id from the hot cache and dissolves the household through
// the same removeHouseholdMemberLocked path a cold death uses (BUG-369).
//
// The other monthly effects (education, job matching, health/satisfaction
// drift) are still COLD-ONLY: writing them to an elevated citizen's cold
// record while leaving the hot mirror untouched would desync the two
// (currently both stay put while elevated, staying observationally
// consistent). Extending those drifts to the hot path with full write-through
// is a larger change outside BUG-270's births/deaths scope, so an elevated
// survivor skips them here -- exactly the prior behaviour for everything
// except the now-universal mortality draw.
//
// Returns the pass totals only (FEAT-087 inc1.5): a hazard-selected death
// is now ENQUEUED into dq, not removed here -- removal (and the BUG-369
// household-dissolution record) happens only at REALISATION, driven
// sequentially by the caller (registry.go's AdvanceDayTick) once every
// shard has had its one scheduled day-tick this month. See the death-queue
// paragraph below for why applyMonthly itself never calls removeAt for a
// mortality selection any more.
//
// # Death-queue wiring (FEAT-087 inc1.5 -- the cohort cliff killed LIVE)
//
// dq is the CitizensAPI-wide DeathQueue (deathwave.go), safe to Enqueue
// into concurrently from every shard's goroutine (it holds its own mutex,
// plus a per-shard index -- see deathwave.go's shardIndex doc comment).
// shard is THIS ColdShard's own index (det.ShardForEntity's partition,
// which is also DeathQueue's shardIndex partition -- the two always agree),
// passed straight through to [DeathQueue.IsQueuedInShard] for an O(1),
// no-allocation, no-cross-shard-contention membership check per citizen
// (BUG-663 REWORK: this replaces the old per-day-tick
// [DeathQueue.QueuedSnapshot] map copy a destructive round REJECTED for
// costing O(pendingQueue) on every one of a month's 30 day-ticks -- 127.9ms
// at 1M pending, 70x worse than the pre-fix per-citizen dq.IsQueued call it
// was meant to improve on. IsQueuedInShard instead locks ONLY
// dq.shardMu[shard] -- the same shard runShardsParallel already pins this
// goroutine to for the whole call -- so distinct shards processed
// concurrently never contend with each other, and there is no snapshot to
// allocate or go stale).
//
// A citizen already queued (selected in this or an earlier month, not yet
// realised) MUST NOT be drawn for mortality again (AC-3(b): the queue entry
// is the single, terminal selection event) -- it is simply skipped past the
// mortality draw and falls through to every other monthly effect below
// exactly as an ordinary living citizen would (ASM-581: queued is alive,
// ageing, and counted, never a frozen or separately-tracked state). A fresh
// hazard hit calls dq.Enqueue (which takes dq.mu plus one shardMu -- but
// only for the small number of citizens actually selected this month, not
// every citizen in the shard) and keeps going through the rest of this
// citizen's monthly effects the same way -- selection alone changes nothing
// observable about the citizen until the caller's Realise step removes them.
func (s *ColdShard) applyMonthly(seed uint64, month int64, params ColdPassParams, isHot func(uint64) bool, dq *DeathQueue, shard int, correlationID string) passTotals {
	var tot passTotals
	i := 0
	for i < s.count() {
		id := s.ids[i]
		hot := isHot != nil && isHot(id)
		birthMonth := s.epochMonth + int64(s.birthDelta[i])
		age := month - birthMonth

		// Mortality: per-person draw from hash(worldSeed, id, month,
		// "mortality") against the Gompertz-Makeham hazard scaled by the
		// sample-measured multiplier. A death is a SELECTION now, never an
		// immediate removal (FEAT-087 inc1.5) — the per-individual
		// probabilistic event §5.1 specifies feeds the smoothing queue,
		// which is what bounds it to a non-cliff monthly release (AC-1).
		if !dq.IsQueuedInShard(shard, id, correlationID) {
			stream := det.NewStream(seed, id, month, "mortality")
			hazard := MortalityHazard(age, HealthBand(s.healthBands[i]), s.access[i]) * params.MortalityMultiplier
			if hazard > 1 {
				hazard = 1
			}
			if stream.Float64() < hazard {
				// Enqueue, do not remove: the Enqueue error is intentionally
				// ignored here -- it can only fire for a citizenID already
				// queued or already realised, and the IsQueuedInShard check
				// just above already excludes both (an id belongs to
				// exactly one shard via det.ShardForEntity, and a shard is
				// visited at most once per day-tick, so nothing else can
				// race this same id into the queue between the check and
				// this call -- see applyMonthly's doc comment).
				_ = dq.Enqueue(id, month, correlationID)
				tot.selected++
			}
		}

		// An elevated survivor takes the mortality draw above (BUG-270) but
		// NOT the drift updates below: those write only the cold record, which
		// would desync the hot mirror while the citizen is elevated. Left for
		// a later, larger write-through change (see applyMonthly's doc).
		if hot {
			i++
			continue
		}

		// Education stage transition (params-derived rate). The September
		// intake gate is engine.season's integration point — out of scope
		// here, so this is a plain monthly probability, not a hardcoded
		// calendar gate.
		if params.EducationTransitionRate > 0 {
			es := det.NewStream(seed, id, month, "education")
			if es.Float64() < params.EducationTransitionRate {
				s.advanceStage(i)
			}
		}

		// Statistical job matching (params-derived rate): a deterministic
		// employment-state transition, calibrated to the sample.
		if params.JobMatchRate > 0 {
			es := det.NewStream(seed, id, month, "employment")
			if es.Float64() < params.JobMatchRate {
				s.matchJob(seed, month, i)
			}
		}

		// Health drift (params-derived signed drift), clamped to the band
		// range.
		if params.HealthDrift != 0 {
			s.healthBands[i] = clampBand(int(s.healthBands[i]) + int(params.HealthDrift))
		}

		// Satisfaction drift from district-level service coverage
		// (params-derived signed drift), applied to every component. The
		// arithmetic is done in int32 (promoting the int8 column) so a large
		// drift can never overflow the int8 range before clampSat.
		if params.SatisfactionDrift != 0 {
			s.satHousing[i] = clampSat(int32(s.satHousing[i]) + params.SatisfactionDrift)
			s.satServices[i] = clampSat(int32(s.satServices[i]) + params.SatisfactionDrift)
			s.satEnvironment[i] = clampSat(int32(s.satEnvironment[i]) + params.SatisfactionDrift)
			s.satLeisureFit[i] = clampSat(int32(s.satLeisureFit[i]) + params.SatisfactionDrift)
			s.satCommute[i] = clampSat(int32(s.satCommute[i]) + params.SatisfactionDrift)
		}

		s.monthlyUpdates[i]++
		tot.updated++
		i++
	}
	return tot
}

// advanceStage advances a citizen's education stage deterministically (the
// stage is a bucketed enum; advancement never exceeds the highest stage).
func (s *ColdShard) advanceStage(i int) {
	st := Stage(s.stages[i])
	if st < StageAdultEd {
		s.stages[i] = uint8(st + 1)
	}
	// Schooling accumulates a month whenever a stage is advanced.
	if s.schooling[i] < 32767 {
		s.schooling[i]++
	}
}

// matchJob applies a deterministic statistical job match: an unemployed or
// never-employed adult moves to employed in a sector drawn from the
// citizen's own hash stream; an employed citizen may switch sector. All
// transitions are per-citizen draws keyed (seed, id, month, purpose), no
// cross-shard state.
func (s *ColdShard) matchJob(seed uint64, month int64, i int) {
	id := s.ids[i]
	state, _ := unpackEmployment(s.employment[i])
	switch state {
	case EmploymentUnemployed, EmploymentNone:
		stream := det.NewStream(seed, id, month, "employment-sector")
		sector := Sector(stream.IntN(4) + 1) // SectorPrimary..SectorPublic
		s.employment[i] = packEmployment(EmploymentEmployed, sector)
	case EmploymentEmployed:
		// Already employed: a small deterministic chance of sector drift.
		stream := det.NewStream(seed, id, month, "employment-drift")
		if stream.IntN(4) == 0 {
			sector := Sector(stream.IntN(4) + 1)
			s.employment[i] = packEmployment(EmploymentEmployed, sector)
		}
	case EmploymentOffMap:
		// Off-map-employed citizens already have a real job (in an
		// extcommute pool, off-map) — the statistical job-matching draw
		// must not fire for them, same as an on-map EmploymentEmployed
		// citizen is exempt from re-matching. Explicit no-op case per
		// docs/planning/icd/engine.citizens-offmap.md §11 (this was an
		// accidental no-op via Go's zero-case fallthrough before FEAT-198;
		// now documented so a future reader cannot mistake it for a gap).
	}
}

// clampBand coerces a health-band value into [0, MaxHealthBand] (GR#16:
// never trust a stored field's declared range).
func clampBand(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > int(MaxHealthBand) {
		return uint8(MaxHealthBand)
	}
	return uint8(v)
}

// clampSat coerces a satisfaction component into its documented 0-100
// contract, returning the int8 the cold column stores. It takes an int32 so
// the drift arithmetic is promoted before the clamp (never int8 arithmetic
// that could overflow); the lower bound is 0, NOT int8's type-wide minimum —
// a component of 0 receiving a negative drift must stay 0, never go negative.
func clampSat(v int32) int8 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int8(v)
}
