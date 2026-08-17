package defence

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// FacilityType names a defence facility kind (naval, army, air, and their
// mandate choice variants). It is an open string enum — the §55 facility
// table and the mandate choices are data (data/defence.json), so a future
// facility/choice is a data edit, not a code change.
type FacilityType string

// FacilityID identifies one built facility, assigned monotonically by the
// DefenceAPI (starting at 1; 0 is the "no facility" sentinel).
type FacilityID uint64

// SiteRef identifies one world cell for facility siting. It uses
// engine.world's public coordinate value types (world.TileCoord /
// world.CellLocal) so the siting command can be handed straight to
// engine.build's BuildCommand without a coordinate translation layer — a
// type-only coupling, not an API consumption.
type SiteRef struct {
	Tile  world.TileCoord
	Local world.CellLocal
}

// String renders a compact, deterministic site identity for logs/errors.
func (s SiteRef) String() string {
	return "tile(" + itoa(s.Tile.X) + "," + itoa(s.Tile.Y) + ")@(" + itoa(s.Local.Row) + "," + itoa(s.Local.Col) + ")"
}

// GrantBid is a competitive grant-bid command (AC-2): a pot, a match-funding
// amount, and the simulation month the bid is made in (feeds the
// deterministic hash stream, AC-13).
type GrantBid struct {
	Pot          string
	MatchFunding finance.Money
	Month        int64
}

// GrantResult is [DefenceAPI.BidForGrant]'s outcome: the pot, whether the
// bid won, the award credited on a win, and the win probability the draw was
// made against (queryable so the AC-2 monotonicity is inspectable, not just
// the win/lose roll).
type GrantResult struct {
	Pot            string
	Won            bool
	Award          finance.Money
	WinProbability float64
}

// MandateChoice is one compliant facility/siting choice within a mandate
// (AC-5): a stable id, the facility type it builds, and a human description.
type MandateChoice struct {
	ID           string
	FacilityType FacilityType
	Description  string
}

// Mandate is a data-driven mandate event (AC-1/AC-4): the mandate id, the
// population threshold it fires at, the default facility type, the
// compensation grant attached to compliance, and the ≥2 compliant choices.
type Mandate struct {
	ID                  string
	FacilityType        FacilityType
	PopulationThreshold int64
	Compensation        finance.Money
	Choices             []MandateChoice
}

// MandateResponse is the respond-to-mandate command (AC-5/AC-6). Exactly one
// of Refuse and a compliant Choice must be supplied: Refuse=true records a
// priced refusal (no build); otherwise Choice names one of the mandate's
// choices and Site names where to site it.
type MandateResponse struct {
	MandateID string
	Refuse    bool
	Choice    string
	Site      SiteRef
	OwnerID   uint32
	Month     int64
}

// MandateResult is [DefenceAPI.RespondToMandate]'s outcome: the mandate id,
// whether it was refused, and — on acceptance — the built facility's id and
// the compensation credited.
type MandateResult struct {
	MandateID    string
	Refused      bool
	FacilityID   FacilityID
	Compensation finance.Money
}

// FacilityInfo is the read-only snapshot of one built facility
// ([DefenceAPI.Facility]). Every field is derived from the internal record
// under the API's lock — a consumer can never write back through it.
type FacilityInfo struct {
	ID              FacilityID
	Type            FacilityType
	Site            SiteRef
	MandateID       string
	ChoiceID        string
	Personnel       int64
	MarriedQuarters int64
	SchoolPlaces    int64
	Payroll         finance.Money
	Procurement     finance.Money
	Closed          bool
}

// ClosureEvent is a national-policy facility closure (AC-10): the §55
// "closure events are §32-scale local shocks" output. It carries the closure
// FACTS only — which facility, where, when, and how many jobs are lost — as a
// queryable value a future engine.spiral consumer routes into its shock
// machinery once the engine.defence → engine.spiral edge is registered
// (BUG-058, AC-10). It deliberately carries NO land-value/blight/unemployment
// magnitude of its own: deriving those is engine.spiral's job (GR#3), and a
// defence-owned shock formula would be exactly the "duplicate blight model"
// AC-10's false-pass risk rejects.
type ClosureEvent struct {
	FacilityID   FacilityID
	FacilityType FacilityType
	Site         SiteRef
	Month        int64
	JobsLost     int64
}
