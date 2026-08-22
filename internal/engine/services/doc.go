// Package services is the generic service framework (MOD-033): one
// capacity/coverage/funding→quality model, the §26 shared staffing pools,
// and the §54 Public Service Pie, behind a single [ServicesAPI] that every
// later service-specific module registers against instead of
// reimplementing the same arithmetic.
//
// Module key: engine.services (see code.json; GUID ab443390-0a8a-459a-ae5a-4b8a35308751)
// Spec refs:  §10 (Service & Feature Inventory — the full gated service
// surface); §54 (The Fiscal Circuit — the Public Service Pie per-1k
// benchmark ratios and the village-mild→city-systemic consequence curve);
// §26 (Emergency & Care Dispatch — "elder care and home-care draw from the
// same staffing pool as hospitals, a nurse shortage is one shortage
// everywhere").
//
// # The generic service model (§10)
//
// A [ServiceKind] is the extensible identity of a service category; the
// built-in §10 kinds are registered by [New], and any further kind is
// added through [ServicesAPI.RegisterKind] — never a Go enum change. A
// registered service [ServiceSpec] carries a capacity figure (verbatim
// from data/buildings.json via [ServiceSpecFromBuilding], AC-10), a
// coverage radius, and a funding level; [ComputeQuality] turns funding,
// capacity-vs-demand, coverage-vs-distance, and staffing into a single
// [0,1] quality (AC-3). Funding is mutated only through the
// [ServicesAPI.SetFunding] command, never a public field setter (AC-1);
// the funding slider is a command per code.json's documented pattern.
//
// # Shared staffing pools (§26, AC-4)
//
// data/services.json's staffingPools table declares which kinds share a
// named pool (nursing: healthcare + elder-care + home-care). A shortage in
// the pool degrades quality for every member service simultaneously;
// [ServicesAPI.AllocateStaffing] splits a pool's available staff across
// its members in ascending ServiceID order (deterministic, AC-13).
//
// # The Public Service Pie (§54, AC-5/AC-6)
//
// data/services.json's pie.benchmarks table carries the default
// per-1,000-population benchmark staffing ratios (police ~2.4, teachers
// per pupil, nurses & GPs, dentists & opticians, firefighters, social
// workers, refuse crews, council officers). These are DEFAULT benchmarks,
// not hard requirements — the player deviates and feels the consequence.
// [ShortfallImpact] maps a relative shortfall to a severity that saturates
// with population, so a 10% cut is mild at 2k and systemic at 2M (AC-6).
// [ServicesAPI.GrossWageCost] / [ServicesAPI.NetFiscalCost] expose §54's
// "gross vs net" civil-service framing (AC-8).
//
// # Tier gating (§4, AC-7)
//
// [ServicesAPI.SetFunding] refuses to fund a service whose enabling
// building's milestone tier has not been reached, consulting the injected
// [UnlockGate] — the seam engine.unlocks implements — rather than a
// locally-duplicated milestone table (mirroring engine.finance's
// MilestoneGate).
//
// # Extensibility contract (US-1)
//
// To add a new service category, a later module:
//
//  1. registers a kind — [ServicesAPI.RegisterKind](ServiceKind("…"), KindDef{Name: "…", Benchmark: "…"});
//  2. declares its shared staffing pool in data/services.json (if it draws staff from one);
//  3. registers instances with [ServicesAPI.RegisterService] (sourcing capacity via [ServiceSpecFromBuilding]);
//  4. drives funding via [ServicesAPI.SetFunding] and demand via [ServicesAPI.UpdateDemand].
//
// Every failure is a registry-sourced *errs.E (MET-G12xx, this package's
// claimed sub-range — see errors.go); nothing in this package reads the
// wall clock (AC-14), and no map feeds an allocation/result order without
// being sorted first (AC-13/GR#21).
//
// # Coverage & enumeration aggregate (AC-18…AC-25)
//
// [ServicesAPI.ServiceIDs], [ServicesAPI.ServiceKinds], and
// [ServicesAPI.DistrictIDs] enumerate the registered surface in ascending
// order (never Go map order). [ServicesAPI.CoverageSummary] folds the whole
// city into one struct — ServiceCount, TotalCapacity (Σcapacity),
// TotalDemand, CoverageRatio, MeanQuality — and
// [ServicesAPI.CoverageByDistrict] / [ServicesAPI.CoverageForDistrict] give
// the same per-district, computed from caller-pushed demand records
// ([ServicesAPI.UpdateDistrictDemand]): the caller supplies district
// identity + demand, and this package performs no spatial read. The ratio is
// CoverageRatio = 1.0 when TotalDemand == 0, else clamp01(Σcapacity/Σdemand)
// — the capacity/demand fraction of demand served, mirroring
// [ComputeQuality]'s capacityFactor.
package services
