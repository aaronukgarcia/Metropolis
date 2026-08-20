package policies

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AdvanceMonth advances the simulation month, posts each active policy's
// recurring enforcement opex line through engine.finance (AC-19 part 2 —
// recurring, never a one-off), and — at the data-declared quarterly
// checkpoint cadence (meta.previewDrift.checkpointMonths, ASM-286) — runs
// the PreviewDrift evaluation (AC-7). It returns any drift events raised at
// this month.
//
// This is the tick-driven checkpoint surface: a caller that only unit-tests
// the divergence arithmetic but never calls this from the tick pipeline
// would leave PreviewDrift unreachable, which AC-7's false-pass warning
// names — hence the checkpoint evaluation lives here, on the tick path.
//
// AdvanceMonth is idempotent per month: a second call for the same month is
// a no-op (never a double opex debit, never a re-run checkpoint), tracked by
// the last-posted month on the API.
func (a *PoliciesAPI) AdvanceMonth(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if month < a.currentMonth {
		return nil, errs.New(ErrUnknownScope, a.correlationID, map[string]any{
			"scope":   "month regression",
			"month":   month,
			"current": a.currentMonth,
		})
	}

	// Idempotency guard: a second AdvanceMonth for the month already fully
	// processed is a no-op — it must never double-post opex or re-run the
	// checkpoint for the same month. The clock only moves past the guard on
	// success below, so a month that failed mid-way can still be retried.
	if month == a.lastPostedMonth {
		return nil, nil
	}

	// Quarterly drift checkpoint (ASM-286, cadence read from data GR#15).
	cadence := a.meta.PreviewDrift.CheckpointMonths
	if cadence <= 0 {
		cadence = 3 // never reachable once a validated meta is loaded; defensive only
	}
	checkpointDue := month > 0 && month%cadence == 0

	// GR#1/GR#12: pre-flight every fallible dependency BEFORE any side effect.
	// A checkpoint needs projections; monthly opex needs finance. Both are
	// checked up front so neither can fail after the other has mutated state.
	if checkpointDue && a.projections == nil {
		return nil, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "AdvanceMonth checkpoint"})
	}
	if a.hasMonthlyOpexLocked() && a.finance == nil {
		return nil, errs.New(ErrFinanceNotWired, a.correlationID, map[string]any{"operation": "AdvanceMonth opex"})
	}

	// Run the checkpoint FIRST (when due): it reads projections and is the only
	// non-posting fallible step. Running it before opex means a checkpoint
	// failure never leaves the month's opex posted with the clock unmoved.
	var events []PreviewDriftEvent
	if checkpointDue {
		raised, err := a.evaluateCheckpointLocked(month)
		if err != nil {
			return nil, err
		}
		events = raised
	}

	// Post the month's recurring opex as ONE atomic transaction: a validation
	// failure on any policy's line rejects the whole month, so an earlier
	// policy's opex is never left posted with the clock unmoved.
	if err := a.postMonthlyOpexLocked(); err != nil {
		return nil, err
	}

	// Only now — after the checkpoint and the opex posting both succeeded —
	// persist the checkpoint events and advance the clock (GR#12): a retried
	// month never re-posts opex, never re-runs a checkpoint, and never
	// double-appends events. The dedupe pass also makes a re-run of the same
	// checkpoint month (e.g. via Checkpoint then AdvanceMonth) accumulate no
	// duplicate events.
	a.appendDriftEventsLocked(events)
	a.currentMonth = month
	a.lastPostedMonth = month
	return events, nil
}

// hasMonthlyOpexLocked reports whether any active enactment carries a non-zero
// monthly enforcement opex line. The caller holds the write lock.
func (a *PoliciesAPI) hasMonthlyOpexLocked() bool {
	if err := a.checkNotCopied("hasMonthlyOpexLocked"); err != nil {
		return false
	}
	for _, e := range a.sortedActiveEnactmentsLocked() {
		def := a.library[e.policyID]
		if def != nil && def.Cost.OpexMonthlyMicroPounds > 0 {
			return true
		}
	}
	return false
}

// postMonthlyOpexLocked posts every active policy's recurring enforcement opex
// as a SINGLE finance transaction (AC-19 part 2). Batching the month's lines
// into one Post makes the month atomic (GR#12): a validation failure on any
// line (e.g. an overdraft on the last policy's debit) rejects the whole
// posting, so an earlier policy's opex is never left posted with the clock
// unmoved — a retry re-posts the full month exactly once, never double-debiting
// the earlier policies. The caller holds the write lock.
func (a *PoliciesAPI) postMonthlyOpexLocked() error {
	if err := a.checkNotCopied("postMonthlyOpexLocked"); err != nil {
		return err
	}
	entries := make([]finance.Entry, 0, 2)
	for _, e := range a.sortedActiveEnactmentsLocked() {
		def := a.library[e.policyID]
		if def == nil || def.Cost.OpexMonthlyMicroPounds <= 0 {
			continue
		}
		amount := finance.Money(def.Cost.OpexMonthlyMicroPounds)
		entries = append(entries,
			finance.Entry{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: amount, Category: finance.CatOpex},
			finance.Entry{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: amount, Category: finance.CatOpex},
		)
	}
	if len(entries) == 0 {
		return nil
	}
	if a.finance == nil {
		return errs.New(ErrFinanceNotWired, a.correlationID, map[string]any{
			"operation": "policy enforcement opex",
		})
	}
	_, err := a.finance.Post(finance.Transaction{
		Description: "policy enforcement opex (month)",
		Entries:     entries,
	})
	return err
}

// Checkpoint runs the PreviewDrift evaluation at month explicitly (AC-7's
// "documented checkpoints after enactment"): for every active enactment it
// compares the stored preview's Computed points against the current
// observed curve values and raises a PreviewDriftEvent for each divergence
// beyond tolerance. It returns the events raised, in deterministic order.
func (a *PoliciesAPI) Checkpoint(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("Checkpoint"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if month < a.currentMonth {
		return nil, errs.New(ErrUnknownScope, a.correlationID, map[string]any{
			"scope":      "checkpoint precedes current month",
			"checkpoint": month,
			"current":    a.currentMonth,
		})
	}
	a.currentMonth = month
	return a.checkpointLocked(month)
}

// checkpointLocked is the shared drift evaluation; it appends the raised
// events to the queryable event log. Caller holds the write lock. It is
// deterministic: enactments and coefficient keys and points are all visited in
// sorted order (AC-14/GR#21).
func (a *PoliciesAPI) checkpointLocked(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("checkpointLocked"); err != nil {
		return nil, err
	}
	raised, err := a.evaluateCheckpointLocked(month)
	if err != nil {
		return nil, err
	}
	a.appendDriftEventsLocked(raised)
	return raised, nil
}

// driftEventKey is the identity of one PreviewDriftEvent for dedupe purposes:
// one enactment (the policy instance in scope), one coefficient (the "kind"
// of drift), and one checkpoint month. Re-evaluating the same checkpoint for
// the same enactment+coefficient is the same reckoning, not a second drift
// event.
type driftEventKey struct {
	enactment   EnactmentID
	coefficient string
	checkpoint  int64
}

// appendDriftEventsLocked appends raised drift events to the queryable log,
// skipping any whose (enactment, coefficient, checkpoint) already exists —
// so a re-run of the same month's checkpoint never double-appends the same
// reckoning (drift-event dedupe, AC-7/US-3). The caller holds the write lock.
func (a *PoliciesAPI) appendDriftEventsLocked(raised []PreviewDriftEvent) {
	if err := a.checkNotCopied("appendDriftEventsLocked"); err != nil {
		return
	}
	seen := make(map[driftEventKey]struct{}, len(a.events)+len(raised))
	for _, ev := range a.events {
		seen[driftEventKey{enactment: ev.EnactmentID, coefficient: ev.Coefficient, checkpoint: ev.Checkpoint}] = struct{}{}
	}
	for _, ev := range raised {
		key := driftEventKey{enactment: ev.EnactmentID, coefficient: ev.Coefficient, checkpoint: ev.Checkpoint}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		a.events = append(a.events, ev)
	}
}

// evaluateCheckpointLocked computes the PreviewDrift events for month WITHOUT
// persisting them, so AdvanceMonth can persist them only after the month's opex
// posting also succeeds (an atomic month, GR#12). Caller holds the write lock.
// It is deterministic (AC-14/GR#21).
func (a *PoliciesAPI) evaluateCheckpointLocked(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("evaluateCheckpointLocked"); err != nil {
		return nil, err
	}
	if a.projections == nil {
		return nil, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "Checkpoint"})
	}
	tolerance := a.meta.PreviewDrift.Tolerance
	if tolerance <= 0 {
		tolerance = 0.10 // defensive; a validated meta always supplies a positive value
	}

	var raised []PreviewDriftEvent
	for _, e := range a.sortedActiveEnactmentsLocked() {
		sp, ok := a.previews[e.id]
		if !ok {
			continue
		}
		keys := make([]string, 0, len(sp.points))
		for k := range sp.points {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			pts := sp.points[key]
			// Compare the stored Computed point at exactly this checkpoint
			// month (one reckoning per coefficient per checkpoint — the
			// event names "the checkpoint", not every month of the curve).
			var stored *projections.Point
			for i := range pts {
				if pts[i].Month == month {
					stored = &pts[i]
					break
				}
			}
			if stored == nil {
				continue
			}
			actualSeries, err := a.projections.Curve(key, month, month)
			if err != nil {
				return nil, err
			}
			if len(actualSeries) == 0 {
				continue
			}
			actual := actualSeries[0].Value
			magnitude := divergenceMagnitude(stored.Value, actual)
			if magnitude > tolerance {
				raised = append(raised, PreviewDriftEvent{
					EnactmentID: e.id,
					PolicyID:    e.policyID,
					Coefficient: key,
					Checkpoint:  month,
					Previewed:   stored.Value,
					Actual:      actual,
					Magnitude:   magnitude,
				})
			}
		}
	}
	return raised, nil
}

// divergenceMagnitude is the ASM-286 divergence measure: the absolute
// difference scaled by the previewed trajectory, with a floor of 1.0 on the
// denominator so a near-zero preview degrades to an absolute difference
// rather than dividing by zero. |actual − previewed| / max(|previewed|, 1).
func divergenceMagnitude(previewed, actual float64) float64 {
	denom := math.Abs(previewed)
	if denom < 1 {
		denom = 1
	}
	return math.Abs(actual-previewed) / denom
}

// PreviewDriftEvents returns every raised drift event in the order raised
// (deterministic, AC-14). Queryable, never silently discarded (US-3).
func (a *PoliciesAPI) PreviewDriftEvents() []PreviewDriftEvent {
	if err := a.checkNotCopied("PreviewDriftEvents"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]PreviewDriftEvent, len(a.events))
	copy(out, a.events)
	return out
}
