package policies

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ResolveScope maps a policy's declared scope (plus a concrete target) to
// the actual affected entities (AC-9/US-5): the whole city for citywide, a
// district's cell set for district, and a named road's edge set for road.
// This is the shared scope-resolution service every consumer (e.g.
// ui.screen.districts) calls rather than reimplementing scope logic
// locally.
//
// policyID names the policy whose declared scope kind is being resolved;
// scope is the concrete target (for a citywide policy, Scope{Kind:
// citywide}; for a district policy, Scope{Kind: district, District: id};
// for a road policy, Scope{Kind: road, Road: id}). A scope whose kind does
// not match the policy's declared kind is rejected with ErrScopeMismatch;
// an unknown district/road is rejected with ErrUnknownScope — never a
// resolved-to-empty-set false success (AC-13).
func (a *PoliciesAPI) ResolveScope(policyID PolicyID, scope Scope) (ScopeResolution, error) {
	if err := a.checkNotCopied("ResolveScope"); err != nil {
		return ScopeResolution{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	def, err := a.lookupLocked(policyID)
	if err != nil {
		return ScopeResolution{}, err
	}
	if !scope.valid() {
		return ScopeResolution{}, errs.New(ErrUnknownScope, a.correlationID, map[string]any{"scope": scope})
	}
	if def.Scope != scope.Kind {
		return ScopeResolution{}, errs.New(ErrScopeMismatch, a.correlationID, map[string]any{
			"policy": string(policyID), "declared": string(def.Scope), "given": string(scope.Kind),
		})
	}
	switch scope.Kind {
	case ScopeCitywide:
		return ScopeResolution{Kind: ScopeCitywide, Citywide: true}, nil
	case ScopeDistrict:
		d, ok := a.districts[scope.District]
		if !ok {
			return ScopeResolution{}, errs.New(ErrUnknownScope, a.correlationID, map[string]any{
				"scope": string(ScopeDistrict), "district": string(scope.District),
			})
		}
		return ScopeResolution{Kind: ScopeDistrict, District: scope.District, Cells: copyCells(d.cells)}, nil
	case ScopeRoad:
		r, ok := a.roads[scope.Road]
		if !ok {
			return ScopeResolution{}, errs.New(ErrUnknownScope, a.correlationID, map[string]any{
				"scope": string(ScopeRoad), "road": string(scope.Road),
			})
		}
		return ScopeResolution{Kind: ScopeRoad, Road: scope.Road, Edges: copyEdges(r.edges)}, nil
	default:
		return ScopeResolution{}, errs.New(ErrUnknownScope, a.correlationID, map[string]any{"scope": scope})
	}
}

// validateScopeLocked validates that scope is well-formed, matches def's
// declared scope kind, and (for district/road scopes) names an existing
// district/road. Caller holds at least a read lock. Used by ResolveScope,
// Enact and PreviewImpact so all three share one scope contract (GR#3).
func (a *PoliciesAPI) validateScopeLocked(def *policyDef, scope Scope) error {
	if err := a.checkNotCopied("validateScopeLocked"); err != nil {
		return err
	}
	if !scope.valid() {
		return errs.New(ErrUnknownScope, a.correlationID, map[string]any{"scope": scope})
	}
	if def.Scope != scope.Kind {
		return errs.New(ErrScopeMismatch, a.correlationID, map[string]any{
			"policy": string(def.ID), "declared": string(def.Scope), "given": string(scope.Kind),
		})
	}
	switch scope.Kind {
	case ScopeDistrict:
		if _, ok := a.districts[scope.District]; !ok {
			return errs.New(ErrUnknownScope, a.correlationID, map[string]any{
				"scope": string(ScopeDistrict), "district": string(scope.District),
			})
		}
	case ScopeRoad:
		if _, ok := a.roads[scope.Road]; !ok {
			return errs.New(ErrUnknownScope, a.correlationID, map[string]any{
				"scope": string(ScopeRoad), "road": string(scope.Road),
			})
		}
	}
	return nil
}

// scopeOverlaps reports whether two concrete scopes overlap (AC-10's
// "same scope" test and AC-11's "overlapping scope" conflict test): a
// citywide scope overlaps everything; two district scopes overlap only on
// the same district; two road scopes overlap only on the same road; a
// district scope and a road scope never overlap each other.
func scopeOverlaps(x, y Scope) bool {
	switch x.Kind {
	case ScopeCitywide:
		return true
	case ScopeDistrict:
		switch y.Kind {
		case ScopeCitywide:
			return true
		case ScopeDistrict:
			return x.District == y.District
		default:
			return false
		}
	case ScopeRoad:
		switch y.Kind {
		case ScopeCitywide:
			return true
		case ScopeRoad:
			return x.Road == y.Road
		default:
			return false
		}
	default:
		return false
	}
}

// coefficientKeysLocked returns every coefficient key currently targeted by
// an active enactment whose scope overlaps scope, sorted (GR#21). Used by
// the combined-effect query to enumerate keys deterministically.
func (a *PoliciesAPI) coefficientKeysLocked(scope Scope) []string {
	if err := a.checkNotCopied("coefficientKeysLocked"); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, e := range a.sortedActiveEnactmentsLocked() {
		if !scopeOverlaps(e.scope, scope) {
			continue
		}
		def := a.library[e.policyID]
		if def == nil {
			continue
		}
		for _, cd := range def.Mechanism {
			seen[cd.Key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
