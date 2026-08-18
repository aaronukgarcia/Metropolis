package leisure

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Patronage is the per-citizen venue-patronage result (AC-1/AC-3): the
// weekly budget, the per-category hour allocation (taste-weighted and
// access-penalised), the per-category patronage probability, and the
// deterministic venue draw for the month (AC-14).
type Patronage struct {
	CitizenID uint64
	Budget    TimeBudget
	Hours     [NumCategories]float64 // going-out hours per category
	// Probability is the per-category share of leisure hours — the
	// probability a given leisure hour is spent at that category.
	Probability [NumCategories]float64
	// DrawnVenue is the deterministically drawn venue this month, 0 = home
	// (no drawn venue).
	DrawnVenue uint64
}

// accessMinutesLocked gathers the per-category door-to-door access minutes
// for a citizen from the traffic dependency. traffic == nil (unwired) yields
// zero minutes (no access penalty) — the documented stub default.
func accessMinutesFor(traffic TrafficAPI, citizenID uint64, correlationID string) [NumCategories]float64 {
	var am [NumCategories]float64
	if traffic == nil {
		return am
	}
	for c := 0; c < NumCategories; c++ {
		if m, err := traffic.AccessMinutes(citizenID, c, correlationID); err == nil && m >= 0 {
			am[c] = m
		}
	}
	return am
}

// VenueHours computes a citizen's venue-category hour allocation (AC-3): the
// citizen's leisure hours split across the going-out categories in
// proportion to their own taste weights, each reduced by an access-time
// penalty from engine.traffic. An unknown citizen returns ErrUnknownCitizen.
func (a *LeisureAPI) VenueHours(citizenID uint64, correlationID string) ([NumCategories]float64, error) {
	if err := a.checkNotCopied("VenueHours"); err != nil {
		return [NumCategories]float64{}, err
	}
	p, err := a.Patronage(citizenID, correlationID)
	if err != nil {
		return [NumCategories]float64{}, err
	}
	return p.Hours, nil
}

// Patronage is the patronage query (AC-1): it takes a citizen ID (whose
// personality/taste the citizen record supplies) and returns the full
// budget, allocation, and deterministic draw. Deterministic (AC-14): the
// draw uses hash(worldSeed, citizenID, month, "leisure.patronage").
func (a *LeisureAPI) Patronage(citizenID uint64, correlationID string) (Patronage, error) {
	if err := a.checkNotCopied("Patronage"); err != nil {
		return Patronage{}, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	traffic := a.traffic
	cfg := a.cfg
	seed := a.seed
	month := a.month
	venues := make(map[uint64]Venue, len(a.venues))
	for id, v := range a.venues {
		venues[id] = v
	}
	fresh := a.freshness[citizenID]
	overtime := a.overtime[citizenID]
	a.mu.RUnlock()

	if citizensAPI == nil {
		return Patronage{}, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "Patronage", "dependency": "citizens",
		})
	}
	cit, ok := citizensAPI.CitizenAt(citizenID, correlationID)
	if !ok {
		return Patronage{}, errs.New(ErrUnknownCitizen, correlationID, map[string]any{
			"citizenId": citizenID,
		})
	}

	commute, err := commuteHours(traffic, citizenID, correlationID)
	if err != nil {
		return Patronage{}, err
	}
	budget := computeBudget(cfg, lifeStageFor(cit), commute, overtime, cit.Leisure)
	access := accessMinutesFor(traffic, citizenID, correlationID)
	hours := allocateHours(cit.Leisure, budget.LeisureHours, access, cfg)

	var prob [NumCategories]float64
	if budget.LeisureHours > 0 {
		for c := 0; c < NumCategories; c++ {
			prob[c] = hours[c] / budget.LeisureHours
		}
	}

	drawn := drawVenue(det.NewStream(seed, citizenID, month, "leisure.patronage"), venues, hours, fresh)

	return Patronage{
		CitizenID:   citizenID,
		Budget:      budget,
		Hours:       hours,
		Probability: prob,
		DrawnVenue:  drawn,
	}, nil
}

// drawVenue deterministically picks a venue to visit this month, weighted by
// the citizen's allocated hours for each venue's category times its
// freshness. Candidates are sorted by id so the pick is order-independent.
// Returns 0 (stayed home) when there is no positive-weight candidate.
func drawVenue(stream det.Stream, venues map[uint64]Venue, hours [NumCategories]float64, fresh map[uint64]float64) uint64 {
	type cand struct {
		id uint64
		w  float64
	}
	cands := make([]cand, 0, len(venues))
	var total float64
	for id, v := range venues {
		if v.Category == CategoryHome {
			continue
		}
		f := 1.0
		if fv, ok := fresh[id]; ok {
			f = fv
		}
		w := hours[v.Category] * f
		if w <= 0 {
			continue
		}
		cands = append(cands, cand{id: id, w: w})
		total += w
	}
	if total <= 0 {
		return 0
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].id < cands[j].id })
	r := stream.Float64() * total
	var acc float64
	for _, c := range cands {
		acc += c.w
		if r < acc {
			return c.id
		}
	}
	return cands[len(cands)-1].id
}
