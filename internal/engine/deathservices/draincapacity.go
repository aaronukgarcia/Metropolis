package deathservices

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// draincapacity.go implements M3 (round-2, the original brief's design
// centre): wiring THIS module's REAL, live disposal capacity back
// upstream into FEAT-087's death queue as the injected [citizens.
// DrainCapacity] (ASM-580's second, independent knob -- see
// citizens/deathwave.go's DrainCapacity/RealiseDrained doc), so the death
// queue only releases what disposal can actually process each period
// instead of an unbounded or static figure.
//
// Wiring point: engine.citizens IS one of this module's eight registered
// code.json outbound edges, and citizens.CitizensAPI.SetDeathDrainCapacity
// already exists as its post-construction wiring surface (mirroring
// CitizensAPI.SetSeason's precedent -- no constructor argument needed, so
// every existing NewCitizensAPI call site is unaffected). No NEW edge
// registration is required for this: [DeathServicesAPI] simply implements
// [citizens.DrainCapacity] directly (its MonthlyDrainCapacity method
// signature matches the interface exactly) and
// [DeathServicesAPI.WireDrainCapacity] passes itself to the ALREADY-
// registered citizens surface.
//
// compose (internal/engine/compose/*) does not yet wire ANY DrainCapacity
// consumer for FEAT-087 -- grepped and confirmed empty at the time of this
// fix (feat.deathwave's DrainCapacity/SetDeathDrainCapacity exist as a
// capability with no consumer yet, since engine.deathservices itself was
// only added to the tree by this same MOD-083 estate). There is therefore
// no existing compose call-site PATTERN to mirror; WireDrainCapacity below
// is the call a FUTURE composition-root wiring pass (out of scope for
// inc1 per MOD-083-inc1.md's "UI screens... deferred to increment 2" and
// the module's own stub-forever contract, GR#20) would make once it
// exists, exactly the same way SetSeason/SetDeathDrainCapacity's own doc
// comments describe their own composition-root call sites.

// daysPerMonthApprox is MonthlyDrainCapacity's coarse day/month
// conversion, translating crematory.go's PER-DAY throughput
// (CremationDailyThroughputPerBody, AC-5's spec-seed 12/d) into a
// per-month figure consistent with AC-9's documented "coarse, aggregated"
// monthly-budget convention for hearse/crematorium/dispensation
// throughput. Not a calendar-accurate day count (no month has exactly 30
// days) -- a future increment that ticks cremation per real calendar day
// could replace this with an exact day-of-month count; inc1 keeps the
// same coarse-aggregate stance AC-9 already applies to the hearse and
// dispensation budgets.
const daysPerMonthApprox = 30

// MonthlyDrainCapacity implements [citizens.DrainCapacity] (M3): the LIVE,
// recomputed-every-call disposal throughput this module can process for
// monthIndex, summing three independently-sourced components:
//
//   - plot capacity: every registered cemetery's currently-allocatable
//     plot count (never-used plus reuse-eligible, [allocatableCountLocked]
//     -- the SAME live figure buryLocked's admission gate reads, so this
//     number is never a stale snapshot of a different rule).
//   - cremation headroom: every registered crematorium's nominal monthly
//     throughput (dailyThroughput x [daysPerMonthApprox]) -- a coarse
//     upper bound, not netted against today's actual daily counter (see
//     daysPerMonthApprox's own doc for why day-level and month-level
//     granularity are deliberately not reconciled at inc1's depth).
//   - hearse headroom: the remaining monthly hearse transport budget for
//     monthIndex (data-sourced budget minus whatever
//     [DeathServicesAPI.RunHearseTransport]/the inactive-mode
//     [DeathServicesAPI.Dispense] path has already consumed this month,
//     M1's shared counter).
//
// This is a PURE, non-mutating READ -- it never advances hearse.lastMonth
// or any crematorium's lastDay as a side effect of being queried (a
// caller polling capacity ahead of the month it actually drains into must
// not silently roll counters forward). Never a static or unbounded
// number: capacity shrinks as plots/throughput/budget are consumed by
// Bury/Cremate/RunHearseTransport/Dispense, and grows again once a new
// month's counters are due to reset (observed here, not caused by this
// read).
func (d *DeathServicesAPI) MonthlyDrainCapacity(monthIndex int64) int {
	// citizens.DrainCapacity's interface signature is fixed (monthIndex
	// int64) int -- no correlationID, no error return -- so a struct-copy
	// call cannot be reported to the caller the way every other exported
	// method on this type does. Fail closed instead (0 capacity, never
	// grant throughput off a copy's aliased-but-independently-locked
	// state), using the SAME checkNotCopied every other method calls
	// (SEC-020, astgate) with a documented literal correlationID standing
	// in for the missing parameter.
	if err := d.checkNotCopied("deathservices.MonthlyDrainCapacity", "MonthlyDrainCapacity"); err != nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	horizon := d.cfg.PlotReuseHorizonMonths()
	var plotCapacity int64
	for _, c := range d.cemeteries {
		plotCapacity += allocatableCountLocked(c, monthIndex, horizon)
	}

	dailyThroughput := d.cfg.CremationDailyThroughputPerBody()
	cremationCapacity := dailyThroughput * daysPerMonthApprox * int64(len(d.crematoria))

	hearseUsed := int64(0)
	if d.hearse.lastMonth == monthIndex {
		hearseUsed = d.hearse.usedThisMonth
	}
	hearseRemaining := d.cfg.HearseMonthlyTransportBudget() - hearseUsed
	if hearseRemaining < 0 {
		// M2: [ErrNegativeBudget] wired to a real call site -- a negative
		// remaining figure here means hearseUsed exceeded the data-sourced
		// budget, always a programming error (RunHearseTransport/Dispense's
		// own commit paths never let usedThisMonth exceed the budget by
		// construction). Logged once (GR#17), never fatal -- this read
		// still returns a safe, clamped-to-zero capacity either way.
		if !d.negativeBudgetWarned {
			d.negativeBudgetWarned = true
			// MonthlyDrainCapacity implements citizens.DrainCapacity's
			// fixed (monthIndex int64) int signature, which carries no
			// correlationID -- a documented literal stands in here,
			// exactly as citizens.DrainCapacityFunc's own callers must.
			_ = errs.New(ErrNegativeBudget, "deathservices.MonthlyDrainCapacity", map[string]any{"budget": hearseRemaining})
		}
		hearseRemaining = 0
	}

	total := plotCapacity + cremationCapacity + hearseRemaining
	if total < 0 {
		total = 0
	}
	return int(total)
}

// WireDrainCapacity injects this module's live [MonthlyDrainCapacity] into
// FEAT-087's death queue through the registered engine.citizens outbound
// edge (M3). A composition root calls this once, after constructing both
// APIs -- this module never calls it on itself (GR#20: wiring order is the
// composition root's job, never a module's own). citizensAPI == nil is a
// documented caller error (there is nothing to wire into) rejected with
// [ErrDeathServicesCopied]'s sibling copy-guard style would not fit here
// (citizensAPI is not a candidate for THIS package's own copy guard) --
// instead this simply returns nil (a no-op), mirroring [DeathServicesAPI.
// Wire]'s own nil-tolerant precedent for optional dependencies rather than
// inventing a new registry code for a caller passing nil.
func (d *DeathServicesAPI) WireDrainCapacity(citizensAPI *citizens.CitizensAPI, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "WireDrainCapacity"); err != nil {
		return err
	}
	if citizensAPI == nil {
		return nil
	}
	return citizensAPI.SetDeathDrainCapacity(d, correlationID)
}
