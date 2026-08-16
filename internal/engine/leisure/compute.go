package leisure

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file holds the pure, deterministic computation primitives the rest
// of the package builds on. Every function here is a pure function of its
// inputs (GR#21): no wall clock, no shared RNG, no map iteration in a
// results-affecting position. They are exercised directly by the package's
// determinism test.

// accessFactor returns the access-time penalty in [0,1] for a category whose
// door-to-door access is `minutes`: 1 at or below free, 0 at or above budget,
// linear between (AC-3's access-time constraint). A non-finite minutes/free/
// budget is treated as a full block (0) so NaN in never yields NaN out
// (GR#16), matching the finite guard DiscretionaryHours applies to commute.
func accessFactor(minutes, free, budget float64) float64 {
	if !num.IsFinite(minutes) || !num.IsFinite(free) || !num.IsFinite(budget) {
		return 0
	}
	if minutes <= free {
		return 1
	}
	if minutes >= budget {
		return 0
	}
	return 1 - (minutes-free)/(budget-free)
}

// goingOutShare returns the fraction of a citizen's taste that is NOT "home"
// (categories 0..6): the share of discretionary time spent out at venues
// rather than resting at home. Pure.
func goingOutShare(w citizens.LeisureWeights) float64 {
	var total, out float64
	for c := 0; c < NumCategories; c++ {
		total += float64(w[c])
		if c != CategoryHome {
			out += float64(w[c])
		}
	}
	if total <= 0 {
		return 0
	}
	return out / total
}

// allocateHours allocates `total` leisure hours across the seven going-out
// categories (0..6) in proportion to taste, each reduced by its access-time
// penalty, then renormalised so the going-out hours sum exactly to `total`
// (the weekly budget is conserved). The home category is rest, handled by
// the budget, not here. Pure.
func allocateHours(w citizens.LeisureWeights, total float64, access [NumCategories]float64, cfg Config) [NumCategories]float64 {
	var out [NumCategories]float64
	if total <= 0 {
		return out
	}
	var weighted [NumCategories]float64
	var sum float64
	for c := 0; c < NumCategories; c++ {
		if c == CategoryHome {
			continue
		}
		f := accessFactor(access[c], cfg.AccessFreeMinutes, cfg.AccessBudgetMinutes)
		weighted[c] = float64(w[c]) * f
		sum += weighted[c]
	}
	if sum <= 0 {
		return out
	}
	for c := 0; c < NumCategories; c++ {
		if c == CategoryHome {
			continue
		}
		out[c] = total * weighted[c] / sum
	}
	return out
}

// venueSupply sums venue capacity per category for venues in the given
// district (0 = citywide). Pure. Venue keys are sorted before summing so the
// float64 accumulation order is deterministic (GR#21): map-iteration order
// would otherwise let same-category capacities round to different sums.
func venueSupply(venues map[uint64]Venue, district uint16) [NumCategories]float64 {
	var s [NumCategories]float64
	ids := make([]uint64, 0, len(venues))
	for id := range venues {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := venues[id]
		if district != 0 && v.District != district {
			continue
		}
		s[v.Category] += float64(v.Capacity)
	}
	return s
}

// normalizeTaste normalises a citizens.LeisureWeights vector into a
// distribution. Returns (dist, sum); a zero sum yields a zero dist and 0.
func normalizeTaste(w citizens.LeisureWeights) (TasteDistribution, float64) {
	var d TasteDistribution
	var sum float64
	for c := 0; c < NumCategories; c++ {
		d[c] = float64(w[c])
		sum += d[c]
	}
	if sum <= 0 {
		return TasteDistribution{}, 0
	}
	for c := 0; c < NumCategories; c++ {
		d[c] /= sum
	}
	return d, sum
}

// leisureFitOverlap is the overlap (0..1) between a normalised taste
// distribution and a venue-capacity distribution — the §18/§5.1 "venue mix
// vs personal taste weights" measure. The home category is treated as always
// satisfiable (a citizen can always stay home), so its share contributes
// fully; the going-out share is min-overlap against the venue supply. A city
// with no going-out venues at all contributes zero on the going-out side.
// Pure.
func leisureFitOverlap(taste TasteDistribution, supply [NumCategories]float64) float64 {
	var ts, ss float64
	for c := 0; c < NumCategories; c++ {
		ts += taste[c]
		if c != CategoryHome {
			ss += supply[c]
		}
	}
	if ts <= 0 {
		return 0
	}
	fit := 0.0
	for c := 0; c < NumCategories; c++ {
		tc := taste[c] / ts
		if c == CategoryHome {
			fit += tc // home always satisfied
			continue
		}
		if ss <= 0 {
			continue // no going-out venues at all
		}
		sc := supply[c] / ss
		if tc < sc {
			fit += tc
		} else {
			fit += sc
		}
	}
	if fit > 1 {
		fit = 1
	}
	return fit
}

// noveltyDecay computes the per-visit freshness decay for a citizen's
// novelty-seeking axis: base + (axis/100)*perNovelty. Higher novelty decays
// faster (AC-4). Pure.
func noveltyDecay(noveltyAxis int32, cfg Config) float64 {
	return cfg.NoveltyDecayBase + (float64(noveltyAxis)/citizens.MaxPersonalityAxis)*cfg.NoveltyDecayPerNovelty
}
