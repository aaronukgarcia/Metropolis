package maintenance

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// Upper bounds on the data-authored magnitudes (BUG-263 / SEC-117 shape). Every
// numeric field below is data-sourced and was previously positive-UNBOUNDED, so
// a near-MaxInt64 authoring value loaded silently and then SATURATED in the
// downstream integer arithmetic (num.SafeMul/SatAdd clamp at int64's edge)
// instead of being rejected — masking a data-authoring bug. validate() now
// rejects anything above these caps loudly at load. Each cap is DERIVED from the
// representable range of the specific downstream product, never a hardcoded
// balance figure (GR#15) — rebalancing within the caps stays a pure data edit.
const (
	// maxEngineerDaysPerYear caps a class's base rate. repairDemandPerYear
	// doubles the base at max age (base + base×age/lifetime → 2×base at
	// age==lifetime), so the rate must leave ×2 headroom to stay
	// representable. (The separate rate×sizePerMille size-scaling product is
	// guarded at the Register boundary, SEC-163.)
	maxEngineerDaysPerYear = int64(math.MaxInt64) / 2

	// maxLifetimeYears caps a class's lifetime. api.go converts years→months
	// via num.SafeMul(LifetimeYears, monthsPerYear); the cap keeps that
	// conversion product below int64 saturation.
	maxLifetimeYears = int64(math.MaxInt64) / monthsPerYear

	// maxCostPerEngineerDay caps a per-engineer-day cost figure. The field is
	// ALREADY in micro-pounds (engine.finance's Money scale) and is never
	// multiplied by MicropoundsPerPound in this module; the real downstream
	// product is `applied × cost` in AdvanceMonth's crew/contract spend
	// (num.SafeMul, runtime-bounded). This cap is therefore a finite
	// sanity ceiling, not the exact saturation bound of that runtime product:
	// dividing MaxInt64 by MicropoundsPerPound keeps any authored cost at or
	// below one-million-pounds-per-engineer-day magnitude, leaving ~10^6
	// headroom in the int64 product so a plausibly-applied engineer-day count
	// can never saturate the SafeMul. The divisor picks a defensible order of
	// magnitude tied to the money scale, not a hardcoded balance figure (GR#15).
	maxCostPerEngineerDay = int64(math.MaxInt64) / int64(det.MicropoundsPerPound)
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
// rate and lifetime per class within their upper bounds, and positive cost
// figures within the money-scale ceiling. The upper bounds (BUG-263) close the
// SEC-117 load-time-saturation shape: a near-MaxInt64 authoring value used to
// load silently and then saturate downstream; it is now rejected at load.
//
// The per-class checks iterate the Classes map in SORTED key order (GR#21): the
// map's own iteration order is randomised, so ranging it directly would make the
// reported first-error field depend on Go's map seed. Sorting the keys makes the
// first offending class deterministic.
func (c Config) validate(correlationID string) error {
	if len(c.Classes) < 2 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"reason": "at least two classes with distinct rates are required",
		})
	}
	classes := make([]Class, 0, len(c.Classes))
	for class := range c.Classes {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	for _, class := range classes {
		cc := c.Classes[class]
		if cc.EngineerDaysPerYear <= 0 {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".engineerDaysPerYear",
				"value": cc.EngineerDaysPerYear,
			})
		}
		if cc.EngineerDaysPerYear > maxEngineerDaysPerYear {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".engineerDaysPerYear",
				"value": cc.EngineerDaysPerYear, "max": maxEngineerDaysPerYear,
				"reason": "exceeds the ×2-headroom cap — would saturate the age-scaled repair demand (SEC-117 shape)",
			})
		}
		if cc.LifetimeYears <= 0 {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".lifetimeYears",
				"value": cc.LifetimeYears,
			})
		}
		if cc.LifetimeYears > maxLifetimeYears {
			return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
				"field": "classes." + string(class) + ".lifetimeYears",
				"value": cc.LifetimeYears, "max": maxLifetimeYears,
				"reason": "exceeds the years→months cap — would saturate the lifetime-in-months conversion (SEC-117 shape)",
			})
		}
	}
	if c.CrewCostPerEngineerDay <= 0 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "crewCostPerEngineerDay", "value": c.CrewCostPerEngineerDay,
		})
	}
	if c.CrewCostPerEngineerDay > maxCostPerEngineerDay {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "crewCostPerEngineerDay", "value": c.CrewCostPerEngineerDay,
			"max": maxCostPerEngineerDay, "reason": "exceeds the money-scale cap (SEC-117 shape)",
		})
	}
	if c.ContractorCostPerEngineerDay <= 0 {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "contractorCostPerEngineerDay", "value": c.ContractorCostPerEngineerDay,
		})
	}
	if c.ContractorCostPerEngineerDay > maxCostPerEngineerDay {
		return errs.New(ErrMaintenanceDataInvalid, correlationID, map[string]any{
			"field": "contractorCostPerEngineerDay", "value": c.ContractorCostPerEngineerDay,
			"max": maxCostPerEngineerDay, "reason": "exceeds the money-scale cap (SEC-117 shape)",
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
