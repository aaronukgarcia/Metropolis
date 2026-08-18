package policies

// PolicyInfo is the library query surface for one policy (AC-1/US-1): its
// data identity (name, category), its data-defined mechanism (the
// coefficient moves it declares — "instruments = data-defined coefficient
// moves"), its cost/enforcement needs, its declared scope kind, and its
// declared conflict pairs. Values are the immutable data-loaded definition,
// never a cached runtime aggregate.
type PolicyInfo struct {
	ID         PolicyID
	Name       string
	Category   string
	Scope      ScopeKind
	Mechanism  []CoefficientDelta
	Cost       CostDef
	Conflicts  []PolicyID
	Disclosure string
}

// Policies lists every library policy in sorted policy-key order (GR#21).
// The returned slice is owned by the caller — never an alias of the
// internal map.
func (a *PoliciesAPI) Policies() []PolicyInfo {
	if err := a.checkNotCopied("Policies"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := a.sortedPolicyIDsLocked()
	out := make([]PolicyInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.infoLocked(a.library[id]))
	}
	return out
}

// Policy returns one policy's library info (AC-1's query half).
func (a *PoliciesAPI) Policy(id PolicyID) (PolicyInfo, error) {
	if err := a.checkNotCopied("Policy"); err != nil {
		return PolicyInfo{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	def, err := a.lookupLocked(id)
	if err != nil {
		return PolicyInfo{}, err
	}
	return a.infoLocked(def), nil
}

// infoLocked builds one policy's query info. Caller holds at least a read
// lock. Mechanism/Conflicts slices are copied so the caller never aliases
// the immutable library entry.
func (a *PoliciesAPI) infoLocked(def *policyDef) PolicyInfo {
	if err := a.checkNotCopied("infoLocked"); err != nil {
		return PolicyInfo{}
	}
	mech := make([]CoefficientDelta, len(def.Mechanism))
	copy(mech, def.Mechanism)
	conflicts := make([]PolicyID, len(def.Conflicts))
	copy(conflicts, def.Conflicts)
	return PolicyInfo{
		ID:         def.ID,
		Name:       def.Name,
		Category:   def.Category,
		Scope:      def.Scope,
		Mechanism:  mech,
		Cost:       def.Cost,
		Conflicts:  conflicts,
		Disclosure: def.Disclosure,
	}
}
