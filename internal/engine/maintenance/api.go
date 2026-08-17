package maintenance

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// instance is the internal, mutable per-structure maintenance state.
// Unexported — the only read surface is [MaintenanceAPI.View]'s exported
// MaintenanceView snapshot, and no consumer can write an instance's fields
// directly (AC-1).
type instance struct {
	id                 StructureID
	class              Class
	placedMonth        int64
	sizePerMille       int64
	monthsSinceAccrual int64 // demand-accrual carry, 0..monthsPerYear-1
}

// job is one backlog repair unit (a class-scaled cost in engineer-days).
type job struct {
	id    JobID
	class Class
	cost  int64
}

// MaintenanceAPI is code.json's "engine.maintenance" inbound contract
// (MaintenanceAPI, "per-instance view keyed to structure identity; backlog
// conservation; spend settles to finance OPEX"). It owns the per-instance
// MaintenanceViews, aging, lifetime, the multi-fix crew backlog, the
// conservation ledger, and the two feed edges — city-wide demand for
// engine.staffing (AC-8) and spend settlement into engine.finance's OPEX
// surface (AC-10).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *MaintenanceAPI is safe for concurrent use (AC-14): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020 family).
type MaintenanceAPI struct {
	mu            sync.RWMutex
	correlationID string

	cfg   Config
	month int64

	instances map[StructureID]*instance

	// jobs is the FIFO backlog (enqueue order). backlog is the per-class
	// engineer-day total; backlogTotal is the maintained running total, so no
	// aggregate ever iterates the jobs slice or the backlog map in map order
	// (GR#21).
	jobs         []job
	nextJobID    JobID
	backlog      map[Class]int64
	backlogTotal int64

	// dailyBudget is the injected crew capacity for the current day (AC-9),
	// wired by engine.staffing via SetDailyBudget.
	dailyBudget int64

	// finance is the OPEX settlement target (AC-10), wired via SetFinance.
	// Nil means spend settlement is skipped — the composition root wires the
	// real engine.finance for the drain to be visible.
	finance *finance.FinanceAPI

	// self is the SEC-020 copy guard, stored exactly once in New before the
	// value is returned to any caller.
	self atomic.Pointer[MaintenanceAPI]
}

// New constructs a MaintenanceAPI from a validated Config. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted rate or lifetime.
func New(cfg Config, correlationID string) (*MaintenanceAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &MaintenanceAPI{
		correlationID: correlationID,
		cfg:           cloneConfig(cfg),
		instances:     make(map[StructureID]*instance),
		backlog:       make(map[Class]int64, len(cfg.Classes)),
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *MaintenanceAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *MaintenanceAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetFinance wires the engine.finance OPEX settlement target (AC-10). A nil
// value un-wires it (spend settlement skipped, never a shadow ledger).
func (a *MaintenanceAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.finance = f
	return nil
}

// Register places a structure into the maintenance system at the current sim
// month, keyed by its structure identity (AC-1). Two same-class structures
// registered at different months hold distinct views with distinct ages. An
// unknown class, a zero id, or a duplicate id returns a registry-sourced
// error and mutates nothing (AC-11).
func (a *MaintenanceAPI) Register(id StructureID, class Class, opts RegisterOptions, correlationID string) error {
	if err := a.checkNotCopied("Register"); err != nil {
		return err
	}
	if id == 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{"reason": "structure id must be non-zero"})
	}
	if opts.SizePerMille < 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"reason":       "size factor must be non-negative (per-mille; 0 means the 1.0x default)",
			"sizePerMille": opts.SizePerMille,
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("Register"); err != nil {
		return err
	}
	if !a.cfg.classKnown(class) {
		return errs.New(ErrUnknownClass, correlationID, map[string]any{"class": string(class)})
	}
	if _, overflow := num.SafeMul(a.cfg.Classes[class].EngineerDaysPerYear, effectiveSizePerMille(opts.SizePerMille)); overflow {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"reason":       "size factor would saturate the class base engineer-days/year rate",
			"sizePerMille": opts.SizePerMille,
			"class":        string(class),
		})
	}
	if _, exists := a.instances[id]; exists {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"reason": "structure already registered", "structureId": uint64(id),
		})
	}
	a.instances[id] = &instance{
		id:           id,
		class:        class,
		placedMonth:  a.month,
		sizePerMille: opts.SizePerMille,
	}
	return nil
}

// AdvanceMonth advances the simulation month by n (aging every instance and
// accruing yearly demand into the backlog). n must be non-negative — a
// negative advance would drive an age negative and is rejected with
// ErrNegativeAge (AC-11). Age advances by month index only, never the wall
// clock (AC-13).
func (a *MaintenanceAPI) AdvanceMonth(n int64, correlationID string) error {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	if n < 0 {
		return errs.New(ErrNegativeAge, correlationID, map[string]any{"months": n})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	// Saturate the month clock and the accrual carry rather than wrapping
	// (SEC-153/GR#16): AdvanceMonth(MaxInt64) must pin the clock, not drive an
	// age negative and enqueue a negative-cost job that breaks conservation.
	a.month = num.SatAdd(a.month, n)

	// Iterate instances in sorted StructureID order so accrual is
	// deterministic regardless of registration/map order (GR#21).
	for _, id := range a.sortedIDsLocked() {
		inst := a.instances[id]
		inst.monthsSinceAccrual = num.SatAdd(inst.monthsSinceAccrual, n)
		years := inst.monthsSinceAccrual / monthsPerYear
		inst.monthsSinceAccrual %= monthsPerYear
		if years == 0 {
			continue
		}
		cc := a.cfg.Classes[inst.class]
		lifetimeMonths, _ := num.SafeMul(cc.LifetimeYears, monthsPerYear)
		base := baseEngineerDaysPerYear(cc.EngineerDaysPerYear, inst.sizePerMille)
		cost := repairDemandPerYear(base, num.SatSub(a.month, inst.placedMonth), lifetimeMonths)
		total, _ := num.SafeMul(cost, years)
		a.enqueueJobLocked(inst.class, total)
	}
	return nil
}

// EnqueueJob appends one class-scaled repair job to the backlog (AC-5's job
// list). It is the public surface the composition root (and tests) use to
// seed demand directly; AdvanceMonth's yearly accrual uses the internal
// enqueueJobLocked. An unknown class or a non-positive cost is rejected with
// a registry-sourced error and mutates nothing (AC-11).
func (a *MaintenanceAPI) EnqueueJob(class Class, cost int64, correlationID string) (JobID, error) {
	if err := a.checkNotCopied("EnqueueJob"); err != nil {
		return 0, err
	}
	if cost <= 0 {
		return 0, errs.New(ErrInvalidInput, correlationID, map[string]any{"reason": "job cost must be positive", "cost": cost})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("EnqueueJob"); err != nil {
		return 0, err
	}
	if !a.cfg.classKnown(class) {
		return 0, errs.New(ErrUnknownClass, correlationID, map[string]any{"class": string(class)})
	}
	id := a.enqueueJobLocked(class, cost)
	return id, nil
}

// enqueueJobLocked appends a job and updates the per-class and total backlog
// (the caller holds a.mu). Total and per-class figures stay exactly equal to
// the sum of live job costs — the conservation invariant's bookkeeping.
func (a *MaintenanceAPI) enqueueJobLocked(class Class, cost int64) JobID {
	a.nextJobID++
	a.jobs = append(a.jobs, job{id: a.nextJobID, class: class, cost: cost})
	a.backlog[class], _ = num.SatAddChecked(a.backlog[class], cost)
	a.backlogTotal, _ = num.SatAddChecked(a.backlogTotal, cost)
	return a.nextJobID
}

// SetDailyBudget injects today's crew capacity (AC-9): the bounded
// engineer-day budget the next RunCrewDay applies. engine.staffing (MOD-073)
// wires this; maintenance never computes supply from citizen/population
// state. A negative budget is rejected with ErrNegativeBudget and mutates
// nothing (AC-11).
func (a *MaintenanceAPI) SetDailyBudget(budget int64, correlationID string) error {
	if err := a.checkNotCopied("SetDailyBudget"); err != nil {
		return err
	}
	if budget < 0 {
		return errs.New(ErrNegativeBudget, correlationID, map[string]any{"budget": budget})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("SetDailyBudget"); err != nil {
		return err
	}
	a.dailyBudget = budget
	return nil
}

// View returns one structure's per-instance MaintenanceView snapshot. An
// unknown structure reference returns ErrUnknownStructure (AC-11).
func (a *MaintenanceAPI) View(id StructureID, correlationID string) (MaintenanceView, error) {
	if err := a.checkNotCopied("View"); err != nil {
		return MaintenanceView{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	inst, ok := a.instances[id]
	if !ok {
		return MaintenanceView{}, errs.New(ErrUnknownStructure, correlationID, map[string]any{"structureId": uint64(id)})
	}
	return a.viewLocked(inst), nil
}

// viewLocked derives a MaintenanceView from an instance (the caller holds
// a.mu). Pure — derives every field from the instance and its class config.
func (a *MaintenanceAPI) viewLocked(inst *instance) MaintenanceView {
	cc := a.cfg.Classes[inst.class]
	lifetimeMonths, _ := num.SafeMul(cc.LifetimeYears, monthsPerYear)
	ageMonths := num.SatSub(a.month, inst.placedMonth)
	base := baseEngineerDaysPerYear(cc.EngineerDaysPerYear, inst.sizePerMille)
	return MaintenanceView{
		StructureID:             inst.id,
		Class:                   inst.class,
		AgeMonths:               ageMonths,
		AgeYears:                float64(ageMonths) / float64(monthsPerYear),
		Efficiency:              efficiency(ageMonths, lifetimeMonths),
		BaseEngineerDaysPerYear: base,
		RepairDemandPerYear:     repairDemandPerYear(base, ageMonths, lifetimeMonths),
		LifetimeYears:           cc.LifetimeYears,
		NeedsRefit:              ageMonths >= lifetimeMonths,
	}
}

// Backlog returns the per-class accumulated (un-fixed) backlog, in
// engineer-days (AC-7). An unknown class returns ErrUnknownClass.
func (a *MaintenanceAPI) Backlog(class Class, correlationID string) (int64, error) {
	if err := a.checkNotCopied("Backlog"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.cfg.classKnown(class) {
		return 0, errs.New(ErrUnknownClass, correlationID, map[string]any{"class": string(class)})
	}
	return a.backlog[class], nil
}

// TotalBacklog returns the city-wide accumulated backlog, in engineer-days
// (AC-7). O(1): the maintained running total, never a sum over map order. A
// struct-copied value is rejected with ErrCopiedValue like every other method
// (SEC-156), never a silent data read off the copied mutex.
func (a *MaintenanceAPI) TotalBacklog(correlationID string) (int64, error) {
	if err := a.checkNotCopied("TotalBacklog"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.backlogTotal, nil
}

// CityDemand returns the city-wide repair demand surfaced for engine.staffing
// (AC-8): the sum of every placed object's current RepairDemandPerYear plus
// the accumulated backlog. Computed by aggregation over the per-instance
// views — never a separately-maintained counter (AC-8's conservation of
// aggregation).
func (a *MaintenanceAPI) CityDemand(correlationID string) (CityDemand, error) {
	if err := a.checkNotCopied("CityDemand"); err != nil {
		return CityDemand{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	var demand int64
	for _, id := range a.sortedIDsLocked() {
		v := a.viewLocked(a.instances[id])
		demand, _ = num.SatAddChecked(demand, v.RepairDemandPerYear)
	}
	return CityDemand{
		RepairDemandPerYear: demand,
		Backlog:             a.backlogTotal,
		Total:               num.SatAdd(demand, a.backlogTotal),
	}, nil
}

// RunCrewDay applies the injected daily budget against the backlog (AC-5/
// AC-6): it resolves jobs in FIFO order until the budget is exhausted or an
// unaffordable job is reached, never over-applying (indivisible integer jobs;
// the total applied never exceeds the budget and the budget never goes
// negative). It then settles the day's maintenance spend — crew cost for the
// applied work plus contractor cost for the un-met remainder — through
// engine.finance's SettleOpex (AC-10).
//
// Ordering is a conservation contract (SEC-162): the resolve pass only PEEKS
// the job list to compute applied/backlogAfter — it commits no mutation. The
// finance settlement runs FIRST, and only when it succeeds does RunCrewDay
// remove the resolved jobs and decrement the backlog. A failed SettleOpex
// (e.g. an insolvent treasury) therefore returns the error with the backlog,
// the job list, and the finance ledger all exactly as they were — the two
// ledgers never diverge (AC-6/AC-7 conservation, AC-10 settle-to-finance).
func (a *MaintenanceAPI) RunCrewDay(correlationID string) (CrewDay, error) {
	if err := a.checkNotCopied("RunCrewDay"); err != nil {
		return CrewDay{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("RunCrewDay"); err != nil {
		return CrewDay{}, err
	}

	budget := a.dailyBudget
	remaining := budget
	var applied int64
	resolved := 0

	// Peek the FIFO resolve order WITHOUT committing: walk the job list,
	// stopping at the first unaffordable job. applied == sum(resolved costs)
	// and remaining == budget - applied >= 0, exactly as a committed pass
	// would compute, but a.jobs/a.backlog/a.backlogTotal are left untouched.
	for _, j := range a.jobs {
		if j.cost > remaining {
			break
		}
		applied = num.SatAdd(applied, j.cost)
		remaining = num.SatSub(remaining, j.cost)
		resolved++
	}
	backlogAfter := num.SatSub(a.backlogTotal, applied)

	// Settle spend FIRST: crew cost for the applied work, contractor cost for
	// the un-met remainder (backlogAfter). Both rates are data placeholders;
	// the economics placement is FEAT-094's — this module only crosses the
	// spend into finance's surface (AC-10). A zero spend settles nothing (no
	// zero-value transaction noise).
	crewSpend, _ := num.SafeMul(applied, a.cfg.CrewCostPerEngineerDay)
	contractSpend, _ := num.SafeMul(backlogAfter, a.cfg.ContractorCostPerEngineerDay)
	spend, _ := num.SatAddChecked(crewSpend, contractSpend)
	if a.finance != nil && spend > 0 {
		if _, err := a.finance.SettleOpex(finance.Money(spend)); err != nil {
			return CrewDay{}, err
		}
	}

	// Commit the resolution only now that the settlement posted: remove the
	// resolved jobs and decrement the backlog by exactly their costs. This is
	// the single, infallible mutation step — no later failure can leave a
	// partial state (SEC-162).
	for i := 0; i < resolved; i++ {
		j := a.jobs[i]
		a.backlog[j.class] = num.SatSub(a.backlog[j.class], j.cost)
		a.backlogTotal = num.SatSub(a.backlogTotal, j.cost)
	}
	a.jobs = a.jobs[resolved:]

	return CrewDay{
		Budget:           budget,
		Applied:          applied,
		JobsResolved:     int64(resolved),
		BudgetRemaining:  num.SatSub(budget, applied),
		BacklogRemaining: backlogAfter,
	}, nil
}

// sortedIDsLocked returns the registered structure ids in ascending order
// (deterministic — never map-iteration order, GR#21). The caller holds a.mu.
func (a *MaintenanceAPI) sortedIDsLocked() []StructureID {
	ids := make([]StructureID, 0, len(a.instances))
	for id := range a.instances {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
