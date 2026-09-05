package deathservices

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// hearse.go implements AC-7/AC-8/AC-9: hearse transport bounded by a
// coarse monthly throughput budget (never per-vehicle routing, AC-9), one
// body per trip (AC-7), routed through the registered engine.logistics
// movement surface (AC-8) so a hearse trip is subject to the SAME
// junction-saturation congestion any logistics round would be -- exactly
// the pattern internal/engine/refuse/round.go's deliverToSite already
// established for refuse collection rounds (§25, the structural analog
// named in docs/planning/acceptance/MOD-083-inc1.md).
//
// AC-8 EDGE HISTORY (B2/round-3): the round-2 rework REMOVED the
// engine.logistics Deliverable call because every exported LogisticsAPI
// throughput method takes a market.CommodityType and engine.market was
// not then a registered edge (GR#25 fail-closed, correctly). The lead
// registered engine.deathservices -> engine.market in the SSOT on the
// round-3 attacker's independently-verified evidence (commit 6a4e210,
// master-plan-v2.1.json regenerated), so the call below is RESTORED:
// hearse movement is expressed as the shared market.Waste commodity's
// Deliverable throughput figure, the same generic "movement capacity"
// channel refuse's own precedent uses -- engine.logistics has no dedicated
// "bodies" commodity, and both edges (engine.logistics AND engine.market)
// are now registered, mirroring engine.refuse's identical pair.

// hearseState tracks the current month's hearse throughput consumption.
type hearseState struct {
	lastMonth     int64
	usedThisMonth int64
}

// HearseTripCapacity returns the fixed per-trip body capacity for the
// NORMAL (non-dispensation) hearse fleet: always exactly 1 (AC-7). This is
// a constant, not data-sourced, because "one body per trip" is the
// structural definition of a hearse (as opposed to the multi-body vans/
// trucks dispensation.go introduces) -- there is no balance number here to
// disclose.
func (d *DeathServicesAPI) HearseTripCapacity(correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "HearseTripCapacity"); err != nil {
		return 0, err
	}
	return 1, nil
}

// HearseMonthlyBudget returns the configured monthly hearse throughput
// budget in bodies/month (AC-7/AC-9, placeholder).
func (d *DeathServicesAPI) HearseMonthlyBudget(correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "HearseMonthlyBudget"); err != nil {
		return 0, err
	}
	return d.cfg.HearseMonthlyTransportBudget(), nil
}

// resetMonthLocked rolls the hearse's used-this-month counter to zero when
// month ADVANCES past the last-seen month. Caller must hold d.mu.
//
// BUG-720 round F4 fix: this used to reset on ANY change (month !=
// h.lastMonth), including month going BACKWARDS relative to the persisted
// watermark. A plain [DeathServicesAPI]-owning Composition.Load restores
// this module's state (including lastMonth/usedThisMonth, participant.go)
// but leaves the ENGINE's own clock at tick/month 0 (save_wire.go's
// documented snapshot-not-tick-continuous contract, unlike LoadAt) — so
// the very first post-Load day called this with month=0, which is almost
// always LESS than the persisted lastMonth, and the old `!=` check treated
// that as "a new month started", handing back the FULL monthly budget for
// free (a save-scum exploit: save mid-month after spending the budget,
// reload, get it back). Comparing with `>` instead of `!=` means a month
// number that goes backwards is no longer treated as a new month at all —
// the persisted usedThisMonth stays in force until the ENGINE's own month
// counter genuinely climbs back past the watermark it was saved at, which
// is the only way a budget can be legitimately exhausted-then-fresh again.
// For LoadAt (tick-continuous restore, month only ever increases) this is
// BYTE-IDENTICAL to the old `!=` check: on any monotonically
// non-decreasing sequence, `month != lastMonth` is true exactly when
// `month > lastMonth` is (month < lastMonth never occurs), so nothing
// observable changes for that path (proven by
// TestAttackBUG720_HookOrderStableAcrossSaveBoundary, which exercises
// exactly that continuation).
func (h *hearseState) resetMonthLocked(month int64) {
	if month > h.lastMonth {
		h.lastMonth = month
		h.usedThisMonth = 0
	}
}

// RemainingHearseBudget returns the hearse fleet's remaining monthly
// transport budget for month (BUG-720 round F1 perf fix, mirroring
// [DeathServicesAPI.RemainingDailyThroughput]'s identical purpose for
// cremation): HearseMonthlyBudget() minus whatever has already been
// consumed against month, or the FULL budget when month is not the
// hearse's last-seen month. A PURE, non-mutating read — it never calls
// resetMonthLocked itself, so a caller may query it any number of times
// (including for a month that has not "started" from this module's own
// perspective yet) with zero effect on state; RunHearseTransport is the
// only thing that ever advances usedThisMonth.
//
// Used by compose's daily run loop to size its submitted batch to what
// the hearse fleet can actually use this month BEFORE calling
// [DeathServicesAPI.RunHearseTransport] — bounding buryLocked's own
// per-submitted-id admission-gate cost
// ([DeathServicesAPI.awaitingAheadCountLocked], O(len(bodies))) to
// O(remaining budget) rather than O(backlog).
func (d *DeathServicesAPI) RemainingHearseBudget(month int64, correlationID string) (int64, error) {
	if err := d.checkNotCopied(correlationID, "RemainingHearseBudget"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	budget := d.cfg.HearseMonthlyTransportBudget()
	if month > d.hearse.lastMonth {
		// A genuinely new month (from resetMonthLocked's own "advances
		// past" definition, F4's fix) has not consumed anything yet.
		return budget, nil
	}
	// month == lastMonth (the common case) OR month < lastMonth (F4's
	// reverted-month case, e.g. the first tick after a plain Load) both
	// read the SAME persisted bucket resetMonthLocked would not touch —
	// whatever has already been spent against it stays spent.
	remaining := budget - d.hearse.usedThisMonth
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// RunHearseTransport attempts to move up to len(bodyIDs) Awaiting bodies
// from the awaiting backlog to Buried-via-cemeteryID this month, ONE BODY
// PER TRIP (AC-7), bounded by BOTH:
//
//  1. the remaining monthly hearse budget (AC-7/AC-9's coarse aggregate,
//     never a per-tick/per-vehicle schedule), and
//  2. engine.logistics' Deliverable throughput for the destination when a
//     LogisticsAPI is wired (AC-8, restored round-3 -- the same
//     junction-saturation congestion any logistics round faces; a
//     congested logistics state measurably reduces this month's hearse
//     throughput below the budget, see TestHearseCongestionDelaysTrips).
//
// H3 (round-2) + N3 (round-3) transactionality: the ENTIRE call runs
// under a SINGLE continuous d.mu hold, in the same validate-then-commit
// shape Cremate/Dispense use.
//
//   - Pass 1 DEDUPLICATES the batch (a repeated id is counted once, its
//     later occurrences skipped -- H4's intake policy (b), which is also
//     what makes pass 2 abort-free: without dedup, a duplicate's second
//     burial attempt would hit ErrBodyAlreadyHandled MID-COMMIT) and
//     VALIDATES every unique id (exists, not already terminal) BEFORE any
//     mutation. Any invalid id aborts the whole call with transported=nil
//     and NO state change -- no body buried, no budget consumed (round-3
//     N3: the prior revision returned from inside the commit loop before
//     `usedThisMonth +=`, so bodies already buried by an aborted call had
//     consumed NO budget -- the same free-disposal shape H1 closed in
//     Cremate).
//   - Pass 2 commits: after validation, buryLocked can only fail with the
//     no-plot/saturation family (which makes no state change and is
//     skipped, AC-4/H6); the defensive invariant-drift branch
//     (ErrPlotNotReusable) still commits the accounting for trips already
//     made BEFORE returning, so a burial is never left uncharged even on
//     that theoretically-unreachable path.
//
// bodyIDs beyond the effective bound are left Awaiting -- the
// unhandled-body backlog AC-7 requires persist across ticks/months, not
// be drained in one call. Each successfully transported body is
// immediately buried at cemeteryID this same month (inc1's coarse
// simplification -- see doc.go's "inc1 simplifications").
//
// Returns the transported ids (unique, input order) and the count left in
// backlog from this call's own input (len(bodyIDs) - len(transported)).
func (d *DeathServicesAPI) RunHearseTransport(bodyIDs []uint64, cemeteryID string, month int64, correlationID string) ([]uint64, int, error) {
	if err := d.checkNotCopied(correlationID, "RunHearseTransport"); err != nil {
		return nil, 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	d.hearse.resetMonthLocked(month)
	budget := d.cfg.HearseMonthlyTransportBudget()
	remaining := budget - d.hearse.usedThisMonth
	if remaining < 0 {
		remaining = 0
	}

	// Pass 1 (N3): deduplicate + validate the WHOLE batch before any
	// mutation.
	seen := make(map[uint64]bool, len(bodyIDs))
	working := make([]uint64, 0, len(bodyIDs))
	for _, id := range bodyIDs {
		if seen[id] {
			continue // duplicate occurrence: counted once (H4 policy (b))
		}
		seen[id] = true
		b, ok := d.bodies[id]
		if !ok {
			return nil, len(bodyIDs), errs.New(ErrUnknownBody, correlationID, map[string]any{"bodyId": id})
		}
		if b.state != BodyAwaiting && b.state != BodyEnRoute {
			return nil, len(bodyIDs), errs.New(ErrBodyAlreadyHandled, correlationID, map[string]any{"bodyId": id, "state": string(b.state)})
		}
		working = append(working, id)
	}

	// AC-8 (restored round-3): engine.logistics' own throughput bound for
	// this destination, consulted BEFORE any trip -- a saturated junction
	// delays a hearse trip exactly as it would any logistics round. The
	// movement is expressed as the shared market.Waste commodity, refuse's
	// own deliverToSite precedent (see file header). Called while holding
	// d.mu -- logistics takes only its own internal lock and never calls
	// back into deathservices, so there is no lock-order hazard (the same
	// reasoning as Cremate's in-lock engine.services post).
	tripCap := remaining
	if d.logisticsAPI != nil && remaining > 0 {
		delivery, err := d.logisticsAPI.Deliverable(cemeteryID, market.Waste, remaining)
		if err != nil {
			// Nothing has been mutated yet -- a failed logistics read
			// aborts cleanly with no state change.
			return nil, len(bodyIDs), err
		}
		if delivery.Delivered < tripCap {
			tripCap = delivery.Delivered
		}
		if tripCap < 0 {
			tripCap = 0
		}
	}

	// Pass 2: commit. Only the no-plot/saturation family can occur now
	// (no state change, skipped); anything else still charges the trips
	// already made before surfacing.
	transported := make([]uint64, 0, len(working))
	var commitErr error
	for _, id := range working {
		if int64(len(transported)) >= tripCap {
			break
		}
		if err := d.buryLocked(id, cemeteryID, month, correlationID); err != nil {
			if isNoPlotAvailable(err) {
				continue // AC-4/H6 saturation: skip, no trip consumed
			}
			commitErr = err
			break
		}
		transported = append(transported, id)
	}

	// N3: the accounting commit happens on EVERY exit path that made
	// trips, including the defensive commitErr break above.
	d.hearse.usedThisMonth += int64(len(transported))

	return transported, len(bodyIDs) - len(transported), commitErr
}
