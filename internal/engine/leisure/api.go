package leisure

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TrafficAPI is the local contract shape for engine.traffic's commute and
// trip-generation surface — code.json's registered engine.leisure →
// engine.traffic outbound edge. engine.traffic (MOD-023) is not yet built,
// so this package consumes the contract shape directly (GR#20 contract-
// first, stub-forever): the composition root wires the real implementation
// once it lands, and tests inject a fake. The commute figure is §19.3's
// real door-to-door number (AC-2), never a flat estimate.
type TrafficAPI interface {
	// CommuteHours returns this citizen's weekly door-to-door work-commute
	// hours (AC-2). Two citizens with different commutes must yield
	// different figures from their own traffic data.
	CommuteHours(citizenID uint64, correlationID string) (float64, error)

	// AccessMinutes returns the door-to-door access time in minutes to the
	// nearest venue of the given category for this citizen (AC-3's access-
	// time constraint, reflecting evening/weekend network load).
	AccessMinutes(citizenID uint64, category Category, correlationID string) (float64, error)

	// AddTripDemand pushes one crowd-transport trip-demand record (AC-6).
	AddTripDemand(d TripDemand) error
}

// TripDemand is one crowd-transport demand record pushed to engine.traffic
// (AC-6): a district/day, the person-trip count, and the purpose. A
// scheduled event pushes a real trip-generation load through this record,
// never a satisfaction-only flag.
type TripDemand struct {
	District uint16
	Day      int
	Count    int64
	Purpose  string
}

// WellbeingAPI is the local contract shape for engine.wellbeing's LeisureFit
// driver — code.json's registered engine.leisure → engine.wellbeing outbound
// edge (the engine/wellbeing import path, i.e.
// github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing). The real
// engine.wellbeing package is bridged by [WellbeingLeisureFitAdapter] (see
// seams.go); this package consumes the contract shape (GR#20 contract-first,
// stub-forever) and the composition root wires the adapter — the per-citizen
// push below is the live call AC-10 requires.
type WellbeingAPI interface {
	// SetLeisureFit pushes one citizen's per-citizen leisure-fit driver
	// value (venue mix vs personal taste weights, §18/§5.1).
	SetLeisureFit(citizenID uint64, fit float64) error
}

// LeisureAPI is code.json's "engine.leisure" inbound contract (GUID
// d7d01c42-212d-48f4-b9b9-984bf33bd1ce, "venue patronage personality-
// weighted; unmet taste demand queryable"): the §42 168-hour weekly
// discretionary-hours budget, the personality-weighted venue-category
// allocation with the access-time penalty, per-citizen per-venue novelty
// decay, the openings/refurbishment pipeline, the events calendar and its
// crowd-transport push, the unmet-taste-demand query, and the citywide /
// per-citizen leisure-fit surface. It consumes engine.citizens,
// engine.traffic, and engine.wellbeing through their registered contracts
// alone (GR#20).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *LeisureAPI is safe for concurrent use (AC-15); a method call on a
// struct-copied value is rejected (SEC-020 family, mirroring
// engine.citizens' CitizensAPI).
type LeisureAPI struct {
	correlationID string
	seed          uint64
	month         int64
	cfg           Config

	// Dependencies, wired via SetCitizens/SetTraffic/SetWellbeing and read
	// under mu. traffic/wellbeing are interfaces; citizens is the concrete
	// *citizens.CitizensAPI, mirroring engine.education. The real
	// engine.wellbeing is bridged by [WellbeingLeisureFitAdapter] (seams.go);
	// engine.traffic is still unbuilt and consumed contract-first.
	citizens  *citizens.CitizensAPI
	traffic   TrafficAPI
	wellbeing WellbeingAPI

	// venues is the authoritative venue inventory (id → venue).
	venues map[uint64]Venue

	// freshness/visits are per (citizenID, venueID). A venue absent from the
	// inner map has implicit freshness 1.0 (never visited).
	freshness map[uint64]map[uint64]float64
	visits    map[uint64]map[uint64]int64

	// overtime is per-citizen weekly overtime hours (the player/citizen
	// choice that drives the overtime trade-off).
	overtime map[uint64]float64

	// tasteDemand is the citywide aggregate population taste distribution,
	// defaulting to Config.DefaultTaste and overridable via
	// SetPopulationTaste (the pushed input for the missing census-enumeration
	// edge — see doc.go).
	tasteDemand TasteDistribution

	// events is the loaded/scheduled events calendar.
	events []Event

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[LeisureAPI]
}

// New constructs a LeisureAPI from a validated Config and a world seed
// (used for every counter-based hash draw — AC-14). correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted gate or scale. Dependencies are wired
// later via SetCitizens/SetTraffic/SetWellbeing.
func New(cfg Config, seed uint64, correlationID string) (*LeisureAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &LeisureAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		venues:        make(map[uint64]Venue),
		freshness:     make(map[uint64]map[uint64]float64),
		visits:        make(map[uint64]map[uint64]int64),
		overtime:      make(map[uint64]float64),
		tasteDemand:   cfg.DefaultTaste,
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *LeisureAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *LeisureAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency (the citizen record:
// personality, taste weights, life stage, employment).
func (a *LeisureAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := a.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.citizens = c
	return nil
}

// SetTraffic wires the engine.traffic commute/trip-generation surface
// (AC-2/AC-3/AC-6).
func (a *LeisureAPI) SetTraffic(t TrafficAPI) error {
	if err := a.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.traffic = t
	return nil
}

// SetWellbeing wires the engine.wellbeing LeisureFit-driver push target
// (AC-10).
func (a *LeisureAPI) SetWellbeing(w WellbeingAPI) error {
	if err := a.checkNotCopied("SetWellbeing"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wellbeing = w
	return nil
}

// AdvanceMonth advances the module's internal sim month, which seeds the
// deterministic patronage draw (AC-14). Wall-clock time is never read.
func (a *LeisureAPI) AdvanceMonth(correlationID string) error {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.month++
	return nil
}

// Month returns the module's internal sim month.
func (a *LeisureAPI) Month(correlationID string) int64 {
	_ = a.checkNotCopied("Month")
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.month
}
