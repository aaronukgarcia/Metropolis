package protocol

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/persona"
)

// FEAT-2326609842 -- the citizen dummy-passport's protocol surface: two
// READ-ONLY request/response queries (KindQueryCitizen, KindQueryOccupants)
// whose answers ride CommandResult.Result as JSON. Both are bounded AT THE
// CODEC (validateBounds, called from DecodeCommand and Command.Validate): one
// citizen per request, one capped page per listing, and no query can return the
// whole population (AC-7).

// ErrQueryOutOfBounds: a citizen query payload failed its codec-level bounds
// (zero or garbage ID, unknown listing key, oversized page, negative or
// out-of-range afterID). Raised by validateBounds, so it surfaces from
// DecodeCommand / Command.Validate before any engine handler runs.
const ErrQueryOutOfBounds = "MET-G5601"

const (
	// MinQueryCitizenID / MaxQueryCitizenID bound a citizen-query ID. 0 is
	// the "no citizen" sentinel everywhere (Partner, Workplace...). The upper
	// bound is the fertility-child namespace base (1<<63, engine.citizens'
	// FertilityChildIDBase) plus 1<<40 births of headroom: anything above is
	// garbage no namespace ever mints. This package is neutral ground and may
	// not import engine.citizens, so the base is repeated here and pinned
	// equal by compose's TestQueryCitizenIDBoundMatchesNamespace.
	MinQueryCitizenID uint64 = 1
	MaxQueryCitizenID uint64 = 1<<63 + 1<<40

	// OccupantsPageCap is the hard cap on one occupants page; with the keyset
	// lookup (O(log n) to the cursor, O(page) to read) it bounds the scrape
	// rate: a page costs O(page), never O(population).
	OccupantsPageCap = 50

	// SatisfactionScaleMax is the top of every satisfaction axis (each is
	// 0..SatisfactionScaleMax, engine.citizens' validated range) -- the ONE
	// scaling constant surfaces use to draw the five satisfaction bars (AC-8).
	SatisfactionScaleMax = 100
)

// Occupant listing keys.
const (
	// OccupantsByHome lists the citizens whose Home cell is Cell.
	OccupantsByHome = "home"
	// OccupantsByWorkplace lists the citizens whose Workplace ref is Ref.
	OccupantsByWorkplace = "workplace"
)

// QueryCitizenPayload asks for one citizen's passport by ID.
type QueryCitizenPayload struct {
	ID uint64 `json:"id"`
}

func (QueryCitizenPayload) commandKind() Kind { return KindQueryCitizen }

func (p QueryCitizenPayload) validateBounds() error {
	if p.ID < MinQueryCitizenID || p.ID > MaxQueryCitizenID {
		return queryBoundsErr(KindQueryCitizen, "citizen id outside the mintable namespace")
	}
	return nil
}

// QueryOccupantsPayload asks for one page of a building's occupants. By is
// OccupantsByHome (the residents of Cell, a map cell) or OccupantsByWorkplace
// (the staff of workplace Ref). AfterID/Limit page the stable ascending-ID
// listing by KEYSET: the page is the next Limit live occupants whose id is
// strictly greater than AfterID (0 = from the start; a client passes the
// previous page's NextAfterID), so concurrent deaths and births can neither
// skip nor repeat a citizen. AfterID is bounded to 0..MaxQueryCitizenID and
// REFUSED outside it, never clamped. Limit 0 means OccupantsPageCap, and Limit above the cap is REFUSED
// (never silently clamped -- a scraper must see the bound).
type QueryOccupantsPayload struct {
	By      string  `json:"by"`
	Cell    CellRef `json:"cell"`
	Ref     uint32  `json:"ref,omitempty"`
	AfterID uint64  `json:"afterID"`
	Limit   int     `json:"limit"`
}

func (QueryOccupantsPayload) commandKind() Kind { return KindQueryOccupants }

func (p QueryOccupantsPayload) validateBounds() error {
	switch p.By {
	case OccupantsByHome:
		if p.Cell.X < 0 || p.Cell.Y < 0 {
			return queryBoundsErr(KindQueryOccupants, "home cell has a negative coordinate")
		}
	case OccupantsByWorkplace:
		if p.Ref == 0 {
			return queryBoundsErr(KindQueryOccupants, "workplace ref 0 means no workplace")
		}
	default:
		return queryBoundsErr(KindQueryOccupants, "by must be home or workplace")
	}
	if p.AfterID > MaxQueryCitizenID {
		return queryBoundsErr(KindQueryOccupants, "afterID outside 0..MaxQueryCitizenID")
	}
	if p.Limit < 0 || p.Limit > OccupantsPageCap {
		return queryBoundsErr(KindQueryOccupants, "limit above OccupantsPageCap")
	}
	return nil
}

func queryBoundsErr(kind Kind, cause string) error {
	return errs.New(ErrQueryOutOfBounds, errs.NewCorrelationID(), map[string]any{
		"kind": string(kind), "cause": cause,
	})
}

// CitizenPassport is the answer to KindQueryCitizen: the ColdRecord-derived
// fields plus the presentational persona. Name/GUID/Avatar are derivations
// from ID (internal/foundation/persona), not engine state. Strings are the
// engine's own enum labels so every surface renders identically.
type CitizenPassport struct {
	ID     uint64         `json:"id"`
	GUID   string         `json:"guid"`
	Name   string         `json:"name"`
	Avatar persona.Avatar `json:"avatar"`

	// Month is the sim month the record was read at; AgeMonths/AgeYears are
	// derived from it and BirthMonth (never stored). BirthMonth is the absolute
	// month index (Jan Y1 = 0; may be negative: born before genesis).
	Month      int64  `json:"month"`
	BirthMonth int64  `json:"birthMonth"`
	AgeMonths  int64  `json:"ageMonths"`
	AgeYears   int64  `json:"ageYears"`
	Sex        string `json:"sex"`

	// Address: HasHome is false for the Home==0 "no fixed address" sentinel,
	// and then HomeCell/AddressLabel are zero/empty -- never fabricated.
	HasHome      bool    `json:"hasHome"`
	HomeCell     CellRef `json:"homeCell"`
	AddressLabel string  `json:"addressLabel,omitempty"`
	District     uint16  `json:"district"`

	// Occupation: Workplace / School are 0 when none; Employment and Sector
	// are the engine labels ("none" sector for the non-employed).
	Employment string `json:"employment"`
	Sector     string `json:"sector"`
	Workplace  uint32 `json:"workplace"`
	School     uint32 `json:"school"`
	Stage      string `json:"stage"`

	Household  uint64 `json:"household"`
	HasPartner bool   `json:"hasPartner"`
	Partner    uint64 `json:"partner"`
	ChildCount int    `json:"childCount"`

	HealthBand        string `json:"healthBand"`
	WealthMicropounds int64  `json:"wealthMicropounds"`

	// Satisfaction is the five axes in citizens' fixed order (housing,
	// services, environment, leisure-fit, commute), each 0..SatisfactionScaleMax.
	Satisfaction [5]int32 `json:"satisfaction"`
}

// OccupantEntry is one row of an occupants page.
type OccupantEntry struct {
	ID   uint64 `json:"id"`
	GUID string `json:"guid"`
	Name string `json:"name"`
}

// OccupantPage is the answer to KindQueryOccupants: one capped page of a stable
// ascending-ID listing. AfterID echoes the request cursor; NextAfterID is the
// last returned id (the cursor for the next page; equal to AfterID when the page
// is empty). Total is the engine's remaining index count for the key: exact for
// what has been visited, an UPPER BOUND otherwise (stale entries are dropped as
// pages reach them). HasMore is true iff a further live occupant exists after
// the last returned entry, verified at the time of the read.
type OccupantPage struct {
	By          string          `json:"by"`
	AfterID     uint64          `json:"afterID"`
	NextAfterID uint64          `json:"nextAfterID"`
	Total       int             `json:"total"`
	HasMore     bool            `json:"hasMore"`
	Entries     []OccupantEntry `json:"entries"`
}
