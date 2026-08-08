package protocol

// Severity classifies an Event for the UI's ticker/bulletin/alert
// surfaces (GDD §29) and the F12 log tail (code.json
// conventions.errorHandling.logging). It is a closed, small set — new
// severities are a protocol change, not a per-event free-text field,
// because the UI's colour palette and F7 alert routing (GDD §13) switch
// on it directly.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Event is a discrete, named occurrence the engine reports — as opposed
// to Delta's continuous state patches. Ticker items (GDD §29), milestone
// crossings (GDD §4), gang formation (GDD §28), a determinism-gate
// failure surfaced to the F12 panel — all Events. Kind is intentionally
// a free-form string here (unlike Command's closed, registry-decoded
// Kind) because the Event vocabulary belongs to whichever engine module
// raises it, not to this neutral protocol package; the protocol only
// fixes the envelope shape every event must carry.
type Event struct {
	// Kind names the event, module-namespaced by convention (e.g.
	// "milestone.reached", "gang.formed", "junction.gridlocked") so the
	// UI's routing table and the News System (GDD §29) can pattern-match
	// on prefix without a central enum living in this package.
	Kind string `json:"kind"`

	// Tick is the simulation tick the event occurred at (never wall
	// time — see Tick's doc comment).
	Tick Tick `json:"tick"`

	Severity Severity `json:"severity"`

	// EntityRefs names the entities the event is about (a road, a
	// citizen, a district), same opaque-reference convention as
	// InspectEntityPayload.EntityRef — lets the UI's drill-through rule
	// (UI-SPEC §4: "every number on every screen is selectable and Enter
	// goes to its source") jump straight from a ticker item to the thing
	// it's about.
	EntityRefs []string `json:"entityRefs,omitempty"`

	// Fields carries the event's structured detail (e.g. {"street":
	// "Pent Lane", "cause": "port strike"}) for bulletin prose and log
	// context — deliberately map[string]string, matching
	// DebugPayload.Args and SubscribePayload.Params, so nothing in this
	// neutral package needs to know any event's specific shape.
	Fields map[string]string `json:"fields,omitempty"`

	// CorrelationID echoes the Command that caused this event, where
	// causal (e.g. a Debug command that force-triggers a milestone).
	// Most Events are caused by simulation progression rather than a
	// single command and this is empty in that case — see Delta's
	// identical field for the same rationale.
	CorrelationID CorrelationID `json:"correlationId,omitempty"`
}
