package census

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// numAgeBands is the fixed number of age bands the census's age spline
// uses (a schema constant, matching engine.citizens' A7 stratification).
const numAgeBands = 5

// Sex-series indices (schema constants).
const (
	sexIndexFemale = 0
	sexIndexMale   = 1
)

// Aggregates is the stats generator's output (US-2): city statistics
// computed as a deterministic aggregation over the owning modules' query
// surfaces — never a census-local estimate. Every field is a sum/aggregate
// of what the sources report.
type Aggregates struct {
	Population int64

	AgeBands       [numAgeBands]int64
	Sex            [2]int64 // [female, male]
	EducationTiers [numStages]int64

	Employed   int64
	Unemployed int64
	Retired    int64
	Students   int64

	MeanHealth float64

	CrimeRate     float64
	UnfedFraction float64

	MeanAttainment     float64
	UneducatedFraction float64

	TotalIncome int64 // micro-pounds
	MeanIncome  int64 // micro-pounds
}

// stageIndex bounds-checks a source-controlled StageKind into a valid
// [0, numStages) EducationTiers bucket index (SEC-126). A stage outside the
// census's stage domain is mapped to StageNone; the snapshot boundary rejects
// such a stage with ErrInvalidStageKind before it can reach the aggregation,
// so this mapping is defence-in-depth for a hand-built Snapshot — never a
// silent acceptance of a source-controlled value.
func stageIndex(k StageKind) int {
	if k >= numStages {
		return int(StageNone)
	}
	return int(k)
}

// ageBandIndex maps an age in months to its census age band (0..4). Pure
// and deterministic — no wall clock (GR#21).
func ageBandIndex(ageMonths int64) int {
	years := ageMonths / 12
	switch {
	case years < 18:
		return 0
	case years < 35:
		return 1
	case years < 55:
		return 2
	case years < 75:
		return 3
	default:
		return 4
	}
}

// AgeBandSeries returns the population's age-band distribution (AC-18): a
// deterministic function of per-citizen birth month vs the snapshot tick.
func (c *CensusAPI) AgeBandSeries(snap *Snapshot) [numAgeBands]int64 {
	var out [numAgeBands]int64
	for _, cv := range snap.Citizens {
		out[ageBandIndex(num.SatSub(snap.Tick, cv.BirthMonth))]++
	}
	return out
}

// SexSeries returns the population's sex distribution [female, male]
// (AC-18): a deterministic function of per-citizen sex.
func (c *CensusAPI) SexSeries(snap *Snapshot) [2]int64 {
	var out [2]int64
	for _, cv := range snap.Citizens {
		if cv.Sex == SexMale {
			out[sexIndexMale]++
		} else {
			out[sexIndexFemale]++
		}
	}
	return out
}

// EducationTierSeries returns the population's highest-education-tier
// distribution (AC-18): each citizen contributes to the bucket of their
// highest recorded stage. It is computed independently of the age/sex
// series — perturbing only education inputs leaves those byte-identical.
func (c *CensusAPI) EducationTierSeries(snap *Snapshot) [numStages]int64 {
	var out [numStages]int64
	for _, cv := range snap.Citizens {
		ev, ok := snap.Education[cv.ID]
		if !ok {
			out[StageNone]++
			continue
		}
		out[stageIndex(ev.highestStage())]++
	}
	return out
}

// Stats runs the stats generator (AC-3): it aggregates every city statistic
// as a deterministic function of the snapshot. Repeated runs over an
// identical snapshot produce byte-identical output; mutating one source's
// input changes exactly the aggregates derived from it.
func (c *CensusAPI) Stats(snap *Snapshot) Aggregates {
	var agg Aggregates
	agg.Population = int64(len(snap.Citizens))
	agg.AgeBands = c.AgeBandSeries(snap)
	agg.Sex = c.SexSeries(snap)
	agg.EducationTiers = c.EducationTierSeries(snap)

	var attSum, attCount, incSum, incCount, healthSum, uneducated int64
	floor := c.cfg.Thresholds.UneducatedAttainmentFloor.Value

	for _, cv := range snap.Citizens {
		switch cv.Employment {
		case EmploymentEmployed:
			agg.Employed++
		case EmploymentUnemployed:
			agg.Unemployed++
		case EmploymentRetired:
			agg.Retired++
		case EmploymentStudent:
			agg.Students++
		}
		healthSum = num.SatAdd(healthSum, int64(cv.HealthBand))

		if ev, ok := snap.Education[cv.ID]; ok {
			attSum = num.SatAdd(attSum, ev.Attainment)
			attCount++
			if float64(ev.Attainment) < floor {
				uneducated++
			}
		}
		if inc, ok := snap.Income[cv.ID]; ok {
			incSum = num.SatAdd(incSum, inc)
			incCount++
		}
	}

	if n := len(snap.Citizens); n > 0 {
		agg.MeanHealth = float64(healthSum) / float64(n)
		agg.UneducatedFraction = float64(uneducated) / float64(n)
	}
	if attCount > 0 {
		agg.MeanAttainment = float64(attSum) / float64(attCount)
	}
	if incCount > 0 {
		agg.MeanIncome = incSum / incCount
	}
	agg.TotalIncome = incSum

	agg.CrimeRate = snap.CrimeRate
	agg.UnfedFraction = snap.UnfedFraction

	return agg
}
