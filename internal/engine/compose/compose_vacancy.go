package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// BUG-1214 (FEAT-083 leg 4) -- the real housing-vacancy signal for
// engine.attract: what the player builds changes who migrates in.
//
// Before: attract's HousingVacancy was the 1,000,000 placeholder, so no amount
// of building (or of overcrowding) could move migration.
//
// Now: vacancy (dwelling units) = floor(max(0, stock - population) / people
// per migrant household), where stock = the seed city's starting homes plus the
// resident capacity of every standing structure build reports. A city with no
// spare homes admits no migrants; building dwellings raises the figure.
//
// Units: people (capacity, population) -> dwelling units (vacancy); the
// conversion divisor equals attract's migrantHouseholdSize (2), which attract
// multiplies back to people.

// baselineOneSeedHomeCapacityPeople is the resident capacity of the seed city's
// pre-existing homes (those no player action built), in people.
// BALANCE PLACEHOLDER - Aaron row-by-row
const baselineOneSeedHomeCapacityPeople = int64(120)

// vacancyPeoplePerUnit mirrors attract's migrantHouseholdSize (two citizens
// per admitted migrant household). BALANCE PLACEHOLDER - Aaron row-by-row
const vacancyPeoplePerUnit = int64(2)

// housingVacancyUnits is the pure vacancy rule. Zero-signal-safe: negative
// inputs clamp to 0 and a stock below the population yields 0.
func housingVacancyUnits(seedPeople, builtPeople, population int64) int64 {
	if seedPeople < 0 {
		seedPeople = 0
	}
	if builtPeople < 0 {
		builtPeople = 0
	}
	if population < 0 {
		population = 0
	}
	stock := num.SatAdd(seedPeople, builtPeople)
	free := stock - population
	if free <= 0 {
		return 0
	}
	return free / vacancyPeoplePerUnit
}

// housingVacancy reads the live figures (deterministic: build's slice is in a
// fixed order and only summed). A nil build or citizens module reads the
// zero-signal case (seed stock only, population 0).
func housingVacancyFor(b *build.BuildAPI, c *citizens.CitizensAPI, cid string, seedOverride int64) int64 {
	var pop int64
	if c != nil {
		pop = int64(c.TotalPopulation(cid))
	}
	return housingVacancyUnits(housingStockFor(b, seedOverride), 0, pop)
}

// housingStockFor is the one place the home stock is summed: the seed city's
// starting homes plus the resident capacity of every standing structure build
// reports, in people. housingVacancyFor and the BUG-1220 housing publish both
// read it, so the player-facing "places" figure and the migration rule can
// never be two notions of homes. A nil build reads the seed stock only.
func housingStockFor(b *build.BuildAPI, seedOverride int64) int64 {
	var built int64
	if b != nil {
		for _, s := range b.StandingCapacities() {
			if s.ResidentCapacity > 0 {
				built = num.SatAdd(built, s.ResidentCapacity)
			}
		}
	}
	seed := baselineOneSeedHomeCapacityPeople
	if seedOverride > 0 {
		seed = seedOverride
	}
	return num.SatAdd(seed, built)
}
