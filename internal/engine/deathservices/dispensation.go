package deathservices

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// dispensation.go implements AC-10/AC-11/AC-12/AC-13: emergency
// dispensation mode, gated strictly on FEAT-087's weather-event signal
// (never a local weather recalculation, GR#3 -- see api.go's Intake/
// SetDispensationActive doc for exactly how that signal reaches this
// module), multi-body van/truck transport while active, and mandatory
// reversion (including a typed rejection of a post-event multi-body
// attempt) the instant the event ends.

// DispensationMode is the read-only snapshot [DeathServicesAPI.Dispensation]
// returns: whether dispensation is currently active, and (informational)
// the data-sourced van capacity/multiplier in effect while it is.
type DispensationMode struct {
	Active            bool
	VanBodyCapacity   int64
	ThroughputMonthly int64
}

// dispensationState is the module's live dispensation flag plus its own
// month-scoped throughput counter, tracked SEPARATELY from hearseState
// (AC-9/AC-11: dispensation throughput is its own budget, not drawn from
// the normal hearse budget, since the two operate under different rules --
// 24x7 vs business-hours, multi-body vs single-body).
type dispensationState struct {
	active        bool
	lastMonth     int64
	usedThisMonth int64
}

func (ds *dispensationState) resetMonthLocked(month int64) {
	if month != ds.lastMonth {
		ds.lastMonth = month
		ds.usedThisMonth = 0
	}
}

// Dispensation returns the current dispensation-mode snapshot (AC-10/
// AC-11).
func (d *DeathServicesAPI) Dispensation(correlationID string) (DispensationMode, error) {
	if err := d.checkNotCopied(correlationID, "Dispensation"); err != nil {
		return DispensationMode{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return DispensationMode{
		Active:            d.dispensation.active,
		VanBodyCapacity:   d.cfg.DispensationVanBodyCapacity(),
		ThroughputMonthly: d.cfg.DispensationMonthlyBudget(),
	}, nil
}

// Dispense transports and terminally disposes of bodyIDs via emergency
// dispensation (AC-11): while dispensation is active, up to the
// data-sourced van capacity may travel in ONE call (a "trip"), and total
// monthly dispensation throughput is bounded by the data-sourced
// multiplier over the normal hearse budget (AC-11's directional-only
// lift). Every dispensed body reaches the DISTINCT BodyDispensed terminal
// state (AC-14/AC-15) -- never counted as buried or cremated, keeping the
// conservation identity's six terms mutually exclusive.
//
// AC-12's reversion is enforced HERE, not just by the caller checking
// DispensationActive first: len(bodyIDs) > 1 while dispensation is NOT
// active is rejected outright with [ErrMultiBodyOutsideDispensation] and
// makes no state change (no body moved, no budget consumed) -- this is
// what makes "dispensation reverts" a real, structural guarantee rather
// than a convention callers might forget. A single-body call (len==1) is
// always permitted regardless of dispensation state (a van/truck carrying
// exactly one body is not distinguishable from a hearse trip's cardinality,
// so AC-12 has no reason to forbid it).
//
// H2 fix (round-2, AC-11/AC-17): the whole call is one atomic transaction
// under a SINGLE continuous d.mu hold. Pass 1 validates every id in the
// (possibly van-capacity-truncated) batch BEFORE any mutation -- the prior
// revision flipped earlier ids to BodyDispensed inline, then hit an
// unknown/already-terminal id mid-batch and returned early WITHOUT
// crediting usedThisMonth, so those already-flipped bodies bypassed the
// monthly budget entirely (attack_round_test.go's
// TestAttackDispenseMidBatchErrorSkipsMonthlyBudget). Any invalid id
// aborts the ENTIRE call with NO state change (dispensed=nil) -- the
// legitimate over-budget truncation (deferring the excess, not an error)
// is computed only AFTER validation passes, then committed together with
// its budget-counter increment in one step that cannot itself fail.
//
// M1 fix (round-2, AC-7): the INACTIVE single-body path used to be
// entirely unbounded ("remaining = len(bodyIDs)"), letting an unlimited
// number of bodies bypass every throughput cap
// (TestAttackInactiveSingleBodyDispenseBypassesAllBudgets). It now draws
// down the SAME monthly counter [DeathServicesAPI.RunHearseTransport]
// does (d.hearse.usedThisMonth, bounded by the identical
// HearseMonthlyTransportBudget) -- an inactive-mode single-body dispense
// IS, morally, a normal hearse trip by another entry point, so sharing one
// quota between the two closes the loophole structurally rather than by
// convention.
func (d *DeathServicesAPI) Dispense(bodyIDs []uint64, month int64, correlationID string) ([]uint64, error) {
	if err := d.checkNotCopied(correlationID, "Dispense"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(bodyIDs) > 1 && !d.dispensation.active {
		return nil, errs.New(ErrMultiBodyOutsideDispensation, correlationID, map[string]any{"bodies": len(bodyIDs)})
	}
	vanCap := d.cfg.DispensationVanBodyCapacity()
	working := bodyIDs
	if int64(len(working)) > vanCap && d.dispensation.active {
		// A single call may not exceed the data-sourced van capacity even
		// while active -- bounded lift (AC-11), not unbounded.
		working = working[:vanCap]
	}

	// Pass 1: DEDUPLICATE + validate the WHOLE (possibly truncated) batch
	// before any mutation. Round-3 N2: without the seen-set,
	// Dispense([2,2]) validated one body twice and charged usedThisMonth
	// twice for it -- the same duplicate-in-batch shape as Cremate's N1,
	// same fix, same H4 policy (b): later occurrences skipped, first kept.
	seen := make(map[uint64]bool, len(working))
	unique := make([]uint64, 0, len(working))
	for _, id := range working {
		if seen[id] {
			continue
		}
		seen[id] = true
		b, ok := d.bodies[id]
		if !ok {
			return nil, errs.New(ErrUnknownBody, correlationID, map[string]any{"bodyId": id})
		}
		if b.state != BodyAwaiting && b.state != BodyEnRoute {
			return nil, errs.New(ErrBodyAlreadyHandled, correlationID, map[string]any{"bodyId": id, "state": string(b.state)})
		}
		unique = append(unique, id)
	}

	var remaining int64
	if d.dispensation.active {
		d.dispensation.resetMonthLocked(month)
		remaining = d.cfg.DispensationMonthlyBudget() - d.dispensation.usedThisMonth
	} else {
		// M1: inactive-mode single-body dispense shares the normal hearse
		// budget's monthly counter (see doc above).
		d.hearse.resetMonthLocked(month)
		remaining = d.cfg.HearseMonthlyTransportBudget() - d.hearse.usedThisMonth
	}
	if remaining < 0 {
		remaining = 0
	}
	n := int64(len(unique))
	if n > remaining {
		n = remaining
	}
	toDispense := unique[:n]

	// Commit: validation already passed for every id in toDispense
	// (a subset of the validated, deduplicated `unique`), so this cannot
	// itself fail.
	for _, id := range toDispense {
		b := d.bodies[id]
		b.state = BodyDispensed
	}
	if d.dispensation.active {
		d.dispensation.usedThisMonth += n
	} else {
		d.hearse.usedThisMonth += n
	}

	return append([]uint64(nil), toDispense...), nil
}

// DispensationWellbeingPenalty returns the data-sourced (negative)
// wellbeing delta to apply THIS PERIOD (AC-13): the configured placeholder
// while dispensation is active, or exactly 0 when it is not. AC-13's
// escalation (MOD-083-inc1.md assumption 8) leaves open whether the real
// penalty should apply continuously or once at event end; inc1's
// mechanism is deliberately the simpler, unambiguous "continuously while
// active" reading, since that is what a caller polling this accessor once
// per period naturally gets, and it is trivially compatible with a future
// "apply once, at end" caller that instead reads this only on the
// active->inactive transition.
func (d *DeathServicesAPI) DispensationWellbeingPenalty(correlationID string) (float64, error) {
	if err := d.checkNotCopied(correlationID, "DispensationWellbeingPenalty"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.dispensation.active {
		return 0, nil
	}
	return d.cfg.DispensationWellbeingPenalty(), nil
}

// DispensationApprovalPenalty returns the data-sourced (negative) approval
// delta to apply THIS PERIOD (AC-13), mirroring
// [DeathServicesAPI.DispensationWellbeingPenalty] exactly.
func (d *DeathServicesAPI) DispensationApprovalPenalty(correlationID string) (float64, error) {
	if err := d.checkNotCopied(correlationID, "DispensationApprovalPenalty"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.dispensation.active {
		return 0, nil
	}
	return d.cfg.DispensationApprovalPenalty(), nil
}
