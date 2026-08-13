package save

// Tier is one rung of §4's 13-tier Wilderness->Centopolis milestone
// ladder (docs/METROPOLIS-MASTER-v2.1.md lines 137-157). Number is the
// tier's 1-indexed position in the ladder; Population is the §4
// threshold that tier's milestone save is taken at crossing.
type Tier struct {
	Number     int
	Name       string
	Population int64
}

// Tiers is §4's population-tier ladder, verbatim from the master plan's
// table (lines 143-155). This package does not itself detect a
// population crossing one of these thresholds — that is engine.unlocks'
// job (see doc.go's "Milestone-trigger linkage" section for why); Tiers
// exists so a caller that HAS detected a crossing can pass the matching
// Tier value to [Manager.Milestone] without re-deriving the ladder, and
// so this package's own tests can exercise AC-5 against the real §4
// data rather than an invented placeholder.
var Tiers = []Tier{
	{Number: 1, Name: "Wilderness", Population: 0},
	{Number: 2, Name: "Hamlet", Population: 100},
	{Number: 3, Name: "Village", Population: 500},
	{Number: 4, Name: "Small Town", Population: 5_000},
	{Number: 5, Name: "Town", Population: 20_000},
	{Number: 6, Name: "Large Town", Population: 50_000},
	{Number: 7, Name: "Small City", Population: 100_000},
	{Number: 8, Name: "City", Population: 250_000},
	{Number: 9, Name: "Metropolis", Population: 1_000_000},
	{Number: 10, Name: "Conurbation", Population: 5_000_000},
	{Number: 11, Name: "Megacity", Population: 10_000_000},
	{Number: 12, Name: "Megalopolis", Population: 50_000_000},
	{Number: 13, Name: "Centopolis", Population: 100_000_000},
}
