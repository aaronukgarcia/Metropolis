package converge

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PopulationDomain is FEAT-1972079936 Phase 3's third converged domain: the
// NATURAL CHANGE of the population (births, deaths, headcount) — migration
// is out of scope and held at zero on both sides (docs/planning/acceptance/
// FEAT-1972079936-phase3-next-domain.md section 1).
//
// # Reference vs candidate
//
// The Go engine is the REFERENCE: Run drives a fresh, real
// [citizens.CitizensAPI] (seeded via SeedColdRecords/SeedHouseholds, advanced
// via AdvanceDayTick) and every sampled figure is read back from it (AC-1) —
// births/deaths are the sums of AdvanceDayTick's own return values and
// "population" is [citizens.CitizensAPI.TotalPopulation]. The TS reducer is
// the CANDIDATE (testdata/population-webconsole-v1.json, emitted by
// webconsole/test/converge-fixture-emit-population.mjs). Report-only: nothing
// flips and neither model is tuned toward the other.
//
// # Journal ops (Journal shape {tick, op, args})
//
//   - "seed" {count, ageYears}: appends a cohort of count citizens aged
//     ageYears (months-exact at genesis month 0). Ids are minted sequentially
//     from populationSeedIDBase in journal order (GR#21: never a map range).
//     Adjacent same-cohort citizens inside the data-file childbearing window
//     are paired as mutual partners sharing a household (the same pairing
//     headless.generateSeedPopulation uses, otherwise fertility is
//     structurally zero — BUG-665).
//   - "advance" {days}: AdvanceDayTick x days, accumulating births/deaths.
//   - "sample": records one Sample at entry.Tick: "births"/"deaths" since the
//     PREVIOUS sample, "population" (TotalPopulation), and "seed_headcount"
//     (the total seeded, a TierExact sanity row).
type PopulationDomain struct{}

// Name implements Domain.
func (PopulationDomain) Name() string { return "population" }

// populationSeedIDBase is the first citizen id minted by the "seed" op.
const populationSeedIDBase = uint64(1_000_000)

// populationMaxCohort bounds a single seed op (fail closed on absurd input).
const populationMaxCohort = 1_000_000

// populationMaxAgeYears bounds a cohort's age (ColdRecord.BirthMonth is int16).
const populationMaxAgeYears = 110

// Contract implements Domain: population/births/deaths are TierDistribution
// (PopulationDistWindow samples, PopulationBandPct), the seeded headcount is
// TierExact (AC-3). Fresh map each call.
func (PopulationDomain) Contract() Contract {
	dist := Tolerance{Tier: TierDistribution, Window: PopulationDistWindow, BandPct: PopulationBandPct}
	return Contract{
		"population":     dist,
		"births":         dist,
		"deaths":         dist,
		"seed_headcount": {Tier: TierExact},
	}
}

func populationOpFailed(entry JournalEntry, reason string) error {
	return errs.New(codeJournalOpFailed, errs.NewCorrelationID(), map[string]any{
		"tick": entry.Tick, "op": entry.Op, "domain": "population", "reason": reason,
	})
}

// Run implements Domain. Deterministic (GR#21): a fixed world seed, ids
// minted in journal order, no wall clock, no map ranges.
func (PopulationDomain) Run(j Journal) (Trajectory, error) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(PopulationWorldSeed, cid)
	if err != nil {
		return nil, err
	}
	minMonths, maxMonths, err := populationFertilityWindow(cid)
	if err != nil {
		return nil, err
	}

	var traj Trajectory
	nextID := populationSeedIDBase
	var seeded int64
	var births, deaths int64 // since the previous sample

	for _, entry := range j.Entries {
		switch entry.Op {
		case "seed":
			var args struct {
				Count    int64 `json:"count"`
				AgeYears int64 `json:"ageYears"`
			}
			if err := json.Unmarshal(entry.Args, &args); err != nil {
				return nil, populationOpFailed(entry, fmt.Sprintf("malformed args: %v", err))
			}
			if args.Count < 0 {
				return nil, populationOpFailed(entry, "count must be non-negative")
			}
			if args.Count == 0 || args.Count > populationMaxCohort {
				return nil, populationOpFailed(entry, fmt.Sprintf("count %d out of range [1,%d]", args.Count, populationMaxCohort))
			}
			if args.AgeYears < 0 || args.AgeYears > populationMaxAgeYears {
				return nil, populationOpFailed(entry, fmt.Sprintf("ageYears %d out of range [0,%d]", args.AgeYears, populationMaxAgeYears))
			}
			recs := populationCohort(nextID, args.Count, args.AgeYears*12, minMonths, maxMonths)
			if err := api.SeedColdRecords(recs, cid); err != nil {
				return nil, populationOpFailed(entry, err.Error())
			}
			if err := api.SeedHouseholds(recs, cid); err != nil {
				return nil, populationOpFailed(entry, err.Error())
			}
			nextID += uint64(args.Count)
			seeded += args.Count

		case "advance":
			var args struct {
				Days int64 `json:"days"`
			}
			if err := json.Unmarshal(entry.Args, &args); err != nil {
				return nil, populationOpFailed(entry, fmt.Sprintf("malformed args: %v", err))
			}
			if args.Days < 1 {
				return nil, populationOpFailed(entry, "days must be >= 1")
			}
			for d := int64(0); d < args.Days; d++ {
				b, dd, err := api.AdvanceDayTick(cid)
				if err != nil {
					return nil, populationOpFailed(entry, err.Error())
				}
				births += int64(b)
				deaths += int64(dd)
			}

		case "sample":
			traj = append(traj, Sample{Tick: entry.Tick, Values: map[string]int64{
				"population":     int64(api.TotalPopulation(cid)),
				"births":         births,
				"deaths":         deaths,
				"seed_headcount": seeded,
			}})
			births, deaths = 0, 0

		default:
			return nil, populationOpFailed(entry, "unrecognised op name")
		}
	}
	return traj, nil
}

// populationFertilityWindow reads the childbearing-age window (months) from
// the data-file fertility config (GR#15: data, never a literal).
func populationFertilityWindow(cid string) (int64, int64, error) {
	cfg, err := citizens.LoadDefaultFertilityConfig(cid)
	if err != nil {
		return 0, 0, err
	}
	return int64(cfg.Params.MinChildbearingAgeYears.Value * 12), int64(cfg.Params.MaxChildbearingAgeYears.Value * 12), nil
}

// populationCohort builds count ColdRecords aged ageMonths at genesis month 0,
// ids [firstID, firstID+count). All other fields are bounded id-modulo walks
// (no RNG), mirroring headless.generateSeedPopulation. Adjacent pairs inside
// the fertility window are mutual partners in household = the first id.
func populationCohort(firstID uint64, count, ageMonths, minMonths, maxMonths int64) []citizens.ColdRecord {
	recs := make([]citizens.ColdRecord, count)
	birth := int64(citizens.BirthMonthForAge(0, int32(ageMonths)))
	for i := int64(0); i < count; i++ {
		id := firstID + uint64(i)
		var p [citizens.NumPersonalityAxes]int8
		for axis := range p {
			p[axis] = int8((id + uint64(axis)) % 101)
		}
		recs[i] = citizens.ColdRecord{
			ID:              id,
			BirthMonth:      birth,
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
	if ageMonths >= minMonths && ageMonths <= maxMonths {
		for i := int64(0); i+1 < count; i += 2 {
			a, b := &recs[i], &recs[i+1]
			a.Partner, b.Partner = b.ID, a.ID
			a.Household, b.Household = a.ID, a.ID
		}
	}
	return recs
}
