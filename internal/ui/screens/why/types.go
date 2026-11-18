package why

// This file is THE CONTRACT between the engine lane that publishes the
// "f5.why" view and this screen. Everything the screen decodes is declared
// here, once, with its meaning. The golden fixtures in testdata/ are
// decoded through this type by the tests, so the Go type and the JSON
// cannot drift apart unnoticed.
//
// Decoding is lenient where the lanes' two documents differ (both spellings
// are accepted, see the alias notes) and strict where a wrong value would
// make the panel lie (lever count, ranges, finite numbers).

// LeverCount is the number of lever lines: Aaron's ten equal-weight levers.
const LeverCount = 10

// ModifierCount is the number of extra modifier lines (safety, environment).
const ModifierCount = 2

// LeverIDs is the fixed lever order. The screen owns the display names; the
// patch carries the levers in exactly this order. If a lever carries an id
// or a name it must normalise to the id at its position (letters and digits
// only, lower case; "Health care", "healthcare" and "health care" all match
// "healthcare", and "transport links" matches "transport").
var LeverIDs = [LeverCount]string{
	"housing", "jobs", "services", "healthcare", "policy",
	"shops", "parks", "transport", "congestion", "tourism",
}

// LeverNames are the on-screen names, in the same order as LeverIDs.
var LeverNames = [LeverCount]string{
	"Housing", "Jobs", "Services", "Health care", "Policy",
	"Shops", "Parks", "Transport links", "Congestion", "Tourism",
}

// ModifierIDs is the fixed modifier order (safety, then environment).
var ModifierIDs = [ModifierCount]string{"safety", "environment"}

// ModifierNames are the on-screen modifier names.
var ModifierNames = [ModifierCount]string{"Safety", "Environment"}

// Limit values for Patch.LimitedBy.
const (
	LimitHomes     = "homes"
	LimitTransport = "transport"
)

// Trend values for Patch.ReputationTrend.
const (
	TrendRising  = "rising"
	TrendSteady  = "steady"
	TrendFalling = "falling"
)

// Part is one number behind a built lever, e.g. {"rent share", 29}. The
// screen prints parts as "k v" joined by commas in the why-text column and
// owns every other word.
type Part struct {
	// K is a short lower-case label (at most 24 bytes shown).
	K string `json:"k"`
	// V is the number, printed with at most one decimal.
	V float64 `json:"v"`
}

// Lever is one lever line (also used for the two modifiers).
type Lever struct {
	// ID is optional: housing, jobs, services, healthcare, policy, shops,
	// parks, transport, congestion, tourism (or safety / environment).
	ID string `json:"id,omitempty"`
	// Name is optional and an alias of ID (a display name such as
	// "Health care" is accepted). Position decides the line; a present ID or
	// Name that does not match the position refuses the patch.
	Name string `json:"name,omitempty"`
	// Built is false while nothing real feeds the lever. An unbuilt lever
	// contributes nothing: the screen prints "not built yet", value "--" and
	// points 0.0 whatever Value and Points say.
	Built bool `json:"built"`
	// Value is the lever's reading on 0..100 (higher pulls, lower pushes).
	// Ignored when Built is false. A built value outside 0..100 refuses the
	// patch.
	Value float64 `json:"value"`
	// Points is the lever's share of the score relative to the neutral
	// world value: 0.1 x (Value - AWorld) when built, 0 when not. Printed
	// signed with one decimal. A built value outside +-100 refuses the patch.
	Points float64 `json:"points"`
	// Parts are the numbers behind a built lever (at most 3 are shown).
	Parts []Part `json:"parts,omitempty"`
}

// Homes mirrors the chrome housing block (same field names as
// chrome.HousingFigures) so the panel and the top bar's second row can never
// disagree. Optional; nil means "not sent". It is decoded and kept but not
// drawn: the chrome second row already shows homes, and the panel's arrivals
// line carries wanted against room.
type Homes struct {
	Known   bool  `json:"known"`
	Places  int64 `json:"places"`
	Lived   int64 `json:"lived"`
	Free    int64 `json:"free"`
	MovedIn int64 `json:"movedIn"`
	Left    int64 `json:"left"`
	Full    bool  `json:"full"`
}

// Homeless is the slot reserved for the later homelessness meter
// (FEAT-9995385613). Optional. While Built is false (or the slot is absent)
// the screen prints the labelled placeholder "homelessness: not built yet".
type Homeless struct {
	Built bool `json:"built"`
	// Count is the number of homeless people when Built (printed as a count;
	// the meter itself is a later lane).
	Count int64 `json:"count,omitempty"`
}

// Patch is one "f5.why" delta patch: the full state, replacing the last one.
//
// Field map to the engine lane's result:
//
//	levers[]            per lever {name, built, value, points}
//	modifiers[]         per modifier {name, built, value, points}
//	reputationPoints    momentum points
//	wellbeingFactor     multiplier on the score (1.0 = no effect)
//	wellbeingPoints     points added by the wellbeing factor
//	score               A, the city's attraction score after the wellbeing factor
//	aWorld / neutral    the neutral world value (alias spellings; aWorld wins)
//	net                 migration net, people per month (= score - neutral)
//	wanted, room        whole-person net before the cap, and the cap
//	limitedBy           "homes" or "transport"
//	movedIn / inflow    arrivals this month (alias spellings; movedIn wins)
//	left / outflow      departures this month (alias spellings; left wins)
//	month               the month that closed
//	known               false until the first month closes (and after a load
//	                    until the next close); every other field except
//	                    schemaVersion is then ignored
//
// The on-screen identity the screen relies on, and the test asserts:
// sum(levers.points) + sum(modifiers.points) + reputationPoints +
// wellbeingPoints = net, to within 0.05. When the identity holds the screen
// rounds the lines in tenths so the printed numbers add up exactly; when it
// does not hold the screen prints the raw rounded figures (the mismatch then
// shows, as it should).
type Patch struct {
	// SchemaVersion must be 1.
	SchemaVersion int `json:"schemaVersion"`
	// Known is false until the first month closes (and after a load until the
	// next close).
	Known bool `json:"known"`
	// Month is the month number that closed (non-negative).
	Month int64 `json:"month"`

	// AWorld is the neutral world value (the document's name); Neutral is the
	// engine lane's spelling. Either may be sent; one is required when Known.
	AWorld  *float64 `json:"aWorld,omitempty"`
	Neutral *float64 `json:"neutral,omitempty"`
	// Score is A after the wellbeing factor.
	Score float64 `json:"score"`
	// Net is the migration net before the cap, in people (== Wanted when the
	// migration rate is 1.0).
	Net float64 `json:"net"`

	// Levers holds exactly LeverCount entries in LeverIDs order.
	Levers []Lever `json:"levers"`
	// Modifiers holds exactly ModifierCount entries (safety, environment) or
	// none (then both lines read "not built yet").
	Modifiers []Lever `json:"modifiers,omitempty"`

	// Reputation is the momentum / pride reading, signed, -100..100.
	Reputation float64 `json:"reputation"`
	// ReputationPoints is its share of the net.
	ReputationPoints float64 `json:"reputationPoints"`
	// ReputationTrend is "rising", "steady" or "falling". Optional; when
	// absent the screen derives it from the sign of ReputationPoints
	// (beyond +-0.05).
	ReputationTrend string `json:"reputationTrend,omitempty"`

	// WellbeingFactor multiplies the score (1.0 = no effect); WellbeingPoints
	// is the effect in points.
	WellbeingFactor float64 `json:"wellbeingFactor"`
	WellbeingPoints float64 `json:"wellbeingPoints"`

	// Wanted is the whole-person net before the cap (negative when people
	// want to leave); Room is the cap in people; LimitedBy names what sets
	// the cap, "homes" or "transport" (empty when nothing limits).
	Wanted    int64  `json:"wanted"`
	Room      int64  `json:"room"`
	LimitedBy string `json:"limitedBy,omitempty"`

	// MovedIn / Left (aliases Inflow / Outflow) are this month's arrivals and
	// departures.
	MovedIn *int64 `json:"movedIn,omitempty"`
	Left    *int64 `json:"left,omitempty"`
	Inflow  *int64 `json:"inflow,omitempty"`
	Outflow *int64 `json:"outflow,omitempty"`

	// Homes is the optional homes block; Homeless the optional reserved slot.
	Homes    *Homes    `json:"homes,omitempty"`
	Homeless *Homeless `json:"homeless,omitempty"`
}
