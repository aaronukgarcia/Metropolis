package deathservices

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// crematory.go implements AC-5/AC-6/AC-15: cremation as the costed,
// plot-free disposal alternative, bounded by a data-sourced daily
// throughput (spec seed 12/d), with its per-body cost routed through the
// registered engine.services outbound edge (never a locally-invented
// ledger, GR#3).

// CrematoriumServiceID is the services.ServiceID this package registers
// against engine.services (AC-6), of [services.ServiceDeathcare] kind --
// the kind constant already reserved in engine.services' catalogue for
// exactly this module (internal/engine/services/kind.go), mirroring
// engine.refuse's RefuseServiceID/ServiceGarbage precedent.
const CrematoriumServiceID services.ServiceID = "deathservices-crematorium"

// crematoriumState is one registered crematorium's per-day cremation
// counter (AC-5(c): more than the daily throughput queues, never exceeds).
type crematoriumState struct {
	id                  string
	lastDay             int64
	cremToday           int64
	cumulativeStaffLoad float64 // fed to engine.services' StaffingNeed, AC-6
}

// RegisterCrematorium registers a crematorium building (consumed through
// the engine.build catalogue edge in the live composition). Idempotent.
func (d *DeathServicesAPI) RegisterCrematorium(crematoriumID string, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "RegisterCrematorium"); err != nil {
		return err
	}
	if crematoriumID == "" {
		return errs.New(ErrUnknownBuildingType, correlationID, map[string]any{"buildingType": "crematorium", "crematoriumId": crematoriumID})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.crematoria[crematoriumID]; exists {
		return nil
	}
	d.crematoria[crematoriumID] = &crematoriumState{id: crematoriumID, lastDay: -1}
	return d.registerCrematoriumServiceLocked(correlationID)
}

// registerCrematoriumServiceLocked idempotently registers
// CrematoriumServiceID against the wired engine.services instance (AC-6).
// A no-op when unwired (see the DeathServicesAPI.servicesAPI field doc) --
// AC-5's own accessors (cost/throughput) work with no engine.services
// dependency at all; only the cross-module posting degrades.
func (d *DeathServicesAPI) registerCrematoriumServiceLocked(correlationID string) error {
	// REDUNDANT with RegisterCrematorium's own guard (the only call site) --
	// kept anyway, matching this package's double-check convention (see
	// api.go's awaitingSortedLocked doc for the identical astgate-blind-spot
	// reasoning).
	if err := d.checkNotCopied(correlationID, "registerCrematoriumServiceLocked"); err != nil {
		return err
	}
	if d.servicesAPI == nil {
		return nil
	}
	spec := services.ServiceSpec{ID: CrematoriumServiceID, Kind: services.ServiceDeathcare}
	err := d.servicesAPI.RegisterService(spec)
	if err == nil {
		return nil
	}
	if e, ok := err.(*errs.E); ok && e.Code == services.ErrDuplicateService {
		return nil // already registered -- idempotent
	}
	return err
}

// UnregisterCrematorium removes a registered crematorium (BUG-734: the
// bulldoze seam DeathServicesAPI never had). Rejects an unknown id with
// [ErrUnknownCrematorium] — not idempotent-as-success, the same stance
// [DeathServicesAPI.UnregisterCemetery] takes and for the same reason
// (GR#1: unregistering an id you do not hold a live building for is a
// programming error worth surfacing, not silently swallowing).
//
// Semantics:
//
//   - bodies already cremated at crematoriumID are UNTOUCHED — a Body record
//     only ever carries crematoriumID BY VALUE (Cremate's commit loop sets
//     it once, on the terminal BodyCremated transition) and is never looked
//     up back through the live crematoriumState, so removing the
//     registration cannot retroactively un-cremate anyone or disturb AC-14's
//     conservation identity;
//   - future cremation stops — a subsequent Cremate call against the removed
//     id gets [ErrUnknownCrematorium], identical to naming an id that was
//     never registered (AC-5/AC-17);
//   - capacity accounting adjusts automatically — draincapacity.go's
//     MonthlyDrainCapacity multiplies the per-body daily throughput by
//     len(d.crematoria), so a removed crematorium stops contributing to
//     city-wide cremation capacity the moment this call returns.
//
// CrematoriumServiceID (AC-6's engine.services registration) is a SINGLE
// shared id across every crematorium instance (crematory.go's own doc:
// "the kind constant... reserved... for exactly this module" — it is not
// per-instance), so it is only deregistered from engine.services once the
// LAST crematorium is removed; deregistering it while other crematoria
// still stand would zero out the whole city's deathcare staffing/coverage
// contribution for buildings that are still standing. The engine.services
// call is best-effort exactly like build.go's own demolish mirror
// (SubmitDemolishCommand's doc): a nil/never-wired servicesAPI, or the
// service already gone (ErrServiceNotRegistered — e.g. a prior
// UnregisterCrematorium already cleared it, or engine.services was reset
// independently), is not an error here — this call's own contract is about
// deathservices' registration bookkeeping, not engine.services' internal
// state machine.
func (d *DeathServicesAPI) UnregisterCrematorium(crematoriumID string, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "UnregisterCrematorium"); err != nil {
		return err
	}
	if crematoriumID == "" {
		return errs.New(ErrUnknownCrematorium, correlationID, map[string]any{"crematoriumId": crematoriumID})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.crematoria[crematoriumID]; !ok {
		return errs.New(ErrUnknownCrematorium, correlationID, map[string]any{"crematoriumId": crematoriumID})
	}
	delete(d.crematoria, crematoriumID)
	if len(d.crematoria) == 0 && d.servicesAPI != nil {
		if err := d.servicesAPI.UnregisterService(CrematoriumServiceID); err != nil {
			if e, ok := err.(*errs.E); !ok || e.Code != services.ErrServiceNotRegistered {
				return err
			}
		}
	}
	return nil
}

// DailyThroughput returns the configured crematorium daily cremation cap
// (AC-5, spec seed 12/d).
func (d *DeathServicesAPI) DailyThroughput(correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "DailyThroughput"); err != nil {
		return 0, err
	}
	return d.cfg.CremationDailyThroughputPerBody(), nil
}

// PerBodyCostMicropounds returns the configured per-body cremation cost in
// micro-pounds (AC-5, placeholder per line 543's unspecified "£/service").
func (d *DeathServicesAPI) PerBodyCostMicropounds(correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "PerBodyCostMicropounds"); err != nil {
		return 0, err
	}
	return d.cfg.CremationCostPerBodyMicropounds(), nil
}

// RemainingDailyThroughput returns crematoriumID's remaining cremation
// capacity for day (BUG-720 round F1 perf fix): DailyThroughput() minus
// whatever that crematorium has already cremated on day, or the FULL
// DailyThroughput() when day is not the crematorium's last-seen day (the
// counter has not reset yet, so nothing has been consumed against day's
// allowance). A PURE, non-mutating read — unlike Cremate, this never
// advances lastDay/cremToday, so calling it any number of times before
// (or without ever) calling Cremate has zero effect on the module's own
// state.
//
// This exists so a caller (compose's daily run loop) can size its
// SUBMITTED batch to what a crematorium can actually use TODAY before
// calling [DeathServicesAPI.Cremate] — Cremate's own admission loop calls
// [DeathServicesAPI.awaitingAheadCountLocked] (O(len(bodies))) once per
// SUBMITTED id, not once per id it actually cremates, so handing it the
// entire (often much larger) Awaiting backlog every day makes that loop
// cost O(backlog x totalBodies) for a result that is capped at
// DailyThroughput regardless (measured: 109ms/2.65s/17.08s per month at
// backlog 500/2000/5000 to cremate the same ~360 bodies either way).
// Truncating the submitted batch to this method's return value first
// bounds Cremate's own loop to O(throughput x totalBodies), independent
// of backlog size.
func (d *DeathServicesAPI) RemainingDailyThroughput(crematoriumID string, day int64, correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "RemainingDailyThroughput"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cr, ok := d.crematoria[crematoriumID]
	if !ok {
		return 0, errs.New(ErrUnknownCrematorium, correlationID, map[string]any{"crematoriumId": crematoriumID})
	}
	throughput := d.cfg.CremationDailyThroughputPerBody()
	if day != cr.lastDay {
		return throughput, nil
	}
	remaining := throughput - cr.cremToday
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// Cremate attempts to cremate bodyIDs at crematoriumID on the given day
// index (a caller-supplied simulation day counter, never wall-clock,
// AC-19). It processes bodies in the given order up to the crematorium's
// remaining daily throughput for that day (AC-5(c)): the excess is left
// Awaiting, not rejected -- calling Cremate again on a later day with
// whatever from that same batch is STILL Awaiting (queried via
// [DeathServicesAPI.AwaitingBacklog]/[DeathServicesAPI.Body]) drains them,
// exactly the documented "queues the excess rather than exceeding it"
// behaviour. Re-offering an id that a PRIOR call already cremated is
// rejected with [ErrBodyAlreadyHandled] (AC-15) rather than silently
// skipped -- the caller is expected to track its own still-awaiting subset
// (backlog), not blindly resubmit an entire original batch every day.
//
// A day index different from the crematorium's last-seen day resets its
// counter to zero (a new day's throughput, AC-5's "12/d" being a PER-DAY
// cap, not a lifetime one). Each successfully cremated body consumes zero
// plots (AC-5(a)) and posts its per-body cost through the registered
// engine.services edge (AC-6) via [registerCrematoriumServiceLocked]'s
// service + [services.ServicesAPI.UpdateStaffing] -- cremation demand is
// converted to a staffing-need delta (bodies x a data-independent 1
// staff-unit/body coarse factor documented here, since data/deathservices.
// json owns the £ cost placeholder and data/services.json owns the £/staff
// wage rate; multiplying the two is this integration's whole job, not a
// duplicate ledger) so [services.ServicesAPI.GrossWageCost] reflects the
// cremation load. A body already terminal is rejected with
// [ErrBodyAlreadyHandled] and is NOT counted against the day's throughput.
//
// Returns the cremated ids (subset of bodyIDs, in input order) and the
// total cost posted this call, in micro-pounds (AC-5's M0-ENG §1.2 fixed-
// point scale -- >0 whenever at least one body was cremated). This
// package deliberately returns a plain int64 micro-pounds value rather
// than engine.finance's Money type: engine.finance is NOT a registered
// code.json outbound edge for engine.deathservices (only engine.services
// is), so taking a direct dependency on it would be exactly the
// hand-invented, unregistered dependency GR#25 fail-closes on. Any caller
// that needs an engine.finance.Money value converts at its own boundary
// (int64 micro-pounds is Money's own underlying representation).
//
// H1 fix (round-2, AC-5c/AC-6/AC-17): this whole call is one atomic
// transaction under a SINGLE continuous d.mu hold. Pass 1 VALIDATES every
// id in bodyIDs (exists, not already terminal) before mutating ANYTHING --
// the prior revision flipped earlier ids to BodyCremated inline, then hit
// an unknown/already-terminal id mid-batch and returned early WITHOUT
// crediting cr.cremToday or the engine.services cost post, so those
// already-flipped bodies were cremated "for free" and invisibly to the
// daily cap (attack_round_test.go's
// TestAttackCremateMidBatchErrorLosesCostAndThroughput). Any invalid id
// anywhere in bodyIDs now aborts the ENTIRE call with NO state change at
// all (cremated=nil, cost=0) -- never a partial commit. Once validated,
// the daily-cap truncation (AC-5c's legitimate "queue the excess" case,
// NOT an error) is computed, the engine.services cost post is attempted
// FIRST (still under d.mu, still before any body's state changes), and
// only once that succeeds do bodies flip to BodyCremated and the
// crematorium's counters advance -- (a) the whole batch-within-cap and
// (b) the services post succeeding are both required before any single
// body is marked cremated.
func (d *DeathServicesAPI) Cremate(bodyIDs []uint64, crematoriumID string, day int64, correlationID string) ([]uint64, int64, error) {
	if err := d.checkNotCopied(correlationID, "Cremate"); err != nil {
		return nil, 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	cr, ok := d.crematoria[crematoriumID]
	if !ok {
		return nil, 0, errs.New(ErrUnknownCrematorium, correlationID, map[string]any{"crematoriumId": crematoriumID})
	}

	// Pass 1: DEDUPLICATE + validate the WHOLE batch before any mutation.
	// Round-3 N1: without the seen-set, Cremate([1,1]) passed validation
	// twice, ranked identically twice, and charged/counted ONE body twice
	// (double-billed through engine.services, cremToday inflated by 2) --
	// the same duplicate-in-batch shape H4 already closed in Intake, with
	// the same policy (b): later occurrences of a repeated id are skipped,
	// the first is kept.
	seen := make(map[uint64]bool, len(bodyIDs))
	unique := make([]uint64, 0, len(bodyIDs))
	for _, id := range bodyIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		b, ok := d.bodies[id]
		if !ok {
			return nil, 0, errs.New(ErrUnknownBody, correlationID, map[string]any{"bodyId": id})
		}
		if b.state != BodyAwaiting && b.state != BodyEnRoute {
			return nil, 0, errs.New(ErrBodyAlreadyHandled, correlationID, map[string]any{"bodyId": id, "state": string(b.state)})
		}
		unique = append(unique, id)
	}

	if day != cr.lastDay {
		cr.lastDay = day
		cr.cremToday = 0
	}
	throughput := d.cfg.CremationDailyThroughputPerBody()
	remaining := throughput - cr.cremToday
	if remaining < 0 {
		remaining = 0
	}

	// H6 fix (round-2, AC-18): admission is decided PER ID by
	// [DeathServicesAPI.awaitingAheadCountLocked] against `remaining`,
	// the SAME deterministic, order-independent rule buryLocked's plot
	// admission gate uses -- NOT "first n entries in bodyIDs' own array
	// order". A single caller submitting an already-sorted batch (the
	// AwaitingSorted-fed common case) gets the identical result either
	// way; the difference only matters when SEPARATE concurrent Cremate
	// calls each submit disjoint subsets competing for the SAME
	// crematorium's daily cap, where positional truncation would let
	// whichever call's goroutine reached d.mu first win the day's slots
	// (the same class of bug ATTACK 8 found in Bury's plot admission,
	// fixed here for the identical reason).
	toCremate := make([]uint64, 0, len(unique))
	for _, id := range unique {
		b := d.bodies[id]
		ahead := d.awaitingAheadCountLocked(b.deathMonth, id, correlationID)
		if ahead < remaining {
			toCremate = append(toCremate, id)
		}
	}

	n := int64(len(toCremate))
	costPerBody := d.cfg.CremationCostPerBodyMicropounds()
	totalCost := costPerBody * n
	newStaffLoad := cr.cumulativeStaffLoad + float64(n)

	// (b) AC-6: the engine.services cost post must SUCCEED before any body
	// flips to BodyCremated -- called here, still under d.mu (services
	// never calls back into deathservices, so no lock-order hazard), so a
	// failed post leaves EVERY body untouched, not just the ones after the
	// failure point.
	if d.servicesAPI != nil && n > 0 {
		if err := d.servicesAPI.UpdateStaffing(CrematoriumServiceID, newStaffLoad); err != nil {
			return nil, 0, err
		}
	}

	// Commit: (a) within-cap and (b) services post both satisfied --
	// this section cannot fail, so it is genuinely all-or-nothing.
	for _, id := range toCremate {
		b := d.bodies[id]
		b.state = BodyCremated
		b.crematoriumID = crematoriumID
	}
	cr.cremToday += n
	cr.cumulativeStaffLoad = newStaffLoad

	cremated := append([]uint64(nil), toCremate...)
	return cremated, totalCost, nil
}
