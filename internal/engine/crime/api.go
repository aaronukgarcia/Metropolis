package crime

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CrimeAPI is code.json's "engine.crime" inbound contract
// (GUID ec4eb047-5101-496f-b9c4-760e21846954, "CrimeAPI",
// "generation/deterrence/clearance decomposed and drill-through"): the §28
// per-district, per-type crime generation, the concave deterrence +
// detective clearance + prevention triad, the stateful gang lifecycle, the
// Constabulary-HQ-gated strategy mix, the MI5-analogue threat dial, and the
// justice-chain conservation pipeline — all behind this single API,
// consumed by engine.census and engine.prison through this interface alone.
//
// The zero value is not usable; construct via [New]. A *CrimeAPI is safe
// for concurrent use (AC-18): every mutable field is guarded by mu, and
// checkNotCopied rejects a method call on a struct-copied value (SEC-020
// family, mirroring engine.households' HouseholdsAPI).
type CrimeAPI struct {
	correlationID string
	seed          uint64
	cfg           config

	mu sync.RWMutex

	districts  map[DistrictID]*districtState
	gangs      map[GangID]*Gang
	nextGangID GangID

	constabularyHQBuilt bool
	mix                 StrategyMix

	threat   threatState
	security SecurityInput

	prison PrisonIntake

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[CrimeAPI]
}

// districtState is one registered district's decomposed month snapshot.
type districtState struct {
	id     DistrictID
	inputs DistrictInput

	generation [numCrimeTypes]float64 // effective new generation per type (AC-1/AC-2)
	rawGen     [numCrimeTypes]float64 // driver-driven generation before deterrence/prevention
	persisted  [numCrimeTypes]float64 // recurrence/persistence metric (AC-5)
	active     [numCrimeTypes]float64 // persistent active-crime stock

	deterrence         float64 // deterrence reduction (AC-4)
	clearance          float64 // clearance rate (AC-5)
	prevention         float64 // prevention reduction (AC-5)
	effectiveClearance float64 // clearance softened by backlog pressure (AC-13)

	sustainedMonths int // consecutive months gang-formation conditions held (AC-6)

	// eligiblePool is this month's live queryable eligible pool (AC-2/AC-3's
	// generation driver, and AC-7d's recruitment-reduced figure): recomputed
	// every AdvanceMonth as max(0, thisMonth'sPushedEligiblePool -
	// recruitedCumulative), then further reduced in-month by any recruitment
	// applyGangEffectsLocked applies this same tick (queryable via
	// EligiblePool for the remainder of the month, until the next push).
	//
	// BUG (destructive round r1, FEAT-167 independent attack) FIXED here:
	// the ORIGINAL design seeded eligiblePool from DistrictInput.EligiblePool
	// only on first sight (`if st.eligiblePool == 0`) and never again — a
	// live monthly population push after month 1 silently stopped affecting
	// Safety/generation entirely, and the pool could only ever fall (never
	// track a real population's growth or, after a later shrink, recover).
	// Reseeding is now unconditional and designed (recruitedCumulative is
	// the ONLY thing that discounts the live push), not an incidental side
	// effect of the pool happening to read exactly zero (which, under the
	// old `==0` gate, wrongly re-triggered a full reseed and threw away
	// every prior month's recruitment deduction).
	eligiblePool int64

	// recruitedCumulative is the district's running total of eligible-pool
	// members gang recruitment has ever drawn off (AC-7d), independent of
	// which specific gang did the recruiting and surviving across gang
	// formation/removal/respawn cycles. It is the one persistent quantity
	// eligiblePool's monthly recompute discounts the live pushed population
	// by, so two states with identical (pushedPopulation,
	// recruitmentHistory) always converge to the identical eligiblePool/
	// Safety regardless of the population's growth PATH that got them there
	// (the destructive round's convergence attack).
	recruitedCumulative int64

	justice justiceState
}

// New constructs a CrimeAPI from a world seed (used for every counter-based
// hash draw — AC-16) and a correlation ID (GR#1). The balance config is
// loaded from the embedded crime.json (GR#15); an invalid embedded config is
// a fatal registry-sourced error, never a silent default.
func New(seed uint64, correlationID string) (*CrimeAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := loadConfig(correlationID)
	if err != nil {
		return nil, err
	}
	a := &CrimeAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		districts:     make(map[DistrictID]*districtState),
		gangs:         make(map[GangID]*Gang),
		nextGangID:    1,
		mix: StrategyMix{
			Patrol:    cfg.Mix.DefaultPatrol,
			Detective: cfg.Mix.DefaultDetective,
			Community: cfg.Mix.DefaultCommunity,
		},
	}
	// Armed exactly once, before a is returned to any caller (SEC-020).
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *CrimeAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *CrimeAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// BuildConstabularyHQ builds the top rung of the §28 command ladder,
// unlocking the citywide strategy mix (AC-10). A station or Divisional HQ
// alone does NOT unlock it — this is the only facility state that does.
func (a *CrimeAPI) BuildConstabularyHQ() error {
	if err := a.checkNotCopied("BuildConstabularyHQ"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.constabularyHQBuilt = true
	return nil
}

// ConstabularyHQBuilt reports whether the command-ladder gate is open.
func (a *CrimeAPI) ConstabularyHQBuilt() bool {
	if err := a.checkNotCopied("ConstabularyHQBuilt"); err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.constabularyHQBuilt
}

// SetStrategyMix sets the citywide patrol/detective/community weighting
// (AC-10). It is rejected with ErrNoConstabularyHQ while no Constabulary
// Headquarters is built, and rejected with ErrInvalidMix when the three
// weights do not sum to the documented total (AC-15) — never silently
// dropped or silently renormalised.
func (a *CrimeAPI) SetStrategyMix(m StrategyMix) error {
	if err := a.checkNotCopied("SetStrategyMix"); err != nil {
		return err
	}
	if !num.IsFinite(m.Patrol) || m.Patrol < 0 ||
		!num.IsFinite(m.Detective) || m.Detective < 0 ||
		!num.IsFinite(m.Community) || m.Community < 0 {
		return errs.New(ErrInvalidMix, a.correlationID, map[string]any{
			"sum": m.Patrol + m.Detective + m.Community, "total": a.cfg.Mix.Total,
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.constabularyHQBuilt {
		return errs.New(ErrNoConstabularyHQ, a.correlationID, nil)
	}
	if !approxEq(m.Patrol+m.Detective+m.Community, a.cfg.Mix.Total) {
		return errs.New(ErrInvalidMix, a.correlationID, map[string]any{
			"sum": m.Patrol + m.Detective + m.Community, "total": a.cfg.Mix.Total,
		})
	}
	a.mix = m
	return nil
}

// StrategyMix returns the current citywide strategy mix.
func (a *CrimeAPI) StrategyMix() StrategyMix {
	if err := a.checkNotCopied("StrategyMix"); err != nil {
		return StrategyMix{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mix
}

// RegisterDistrict registers a district id as existing (so subsequent
// queries for it are valid). AdvanceMonth also registers every district it
// processes. A query for a never-registered district returns
// ErrUnregisteredDistrict, never a silently-created zero entry (AC-14).
func (a *CrimeAPI) RegisterDistrict(id DistrictID) error {
	if err := a.checkNotCopied("RegisterDistrict"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureDistrictLocked(id)
	return nil
}

// SetPrisonIntake wires the (future) engine.prison intake ledger seam
// (AC-12). It is read by VerifyPrisonIntake as the independent party in the
// sentenced-to-prison cross-check.
func (a *CrimeAPI) SetPrisonIntake(p PrisonIntake) error {
	if err := a.checkNotCopied("SetPrisonIntake"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prison = p
	return nil
}

// ensureDistrictLocked returns the districtState for id, creating it if
// absent. Callers must hold a.mu. District registration is explicit (via
// AdvanceMonth or RegisterDistrict), never a silent side-effect of a query.
func (a *CrimeAPI) ensureDistrictLocked(id DistrictID) *districtState {
	if err := a.checkNotCopied("ensureDistrictLocked"); err != nil {
		return nil
	}
	st, ok := a.districts[id]
	if !ok {
		st = &districtState{id: id}
		a.districts[id] = st
	}
	return st
}

// districtLocked returns the districtState for id, or nil if unregistered.
// Callers must hold a.mu (read or write).
func (a *CrimeAPI) districtLocked(id DistrictID) *districtState {
	if err := a.checkNotCopied("districtLocked"); err != nil {
		return nil
	}
	return a.districts[id]
}

// sortedDistrictIDs returns the registered district ids in ascending order
// (never a map range — determinism, GR#21).
func (a *CrimeAPI) sortedDistrictIDs() []DistrictID {
	if err := a.checkNotCopied("sortedDistrictIDs"); err != nil {
		return nil
	}
	ids := make([]DistrictID, 0, len(a.districts))
	for id := range a.districts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// AdvanceMonth advances the whole module one simulated month: per-district
// generation (from the pushed driver inputs), the policing triad, the gang
// lifecycle, the justice chain, and the citywide threat dial (AC-11). It is
// the single mutation path — accessors never mutate.
func (a *CrimeAPI) AdvanceMonth(month int64, districts []DistrictInput, security SecurityInput) error {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	if month < 0 {
		return errs.New(ErrInvalidDistrictInput, a.correlationID, map[string]any{
			"district": "city", "field": "month", "value": month,
		})
	}
	if err := validateSecurityInput(security, a.correlationID); err != nil {
		return err
	}
	for _, d := range districts {
		if err := validateDistrictInput(d, a.correlationID); err != nil {
			return err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.security = security

	// Process districts in sorted id order so the threat-dial aggregate (and
	// any future cross-district read) is deterministic regardless of input
	// slice order (GR#21).
	sorted := append([]DistrictInput(nil), districts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].District < sorted[j].District })

	for _, d := range sorted {
		st := a.ensureDistrictLocked(d.District)
		st.inputs = d
		// Recomputed every month from the LIVE pushed pool, discounted by
		// the district's persistent recruitment history — never seeded once
		// and left stale (destructive round r1 fix, see districtState's
		// eligiblePool doc comment above). The composition root owns
		// population; this module reports only the recruitment-driven
		// reduction, via recruitedCumulative, against whatever pool value
		// is pushed each month.
		pool := d.EligiblePool - st.recruitedCumulative
		if pool < 0 {
			pool = 0
		}
		st.eligiblePool = pool
		a.advanceDistrictLocked(month, st, d)
	}

	a.advanceThreatLocked(month)

	return nil
}

// advanceDistrictLocked runs one district's month: generation, the policing
// triad, the justice chain (which updates the backlog), the softened
// effective clearance, and the gang lifecycle.
func (a *CrimeAPI) advanceDistrictLocked(month int64, st *districtState, in DistrictInput) {
	if err := a.checkNotCopied("advanceDistrictLocked"); err != nil {
		return
	}
	cfg := a.cfg
	drivers := driverValues(in)

	patrol := in.PatrolCoverage * a.mix.Patrol
	detectives := in.DetectiveCapacity * a.mix.Detective
	preventionInfra := in.PreventionInfrastructure * a.mix.Community

	deterrence := DeterrenceFor(patrol, cfg.Deterrence.HalfSaturationCoverage)
	clearance := ClearanceFor(detectives, cfg.Clearance.RatePerDetective, cfg.Clearance.MaxRate)
	prevention := PreventionFor(preventionInfra, cfg.Prevention.ScalePerInfrastructure)

	// Gang crime uplift raises every local crime type in its territory
	// (AC-7b), not only organised crime.
	uplift := 1.0
	if a.gangInDistrictLocked(st.id) != nil {
		uplift = 1 + cfg.Gangs.CrimeUplift
	}

	totalActive := 0.0
	for _, t := range crimeTypeKeys {
		raw := rawGeneration(cfg, t, drivers, st.eligiblePool) * uplift
		gen := effectiveGeneration(raw, deterrence, prevention)
		st.rawGen[t] = raw
		st.generation[t] = gen
		persisted := st.active[t] * (1 - clearance)
		st.persisted[t] = persisted
		st.active[t] = persisted + gen
		totalActive += st.active[t]
	}

	st.deterrence = deterrence
	st.clearance = clearance
	st.prevention = prevention

	// Justice chain first (it updates the backlog), then the softened
	// effective clearance (AC-13's release feeds the gang-formation driver
	// this same month), then gangs.
	a.advanceJusticeLocked(month, st, in, totalActive)
	st.effectiveClearance = a.effectiveClearanceLocked(st)
	a.advanceGangsLocked(month, st, in)
}

// gangInDistrictLocked returns the formed gang currently holding the given
// district, or nil. Callers must hold a.mu.
func (a *CrimeAPI) gangInDistrictLocked(id DistrictID) *Gang {
	if err := a.checkNotCopied("gangInDistrictLocked"); err != nil {
		return nil
	}
	for _, g := range a.gangs {
		if g.District == id {
			return g
		}
	}
	return nil
}

// effectiveClearanceLocked computes the clearance figure softened by the
// district's courthouse backlog (AC-13): a large awaiting-trial backlog is
// itself a low-clearance signal, and releasing offenders from it (AC-13)
// measurably raises the effective clearance AC-6's gang formation reads.
func (a *CrimeAPI) effectiveClearanceLocked(st *districtState) float64 {
	if err := a.checkNotCopied("effectiveClearanceLocked"); err != nil {
		return 0
	}
	backlog := float64(len(st.justice.backlog))
	pressure := clampUnit(backlog / (backlog + a.cfg.Justice.BacklogPressureHalfSaturation))
	return st.clearance * (1 - pressure)
}

// totalActiveCrimeLocked sums a district's active crime stock (deterministic
// fixed type order).
func totalActiveCrimeLocked(st *districtState) float64 {
	total := 0.0
	for _, t := range crimeTypeKeys {
		total += st.active[t]
	}
	return total
}

// --- accessors (all read-only, all copy-guarded) ---

// requireDistrict returns the districtState for id or a registry-sourced
// ErrUnregisteredDistrict error. Callers must hold a.mu.
func (a *CrimeAPI) requireDistrict(id DistrictID) (*districtState, error) {
	if err := a.checkNotCopied("requireDistrict"); err != nil {
		return nil, err
	}
	st := a.districtLocked(id)
	if st == nil {
		return nil, errs.New(ErrUnregisteredDistrict, a.correlationID, map[string]any{"district": id})
	}
	return st, nil
}

// Generation returns the per-district, per-type effective new generation
// this month (AC-1): independently queryable per type, drill-through to
// Deterrence/Prevention.
func (a *CrimeAPI) Generation(id DistrictID, t CrimeType) (float64, error) {
	if err := a.checkNotCopied("Generation"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.generation[t], nil
}

func (a *CrimeAPI) typeAccessor(id DistrictID, t CrimeType) (float64, error) {
	if err := a.checkNotCopied(typeAccessorMethod(t)); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.generation[t], nil
}

// The nine named per-type accessors (AC-2). Each returns the district's
// per-type generation sub-figure; the nine are tracked as distinct values,
// never a blended index split for display.

// PettyTheft returns the petty-theft generation figure for the district.
func (a *CrimeAPI) PettyTheft(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("PettyTheft"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimePettyTheft)
}

// Burglary returns the burglary generation figure for the district.
func (a *CrimeAPI) Burglary(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("Burglary"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeBurglary)
}

// VehicleCrime returns the vehicle-crime generation figure for the district.
func (a *CrimeAPI) VehicleCrime(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("VehicleCrime"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeVehicleCrime)
}

// CriminalDamage returns the criminal-damage generation figure.
func (a *CrimeAPI) CriminalDamage(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("CriminalDamage"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeCriminalDamage)
}

// ViolentCrime returns the violent-crime generation figure.
func (a *CrimeAPI) ViolentCrime(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("ViolentCrime"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeViolent)
}

// DrugsSupply returns the drugs-supply generation figure.
func (a *CrimeAPI) DrugsSupply(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("DrugsSupply"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeDrugsSupply)
}

// OrganisedCrime returns the organised-crime generation figure.
func (a *CrimeAPI) OrganisedCrime(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("OrganisedCrime"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeOrganised)
}

// FraudCyber returns the fraud/cyber generation figure.
func (a *CrimeAPI) FraudCyber(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("FraudCyber"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeFraudCyber)
}

// Smuggling returns the smuggling generation figure.
func (a *CrimeAPI) Smuggling(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("Smuggling"); err != nil {
		return 0, err
	}
	return a.typeAccessor(id, CrimeSmuggling)
}

// Recurrence returns the persistence/recurrence metric (AC-5): the active
// stock carried over after clearance — the figure detective clearance
// suppresses independently of the generation term.
func (a *CrimeAPI) Recurrence(id DistrictID, t CrimeType) (float64, error) {
	if err := a.checkNotCopied("Recurrence"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.persisted[t], nil
}

// ActiveCrime returns the district's current active-crime stock for a type.
func (a *CrimeAPI) ActiveCrime(id DistrictID, t CrimeType) (float64, error) {
	if err := a.checkNotCopied("ActiveCrime"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.active[t], nil
}

// Deterrence returns the district's deterrence reduction (AC-4 drill-through).
func (a *CrimeAPI) Deterrence(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("Deterrence"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.deterrence, nil
}

// Clearance returns the district's clearance rate (AC-5 drill-through).
func (a *CrimeAPI) Clearance(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("Clearance"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.clearance, nil
}

// Prevention returns the district's prevention reduction (AC-5 drill-through).
func (a *CrimeAPI) Prevention(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("Prevention"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.prevention, nil
}

// EffectiveClearance returns the district's clearance softened by its
// courthouse backlog (AC-13's downstream driver for AC-6's gang formation).
func (a *CrimeAPI) EffectiveClearance(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("EffectiveClearance"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.effectiveClearance, nil
}

// EligiblePool returns the district's remaining eligible-pool figure (the
// pool AC-2/AC-3 drivers read from, reduced by gang recruitment, AC-7d).
func (a *CrimeAPI) EligiblePool(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("EligiblePool"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return st.eligiblePool, nil
}

// SafetyTerm returns the queryable safety-shaped output for engine.attract's
// pushed-input surface (US-6): a [0,100] figure, higher = safer, a monotonic
// inversion of the district's active crime stock.
func (a *CrimeAPI) SafetyTerm(id DistrictID) (float64, error) {
	if err := a.checkNotCopied("SafetyTerm"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	total := totalActiveCrimeLocked(st)
	pressure := clampUnit(total / (total + a.cfg.Safety.HalfSaturationActiveCrime))
	return 100 * (1 - pressure), nil
}

// --- gang accessors ---

// Gang returns the formed gang with the given id, and whether it exists.
func (a *CrimeAPI) Gang(id GangID) (Gang, bool) {
	if err := a.checkNotCopied("Gang"); err != nil {
		return Gang{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	g, ok := a.gangs[id]
	if !ok {
		return Gang{}, false
	}
	return *g, true
}

// GangIDs returns the ids of every formed gang, ascending (deterministic).
func (a *CrimeAPI) GangIDs() []GangID {
	if err := a.checkNotCopied("GangIDs"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]GangID, 0, len(a.gangs))
	for id := range a.gangs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// --- threat-dial accessors (AC-11) ---

// ThreatLevel returns the current MI5-analogue threat level (queryable,
// [0, maxLevel]).
func (a *CrimeAPI) ThreatLevel() float64 {
	if err := a.checkNotCopied("ThreatLevel"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.threat.level
}

// ThreatRisingStreak returns the number of consecutive months the threat
// level has been nonzero (the visible, queryable "nonzero and rising"
// precursor readout AC-11 requires before any event may fire).
func (a *CrimeAPI) ThreatRisingStreak() int {
	if err := a.checkNotCopied("ThreatRisingStreak"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.threat.elevatedMonths
}

// LastThreatEventMonth returns the month the last terror-threat event fired,
// or 0 if none ever has (AC-11: an event is always preceded by the lead
// window within the same run).
func (a *CrimeAPI) LastThreatEventMonth() int64 {
	if err := a.checkNotCopied("LastThreatEventMonth"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.threat.lastEventMonth
}

// TriggerProbability returns the current terror-threat trigger probability —
// a pure function of exposure, Security Service funding, and liaison level
// (AC-11), NOT an unconditioned per-tick draw.
func (a *CrimeAPI) TriggerProbability() float64 {
	if err := a.checkNotCopied("TriggerProbability"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return TriggerProbabilityFor(a.security.Exposure, a.security.Funding, a.security.Liaison, a.cfg.Threat)
}

// validateDistrictInput rejects a non-finite or negative driver/count field
// with no state change (GR#16). Driver fractions are coerced to [0,1] by
// clampUnit at use, but a negative count or a non-finite fraction is a
// caller error, not something to silently absorb.
func validateDistrictInput(in DistrictInput, correlationID string) error {
	bad := func(field string, v any) error {
		return errs.New(ErrInvalidDistrictInput, correlationID, map[string]any{
			"district": in.District, "field": field, "value": v,
		})
	}
	fields := []struct {
		name string
		v    float64
	}{
		{"ownDeprivation", in.OwnDeprivation},
		{"neighbourWealth", in.NeighbourWealth},
		{"youthUnemployment", in.YouthUnemployment},
		{"blight", in.Blight},
		{"youthLeisureDesert", in.YouthLeisureDesert},
		{"policePresence", in.PolicePresence},
		{"eraWealth", in.EraWealth},
		{"portThroughput", in.PortThroughput},
		{"customsFunding", in.CustomsFunding},
		{"patrolCoverage", in.PatrolCoverage},
		{"detectiveCapacity", in.DetectiveCapacity},
		{"preventionInfrastructure", in.PreventionInfrastructure},
		{"regenerationInvestment", in.RegenerationInvestment},
		{"prisonAbsorption", in.PrisonAbsorption},
	}
	for _, f := range fields {
		if !num.IsFinite(f.v) {
			return bad(f.name, f.v)
		}
	}
	if in.EligiblePool < 0 {
		return bad("eligiblePool", in.EligiblePool)
	}
	if in.CourthouseThroughput < 0 {
		return bad("courthouseThroughput", in.CourthouseThroughput)
	}
	return nil
}

func validateSecurityInput(in SecurityInput, correlationID string) error {
	fields := []struct {
		name string
		v    float64
	}{
		{"exposure", in.Exposure},
		{"funding", in.Funding},
		{"liaison", in.Liaison},
	}
	for _, f := range fields {
		if !num.IsFinite(f.v) {
			return errs.New(ErrInvalidDistrictInput, correlationID, map[string]any{
				"district": "city", "field": "security." + f.name, "value": f.v,
			})
		}
	}
	return nil
}

// typeAccessorMethod returns the method name for a named type accessor —
// used only for the copy-guard's "method" context, kept deterministic.
func typeAccessorMethod(t CrimeType) string {
	switch t {
	case CrimePettyTheft:
		return "PettyTheft"
	case CrimeBurglary:
		return "Burglary"
	case CrimeVehicleCrime:
		return "VehicleCrime"
	case CrimeCriminalDamage:
		return "CriminalDamage"
	case CrimeViolent:
		return "ViolentCrime"
	case CrimeDrugsSupply:
		return "DrugsSupply"
	case CrimeOrganised:
		return "OrganisedCrime"
	case CrimeFraudCyber:
		return "FraudCyber"
	case CrimeSmuggling:
		return "Smuggling"
	default:
		return "TypeAccessor"
	}
}

// detStream derives a counter-based stream for a purpose-tagged draw within
// a district/month — the single route to every stochastic draw (AC-16). It
// is a pure package-level function (no *CrimeAPI receiver) so the copy-guard
// gate does not treat it as a reachable method needing a guard.
func detStream(seed uint64, id DistrictID, month int64, purpose string) det.Stream {
	return det.NewStream(seed, uint64(id), month, purpose)
}
