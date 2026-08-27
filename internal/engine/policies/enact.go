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
//
// Enact is atomic (GR#1/GR#12): it splits cleanly into a validate phase
// that performs no mutation at all, and a commit phase that applies the
// mechanism, then the cost, and only then records the enactment. Every
// fallible external step is either pre-flighted in the validate phase (so
// it cannot fail later for a pre-validatable reason) or rolled back exactly
// if it does fail in the commit phase — a failed enactment never leaves the
// treasury debited, a projection decision enqueued, or a tax move applied.
//
//  1. validate (no mutation): policy/scope lookup, identical-scope
//     re-enactment rejection (AC-13), and a full dependency pre-flight
//     (projections/finance/tax wired, tax-move validity and district-scope
//     requirement);
//  2. compute the enactment preview snapshot (AC-7) via the same
//     coefficient payload the mechanism applies (AC-4) — transient
//     projections state only, no permanent mutation;
//  3. commit: enqueue every projection decision (reversible), route every
//     tax move through engine.tax, post the declared enactment cost through
//     engine.finance as the LAST external side effect (AC-19), then record
//     the enactment and raise conflict warnings (AC-11). If any commit step
//     fails, everything already applied is rolled back before the error is
//     returned.
//
// The second policy still enacts even when a conflict is warned (AC-11).
func (a *PoliciesAPI) Enact(policyID PolicyID, scope Scope) (EnactmentID, error) {
	if err := a.checkNotCopied("Enact"); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// ---- Phase 1: validate (no mutation) ----
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

	// GR#1/GR#12: pre-flight every dependency that can fail recoverably
	// BEFORE any side effect. A recoverable failure must never occur after
	// money has been debited or a projection decision enqueued.
	if err := a.preflightEnactLocked(def, scope); err != nil {
		return "", err
	}

	// ---- Phase 2: compute the preview snapshot (no permanent mutation) ----
	// The ID is minted deterministically from the counter but the counter is
	// only bumped in the commit phase, so a preview failure never burns an ID.
	id := a.peekEnactmentIDLocked()
	preview, err := a.computeEnactmentPreviewLocked(def, scope)
	if err != nil {
		return "", err
	}

	// ---- Phase 3: commit (with exact rollback on any failure) ----
	if err := a.commitEnactLocked(id, def, scope, preview); err != nil {
		return "", err
	}
	return id, nil
}

// peekEnactmentIDLocked computes the next enactment ID without advancing the
// counter (the caller advances it in the commit phase). The caller holds the
// write lock.
func (a *PoliciesAPI) peekEnactmentIDLocked() EnactmentID {
	if err := a.checkNotCopied("peekEnactmentIDLocked"); err != nil {
		return ""
	}
	return EnactmentID(encodeEnactmentID(a.nextEnactmentID))
}

// preflightEnactLocked validates every recoverable dependency an Enact needs
// BEFORE any irreversible side effect (a money debit, a projection decision,
// or a tax move). The caller holds the write lock. It exists so a recoverable
// failure can never occur after the treasury has been debited or a projection
// decision has been enqueued (GR#1/GR#12).
func (a *PoliciesAPI) preflightEnactLocked(def *policyDef, scope Scope) error {
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
		// A district-multiplier tax move can only target a district; a
		// citywide/road policy carrying one would fail in the commit phase
		// (empty district), so reject it here, before any mutation.
		if scope.Kind != ScopeDistrict {
			return errs.New(ErrScopeMismatch, a.correlationID, map[string]any{
				"policy":   string(def.ID),
				"declared": string(def.Scope),
				"given":    string(scope.Kind),
			})
		}
		if a.tax == nil {
			return errs.New(ErrTaxNotWired, a.correlationID, map[string]any{"operation": "Enact"})
		}
		if cd.Tax.Mode != taxMoveDistrictMultiplier {
			return errs.New(ErrPoliciesDataInvalid, a.correlationID, map[string]any{
				"mode": cd.Tax.Mode, "instrument": cd.Tax.Instrument,
				"cause": "unsupported tax move mode",
			})
		}
	}
	return nil
}

// commitEnactLocked applies the mechanism and the cost, then records the
// enactment. Every fallible external step is followed by an exact rollback on
// failure: projection decisions are cancelled, applied tax moves are reset to
// neutral, and the enactment-cost posting is the LAST external side effect so
// a ledger failure never leaves a projection decision or tax move orphaned.
// The final local bookkeeping (preview, active set, ID counter, warnings) is
// infallible, so the caller never observes a half-recorded enactment. The
// caller holds the write lock.
func (a *PoliciesAPI) commitEnactLocked(id EnactmentID, def *policyDef, scope Scope, preview storedPreview) error {
	if err := a.checkNotCopied("commitEnactLocked"); err != nil {
		return err
	}

	// 1. Projection decisions (reversible). A failure cancels the decisions
	// already enqueued inside applyProjectionDecisionsLocked.
	enqueued, err := a.applyProjectionDecisionsLocked(id, def)
	if err != nil {
		return err
	}

	// 2. Tax moves (not reversible through the seam — reset to neutral on
	// failure, best-effort, see resetTaxMovesLocked).
	appliedTax, err := a.applyTaxMovesLocked(def, scope)
	if err != nil {
		a.rollbackProjectionsLocked(enqueued)
		a.resetTaxMovesLocked(appliedTax)
		return err
	}

	// 3. Enactment cost — the LAST external side effect (AC-19). If the
	// ledger rejects it, roll the mechanism back so no orphaned projection
	// decision or tax move outlives the failed enact.
	if def.Cost.EnactmentMicroPounds > 0 {
		if err := a.postOpex(def.ID, finance.Money(def.Cost.EnactmentMicroPounds), "policy enactment ("+string(def.ID)+")"); err != nil {
			a.rollbackProjectionsLocked(enqueued)
			a.resetTaxMovesLocked(appliedTax)
			return err
		}
	}

	// 4. Infallible local commit — nothing below can fail.
	a.previews[id] = preview
	a.active[id] = &enactment{id: id, policyID: def.ID, scope: scope}
	a.nextEnactmentID++
	a.raiseConflictWarningsLocked(def, scope)
	return nil
}

// Repeal removes an active enactment: it cancels the enactment's projection
// decision steps, restores any district-multiplier the enactment applied
// through engine.tax, drops its stored preview, and deletes it from the
// active set. An unknown enactment ID is rejected (ErrEnactmentNotFound),
// never a silent no-op.
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

	// Remember which (district, instrument) tax moves this enactment applied,
	// so their multipliers can be restored once the enactment is gone.
	var taxTouches []appliedTaxMove
	if def != nil && a.tax != nil {
		for _, cd := range def.Mechanism {
			if cd.Tax != nil {
				taxTouches = append(taxTouches, appliedTaxMove{
					district:   tax.DistrictID(e.scope.District),
					instrument: cd.Tax.Instrument,
				})
			}
		}
	}

	delete(a.active, id)
	delete(a.previews, id)

	// Restore each touched district+instrument multiplier to the value the
	// REMAINING active enactments imply (GR#1/GR#12): a repealed freeport must
	// not leave its district at the 0.0 multiplier it applied, and a
	// still-active sibling policy's multiplier must survive the repeal.
	for _, mv := range taxTouches {
		restored := a.districtMultiplierFromActiveLocked(mv.district, mv.instrument)
		_ = a.tax.SetDistrictMultiplier(mv.district, mv.instrument, restored)
	}
	return nil
}

// applyProjectionDecisionsLocked enqueues each coefficient delta as a
// permanent projection decision step and returns the decision IDs enqueued
// (for a caller to cancel on a later failure). On its own failure it cancels
// the decisions it already enqueued and returns a nil slice, so the caller
// never has to reason about a partial list. The caller holds the write lock.
// It mutates exactly the declared coefficients and nothing else (AC-3).
func (a *PoliciesAPI) applyProjectionDecisionsLocked(id EnactmentID, def *policyDef) ([]string, error) {
	if err := a.checkNotCopied("applyProjectionDecisionsLocked"); err != nil {
		return nil, err
	}
	if a.projections == nil {
		return nil, errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "Enact"})
	}
	if err := a.projections.SetCurrentMonth(a.currentMonth); err != nil {
		return nil, err
	}

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
			return nil, err
		}
		enqueued = append(enqueued, decisionID)
	}
	return enqueued, nil
}

// appliedTaxMove records one tax move applied in the commit phase, naming the
// district+instrument it touched so a later failure (or a Repeal) can restore
// exactly the multiplier that was moved — rather than resetting moves that were
// never applied, or clobbering a prior still-active enactment's multiplier.
type appliedTaxMove struct {
	district   tax.DistrictID
	instrument string
}

// applyTaxMovesLocked routes every data-declared tax move through engine.tax
// and returns the moves actually applied (for rollback). On failure it returns
// the moves applied so far, so the caller can restore them. The caller holds
// the write lock.
func (a *PoliciesAPI) applyTaxMovesLocked(def *policyDef, scope Scope) ([]appliedTaxMove, error) {
	if err := a.checkNotCopied("applyTaxMovesLocked"); err != nil {
		return nil, err
	}
	var applied []appliedTaxMove
	for _, cd := range def.Mechanism {
		if cd.Tax == nil {
			continue
		}
		mv, err := a.applyTaxMoveLocked(scope, *cd.Tax, cd.Delta)
		if err != nil {
			return applied, err
		}
		applied = append(applied, mv)
	}
	return applied, nil
}

// rollbackProjectionsLocked cancels the projection decision IDs a partially-
// committed enactment enqueued, undoing exactly those steps (GR#12).
func (a *PoliciesAPI) rollbackProjectionsLocked(ids []string) {
	cancelDecisions(a.projections, ids)
}

// resetTaxMovesLocked restores, in reverse order, the district multipliers the
// passed-in moves touched, back to the value the still-active enactments imply
// (the pre-enactment value). The tax seam exposes no inverse, so policies
// recomputes the prior value from its own tracked active set — it never
// clobbers a prior still-active enactment's multiplier with a blanket neutral
// 1.0.
func (a *PoliciesAPI) resetTaxMovesLocked(applied []appliedTaxMove) {
	if err := a.checkNotCopied("resetTaxMovesLocked"); err != nil {
		return
	}
	for i := len(applied) - 1; i >= 0; i-- {
		mv := applied[i]
		if a.tax == nil {
			continue
		}
		prior := a.districtMultiplierFromActiveLocked(mv.district, mv.instrument)
		_ = a.tax.SetDistrictMultiplier(mv.district, mv.instrument, prior)
	}
}

// districtMultiplierFromActiveLocked returns the district multiplier the
// currently active enactments imply for (district, instrument): the product of
// (1.0 + delta) over every active district-scoped enactment carrying a tax move
// on that instrument — 1.0 (neutral) when none does. It applies the same
// data-declared multiplicative rule CombinedEffect uses, so the applied
// multiplier always equals the reported one (GR#3). The caller holds the write
// lock.
func (a *PoliciesAPI) districtMultiplierFromActiveLocked(district tax.DistrictID, instrument string) float64 {
	if err := a.checkNotCopied("districtMultiplierFromActiveLocked"); err != nil {
		return 1.0
	}
	factor := 1.0
	for _, e := range a.sortedActiveEnactmentsLocked() {
		if e.scope.Kind != ScopeDistrict || tax.DistrictID(e.scope.District) != district {
			continue
		}
		def := a.library[e.policyID]
		if def == nil {
			continue
		}
		for _, cd := range def.Mechanism {
			if cd.Tax != nil && cd.Tax.Instrument == instrument {
				factor *= (1.0 + cd.Delta)
			}
		}
	}
	return factor
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
// engine.tax, composing it multiplicatively with the current multiplier (the
// data-declared combination rule, AC-10): the new multiplier is the current
// one × (1.0 + delta), so two -0.5 moves yield 0.25 — exactly the value
// CombinedEffect reports. Only the single supported mode (districtMultiplier)
// is reachable here — Validate rejects any other mode at load time and
// preflightEnactLocked re-checks it. The current multiplier is read back from
// engine.tax via GetDistrictMultiplier BEFORE the move is computed (getter-first),
// never from a policies-side mirror — so an out-of-band mutation of the applied
// multiplier is composed against, not silently clobbered with a stale figure.
func (a *PoliciesAPI) applyTaxMoveLocked(scope Scope, mv TaxMove, delta float64) (appliedTaxMove, error) {
	if err := a.checkNotCopied("applyTaxMoveLocked"); err != nil {
		return appliedTaxMove{}, err
	}
	if a.tax == nil {
		return appliedTaxMove{}, errs.New(ErrTaxNotWired, a.correlationID, map[string]any{"operation": "Enact"})
	}
	switch mv.Mode {
	case taxMoveDistrictMultiplier:
		district := tax.DistrictID(scope.District)
		current, err := a.tax.GetDistrictMultiplier(district, mv.Instrument)
		if err != nil {
			return appliedTaxMove{}, err
		}
		next := current * (1.0 + delta)
		if err := a.tax.SetDistrictMultiplier(district, mv.Instrument, next); err != nil {
			return appliedTaxMove{}, err
		}
		return appliedTaxMove{district: district, instrument: mv.Instrument}, nil
	default:
		return appliedTaxMove{}, errs.New(ErrPoliciesDataInvalid, a.correlationID, map[string]any{
			"mode": mv.Mode, "instrument": mv.Instrument,
			"cause": "unknown tax move mode",
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

// computeEnactmentPreviewLocked computes the enactment preview via the shared
// preview engine (same model, AC-4) and returns its Computed points as a
// storedPreview WITHOUT persisting it — the caller persists it in the commit
// phase so a failed enactment never leaves a dangling preview behind (AC-7).
// The caller holds the write lock.
func (a *PoliciesAPI) computeEnactmentPreviewLocked(def *policyDef, scope Scope) (storedPreview, error) {
	if err := a.checkNotCopied("computeEnactmentPreviewLocked"); err != nil {
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
