package social

// Category is one of the five §40 provision categories. §40's own words
// ("decomposed" caseload, five named provisions) are binding: each category
// is an independent value with its own demand signal, never five labels over
// one blended "social need" score (AC-2).
type Category uint8

// The five documented provision categories, in the fixed order every
// deterministic iteration uses (categoryOrder below).
const (
	CategoryFamilySupport Category = iota // family support & child protection
	CategoryHomelessness
	CategoryDisabilityCarers
	CategoryFostering
	CategoryAddiction

	// numCategories is the fixed category count (a schema constant, not a
	// balance number). Unexported; it sizes the per-category arrays.
	numCategories
)

// categoryOrder is the fixed, documented iteration order over the five
// categories (a slice, never a map) so any loop over categories is
// deterministic (GR#21).
var categoryOrder = []Category{
	CategoryFamilySupport,
	CategoryHomelessness,
	CategoryDisabilityCarers,
	CategoryFostering,
	CategoryAddiction,
}

// String renders the category's stable name (the same token data files and
// logs use). An out-of-range Category renders "unknown" — never a panic.
func (c Category) String() string {
	switch c {
	case CategoryFamilySupport:
		return "family-support"
	case CategoryHomelessness:
		return "homelessness"
	case CategoryDisabilityCarers:
		return "disability-carers"
	case CategoryFostering:
		return "fostering"
	case CategoryAddiction:
		return "addiction"
	default:
		return "unknown"
	}
}

// Valid reports whether c is one of the five registered categories.
func (c Category) Valid() bool { return c < numCategories }

// CaseID identifies one open (or closed) case in the ledger. Monotonic,
// minted by the API, never reused.
type CaseID uint64

// CaseStatus is a case's lifecycle state. The three closure statuses are
// the three documented closure kinds AC-11's identity names — a case is
// never closed by any other means, so the identity can always be reconciled
// from the ledger.
type CaseStatus uint8

const (
	StatusOpen           CaseStatus = iota
	StatusResolved                  // closed with a documented outcome
	StatusEscalated                 // closed here, reopened as a linked case elsewhere
	StatusLostToFollowUp            // documented fallback: relocated / untraceable
)

// Case is one entry in the case ledger — the "literal and auditable" record
// §40 promises. Every field is written at the moment the event happens,
// never backfilled from a later aggregate.
type Case struct {
	ID           CaseID
	Category     Category
	OpenedMonth  int64
	ClosedMonth  int64 // -1 while open
	Status       CaseStatus
	CitizenID    uint64 // 0 for aggregate steady-state cases
	Source       string // "steady-state" | "crisis:<id>" | "escalation:<caseID>" | "intervention"
	CrisisID     string // non-empty when this case is attributable to a discrete crisis event (AC-5)
	Resolution   string // documented outcome text when StatusResolved (e.g. "hostel", "prevention")
	LinkedCaseID CaseID // escalation: the reopened case in the destination category; 0 otherwise
}

// CrisisEvent is a discrete, per-household domestic-crisis trigger (AC-5) —
// not a smooth background rate. Each event is independently traceable by ID.
type CrisisEvent struct {
	ID          string // caller-supplied traceable id (required — AC-5)
	HouseholdID uint64
	Month       int64
	Kind        string // "domestic-crisis" (documented)
}

// DriverInputs is the decomposed driver set the steady-state caseload
// generator is a pure function of (AC-2/AC-15). Each field is sourced from
// its own driver — never a blended "social need" score. Fraction inputs are
// in [0,1] unless noted.
type DriverInputs struct {
	// Deprivation is the area deprivation index in [0,1] (§40).
	Deprivation float64
	// UnemploymentMonths is the long-term unemployment duration in months
	// (≥ 0, §40). Drives homelessness, not disability (AC-2 isolation).
	UnemploymentMonths int64
	// CrowdingStress is the magnitude of engine.wellbeing's Crowding driver
	// (≥ 0), consumed — never locally recomputed (AC-3).
	CrowdingStress float64
	// FinancialStress is the magnitude of engine.wellbeing's FinancialStress
	// driver (≥ 0), consumed — never locally recomputed (AC-3).
	FinancialStress float64
	// NightlifeDensity is the nightlife density in [0,1]; addiction pressure
	// is the §40 nightlife/deprivation coupling (Deprivation × NightlifeDensity).
	NightlifeDensity float64
}

// NewCase is one case the steady-state generator proposes opening for a
// month (AC-2). It is a proposal, not yet a ledger entry — the tick opens it
// through openCase so the ledger is the single source of truth.
type NewCase struct {
	Category  Category
	CitizenID uint64 // 0 for aggregate cases
	CrisisID  string // empty for steady-state
	Source    string
}

// PlacementResult is the documented outcome of one fostering-placement
// attempt (AC-9): placed, or queued because capacity is exhausted — never a
// silently-succeeded placement regardless of capacity.
type PlacementResult uint8

const (
	PlacementPlaced PlacementResult = iota
	PlacementQueued                 // capacity exhausted; the child waits, is not silently placed
)

// String renders the placement result's stable name.
func (p PlacementResult) String() string {
	switch p {
	case PlacementPlaced:
		return "placed"
	case PlacementQueued:
		return "queued"
	default:
		return "unknown"
	}
}
