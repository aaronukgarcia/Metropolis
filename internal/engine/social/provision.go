package social

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file holds the provision effects — the "what happens to a case"
// mechanics §40 describes: the child-protection intervention marker written
// to the citizen record (AC-6), the three-path homelessness pipeline and its
// town-centre rough-sleeping count (AC-7), the informal-carers workforce
// release (AC-8), and the capacity-gated fostering placement (AC-9).

// RecordChildProtectionIntervention is the AC-6 cohort-audit write: at the
// moment of the intervention decision it (1) opens a family-support case and
// (2) writes a documented marker to the affected citizen's record through
// engine.citizens' command path (LifeEventHealth → HealthBand, per
// engine.citizens.md AC-1b — never a direct field write). Under underfunding
// (quality below the data-sourced InterventionHarmThreshold) the marker is
// HealthCritical — the "harm" outcome §40 says surfaces a decade later as
// attainment down and crime up, made literal and inspectable now rather than
// deferred. Adequate funding writes HealthFair (stabilised under protection).
// The citizen-record write happens at the SAME simulated month as the
// decision — no elapsed time required to inspect it (AC-6).
func (a *SocialAPI) RecordChildProtectionIntervention(citizenID uint64, month int64, quality float64) (CaseID, error) {
	if err := a.checkNotCopied("RecordChildProtectionIntervention"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	a.mu.RUnlock()
	if citizensAPI == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "citizens", "operation": "RecordChildProtectionIntervention",
		})
	}

	band := citizens.HealthFair
	if quality < a.cfg.InterventionHarmThreshold {
		band = citizens.HealthCritical
	}

	// Open the authoritative case record (internal, conserved) first.
	id := a.openCase(CategoryFamilySupport, month, citizenID, "intervention", "", 0)

	// Then write the citizen-record fingerprint (external, via the command
	// path). The citizens write never fails for an unknown citizen (it
	// no-ops), so the ledger record above is the durable audit trail and the
	// health-band write is the traceable marker in the citizen record.
	if err := citizensAPI.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: a.correlationID,
		Kind:          citizens.LifeEventHealth,
		CitizenID:     citizenID,
		HealthBand:    band,
	}); err != nil {
		return id, err
	}
	return id, nil
}

// SetPrevention toggles the homelessness-prevention path (AC-7): when on, an
// incoming homelessness case is intercepted before street homelessness.
func (a *SocialAPI) SetPrevention(enabled bool) error {
	if err := a.checkNotCopied("SetPrevention"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.preventionEnabled = enabled
	return nil
}

// SetHousingFirst toggles the policy-gated housing-first path (AC-7): when
// off, a homelessness case that fails prevention and hostel goes to rough
// sleeping even if a direct-to-housing path would otherwise exist.
func (a *SocialAPI) SetHousingFirst(enabled bool) error {
	if err := a.checkNotCopied("SetHousingFirst"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.housingFirstEnabled = enabled
	return nil
}

// RouteHomelessness routes every currently-open homelessness case through the
// three documented paths in priority order — prevention → hostel → housing-
// first — and leaves the cases all three fail open (AC-7). RoughSleeping()
// derives the town-centre rough-sleeping count from those still-open cases, so
// re-routing the same open case across months never double-counts it
// (SEC-177). Hostel capacity is per-month occupancy: a new routing month
// releases the previous month's beds (SEC-178). Allocation of limited hostel
// beds across pending cases follows a documented deterministic rule: cases are
// sorted by their counter-based hash-stream priority (worldSeed, caseID,
// month), with CaseID as the final tiebreak — never map-iteration order
// (AC-15/GR#21). Only cases already open at (or before) `month` are routed;
// a not-yet-opened case is skipped, never closed back-dated (SEC-180).
func (a *SocialAPI) RouteHomelessness(month int64) error {
	if err := a.checkNotCopied("RouteHomelessness"); err != nil {
		return err
	}

	// Snapshot the services dependency and read the hostel capacity OUTSIDE
	// the lock (services holds its own lock; no social lock across the seam).
	a.mu.RLock()
	servicesAPI := a.services
	a.mu.RUnlock()
	capacity := a.cfg.HostelCapacity
	if servicesAPI != nil {
		if c, err := servicesAPI.Capacity(services.ServiceID(categoryServiceID(CategoryHomelessness))); err == nil && c >= 0 {
			capacity = num.ClampInt64FromFloat(c) // GR#16: no bare int64(f) — float64(MaxInt64)=2^63 wraps negative on amd64 (SEC-201)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// A new routing month releases the previous month's hostel beds: capacity
	// is per-month occupancy, never a lifetime cap (SEC-178). The still-open
	// cases from earlier months are re-picked below so they can fill the
	// freshly-freed beds.
	if month > a.lastHostelMonth {
		a.hostelOccupancy = 0
		a.lastHostelMonth = month
	}

	type pending struct {
		id  CaseID
		pri uint64
	}
	var queue []pending
	for _, c := range a.cases {
		// Only a case that already exists at `month` can be routed; one
		// opened after `month` is skipped rather than closed back-dated
		// (SEC-180). Since every queued case has OpenedMonth <= month, the
		// closeCaseLocked below can never fail on the back-dated check.
		if c.Category == CategoryHomelessness && c.Status == StatusOpen && c.OpenedMonth <= month {
			st := det.NewStream(a.seed, uint64(c.ID), month, "social.placement")
			queue = append(queue, pending{id: c.ID, pri: st.Uint64()})
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].pri != queue[j].pri {
			return queue[i].pri < queue[j].pri
		}
		return queue[i].id < queue[j].id
	})

	for _, p := range queue {
		switch {
		case a.preventionEnabled:
			a.prevented++
			_ = a.closeCaseLocked(p.id, month, StatusResolved, "prevention", 0)
		case a.hostelOccupancy < capacity:
			a.hostelOccupancy++
			_ = a.closeCaseLocked(p.id, month, StatusResolved, "hostel", 0)
		case a.housingFirstEnabled:
			a.housingFirstPlaced++
			_ = a.closeCaseLocked(p.id, month, StatusResolved, "housing-first", 0)
		default:
			// stays open: unresolved. RoughSleeping() derives the current
			// rough-sleeping stock from open homelessness cases, so this pass
			// never re-increments a cumulative ever-counter (SEC-177).
		}
	}
	return nil
}

// RoughSleeping returns the current rough-sleeping stock (AC-7): the number of
// homelessness cases still open because all three paths failed to place them.
// It is derived from the ledger each call — a case that stays open across
// routing passes is counted once, never re-incremented per pass (SEC-177).
func (a *SocialAPI) RoughSleeping() int64 {
	if err := a.checkNotCopied("RoughSleeping"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.roughSleepersLocked()
}

// roughSleepersLocked returns the current rough-sleeping stock: the count of
// open homelessness cases. Caller holds a.mu (read or write).
func (a *SocialAPI) roughSleepersLocked() int64 {
	var n int64
	for _, c := range a.cases {
		if c.Category == CategoryHomelessness && c.Status == StatusOpen {
			n++
		}
	}
	return n
}

// RoughSleepingLocation returns the documented location rough sleeping is
// attributed to (town centre, §40) — data-sourced, never a hardcoded literal.
func (a *SocialAPI) RoughSleepingLocation() string {
	if err := a.checkNotCopied("RoughSleepingLocation"); err != nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.RoughSleepingLocation
}

// Prevented returns the cumulative homelessness cases intercepted by
// prevention (an ever-counter — prevention has no capacity gate, so a
// lifetime total is the correct shape).
func (a *SocialAPI) Prevented() int64 {
	if err := a.checkNotCopied("Prevented"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.prevented
}

// HostelPlaced returns the current-month hostel occupancy (the number of beds
// filled this routing month, against HostelCapacity). Hostel capacity is
// per-month occupancy, not a lifetime cap (SEC-178).
func (a *SocialAPI) HostelPlaced() int64 {
	if err := a.checkNotCopied("HostelPlaced"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hostelOccupancy
}

// HousingFirstPlaced returns the cumulative homelessness cases placed via the
// housing-first path (an ever-counter — housing-first has no capacity gate).
func (a *SocialAPI) HousingFirstPlaced() int64 {
	if err := a.checkNotCopied("HousingFirstPlaced"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.housingFirstPlaced
}

// CarersReleased returns the "informal carers released back to the
// workforce" figure (AC-8): the disability & carers funding level × the
// data-sourced release rate. At fixed caseload, increasing funding increases
// the figure — a real labour-supply effect, not a satisfaction-only number.
// Funding is read live from engine.services (AC-4's shared funding path).
func (a *SocialAPI) CarersReleased() int64 {
	if err := a.checkNotCopied("CarersReleased"); err != nil {
		return 0
	}
	a.mu.RLock()
	servicesAPI := a.services
	a.mu.RUnlock()
	if servicesAPI == nil {
		return 0
	}
	level, err := servicesAPI.FundingLevel(services.ServiceID(categoryServiceID(CategoryDisabilityCarers)))
	if err != nil || level <= 0 {
		return 0
	}
	return caseloadCount(level * a.cfg.CarersReleasedPerFundingUnit)
}

// AttemptFosteringPlacement attempts one fostering placement for an open
// fostering case (AC-9). Capacity is gated via engine.services; when the
// current placement count is at capacity the attempt returns PlacementQueued
// — a documented no-match/queued state — never a silently-succeeded
// placement. Returns the placement result; a non-open or unknown case is an
// error.
func (a *SocialAPI) AttemptFosteringPlacement(caseID CaseID, month int64) (PlacementResult, error) {
	if err := a.checkNotCopied("AttemptFosteringPlacement"); err != nil {
		return PlacementQueued, err
	}

	a.mu.RLock()
	servicesAPI := a.services
	a.mu.RUnlock()
	capacity := a.cfg.FosterCapacity
	if servicesAPI != nil {
		if c, err := servicesAPI.Capacity(services.ServiceID(categoryServiceID(CategoryFostering))); err == nil && c >= 0 {
			capacity = num.ClampInt64FromFloat(c) // GR#16: no bare int64(f) — float64(MaxInt64)=2^63 wraps negative on amd64 (SEC-201)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// A new placement month releases the previous month's foster placements:
	// capacity is per-month occupancy, not a lifetime cap (SEC-178).
	if month > a.lastFosterMonth {
		a.fosterPlacements = 0
		a.lastFosterMonth = month
	}

	c, ok := a.lookupLocked(caseID)
	if !ok {
		return PlacementQueued, errs.New(ErrUnknownCase, a.correlationID, map[string]any{"case": uint64(caseID)})
	}
	if c.Status != StatusOpen {
		return PlacementQueued, errs.New(ErrDoubleClose, a.correlationID, map[string]any{"case": uint64(caseID), "status": int(c.Status)})
	}
	if a.fosterPlacements >= capacity {
		return PlacementQueued, nil
	}
	// Close first and check the error (a back-dated month is rejected by
	// closeCaseLocked — SEC-180) so a failed placement never increments the
	// occupancy counter.
	if err := a.closeCaseLocked(caseID, month, StatusResolved, "placement", 0); err != nil {
		return PlacementQueued, err
	}
	a.fosterPlacements++
	return PlacementPlaced, nil
}
