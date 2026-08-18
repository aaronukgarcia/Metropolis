package tourism

// TermKind is one attraction-portfolio term (§44's draw formula
// "beach + promenade + pier + venues + events + landmarks + heritage +
// café culture + countryside/BDI"). The café-culture term is deliberately
// absent: it is blocked pending BUG-058 finding #2 (no
// engine.tourism→engine.cafe edge is registered) and is never computed,
// imported, or silently approximated by this package (AC-17).
type TermKind uint8

const (
	// TermBeach is the beach/promenade/pier term — the seaside core of a
	// coastal-town draw, driven by registered attractions of this term.
	TermBeach TermKind = iota

	// TermVenues is the venues term, sourced live from engine.leisure's
	// VenueMix (the registered engine.tourism→engine.leisure edge) — never
	// a second venue inventory (GR#3).
	TermVenues

	// TermEvents is the events term, driven by registered attractions
	// (player-scheduled festivals, match days, concerts — §42/§44).
	TermEvents

	// TermLandmarks is the landmarks/heritage term, driven by registered
	// attractions (heritage trails, observation towers, the lido).
	TermLandmarks

	// TermCountryside is the countryside/BDI term (§31 escarpment walks,
	// woodland) — driven by registered attractions.
	TermCountryside

	numTerms
)

// numPortfolioTerms is the number of active (non-blocked) portfolio terms
// this package computes. It is the loop bound for every term-table access,
// never derived from map iteration (GR#21).
const numPortfolioTerms = int(numTerms)

// String renders the term's canonical data-file key (matching
// data/tourism.json's portfolioWeights keys).
func (k TermKind) String() string {
	switch k {
	case TermBeach:
		return "beach"
	case TermVenues:
		return "venues"
	case TermEvents:
		return "events"
	case TermLandmarks:
		return "landmarks"
	case TermCountryside:
		return "countryside"
	}
	return "unknown"
}

// AccommodationKind is one §44 accommodation-stock category.
type AccommodationKind uint8

const (
	AccommodationHotel AccommodationKind = iota
	AccommodationBnB
	AccommodationCampsite
	AccommodationHolidayLet

	accommodationKindCount
)

// numAccommodationKinds is the fixed number of accommodation categories
// (4, §44/Catalogue Supplement 2), the loop bound for accommodation
// capacity aggregation — never derived from map iteration (GR#21).
const numAccommodationKinds = int(accommodationKindCount)

// String renders the accommodation kind's canonical data-file key.
func (k AccommodationKind) String() string {
	switch k {
	case AccommodationHotel:
		return "hotel"
	case AccommodationBnB:
		return "bnb"
	case AccommodationCampsite:
		return "campsite"
	case AccommodationHolidayLet:
		return "holidayLet"
	}
	return "unknown"
}

// AccessTier is §44's access step-change rung: domestic → continental →
// global. The reach multiplier for each rung is data-derived
// (data/tourism.json's accessTiers), never a Go literal (GR#15).
type AccessTier uint8

const (
	AccessDomestic AccessTier = iota
	AccessContinental
	AccessGlobal

	accessTierCount
)

// numAccessTiers is the fixed number of access-tier rungs (3, §44's
// domestic → continental → global ladder).
const numAccessTiers = int(accessTierCount)

// String renders the access tier's canonical data-file key.
func (a AccessTier) String() string {
	switch a {
	case AccessDomestic:
		return "domestic"
	case AccessContinental:
		return "continental"
	case AccessGlobal:
		return "global"
	}
	return "unknown"
}

// Attraction is one registered portfolio contribution (§44): a beach, a
// pier, an event, a landmark, or a countryside asset. It carries the term
// it feeds and a non-negative score. Construction of attractions is the
// player's (or a downstream module's) job; this module consumes them as
// input.
type Attraction struct {
	ID    uint64
	Term  TermKind
	Score float64
}

// Accommodation is one registered accommodation facility: a hotel, B&B,
// campsite/caravan park, or holiday let with a non-negative bed count.
// Construction mechanics are engine.build's job (out of scope); this
// module consumes capacity as configured input.
type Accommodation struct {
	ID   uint64
	Kind AccommodationKind
	Beds int64
}

// DayTripper is one month's day-tripper visitor stream (§44): hours, no
// accommodation draw, small spend, large transport load. It deliberately
// carries no accommodation-nights field — a day-tripper consumes zero
// accommodation capacity (AC-5).
type DayTripper struct {
	Count            int64 // day-trippers admitted this month
	Hours            float64
	SpendMicroPounds int64
	TransportLoad    float64
}

// StayingVisitor is one month's staying-visitor stream (§44): nights,
// accommodation-bound, larger spend. Unlike [DayTripper] it carries the
// accommodation-nights field (AC-5) — the structural distinction the
// day-tripper vs staying-visitor check asserts.
type StayingVisitor struct {
	Count            int64 // staying visitors admitted this month
	Nights           int64 // accommodation nights consumed (Count × stay months)
	SpendMicroPounds int64
	TransportLoad    float64
}

// VisitorLoad is the volume-responsive logistics signal §44 names ("fill
// your trains, spike waste and policing on event days") — a queryable
// output proportional to realised visitor volume, not a wall-clock figure
// (AC-11).
type VisitorLoad struct {
	Transport float64
	Waste     float64
	Policing  float64
}

// DrawProjection is AC-12's anti-ambush projection: the seasonal
// multiplier and resulting draw score for a future month index, queryable
// ahead of the month it happens (consistent with engine.projections'
// contract and engine.season's "queryable ahead of time" pattern).
type DrawProjection struct {
	Month                int64
	SeasonalMultiplier   float64
	ReputationMultiplier float64
	AccessMultiplier     float64
	DrawScore            float64
}
