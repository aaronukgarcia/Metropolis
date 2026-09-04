package headless

import (
	"fmt"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// PerfSeedIDBase (BUG-665) is the id-space floor Config.SeedCitizenCount's
// bulk cold-seeded population starts from. It must stay disjoint from
// every other id range this codebase mints citizens from:
//
//   - compose's own genesis founder seed mints ids [1, 64] sequentially
//     (compose.go's seedCitizenCount constant, spawnCitizens) — this
//     range starts at 1,000,001, comfortably clear of 64.
//   - engine.attract's migrant ids start at attract.MigrantIDBase
//     (1<<62) — this range tops out at PerfSeedIDBase + 100,000,000
//     (the 100M finale ceiling this item's own item, BUG-665, exists to
//     let CI actually measure), still ~40 quintillion below 1<<62.
//   - engine.citizens' own fertility-child ids start at 1<<63, disjoint
//     from both of the above for the identical reason (see
//     compose.spawnCitizens' id-seam guard doc comment, which names the
//     live FEAT-169 defect a silent collision between exactly these
//     kinds of ranges caused).
const PerfSeedIDBase = 1_000_000

// seedGenesisMonth is the simulation month a BUG-665-seeded population is
// generated AND paired at — mirrors compose.spawnCitizens(0, ...)'s own
// genesis-month convention exactly (the seeded population, like compose's
// own founder seed, is a founding city's residents, not citizens born
// partway through the run).
const seedGenesisMonth = int64(0)

// fertilityWindowMonthsOnce/fertilityMinMonths/fertilityMaxMonths cache the
// data/fertility.json childbearing-age window (GR#15: never a hand-picked
// literal), loaded exactly once regardless of how many times
// generateSeedPopulation runs (a 100,000,000-citizen generation call must
// not re-read and re-parse the data file per record). See
// loadFertilityWindowMonths' own doc comment for why a panic, not an
// error return, is this package's answer to a load failure here.
var (
	fertilityWindowMonthsOnce sync.Once
	fertilityMinMonths        int64
	fertilityMaxMonths        int64
)

// loadFertilityWindowMonths returns (minChildbearingAgeMonths,
// maxChildbearingAgeMonths) from data/fertility.json, loaded once (GR#15:
// the window is data, never a Go literal) and cached for the life of the
// process.
//
// Panics on a load failure rather than returning an error: this function
// exists to serve generateSeedPopulation(seed uint64, n int64)
// []citizens.ColdRecord, a signature this round's own destructive-round
// evidence (attack_bug665_round_test.go) calls with exactly those two
// arguments and takes only the one return value — widening it to
// (..., error) would break that committed, must-stay-green evidence file
// for no benefit, since a data/fertility.json load failure here means the
// engine is already unusable (citizens.NewCitizensAPI itself loads the
// SAME file and fails identically for every other real caller in this
// codebase — this is not a new, narrower failure mode this function
// introduces, just one with no error-return outlet available to report it
// through).
func loadFertilityWindowMonths() (int64, int64) {
	fertilityWindowMonthsOnce.Do(func() {
		cfg, err := citizens.LoadDefaultFertilityConfig("headless.generateSeedPopulation")
		if err != nil {
			panic(fmt.Sprintf("headless.generateSeedPopulation: citizens.LoadDefaultFertilityConfig failed: %v", err))
		}
		fertilityMinMonths = int64(cfg.Params.MinChildbearingAgeYears.Value * 12)
		fertilityMaxMonths = int64(cfg.Params.MaxChildbearingAgeYears.Value * 12)
	})
	return fertilityMinMonths, fertilityMaxMonths
}

// generateSeedPopulation deterministically builds n citizens.ColdRecord
// values for headless.Config.SeedCitizenCount (BUG-665): ids
// [PerfSeedIDBase+1, PerfSeedIDBase+n]. Age and personality are drawn
// through the SAME det-stream helpers compose.spawnCitizens uses for the
// genesis founder population (citizens.DrawAgeAtCreationMonths /
// citizens.InitPersonality), so a perf-probe population is derived from
// the world seed exactly like a real one — never math/rand, never the
// wall clock (GR#21: this whole path must stay bit-identical across
// platforms and repeat runs). Every other field is a bounded, purely
// arithmetic (id-keyed modulo) walk over its own enum's valid domain: no
// extra det.Stream draws are spent on them, so generation stays cheap
// and O(n) even at 100,000,000 records, but the population is not one
// single homogeneous branch either — Stage/HealthBand/EmploymentState/
// Sector all vary across the seeded set, which matters for a perf probe:
// a population that only ever exercises "row 0 of every branch" would be
// exactly the kind of vacuous measurement BUG-665 exists to close.
//
// This mirrors internal/engine/citizens' own test helper (mkRecord,
// coldshard_test.go) field-for-field where the domains coincide — not a
// second, independently-invented shape for the same kind of fixture
// (GR#3) — but lives in this package rather than that one because every
// field/type/constant it touches (ColdRecord, Sex, Stage, HealthBand,
// EmploymentState, Sector, DrawAgeAtCreationMonths, InitPersonality,
// BirthMonthForAge, NumPersonalityAxes) is already part of
// engine.citizens' PUBLIC surface (SeedColdRecords' own doc comment:
// "the harness.synth path") — this package never reaches into
// ColdShard or any other unexported citizens.go internal.
//
// # BUG-665 round finding: mutual partner pairing during generation
//
// An independent destructive round proved the first landing's records
// (Household==0, Partner==0 for every citizen) made the seeded population
// invisible to fertility.go's applyFertilityLocked ENTIRELY — it scans
// ColdRecord.Partner directly and skips any citizen with Partner==0 — so
// births were structurally zero regardless of tick count or population
// size, a second, subtler shape of the exact vacuity class BUG-665 exists
// to close. A second pass below pairs adjacent records
// (records[i], records[i+1]) into mutual partners whenever BOTH fall
// inside the data-file childbearing-age window (loadFertilityWindowMonths,
// GR#15) — mirroring fertility.go's own "acting partner is the LOWER id"
// convention (applyFertilityLocked's `if partner < id { continue }`
// dedup-skip) exactly: since records[i].ID < records[i+1].ID always
// (sequential minting), citizen i is the one whose monthly fertility scan
// actually runs. The shared household id is deliberately records[i].ID
// itself (this seeded range's own id space, never compose's own
// nextHouseholdID counter) — a raw field write here is NOT enough on its
// own to make a birth succeed (fertility.go's birthChildLocked validates
// the child's Household against c.households via householdExistsLocked),
// so a caller MUST also register these households via
// citizens.CitizensAPI.SeedHouseholds(records, correlationID) after
// SeedColdRecords, before any tick advances (see SeedHouseholds' own doc
// comment for why this is a separate, explicit, O(n) call rather than
// folded into SeedColdRecords itself).
func generateSeedPopulation(seed uint64, n int64) []citizens.ColdRecord {
	records := make([]citizens.ColdRecord, n)
	for i := int64(0); i < n; i++ {
		id := uint64(PerfSeedIDBase) + uint64(i) + 1

		age := citizens.DrawAgeAtCreationMonths(seed, id, seedGenesisMonth)
		personality := citizens.InitPersonality(seed, id, seedGenesisMonth, citizens.Personality{}, citizens.Personality{})
		var p [citizens.NumPersonalityAxes]int8
		for axis := 0; axis < citizens.NumPersonalityAxes; axis++ {
			// InitPersonality's own domain is [0, MaxPersonalityAxis]
			// (100) — always representable in int8 with no clamp needed.
			p[axis] = int8(personality[axis])
		}

		records[i] = citizens.ColdRecord{
			ID:              id,
			BirthMonth:      int64(citizens.BirthMonthForAge(seedGenesisMonth, age)),
			Sex:             citizens.Sex(id % 2),
			Personality:     p,
			Stage:           citizens.Stage(id % (uint64(citizens.StageAdultEd) + 1)),
			HealthBand:      citizens.HealthBand(id % (uint64(citizens.MaxHealthBand) + 1)),
			EmploymentState: citizens.EmploymentState(id % (uint64(citizens.EmploymentOffMap) + 1)),
			Sector:          citizens.Sector(id % (uint64(citizens.SectorPublic) + 1)),
			Access:          uint8(id % 101),
			SatHousing:      int32(id % 101),
			SatServices:     int32(id % 101),
			SatEnvironment:  int32(id % 101),
			SatLeisureFit:   int32(id % 101),
			SatCommute:      int32(id % 101),
		}
	}

	// Second pass: mutual partner pairing (see this function's own doc
	// comment above). Pure arithmetic + one cached config read — O(n),
	// no det.Stream draws, no citizens.CitizensAPI calls, so this stays
	// cheap even at 100,000,000 records exactly like the first pass.
	minMonths, maxMonths := loadFertilityWindowMonths()
	for i := 0; i+1 < len(records); i += 2 {
		a, b := &records[i], &records[i+1]
		ageA := seedGenesisMonth - a.BirthMonth
		ageB := seedGenesisMonth - b.BirthMonth
		if ageA < minMonths || ageA > maxMonths || ageB < minMonths || ageB > maxMonths {
			continue
		}
		a.Partner = b.ID
		b.Partner = a.ID
		a.Household = a.ID
		b.Household = a.ID
	}

	return records
}
