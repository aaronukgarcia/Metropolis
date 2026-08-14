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
	deaths  int
	updated int
}

// add folds one shard's totals into the accumulator (in shard order).
func (t passTotals) add(o passTotals) passTotals {
	return passTotals{deaths: t.deaths + o.deaths, updated: t.updated + o.updated}
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

// applyMonthly advances every cold citizen in the shard exactly once for
// the given month (AC-7): aging (implicit — age is derived from
// birthMonth), per-person Gompertz-Makeham mortality (AC-11),
// education-stage transition, statistical job matching, health drift, and
// satisfaction drift (AC-8 — every rate comes from params, none
// hardcoded). It is a pure function of (seed, the shard's own columns,
// month, params): no shared RNG, no wall clock, no cross-shard reads
// (AC-15/AC-17). isHot, when non-nil, identifies citizens currently
// elevated to HOT/WARM: those are advanced by the daily path, not the
// monthly cold pass, so they are skipped here (never double-advanced).
func (s *ColdShard) applyMonthly(seed uint64, month int64, params ColdPassParams, isHot func(uint64) bool) passTotals {
	var tot passTotals
	i := 0
	for i < s.count() {
		id := s.ids[i]
		if isHot != nil && isHot(id) {
			i++
			continue
		}
		birthMonth := s.epochMonth + int64(s.birthDelta[i])
		age := month - birthMonth

		// Mortality: per-person draw from hash(worldSeed, id, month,
		// "mortality") against the Gompertz-Makeham hazard scaled by the
		// sample-measured multiplier. A death is a removal, never a cull —
		// it is the per-individual probabilistic event §5.1 specifies.
		stream := det.NewStream(seed, id, month, "mortality")
		hazard := MortalityHazard(age, HealthBand(s.healthBands[i]), s.access[i]) * params.MortalityMultiplier
		if hazard > 1 {
			hazard = 1
		}
		if stream.Float64() < hazard {
			s.removeAt(i)
			tot.deaths++
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
