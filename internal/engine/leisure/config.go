package leisure

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Config is engine.leisure's runtime configuration — the balance numbers
// §42/§5.1 describe only by direction and mechanism, never by magnitude.
// Every field is data-sourced from data/leisure.json (GR#15): the values
// here are placeholders pending the M2 balance pass, so rebalancing is a
// data edit, never a code change.
type Config struct {
	// HoursPerWeek is the fixed weekly budget (§42: 168 hours).
	HoursPerWeek float64

	// Work/Education/Sleep/Chores are the per-life-stage baseline weekly
	// allocations (hours), indexed by LifeStage. Work and education are the
	// two halves of the "productive obligation" — a citizen's week is spent
	// at a firm (work) or at a school (education) depending on life stage,
	// never both. These are DATA baselines: this module does not call
	// engine.firms/engine.education because code.json registers no such
	// outbound edge (see doc.go / ASM).
	Work      [numLifeStages]float64
	Education [numLifeStages]float64
	Sleep     [numLifeStages]float64
	Chores    [numLifeStages]float64

	// AccessFreeMinutes: access time at or below this is "free" (no
	// allocation penalty). AccessBudgetMinutes: access time at or above this
	// fully blocks a venue category. Between the two the penalty is linear.
	AccessFreeMinutes   float64
	AccessBudgetMinutes float64

	// OvertimeWageRate is the wage (abstract units) earned per overtime hour
	// — the "overtime generates wages" half of the trade-off (§42/§18). The
	// "overtime harms wellbeing" half is the leisure-time squeeze it causes
	// (overtime reduces discretionary hours). Placeholder.
	OvertimeWageRate float64

	// NoveltyDecayBase and NoveltyDecayPerNovelty parameterise the per-visit
	// freshness decay: decay = base + (noveltyAxis/100)*perNovelty, so a
	// novelty-seeking citizen's freshness falls faster (AC-4). Placeholders.
	NoveltyDecayBase       float64
	NoveltyDecayPerNovelty float64

	// FreshnessRecovery is the freshness value a refurbish/open restores
	// matching citizens to (AC-5). Placeholder.
	FreshnessRecovery float64

	// EventCrowd is the base crowd-transport demand (person-trips) per event
	// kind (AC-6). Placeholders.
	EventCrowd [numEventKinds]int64

	// MatchThreshold is the minimum taste weight (0-100) for a citizen's
	// taste to "match" a venue's category (AC-5's refurbish reset).
	MatchThreshold float64

	// DefaultTaste is the citywide aggregate population taste distribution,
	// the default for UnmetTasteDemand until SetPopulationTaste overrides it
	// (the pushed input for the missing census-enumeration edge — see doc.go).
	DefaultTaste TasteDistribution
}

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. Hour figures must
// be finite and non-negative, the access-time thresholds ordered
// (free < budget), decay/recovery figures in-domain, and event-crowd counts
// non-negative.
func (c Config) validate(correlationID string) error {
	if !num.IsFinite(c.HoursPerWeek) || c.HoursPerWeek <= 0 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "hoursPerWeek", "value": c.HoursPerWeek,
			"cause": fmt.Sprintf("field %q has invalid value %v", "hoursPerWeek", c.HoursPerWeek),
		})
	}
	for s := LifeStage(0); s < numLifeStages; s++ {
		for _, f := range []float64{c.Work[s], c.Education[s], c.Sleep[s], c.Chores[s]} {
			if !num.IsFinite(f) || f < 0 {
				return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
					"field": "lifeStages." + s.String(), "value": f,
					"cause": fmt.Sprintf("field %q has invalid value %v", "lifeStages."+s.String(), f),
				})
			}
		}
	}
	if !num.IsFinite(c.AccessFreeMinutes) || c.AccessFreeMinutes < 0 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "accessFreeMinutes", "value": c.AccessFreeMinutes,
			"cause": fmt.Sprintf("field %q has invalid value %v", "accessFreeMinutes", c.AccessFreeMinutes),
		})
	}
	if !num.IsFinite(c.AccessBudgetMinutes) || c.AccessBudgetMinutes <= c.AccessFreeMinutes {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "accessBudgetMinutes", "value": c.AccessBudgetMinutes,
			"cause": fmt.Sprintf("field %q has invalid value %v", "accessBudgetMinutes", c.AccessBudgetMinutes),
		})
	}
	if !num.IsFinite(c.OvertimeWageRate) || c.OvertimeWageRate < 0 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "overtimeWageRate", "value": c.OvertimeWageRate,
			"cause": fmt.Sprintf("field %q has invalid value %v", "overtimeWageRate", c.OvertimeWageRate),
		})
	}
	if !num.IsFinite(c.NoveltyDecayBase) || c.NoveltyDecayBase < 0 ||
		!num.IsFinite(c.NoveltyDecayPerNovelty) || c.NoveltyDecayPerNovelty < 0 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "noveltyDecay", "value": c.NoveltyDecayBase,
			"cause": fmt.Sprintf("field %q has invalid value %v", "noveltyDecay", c.NoveltyDecayBase),
		})
	}
	if !num.IsFinite(c.FreshnessRecovery) || c.FreshnessRecovery < 0 || c.FreshnessRecovery > 1 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "freshnessRecovery", "value": c.FreshnessRecovery,
			"cause": fmt.Sprintf("field %q has invalid value %v", "freshnessRecovery", c.FreshnessRecovery),
		})
	}
	for k := EventKind(0); k < numEventKinds; k++ {
		if c.EventCrowd[k] < 0 {
			return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
				"field": "eventCrowd." + k.String(), "value": c.EventCrowd[k],
				"cause": fmt.Sprintf("field %q has invalid value %v", "eventCrowd."+k.String(), c.EventCrowd[k]),
			})
		}
	}
	if !num.IsFinite(c.MatchThreshold) || c.MatchThreshold < 0 || c.MatchThreshold > 100 {
		return errs.New(ErrLeisureDataInvalid, correlationID, map[string]any{
			"field": "matchThreshold", "value": c.MatchThreshold,
			"cause": fmt.Sprintf("field %q has invalid value %v", "matchThreshold", c.MatchThreshold),
		})
	}
	return nil
}
