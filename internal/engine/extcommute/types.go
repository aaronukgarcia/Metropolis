package extcommute

// Public types and the three dependency seams this module consumes through
// (GR#20, contract-first). The seams are wired by the composition root to
// the real engine.citizens / engine.traffic / engine.finance modules; this
// package never imports those engine packages directly, so no unregistered
// cross-module edge is needed (GR#25).

// Pool is one off-map job pool (§21/A6) as this package exposes it: the
// named pool's id, display name, monthly off-map wage, and its finite,
// era-scaled capacity curve loaded from data/external_world.json (GR#15).
// A Pool is an immutable value snapshot; the live state lives in
// ExtCommuteAPI.
type Pool struct {
	// ID is the stable lowercase pool slug ("london", "ashford", "dover"),
	// globally unique within data/external_world.json.
	ID string

	// Name is the display name ("London", ...).
	Name string

	// WageMicropounds is the monthly gross off-map wage per job held, in
	// int64 micro-pounds (1 GBP = 1,000,000 micropounds). It is the
	// income-tax-eligible base for an off-map-employed citizen and carries
	// no business rates or corporate share (AC-12/A6c).
	WageMicropounds int64

	capacityByEra []capacityPoint
	transport     []transportRequirement
}

// capacityPoint is one (era, capacity) point of a pool's finite,
// era-scaled capacity curve.
type capacityPoint struct {
	era      int
	capacity int
}

// transportRequirement is one transport channel gating a pool, with the
// milestone tier it becomes available from (A6's era-scaled rail gating).
type transportRequirement struct {
	channel           string
	availableFromTier int
}

// Capacity returns the pool's finite, era-scaled job capacity at era (AC-2,
// A6's "bounded and slowly growing" mechanism): the capacity of the latest
// curve point whose era is <= era, clamped to the curve's ends. It is total
// over the whole milestone ladder and non-decreasing in era (AC-5).
func (p Pool) Capacity(era int) int {
	if len(p.capacityByEra) == 0 {
		return 0
	}
	first := p.capacityByEra[0]
	if era <= first.era {
		return first.capacity
	}
	last := p.capacityByEra[len(p.capacityByEra)-1]
	if era >= last.era {
		return last.capacity
	}
	// The curve is era-sorted (foundation/data enforces strictly increasing
	// eras); find the latest point with point.era <= era.
	for i := len(p.capacityByEra) - 1; i >= 0; i-- {
		if p.capacityByEra[i].era <= era {
			return p.capacityByEra[i].capacity
		}
	}
	return first.capacity
}

// EmploymentState is the coarse employment bucket the CitizensSeam exposes to
// this module (GR#20 contract-first — the seam, never a direct engine.citizens
// import). It mirrors the slice of engine.citizens' EmploymentState enum the
// off-map assign/release transitions and the AC-6/AC-7 dormitory-arithmetic
// identity need (ICD engine.citizens-offmap.md §4, §12): an assigned citizen
// flips to EmploymentOffMap, a released citizen flips to EmploymentUnemployed.
// The numeric values are kept equal to citizens' (EmploymentUnemployed=3,
// EmploymentOffMap=5) so the composition-root adapter is the identity function;
// the remaining values exist so a test seam can model the full working-age
// population the identity sums over (LocallyEmployed, Unemployed,
// NotInLaborForce). The off-map pool id and since-month are NOT carried here —
// that stays in ExtCommuteAPI.assignments (single source of truth, ICD §12
// Open Decision 1).
type EmploymentState uint8

const (
	EmploymentNone       EmploymentState = 0 // child / never worked (NotInLaborForce once adult)
	EmploymentStudent    EmploymentState = 1 // NotInLaborForce
	EmploymentEmployed   EmploymentState = 2 // LocallyEmployed
	EmploymentUnemployed EmploymentState = 3 // Unemployed
	EmploymentRetired    EmploymentState = 4 // NotInLaborForce
	EmploymentOffMap     EmploymentState = 5 // OffMapEmployed(pool)
)

// CitizensSeam is the slice of engine.citizens (registered edge) this module
// consumes. Wired via SetCitizensSeam by the composition root; a nil seam
// makes any operation that needs it fail closed with ErrDependencyNotWired.
type CitizensSeam interface {
	// TotalPopulation returns the current resident population count (AC-9's
	// unchanged-population check).
	TotalPopulation() int

	// CitizenExists reports whether a resident citizen id exists.
	CitizenExists(id uint64) bool

	// ApplyLifeEventEmployment routes a coarse employment-state write through
	// the engine.citizens edge — the LifeEventEmployment slice of citizens'
	// ApplyLifeEventCommand (ICD engine.citizens-offmap.md §4: no new
	// CitizensAPI method). Assign calls it with EmploymentOffMap, Release with
	// EmploymentUnemployed; the sector is always SectorNone (this module never
	// knows or restores a prior local job/sector), so it is not a parameter.
	ApplyLifeEventEmployment(citizenID uint64, state EmploymentState) error
}

// TrafficSeam is the slice of engine.traffic (registered edge) this module
// consumes: per-channel congestion in [0,1] (0 = free-flow, 1 = gridlock).
// This module applies congestion to its own data-loaded base transport
// capacity (data/extcommute.json) to form the second cap (AC-8); it does not
// compute engine.traffic's congestion itself.
type TrafficSeam interface {
	// Congestion returns the per-channel congestion fraction in [0,1].
	Congestion(channel string) (float64, error)
}

// FinanceSeam is the slice of engine.finance (registered edge) this module
// consumes: the two ledger facts §21/A6/F2 require. The module records an
// off-map wage (income-tax-eligible, no business rates or corporate share —
// AC-12/A6c) and an in-commuting wage-leakage entry (F6 — AC-10). It never
// records business rates or corporate share for off-map employment; those two
// methods exist on the seam so a recording stub can PROVE the module never
// emits them.
type FinanceSeam interface {
	// RecordOffMapWage records an out-commuter's monthly off-map wage income
	// attributable to citizenID, which engine.finance treats as
	// income-tax-eligible only.
	RecordOffMapWage(citizenID uint64, poolID string, wageMicropounds int64) error

	// RemoveOffMapWage is the compensating inverse of RecordOffMapWage:
	// Assign calls it to roll the just-posted off-map wage back when the
	// citizens seam's employment flip fails, so a rejected assignment leaves
	// no phantom wage income (AC-4). It is the same compensating-removal
	// shape engine.accelerator uses for its FDI anchor draw
	// (RemoveAnchorProspect).
	RemoveOffMapWage(citizenID uint64, poolID string, wageMicropounds int64) error

	// RecordBusinessRates records a business-rates amount attributable to a
	// citizen. This module never calls it for off-map employment (AC-12).
	RecordBusinessRates(citizenID uint64, amountMicropounds int64) error

	// RecordCorpShare records a corporate-share amount attributable to a
	// citizen. This module never calls it for off-map employment (AC-12).
	RecordCorpShare(citizenID uint64, amountMicropounds int64) error

	// RecordWageLeakage records an in-commuting wage-leakage ledger entry —
	// the wage an off-map in-commuter is paid locally but takes home, visible
	// in F6 (AC-10). Distinct from an ordinary local wage payment.
	RecordWageLeakage(poolID string, amountMicropounds int64) error
}

// AssignCommand is an out-commuting assignment request: place a resident
// citizen into an off-map job pool, subject to the two independent caps
// (pool capacity AC-3, transport capacity AC-8) and the no-double-assignment
// rule (AC-11).
type AssignCommand struct {
	CitizenID uint64
	PoolID    string
	Era       int   // current milestone tier (validates against the ladder)
	Month     int64 // bookkeeping "since when" (no tenure cap, AC-14)
}

// ReleaseCommand removes a citizen's off-map assignment (job loss, death,
// emigration, or a local job found — the release transitions AC-7 names).
type ReleaseCommand struct {
	CitizenID uint64
	Month     int64 // bookkeeping; unused beyond record-keeping
}

// InCommuteCommand is an aggregate in-commuting request: fill Vacancies
// local labour-shortage jobs with off-map in-commuters from a pool. The
// filling workers are never residents (AC-9); their wage leaks out (AC-10).
type InCommuteCommand struct {
	PoolID    string
	Vacancies int
}

// InCommuteResult is the aggregate in-commuting outcome (AC-9/AC-10).
type InCommuteResult struct {
	PoolID                 string
	FilledVacancies        int
	WageLeakageMicropounds int64
}

// Assignment is a citizen's current off-map assignment as this module tracks
// it internally — the "which pool, since when" the citizens EmploymentState
// enum does not carry (see the AC-6/AC-9/AC-11 escalation).
type Assignment struct {
	CitizenID  uint64
	PoolID     string
	SinceMonth int64
}
