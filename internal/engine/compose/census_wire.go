package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/census"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// FEAT-209 wiring (docs/planning/acceptance/ui.screen.census.md): the
// "f6.census" view's data source. internal/engine/census (CensusAPI,
// feat.citycensus.md) is a pure observer over SEVEN consumed source
// interfaces (census/sources.go). This file adapts compose's real modules
// onto those interfaces — the same one-file-per-integration convention
// traffic_wire.go / extcommute_wire.go / servicesfirms_wire.go already
// establish, and the same compose-owns-the-seam discipline (no
// internal/engine/census -> internal/engine/* direct edge beyond these
// adapters; compose is the single integration point).
//
// # Honest scope of the six non-citizens sources
//
// Only the citizens source is wired to a REAL module today. The other six
// (education, crime, wellbeing, services, policies, finance) are returned
// by adapters that answer their interface's question honestly — zero /
// "absent" — because the real modules those interfaces were written
// against do not yet expose the corresponding accessors in this tree:
//
//   - education: no EducationFor(id) returning a census.EducationView
//     (engine.education has Attainment/Pupil/StageLedger, a different shape).
//   - crime: no CityCrimeRate() (engine.crime has SafetyTerm/ActiveCrime,
//     neither of which is the per-tick rate figure the census consumes).
//   - wellbeing: no HeadlineHappiness()/UnfedFraction().
//   - services: no HospitalWaitingList()/UnfilledJobs()/JobSkillDemand().
//   - policies: no EducationPolicyCoefficient().
//   - finance: no IncomeFor()/GDPFlows()/LandValue().
//
// Each adapter is therefore a documented zero-seam, NOT a silent guess:
// the census's own requireSources() fails closed unless all seven are
// non-nil, so these adapters exist so the citizens-sourced splines (age
// band, sex, education tier) and the population-derived KPIs (homeless =
// every seed citizen has no home yet) can publish REAL figures now, while
// every KPI/field whose real module is not yet wired reads a truthful
// zero rather than being invented. When a module exposes its accessor,
// replace the matching adapter with a real seam in this file — nothing
// else changes.

// censusCitizensSeam adapts *citizens.CitizensAPI onto census.CitizensSource
// (the one REAL source wired today). AllCitizens reads the whole population
// through citizens' own lock (a new enumeration accessor, registry.go);
// CitizenFor maps a single id.
type censusCitizensSeam struct {
	api *citizens.CitizensAPI
	cid string
}

// AllCitizens returns every citizen as a census.CitizenView, sorted by id
// (citizens.AllCitizens already returns id order — GR#21).
func (s *censusCitizensSeam) AllCitizens(correlationID string) ([]census.CitizenView, error) {
	all, err := s.api.AllCitizens(correlationID)
	if err != nil {
		return nil, err
	}
	out := make([]census.CitizenView, 0, len(all))
	for _, c := range all {
		out = append(out, censusViewFromCitizen(c))
	}
	return out, nil
}

// CitizenFor returns one citizen's census view, or ok=false if unknown.
func (s *censusCitizensSeam) CitizenFor(id uint64, correlationID string) (census.CitizenView, bool) {
	c, ok := s.api.CitizenAt(id, correlationID)
	if !ok {
		return census.CitizenView{}, false
	}
	return censusViewFromCitizen(c), true
}

// censusViewFromCitizen maps a citizens.Citizen hot record onto the
// census's own read-only projection. Sex/Employment/Sector are bucketed
// enums on both sides with identical numeric values for the shared domain,
// so the mapping is a widening conversion plus one explicit off-map
// treatment (the census has no off-map employment bucket — a closed 5-value
// enum, per docs/planning/icd/engine.citizens-offmap.md's own gap note).
func censusViewFromCitizen(c citizens.Citizen) census.CitizenView {
	return census.CitizenView{
		ID:         c.ID,
		BirthMonth: int64(c.BirthMonth),
		Sex:        census.Sex(c.Sex),
		Household:  c.Household,
		Partner:    c.Partner,
		Home:       uint64(c.Home),
		Workplace:  c.Workplace,
		School:     c.School,
		Employment: censusEmploymentState(c.Employment.State),
		Sector:     census.Sector(c.Employment.Sector),
		HealthBand: uint8(c.HealthBand),
		Wealth:     c.Wealth,
	}
}

// censusEmploymentState maps citizens' employment bucket onto the census's
// (identical for the 0..4 domain; OffMap — which citizens added for
// FEAT-198 and the census has no bucket for — falls to EmploymentNone).
func censusEmploymentState(s citizens.EmploymentState) census.EmploymentState {
	switch s {
	case citizens.EmploymentStudent:
		return census.EmploymentStudent
	case citizens.EmploymentEmployed:
		return census.EmploymentEmployed
	case citizens.EmploymentUnemployed:
		return census.EmploymentUnemployed
	case citizens.EmploymentRetired:
		return census.EmploymentRetired
	default:
		// EmploymentNone and EmploymentOffMap both land here: the census's
		// EmploymentState has no off-map bucket (closed 5-value enum).
		return census.EmploymentNone
	}
}

// The six zero-seams below answer their interface's question honestly
// (zero / absent) because the owning real module has no matching accessor
// yet. Each is documented at the top of this file; the methods are the
// mechanical interface satisfaction only.

type censusEducationSeam struct{}

func (censusEducationSeam) EducationFor(uint64, string) (census.EducationView, bool) {
	return census.EducationView{}, false
}

type censusCrimeSeam struct{}

func (censusCrimeSeam) CityCrimeRate(string) (float64, error) { return 0, nil }

type censusWellbeingSeam struct{}

func (censusWellbeingSeam) HeadlineHappiness(string) (float64, error) { return 0, nil }
func (censusWellbeingSeam) UnfedFraction(string) (float64, error)     { return 0, nil }

type censusServicesSeam struct{}

func (censusServicesSeam) HospitalWaitingList(string) (int64, error) { return 0, nil }
func (censusServicesSeam) UnfilledJobs(string) (int64, error)        { return 0, nil }
func (censusServicesSeam) JobSkillDemand(string) (int64, error)      { return 0, nil }

type censusPoliciesSeam struct{}

func (censusPoliciesSeam) EducationPolicyCoefficient(string) (float64, error) { return 0, nil }

type censusFinanceSeam struct{}

func (censusFinanceSeam) IncomeFor(uint64, string) (int64, bool) { return 0, false }
func (censusFinanceSeam) GDPFlows(string) (int64, error)         { return 0, nil }
func (censusFinanceSeam) LandValue(string) (int64, error)        { return 0, nil }

// wireCensus wires all seven census source interfaces onto a freshly
// constructed *census.CensusAPI. The only real seam is citizens; the other
// six are the documented zero-seams above. A Wire* failure here is a
// compose-level module failure (the same discipline every other Wire-time
// construction follows), never a silently half-wired observer.
func wireCensus(c *census.CensusAPI, citizensAPI *citizens.CitizensAPI, cid string) error {
	if err := c.WireCitizens(&censusCitizensSeam{api: citizensAPI, cid: cid}); err != nil {
		return err
	}
	if err := c.WireEducation(censusEducationSeam{}); err != nil {
		return err
	}
	if err := c.WireCrime(censusCrimeSeam{}); err != nil {
		return err
	}
	if err := c.WireWellbeing(censusWellbeingSeam{}); err != nil {
		return err
	}
	if err := c.WireServices(censusServicesSeam{}); err != nil {
		return err
	}
	if err := c.WirePolicies(censusPoliciesSeam{}); err != nil {
		return err
	}
	if err := c.WireFinance(censusFinanceSeam{}); err != nil {
		return err
	}
	return nil
}
