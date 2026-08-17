package refuse

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// RefuseServiceID is the ServiceID this package registers against
// engine.services for the refuse service (kind ServiceGarbage, US-4). The
// refuse service's funding gates round reliability (AC-6's depot-
// underfunding cause) and its Public Service Pie refuseCrews benchmark
// ratio drives the truck count (AC-6's truck-shortage cause) through the
// generic funding→quality path.
const RefuseServiceID services.ServiceID = "refuse"

// RegisterDepot registers a depot ID that collection rounds can be
// scheduled against (AC-13). Registering is idempotent.
func (r *RefuseAPI) RegisterDepot(depotID string) error {
	if err := r.checkNotCopied("RegisterDepot"); err != nil {
		return err
	}
	if depotID == "" {
		return errs.New(ErrUnknownDepot, r.correlationID, map[string]any{"depot": depotID})
	}
	r.mu.Lock()
	r.depots[depotID] = true
	r.mu.Unlock()
	return nil
}

// ScheduleRound schedules a collection round against a registered depot
// (AC-4). An unregistered depot is rejected with ErrUnknownDepot (AC-13),
// never a silently-created zero-value round. Re-scheduling an existing round
// (in flight or completed) is rejected with ErrInvalidOverride (AC-14),
// matching AutoOptimise/OverrideRound/ClearOverride — never silently
// overwriting a round and destroying its stranded in-transit tonnage, which
// would break AC-11's mass-conservation identity. The round's initial route
// is the given cell order; [AutoOptimise] re-derives the auto route.
func (r *RefuseAPI) ScheduleRound(roundID, depotID string, cellIDs []string) error {
	if err := r.checkNotCopied("ScheduleRound"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.depots[depotID] {
		return errs.New(ErrUnknownDepot, r.correlationID, map[string]any{"depot": depotID})
	}
	if _, exists := r.rounds[roundID]; exists {
		return errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	cells := append([]string(nil), cellIDs...)
	route := append([]string(nil), cellIDs...)
	sort.Strings(route) // auto route = ascending cell ID (deterministic)
	r.rounds[roundID] = &roundState{
		id:      roundID,
		depotID: depotID,
		cells:   cells,
		route:   route,
	}
	return nil
}

// AutoOptimise recomputes a round's optimal route (AC-5): the documented
// objective is minimising total truck distance, approximated at stub depth
// by sorting the round's cells into ascending cell-ID order — a
// deterministic, seed-free stand-in for a distance-optimising pass (GR#21).
// It returns the computed route. When the round has an active player
// override, the override keeps precedence (the auto route is computed but
// not applied until the override is cleared). A round that is unknown or
// has already completed is rejected with ErrInvalidOverride (AC-14) — never
// silently rewriting a finished round's route.
func (r *RefuseAPI) AutoOptimise(roundID string) ([]string, error) {
	if err := r.checkNotCopied("AutoOptimise"); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rd, ok := r.rounds[roundID]
	if !ok || rd.completed {
		return nil, errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	route := append([]string(nil), rd.cells...)
	sort.Strings(route)
	if !rd.overridden {
		rd.route = route
	}
	return route, nil
}

// OverrideRound applies a player override to a round's route (AC-5): it
// takes precedence over the auto-optimiser for that round until
// [ClearOverride] is called. A round that is unknown or has already
// completed is rejected with ErrInvalidOverride (AC-14) — never silently
// overriding a finished round.
func (r *RefuseAPI) OverrideRound(roundID string, route []string) error {
	if err := r.checkNotCopied("OverrideRound"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rd, ok := r.rounds[roundID]
	if !ok || rd.completed {
		return errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	rd.overridden = true
	rd.overrideRoute = append([]string(nil), route...)
	rd.route = append([]string(nil), route...)
	return nil
}

// ClearOverride clears a round's player override (AC-5), restoring the
// auto-optimised route. A round that is unknown or has already completed is
// rejected with ErrInvalidOverride (AC-14) — never silently clearing a
// finished round's history.
func (r *RefuseAPI) ClearOverride(roundID string) error {
	if err := r.checkNotCopied("ClearOverride"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rd, ok := r.rounds[roundID]
	if !ok || rd.completed {
		return errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	rd.overridden = false
	rd.overrideRoute = nil
	route := append([]string(nil), rd.cells...)
	sort.Strings(route)
	rd.route = route
	return nil
}

// Round returns the read-only snapshot of a scheduled round. An unknown
// round is rejected with ErrInvalidOverride.
func (r *RefuseAPI) Round(roundID string) (Round, error) {
	if err := r.checkNotCopied("Round"); err != nil {
		return Round{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rd, ok := r.rounds[roundID]
	if !ok {
		return Round{}, errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	return snapshotRound(rd), nil
}

func snapshotRound(rd *roundState) Round {
	return Round{
		ID:                 rd.id,
		DepotID:            rd.depotID,
		Route:              append([]string(nil), rd.route...),
		Overridden:         rd.overridden,
		Completed:          rd.completed,
		InTransitGeneral:   rd.inTransit[0],
		InTransitRecycling: rd.inTransit[1],
		InTransitFood:      rd.inTransit[2],
	}
}

// SetTrucks sets the number of refuse trucks available this tick — the
// refuse-crew-derived truck count the composition root feeds from
// engine.services' refuseCrews benchmark (US-4). A negative count is
// clamped to zero (a negative truck count has the unambiguous meaning "no
// trucks").
func (r *RefuseAPI) SetTrucks(n int64) error {
	if err := r.checkNotCopied("SetTrucks"); err != nil {
		return err
	}
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	r.trucksAvailable = n
	r.mu.Unlock()
	return nil
}

// SetStrike activates (or clears) a strike event at a depot (AC-6's strike
// miss cause). An unregistered depot is rejected with ErrUnknownDepot.
func (r *RefuseAPI) SetStrike(depotID string, active bool) error {
	if err := r.checkNotCopied("SetStrike"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.depots[depotID] {
		return errs.New(ErrUnknownDepot, r.correlationID, map[string]any{"depot": depotID})
	}
	r.strike[depotID] = active
	return nil
}

// SetFunding sets the refuse service's funding level (US-4), validated in
// [0,1] at this package's boundary (ErrInvalidFunding, AC-14) before
// delegating to engine.services' generic SetFunding.
func (r *RefuseAPI) SetFunding(level float64) error {
	if err := r.requireWired("SetFunding"); err != nil {
		return err
	}
	if !num.IsFinite(level) || level < 0 || level > 1 {
		return errs.New(ErrInvalidFunding, r.correlationID, map[string]any{"level": level})
	}
	return r.snapshotDeps().services.SetFunding(RefuseServiceID, level)
}

// registerService ensures the refuse service instance is registered against
// engine.services (idempotent across re-wires). sv is the services pointer
// already stored by Wire — never re-read from r.services, which would race a
// concurrent Wire (AC-17).
func (r *RefuseAPI) registerService(sv *services.ServicesAPI) error {
	spec := services.ServiceSpec{
		ID:   RefuseServiceID,
		Kind: services.ServiceGarbage,
	}
	err := sv.RegisterService(spec)
	if err == nil {
		return nil
	}
	if code, ok := err.(*errs.E); ok && code.Code == services.ErrDuplicateService {
		return nil // already registered — idempotent
	}
	return err
}

// RunRound drives one collection round (AC-4/AC-5/AC-6): it evaluates the
// four documented miss causes, collects the route cells into the round's
// in-transit tonnage, then delivers that tonnage to the disposal sites
// through engine.logistics' movement machinery (throughput-bounded, so a
// saturated movement queues to the next tick — the stub-depth analogue of
// engine.logistics.md AC-4's next-day junction queue). The round is marked
// completed once it fully delivers (or fully misses); a round left with a
// throughput shortfall stays open so a later RunRound drains the stranded
// in-transit tonnage.
func (r *RefuseAPI) RunRound(roundID string) (RoundResult, error) {
	if err := r.requireWired("RunRound"); err != nil {
		return RoundResult{}, err
	}
	deps := r.snapshotDeps()

	// Claim the round under the lock: a round may only be driven by one
	// RunRound at a time. The completed/in-progress check and the claim are
	// ONE critical section, so a second concurrent RunRound on the same round
	// is rejected (ErrInvalidOverride) rather than re-collecting and
	// re-delivering the same tonnage (AC-17/AC-11). The round stays claimed
	// until the end of the call, even on an error return.
	r.mu.Lock()
	rd, ok := r.rounds[roundID]
	if !ok {
		r.mu.Unlock()
		return RoundResult{}, errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	if rd.completed || rd.active {
		r.mu.Unlock()
		return RoundResult{}, errs.New(ErrInvalidOverride, r.correlationID, map[string]any{"round": roundID})
	}
	rd.active = true
	depotID := rd.depotID
	route := append([]string(nil), rd.route...)
	strike := r.strike[depotID]
	fundingThreshold := r.cfg.Funding.FundingThreshold
	truckCapacity := r.cfg.Trucks.TruckCapacityKg
	trucksAvailable := r.trucksAvailable
	r.mu.Unlock()

	// Release the claim no matter how this call exits.
	defer func() {
		r.mu.Lock()
		rd.active = false
		r.mu.Unlock()
	}()

	// --- miss causes (AC-6), evaluated in documented priority order ---
	var cause MissCause

	if strike {
		cause = MissStrike
	} else {
		funding, ferr := deps.services.FundingLevel(RefuseServiceID)
		if ferr != nil {
			// A funding read that fails must not be silently skipped: it would
			// treat the round as funded and proceed when we cannot actually
			// know. Propagate the registry-sourced error (GR#1/GR#7).
			return RoundResult{}, ferr
		}
		if funding < fundingThreshold {
			cause = MissDepotUnderfunding
		}
	}

	// Truck shortage: does the available truck count cover the route's
	// collectable tonnage at the data-sourced per-truck capacity?
	if cause == "" {
		var collectable int64
		r.mu.RLock()
		for _, id := range route {
			if c, ok := r.cells[id]; ok {
				for i := 0; i < 3; i++ {
					collectable = num.SatAdd(collectable, num.SatAdd(c.levels[i], c.overflow[i]))
				}
			}
		}
		r.mu.RUnlock()
		trucksNeeded := int64(1)
		if collectable > truckCapacity {
			trucksNeeded = num.ClampInt64FromFloat(math.Ceil(float64(collectable) / float64(truckCapacity)))
		}
		if trucksAvailable < trucksNeeded {
			cause = MissTruckShortage
		}
	}

	// A full miss leaves every route cell uncollected and records the cause
	// on their overflow state (AC-6).
	if cause == MissStrike || cause == MissDepotUnderfunding || cause == MissTruckShortage {
		r.mu.Lock()
		for _, id := range route {
			if c, ok := r.cells[id]; ok {
				cc := cause
				c.missCause = &cc
			}
		}
		rd.completed = true
		r.mu.Unlock()
		return RoundResult{RoundID: roundID, Missed: true, Cause: &cause}, nil
	}

	// --- collect (AC-4): move each route cell's bins into the round ---
	r.mu.Lock()
	for _, id := range route {
		if c, ok := r.cells[id]; ok {
			for i := 0; i < 3; i++ {
				taken := num.SatAdd(c.levels[i], c.overflow[i])
				rd.inTransit[i] = num.SatAdd(rd.inTransit[i], taken)
				c.levels[i] = 0
				c.overflow[i] = 0
				c.missCause = nil
			}
		}
	}
	collected := rd.inTransit
	generalSiteID := r.generalSiteID
	compostSiteID := r.compostSiteID
	r.mu.Unlock()

	// --- deliver (AC-4): through engine.logistics' movement machinery ---
	// general → general site, food → compost site, recycling → resale.
	res := RoundResult{
		RoundID:            roundID,
		CollectedGeneral:   collected[0],
		CollectedRecycling: collected[1],
		CollectedFood:      collected[2],
	}

	// Recycling resale: no disposal site needed; delivered = collected.
	res.DeliveredRecycling = collected[1]
	res.ShortfallRecycling = 0
	r.mu.Lock()
	rd.inTransit[1] = 0
	r.collected[1] = num.SatAdd(r.collected[1], collected[1])
	r.mu.Unlock()

	// gridlocked is a THROUGHPUT shortfall only: the round collected tonnage
	// and a configured disposal site could not accept all of it this tick
	// (movement saturated or errored). That round stays open so a later
	// RunRound drains the stranded in-transit tonnage. A stream with NO site
	// configured is NOT gridlock — its tonnage waits in transit but the round
	// is still complete (collection happened; only the throughput-limited
	// movement defers completion, AC-4's next-day-queue analogue).
	gridlocked := false
	if generalSiteID != "" {
		d, short := r.deliverToSite(deps.logistics, generalSiteID, 0, collected[0])
		res.DeliveredGeneral = d
		res.ShortfallGeneral = short
		if short > 0 {
			gridlocked = true
		}
	} else {
		res.ShortfallGeneral = collected[0] // no site: stays in transit (not gridlock)
	}
	if compostSiteID != "" {
		d, short := r.deliverToSite(deps.logistics, compostSiteID, 2, collected[2])
		res.DeliveredFood = d
		res.ShortfallFood = short
		if short > 0 {
			gridlocked = true
		}
	} else {
		res.ShortfallFood = collected[2] // no compost site: stays in transit (not gridlock)
	}

	r.mu.Lock()
	rd.inTransit[0] = res.ShortfallGeneral
	rd.inTransit[2] = res.ShortfallFood
	// A round with a remaining THROUGHPUT shortfall (gridlock) stays open
	// (not completed) so a later RunRound drains the stranded tonnage through
	// the same throughput-bounded machinery. Marking it completed here would
	// strand the shortfall with no re-delivery path.
	rd.completed = !gridlocked
	if gridlocked {
		for _, id := range route {
			if c, ok := r.cells[id]; ok {
				cc := MissGridlockDelay
				c.missCause = &cc
			}
		}
	}
	r.mu.Unlock()

	if gridlocked {
		res.Missed = true
		cause = MissGridlockDelay
		res.Cause = &cause
	}
	return res, nil
}

// deliverToSite moves stream idx's tonnage to a disposal site's backlog
// through engine.logistics' throughput machinery (AC-4). It returns the
// delivered amount and the shortfall (which stays in transit for the next
// tick — the stub-depth analogue of a saturated junction queue). The
// movement's capacity is engine.logistics' own Deliverable throughput
// bound, never a refuse-only truck model; the delivered tonnage lands in
// the site's refuse-owned backlog, from which [ProcessDisposal] routes it
// into the terminal form (the landfill's logistics stock fill, AC-8).
func (r *RefuseAPI) deliverToSite(lg *logistics.LogisticsAPI, siteID string, streamIdx int, tonnage int64) (int64, int64) {
	if tonnage <= 0 {
		return 0, 0
	}
	// A reclaimed landfill is no longer a valid disposal target (AC-8): the
	// delivery is rejected and the tonnage stays in transit rather than
	// silently backlogging into a closed site, where ProcessDisposal would
	// refuse to process it and the tonnage would be stuck. Matches
	// RouteGeneralToSite's reclaimed-site rejection. The reclaimed flag is
	// snapshotted under RLock (AC-17): CapAndReclaim writes it under Lock.
	r.mu.RLock()
	site, ok := r.sites[siteID]
	reclaimed := false
	if ok {
		reclaimed = site.reclaimed
	}
	r.mu.RUnlock()
	if !ok || reclaimed {
		return 0, tonnage
	}
	// The movement is a logistics movement of the Waste commodity; its
	// per-tick capacity is logistics' throughput/shortfall figure (the
	// stub-depth analogue of the junction slot ledger). lg is the caller's
	// dependency snapshot (AC-17).
	delivery, err := lg.Deliverable(siteID, market.Waste, tonnage)
	if err != nil {
		return 0, tonnage // can't move this tick — stays in transit
	}
	delivered := delivery.Delivered
	if delivered < 0 {
		delivered = 0
	}
	if delivered > tonnage {
		delivered = tonnage
	}
	shortfall := num.SatSub(tonnage, delivered)

	r.mu.Lock()
	if site, ok := r.sites[siteID]; ok {
		site.backlog[streamIdx] = num.SatAdd(site.backlog[streamIdx], delivered)
	}
	r.mu.Unlock()
	return delivered, shortfall
}
