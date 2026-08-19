package services

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the city/district coverage & enumeration aggregate
// (AC-18…AC-25, docs/planning/icd/engine.services-coverage.md): the
// deterministic enumeration of the registered surface, the city-wide
// CoverageSummary (Σcapacity vs Σdemand), and the per-district
// CoverageByDistrict breakdown over caller-pushed district demand. It is a
// pure read over ServicesAPI state plus caller-pushed demand — no
// cross-module read, no spatial read, no conserved-stock mutation, and no
// wall-clock read (GR#21).

// DistrictID identifies one city district for per-district coverage
// accounting. It is owned by this package and supplied by the caller (the
// composition root) through [ServicesAPI.UpdateDistrictDemand] — the
// aggregate performs no spatial read, so district identity is a push-input
// (mirroring engine.policies' named-district string identity), never derived
// from a geometry module this package has no registered edge to (AC-21).
type DistrictID string

// demandRecord is one pushed (district, service) demand datum: the demand
// placed on the service and that demand's distance from the service cell —
// the two inputs the per-district mean quality needs alongside the
// service's stored funding/capacity/staffing state.
type demandRecord struct {
	demand   float64
	distance float64
}

// CoverageSummary is the city-wide coverage aggregate (AC-19): the
// registered instance count, the Σ of every instance's capacity ceiling
// (the AC-9 capacityCeiling() value), the Σ of every instance's pushed
// demand (via UpdateDemand), the clamped coverage ratio, and the mean
// realised Quality over all instances (0.0 when nothing is registered).
type CoverageSummary struct {
	ServiceCount  int
	TotalCapacity float64
	TotalDemand   float64
	CoverageRatio float64
	MeanQuality   float64
}

// DistrictCoverage is one district's coverage breakdown (AC-21): the
// district's own Σcapacity/Σdemand ratio and mean quality, computed from
// only that district's pushed demand records.
type DistrictCoverage struct {
	District      DistrictID
	TotalCapacity float64
	TotalDemand   float64
	CoverageRatio float64
	MeanQuality   float64
}

// coverageRatio is the aggregate coverage fraction (AC-19/AC-25): 1.0 when
// demand is zero (nothing to serve ⇒ fully covered), else
// clamp01(capacity/demand) — mirroring ComputeQuality's capacityFactor. The
// clamp bounds the result to [0,1] and collapses any non-finite ratio to a
// defined value, so the aggregate never leaks +Inf/NaN even when a huge
// capacity/demand pair overflows the division (GR#16). The aggregate carries
// no coefficients — the only numeric inputs are per-service state (capacity,
// demand, coverage radius, funding, staffing), so GR#15's "weights come from
// data" rule has nothing to load here.
func coverageRatio(capacity, demand float64) float64 {
	if demand == 0 {
		return 1.0
	}
	return clamp01(capacity / demand)
}

// sortedKeys returns a map's keys in ascending order — the deterministic
// enumeration GR#21 requires. Every enumeration/aggregate accessor routes
// through this rather than ranging a map directly, so ServiceID/ServiceKind/
// DistrictID order is fixed regardless of insertion or Go map-iteration
// order (AC-18/AC-21/AC-24).
func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ServiceIDs returns every registered service instance id in ascending
// order (AC-18). It is a pure read over the registration set.
func (a *ServicesAPI) ServiceIDs() ([]ServiceID, error) {
	if err := a.checkNotCopied("ServiceIDs"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return sortedKeys(a.instances), nil
}

// ServiceKinds returns every registered service kind in ascending order
// (AC-18).
func (a *ServicesAPI) ServiceKinds() ([]ServiceKind, error) {
	if err := a.checkNotCopied("ServiceKinds"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return sortedKeys(a.kinds), nil
}

// DistrictIDs returns every district that has had demand pushed, in
// ascending order (AC-21).
func (a *ServicesAPI) DistrictIDs() ([]DistrictID, error) {
	if err := a.checkNotCopied("DistrictIDs"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return sortedKeys(a.districtDemand), nil
}

// CoverageSummary returns the city-wide aggregate (AC-19). It is a pure
// read: no registration, funding, demand, or staffing state is mutated
// (AC-22).
func (a *ServicesAPI) CoverageSummary() (CoverageSummary, error) {
	if err := a.checkNotCopied("CoverageSummary"); err != nil {
		return CoverageSummary{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := sortedKeys(a.instances)
	var s CoverageSummary
	s.ServiceCount = len(ids)
	var totalQuality float64
	for _, id := range ids {
		inst := a.instances[id]
		s.TotalCapacity += inst.capacityCeiling()
		s.TotalDemand += inst.demand
		totalQuality += realizedQuality(inst)
	}
	s.CoverageRatio = coverageRatio(s.TotalCapacity, s.TotalDemand)
	if s.ServiceCount > 0 {
		s.MeanQuality = totalQuality / float64(s.ServiceCount)
	}
	return s, nil
}

// CoverageByDistrict returns one entry per district that has had demand
// pushed, sorted by DistrictID (AC-21/GR#21). Each entry's
// TotalCapacity/TotalDemand/CoverageRatio/MeanQuality is computed from only
// that district's own pushed records — mutating one district's demand never
// moves another district's entry.
func (a *ServicesAPI) CoverageByDistrict() ([]DistrictCoverage, error) {
	if err := a.checkNotCopied("CoverageByDistrict"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := sortedKeys(a.districtDemand)
	out := make([]DistrictCoverage, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.districtCoverageLocked(id))
	}
	return out, nil
}

// CoverageForDistrict returns one district's coverage breakdown, or
// ErrUnknownDistrict when the district has never had demand pushed (AC-23) —
// never a zero-value coverage silently read as "the district exists but is
// empty".
func (a *ServicesAPI) CoverageForDistrict(district DistrictID) (DistrictCoverage, error) {
	if err := a.checkNotCopied("CoverageForDistrict"); err != nil {
		return DistrictCoverage{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if _, ok := a.districtDemand[district]; !ok {
		return DistrictCoverage{}, serviceErr(a.correlationID, ErrUnknownDistrict, map[string]any{"district": string(district)})
	}
	return a.districtCoverageLocked(district), nil
}

// districtCoverageLocked computes one district's coverage from its pushed
// records; the caller holds a.mu (RLock).
func (a *ServicesAPI) districtCoverageLocked(district DistrictID) DistrictCoverage {
	records := a.districtDemand[district]
	serviceIDs := sortedKeys(records)
	dc := DistrictCoverage{District: district}
	var totalQuality float64
	count := 0
	for _, id := range serviceIDs {
		inst, ok := a.instances[id]
		if !ok {
			// Unreachable through UpdateDistrictDemand (which rejects an
			// unknown service before writing the record), but kept defensive:
			// a torn/hand-edited state must never fabricate a zero-value
			// record (GR#16).
			continue
		}
		rec := records[id]
		dc.TotalCapacity += inst.capacityCeiling()
		dc.TotalDemand += rec.demand
		totalQuality += ComputeQuality(QualityInput{
			Funding:        inst.funding,
			Capacity:       inst.capacityCeiling(),
			Demand:         rec.demand,
			CoverageRadius: inst.spec.CoverageRadius,
			DemandDistance: rec.distance,
			StaffingRatio:  inst.staffingRatio(),
		})
		count++
	}
	dc.CoverageRatio = coverageRatio(dc.TotalCapacity, dc.TotalDemand)
	if count > 0 {
		dc.MeanQuality = totalQuality / float64(count)
	}
	return dc
}

// UpdateDistrictDemand records the caller-attributed per-district demand for
// a service (AC-21): the demand placed on service by district, plus the
// demand's distance from the service cell. The district identity is supplied
// by the caller — this package performs no spatial read. It rejects an empty
// DistrictID (ErrUnknownDistrict — no valid district identity), an
// unregistered ServiceID (ErrServiceNotRegistered), and a NaN/±Inf demand or
// distance (ErrNonFiniteInput, SEC-093).
func (a *ServicesAPI) UpdateDistrictDemand(district DistrictID, service ServiceID, demand, distance float64) error {
	if err := a.checkNotCopied("UpdateDistrictDemand"); err != nil {
		return err
	}
	if district == "" {
		return serviceErr(a.correlationID, ErrUnknownDistrict, map[string]any{"district": string(district)})
	}
	if !num.IsFinite(demand) {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "demand"})
	}
	if !num.IsFinite(distance) {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "distance"})
	}
	if demand < 0 {
		demand = 0
	}
	if distance < 0 {
		distance = 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.lookupLocked(service); err != nil {
		return err
	}
	if a.districtDemand[district] == nil {
		a.districtDemand[district] = make(map[ServiceID]demandRecord)
	}
	a.districtDemand[district][service] = demandRecord{demand: demand, distance: distance}
	return nil
}
