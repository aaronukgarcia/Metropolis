package services

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// incomeTaxBasisPointScale is the fixed-point denominator for the income
// tax rate engine.finance expresses as a BasisPoints value (10000 bp =
// 100%). This is a deliberate duplication of engine.finance's unexported
// basisPointScale — the value exists on both sides of the module boundary
// because finance does not export its scale, and the drift risk is held
// by TestIncomeTaxBasisPointScaleMatchesFinance (a _test.go import of
// finance that proves the two agree through the public CollectTax API).
const incomeTaxBasisPointScale int64 = 10000

// UnlockGate reports whether a §4 milestone tier has been reached. It is
// the shape this package consumes engine.unlocks's milestone state through
// (AC-7): engine.services never hardcodes a tier table, and the
// composition root wires the real engine.unlocks gate (or a test fake)
// here — exactly the seam engine.finance's MilestoneGate establishes for
// loan gating. A nil gate means "no milestone state available": SetFunding
// then fails closed for any service whose enabling building carries a
// milestone, rather than silently funding a not-yet-unlocked service.
type UnlockGate interface {
	// IsUnlocked reports whether tier has been reached.
	IsUnlocked(tier int) bool
}

// UnlockGateFunc adapts a plain function to UnlockGate, for tests and
// one-line composition-root wiring.
type UnlockGateFunc func(tier int) bool

// IsUnlocked implements UnlockGate.
func (f UnlockGateFunc) IsUnlocked(tier int) bool { return f(tier) }

// ServicesAPI is code.json's "engine.services" inbound contract
// (ServicesAPI, "per-service capacity/demand/coverage; funding sliders are
// commands"): the generic service model every later service-specific
// module (engine.education, engine.crime, engine.dispatch, engine.refuse,
// engine.social, engine.coastal, engine.prison, engine.comms) registers
// against, instead of N independent reimplementations of the same
// capacity/coverage/quality arithmetic.
//
// The zero value is not usable; construct via [New] or [Load]. A
// *ServicesAPI is safe for concurrent use: every mutable field is guarded
// by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020-class).
type ServicesAPI struct {
	mu            sync.RWMutex
	correlationID string

	kinds     map[ServiceKind]KindDef
	instances map[ServiceID]*serviceInstance
	// districtDemand is the caller-pushed per-district demand table:
	// district → service → (demand, distance) record (AC-21). The district
	// identity is supplied by the caller; this package performs no spatial
	// read.
	districtDemand map[DistrictID]map[ServiceID]demandRecord
	pools          []StaffingPool
	poolAvailable  map[string]float64
	pie            []PieBenchmark

	wagePerStaffMicropounds int64
	severityHalfPoint       float64

	gate UnlockGate

	// self is the SEC-020 copy guard, stored exactly once before the value
	// is returned to any caller.
	self atomic.Pointer[ServicesAPI]
}

// New constructs an empty, ready-to-register ServicesAPI with the built-in
// §10 service kinds registered and an empty staffing-pool / Pie table
// (those are loaded from data/services.json by [Load]). correlationID is
// attached to every error the returned API constructs (GR#1); an empty one
// mints a fresh ID.
func New(correlationID string) *ServicesAPI {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	a := &ServicesAPI{
		correlationID:  correlationID,
		kinds:          make(map[ServiceKind]KindDef, len(builtinKinds)),
		instances:      make(map[ServiceID]*serviceInstance),
		districtDemand: make(map[DistrictID]map[ServiceID]demandRecord),
		poolAvailable:  make(map[string]float64),
	}
	for _, k := range builtinKinds {
		a.kinds[k] = defaultKindDefs[k]
	}
	a.self.Store(a)
	return a
}

// Load reads and validates data/services.json (via [LoadServices]) and
// returns a ready-to-register *ServicesAPI whose staffing-pool table, Pie
// benchmark table, wage placeholder, and severity half-point are populated
// from that data (GR#15 — none of those figures are hardcoded in Go).
// Every failure is a registry-sourced *errs.E.
func Load(dir, correlationID string) (*ServicesAPI, error) {
	a := New(correlationID)

	f, err := LoadServices(dir, correlationID)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pools = f.StaffingPools
	a.pie = f.Pie.Benchmarks
	a.wagePerStaffMicropounds = f.WagePerStaffPerMonthMicropounds
	a.severityHalfPoint = f.Pie.SeverityHalfPointPopulation
	return a, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*ServicesAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// serviceErr builds a registry-sourced error under the given correlation ID
// (GR#7/GR#1). It is a package-level function (not a ServicesAPI method) so
// that checkNotCopied can call it without recursing, and so the astgate
// copy-guard gate does not treat it as an unguarded candidate-type method.
func serviceErr(correlationID, code string, ctx map[string]any) *errs.E {
	return errs.New(code, correlationID, ctx)
}

// checkNotCopied rejects a method call on a struct-copied *ServicesAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load.
func (a *ServicesAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return serviceErr(a.correlationID, ErrCopiedValue, map[string]any{"method": method})
	}
	return nil
}

// RegisterKind registers (or replaces the definition of) a service kind —
// the extensibility contract AC-2 names: a synthetic kind registered here
// is queryable through the same ServicesAPI methods as a built-in §10
// kind, so adding a new service category is a registration call, never a
// code change to this package.
func (a *ServicesAPI) RegisterKind(kind ServiceKind, def KindDef) error {
	if err := a.checkNotCopied("RegisterKind"); err != nil {
		return err
	}
	if kind == "" {
		return serviceErr(a.correlationID, ErrUnknownServiceKind, map[string]any{"kind": string(kind)})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kinds[kind] = def
	return nil
}

// KindDef returns the registered definition of a kind, and whether it is
// registered.
func (a *ServicesAPI) KindDef(kind ServiceKind) (KindDef, bool) {
	if err := a.checkNotCopied("KindDef"); err != nil {
		return KindDef{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	def, ok := a.kinds[kind]
	return def, ok
}

// RegisterService registers one service instance. It rejects (AC-11):
//   - a duplicate ServiceID (ErrDuplicateService — no silent overwrite);
//   - a ServiceKind with no registered KindDef (ErrUnknownServiceKind);
//   - any non-finite float field (ErrNonFiniteInput — SEC-093: a NaN/±Inf
//     coverage radius, staffing need, location, or capacity ceiling must
//     never be stored where the quality/staffing arithmetic would consume
//     it).
//
// A service instance's catalogue-sourced fields should be produced by
// [ServiceSpecFromBuilding] so capacity is sourced, not hand-authored
// (AC-10).
func (a *ServicesAPI) RegisterService(spec ServiceSpec) error {
	if err := a.checkNotCopied("RegisterService"); err != nil {
		return err
	}
	if field := nonFiniteSpecField(spec); field != "" {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": field})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("RegisterService"); err != nil {
		return err
	}
	if _, ok := a.kinds[spec.Kind]; !ok {
		return serviceErr(a.correlationID, ErrUnknownServiceKind, map[string]any{"kind": string(spec.Kind)})
	}
	if _, ok := a.instances[spec.ID]; ok {
		return serviceErr(a.correlationID, ErrDuplicateService, map[string]any{"service": string(spec.ID)})
	}
	a.instances[spec.ID] = &serviceInstance{spec: spec}
	return nil
}

// UnregisterService removes a registered service instance and every
// per-district demand record naming it (FEAT-build-services-bridge
// 2026-09-02, AC "demolition/offline deregisters"): the deterministic
// mirror of RegisterService for the demolition path — engine.build calls
// this when a completed service building is demolished, so a demolished
// fire station's capacity/coverage contribution disappears from the next
// CoverageSummary/CoverageByDistrict read exactly as its registration made
// it appear. Rejects an unregistered id with ErrServiceNotRegistered (the
// same code every other id-keyed query already uses) — never a silent
// no-op that could mask a caller tracking the wrong id (GR#1).
func (a *ServicesAPI) UnregisterService(id ServiceID) error {
	if err := a.checkNotCopied("UnregisterService"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.lookupLocked(id); err != nil {
		return err
	}
	delete(a.instances, id)
	// Clear every per-district record naming id — districtCoverageLocked
	// defensively skips a dangling id already, but leaving the record
	// behind would silently accumulate orphaned demand for a service that
	// no longer exists (GR#3: no stale duplicate of state that belongs
	// solely to the instances map).
	for _, records := range a.districtDemand {
		delete(records, id)
	}
	return nil
}

// nonFiniteSpecField returns the name of the first non-finite float field
// in spec, or "" when every float field is finite. It is RegisterService's
// SEC-093 boundary guard: NaN/±Inf never crosses the registration boundary
// into stored state.
func nonFiniteSpecField(spec ServiceSpec) string {
	switch {
	case !num.IsFinite(spec.CoverageRadius):
		return "coverageRadius"
	case !num.IsFinite(spec.X), !num.IsFinite(spec.Y):
		return "location"
	case !num.IsFinite(spec.StaffingNeed):
		return "staffingNeed"
	}
	for _, step := range spec.UpgradePath {
		if !num.IsFinite(step.CapacityCeiling) {
			return "upgradePath.capacityCeiling"
		}
	}
	return ""
}

// SetUnlockGate installs the milestone gate SetFunding consults (AC-7).
func (a *ServicesAPI) SetUnlockGate(g UnlockGate) error {
	if err := a.checkNotCopied("SetUnlockGate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gate = g
	return nil
}

// lookupLocked resolves a registered service instance; the caller holds
// a.mu (RLock or Lock).
func (a *ServicesAPI) lookupLocked(id ServiceID) (*serviceInstance, error) {
	if err := a.checkNotCopied("lookupLocked"); err != nil {
		return nil, err
	}
	inst, ok := a.instances[id]
	if !ok {
		return nil, serviceErr(a.correlationID, ErrServiceNotRegistered, map[string]any{"service": string(id)})
	}
	return inst, nil
}

// SetFunding is the funding-slider command (AC-1, AC-7, AC-12): it sets a
// service's funding level. A level outside [0,1] is rejected with
// ErrInvalidFunding (never silently clamped); a service whose enabling
// building's milestone tier is not reached is rejected with ErrNotUnlocked
// (via the injected UnlockGate); an unregistered service is rejected with
// ErrServiceNotRegistered.
func (a *ServicesAPI) SetFunding(id ServiceID, level float64) error {
	if err := a.checkNotCopied("SetFunding"); err != nil {
		return err
	}
	if !num.IsFinite(level) {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "level"})
	}
	if level < 0 || level > 1 {
		return serviceErr(a.correlationID, ErrInvalidFunding, map[string]any{"service": string(id), "level": level})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("SetFunding"); err != nil {
		return err
	}
	inst, err := a.lookupLocked(id)
	if err != nil {
		return err
	}
	if m := inst.currentMilestone(); m > 0 {
		if a.gate == nil || !a.gate.IsUnlocked(m) {
			return serviceErr(a.correlationID, ErrNotUnlocked, map[string]any{"service": string(id), "milestone": m})
		}
	}
	inst.funding = level
	return nil
}

// FundingLevel returns a service's current funding level (0..1).
func (a *ServicesAPI) FundingLevel(id ServiceID) (float64, error) {
	if err := a.checkNotCopied("FundingLevel"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	return inst.funding, nil
}

// Capacity returns a service's numeric capacity ceiling at its current
// upgrade step (AC-3, AC-9). An unregistered service is rejected with
// ErrServiceNotRegistered (AC-11), never a zero-value "empty service".
func (a *ServicesAPI) Capacity(id ServiceID) (float64, error) {
	if err := a.checkNotCopied("Capacity"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	return inst.capacityCeiling(), nil
}

// CapacityRaw returns a service's verbatim catalogue capacity string
// (data/buildings.json's capacityRaw, sourced via [ServiceSpecFromBuilding]
// — AC-10), never a re-authored duplicate.
func (a *ServicesAPI) CapacityRaw(id ServiceID) (string, error) {
	if err := a.checkNotCopied("CapacityRaw"); err != nil {
		return "", err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return "", err
	}
	return inst.spec.CapacityRaw, nil
}

// CoverageRadius returns a service's coverage radius (AC-3).
func (a *ServicesAPI) CoverageRadius(id ServiceID) (float64, error) {
	if err := a.checkNotCopied("CoverageRadius"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	return inst.spec.CoverageRadius, nil
}

// Demand returns a service's last-tick demand (set via UpdateDemand).
func (a *ServicesAPI) Demand(id ServiceID) (float64, error) {
	if err := a.checkNotCopied("Demand"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	return inst.demand, nil
}

// UpdateDemand is the per-tick demand command: it records the demand
// placed on a service and the demand's distance from the service cell, the
// two inputs the quality computation needs alongside the stored
// funding/capacity/staffing state.
func (a *ServicesAPI) UpdateDemand(id ServiceID, demand, distance float64) error {
	if err := a.checkNotCopied("UpdateDemand"); err != nil {
		return err
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
	inst, err := a.lookupLocked(id)
	if err != nil {
		return err
	}
	inst.demand = demand
	inst.demandDist = distance
	return nil
}

// UpdateStaffing is the per-tick staffing-need command: it sets a
// service's benchmark staffing requirement, the denominator the shared
// pool allocation (and the quality staffing factor) is measured against.
func (a *ServicesAPI) UpdateStaffing(id ServiceID, need float64) error {
	if err := a.checkNotCopied("UpdateStaffing"); err != nil {
		return err
	}
	if !num.IsFinite(need) {
		return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "need"})
	}
	if need < 0 {
		need = 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return err
	}
	inst.spec.StaffingNeed = need
	return nil
}

// Quality computes a service's realised quality from its current funding,
// capacity ceiling, staffing ratio, and the supplied demand + demand
// distance (set via UpdateDemand, or passed here directly). It is a pure
// function of simulation state (AC-14); the arithmetic lives in
// [ComputeQuality] (AC-3).
func (a *ServicesAPI) Quality(id ServiceID) (float64, error) {
	if err := a.checkNotCopied("Quality"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	return realizedQuality(inst), nil
}

// UpgradePath returns a service's §10 upgrade path (AC-9), the catalogue
// tier progression from its current building upward.
func (a *ServicesAPI) UpgradePath(id ServiceID) ([]UpgradeStep, error) {
	if err := a.checkNotCopied("UpgradePath"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return nil, err
	}
	out := make([]UpgradeStep, len(inst.spec.UpgradePath))
	copy(out, inst.spec.UpgradePath)
	return out, nil
}

// Upgrade advances a service to the next step of its upgrade path, raising
// its capacity ceiling (AC-9). Upgrading past the final step (or a service
// with no path) is rejected with ErrUpgradeUnavailable, never a silent
// no-op. The next step's §4 milestone tier is a live check against the
// injected UnlockGate (SEC-095): a tier-2 clinic cannot be upgraded to a
// tier-6 hospital while the gate only reaches tier 2, and a nil gate fails
// closed exactly as SetFunding does.
func (a *ServicesAPI) Upgrade(id ServiceID) error {
	if err := a.checkNotCopied("Upgrade"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return err
	}
	if inst.currentUpgrade+1 >= len(inst.spec.UpgradePath) {
		return serviceErr(a.correlationID, ErrUpgradeUnavailable, map[string]any{"service": string(id)})
	}
	next := inst.spec.UpgradePath[inst.currentUpgrade+1]
	if next.Milestone > 0 {
		if a.gate == nil || !a.gate.IsUnlocked(next.Milestone) {
			return serviceErr(a.correlationID, ErrNotUnlocked, map[string]any{"service": string(id), "milestone": next.Milestone})
		}
	}
	inst.currentUpgrade++
	return nil
}

// GrossWageCost returns a service's gross wage cost for the month: its
// staffing need × the civil-service wage placeholder (data/services.json's
// wagePerStaffPerMonthMicropounds), as engine.finance's int64 Money
// (micro-pounds). It is the numerator of §54's "public employees shown
// honestly as net cost" pair (AC-8).
func (a *ServicesAPI) GrossWageCost(id ServiceID) (finance.Money, error) {
	if err := a.checkNotCopied("GrossWageCost"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, err := a.lookupLocked(id)
	if err != nil {
		return 0, err
	}
	if inst.spec.StaffingNeed <= 0 {
		return 0, nil
	}
	return finance.Money(num.ClampInt64FromFloat(inst.spec.StaffingNeed * float64(a.wagePerStaffMicropounds))), nil
}

// NetFiscalCost returns a service's net fiscal cost: gross wage minus the
// income-tax clawback (gross × incomeRate), the §54 "the books show gross
// vs net" distinction (AC-8). The clawback is a live query of the passed
// incomeRate (an engine.finance BasisPoints value — the same fixed-point
// scale finance's CollectTax applies), never a baked-in constant; holding
// gross fixed and changing incomeRate changes the result by exactly
// gross × Δrate / 10000.
func (a *ServicesAPI) NetFiscalCost(id ServiceID, incomeRate finance.BasisPoints) (finance.Money, error) {
	if err := a.checkNotCopied("NetFiscalCost"); err != nil {
		return 0, err
	}
	gross, err := a.GrossWageCost(id)
	if err != nil {
		return 0, err
	}
	if gross <= 0 {
		return 0, nil
	}
	p, overflow := num.SafeMul(int64(gross), int64(incomeRate))
	if overflow {
		// The clawback intermediate (gross × rate) overflowed int64, so the
		// honest net cannot be computed — surfacing it is the only way to
		// keep "the books show gross vs net" a real distinction rather than a
		// net that silently reads ≈gross at a 100% rate (SEC-094).
		return 0, serviceErr(a.correlationID, ErrFiscalOverflow, map[string]any{
			"service": string(id),
			"gross":   int64(gross),
			"rate":    int64(incomeRate),
		})
	}
	clawback := p / incomeTaxBasisPointScale
	return finance.Money(num.SatSub(int64(gross), clawback)), nil
}

// SeverityHalfPoint returns the loaded §54 severity half-point population
// (data/services.json's severityHalfPointPopulation), the curve parameter
// ShortfallImpact's scale-dependence uses (AC-6).
func (a *ServicesAPI) SeverityHalfPoint() float64 {
	if err := a.checkNotCopied("SeverityHalfPoint"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.severityHalfPoint
}
