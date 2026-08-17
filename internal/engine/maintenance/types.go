package maintenance

// Class is the resolvable class identity a MaintenanceView scales by. It is
// a string key into data/maintenance.json's "classes" table — today
// engine.build's eight ZoneType strings ("dwelling", "shop", ...), extensible
// to the future building/road catalogues without a code change (ASM-888: the
// full taxonomy is un-authored; this module keys off any resolvable class
// identity and does not author the ladder).
type Class string

// StructureID identifies one placed structure. It is the value the
// composition root derives from engine.build's per-cell Structure reference
// (a BuildOrderID); maintenance keys its per-instance MaintenanceView by it,
// never by class (AC-1 — one view per placed object, not per zone type).
type StructureID uint64

// JobID identifies one backlog job (a class-scaled repair unit: a pothole
// costs a fraction of a school's repair).
type JobID uint64

// RegisterOptions carries the optional per-instance inputs for Register.
type RegisterOptions struct {
	// SizePerMille is the size/scale factor, fixed-point per-mille: 1000
	// means 1.0× (the default when zero), 2500 means 2.5×. It scales the
	// class's engineer-days/year figure — the "scaled by size/class" half of
	// the design. The size dimension is part of the un-authored class ladder
	// (ES-2); this is the minimal per-instance hook for it. Placeholder.
	// A negative factor is an authoring bug and is rejected by Register
	// (SEC-155) with ErrInvalidInput, never silently coerced to the default.
	// A factor so large that rate × SizePerMille overflows int64 is likewise
	// rejected (SEC-163) — never silently saturated into a wrong base rate.
	SizePerMille int64
}

// MaintenanceView is the read-only per-instance maintenance state of one
// placed structure (AC-1/AC-3/AC-4). Every field is derived under the API's
// lock — a consumer can never write back through it.
type MaintenanceView struct {
	StructureID StructureID
	Class       Class

	// AgeMonths is the instance's age in simulation months; AgeYears is the
	// same age in years (AgeMonths/12). Age advances by simulation month
	// index only, never the wall clock.
	AgeMonths int64
	AgeYears  float64

	// Efficiency is a non-increasing function of age: 1.0 at age zero,
	// declining to 0 at the class lifetime and holding at 0 beyond it.
	// The linear shape is a documented placeholder; monotonicity is the
	// tested invariant, not the slope.
	Efficiency float64

	// BaseEngineerDaysPerYear is the class rate × the size factor — the
	// instance's demand when new. Constant for the instance's life.
	BaseEngineerDaysPerYear int64

	// RepairDemandPerYear is the age-scaled engineer-days/year demand:
	// monotonic non-decreasing with age, so an older instance needs more
	// repair than a newer one (AC-3). It is the figure the yearly backlog
	// accrual draws on.
	RepairDemandPerYear int64

	// LifetimeYears is the class's lifetime in simulation-years (data).
	LifetimeYears int64

	// NeedsRefit reports the distinct end-of-life state: the instance's age
	// has reached or passed its lifetime and it is flagged for refit/rebuild
	// rather than silently kept working at full efficiency (AC-4, §12).
	NeedsRefit bool
}

// CrewDay is the observable result of one maintenance tick's crew work
// (AC-5/AC-6): the bounded budget applied against the backlog, and the exact
// conservation ledger of that application.
type CrewDay struct {
	// Budget is the day's injected engineer-day budget.
	Budget int64
	// Applied is the total engineer-days applied across resolved jobs —
	// always <= Budget, never negative, and exactly equal to the sum of the
	// resolved jobs' costs.
	Applied int64
	// JobsResolved is the number of backlog jobs resolved this day.
	JobsResolved int64
	// BudgetRemaining is Budget - Applied (never negative — un-applied budget
	// neither invents nor destroys work).
	BudgetRemaining int64
	// BacklogRemaining is the total backlog after the day, always equal to
	// (backlog before) - Applied.
	BacklogRemaining int64
}

// CityDemand is the city-wide repair demand surfaced for engine.staffing
// (AC-8): the aggregate of every placed object's current engineer-days/year
// plus the accumulated backlog. It is computed by aggregation over the
// per-instance views, never a separately-maintained counter.
type CityDemand struct {
	// RepairDemandPerYear is the sum of every instance's RepairDemandPerYear.
	RepairDemandPerYear int64
	// Backlog is the accumulated (un-fixed) backlog, in engineer-days.
	Backlog int64
	// Total is RepairDemandPerYear + Backlog.
	Total int64
}
