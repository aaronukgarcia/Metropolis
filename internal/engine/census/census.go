package census

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Snapshot is the census's committed, immutable view of resolved tick
// state (US-1): a deep copy of everything the four observation threads
// need, captured once from the seven consumed modules' query surfaces
// before any thread runs. The threads read only this snapshot and write
// only to the census's own output surfaces — never back into a consumed
// module (AC-2, GR#21).
type Snapshot struct {
	Tick     int64
	Citizens []CitizenView // sorted by id
	// Education and Income are per-citizen joins keyed by citizen id.
	Education map[uint64]EducationView
	Income    map[uint64]int64

	CrimeRate     float64
	Happiness     float64
	UnfedFraction float64

	HospitalWaiting int64
	UnfilledJobs    int64
	JobSkillDemand  int64

	EducationPolicyCoefficient float64

	GDPFlows  int64
	LandValue int64
}

// CensusAPI is code.json's "engine.census" inbound contract (MOD-078): the
// read/query surface over the four non-blocking observation threads (stats
// generator, auditor, consistency checker, regulator), the cradle-to-grave
// bio assembly, and the demographics/KPI + drill-in data model. It consumes
// engine.citizens/education/crime/wellbeing/services/policies/finance
// through their registered interfaces alone (GR#20) and never re-implements
// any of them.
//
// The zero value is not usable; construct via [New] or [Load]. A *CensusAPI
// is safe for concurrent use (AC-25): every mutable field is guarded by mu,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020 family).
type CensusAPI struct {
	correlationID string
	cfg           Config

	// The seven consumed sources, wired via Wire* and read under mu.
	citizens  CitizensSource
	education EducationSource
	crime     CrimeSource
	wellbeing WellbeingSource
	services  ServicesSource
	policies  PoliciesSource
	finance   FinanceSource

	mu sync.RWMutex

	// census-owned output surfaces (written only by the observation
	// threads, read by the query surfaces — never a consumed module).
	latest   Aggregates
	history  []HistoryPoint
	tracked  map[GUID]*checkInRecord
	findings []Finding
	lost     []LostObject

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[CensusAPI]
}

// New constructs an empty CensusAPI from a validated Config. correlationID
// is attached to every error this (and the returned API's methods)
// construct (GR#1). The seven sources are wired later via Wire*.
func New(cfg Config, correlationID string) (*CensusAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	c := &CensusAPI{
		correlationID: correlationID,
		cfg:           cfg,
		tracked:       make(map[GUID]*checkInRecord),
	}
	c.self.Store(c)
	return c, nil
}

// Load reads and validates data/census.json from dir and constructs a
// ready-to-wire *CensusAPI.
func Load(dir, correlationID string) (*CensusAPI, error) {
	cfg, err := LoadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(cfg, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *CensusAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load.
func (c *CensusAPI) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return errs.New(ErrCopiedValue, c.correlationID, map[string]any{"method": method})
	}
	return nil
}

// WireCitizens wires the engine.citizens query surface.
func (c *CensusAPI) WireCitizens(s CitizensSource) error {
	if err := c.checkNotCopied("WireCitizens"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.citizens = s
	return nil
}

// WireEducation wires the engine.education query surface.
func (c *CensusAPI) WireEducation(s EducationSource) error {
	if err := c.checkNotCopied("WireEducation"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.education = s
	return nil
}

// WireCrime wires the engine.crime query surface.
func (c *CensusAPI) WireCrime(s CrimeSource) error {
	if err := c.checkNotCopied("WireCrime"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crime = s
	return nil
}

// WireWellbeing wires the engine.wellbeing query surface.
func (c *CensusAPI) WireWellbeing(s WellbeingSource) error {
	if err := c.checkNotCopied("WireWellbeing"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wellbeing = s
	return nil
}

// WireServices wires the engine.services query surface.
func (c *CensusAPI) WireServices(s ServicesSource) error {
	if err := c.checkNotCopied("WireServices"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services = s
	return nil
}

// WirePolicies wires the engine.policies query surface.
func (c *CensusAPI) WirePolicies(s PoliciesSource) error {
	if err := c.checkNotCopied("WirePolicies"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policies = s
	return nil
}

// WireFinance wires the engine.finance query surface.
func (c *CensusAPI) WireFinance(s FinanceSource) error {
	if err := c.checkNotCopied("WireFinance"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finance = s
	return nil
}

// requireSources returns ErrDependencyMissing if any of the seven consumed
// sources is unwired. The census fails closed rather than silently
// reporting a zero aggregate from an absent source (GR#1).
func (c *CensusAPI) requireSources(operation string) error {
	missing := func(name string) error {
		return errs.New(ErrDependencyMissing, c.correlationID, map[string]any{
			"operation":  operation,
			"dependency": name,
		})
	}
	if c.citizens == nil {
		return missing("citizens")
	}
	if c.education == nil {
		return missing("education")
	}
	if c.crime == nil {
		return missing("crime")
	}
	if c.wellbeing == nil {
		return missing("wellbeing")
	}
	if c.services == nil {
		return missing("services")
	}
	if c.policies == nil {
		return missing("policies")
	}
	if c.finance == nil {
		return missing("finance")
	}
	return nil
}

// snapshotSources returns the current source pointers under the lock, then
// the caller reads them outside it (each source holds its own lock).
func (c *CensusAPI) snapshotSources() (CitizensSource, EducationSource, CrimeSource, WellbeingSource, ServicesSource, PoliciesSource, FinanceSource) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.citizens, c.education, c.crime, c.wellbeing, c.services, c.policies, c.finance
}

// Snapshot captures a committed, deep-copied view of resolved tick state by
// reading the seven consumed modules' query surfaces. The returned snapshot
// is immutable — the four threads read it, and only it, when they run.
func (c *CensusAPI) Snapshot(tick int64, correlationID string) (*Snapshot, error) {
	if err := c.checkNotCopied("Snapshot"); err != nil {
		return nil, err
	}
	if err := c.requireSources("Snapshot"); err != nil {
		return nil, err
	}
	citizens, education, crime, wellbeing, services, policies, finance := c.snapshotSources()

	snap := &Snapshot{
		Tick:      tick,
		Education: make(map[uint64]EducationView),
		Income:    make(map[uint64]int64),
		Citizens:  []CitizenView{},
	}

	views, err := citizens.AllCitizens(correlationID)
	if err != nil {
		return nil, err
	}
	// Defensive copy + deterministic id order (GR#21).
	snap.Citizens = append([]CitizenView(nil), views...)
	sortCitizens(snap.Citizens)

	for _, cv := range snap.Citizens {
		if ev, ok := education.EducationFor(cv.ID, correlationID); ok {
			// Reject an out-of-domain stage at the boundary (SEC-126) and
			// deep-copy the trajectory so the snapshot never aliases the
			// source's backing array (SEC-128).
			if err := validateStages(ev.Stages, cv.ID, correlationID); err != nil {
				return nil, err
			}
			ev.Stages = cloneStages(ev.Stages)
			snap.Education[cv.ID] = ev
		}
		if inc, ok := finance.IncomeFor(cv.ID, correlationID); ok {
			snap.Income[cv.ID] = inc
		}
	}

	crimeRate, err := crime.CityCrimeRate(correlationID)
	if err != nil {
		return nil, err
	}
	if snap.CrimeRate, err = safeSourceFloat(crimeRate, "crime.CityCrimeRate", correlationID); err != nil {
		return nil, err
	}
	happiness, err := wellbeing.HeadlineHappiness(correlationID)
	if err != nil {
		return nil, err
	}
	if snap.Happiness, err = safeSourceFloat(happiness, "wellbeing.HeadlineHappiness", correlationID); err != nil {
		return nil, err
	}
	unfed, err := wellbeing.UnfedFraction(correlationID)
	if err != nil {
		return nil, err
	}
	if snap.UnfedFraction, err = safeSourceFloat(unfed, "wellbeing.UnfedFraction", correlationID); err != nil {
		return nil, err
	}
	if snap.HospitalWaiting, err = services.HospitalWaitingList(correlationID); err != nil {
		return nil, err
	}
	if snap.UnfilledJobs, err = services.UnfilledJobs(correlationID); err != nil {
		return nil, err
	}
	if snap.JobSkillDemand, err = services.JobSkillDemand(correlationID); err != nil {
		return nil, err
	}
	coef, err := policies.EducationPolicyCoefficient(correlationID)
	if err != nil {
		return nil, err
	}
	if snap.EducationPolicyCoefficient, err = safeSourceFloat(coef, "policies.EducationPolicyCoefficient", correlationID); err != nil {
		return nil, err
	}
	if snap.GDPFlows, err = finance.GDPFlows(correlationID); err != nil {
		return nil, err
	}
	if snap.LandValue, err = finance.LandValue(correlationID); err != nil {
		return nil, err
	}

	return snap, nil
}

// validateStages rejects an education trajectory carrying any stage outside
// the census's [0, numStages) stage domain (SEC-126). A source-controlled
// stage that would index a fixed [numStages]int64 array out of range is
// rejected here, at the snapshot boundary, with a registry-sourced error
// rather than panicking deep in the stats aggregation.
func validateStages(stages []StageView, citizenID uint64, correlationID string) error {
	for _, sv := range stages {
		if sv.Stage >= numStages {
			return errs.New(ErrInvalidStageKind, correlationID, map[string]any{
				"stage":   int(sv.Stage),
				"citizen": citizenID,
			})
		}
	}
	return nil
}

// safeSourceFloat rejects a non-finite source float at the snapshot boundary
// (SEC-130): a NaN/±Inf crime rate, happiness, unfed fraction or policy
// coefficient must never be stored where the regulator's ordered threshold
// comparison would silently treat it as "not above" the threshold and disable
// the watchdog (NaN > threshold is false — GR#17).
func safeSourceFloat(v float64, source, correlationID string) (float64, error) {
	if !num.IsFinite(v) {
		return 0, errs.New(ErrNonFiniteSource, correlationID, map[string]any{
			"source": source,
			"value":  v,
		})
	}
	return v, nil
}

// RunObservers captures a committed snapshot at tick and runs all four
// observation threads to completion. The threads are observers: they read
// the snapshot and write only to the census's own output surfaces — the
// consumed modules' state is byte-identical before and after (AC-2).
func (c *CensusAPI) RunObservers(tick int64, correlationID string) error {
	if err := c.checkNotCopied("RunObservers"); err != nil {
		return err
	}
	snap, err := c.Snapshot(tick, correlationID)
	if err != nil {
		return err
	}

	// stats generator + regulator are pure functions of the snapshot.
	agg := c.Stats(snap)
	findings := c.regulate(agg)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.latest = agg
	c.auditLocked(snap, agg)
	c.checkLocked(snap)
	c.findings = findings
	return nil
}

// LatestAggregates returns the most recent stats-generator result written
// by RunObservers (the stats surface's query accessor, AC-1a).
func (c *CensusAPI) LatestAggregates() Aggregates {
	if err := c.checkNotCopied("LatestAggregates"); err != nil {
		return Aggregates{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}
