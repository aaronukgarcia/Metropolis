package citizens

// Small named types shared across the hot and cold records. Every enum
// here is a bucketed code (an integer, never a string) so the cold store
// can field-compress it (AC-5's "bucketed enums, not strings").

// Sex is the citizen's sex, a two-value bucket.
type Sex uint8

const (
	SexFemale Sex = 0
	SexMale   Sex = 1
)

// HealthBand is the citizen's health band, six buckets 0 (critical) to 5
// (excellent). A bucketed enum, not a string (AC-5).
type HealthBand uint8

const (
	HealthCritical  HealthBand = 0
	HealthPoor      HealthBand = 1
	HealthFair      HealthBand = 2
	HealthGood      HealthBand = 3
	HealthVeryGood  HealthBand = 4
	HealthExcellent HealthBand = 5
)

// MaxHealthBand is the highest (healthiest) band; used by the mortality
// modifier to map "worse band ⇒ higher hazard" without a hardcoded count.
const MaxHealthBand = HealthExcellent

// EmploymentState is the citizen's employment state.
type EmploymentState uint8

const (
	EmploymentNone       EmploymentState = 0 // child / never worked
	EmploymentStudent    EmploymentState = 1
	EmploymentEmployed   EmploymentState = 2
	EmploymentUnemployed EmploymentState = 3
	EmploymentRetired    EmploymentState = 4
)

// Sector is the employment sector, a bucketed code.
type Sector uint8

const (
	SectorNone      Sector = 0
	SectorPrimary   Sector = 1 // agriculture / mining
	SectorSecondary Sector = 2 // manufacturing / construction
	SectorTertiary  Sector = 3 // services / retail
	SectorPublic    Sector = 4 // public sector
)

// Fidelity is the HOT/WARM/COLD adaptive-fidelity tier (§5.2, AC-4).
type Fidelity uint8

const (
	FidelityCold Fidelity = 0
	FidelityWarm Fidelity = 1
	FidelityHot  Fidelity = 2
)

// String renders the tier's canonical name for logs/inspection.
func (f Fidelity) String() string {
	switch f {
	case FidelityCold:
		return "COLD"
	case FidelityWarm:
		return "WARM"
	case FidelityHot:
		return "HOT"
	}
	return "UNKNOWN"
}

// Stage is the education stage (§5.1, §27).
type Stage uint8

const (
	StageNone       Stage = 0
	StageNursery    Stage = 1
	StagePrimary    Stage = 2
	StageSecondary  Stage = 3
	StageSixthForm  Stage = 4
	StageTechnical  Stage = 5
	StageUniversity Stage = 6
	StageAdultEd    Stage = 7
)

// CellRef is a packed home-cell reference: the linear index of a cell
// within the world grid (30×30 tiles × 200×200 cells = 36M cells fits in
// 26 bits, so uint32 is sufficient and field-compressible in the cold
// store). 0 is the "no home" sentinel.
type CellRef uint32

// AgeBand is a citizen's age band for the A7 stratified sample.
type AgeBand uint8

const (
	AgeBand0to17  AgeBand = 0
	AgeBand18to34 AgeBand = 1
	AgeBand35to54 AgeBand = 2
	AgeBand55to74 AgeBand = 3
	AgeBand75plus AgeBand = 4

	// NumAgeBands is the fixed number of age bands the stratification
	// scheme uses (AC-23). A schema constant, not a balance number.
	NumAgeBands = 5
)

// AgeBandFor maps an age in months to its stratification band. Pure and
// deterministic (no wall clock).
func AgeBandFor(ageMonths int64) AgeBand {
	years := ageMonths / 12
	switch {
	case years < 18:
		return AgeBand0to17
	case years < 35:
		return AgeBand18to34
	case years < 55:
		return AgeBand35to54
	case years < 75:
		return AgeBand55to74
	default:
		return AgeBand75plus
	}
}

// IncomeBand is a citizen's income band for the A7 stratified sample,
// derived from wealth.
type IncomeBand uint8

const (
	IncomeBand0 IncomeBand = 0 // lowest
	IncomeBand1 IncomeBand = 1
	IncomeBand2 IncomeBand = 2
	IncomeBand3 IncomeBand = 3
	IncomeBand4 IncomeBand = 4 // highest

	// NumIncomeBands is the fixed number of income bands the
	// stratification scheme uses (AC-23). A schema constant.
	NumIncomeBands = 5
)

// IncomeBandFor maps wealth (micro-pounds) to its stratification band.
// The thresholds are a documented balance placeholder (GR#15's balance
// regime: directional, pending M2 Batch tuning) — they are the five
// quintile boundaries of a synthetic population, not player-felt numbers.
func IncomeBandFor(wealthMicroPounds int64) IncomeBand {
	// Thresholds in whole pounds (wealth is stored in micro-pounds).
	pounds := wealthMicroPounds / 1_000_000
	switch {
	case pounds < 15_000:
		return IncomeBand0
	case pounds < 30_000:
		return IncomeBand1
	case pounds < 60_000:
		return IncomeBand2
	case pounds < 120_000:
		return IncomeBand3
	default:
		return IncomeBand4
	}
}

// MaxPersonalityAxis is the documented upper bound of every personality
// axis (0-100, §5.1); used by validation, not as a hardcoded per-axis
// constant.
const MaxPersonalityAxis = 100
