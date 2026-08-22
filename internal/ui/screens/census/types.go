package census

// NumAgeBands/NumSexBuckets/NumEducationTiers are the fixed series widths
// this screen renders (AC-3): they mirror engine.census's own schema
// constants (internal/engine/census/stats.go's numAgeBands=5, the
// female/male sex-series pair, and internal/engine/census/sources.go's
// numStages=8) — this package cannot import those unexported constants
// (GR#20/AC-1), so it carries its own copies here, deliberately at the
// same values, with the source cited so a schema change on either side is
// a two-file review, not a silent divergence.
const (
	NumAgeBands       = 5
	NumSexBuckets     = 2
	NumEducationTiers = 8
)

// Sex-series bucket indices (mirrors engine.census's sexIndexFemale/
// sexIndexMale).
const (
	SexIndexFemale = 0
	SexIndexMale   = 1
)

// KPI aggregate keys (AC-5/AC-6) — this screen's own copies of
// engine.census's exported KPIKey* constants
// (internal/engine/census/demographics.go), carried as literals rather
// than an internal/engine import (GR#20/AC-1). kpi_drift_test.go asserts
// these stay byte-identical to the engine's own constants.
const (
	KPIKeyGDP            = "gdp"
	KPIKeyHappiness      = "happiness"
	KPIKeyLandValue      = "land-value"
	KPIKeyHomeless       = "homeless"
	KPIKeyInHospital     = "in-hospital"
	KPIKeyOutOfWork      = "out-of-work"
	KPIKeyUnfilledJobs   = "unfilled-jobs"
	KPIKeyJobSkillDemand = "job-skill-demand"
)

// AllKPIKeys is the fixed, ordered set of the eight city KPIs (AC-5),
// ordered to match §13-F6's documented tile row. Deterministic — never a
// map range (GR#21).
var AllKPIKeys = [8]string{
	KPIKeyGDP,
	KPIKeyHappiness,
	KPIKeyLandValue,
	KPIKeyHomeless,
	KPIKeyInHospital,
	KPIKeyOutOfWork,
	KPIKeyUnfilledJobs,
	KPIKeyJobSkillDemand,
}

// BlueWhiteCollar is AC-4's blue/white-collar workforce split — sourced
// from CensusAPI.BlueWhiteCollar's subscribed view field, never a
// screen-local constant.
type BlueWhiteCollar struct {
	Blue  int64
	White int64
}

// KPITile is one of AC-5's eight city-KPI tiles: a named aggregate value
// sourced from its own subscribed view field.
type KPITile struct {
	Key   string
	Value float64
}

// KPISource is AC-6's drill-in resolution for one KPI: either the entity
// IDs composing a population-derived KPI (homeless/out-of-work) or the
// ledger LineValue for an aggregate KPI (gdp/land-value/in-hospital/
// unfilled-jobs/job-skill-demand/happiness) — mirrors
// engine.census.SourceResolution exactly (AC-20's UI-side rendering).
type KPISource struct {
	Key       string
	EntityIDs []uint64
	LineValue int64
	// Unavailable is true when the engine rejected this KPI's source
	// query (AC-12) — the resolved pane must render an explicit
	// "unavailable" state, never a zero EntityIDs/LineValue the player
	// could mistake for a real empty figure.
	Unavailable bool
	Reason      string
}

// EducationStage is one entry of a citizen's education-stage trajectory
// (AC-7), mirroring engine.census.StageView — a string stage name on the
// wire rather than the engine's unexported StageKind enum (GR#20).
type EducationStage struct {
	Stage      string
	StartMonth int64
	EndMonth   int64 // -1 while ongoing
}

// CitizenEducationBio is AC-7's education facet: stage trajectory,
// quality-weighted attainment, and the specialist-university industry
// tie — sourced from CensusAPI.CitizenBio's EducationBio, never a
// screen-local recomputation.
type CitizenEducationBio struct {
	Attainment  int64
	Schooling   int64
	Stages      []EducationStage
	IndustryTie string
}

// CitizenEmploymentBio is AC-7's employment facet.
type CitizenEmploymentBio struct {
	State     string
	Sector    string
	Workplace uint64
}

// CitizenFamilyBio is AC-7's family/home facet.
type CitizenFamilyBio struct {
	Household uint64
	Partner   uint64
	Home      uint64
}

// CitizenBio is AC-7's cradle-to-grave citizen bio — every facet sourced
// from the subscribed CensusAPI.CitizenBio view, never a screen-local
// recomputation.
type CitizenBio struct {
	GUID       string
	ID         uint64
	BirthMonth int64
	Sex        string
	Education  CitizenEducationBio
	Employment CitizenEmploymentBio
	Family     CitizenFamilyBio
	Retirement int64 // retirement month
	Income     int64 // micro-pounds
	// Unavailable is true when the engine rejected this bio query (AC-12)
	// — the bio pane must render an explicit "unavailable" state, never a
	// zero-value bio the player could mistake for a real empty citizen.
	Unavailable bool
	Reason      string
}

// EducationCrimeLinkage is AC-8's education→crime linkage report —
// sourced from CensusAPI.EducationCrimeLinkage's subscribed view field.
type EducationCrimeLinkage struct {
	Population         int64
	MeanAttainment     float64
	CrimeRate          float64
	UneducatedFraction float64
	PolicyCoefficient  float64
}
