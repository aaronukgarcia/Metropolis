package citizens

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-270 fix: the cold monthly pass skips every elevated citizen
// (applyMonthly's isHot filter) on the documented assumption that "the
// daily path" advances them — but no such path existed, so a HOT/WARM
// citizen could neither die nor be fertility-drawn while elevated. Fertility
// is fixed by dropping applyFertilityLocked's own tier skip (elevated
// citizens live in the cold store like everyone else); this file supplies
// the missing elevated MORTALITY half.

// hotScheduledDay returns the logistics day-tick (0..DaysPerMonth-1) an
// elevated citizen's monthly mortality draw is scheduled on: the identical
// shard-to-day mapping ColdPassSchedule assigns the citizen's id-hash shard,
// so an elevated citizen draws on exactly the day their shard would have
// been processed had they stayed COLD. A pure function of the id — never of
// elevation set membership or map iteration order.
func hotScheduledDay(id uint64) int {
	shard := det.ShardForEntity(id)
	return shard * DaysPerMonth / numColdShards
}

// applyHotMortalityLocked runs the monthly mortality draw for every
// currently-elevated HOT/WARM citizen scheduled on this day-tick (BUG-270).
// The draw mirrors applyMonthly's exactly — the same (seed, id, month,
// "mortality") stream against the same Gompertz-Makeham hazard scaled by the
// same sample-derived multiplier — so an elevated citizen's death decision is
// bit-identical to what the cold pass would have made for them; only the age/
// health/access inputs are re-read from the cold record, the single source of
// truth, rather than the elevation cache. Runs sequentially under c.mu (from
// AdvanceDayTick), before the fertility pass, mirroring the cold pass's own
// mortality-then-fertility ordering so a just-deceased partner can never
// conceive this tick. A death unwires the departed citizen through the SAME
// LifeEventDeath dissolution contract removeHouseholdMemberLocked provides —
// household Members pruning plus surviving-partner Partner clear — which the
// cold pass's bare removeAt has never done for a cold death (the residual gap
// BUG-270 documents). Returns the number of elevated deaths applied.
func (c *CitizensAPI) applyHotMortalityLocked(seed uint64, month int64, day int, params ColdPassParams) int {
	if len(c.hot) == 0 {
		return 0
	}
	ids := make([]uint64, 0, len(c.hot))
	for id := range c.hot {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	deaths := 0
	for _, id := range ids {
		if hotScheduledDay(id) != day {
			continue
		}
		r, ok := c.coldRecord(id)
		if !ok {
			continue // an elevated row must exist in the cold store; never mutate blind (GR#16)
		}
		age := month - r.BirthMonth
		stream := det.NewStream(seed, id, month, "mortality")
		hazard := MortalityHazard(age, r.HealthBand, r.Access) * params.MortalityMultiplier
		if hazard > 1 {
			hazard = 1
		}
		if stream.Float64() >= hazard {
			continue
		}
		// Resolve the dissolution targets BEFORE any removal — no record
		// remains to read them back from afterwards (LifeEventDeath's own
		// ordering discipline).
		var householdID, partnerID uint64
		if cit, ok := c.hot[id]; ok {
			householdID = cit.Household
			partnerID = cit.Partner
		} else {
			householdID = uint64(r.Household)
			partnerID = uint64(r.Partner)
		}
		delete(c.hot, id)
		c.removeColdLocked(id)
		c.removeHouseholdMemberLocked(id, householdID, partnerID)
		deaths++
	}
	return deaths
}
