package census

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Collar is the census's blue/white-collar classification of a citizen's
// employment sector (§45).
type Collar uint8

const (
	CollarNone Collar = iota
	CollarBlue Collar = iota
	CollarWhite
)

// collarFor maps a census-owned Sector to its §45 blue/white-collar class:
// blue = production and services-to-buildings (primary/secondary/tertiary),
// white = firm overhead / finance / public administration (public). The
// finer "firm overhead vs finance vs public administration" white-collar
// split is a master-spec concern (engine.citizens' Sector enum is coarse);
// this mapping is the census's own documented classification, never a
// hardcoded population ratio (AC-17).
func collarFor(s Sector) Collar {
	switch s {
	case SectorPrimary, SectorSecondary, SectorTertiary:
		return CollarBlue
	case SectorPublic:
		return CollarWhite
	default:
		return CollarNone
	}
}

// BlueWhiteCollar is the blue/white-collar split (AC-17): counts of the
// population by their employment sector's collar class. It is emergent from
// per-citizen sector data, never a fixed ratio.
type BlueWhiteCollar struct {
	Blue  int64
	White int64
}

// BlueWhiteCollar computes the blue/white-collar split from the snapshot's
// per-citizen sector data (AC-17). Moving a cohort of citizens between
// blue- and white-collar sectors moves the split by exactly that cohort.
func (c *CensusAPI) BlueWhiteCollar(snap *Snapshot) BlueWhiteCollar {
	if err := c.checkNotCopied("BlueWhiteCollar"); err != nil {
		return BlueWhiteCollar{}
	}
	var out BlueWhiteCollar
	for _, cv := range snap.Citizens {
		switch collarFor(cv.Sector) {
		case CollarBlue:
			out.Blue++
		case CollarWhite:
			out.White++
		}
	}
	return out
}

// KPI aggregate IDs — the keys the Source resolution (AC-20) and the
// drill-in UI bind against.
const (
	KPIKeyGDP            = "gdp"
	KPIKeyHappiness      = "happiness"
	KPIKeyLandValue      = "land-value"
	KPIKeyHomeless       = "homeless"
	KPIKeyInHospital     = "in-hospital"
	KPIKeyOutOfWork      = "out-of-work"
	KPIKeyUnfilledJobs   = "unfilled-jobs"
	KPIKeyJobSkillDemand = "job-skill-demand"
)

// homelessIDs returns the citizen ids the homelessness KPI aggregates over:
// citizens with no home cell (Home == 0). Sorted by id (GR#21).
func (c *CensusAPI) homelessIDs(snap *Snapshot) []uint64 {
	var ids []uint64
	for _, cv := range snap.Citizens {
		if cv.Home == 0 {
			ids = append(ids, cv.ID)
		}
	}
	return ids
}

// outOfWorkIDs returns the citizen ids the out-of-work KPI aggregates over:
// citizens whose employment state is unemployed. Sorted by id (GR#21).
func (c *CensusAPI) outOfWorkIDs(snap *Snapshot) []uint64 {
	var ids []uint64
	for _, cv := range snap.Citizens {
		if cv.Employment == EmploymentUnemployed {
			ids = append(ids, cv.ID)
		}
	}
	return ids
}

// GDP returns the GDP KPI: the finance ledger's GDP-relevant flows for the
// snapshot tick (AC-19, ASM-648). Aggregated over FinanceSource, never a
// separately-maintained counter.
func (c *CensusAPI) GDP(snap *Snapshot) int64 {
	if err := c.checkNotCopied("GDP"); err != nil {
		return 0
	}
	return snap.GDPFlows
}

// Happiness returns the happiness KPI: the §18 wellbeing headline composite
// (AC-19), aggregated over WellbeingSource.
func (c *CensusAPI) Happiness(snap *Snapshot) float64 {
	if err := c.checkNotCopied("Happiness"); err != nil {
		return 0
	}
	return snap.Happiness
}

// LandValue returns the land-value KPI: FinanceSource's city land value
// (AC-19).
func (c *CensusAPI) LandValue(snap *Snapshot) int64 {
	if err := c.checkNotCopied("LandValue"); err != nil {
		return 0
	}
	return snap.LandValue
}

// Homeless returns the homeless KPI: the count of citizens with no home
// cell, aggregated over CitizensSource's home state (AC-19).
func (c *CensusAPI) Homeless(snap *Snapshot) int64 {
	if err := c.checkNotCopied("Homeless"); err != nil {
		return 0
	}
	return int64(len(c.homelessIDs(snap)))
}

// InHospital returns the in-hospital KPI: ServicesSource's hospital waiting
// list (AC-19).
func (c *CensusAPI) InHospital(snap *Snapshot) int64 {
	if err := c.checkNotCopied("InHospital"); err != nil {
		return 0
	}
	return snap.HospitalWaiting
}

// OutOfWork returns the out-of-work KPI: the count of unemployed citizens,
// aggregated over CitizensSource's employment state (AC-19).
func (c *CensusAPI) OutOfWork(snap *Snapshot) int64 {
	if err := c.checkNotCopied("OutOfWork"); err != nil {
		return 0
	}
	return int64(len(c.outOfWorkIDs(snap)))
}

// UnfilledJobs returns the unfilled-jobs KPI: ServicesSource's unfilled-job
// count (AC-19).
func (c *CensusAPI) UnfilledJobs(snap *Snapshot) int64 {
	if err := c.checkNotCopied("UnfilledJobs"); err != nil {
		return 0
	}
	return snap.UnfilledJobs
}

// JobSkillDemand returns the job→skill demand KPI: ServicesSource's demand
// figure (AC-19, the staffing edge — ES-3).
func (c *CensusAPI) JobSkillDemand(snap *Snapshot) int64 {
	if err := c.checkNotCopied("JobSkillDemand"); err != nil {
		return 0
	}
	return snap.JobSkillDemand
}

// SourceResolution is the drill-in result (AC-20): the underlying entities
// (for population-derived KPIs) or ledger line (for aggregate KPIs) that
// compose an aggregate figure, so a UI surface can bind Enter-to-source per
// UI-SPEC §4.
type SourceResolution struct {
	AggregateID string
	EntityIDs   []uint64 // the citizens composing a population-derived KPI
	LineValue   int64    // the ledger/aggregate line for a sourced KPI
}

// Source resolves an aggregate's drill-target (AC-20): for a population-
// derived KPI it returns the entity set whose count equals the aggregate;
// for an aggregate KPI it returns the ledger line that equals the figure.
// An unknown aggregate key returns ErrUnknownKey (AC-21), never a zero
// resolution.
func (c *CensusAPI) Source(snap *Snapshot, aggregateID string) (SourceResolution, error) {
	if err := c.checkNotCopied("Source"); err != nil {
		return SourceResolution{}, err
	}
	switch aggregateID {
	case KPIKeyHomeless:
		ids := c.homelessIDs(snap)
		return SourceResolution{AggregateID: aggregateID, EntityIDs: ids, LineValue: int64(len(ids))}, nil
	case KPIKeyOutOfWork:
		ids := c.outOfWorkIDs(snap)
		return SourceResolution{AggregateID: aggregateID, EntityIDs: ids, LineValue: int64(len(ids))}, nil
	case KPIKeyGDP:
		return SourceResolution{AggregateID: aggregateID, LineValue: snap.GDPFlows}, nil
	case KPIKeyLandValue:
		return SourceResolution{AggregateID: aggregateID, LineValue: snap.LandValue}, nil
	case KPIKeyInHospital:
		return SourceResolution{AggregateID: aggregateID, LineValue: snap.HospitalWaiting}, nil
	case KPIKeyUnfilledJobs:
		return SourceResolution{AggregateID: aggregateID, LineValue: snap.UnfilledJobs}, nil
	case KPIKeyJobSkillDemand:
		return SourceResolution{AggregateID: aggregateID, LineValue: snap.JobSkillDemand}, nil
	case KPIKeyHappiness:
		// The snapshot boundary finiteness-checks the happiness float, but
		// Source accepts a *Snapshot directly and must never coerce a
		// non-finite value with a bare int64() (which wraps NaN to MinInt64 —
		// SEC-129). Route through num.SafeInt64, which rejects non-finite and
		// out-of-range values with a registry-sourced error.
		v, err := num.SafeInt64(snap.Happiness)
		if err != nil {
			return SourceResolution{}, err
		}
		return SourceResolution{AggregateID: aggregateID, LineValue: v}, nil
	default:
		return SourceResolution{}, errs.New(ErrUnknownKey, c.correlationID, map[string]any{"key": aggregateID})
	}
}

// EducationCrimeLinkage is the census's data-derived education→crime report
// (AC-14): the population's aggregate education attainment joined against
// the crime source's rate figure, plus the observed reward/penalise-
// education policy coefficient (AC-15). The census reports the relationship
// — it never invents the numbers; the direction (lower attainment ↔ higher
// crime) emerges from the owning modules' live data.
type EducationCrimeLinkage struct {
	Population         int64
	MeanAttainment     float64
	CrimeRate          float64
	UneducatedFraction float64
	PolicyCoefficient  float64
}

// EducationCrimeLinkage computes the education→crime linkage from the
// snapshot: mean attainment (EducationSource, joined per citizen) against
// the crime rate (CrimeSource), plus the uneducated fraction (AC-14).
func (c *CensusAPI) EducationCrimeLinkage(snap *Snapshot) EducationCrimeLinkage {
	if err := c.checkNotCopied("EducationCrimeLinkage"); err != nil {
		return EducationCrimeLinkage{}
	}
	var sum, count, uneducated int64
	floor := c.cfg.Thresholds.UneducatedAttainmentFloor.Value
	for _, cv := range snap.Citizens {
		ev, ok := snap.Education[cv.ID]
		if !ok {
			continue
		}
		sum = num.SatAdd(sum, ev.Attainment)
		count++
		if float64(ev.Attainment) < floor {
			uneducated++
		}
	}
	out := EducationCrimeLinkage{
		Population:        int64(len(snap.Citizens)),
		CrimeRate:         snap.CrimeRate,
		PolicyCoefficient: snap.EducationPolicyCoefficient,
	}
	if count > 0 {
		out.MeanAttainment = float64(sum) / float64(count)
	}
	if n := len(snap.Citizens); n > 0 {
		out.UneducatedFraction = float64(uneducated) / float64(n)
	}
	return out
}
