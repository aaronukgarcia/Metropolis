package crime

// Core identity and input types for engine.crime (MOD-042). District and
// gang identities are this package's own, because neither engine.citizens
// nor engine.world exposes a district concept on a registered outbound
// edge yet (see doc.go's registered-edge honesty note) — the composition
// root maps world/citizens space onto DistrictID and supplies per-district
// driver values through [DistrictInput].

// DistrictID identifies one city district for per-district accounting. It
// is assigned by the composition root (the authority on district
// existence) and registered with this API via [CrimeAPI.AdvanceMonth] or
// [CrimeAPI.RegisterDistrict].
type DistrictID uint64

// CrimeType enumerates the nine §28 crime types, each tracked as a
// distinct sub-figure per district (AC-2) — never a single blended index
// split for display.
type CrimeType uint8

const (
	CrimePettyTheft CrimeType = iota
	CrimeBurglary
	CrimeVehicleCrime
	CrimeCriminalDamage
	CrimeViolent
	CrimeDrugsSupply
	CrimeOrganised
	CrimeFraudCyber
	CrimeSmuggling

	numCrimeTypes
)

// String returns the crime type's canonical §28 name (log/debug use —
// never the GR#1 user-visible error text).
func (t CrimeType) String() string {
	switch t {
	case CrimePettyTheft:
		return "petty-theft"
	case CrimeBurglary:
		return "burglary"
	case CrimeVehicleCrime:
		return "vehicle-crime"
	case CrimeCriminalDamage:
		return "criminal-damage"
	case CrimeViolent:
		return "violent-crime"
	case CrimeDrugsSupply:
		return "drugs-supply"
	case CrimeOrganised:
		return "organised-crime"
	case CrimeFraudCyber:
		return "fraud-cyber"
	case CrimeSmuggling:
		return "smuggling"
	default:
		return "unknown"
	}
}

// crimeTypeKeys is the fixed slice of the nine crime types, ordered by
// enum value — used wherever a deterministic iteration over all types is
// required (never a map range on the tick path).
var crimeTypeKeys = []CrimeType{
	CrimePettyTheft, CrimeBurglary, CrimeVehicleCrime, CrimeCriminalDamage,
	CrimeViolent, CrimeDrugsSupply, CrimeOrganised, CrimeFraudCyber,
	CrimeSmuggling,
}

// Driver enumerates the independent per-district crime drivers (US-1/AC-1).
// Each crime type responds to only the drivers the §28 spec ties it to, so
// moving one driver moves only the types that list it — the isolability
// AC-1/AC-2/AC-3 check.
type Driver uint8

const (
	DriverDeprivation Driver = iota
	DriverInequality
	DriverYouthUnemployment
	DriverBlight
	DriverLeisureDesert
	DriverLowPresence
	DriverEraWealth
	DriverSmugglingPressure

	numDrivers
)

// String returns the driver's canonical name.
func (d Driver) String() string {
	switch d {
	case DriverDeprivation:
		return "deprivation"
	case DriverInequality:
		return "inequality"
	case DriverYouthUnemployment:
		return "youthUnemployment"
	case DriverBlight:
		return "blight"
	case DriverLeisureDesert:
		return "leisureDesert"
	case DriverLowPresence:
		return "lowPresence"
	case DriverEraWealth:
		return "eraWealth"
	case DriverSmugglingPressure:
		return "smugglingPressure"
	default:
		return "unknown"
	}
}

// typeDrivers is the structural mapping of which §28 drivers each crime
// type responds to (the mechanism — AC-1/AC-2/AC-3). This is the spec's
// structure, not a balance figure, so it lives in code; the elasticities
// (magnitudes) live in crime.json (GR#15).
//
// The mapping is what makes AC-2 and AC-3 mechanically true: smuggling
// lists only DriverSmugglingPressure (port throughput vs customs), so
// raising port throughput alone moves only the smuggling figure; fraud/cyber
// lists only DriverEraWealth; and only burglary/violent list
// DriverInequality (the genuine neighbour comparison).
var typeDrivers = [numCrimeTypes][]Driver{
	CrimePettyTheft:     {DriverDeprivation, DriverYouthUnemployment, DriverLowPresence},
	CrimeBurglary:       {DriverDeprivation, DriverInequality},
	CrimeVehicleCrime:   {DriverDeprivation, DriverLowPresence},
	CrimeCriminalDamage: {DriverLeisureDesert, DriverBlight},
	CrimeViolent:        {DriverInequality, DriverYouthUnemployment, DriverLeisureDesert},
	CrimeDrugsSupply:    {DriverDeprivation, DriverYouthUnemployment},
	CrimeOrganised:      {DriverBlight, DriverLowPresence},
	CrimeFraudCyber:     {DriverEraWealth},
	CrimeSmuggling:      {DriverSmugglingPressure},
}

// DistrictInput is the per-district, per-month driver + justice + removal
// snapshot the composition root supplies to [CrimeAPI.AdvanceMonth]. Every
// driver is a [0,1] fraction (or non-negative count) and is validated at
// the entry point (GR#16). The values are pushed input: the composition
// root derives them from engine.citizens / engine.services /
// engine.wellbeing / engine.world adjacency and hands them here, because
// those modules' district-level driver surfaces are not all live on a
// registered edge yet (see doc.go).
type DistrictInput struct {
	District DistrictID

	// Generation drivers (§28), each [0,1].
	OwnDeprivation     float64 // this district's deprivation (1 = max)
	NeighbourWealth    float64 // adjacent districts' wealth (1 = richest) — AC-3's genuine neighbour comparison
	YouthUnemployment  float64 // young-male unemployment rate
	Blight             float64 // abandonment/blight level
	YouthLeisureDesert float64 // youth leisure-desert measure
	PolicePresence     float64 // baseline police presence (0 = none)
	EraWealth          float64 // era/wealth progression (fraud/cyber driver)
	PortThroughput     float64 // harbour throughput (smuggling driver)
	CustomsFunding     float64 // customs funding (smuggling damper)

	// Policing triad capacity (per-district physical capacity; the citywide
	// strategy mix scales these).
	PatrolCoverage           float64 // patrol units per 10k (deterrence)
	DetectiveCapacity        float64 // detective capacity (clearance)
	PreventionInfrastructure float64 // youth/job/lighting/community provision (prevention)

	// Population pool the drivers read from (AC-7d's eligible-pool figure).
	EligiblePool int64

	// Gang removal-stack components (§28 "removal needs the full stack").
	RegenerationInvestment float64 // regeneration investment [0,1]
	PrisonAbsorption       float64 // prison absorption of members [0,1]

	// Justice chain parameters (AC-12/AC-13).
	CourthouseThroughput int64 // cases the district courthouse can decide per month
}

// SecurityInput is the citywide Security-Service / liaison input carried by
// AdvanceMonth (AC-11's MI5-analogue threat dial).
type SecurityInput struct {
	Exposure float64 // major-event/stadium/airport exposure [0,1]
	Funding  float64 // Security Service funding [0,1]
	Liaison  float64 // liaison level [0,1]
}

// StrategyMix is the citywide patrol/detective/community weighting (AC-10).
// The three weights must sum to the documented total (crime.json mix.total,
// AC-15) and are settable only once a Constabulary Headquarters is built.
type StrategyMix struct {
	Patrol    float64
	Detective float64
	Community float64
}

// GangID identifies a formed gang (a stable, queryable identity — AC-6).
type GangID uint64

// Gang is one formed, named, tracked gang entity (§28, AC-6/AC-7). Its
// strength trends toward removal only under the full removal stack (AC-8),
// and a decapitation without regeneration respawns a fresh entity (AC-9).
type Gang struct {
	ID        GangID
	Name      string
	District  DistrictID
	FormedAt  int64    // simulated month it formed
	Strength  float64  // 1 = full presence, 0 = removed
	Territory []uint64 // the queryable cell-set the gang claims (AC-7a)

	// AC-7c: a queryable business levy and the resulting closure pressure.
	TaxLevyMicroPounds int64
	BusinessClosures   int64

	// AC-7d: cumulative recruits drawn from the matching demographic,
	// reducing the district's eligible-pool figure (EligiblePool).
	Recruited int64
}

// threatState is the internal MI5-analogue threat-dial state (AC-11).
type threatState struct {
	level          float64
	elevatedMonths int   // consecutive months the level has been nonzero (the "nonzero and rising" precursor)
	lastRiseMonth  int64 // first month of the current elevated run
	lastEventMonth int64 // month the last terror-threat event fired (0 = never)
}

// justiceState is one district's justice-chain ledger (AC-12/AC-13). Every
// term below is the count of that stage's own log (its independently
// sourced outcome), never a remainder computed to balance an identity.
type justiceState struct {
	// The awaiting-trial backlog stock, FIFO, of identifiable offenders.
	backlog []uint64

	// Current month's per-stage logs (reset each AdvanceMonth).
	arrested              []uint64
	charged               []uint64
	releasedNoCharge      []uint64
	convicted             []uint64
	acquitted             []uint64
	awaitingTrial         []uint64 // the charged-this-month overflow into backlog (AC-12's identity-2 term)
	sentencedToPrison     []uint64
	sentencedNonCustodial []uint64
	releasedOnBacklog     []uint64
}
