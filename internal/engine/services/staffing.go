package services

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is §26's shared staffing-pool mechanism (AC-4, AC-13, AC-15):
// a named pool of staff that several service kinds draw from, so a nurse
// shortage is genuinely one shortage everywhere rather than N per-service
// silos. Pool→service membership is configured in data/services.json
// (staffingPools[].members), never hardcoded in Go (AC-4).

// StaffingAllocation is the per-service outcome of allocating one pool's
// available staff across its member services for a tick.
type StaffingAllocation struct {
	ServiceID ServiceID
	Needed    float64 // the service's staffing need
	Allocated float64 // staff this service actually received from the pool
	Shortfall float64 // Needed - Allocated (0 when the pool was sufficient)
}

// poolDefFor resolves a pool id against the loaded staffingPools table.
func (a *ServicesAPI) poolDefFor(id string) (StaffingPool, bool) {
	if err := a.checkNotCopied("poolDefFor"); err != nil {
		return StaffingPool{}, false
	}
	for _, p := range a.pools {
		if p.ID == id {
			return p, true
		}
	}
	return StaffingPool{}, false
}

// poolAvailableFor returns the current available staff for a pool (0 when
// never set this tick).
func (a *ServicesAPI) poolAvailableFor(id string) float64 {
	if err := a.checkNotCopied("poolAvailableFor"); err != nil {
		return 0
	}
	return a.poolAvailable[id]
}

// SetPoolStaff sets the available staff for a named pool for the current
// tick — the composition root's per-tick staffing input. An unknown pool
// id is rejected with ErrUnknownStaffingPool (never a silently-ignored
// write), and a negative availability is clamped to zero (a negative staff
// count has the unambiguous meaning "no staff", which needs no distinct
// error class).
func (a *ServicesAPI) SetPoolStaff(poolID string, available float64) error {
	if err := a.checkNotCopied("SetPoolStaff"); err != nil {
		return err
	}
	if !num.IsFinite(available) {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "available"})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.poolDefFor(poolID); !ok {
		return serviceErr(a.correlationID, ErrUnknownStaffingPool, map[string]any{"pool": poolID})
	}
	if available < 0 {
		available = 0
	}
	a.poolAvailable[poolID] = available
	return nil
}

// AllocateStaffing allocates one pool's available staff across its member
// services as a uniform proportional share of need: every member receives
// need × available/totalNeed (capped at need), so a short pool degrades
// EVERY member simultaneously (AC-4) rather than fully satisfying the
// first and starving the rest. The allocation order is documented and
// deterministic — members are collected and summed in ascending ServiceID
// order, never Go map-iteration order (AC-13/GR#21). The result is written
// back onto each member instance (its allocated staff, consumed by the
// quality staffing factor) and returned for inspection.
//
// A pool with zero available staff still allocates (every member gets
// zero, with a full shortfall) rather than erroring — "the pool is empty"
// is a legitimate state, distinct from "the pool does not exist" (which
// SetPoolStaff already rejects).
func (a *ServicesAPI) AllocateStaffing(poolID string) ([]StaffingAllocation, error) {
	if err := a.checkNotCopied("AllocateStaffing"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	def, ok := a.poolDefFor(poolID)
	if !ok {
		return nil, serviceErr(a.correlationID, ErrUnknownStaffingPool, map[string]any{"pool": poolID})
	}

	members := make(map[ServiceKind]bool, len(def.Members))
	for _, m := range def.Members {
		members[ServiceKind(m)] = true
	}

	// Collect the registered instances whose kind is a member of the pool,
	// then sort by ServiceID — both the total-need sum and the per-service
	// allocation iterate this sorted slice, so floating-point accumulation
	// order is fixed (GR#21).
	var ids []ServiceID
	for id, inst := range a.instances {
		if members[inst.spec.Kind] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var totalNeed float64
	for _, id := range ids {
		if need := a.instances[id].spec.StaffingNeed; need > 0 {
			totalNeed += need
		}
	}

	available := a.poolAvailableFor(poolID)
	var ratio float64
	if totalNeed > 0 {
		ratio = clamp01(available / totalNeed)
	} else {
		ratio = 1 // no member has any staffing need ⇒ nothing to short
	}

	out := make([]StaffingAllocation, 0, len(ids))
	for _, id := range ids {
		inst := a.instances[id]
		need := inst.spec.StaffingNeed
		if need < 0 {
			need = 0
		}
		alloc := need * ratio
		if alloc > need {
			alloc = need
		}
		inst.allocated = alloc
		out = append(out, StaffingAllocation{
			ServiceID: id,
			Needed:    need,
			Allocated: alloc,
			Shortfall: need - alloc,
		})
	}
	return out, nil
}

// StaffingAllocations returns the per-service allocation results for a pool
// WITHOUT re-running the allocation (a read of the last AllocateStaffing's
// outcome), in the same deterministic ServiceID order. It exists so a
// caller can inspect the last allocation's shortfalls without mutating the
// instances' allocated state. An unknown pool id is rejected.
func (a *ServicesAPI) StaffingAllocations(poolID string) ([]StaffingAllocation, error) {
	if err := a.checkNotCopied("StaffingAllocations"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	def, ok := a.poolDefFor(poolID)
	if !ok {
		return nil, serviceErr(a.correlationID, ErrUnknownStaffingPool, map[string]any{"pool": poolID})
	}
	members := make(map[ServiceKind]bool, len(def.Members))
	for _, m := range def.Members {
		members[ServiceKind(m)] = true
	}
	var ids []ServiceID
	for id, inst := range a.instances {
		if members[inst.spec.Kind] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]StaffingAllocation, 0, len(ids))
	for _, id := range ids {
		inst := a.instances[id]
		need := inst.spec.StaffingNeed
		if need < 0 {
			need = 0
		}
		out = append(out, StaffingAllocation{
			ServiceID: id,
			Needed:    need,
			Allocated: inst.allocated,
			Shortfall: need - inst.allocated,
		})
	}
	return out, nil
}
