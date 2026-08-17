package social

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// socialServiceKind is the synthetic engine.services kind this module
// registers its five provision categories under (AC-4/US-4): social-worker
// staffing and funding flow through the shared service framework, not a
// hand-rolled parallel model.
const socialServiceKind = services.ServiceKind("social-services")

// SocialAPI is code.json's "engine.social" inbound contract (GUID
// b31dcdb1-b4f7-4d12-abec-9279e74412db, "caseload generation decomposed;
// Slow-Fuse projections on cuts"): the five decomposed provision categories
// (§40), per-category caseload queries, the funding-level mutation command,
// the child-protection cohort-audit marker write, the three-path homelessness
// pipeline, the carers-released and fostering-placement effects, the
// conserved case ledger, and the Slow-Fuse projection submission on funding
// cuts. It consumes engine.services, engine.citizens, engine.wellbeing, and
// engine.projections through their registered contracts alone (GR#20).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *SocialAPI is safe for concurrent use (AC-17): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020 family, mirroring engine.education's
// EducationAPI).
type SocialAPI struct {
	correlationID string
	seed          uint64
	cfg           Config

	// Dependencies, wired via SetCitizens/SetServices/SetFamilyStressSource/
	// SetProjections and read under mu. citizens/services/projections are the
	// concrete *…API values (mirroring engine.education); familyStress is the
	// local seam over engine.wellbeing's two family-stress drivers (AC-3).
	citizens     *citizens.CitizensAPI
	services     *services.ServicesAPI
	familyStress FamilyStressSource
	projections  *projections.ProjectionsAPI

	// cases is the append-only case ledger — the conserved caseload stock
	// (AC-11). nextCaseID mints monotonically-increasing case ids.
	cases      []Case
	nextCaseID CaseID

	// registered marks which categories have been registered as engine.services
	// instances (AC-4). A funding command against an unregistered category is
	// rejected.
	registered [numCategories]bool

	// Provision-effect state (AC-7/AC-9).
	preventionEnabled   bool
	housingFirstEnabled bool
	hostelOccupancy     int64 // current-month hostel placements (capacity gate)
	housingFirstPlaced  int64
	prevented           int64
	fosterPlacements    int64 // current-month foster placements (capacity gate)

	// lastHostelMonth/lastFosterMonth record which sim month the occupancy
	// counters were last reset for, so a new month's routing/placement
	// releases the previous month's beds — capacity is per-month occupancy,
	// not a lifetime cap (SEC-178). Sentinel -1 = never routed yet.
	lastHostelMonth int64
	lastFosterMonth int64

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[SocialAPI]
}

// New constructs a SocialAPI from a validated Config and a world seed (used
// for the deterministic placement-priority hash stream — AC-15). correlationID
// is attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted rate. Dependencies are wired later via
// SetCitizens/SetServices/SetFamilyStressSource/SetProjections.
//
// Config is stored by value: every Config field is a value type (float64,
// int64, string), so there is no reference-typed field to alias a caller's
// memory (GR#3 — no deep-copy needed).
func New(cfg Config, seed uint64, correlationID string) (*SocialAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &SocialAPI{
		correlationID:   correlationID,
		seed:            seed,
		cfg:             cfg,
		lastHostelMonth: -1,
		lastFosterMonth: -1,
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *SocialAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *SocialAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency (the command-based
// citizen-record marker write path, AC-6).
func (a *SocialAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := a.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.citizens = c
	return nil
}

// SetServices wires the engine.services dependency (category registration
// and funding→quality, AC-4/AC-7/AC-9).
func (a *SocialAPI) SetServices(s *services.ServicesAPI) error {
	if err := a.checkNotCopied("SetServices"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.services = s
	return nil
}

// SetFamilyStressSource wires the engine.wellbeing family-stress driver seam
// (AC-3). The composition root passes the real [WellbeingFamilyStress] bridge
// (or a test fake).
func (a *SocialAPI) SetFamilyStressSource(src FamilyStressSource) error {
	if err := a.checkNotCopied("SetFamilyStressSource"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.familyStress = src
	return nil
}

// SetProjections wires the engine.projections dependency (the Slow-Fuse gate
// and the curve-provider registry — AC-10).
func (a *SocialAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := a.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.projections = p
	return nil
}

// RegisterServices registers every provision category as an engine.services
// instance (AC-4): each category's capacity and funding→quality then flow
// through the shared service framework. Idempotent per category and must run
// before SetFunding / FundingLevel / CarersReleased / RouteHomelessness /
// AttemptFosteringPlacement read capacity or funding through services.
func (a *SocialAPI) RegisterServices() error {
	if err := a.checkNotCopied("RegisterServices"); err != nil {
		return err
	}
	a.mu.RLock()
	servicesAPI := a.services
	a.mu.RUnlock()
	if servicesAPI == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "services", "operation": "RegisterServices",
		})
	}
	if err := servicesAPI.RegisterKind(socialServiceKind, services.KindDef{Name: "Social services"}); err != nil {
		return err
	}
	for _, cat := range categoryOrder {
		a.mu.RLock()
		done := a.registered[cat]
		a.mu.RUnlock()
		if done {
			continue
		}
		spec := services.ServiceSpec{
			ID:             services.ServiceID(categoryServiceID(cat)),
			Kind:           socialServiceKind,
			CapacityRaw:    "1",
			CoverageRadius: 1,
			UpgradePath: []services.UpgradeStep{{
				Name:            cat.String(),
				CapacityCeiling: a.categoryCapacity(cat),
			}},
		}
		if err := servicesAPI.RegisterService(spec); err != nil {
			return err
		}
		a.mu.Lock()
		a.registered[cat] = true
		a.mu.Unlock()
	}
	return nil
}

// categoryServiceID returns the engine.services ServiceID for a category.
func categoryServiceID(c Category) string {
	switch c {
	case CategoryFamilySupport:
		return "social.family-support"
	case CategoryHomelessness:
		return "social.homelessness"
	case CategoryDisabilityCarers:
		return "social.disability-carers"
	case CategoryFostering:
		return "social.fostering"
	case CategoryAddiction:
		return "social.addiction"
	default:
		return "social.unknown"
	}
}

// categoryCapacity returns the numeric capacity ceiling a category registers
// into engine.services (AC-7/AC-9 read capacity back through services).
func (a *SocialAPI) categoryCapacity(c Category) float64 {
	switch c {
	case CategoryHomelessness:
		return float64(a.cfg.HostelCapacity)
	case CategoryFostering:
		return float64(a.cfg.FosterCapacity)
	default:
		return 1
	}
}

// FamilyStressInput returns the family-stress caseload input for one citizen
// by consulting the wired family-stress source (engine.wellbeing's registered
// Crowding and FinancialStress drivers — AC-3). Social never re-derives
// crowding or the 35% rent-burden threshold itself.
func (a *SocialAPI) FamilyStressInput(q FamilyStressQuery) (FamilyStressResult, error) {
	if err := a.checkNotCopied("FamilyStressInput"); err != nil {
		return FamilyStressResult{}, err
	}
	a.mu.RLock()
	src := a.familyStress
	a.mu.RUnlock()
	if src == nil {
		return FamilyStressResult{}, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "familyStress", "operation": "FamilyStressInput",
		})
	}
	return src.FamilyStress(q)
}

// FundingLevel returns a category's current funding level (0..1) through the
// shared service framework. An unregistered category is rejected with
// ErrUnknownCategory.
func (a *SocialAPI) FundingLevel(c Category) (float64, error) {
	if err := a.checkNotCopied("FundingLevel"); err != nil {
		return 0, err
	}
	if !c.Valid() {
		return 0, errs.New(ErrUnknownCategory, a.correlationID, map[string]any{"category": c.String()})
	}
	a.mu.RLock()
	servicesAPI := a.services
	a.mu.RUnlock()
	if servicesAPI == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "services", "operation": "FundingLevel",
		})
	}
	return servicesAPI.FundingLevel(services.ServiceID(categoryServiceID(c)))
}

// AdvanceMonth opens one month's steady-state caseload into the ledger (the
// decomposed generator, AC-2/AC-11). It is a pure function of (month,
// DriverInputs) plus the current ledger — the same inputs always open the
// same cases (AC-15).
func (a *SocialAPI) AdvanceMonth(month int64, in DriverInputs) error {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	if err := validateDriverInputs(in, a.correlationID); err != nil {
		return err
	}
	counts := generateCaseload(a.cfg, in)
	// Bound the per-month proposal count at the allocation site (SEC-195):
	// a magnitude driver or config rate that would open more than the
	// resource ceiling is rejected before any case is opened, so the ledger is
	// never poisoned with a month's worth of unbounded cases.
	if err := checkProposalLimit(totalCaseload(counts), a.correlationID); err != nil {
		return err
	}
	for _, cat := range categoryOrder {
		for i := int64(0); i < counts[cat]; i++ {
			a.openCase(cat, month, 0, "steady-state", "", 0)
		}
	}
	return nil
}

// InjectCrisis handles a discrete domestic-crisis event (AC-5): it opens an
// immediate family-support/child-protection caseload spike (a documented
// number of cases per event, data-sourced), each case traceable to the event
// by CrisisID — never folded anonymously into the monthly aggregate. It
// returns the first opened case id.
func (a *SocialAPI) InjectCrisis(ev CrisisEvent) (CaseID, error) {
	if err := a.checkNotCopied("InjectCrisis"); err != nil {
		return 0, err
	}
	// SEC-203: the caller-supplied crisis id is an input, and it is copied once
	// per opened case into the conserved ledger. Bound its length at the write
	// boundary via num.SanitizeEventID (MaxEventIDLength) — an empty id and an
	// over-length id are both rejected with a registry-sourced error before any
	// case is opened, so one InjectCrisis call can no longer retain
	// O(count × len(id)) bytes.
	saneID, err := num.SanitizeEventID(ev.ID)
	if err != nil {
		return 0, err
	}
	ev.ID = saneID
	count := caseloadCount(a.cfg.Caseload.CrisisFamilyCases)
	// A crisis event's case spike is proportional to the config rate, so it
	// shares the SEC-195 shape: a huge finite CrisisFamilyCases would loop
	// ~forever. Bound the count at the allocation site.
	if err := checkProposalLimit(count, a.correlationID); err != nil {
		return 0, err
	}
	// Hoist the Source prefix out of the loop so every opened case shares ONE
	// backing array (SEC-203's retained-amplification half): the old
	// "crisis:"+ev.ID-in-loop made a distinct byte copy per case, so a single
	// event id was retained count× instead of 1×.
	source := "crisis:" + ev.ID
	var first CaseID
	for i := int64(0); i < count; i++ {
		id := a.openCase(CategoryFamilySupport, ev.Month, 0, source, ev.ID, 0)
		if i == 0 {
			first = id
		}
	}
	return first, nil
}
