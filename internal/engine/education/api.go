package education

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TrafficAPI is the local contract shape for engine.traffic's trip-generation
// surface — code.json's registered engine.education → engine.traffic outbound
// edge. engine.traffic (MOD-023) is not yet built, so this package consumes
// the contract shape directly (GR#20 contract-first, stub-forever): the
// composition root wires the real *traffic.TrafficAPI once it lands, and
// tests inject a fake. AC-5's school-run trips are a genuine addition to
// traffic demand, not an attendance flag traffic never reads.
type TrafficAPI interface {
	// RegisterTrip feeds one trip-demand record into the traffic surface.
	RegisterTrip(d TripDemand) error
	// AddDemand adds count pupils to the school-run demand for a school.
	AddDemand(schoolID uint64, count int64) error
}

// TripDemand is one school-run trip record (AC-5): a school's catchment,
// the mode (walk/bus, distance-dependent), and the pupil count.
type TripDemand struct {
	SchoolID uint64
	Mode     string
	Count    int64
}

// EducationAPI is code.json's "engine.education" inbound contract
// (GUID bae4923b-5043-45e1-82b7-7772afd79b0f, "stage pipeline gated by
// SeasonAPI Sept intake"): per-stage capacity/enrolment/attainment queries,
// the stage-funding mutation command, the September-gated pipeline, the
// three-way secondary-exit fork, the university halls/research-points
// surface, and the Slow-Fuse projection submission. It consumes
// engine.citizens, engine.services, engine.season, engine.traffic, and
// engine.projections through their registered contracts alone (GR#20).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *EducationAPI is safe for concurrent use (AC-16): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.citizens'
// CitizensAPI).
type EducationAPI struct {
	correlationID string
	seed          uint64
	cfg           Config

	// Dependencies, wired via SetCitizens/SetServices/SetSeason/SetTraffic/
	// SetProjections and read under mu. The traffic dependency is an
	// interface (engine.traffic is not yet built); the rest are concrete
	// *…API values, mirroring engine.attract/engine.households.
	citizens    *citizens.CitizensAPI
	services    *services.ServicesAPI
	season      *season.SeasonAPI
	traffic     TrafficAPI
	projections *projections.ProjectionsAPI

	// pupils is the authoritative pupil cohort: citizen id → pupil record.
	// Attainment and stage are this module's single source of truth for the
	// pipeline (the citizen record's education snapshot is citizens-owned;
	// see doc.go's AC-6 note).
	pupils map[uint64]*Pupil

	// events is the cohort-accounting event log (AC-10): every intake,
	// promotion, fork, and departure is recorded as its own independently
	// sourced term, never computed as a balancing remainder.
	events []cohortEvent

	// enrolled is the live per-stage enrolment count, mutated exactly in
	// step with the event log so the AC-10 identity can be reconciled
	// against an independently-summed event replay.
	enrolled [numStages]int64

	// registered marks which stages have been registered as engine.services
	// instances (AC-2). A stage-funding command or quality query against an
	// unregistered stage is rejected (AC-12).
	registered [numStages]bool

	// researchPoints is the accumulated university research-points output
	// (AC-8), in fixed-point units of ResearchPointsPerGraduate.
	researchPoints int64

	// lastIntakeMonth is the last month AdvanceIntake already processed,
	// so a re-run of the same September gate cannot double-advance a cohort
	// (AC-10's conservation, enforced at the write boundary).
	lastIntakeMonth int64
	hasIntakeRun    bool

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[EducationAPI]
}

// New constructs an EducationAPI from a validated Config and a world seed
// (used for every counter-based hash draw — AC-14). correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted gate or scale. The dependencies are
// wired later via SetCitizens/SetServices/SetSeason/SetTraffic/
// SetProjections.
func New(cfg Config, seed uint64, correlationID string) (*EducationAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID, "constructor"); err != nil {
		return nil, err
	}
	a := &EducationAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		pupils:        make(map[uint64]*Pupil),
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *EducationAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *EducationAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency (age lookup, the
// command-based education-drift write path, and the life-event departures).
func (a *EducationAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := a.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.citizens = c
	return nil
}

// SetServices wires the engine.services dependency (stage registration and
// funding→quality — AC-2/AC-6).
func (a *EducationAPI) SetServices(s *services.ServicesAPI) error {
	if err := a.checkNotCopied("SetServices"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.services = s
	return nil
}

// SetSeason wires the engine.season dependency (the September intake gate —
// AC-4).
func (a *EducationAPI) SetSeason(s *season.SeasonAPI) error {
	if err := a.checkNotCopied("SetSeason"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.season = s
	return nil
}

// SetTraffic wires the engine.traffic trip-generation surface (AC-5).
func (a *EducationAPI) SetTraffic(t TrafficAPI) error {
	if err := a.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.traffic = t
	return nil
}

// SetProjections wires the engine.projections dependency (the Slow-Fuse
// gate and the curve-provider registry — AC-9).
func (a *EducationAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := a.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.projections = p
	return nil
}

// RegisterStages registers every pipeline stage as an engine.services
// instance (AC-2): each stage's capacity and funding→quality then flow
// through the shared service framework, not a hand-rolled parallel model.
// Registering is idempotent per stage and must run before SetStageFunding /
// StageQuality / StageCapacity.
func (a *EducationAPI) RegisterStages() error {
	if err := a.checkNotCopied("RegisterStages"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.services == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "services", "operation": "RegisterStages",
		})
	}
	for _, s := range stageOrder {
		if a.registered[s] {
			continue
		}
		id := services.ServiceID(stageServiceID(s))
		spec := services.ServiceSpec{
			ID:             id,
			Kind:           services.ServiceEducation,
			CapacityRaw:    "1", // nominal; per-stage capacity is a balance placeholder (see data file)
			CoverageRadius: 1,
			UpgradePath:    []services.UpgradeStep{{BuildingID: stageServiceID(s), Name: s.String(), CapacityCeiling: 1}},
		}
		if err := a.services.RegisterService(spec); err != nil {
			return err
		}
		a.registered[s] = true
	}
	return nil
}

// stageServiceIDLocked returns the engine.services ServiceID for a stage and
// whether the stage is registered; the caller holds a.mu. An out-of-range
// Stage (a malformed uint8 value in [numStages, 255]) is treated as
// unregistered — bounds-checked BEFORE registered[s] is indexed, so the
// query boundaries that funnel through here (StageQuality/StageCapacity)
// reject a malformed stage with ErrStageNotRegistered rather than panicking.
func (a *EducationAPI) stageServiceIDLocked(s Stage) (services.ServiceID, bool) {
	if !validStage(s) || !a.registered[s] {
		return "", false
	}
	return services.ServiceID(stageServiceID(s)), true
}

// sortedPupilIDs returns the enrolled pupil ids in ascending order (GR#21:
// deterministic iteration, never map range).
func (a *EducationAPI) sortedPupilIDs() []uint64 {
	ids := make([]uint64, 0, len(a.pupils))
	for id := range a.pupils {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
