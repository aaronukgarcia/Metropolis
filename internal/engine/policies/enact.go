package policies

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ConflictWarning is the AC-11 non-blocking warning raised when a policy is
// enacted that conflicts with an already-active policy in an overlapping
// scope (§52: bundles "warn", not "are prevented" — the second policy still
// enacts).
type ConflictWarning struct {
	EnactedPolicy PolicyID
	EnactedScope  Scope
	ConflictWith  PolicyID
	ActiveScope   Scope
}

// Enact enacts policyID in scope and returns the assigned EnactmentID.
// It:
//
//  1. validates the policy and scope (ErrUnknownPolicy/ErrScopeMismatch/
//     ErrUnknownScope);
//  2. rejects an already-active policy in an identical scope
//     (ErrPolicyAlreadyActive, AC-13);
//  3. pre-flights every dependency that can fail recoverably (finance,
//     projections, tax, tax-move validity) BEFORE any irreversible side
//     effect, so a later failure can never leave the treasury debited or a
//     projection decision orphaned (GR#1/GR#12);
//  4. computes and persists the enactment preview snapshot (AC-7);
//  5. applies the data-declared mechanism — enqueueing each coefficient
//     delta into engine.projections (the same payload PreviewImpact uses,
//     AC-4) and routing any data-declared tax move through engine.tax;
//  6. posts the declared enactment cost through engine.finance (AC-19) as
//     the LAST step, rolling back the mechanism if the posting fails;
//  7. records the enactment and raises any conflict warnings (AC-11).
//
// The second policy still enacts even when a conflict is warned (AC-11).
func (a *PoliciesAPI) Enact(policyID PolicyID, scope Scope) (EnactmentID, error) {
	if err := a.checkNotCopied("Enact"); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	def, err := a.lookupLocked(policyID)
	if err != nil {
		return "", err
	}
	if err := a.validateScopeLocked(def, scope); err != nil {
		return "", err
	}

	// AC-13: identical-scope re-enactment is rejected, never a silent no-op.
	for _, e := range a.active {
		if e.policyID == policyID && e.scope == scope {
			return "", errs.New(ErrPolicyAlreadyActive, a.correlationID, map[string]any{
				"policy": string(policyID), "scope": scope,
			})
		}
	}

	// GR#1/GR#12: validate everything that can fail BEFORE any irreversible
	// side effect. A recoverable failure (unwired projections/tax/finance)
	// must never occur after money has been debited or a projection decision
	// enqueued — otherwise a retry double-debits and orphaned decisions skew
	// future Curve queries.
	if err := a.preflightEnactLocked(def); err != nil {
		return "", err
	}

	// AC-7: persist the preview snapshot keyed by the enactment ID — BEFORE
	// the permanent mechanism is applied, so the preview reflects the base
	// curve plus this policy's delta alone, never the policy's delta twice
	// (the preview's temporary steps are cancelled before the permanent
	// steps are enqueued below).
	id := a.nextEnactmentIDLocked()
	if _, err := a.persistPreviewLocked(id, def, scope); err != nil {
		return "", err
	}

	// AC-3/AC-4: apply the mechanism — the same coefficient payload the
	// preview feeds into projections. Deterministic order (mechanism is
	// sorted by key at load; GR#21).
	if err := a.applyMechanismLocked(id, def, scope); err != nil {
		delete(a.previews, id)
		return "", err
	}

	// AC-19: enactment cost debits through engine.finance (never through
	// engine.tax — the wrong category). This is the LAST side effect: every
	// fallible step above has already succeeded, so a recoverable failure
	// can never leave money debited before the mechanism is committed. If
	// the ledger still rejects the posting, roll the mechanism back so no
	// orphaned projection decision (or tax move) outlives the failed enact.
	if def.Cost.EnactmentMicroPounds > 0 {
		if err := a.postOpex(def.ID, finance.Money(def.Cost.EnactmentMicroPounds), "policy enactment ("+string(def.ID)+")"); err != nil {
			a.rollbackMechanismLocked(id, def, scope)
			delete(a.previews, id)
			return "", err
		}
	}

	a.active[id] = &enactment{id: id, policyID: policyID, scope: scope}
	a.raiseConflictWarningsLocked(def, scope)
	return id, nil
}

// preflightEnactLocked validates every recoverable dependency an Enact
// needs BEFORE any irreversible side effect (a money debit, a projection
// decision, or a tax move). The caller holds the write lock. It exists so a
// recoverable failure can never occur after the treasury has been debited or
// a projection decision has been enqueued (GR#1/GR#12).
func (a *PoliciesAPI) preflightEnactLocked(def *policyDef) error {
	if err := a.checkNotCopied("preflightEnactLocked"); err != nil {
		return err
	}
	if a.projections == nil {
		return errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "Enact"})
	}
	if def.Cost.EnactmentMicroPounds > 0 && a.finance == nil {
		return errs.New(ErrFinanceNotWired, a.correlationID, map[string]any{
			"operation": "policy enactment (" + string(def.ID) + ")",
			"policy":    string(def.ID),
		})
	}
	for _, cd := range def.Mechanism {
		if cd.Tax == nil {
			continue
		}
		if a.tax == nil {
			return errs.New(ErrTaxNotWired, a.correlationID, map[string]any{"operation": "Enact"})
		}
		if cd.Tax.Mode != taxMoveDistrictMultiplier {
			return errs.New(ErrPoliciesDataInvalid, a.correlationID, map[string]any{
				"mode": cd.Tax.Mode, "instrument": cd.Tax.Instrument,
			})
		}
	}
	return nil
}

// rollbackMechanismLocked best-effort undoes the mechanism an Enact applied
// when the enactment-cost posting (the LAST Enact step) fails. It cancels
// every projection decision the enactment enqueued and resets each
// data-declared tax move back to the neutral 1.0 multiplier. The tax seam
// exposes no inverse, so the reset to neutral is best-effort (a district
// multiplier set by a prior enactment is not recoverable through this
// seam); the projection cancellation is the load-bearing half — it is what
// keeps a retried Enact from double-counting the delta.
func (a *PoliciesAPI) rollbackMechanismLocked(id EnactmentID, def *policyDef, scope Scope) {
	if err := a.checkNotCopied("rollbackMechanismLocked"); err != nil {
		return
	}
	for _, cd := range def.Mechanism {
		if a.projections != nil {
			_ = a.projections.CancelDecision(string(id) + ":" + cd.Key)
		}
		if cd.Tax != nil && a.tax != nil {
			_ = a.tax.SetDistrictMultiplier(tax.DistrictID(scope.District), cd.Tax.Instrument, 1.0)
		}
	}
}

// Repeal removes an active enactment: it cancels the enactment's projection
// decision steps, drops its stored preview, and deletes it from the active
// set. An unknown enactment ID is rejected (ErrEnactmentNotFound), never a
// silent no-op.
func (a *PoliciesAPI) Repeal(id EnactmentID) error {
	if err := a.checkNotCopied("Repeal"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	e, ok := a.active[id]
	if !ok {
		return errs.New(ErrEnactmentNotFound, a.correlationID, map[string]any{"enactment": string(id)})
	}

	// Cancel the projection steps this enactment enqueued (best-effort only
	// when projections is unwired — an enactment can only have steps if
	// projections was wired at enact time).
	def := a.library[e.policyID]
	if a.projections != nil && def != nil {
		for _, cd := range def.Mechanism {
			_ = a.projections.CancelDecision(string(id) + ":" + cd.Key)
		}
	}

	delete(a.active, id)
	delete(a.previews, id)
	return nil
}

// applyMechanismLocked applies the policy's mechanism: each coefficient
// delta is enqueued into engine.projections as a permanent decision step,
// and any data-declared tax move is routed through engine.tax. The caller
// holds the write lock. It mutates exactly the declared coefficients and
// nothing else (AC-3).
func (a *PoliciesAPI) applyMechanismLocked(id EnactmentID, def *policyDef, scope Scope) error {
	if err := a.checkNotCopied("applyMechanismLocked"); err != nil {
		return err
	}
	if a.projections == nil {
		return errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "Enact"})
	}
	if err := a.projections.SetCurrentMonth(a.currentMonth); err != nil {
		return err
	}

	// enqueued records the decision IDs enqueued so far in this pass, so a
	// later failure (e.g. a tax move rejection) can cancel them — an
	// enactment must never leave an orphaned projection decision behind
	// (GR#1/GR#12).
	var enqueued []string
	for _, cd := range def.Mechanism {
		decisionID := string(id) + ":" + cd.Key
		if err := a.projections.EnqueueDecision(projections.Decision{
			ID:              decisionID,
			CurveKey:        cd.Key,
			CompletionMonth: a.currentMonth,
			Delta:           cd.Delta,
		}); err != nil {
			cancelDecisions(a.projections, enqueued)
			return err
		}
		enqueued = append(enqueued, decisionID)
		if cd.Tax != nil {
			if err := a.applyTaxMoveLocked(scope, *cd.Tax, cd.Delta); err != nil {
				cancelDecisions(a.projections, enqueued)
				return err
			}
		}
	}
	return nil
}

// cancelDecisions best-effort cancels a list of projection decision IDs,
// rolling back the decisions a partially-applied mechanism already enqueued
// when a later step in the same pass fails (GR#12).
func cancelDecisions(proj projectionSeam, ids []string) {
	for _, id := range ids {
		_ = proj.CancelDecision(id)
	}
}

// applyTaxMoveLocked routes a data-declared tax coefficient move through
// engine.tax. Only the single supported mode (districtMultiplier) is
// reachable here — Validate rejects any other mode at load time.
func (a *PoliciesAPI) applyTaxMoveLocked(scope Scope, mv TaxMove, delta float64) error {
	if err := a.checkNotCopied("applyTaxMoveLocked"); err != nil {
		return err
	}
	if a.tax == nil {
		return errs.New(ErrTaxNotWired, a.correlationID, map[string]any{"operation": "Enact"})
	}
	switch mv.Mode {
	case taxMoveDistrictMultiplier:
		// A district-scoped tax move applies the multiplier to the district
		// the policy was enacted on. 1.0 + delta: a delta of -1.0 yields the
		// 0.0 tax-free multiplier a freeport declares.
		return a.tax.SetDistrictMultiplier(tax.DistrictID(scope.District), mv.Instrument, 1.0+delta)
	default:
		return errs.New(ErrPoliciesDataInvalid, a.correlationID, map[string]any{
			"mode": mv.Mode, "instrument": mv.Instrument,
		})
	}
}

// postOpex posts a single opex debit through the wired engine.finance
// ledger: treasury debited, external credited (money leaves the city's
// economy). Never silently skipped (ErrFinanceNotWired, GR#17).
func (a *PoliciesAPI) postOpex(policy PolicyID, amount finance.Money, description string) error {
	if err := a.checkNotCopied("postOpex"); err != nil {
		return err
	}
	if a.finance == nil {
		return errs.New(ErrFinanceNotWired, a.correlationID, map[string]any{
			"operation": description, "policy": string(policy),
		})
	}
	_, err := a.finance.Post(finance.Transaction{
		Description: description,
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: amount, Category: finance.CatOpex},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: amount, Category: finance.CatOpex},
		},
	})
	return err
}

// persistPreviewLocked computes the enactment preview via the shared preview
// engine (same model, AC-4) and stores its Computed points under id (AC-7).
// The caller holds the write lock.
func (a *PoliciesAPI) persistPreviewLocked(id EnactmentID, def *policyDef, scope Scope) (storedPreview, error) {
	if err := a.checkNotCopied("persistPreviewLocked"); err != nil {
		return storedPreview{}, err
	}
	if a.projections == nil {
		return storedPreview{}, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "Enact preview"})
	}
	horizon, err := a.projections.HorizonMonths()
	if err != nil {
		return storedPreview{}, err
	}
	preview, err := computePreview(def, scope, a.projections, a.currentMonth, a.currentMonth+horizon, a.correlationID)
	if err != nil {
		return storedPreview{}, err
	}
	sp := storedPreview{deltas: coefficientPayload(def), points: make(map[string][]projections.Point), horizon: horizon}
	for _, s := range preview.Series {
		sp.points[s.Key] = computedPoints(s.Points)
	}
	a.previews[id] = sp
	return sp, nil
}

// raiseConflictWarningsLocked raises a ConflictWarning for every already-
// active enactment whose policy conflicts with def and whose scope overlaps
// the newly-enacted scope (AC-11). It never blocks the enactment.
func (a *PoliciesAPI) raiseConflictWarningsLocked(def *policyDef, scope Scope) {
	if err := a.checkNotCopied("raiseConflictWarningsLocked"); err != nil {
		return
	}
	conflictSet := make(map[PolicyID]bool, len(def.Conflicts))
	for _, c := range def.Conflicts {
		conflictSet[c] = true
	}
	for _, e := range a.sortedActiveEnactmentsLocked() {
		if !conflictSet[e.policyID] {
			continue
		}
		if !scopeOverlaps(e.scope, scope) {
			continue
		}
		a.warnings = append(a.warnings, ConflictWarning{
			EnactedPolicy: def.ID,
			EnactedScope:  scope,
			ConflictWith:  e.policyID,
			ActiveScope:   e.scope,
		})
	}
}

// Conflicts returns every raised conflict warning in the order raised
// (deterministic: enactments are visited in stable order at raise time,
// GR#21). Queryable, never rendered text.
func (a *PoliciesAPI) Conflicts() []ConflictWarning {
	if err := a.checkNotCopied("Conflicts"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]ConflictWarning, len(a.warnings))
	copy(out, a.warnings)
	return out
}
