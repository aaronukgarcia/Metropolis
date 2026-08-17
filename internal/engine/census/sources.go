package census

import (
	"fmt"
	"sort"
)

// GUID is the census's object identity: a wrapper over the owning module's
// existing identity (ASM-641), never a fresh id the census mints itself.
// The string form is "<kind>:<owning-id>" so a citizen GUID and a car GUID
// can never collide even if the owning modules used overlapping numeric
// id spaces.
type GUID string

// GUID constructors — the census wraps, never mints.
func citizenGUID(id uint64) GUID { return GUID(fmt.Sprintf("citizen:%d", id)) }
func carGUID(id uint64) GUID     { return GUID(fmt.Sprintf("car:%d", id)) }
func houseGUID(id uint64) GUID   { return GUID(fmt.Sprintf("house:%d", id)) }
func chopperGUID(id uint64) GUID { return GUID(fmt.Sprintf("chopper:%d", id)) }

// canonicalGUID returns the canonical serialisation of a GUID: the object
// kind name plus the unpadded decimal owning id (SEC-152). parseGUID reads
// "car:007" and "car:7" as the same identity (id 7), so canonicalGUID
// collapses both spellings to "car:7" — a GUID's identity is its (kind, id)
// pair, and the string is only a serialisation of it. Using the raw spelling
// as a map key or lookup would let two spellings of one identity fork into
// two tracked records (GR#3). ok=false for a GUID parseGUID rejects.
func canonicalGUID(g GUID) (GUID, bool) {
	kind, id, ok := parseGUID(g)
	if !ok {
		return "", false
	}
	return GUID(fmt.Sprintf("%s:%d", kind.String(), id)), true
}

// ObjectKind is the census's generic object classification (AC-11): a
// citizen and a car both satisfy the same tracked-object contract.
type ObjectKind uint8

const (
	ObjectCitizen ObjectKind = iota
	ObjectCar
	ObjectHouse
	ObjectChopper
)

// String renders the object kind's canonical name (log/debug use).
func (k ObjectKind) String() string {
	switch k {
	case ObjectCitizen:
		return "citizen"
	case ObjectCar:
		return "car"
	case ObjectHouse:
		return "house"
	case ObjectChopper:
		return "chopper"
	default:
		return "unknown"
	}
}

// LifeSpan distinguishes a short-lived object (a car, ~annual-mileage-
// bounded) from a whole-game object (a house, from construction onward) —
// the documented short-lived vs whole-game distinction AC-11 requires.
type LifeSpan uint8

const (
	LifeSpanShortLived LifeSpan = iota
	LifeSpanWholeGame
)

// Sex is the census's two-bucket sex classification. It is census-owned:
// the census never reaches for citizens.Sex — it reads sex through the
// CitizensSource interface and re-buckets into this type.
type Sex uint8

const (
	SexFemale Sex = 0
	SexMale   Sex = 1
)

// EmploymentState is the census's employment-state bucket (census-owned).
type EmploymentState uint8

const (
	EmploymentNone       EmploymentState = 0
	EmploymentStudent    EmploymentState = 1
	EmploymentEmployed   EmploymentState = 2
	EmploymentUnemployed EmploymentState = 3
	EmploymentRetired    EmploymentState = 4
)

// Sector is the census's employment-sector bucket (census-owned).
type Sector uint8

const (
	SectorNone      Sector = 0
	SectorPrimary   Sector = 1 // agriculture / mining
	SectorSecondary Sector = 2 // manufacturing / construction
	SectorTertiary  Sector = 3 // services / retail / logistics
	SectorPublic    Sector = 4 // public administration / finance / firm overhead
)

// StageKind is the census's education-stage bucket (§27, census-owned).
type StageKind uint8

const (
	StageNone       StageKind = 0
	StageNursery    StageKind = 1
	StagePrimary    StageKind = 2
	StageSecondary  StageKind = 3
	StageSixthForm  StageKind = 4
	StageTechnical  StageKind = 5
	StageUniversity StageKind = 6
	StageAdultEd    StageKind = 7

	// numStages is the fixed number of education stages (a schema constant).
	numStages = 8
)

// CitizenView is the census's read-only projection of one citizen's
// resolved state, assembled from the owning modules' query surfaces. It is
// a census-owned type — never another module's concrete citizen struct,
// never a census-local copy of a citizen's record (AC-9).
type CitizenView struct {
	ID         uint64
	BirthMonth int64
	Sex        Sex
	Household  uint64
	Partner    uint64
	Home       uint64
	Workplace  uint64
	School     uint64
	Employment EmploymentState
	Sector     Sector
	HealthBand uint8 // 0..5 (census-owned re-bucket)
	Wealth     int64 // micro-pounds
}

// StageView is one entry in a citizen's education-stage trajectory.
type StageView struct {
	Stage      StageKind
	StartMonth int64
	EndMonth   int64 // -1 while ongoing
}

// EducationView is the census's read-only projection of one citizen's
// education record (stages + attainment + industry tie).
type EducationView struct {
	Attainment  int64
	Schooling   int64
	Stages      []StageView
	IndustryTie string // specialist-university industry tie ("" if none)
}

// cloneStages returns a defensive copy of a stage trajectory so a stored
// snapshot (or an assembled bio) never aliases the education source's
// backing array (SEC-128). It is the single choke point for the "returned
// slice aliases internal state" class — used by Snapshot and CitizenBio
// alike, never a per-site transcription.
func cloneStages(stages []StageView) []StageView {
	return append([]StageView(nil), stages...)
}

// highestStage returns the latest (highest) stage in the trajectory, or
// StageNone when empty.
func (e EducationView) highestStage() StageKind {
	if len(e.Stages) == 0 {
		return StageNone
	}
	return e.Stages[len(e.Stages)-1].Stage
}

// CitizensSource is the census's narrow query surface over engine.citizens
// (GR#20 contract-first): population and per-citizen identity/employment/
// home/family. The composition root wires the real citizens API; tests
// inject fakes. The census never reaches for a concrete citizen or cold-
// shard struct — it reads only through this interface.
type CitizensSource interface {
	// AllCitizens returns every citizen's census view, sorted by id
	// (deterministic).
	AllCitizens(correlationID string) ([]CitizenView, error)
	// CitizenFor returns one citizen's view, or ok=false if unknown.
	CitizenFor(id uint64, correlationID string) (CitizenView, bool)
}

// EducationSource is the census's narrow query surface over engine.education
// (stage trajectory, attainment, specialist-university industry tie).
type EducationSource interface {
	// EducationFor returns a citizen's education view, or ok=false.
	EducationFor(id uint64, correlationID string) (EducationView, bool)
}

// CrimeSource is the census's narrow query surface over engine.crime (the
// citywide crime rate figure the regulator and the education→crime linkage
// consume).
type CrimeSource interface {
	// CityCrimeRate returns the citywide crime rate for the current tick.
	CityCrimeRate(correlationID string) (float64, error)
}

// WellbeingSource is the census's narrow query surface over engine.wellbeing
// (the §18 headline happiness composite and the unfed fraction).
type WellbeingSource interface {
	// HeadlineHappiness returns the citywide wellbeing composite (0-100).
	HeadlineHappiness(correlationID string) (float64, error)
	// UnfedFraction returns the citywide unfed fraction in [0,1].
	UnfedFraction(correlationID string) (float64, error)
}

// ServicesSource is the census's narrow query surface over engine.services
// (hospital waiting list, unfilled jobs, job→skill demand).
type ServicesSource interface {
	// HospitalWaitingList returns the in-hospital / waiting-list count.
	HospitalWaitingList(correlationID string) (int64, error)
	// UnfilledJobs returns the unfilled-job count.
	UnfilledJobs(correlationID string) (int64, error)
	// JobSkillDemand returns the job→skill demand figure.
	JobSkillDemand(correlationID string) (int64, error)
}

// PoliciesSource is the census's narrow query surface over engine.policies
// (the enacted reward/penalise-education coefficient). The census observes
// the policy's enacted state — it never enacts or repeals one (AC-15).
type PoliciesSource interface {
	// EducationPolicyCoefficient returns the enacted reward/penalise-
	// education coefficient (0 = neutral, positive = reward, negative =
	// penalise).
	EducationPolicyCoefficient(correlationID string) (float64, error)
}

// FinanceSource is the census's narrow query surface over engine.finance
// (per-citizen income, GDP ledger flows, land value).
type FinanceSource interface {
	// IncomeFor returns a citizen's tracked income (micro-pounds), ok=false
	// if the finance ledger does not track that citizen.
	IncomeFor(id uint64, correlationID string) (int64, bool)
	// GDPFlows returns the current tick's GDP-relevant ledger flows
	// (micro-pounds).
	GDPFlows(correlationID string) (int64, error)
	// LandValue returns the citywide land value aggregate (micro-pounds).
	LandValue(correlationID string) (int64, error)
}

// sortCitizens sorts a citizen-view slice by id ascending (GR#21: every
// ordered output over a population is sorted, never a map range).
func sortCitizens(cs []CitizenView) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
}

// sortedGUIDs returns the sorted GUIDs of a tracked set (never a map range).
func sortedGUIDs(m map[GUID]*checkInRecord) []GUID {
	ids := make([]GUID, 0, len(m))
	for g := range m {
		ids = append(ids, g)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
