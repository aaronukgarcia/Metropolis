package citizens

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-517: every citizen minted OUTSIDE the real-birth path (the seed
// population at Wire time, and migrants admitted by engine.attract) was
// created with BirthMonth == the current creation month, i.e. age 0 —
// regardless of whether the record represented a newborn or an adult
// arriving/founding a city. That degenerated the whole age-based economy:
// no citizen could ever be old enough to work or retire within any
// reachable game horizon, which is exactly why FEAT-083's finance de-stub
// had to set workingAgeMinMonths=0 as a placeholder.
//
// This file gives those two paths (seed + migrant) a REALISTIC age at
// creation, drawn from a grounded, documented UK-like age pyramid, via a
// deterministic counter-based stream (det.NewStream) — never math/rand or
// wall-clock time (GR#21). Real newborns (engine.citizens' own fertility
// birth path, birthChildLocked) are UNCHANGED and correctly stay at age 0
// — this file is never called from there.
//
// Ages are represented as a NEGATIVE-BirthMonth extension of the existing
// domain: a citizen "born" 40 years before the world's month-0 genesis has
// BirthMonth == -480, exactly the same way a real city's population at
// founding already contains adults who were born decades earlier. Age() =
// Month - BirthMonth is unaffected by the sign of BirthMonth — the
// derivation was already correct for any int32 BirthMonth, only
// ValidateCitizen/ValidateColdRecord's domain check needed widening (see
// citizen.go) to admit pre-genesis birth months, symmetric with the
// existing MaxInt16 upper bound the cold store's int16 delta encoding
// requires.

// UK-like age-band weights (BUG-517 placeholder, balance-pass adjustable):
// grounded in the ONS mid-2024 UK population pyramid's broad three-band
// shape (roughly 18% under-16, 64% working-age 16-64, 18% aged 65+). These
// are NAMED felt numbers, not the exact ONS figures — a coarse, documented
// starting point the age-based economy can act on, refined in a later
// balance pass.
const (
	// ageBandChildWeight is the relative weight of the 0-15 (child) band.
	ageBandChildWeight = 18
	// ageBandWorkingWeight is the relative weight of the 16-64 (working-age)
	// band.
	ageBandWorkingWeight = 64
	// ageBandRetiredWeight is the relative weight of the 65+ (retired) band.
	ageBandRetiredWeight = 18
	// ageBandWeightTotal is the sum the three weights above are drawn
	// against (must equal ageBandChildWeight+ageBandWorkingWeight+
	// ageBandRetiredWeight — checked by TestAgeBandWeightsSumToTotal).
	ageBandWeightTotal = ageBandChildWeight + ageBandWorkingWeight + ageBandRetiredWeight

	// monthsPerYear converts the band boundaries below (documented in
	// years, matching how population pyramids are normally described) into
	// the engine's native month unit.
	monthsPerYear = 12

	// childMinAgeMonths / childMaxAgeMonths bound the 0-15 band (inclusive
	// at both ends): a newly-created "child" seed/migrant citizen is
	// between 0 and 15 years 11 months old.
	childMinAgeMonths = 0
	childMaxAgeMonths = 16*monthsPerYear - 1

	// workingMinAgeMonths / workingMaxAgeMonths bound the 16-64 band.
	workingMinAgeMonths = 16 * monthsPerYear
	workingMaxAgeMonths = 65*monthsPerYear - 1

	// retiredMinAgeMonths is the start of the 65+ band.
	retiredMinAgeMonths = 65 * monthsPerYear
	// retiredMaxAgeMonths caps the oldest age this draw can produce at 100
	// years — a grounded upper bound (the UK's centenarian population is a
	// small tail of the 65+ band, not representative of its bulk), and it
	// keeps every drawn age comfortably inside BirthMonth's widened int16
	// domain (±32767 months, ~2730 years) regardless of the creation month.
	retiredMaxAgeMonths = 100 * monthsPerYear
)

// DrawAgeAtCreationMonths deterministically draws an age (in whole months)
// for a citizen minted OUTSIDE the real-birth path, from the UK-like age
// pyramid above. worldSeed/citizenID/month key a det.NewStream (never
// math/rand, never time.Now) so the SAME (seed, id, month) always draws the
// SAME age, in any process, any number of times, satisfying GR#21's
// determinism gate and genesis-replay byte-identity.
//
// The first draw picks a band weighted by ageBand*Weight (a single IntN
// over ageBandWeightTotal); the second draw picks a uniform age within
// that band's month range. Both draws come from the same Stream, so the
// pair is fully determined by (worldSeed, citizenID, month) alone.
func DrawAgeAtCreationMonths(worldSeed uint64, citizenID uint64, month int64) int32 {
	stream := det.NewStream(worldSeed, citizenID, month, "age-at-creation")
	bandPick := stream.IntN(ageBandWeightTotal)

	switch {
	case bandPick < ageBandChildWeight:
		return childMinAgeMonths + int32(stream.IntN(childMaxAgeMonths-childMinAgeMonths+1))
	case bandPick < ageBandChildWeight+ageBandWorkingWeight:
		return workingMinAgeMonths + int32(stream.IntN(workingMaxAgeMonths-workingMinAgeMonths+1))
	default:
		return retiredMinAgeMonths + int32(stream.IntN(retiredMaxAgeMonths-retiredMinAgeMonths+1))
	}
}

// BirthMonthForAge computes the BirthMonth that makes a citizen created at
// creationMonth exactly ageMonths old right now: BirthMonth = creationMonth
// - ageMonths. The result is clamped so it can never be:
//   - in the future (BirthMonth > creationMonth would mean a negative age
//     at the very moment of creation — a newborn's floor is ageMonths=0,
//     never negative);
//   - outside BirthMonth's storage domain (the cold store's int16 delta
//     encoding — see citizen.go's widened ValidateCitizen bound).
//
// This is the sole place seed/migrant minting turns a drawn age into the
// BirthMonth ValidateCitizen actually checks, so every caller gets the
// same clamp behaviour for free.
func BirthMonthForAge(creationMonth int64, ageMonths int32) int32 {
	if ageMonths < 0 {
		ageMonths = 0
	}
	bm := creationMonth - int64(ageMonths)
	if bm > creationMonth {
		bm = creationMonth
	}
	if bm > math.MaxInt16 {
		bm = math.MaxInt16
	}
	if bm < math.MinInt16 {
		bm = math.MinInt16
	}
	return int32(bm)
}
