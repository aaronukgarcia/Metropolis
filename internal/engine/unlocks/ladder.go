package unlocks

// Milestone is one rung of §4's Population Scale & the Milestone Ladder.
type Milestone struct {
	// Tier is the 1-based milestone number (1 = Wilderness ..
	// 13 = Centopolis).
	Tier int
	// Name is the §4 tier name (Wilderness…Centopolis).
	Name string
	// Population is the population threshold at which this tier is
	// crossed (§4's table, second column).
	Population int64
}

// milestoneLadder is §4's thirteen-tier ladder, transcribed verbatim from
// docs/METROPOLIS-MASTER-v2.1.md §4 (the "Tier / Population" table).
//
// This is SPEC data, not config data: unlike the DP trees (which live in
// data/unlock_trees.json and are loaded via foundation.data), the
// milestone ladder's population thresholds and tier names exist only in
// §4's table — no §24 config file carries them. The values are therefore
// transcribed here verbatim (GR#15's "transcribed from §4, not invented")
// and pinned to §4 by TestMilestoneLadderMatchesSpec, rather than read
// from a data file this module does not own. The category COUNT (12) and
// node graph ARE data-driven, from data/unlock_trees.json. This division
// is recorded as an ASM (see the dispatch report) — data-authoring a
// milestones.json is data.unlocktrees's scope, not this module's.
var milestoneLadder = []Milestone{
	{Tier: 1, Name: "Wilderness", Population: 0},
	{Tier: 2, Name: "Hamlet", Population: 100},
	{Tier: 3, Name: "Village", Population: 500},
	{Tier: 4, Name: "Small Town", Population: 5_000},
	{Tier: 5, Name: "Town", Population: 20_000},
	{Tier: 6, Name: "Large Town", Population: 50_000},
	{Tier: 7, Name: "Small City", Population: 100_000},
	{Tier: 8, Name: "City", Population: 250_000},
	{Tier: 9, Name: "Metropolis", Population: 1_000_000},
	{Tier: 10, Name: "Conurbation", Population: 5_000_000},
	{Tier: 11, Name: "Megacity", Population: 10_000_000},
	{Tier: 12, Name: "Megalopolis", Population: 50_000_000},
	{Tier: 13, Name: "Centopolis", Population: 100_000_000},
}

// milestoneAt returns the milestone with the given 1-based tier, and
// whether it exists. tier 0 (the "no milestone reached yet" sentinel) is
// not a real milestone and reports ok == false.
func milestoneAt(tier int) (Milestone, bool) {
	if tier < 1 || tier > len(milestoneLadder) {
		return Milestone{}, false
	}
	return milestoneLadder[tier-1], true
}
