package policies

// CombinedEffect returns the combined, queryable effect of every active
// policy targeting coefficientKey in scopes overlapping scope (AC-10). The
// combination rule is data-declared (meta.combination, the single supported
// rule "multiplicative"), so the result is a distinct value of its own —
// ∏(1+delta_i) − 1 over the overlapping active policies — never the naive
// sum of the individual declared deltas (two deltas a,b combine to
// a+b+ab, visibly different from a+b whenever ab ≠ 0).
//
// Evaluation order is deterministic (GR#21/AC-14): active enactments are
// visited in the stable (PolicyID, district, road) order, though the
// multiplicative combination is order-independent by construction.
func (a *PoliciesAPI) CombinedEffect(coefficientKey string, scope Scope) (float64, error) {
	if err := a.checkNotCopied("CombinedEffect"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	deltas := make([]float64, 0)
	for _, e := range a.sortedActiveEnactmentsLocked() {
		if !scopeOverlaps(e.scope, scope) {
			continue
		}
		def := a.library[e.policyID]
		if def == nil {
			continue
		}
		for _, cd := range def.Mechanism {
			if cd.Key == coefficientKey {
				deltas = append(deltas, cd.Delta)
			}
		}
	}
	return combineMultiplicative(deltas), nil
}

// CoefficientState returns the net active delta per coefficient key across
// every active enactment (citywide), sorted by key (GR#21). This is the
// observable "engine state" a mechanism-vs-mutation diff snapshots (AC-3):
// enacting a policy mutates exactly the coefficient keys its data entry
// declares, and this query is the field-by-field surface that proves it.
type CoefficientStateEntry struct {
	Key   string
	Delta float64
}

// CoefficientState implements the AC-3 snapshot surface.
func (a *PoliciesAPI) CoefficientState() []CoefficientStateEntry {
	if err := a.checkNotCopied("CoefficientState"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	keys := a.coefficientKeysLocked(Scope{Kind: ScopeCitywide})
	out := make([]CoefficientStateEntry, 0, len(keys))
	for _, key := range keys {
		var deltas []float64
		for _, e := range a.sortedActiveEnactmentsLocked() {
			def := a.library[e.policyID]
			if def == nil {
				continue
			}
			for _, cd := range def.Mechanism {
				if cd.Key == key {
					deltas = append(deltas, cd.Delta)
				}
			}
		}
		out = append(out, CoefficientStateEntry{Key: key, Delta: combineMultiplicative(deltas)})
	}
	return out
}

// combineMultiplicative applies the data-declared multiplicative rule:
// ∏(1+delta_i) − 1. A single delta returns itself; zero deltas return 0.
func combineMultiplicative(deltas []float64) float64 {
	factor := 1.0
	for _, d := range deltas {
		factor *= (1 + d)
	}
	return factor - 1
}
