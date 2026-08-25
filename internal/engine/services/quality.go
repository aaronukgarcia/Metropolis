package services

// This file is the funding→quality core (§10, AC-3): a service's realised
// quality is a deterministic product of four independent factors — funding
// level, capacity vs demand, coverage vs demand distance, and staffing —
// so a shortfall in ANY one dimension degrades quality even when the other
// three are perfect. That is exactly AC-3's "funding alone doesn't
// determine quality if capacity/coverage are also short".

// QualityInput is the complete, self-contained input to [ComputeQuality].
// Every field is a pure simulation-state value (a funding fraction, a
// capacity figure, a demand figure, a distance) — never a wall-clock time
// (AC-14), never a hidden global.
type QualityInput struct {
	// Funding is the funding level as a fraction in [0,1] (0 = defunded,
	// 1 = fully funded).
	Funding float64
	// Capacity is the realised numeric capacity ceiling the service can
	// serve per tick (the ceiling its current upgrade step provides, AC-9).
	Capacity float64
	// Demand is the numeric demand placed on the service this tick.
	Demand float64
	// CoverageRadius is the spatial reach from the service building's cell.
	CoverageRadius float64
	// DemandDistance is the distance from the service cell to the demand.
	DemandDistance float64
	// StaffingRatio is allocated staff / staffing need, in [0,1] (1 = fully
	// staffed; below 1 the shared pool is short, §26/AC-4).
	StaffingRatio float64
}

// ComputeQuality returns the realised service quality in [0,1], the
// product of four clamped factors:
//
//	fundingFactor   = clamp(Funding, 0, 1)                        — the slider.
//	capacityFactor  = 1 if Demand <= Capacity, else Capacity/Demand — degraded only when demand exceeds capacity (AC-3).
//	coverageFactor  = 1 if DemandDistance <= CoverageRadius, else CoverageRadius/DemandDistance — degraded only out of coverage (AC-3).
//	staffingFactor  = clamp(StaffingRatio, 0, 1)                  — the shared-pool shortage penalty (AC-4).
//
// Zero-demand does not penalise capacity (nothing to serve ⇒ capacity is
// sufficient), and zero-distance demand is always in coverage. The result
// is clamped to [0,1] so a non-finite or out-of-domain input can never
// leak a value outside the documented quality domain (GR#16: never leak
// +Inf/NaN from a finite input).
func ComputeQuality(in QualityInput) float64 {
	fundingFactor := clamp01(in.Funding)

	capacityFactor := 1.0
	if in.Demand > in.Capacity {
		if in.Demand <= 0 {
			capacityFactor = 1
		} else {
			capacityFactor = in.Capacity / in.Demand
		}
	}

	coverageFactor := 1.0
	if in.DemandDistance > in.CoverageRadius {
		if in.CoverageRadius <= 0 {
			coverageFactor = 0
		} else {
			coverageFactor = in.CoverageRadius / in.DemandDistance
		}
	}

	staffingFactor := clamp01(in.StaffingRatio)

	return clamp01(fundingFactor * capacityFactor * coverageFactor * staffingFactor)
}

// clamp01 clamps v into [0,1]. A non-finite value collapses to 0 (it is
// never a quality we can report as "fine"), and a value above 1 collapses
// to 1 (over-funding/over-staffing never yields quality above 100%).
func clamp01(v float64) float64 {
	if v != v || v < 0 { // NaN or negative
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// realizedQuality computes one service instance's current realised quality
// from its stored state — the single place that assembles a [QualityInput]
// from an instance's funding/capacity/demand/coverage/staffing fields, used
// by both [ServicesAPI.Quality] and the coverage aggregate (GR#3: one source
// of truth for "quality of this instance right now").
func realizedQuality(inst *serviceInstance) float64 {
	return ComputeQuality(QualityInput{
		Funding:        inst.funding,
		Capacity:       inst.capacityCeiling(),
		Demand:         inst.demand,
		CoverageRadius: inst.spec.CoverageRadius,
		DemandDistance: inst.demandDist,
		StaffingRatio:  inst.staffingRatio(),
	})
}
