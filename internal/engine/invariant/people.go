package invariant

// PeopleInvariant checks the citizen-conservation balance (§14, US-1):
// the total citizen count across all fidelity tiers (HOT/WARM/COLD,
// §5.2) at the end of the tick must equal the count at the start of the
// tick plus every tracked birth/death/migration event for the tick.
//
// Balance identity: Closing - Opening == TrackedDelta, where
// TrackedDelta = births - deaths + immigration - emigration for the
// tick. A citizen must be traceable to birth, death, or migration; if
// the total changes by any amount not accounted for by those tracked
// events, this invariant reports a Violation — a citizen simply
// vanishing or appearing, uncounted, is exactly the class of bug §14
// exists to catch at the tick it happens.
type PeopleInvariant struct {
	stockCheck
}

// NewPeopleInvariant constructs the people-conservation invariant.
func NewPeopleInvariant() PeopleInvariant {
	return PeopleInvariant{stockCheck{name: "people", stock: StockPeople}}
}
