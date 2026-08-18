package leisure

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// UnmetDemand is the unmet-taste-demand signal (AC-7): the population's
// taste demand minus the venue supply, per category, clamped at zero. It is
// decomposed per category — never a single blended shortfall number — so F6
// "tells you literally what to build next" (§42).
type UnmetDemand struct {
	District uint16
	Category [NumCategories]float64
}

// districtKnown reports whether a district query is valid: 0 is the citywide
// sentinel (always valid); a non-zero district is valid iff at least one
// venue is registered in it.
func districtKnown(venues map[uint64]Venue, district uint16) bool {
	if district == 0 {
		return true
	}
	for _, v := range venues {
		if v.District == district {
			return true
		}
	}
	return false
}

// validateTasteDistribution rejects a non-finite, negative, or zero-sum
// distribution (AC-9) with a registry-sourced error.
func validateTasteDistribution(d TasteDistribution, correlationID string) error {
	sum := 0.0
	for c := 0; c < NumCategories; c++ {
		if !num.IsFinite(d[c]) || d[c] < 0 {
			return errs.New(ErrInvalidTasteDistribution, correlationID, map[string]any{
				"reason": "non-finite or negative entry",
			})
		}
		sum += d[c]
	}
	if sum <= 0 {
		return errs.New(ErrInvalidTasteDistribution, correlationID, map[string]any{
			"reason": "distribution sums to zero",
		})
	}
	return nil
}

// normalizeDistribution normalises a TasteDistribution to sum to 1.
func normalizeDistribution(d TasteDistribution) TasteDistribution {
	var sum float64
	for c := 0; c < NumCategories; c++ {
		sum += d[c]
	}
	if sum <= 0 {
		return TasteDistribution{}
	}
	var out TasteDistribution
	for c := 0; c < NumCategories; c++ {
		out[c] = d[c] / sum
	}
	return out
}

// SetPopulationTaste sets the citywide aggregate population taste
// distribution used by UnmetTasteDemand. This is the pushed input for the
// missing census-enumeration edge (see doc.go): the composition root pushes
// the population's aggregate taste from engine.citizens rather than this
// module enumerating citizens it cannot reach.
func (a *LeisureAPI) SetPopulationTaste(d TasteDistribution, correlationID string) error {
	if err := a.checkNotCopied("SetPopulationTaste"); err != nil {
		return err
	}
	if err := validateTasteDistribution(d, correlationID); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tasteDemand = d
	return nil
}

// PopulationTaste returns the current citywide aggregate population taste
// distribution (Config.DefaultTaste until SetPopulationTaste overrides it)
// — the read side of the pushed-input seam. Added for FEAT-167's
// engine.attract Safety/LeisureFit/Environment wiring
// (docs/planning/icd/engine.attract-terms.md §3): the composition root
// needs to read leisure's own data-loaded would-be-migrant taste
// distribution to feed LeisureFitAggregate, without compose duplicating
// that data into a second copy it would have to keep in sync (GR#3).
func (a *LeisureAPI) PopulationTaste(correlationID string) TasteDistribution {
	_ = a.checkNotCopied("PopulationTaste")
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tasteDemand
}

// UnmetTasteDemand computes the per-category unmet taste demand for a
// district (0 = citywide), against the current population taste (AC-7). A
// non-zero district with no registered venues returns ErrUnknownDistrict.
// The home category is always satisfiable, so its unmet figure is always 0.
func (a *LeisureAPI) UnmetTasteDemand(district uint16, correlationID string) (UnmetDemand, error) {
	if err := a.checkNotCopied("UnmetTasteDemand"); err != nil {
		return UnmetDemand{}, err
	}
	a.mu.RLock()
	venues := make(map[uint64]Venue, len(a.venues))
	for id, v := range a.venues {
		venues[id] = v
	}
	demand := a.tasteDemand
	a.mu.RUnlock()

	if !districtKnown(venues, district) {
		return UnmetDemand{}, errs.New(ErrUnknownDistrict, correlationID, map[string]any{
			"district": district,
		})
	}

	supply := venueSupply(venues, district)
	var unmet UnmetDemand
	unmet.District = district
	for c := 0; c < NumCategories; c++ {
		if c == CategoryHome {
			unmet.Category[c] = 0 // home always satisfiable
			continue
		}
		d := demand[c]
		s := supply[c]
		if d > s {
			unmet.Category[c] = d - s
		}
	}
	return unmet, nil
}

// VenueMix returns the per-category venue capacity for a district (0 =
// citywide) — the queryable venue-mix signal for engine.tourism and the F6
// "how your city spends Saturday" view (US-5/US-7). A non-zero district with
// no venues returns ErrUnknownDistrict.
func (a *LeisureAPI) VenueMix(district uint16, correlationID string) ([NumCategories]float64, error) {
	if err := a.checkNotCopied("VenueMix"); err != nil {
		return [NumCategories]float64{}, err
	}
	a.mu.RLock()
	venues := make(map[uint64]Venue, len(a.venues))
	for id, v := range a.venues {
		venues[id] = v
	}
	a.mu.RUnlock()

	if !districtKnown(venues, district) {
		return [NumCategories]float64{}, errs.New(ErrUnknownDistrict, correlationID, map[string]any{
			"district": district,
		})
	}
	return venueSupply(venues, district), nil
}

// LeisureFitAggregate computes the citywide leisureFit aggregate (AC-9):
// venue mix vs a configurable would-be-migrant personality distribution,
// distinct from AC-3's per-citizen allocation and AC-10's per-citizen
// leisure-fit. It matches §11's literal term w₅·leisureFit(venue mix vs
// would-be migrant personality distribution). The value is queried here and
// PUSHED into engine.attract by a caller (no engine.attract → engine.leisure
// edge is registered — the known BUG-058 gap).
func (a *LeisureAPI) LeisureFitAggregate(d TasteDistribution, correlationID string) (float64, error) {
	if err := a.checkNotCopied("LeisureFitAggregate"); err != nil {
		return 0, err
	}
	if err := validateTasteDistribution(d, correlationID); err != nil {
		return 0, err
	}
	a.mu.RLock()
	venues := make(map[uint64]Venue, len(a.venues))
	for id, v := range a.venues {
		venues[id] = v
	}
	a.mu.RUnlock()

	supply := venueSupply(venues, 0)
	return leisureFitOverlap(normalizeDistribution(d), supply), nil
}

// LeisureFit computes a citizen's per-citizen leisure-fit (venue mix vs
// their own taste weights, §18/§5.1) and pushes it through the registered
// engine.leisure → engine.wellbeing edge (AC-10), matching the LeisureFit
// driver name engine.wellbeing.md AC-1 requires. A citizen with zero
// matching venues in range yields a lower value than an otherwise-identical
// citizen with several matching venues. If wellbeing is not wired, the value
// is still computed and returned (the push is the only thing skipped).
func (a *LeisureAPI) LeisureFit(citizenID uint64, correlationID string) (float64, error) {
	if err := a.checkNotCopied("LeisureFit"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	wellbeing := a.wellbeing
	venues := make(map[uint64]Venue, len(a.venues))
	for id, v := range a.venues {
		venues[id] = v
	}
	a.mu.RUnlock()

	if citizensAPI == nil {
		return 0, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "LeisureFit", "dependency": "citizens",
		})
	}
	cit, ok := citizensAPI.CitizenAt(citizenID, correlationID)
	if !ok {
		return 0, errs.New(ErrUnknownCitizen, correlationID, map[string]any{"citizenId": citizenID})
	}

	supply := venueSupply(venues, 0)
	taste, _ := normalizeTaste(cit.Leisure)
	fit := leisureFitOverlap(taste, supply)

	if wellbeing != nil {
		_ = wellbeing.SetLeisureFit(citizenID, fit)
	}
	return fit, nil
}
