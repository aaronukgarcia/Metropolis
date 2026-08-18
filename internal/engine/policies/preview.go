package policies

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// coefficientPayload returns the policy's coefficient-delta payload in a
// stable, sorted-by-key order (def.Mechanism is already sorted at build
// time; this returns a fresh copy so the caller never aliases the library
// entry). It is the SINGLE source of the payload both PreviewImpact and
// Enact feed into engine.projections (AC-4) — the two paths share one
// function, never two hand-written lookalike payloads.
func coefficientPayload(def *policyDef) []CoefficientDelta {
	out := make([]CoefficientDelta, len(def.Mechanism))
	copy(out, def.Mechanism)
	return out
}

// PreviewImpact computes the projected curve for enacting policyID in scope
// — a same-model conditional/what-if query (AC-4): it feeds the identical
// coefficient-delta payload the real Enact applies into engine.projections'
// registered curve providers (via temporary decision steps), queries the
// resulting curve over the current forecasting horizon, and returns it with
// engine.projections' own Point/Confidence tags (AC-5). It is exactly one
// thing (AC-6): a conditional projection of the declared deltas — never a
// guarantee, never an independent estimate formula.
//
// Points beyond ProjectionsAPI's horizon N are tagged by engine.projections
// itself as Extrapolated, never Computed (AC-5) — this package never
// re-invents or overrides that distinction.
func (a *PoliciesAPI) PreviewImpact(policyID PolicyID, scope Scope) (Preview, error) {
	if err := a.checkNotCopied("PreviewImpact"); err != nil {
		return Preview{}, err
	}
	a.mu.RLock()
	def, err := a.lookupLocked(policyID)
	if err != nil {
		a.mu.RUnlock()
		return Preview{}, err
	}
	if err := a.validateScopeLocked(def, scope); err != nil {
		a.mu.RUnlock()
		return Preview{}, err
	}
	proj := a.projections
	currentMonth := a.currentMonth
	a.mu.RUnlock()

	if proj == nil {
		return Preview{}, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "PreviewImpact"})
	}
	horizon, err := proj.HorizonMonths()
	if err != nil {
		return Preview{}, err
	}
	return computePreview(def, scope, proj, currentMonth, currentMonth+horizon, a.correlationID)
}

// PreviewImpactRange is [PreviewImpact] with an explicit end month, so a
// caller (or a test, AC-5) can request a preview whose range extends past
// the forecasting horizon and observe the beyond-horizon points arrive
// tagged Extrapolated rather than Computed.
func (a *PoliciesAPI) PreviewImpactRange(policyID PolicyID, scope Scope, toMonth int64) (Preview, error) {
	if err := a.checkNotCopied("PreviewImpactRange"); err != nil {
		return Preview{}, err
	}
	a.mu.RLock()
	def, err := a.lookupLocked(policyID)
	if err != nil {
		a.mu.RUnlock()
		return Preview{}, err
	}
	if err := a.validateScopeLocked(def, scope); err != nil {
		a.mu.RUnlock()
		return Preview{}, err
	}
	proj := a.projections
	currentMonth := a.currentMonth
	a.mu.RUnlock()

	if proj == nil {
		return Preview{}, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "PreviewImpactRange"})
	}
	return computePreview(def, scope, proj, currentMonth, toMonth, a.correlationID)
}

// computePreview is the shared preview engine behind PreviewImpact and
// PreviewImpactRange. It enqueues the payload as temporary decision steps,
// queries each coefficient key's curve over [fromMonth, toMonth], cancels
// the temporary steps, and returns the payload + per-key series. The
// temporary decision IDs are namespaced under a preview prefix so they can
// never collide with an enactment's permanent decisions.
func computePreview(def *policyDef, scope Scope, proj projectionSeam, fromMonth, toMonth int64, correlationID string) (Preview, error) {
	if err := proj.SetCurrentMonth(fromMonth); err != nil {
		return Preview{}, err
	}
	if toMonth < fromMonth {
		return Preview{}, errs.New(ErrPreviewRangeInverted, correlationID, map[string]any{
			"fromMonth": fromMonth, "toMonth": toMonth,
		})
	}

	deltas := coefficientPayload(def)
	prefix := "preview:" + string(def.ID)
	enqueued := make([]string, 0, len(deltas))
	for _, cd := range deltas {
		id := prefix + ":" + cd.Key
		if err := proj.EnqueueDecision(projections.Decision{
			ID:              id,
			CurveKey:        cd.Key,
			CompletionMonth: fromMonth,
			Delta:           cd.Delta,
		}); err != nil {
			cancelTemporary(proj, enqueued)
			return Preview{}, err
		}
		enqueued = append(enqueued, id)
	}

	series := make([]CoefficientSeries, 0, len(deltas))
	for _, cd := range deltas {
		pts, err := proj.Curve(cd.Key, fromMonth, toMonth)
		if err != nil {
			cancelTemporary(proj, enqueued)
			return Preview{}, err
		}
		series = append(series, CoefficientSeries{Key: cd.Key, Points: pts})
	}

	cancelTemporary(proj, enqueued)
	return Preview{PolicyID: def.ID, Scope: scope, Deltas: deltas, Series: series}, nil
}

// cancelTemporary best-effort cancels the temporary preview decisions. The
// steps were already applied and are about to be discarded; a cancel error
// (which cannot happen for IDs we just enqueued on the real API) is
// deliberately non-fatal here — the preview is still valid, and the
// temporary IDs are namespaced so they cannot collide with enactment
// decisions.
func cancelTemporary(proj projectionSeam, ids []string) {
	for _, id := range ids {
		_ = proj.CancelDecision(id)
	}
}

// computedPoints returns only the ConfidenceComputed points of a series —
// the AC-7 persisted "Computed-tagged portion of its curve".
func computedPoints(pts []projections.Point) []projections.Point {
	out := make([]projections.Point, 0, len(pts))
	for _, p := range pts {
		if p.Confidence == projections.ConfidenceComputed {
			out = append(out, p)
		}
	}
	return out
}
