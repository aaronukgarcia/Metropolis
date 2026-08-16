package leisure

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Freshness returns a citizen's current freshness (novelty) for a venue —
// the AC-4 accessor. A venue the citizen has never visited has implicit
// freshness 1.0. Unknown citizen/venue returns a registry-sourced error.
func (a *LeisureAPI) Freshness(citizenID, venueID uint64, correlationID string) (float64, error) {
	if err := a.checkNotCopied("Freshness"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	if _, ok := a.venues[venueID]; !ok {
		a.mu.RUnlock()
		return 0, errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": venueID})
	}
	f := 1.0
	if m, ok := a.freshness[citizenID]; ok {
		if v, ok2 := m[venueID]; ok2 {
			f = v
		}
	}
	a.mu.RUnlock()

	if citizensAPI == nil {
		return 0, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "Freshness", "dependency": "citizens",
		})
	}
	if _, ok := citizensAPI.CitizenAt(citizenID, correlationID); !ok {
		return 0, errs.New(ErrUnknownCitizen, correlationID, map[string]any{"citizenId": citizenID})
	}
	return f, nil
}

// Visit simulates one patronage visit by a citizen to a venue (AC-4): it
// decays the citizen's freshness for that venue by the novelty-scaled rate,
// so a novelty-seeking citizen's freshness falls faster. Deterministic and
// monotone — freshness never rises on a visit, never goes below zero.
func (a *LeisureAPI) Visit(citizenID, venueID uint64, correlationID string) error {
	if err := a.checkNotCopied("Visit"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.venues[venueID]; !ok {
		return errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": venueID})
	}
	if a.citizens == nil {
		return errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "Visit", "dependency": "citizens",
		})
	}
	cit, ok := a.citizens.CitizenAt(citizenID, correlationID)
	if !ok {
		return errs.New(ErrUnknownCitizen, correlationID, map[string]any{"citizenId": citizenID})
	}

	m := a.freshness[citizenID]
	if m == nil {
		m = make(map[uint64]float64)
		a.freshness[citizenID] = m
	}
	cur := 1.0
	if fv, ok := m[venueID]; ok {
		cur = fv
	}
	cur -= noveltyDecay(cit.Personality[citizens.AxisNovelty], a.cfg)
	if cur < 0 {
		cur = 0
	}
	m[venueID] = cur

	vm := a.visits[citizenID]
	if vm == nil {
		vm = make(map[uint64]int64)
		a.visits[citizenID] = vm
	}
	vm[venueID]++
	return nil
}

// PatronageProbability returns the citizen's patronage probability for a
// venue (AC-4): a deterministic function of their taste match for the
// venue's category, the access-time penalty, and the venue's current
// freshness. As freshness decays with repeated visits, the probability
// strictly decreases for a novelty-seeking citizen.
func (a *LeisureAPI) PatronageProbability(citizenID, venueID uint64, correlationID string) (float64, error) {
	if err := a.checkNotCopied("PatronageProbability"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	traffic := a.traffic
	cfg := a.cfg
	v, ok := a.venues[venueID]
	if !ok {
		a.mu.RUnlock()
		return 0, errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": venueID})
	}
	f := 1.0
	if m, ok := a.freshness[citizenID]; ok {
		if fv, ok2 := m[venueID]; ok2 {
			f = fv
		}
	}
	a.mu.RUnlock()

	if citizensAPI == nil {
		return 0, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "PatronageProbability", "dependency": "citizens",
		})
	}
	cit, ok := citizensAPI.CitizenAt(citizenID, correlationID)
	if !ok {
		return 0, errs.New(ErrUnknownCitizen, correlationID, map[string]any{"citizenId": citizenID})
	}

	access := accessMinutesFor(traffic, citizenID, correlationID)
	tasteMatch := float64(cit.Leisure[v.Category]) / citizens.MaxPersonalityAxis
	p := f * tasteMatch * accessFactor(access[v.Category], cfg.AccessFreeMinutes, cfg.AccessBudgetMinutes)
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	return p, nil
}

// OpenVenue registers a new leisure venue (AC-5's "opening" — a fresh venue
// has implicit freshness 1.0 for every citizen until visited). A zero ID, a
// category outside the seven going-out categories, or a non-positive
// capacity is rejected (ErrInvalidVenue).
func (a *LeisureAPI) OpenVenue(v Venue, correlationID string) error {
	if err := a.checkNotCopied("OpenVenue"); err != nil {
		return err
	}
	if v.ID == 0 {
		return errs.New(ErrInvalidVenue, correlationID, map[string]any{"reason": "venue ID must be non-zero"})
	}
	if v.Category < CategorySport || v.Category >= CategoryHome {
		return errs.New(ErrInvalidVenue, correlationID, map[string]any{
			"reason": "venue category must be a going-out category (home is not a venue)",
		})
	}
	if v.Capacity <= 0 {
		return errs.New(ErrInvalidVenue, correlationID, map[string]any{
			"reason": "venue capacity must be positive",
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.venues[v.ID] = v
	return nil
}

// RefurbishVenue applies an "opened/refurbished" event to an existing venue
// (AC-5): it resets the freshness value (which novelty decay had reduced)
// back to Config.FreshnessRecovery for every citizen whose taste weight
// matches the venue's category (at least MatchThreshold).
func (a *LeisureAPI) RefurbishVenue(venueID uint64, correlationID string) error {
	if err := a.checkNotCopied("RefurbishVenue"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	v, ok := a.venues[venueID]
	if !ok {
		return errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": venueID})
	}

	recovery := a.cfg.FreshnessRecovery
	threshold := a.cfg.MatchThreshold
	for cid, m := range a.freshness {
		if _, has := m[venueID]; !has {
			continue
		}
		matches := true
		if a.citizens != nil {
			if cit, ok := a.citizens.CitizenAt(cid, correlationID); ok {
				matches = float64(cit.Leisure[v.Category]) >= threshold
			}
		}
		if matches {
			m[venueID] = recovery
		}
	}
	return nil
}

// RemoveVenue removes a venue from the inventory (AC-7's fixture-city
// operation). Its per-citizen freshness entries become inert (a removed
// venue is never queried).
func (a *LeisureAPI) RemoveVenue(venueID uint64, correlationID string) error {
	if err := a.checkNotCopied("RemoveVenue"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.venues[venueID]; !ok {
		return errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": venueID})
	}
	delete(a.venues, venueID)
	return nil
}
