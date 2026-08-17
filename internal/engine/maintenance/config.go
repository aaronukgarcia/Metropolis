package maintenance

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// ClassConfig is one class's data-driven maintenance parameters
// (data/maintenance.json's "classes" entry). Every numeric field is a
// PLACEHOLDER pending Aaron's balance pass (balance-number regime) — see
// doc.go and the data file's own $comment.
type ClassConfig struct {
	// EngineerDaysPerYear is the class's base maintenance demand, in
	// engineer-days per simulation-year (rate unit, AC-16). Placeholder.
	EngineerDaysPerYear int64

	// LifetimeYears is the class's lifetime, in simulation-years
	// (lifetime unit, AC-16). Placeholder.
	LifetimeYears int64
}

// Config is engine.maintenance's runtime configuration, derived from
// data/maintenance.json (GR#15). Every magnitude here is data-sourced; the
// values are placeholders pending the balance pass, so rebalancing is a data
// edit, never a code change.
type Config struct {
	// Classes maps a resolvable class key to its rate and lifetime. Never
	// ranged over for output — only looked up by key — so map ordering is
	// irrelevant to determinism (GR#21).
	Classes map[Class]ClassConfig

	// CrewCostPerEngineerDay is the cost of one engineer-day of local crew
	// work, in micro-pounds (1 GBP = 1,000,000 micro-pounds, matching
	// engine.finance's Money). Placeholder.
	CrewCostPerEngineerDay int64

	// ContractorCostPerEngineerDay is the cost of one engineer-day of
	// off-map contractor work for the un-met remainder, in micro-pounds.
	// Placeholder.
	ContractorCostPerEngineerDay int64
}

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. Requires at least
// two classes (so "scaled by class" is a real ordering, AC-2), a positive
// rate and lifetime per class, and non-negative cost figures.
func (c Config) validate(correlationID string) error {
	if len(c.Classes) < 2 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"reason": "at least two classes with distinct rates are required",
		})
	}
	for class, cc := range c.Classes {
		if cc.EngineerDaysPerYear <= 0 {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".engineerDaysPerYear",
				"value": cc.EngineerDaysPerYear,
			})
		}
		if cc.LifetimeYears <= 0 {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".lifetimeYears",
				"value": cc.LifetimeYears,
			})
		}
	}
	if c.CrewCostPerEngineerDay <= 0 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "crewCostPerEngineerDay", "value": c.CrewCostPerEngineerDay,
		})
	}
	if c.ContractorCostPerEngineerDay <= 0 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "contractorCostPerEngineerDay", "value": c.ContractorCostPerEngineerDay,
		})
	}
	return nil
}

// classKnown reports whether class has a data entry (lookup, never ranged).
func (c Config) classKnown(class Class) bool {
	_, ok := c.Classes[class]
	return ok
}

// cloneConfig deep-copies the mutable parts of a Config so the stored config
// can never alias caller-owned memory (SEC-167). A Config is stored by value
// in New, which copies the struct but not the Classes map, so a.cfg.Classes
// would otherwise alias the caller's map and let a post-New mutation of the
// caller's config silently change the running API's class table — bypassing
// validate()'s positive-rate check, letting Register succeed with a negative
// BaseEngineerDaysPerYear, and letting AdvanceMonth accrue a negative-cost job
// that breaks the AC-6/AC-7 conservation invariant with no error. The only
// reference-typed field in the config is Classes; every other field is a value
// (int64), so one map clone is a full deep copy. Mirrors engine.wellbeing's
// cloneConfig (SEC-157) and engine.attract's cloneTermInputs, via the
// sanctioned registry.CloneMap name (SEC-066).
func cloneConfig(cfg Config) Config {
	cfg.Classes = registry.CloneMap(cfg.Classes)
	return cfg
}
