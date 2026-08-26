package education

import (
	"fmt"
	"math"
)

// Config is engine.education's runtime configuration — the balance numbers
// that §27 describes only by direction and mechanism, never by magnitude
// (ASM-…, see the data file's $comment). Every field is data-sourced from
// data/education.json (GR#15): the values here are placeholders pending the
// M2 balance pass, so rebalancing is a data edit, never a code change.
type Config struct {
	// EntryAgeMonths is the entry-age gate for each Stage, in months of
	// age (indexed by Stage). It is the ONLY source of the documented age
	// gates (primary's 5-year = 60 months, secondary's 11-year = 132
	// months) plus the placeholder gates for the fork/university/adult/U3A
	// boundaries the spec leaves unquantified (AC-3).
	EntryAgeMonths [numStages]int64

	// BaselineQuality is the realised funding-quality at which schooling
	// counts as "good" rather than "poor" (AC-6/AC-7's above/below-baseline
	// split). Placeholder: 0.5.
	BaselineQuality float64

	// AttainmentScale maps a quality deviation above/below BaselineQuality
	// onto the signed quality-weighted attainment score:
	// score = round((quality - BaselineQuality) * AttainmentScale).
	// Positive = above-baseline (good), negative = below (poor).
	AttainmentScale float64

	// ResearchPointsPerGraduate is the research-points output per graduating
	// university student (AC-8). Placeholder.
	ResearchPointsPerGraduate float64

	// HallsCapacity is the university halls-of-residence capacity, a DISTINCT
	// capacity input from teaching capacity (AC-8): enrolment above it is
	// rejected/queued even when teaching capacity has headroom.
	HallsCapacity float64

	// DropoutRate is the documented per-transition attrition probability,
	// drawn deterministically from hash(worldSeed, id, month, "education")
	// (AC-14). Placeholder: 0.0 (no dropout) until the M2 balance pass.
	DropoutRate float64
}

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. The entry-age
// gates must be non-negative and non-decreasing along the pipeline order,
// the quality/scale/rate figures finite and in-domain, and the
// research-points/halls figures non-negative. The dir parameter is supplied
// to all ErrEducationDataInvalid errors so the template's {dir} and {cause}
// tokens render real values, not literals (BUG-357).
func (c Config) validate(correlationID, dir string) error {
	// The automatic pipeline must be age-monotone: nursery ≤ primary ≤
	// secondary ≤ fork ≤ university ≤ adult ≤ u3a.
	var prev int64 = 0
	for _, s := range stageOrder {
		v := c.EntryAgeMonths[s]
		if v < prev {
			return educationDataInvalid(correlationID, dir, fmt.Sprintf("entryAgeMonths[%s]", s.String()), "entry ages must be non-decreasing along the pipeline", v)
		}
		prev = v
	}
	if !numFinite(c.BaselineQuality) || c.BaselineQuality < 0 || c.BaselineQuality > 1 {
		return educationDataInvalid(correlationID, dir, "baselineQuality", "must be finite and in [0,1]", c.BaselineQuality)
	}
	if !numFinite(c.AttainmentScale) || c.AttainmentScale < 0 {
		return educationDataInvalid(correlationID, dir, "attainmentScale", "must be finite and non-negative", c.AttainmentScale)
	}
	if !numFinite(c.ResearchPointsPerGraduate) || c.ResearchPointsPerGraduate < 0 {
		return educationDataInvalid(correlationID, dir, "researchPointsPerGraduate", "must be finite and non-negative", c.ResearchPointsPerGraduate)
	}
	if !numFinite(c.HallsCapacity) || c.HallsCapacity < 0 {
		return educationDataInvalid(correlationID, dir, "hallsCapacity", "must be finite and non-negative", c.HallsCapacity)
	}
	if !numFinite(c.DropoutRate) || c.DropoutRate < 0 || c.DropoutRate > 1 {
		return educationDataInvalid(correlationID, dir, "dropoutRate", "must be finite and in [0,1]", c.DropoutRate)
	}
	return nil
}

// numFinite reports whether f is a finite IEEE-754 value (GR#16: NaN/±Inf
// must never cross the configuration boundary into stored state).
func numFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
