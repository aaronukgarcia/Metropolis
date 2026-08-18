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
func (a *PoliciesAPI) AdvanceMonth(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if month < a.currentMonth {
		return nil, errs.New(ErrMonthRegression, a.correlationID, map[string]any{
			"current": a.currentMonth, "month": month,
		})
	}

	// Quarterly drift checkpoint (ASM-286, cadence read from data GR#15).
	cadence := a.meta.PreviewDrift.CheckpointMonths
	if cadence <= 0 {
		cadence = 3 // never reachable once a validated meta is loaded; defensive only
	}
	checkpointDue := month > 0 && month%cadence == 0

	// GR#1/GR#12: pre-flight the checkpoint dependency BEFORE posting any
	// recurring opex — otherwise the opex debits would post and then the
	// checkpoint could fail with ErrProjectionsNotWired, leaving the month's
	// opex spent with no checkpoint run (and a retry would re-post it).
	if checkpointDue && a.projections == nil {
		return nil, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "AdvanceMonth checkpoint"})
	}

	// Recurring enforcement opex (AC-19 part 2): deterministic order.
	for _, e := range a.sortedActiveEnactmentsLocked() {
		def := a.library[e.policyID]
		if def == nil || def.Cost.OpexMonthlyMicroPounds <= 0 {
			continue
		}
		if err := a.postOpex(def.ID, finance.Money(def.Cost.OpexMonthlyMicroPounds), "policy enforcement opex ("+string(def.ID)+")"); err != nil {
			return nil, err
		}
	}

	var events []PreviewDriftEvent
	if checkpointDue {
		var err error
		events, err = a.checkpointLocked(month)
		if err != nil {
			return nil, err
		}
	}

	// Advance the clock only after every fallible step above has succeeded
	// (GR#12): a retried month never re-posts opex or re-runs a checkpoint.
	a.currentMonth = month
	return events, nil
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
		return nil, errs.New(ErrCheckpointPrecedesCurrentMonth, a.correlationID, map[string]any{
			"checkpoint": month, "current": a.currentMonth,
		})
	}
	a.currentMonth = month
	return a.checkpointLocked(month)
}

// checkpointLocked is the shared drift evaluation. Caller holds the write
// lock. It is deterministic: enactments and coefficient keys and points are
// all visited in sorted order (AC-14/GR#21).
func (a *PoliciesAPI) checkpointLocked(month int64) ([]PreviewDriftEvent, error) {
	if err := a.checkNotCopied("checkpointLocked"); err != nil {
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
	a.events = append(a.events, raised...)
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
