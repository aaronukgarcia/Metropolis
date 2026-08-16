package leisure

import "github.com/aaronukgarcia/Metropolis/internal/engine/citizens"

// Category is a venue-category index into citizens.LeisureWeights (§5.1):
// sport, arts, nightlife, nature, community, gaming, dining, home. The
// constants alias citizens' own leisure-weight indices (GR#3: single source
// of truth — this package never re-derives the taste taxonomy, it consumes
// engine.citizens' LeisureWeights and its DeriveLeisureWeights output).
type Category = int

const (
	CategorySport     = citizens.LeisureSport
	CategoryArts      = citizens.LeisureArts
	CategoryNightlife = citizens.LeisureNightlife
	CategoryNature    = citizens.LeisureNature
	CategoryCommunity = citizens.LeisureCommunity
	CategoryGaming    = citizens.LeisureGaming
	CategoryDining    = citizens.LeisureDining
	CategoryHome      = citizens.LeisureHome

	// NumCategories is the fixed number of venue categories (8, §5.1).
	NumCategories = citizens.NumLeisureWeights
)

// categoryKey renders a category's canonical data-file key. Used only to
// ACCESS data maps by key (never to range over them), so ordering is
// irrelevant to determinism (GR#21).
func categoryKey(c Category) string {
	switch c {
	case CategorySport:
		return "sport"
	case CategoryArts:
		return "arts"
	case CategoryNightlife:
		return "nightlife"
	case CategoryNature:
		return "nature"
	case CategoryCommunity:
		return "community"
	case CategoryGaming:
		return "gaming"
	case CategoryDining:
		return "dining"
	case CategoryHome:
		return "home"
	}
	return "unknown"
}

// Venue is one leisure venue — a concrete place of a given category with a
// weekly patronage capacity (person-hours). Venue construction/zoning is
// engine.build's job (out of scope); this module only consumes venue
// existence/category/capacity as input, via OpenVenue.
type Venue struct {
	ID       uint64
	Category Category
	District uint16
	Capacity int64 // weekly patronage capacity, person-hours
}

// TasteDistribution is a distribution over the eight venue categories
// (AC-9): a would-be-migrant personality distribution, distinct from a
// specific citizen's own taste weights (AC-3/AC-10).
type TasteDistribution [NumCategories]float64

// LifeStage buckets a citizen into the weekly-budget baseline table.
type LifeStage uint8

const (
	StageChild LifeStage = iota
	StageStudent
	StageEmployed
	StageUnemployed
	StageRetired

	numLifeStages
)

// String renders the life stage's canonical data-file key.
func (s LifeStage) String() string {
	switch s {
	case StageChild:
		return "child"
	case StageStudent:
		return "student"
	case StageEmployed:
		return "employed"
	case StageUnemployed:
		return "unemployed"
	case StageRetired:
		return "retired"
	}
	return "unknown"
}

// lifeStageFor maps a citizen record to its weekly-budget life stage.
// Children (EmploymentNone + under 18) get school hours; students get
// education hours; the employed get firm hours; the retired/unemployed have
// no productive obligation. Pure and deterministic — never the wall clock.
func lifeStageFor(cit citizens.Citizen) LifeStage {
	switch cit.Employment.State {
	case citizens.EmploymentStudent:
		return StageStudent
	case citizens.EmploymentEmployed:
		return StageEmployed
	case citizens.EmploymentRetired:
		return StageRetired
	case citizens.EmploymentUnemployed:
		return StageUnemployed
	}
	// EmploymentNone: child vs adult (never worked).
	if cit.Age() < 18*12 {
		return StageChild
	}
	return StageUnemployed
}

// EventKind enumerates the §42 events calendar kinds.
type EventKind uint8

const (
	EventFestival EventKind = iota
	EventFoodFair
	EventMatchDay
	EventConcert
	EventChristmasMarket

	numEventKinds
)

// String renders the event kind's canonical data-file key.
func (k EventKind) String() string {
	switch k {
	case EventFestival:
		return "festival"
	case EventFoodFair:
		return "food-fair"
	case EventMatchDay:
		return "match-day"
	case EventConcert:
		return "concert"
	case EventChristmasMarket:
		return "christmas-market"
	}
	return "unknown"
}

// Event is one scheduled event on the §42 calendar.
type Event struct {
	ID       uint64
	Kind     EventKind
	District uint16
	Day      int // day-of-month index, [0, DaysPerMonth)
	VenueID  uint64
}
