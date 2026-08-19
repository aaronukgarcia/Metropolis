package extcommute

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Milestone-ladder bounds (§4, mirroring foundation/data's unexported
// milestoneTierMin/milestoneTierMax). These are structural game-framework
// constants, not player-felt balance numbers (GR#15): an era/tier argument
// outside [eraMin, eraMax] is rejected rather than silently clamped.
const (
	eraMin = 1
	eraMax = 13
)

// ExtCommuteAPI is code.json's "engine.extcommute" inbound contract (GUID
// bc5b137e-5692-410b-a6cf-80da601ee414, ExtCommuteAPI, "pools from
// external_world.json; era scaling"): the §21/A6 off-map external-commuting
// model — the three finite, era-scaled job pools (London/Ashford/Dover)
// loaded from data/external_world.json, out-commuting assignment/release
// subject to the two independent caps (pool capacity AC-3, transport capacity
// AC-8), the no-double-assignment rule (AC-11), aggregate in-commuting with
// no resident gain (AC-9) and visible wage leakage (AC-10), and the fiscal
// thinness shape of off-map employment (income-tax-only, zero rates/corp
// share, AC-12).
//
// The zero value is not usable; construct via [Load] or [LoadDefault]. A
// *ExtCommuteAPI is safe for concurrent use (AC-18): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020 family, mirroring engine.crime / engine.prison).
type ExtCommuteAPI struct {
	correlationID string
	seed          uint64
	cfg           config // immutable after construction (safe to read without mu)

	mu sync.RWMutex

	pools     []Pool         // sorted by ID (deterministic iteration, GR#21)
	poolIndex map[string]int // pool ID -> index into pools

	assignments map[uint64]assignment // citizen ID -> current off-map assignment (single source of truth for FilledSlots)

	citizens CitizensSeam
	traffic  TrafficSeam
	finance  FinanceSeam

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in newAPI, before the value is returned to any caller.
	self atomic.Pointer[ExtCommuteAPI]
}

// assignment is one citizen's off-map job as this module tracks it — the
// "which pool, since when" the citizens EmploymentState enum does not carry.
type assignment struct {
	poolID     string
	sinceMonth int64
}

// Load reads and validates data/extcommute.json and data/external_world.json
// from dir, cross-checks them, and returns a ready *ExtCommuteAPI (GR#15,
// GR#7). Every failure is a registry-sourced *errs.E — never a panic, never
// a silent zero-capacity or unlimited-capacity default (AC-15).
func Load(dir, correlationID string) (*ExtCommuteAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := loadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	world, err := data.LoadExternalWorld(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrExternalWorldDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	if err := crossCheck(cfg, world, correlationID); err != nil {
		return nil, err
	}
	return newAPI(0, cfg, world, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*ExtCommuteAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// crossCheck performs the cross-file completeness checks GR#3 requires beyond
// each file's own schema validation (AC-15):
//
//   - every pool's capacity curve must cover era 1 (the world's starting
//     era) — a pool whose curve starts later would be silently unreachable
//     at boot;
//   - every transport channel a pool names must have a capacity entry in
//     data/extcommute.json — a pool whose reaching leg has no defined
//     capacity must fail closed, never default to zero or unlimited.
func crossCheck(cfg config, world data.ExternalWorld, correlationID string) error {
	for i := range world.Profiles {
		p := &world.Profiles[i]
		id := p.ID
		if len(p.CapacityByEra) == 0 || p.CapacityByEra[0].Era != eraMin {
			return errs.New(ErrExternalWorldDataInvalid, correlationID, map[string]any{
				"pool": id,
				"rule": "capacityByEra must cover era 1 (the world's starting era)",
			})
		}
		for _, tr := range p.TransportRequirement {
			if _, ok := cfg.TransportCapacity[tr.Channel]; !ok {
				return errs.New(ErrExternalWorldDataInvalid, correlationID, map[string]any{
					"pool":    id,
					"channel": tr.Channel,
					"rule":    "transport channel has no capacity entry in data/extcommute.json",
				})
			}
		}
	}
	return nil
}

// newAPI assembles an ExtCommuteAPI from a validated config and external
// world. The pools slice is sorted by ID so every iteration is deterministic
// regardless of data/external_world.json's profile order (GR#21).
func newAPI(seed uint64, cfg config, world data.ExternalWorld, correlationID string) (*ExtCommuteAPI, error) {
	a := &ExtCommuteAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		pools:         make([]Pool, 0, len(world.Profiles)),
		poolIndex:     make(map[string]int, len(world.Profiles)),
		assignments:   make(map[uint64]assignment),
	}
	for _, p := range world.Profiles {
		pool := Pool{
			ID:              p.ID,
			Name:            p.Name,
			WageMicropounds: p.WageMicropounds,
			capacityByEra:   make([]capacityPoint, 0, len(p.CapacityByEra)),
			transport:       make([]transportRequirement, 0, len(p.TransportRequirement)),
		}
		for _, ce := range p.CapacityByEra {
			pool.capacityByEra = append(pool.capacityByEra, capacityPoint{era: ce.Era, capacity: ce.Capacity})
		}
		for _, tr := range p.TransportRequirement {
			pool.transport = append(pool.transport, transportRequirement{channel: tr.Channel, availableFromTier: tr.AvailableFromTier})
		}
		a.pools = append(a.pools, pool)
	}
	sort.Slice(a.pools, func(i, j int) bool { return a.pools[i].ID < a.pools[j].ID })
	for i, p := range a.pools {
		a.poolIndex[p.ID] = i
	}
	// Armed exactly once, before a is returned to any caller (SEC-020).
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *ExtCommuteAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *ExtCommuteAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetSeed sets the world seed used by SelectPool's deterministic
// counter-based selection draw (AC-16/GR#21). The composition root wires the
// world seed after Load.
func (a *ExtCommuteAPI) SetSeed(seed uint64) error {
	if err := a.checkNotCopied("SetSeed"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seed = seed
	return nil
}

// SetCitizensSeam wires the engine.citizens seam (registered edge). A nil
// seam is allowed here but makes Assign fail closed (GR#17/GR#20).
func (a *ExtCommuteAPI) SetCitizensSeam(s CitizensSeam) error {
	if err := a.checkNotCopied("SetCitizensSeam"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.citizens = s
	return nil
}

// SetTrafficSeam wires the engine.traffic seam (registered edge).
func (a *ExtCommuteAPI) SetTrafficSeam(s TrafficSeam) error {
	if err := a.checkNotCopied("SetTrafficSeam"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.traffic = s
	return nil
}

// SetFinanceSeam wires the engine.finance seam (registered edge).
func (a *ExtCommuteAPI) SetFinanceSeam(s FinanceSeam) error {
	if err := a.checkNotCopied("SetFinanceSeam"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finance = s
	return nil
}

// PoolIDs returns the pool ids, ascending (deterministic — never a map
// iteration, GR#21).
func (a *ExtCommuteAPI) PoolIDs() []string {
	if err := a.checkNotCopied("PoolIDs"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]string, 0, len(a.pools))
	for _, p := range a.pools {
		ids = append(ids, p.ID)
	}
	return ids
}

// Pool returns the immutable snapshot of the named pool (AC-1/AC-2), or
// ErrUnknownPool.
func (a *ExtCommuteAPI) Pool(id string) (Pool, error) {
	if err := a.checkNotCopied("Pool"); err != nil {
		return Pool{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	idx, ok := a.poolIndex[id]
	if !ok {
		return Pool{}, errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": id})
	}
	return a.pools[idx], nil
}

// Capacity returns the named pool's finite, era-scaled capacity at era
// (AC-2/AC-5). era must be within the milestone ladder [1,13] (ErrInvalidEra
// otherwise); within the ladder the capacity clamps to the curve's ends and
// is non-decreasing in era.
func (a *ExtCommuteAPI) Capacity(id string, era int) (int, error) {
	if err := a.checkNotCopied("Capacity"); err != nil {
		return 0, err
	}
	if era < eraMin || era > eraMax {
		return 0, errs.New(ErrInvalidEra, a.correlationID, map[string]any{"era": era})
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	idx, ok := a.poolIndex[id]
	if !ok {
		return 0, errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": id})
	}
	return a.pools[idx].Capacity(era), nil
}

// FilledSlots returns the number of citizens currently holding an off-map
// job in the named pool (AC-3's filled term). Always 0 <= FilledSlots <=
// Capacity(pool, era): Assign refuses the slot that would exceed capacity, so
// the invariant holds after every transition.
func (a *ExtCommuteAPI) FilledSlots(id string) (int, error) {
	if err := a.checkNotCopied("FilledSlots"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if _, ok := a.poolIndex[id]; !ok {
		return 0, errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": id})
	}
	return a.filledLocked(id), nil
}

// Assignment returns a citizen's current off-map assignment, if any
// (AC-11's verification surface, and the "which pool, since when" the
// citizens enum does not carry).
func (a *ExtCommuteAPI) Assignment(citizenID uint64) (Assignment, bool, error) {
	if err := a.checkNotCopied("Assignment"); err != nil {
		return Assignment{}, false, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	as, ok := a.assignments[citizenID]
	if !ok {
		return Assignment{}, false, nil
	}
	return Assignment{CitizenID: citizenID, PoolID: as.poolID, SinceMonth: as.sinceMonth}, true, nil
}

// Assign places a resident citizen into an off-map job pool subject to the
// two independent caps and the no-double-assignment rule. On any rejection
// nothing is mutated (AC-4): the citizen's pre-command state is left exactly
// as it was — never granted a phantom off-map job.
func (a *ExtCommuteAPI) Assign(cmd AssignCommand) error {
	if err := a.checkNotCopied("Assign"); err != nil {
		return err
	}
	if cmd.Era < eraMin || cmd.Era > eraMax {
		return errs.New(ErrInvalidEra, a.correlationID, map[string]any{"era": cmd.Era})
	}

	// Snapshot read-only state and the seams under RLock, then release — seam
	// calls run outside this module's write lock (they may be composition-root
	// calls with their own locking, mirroring engine.prison's Admit).
	a.mu.RLock()
	idx, ok := a.poolIndex[cmd.PoolID]
	var pool Pool
	if ok {
		pool = a.pools[idx]
	}
	_, already := a.assignments[cmd.CitizenID]
	filled := a.filledLocked(cmd.PoolID)
	citizens := a.citizens
	traffic := a.traffic
	finance := a.finance
	a.mu.RUnlock()

	if !ok {
		return errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": cmd.PoolID})
	}
	if already {
		return errs.New(ErrAlreadyOffMap, a.correlationID, map[string]any{"citizen": cmd.CitizenID, "pool": cmd.PoolID})
	}
	if filled >= pool.Capacity(cmd.Era) {
		return errs.New(ErrPoolFull, a.correlationID, map[string]any{
			"pool": cmd.PoolID, "filled": filled, "capacity": pool.Capacity(cmd.Era), "era": cmd.Era,
		})
	}
	if citizens == nil {
		return errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "citizens"})
	}
	if !citizens.CitizenExists(cmd.CitizenID) {
		return errs.New(ErrUnknownCitizen, a.correlationID, map[string]any{"citizen": cmd.CitizenID})
	}
	if traffic == nil {
		return errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "traffic"})
	}
	available, err := a.transportAvailable(pool, cmd.Era, traffic)
	if err != nil {
		return err
	}
	if !available {
		return errs.New(ErrTransportCapacity, a.correlationID, map[string]any{"pool": cmd.PoolID, "era": cmd.Era})
	}
	if finance == nil {
		return errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "finance"})
	}

	// Re-check the state-race-sensitive conditions under the write lock
	// (concurrent Assigns can pass the snapshot above), then commit. The
	// capacity invariant (AC-3) is re-checked here so two racing Assigns
	// cannot both squeeze into the final slot.
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok2 := a.poolIndex[cmd.PoolID]; !ok2 {
		return errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": cmd.PoolID})
	}
	if _, already2 := a.assignments[cmd.CitizenID]; already2 {
		return errs.New(ErrAlreadyOffMap, a.correlationID, map[string]any{"citizen": cmd.CitizenID, "pool": cmd.PoolID})
	}
	pool2 := a.pools[a.poolIndex[cmd.PoolID]]
	if a.filledLocked(cmd.PoolID) >= pool2.Capacity(cmd.Era) {
		return errs.New(ErrPoolFull, a.correlationID, map[string]any{
			"pool": cmd.PoolID, "filled": a.filledLocked(cmd.PoolID), "capacity": pool2.Capacity(cmd.Era), "era": cmd.Era,
		})
	}

	// Post the off-map wage first (income-tax-eligible, no rates/corp share,
	// AC-12). The finance post is the compensatable external write, so it runs
	// BEFORE the citizens flip below: if the citizens flip fails, the wage is
	// compensated back via RemoveOffMapWage, so no store is left changed.
	// engine.finance is one-way (never calls back into this module), so this
	// seam call under the write lock cannot deadlock.
	if err := finance.RecordOffMapWage(cmd.CitizenID, cmd.PoolID, pool2.WageMicropounds); err != nil {
		return errs.Wrap(ErrDependencyNotWired, a.correlationID, err, map[string]any{
			"dependency": "finance", "cause": err.Error(),
		})
	}

	// Flip the citizen's coarse employment state through the citizens seam
	// (ICD engine.citizens-offmap.md §4): EmploymentOffMap is the one bucket
	// citizens could not previously express, and without this write AC-6's
	// identity double-counts (citizens still reads Employed/Unemployed) or
	// silently uncounts the citizen. This is the final external write before
	// the (infallible) assignments-map commit below, so on failure the finance
	// post above is compensated back — on ANY failure no store (finance,
	// citizens, assignments) is left changed (AC-4). engine.citizens is
	// one-way (never calls back into this module), so this call under the
	// write lock cannot deadlock (mirrors the finance call above).
	if err := citizens.ApplyLifeEventEmployment(cmd.CitizenID, EmploymentOffMap); err != nil {
		citizensErr := errs.Wrap(ErrDependencyNotWired, a.correlationID, err, map[string]any{
			"dependency": "citizens", "cause": err.Error(),
		})
		if rmErr := finance.RemoveOffMapWage(cmd.CitizenID, cmd.PoolID, pool2.WageMicropounds); rmErr != nil {
			// The compensating rollback itself failed: surface both failures
			// (GR#1 — a silent rollback failure would leave the phantom wage
			// behind and read as a clean rejection).
			return errors.Join(citizensErr, errs.Wrap(ErrDependencyNotWired, a.correlationID, rmErr, map[string]any{
				"dependency": "finance", "cause": rmErr.Error(),
			}))
		}
		return citizensErr
	}

	a.assignments[cmd.CitizenID] = assignment{poolID: cmd.PoolID, sinceMonth: cmd.Month}
	return nil
}

// Release removes a citizen's off-map assignment (a job-loss/death/emigration
// or local-job-found transition, AC-7). Releasing a citizen not currently
// off-map-assigned is rejected with ErrNotOffMapAssigned — never a silent
// no-op.
func (a *ExtCommuteAPI) Release(cmd ReleaseCommand) error {
	if err := a.checkNotCopied("Release"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.assignments[cmd.CitizenID]; !ok {
		return errs.New(ErrNotOffMapAssigned, a.correlationID, map[string]any{"citizen": cmd.CitizenID})
	}
	if a.citizens == nil {
		return errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "citizens"})
	}
	// Flip the citizen's coarse state back to unemployed through the citizens
	// seam (ICD engine.citizens-offmap.md §4: Release -> EmploymentUnemployed,
	// SectorNone — this module does not know or restore a prior local job). The
	// seam write precedes the map delete, and the delete cannot fail, so a
	// citizens-side failure leaves the assignment intact (consistent — still
	// OffMap on both sides). engine.citizens is one-way, so this call under the
	// write lock cannot deadlock.
	if err := a.citizens.ApplyLifeEventEmployment(cmd.CitizenID, EmploymentUnemployed); err != nil {
		return errs.Wrap(ErrDependencyNotWired, a.correlationID, err, map[string]any{
			"dependency": "citizens", "cause": err.Error(),
		})
	}
	delete(a.assignments, cmd.CitizenID)
	return nil
}

// InCommute fills a local labour shortage with off-map in-commuters from a
// pool (US-5/§21). The filling workers are NEVER residents — this method does
// not touch the citizens seam's population or create any citizen record
// (AC-9); it records the wage that leaks out of the city economy as a
// distinct ledger entry (AC-10).
func (a *ExtCommuteAPI) InCommute(cmd InCommuteCommand) (InCommuteResult, error) {
	if err := a.checkNotCopied("InCommute"); err != nil {
		return InCommuteResult{}, err
	}
	if cmd.Vacancies < 0 {
		return InCommuteResult{}, errs.New(ErrInvalidInput, a.correlationID, map[string]any{
			"field": "vacancies", "value": cmd.Vacancies,
		})
	}

	a.mu.RLock()
	idx, ok := a.poolIndex[cmd.PoolID]
	var pool Pool
	if ok {
		pool = a.pools[idx]
	}
	finance := a.finance
	a.mu.RUnlock()

	if !ok {
		return InCommuteResult{}, errs.New(ErrUnknownPool, a.correlationID, map[string]any{"pool": cmd.PoolID})
	}
	if finance == nil {
		return InCommuteResult{}, errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "finance"})
	}

	// Saturating multiply (GR#16): a huge vacancy count times a large wage
	// must saturate at math.MaxInt64, never wrap negative and invent a
	// negative leakage figure.
	leak, _ := num.SafeMul(int64(cmd.Vacancies), pool.WageMicropounds)
	if err := finance.RecordWageLeakage(cmd.PoolID, leak); err != nil {
		return InCommuteResult{}, errs.Wrap(ErrDependencyNotWired, a.correlationID, err, map[string]any{
			"dependency": "finance", "cause": err.Error(),
		})
	}
	return InCommuteResult{
		PoolID:                 cmd.PoolID,
		FilledVacancies:        cmd.Vacancies,
		WageLeakageMicropounds: leak,
	}, nil
}

// SelectPool deterministically selects an eligible pool for the given era and
// month: a pool with a free slot (AC-3) and an available reaching transport
// leg (AC-8). Among eligible pools it breaks ties with a counter-based draw
// hash(worldSeed, 0, month, "extcommute.select") — never a map iteration,
// never a wall-clock read (AC-16, GR#21). Returns ErrNoEligiblePool if none
// is eligible.
func (a *ExtCommuteAPI) SelectPool(era int, month int64) (string, error) {
	if err := a.checkNotCopied("SelectPool"); err != nil {
		return "", err
	}
	if era < eraMin || era > eraMax {
		return "", errs.New(ErrInvalidEra, a.correlationID, map[string]any{"era": era})
	}

	a.mu.RLock()
	traffic := a.traffic
	pools := make([]Pool, len(a.pools))
	copy(pools, a.pools)
	filled := make([]int, len(a.pools))
	for i, p := range a.pools {
		filled[i] = a.filledLocked(p.ID)
	}
	seed := a.seed
	a.mu.RUnlock()

	if traffic == nil {
		return "", errs.New(ErrDependencyNotWired, a.correlationID, map[string]any{"dependency": "traffic"})
	}

	eligible := make([]string, 0, len(pools))
	for i, pool := range pools {
		if filled[i] >= pool.Capacity(era) {
			continue // first cap: pool full
		}
		available, err := a.transportAvailable(pool, era, traffic)
		if err != nil {
			return "", err
		}
		if !available {
			continue // second cap: no reaching leg with room
		}
		eligible = append(eligible, pool.ID)
	}
	if len(eligible) == 0 {
		return "", errs.New(ErrNoEligiblePool, a.correlationID, map[string]any{"era": era})
	}
	if len(eligible) == 1 {
		return eligible[0], nil
	}

	stream := det.NewStream(seed, 0, month, "extcommute.select")
	return eligible[stream.IntN(int64(len(eligible)))], nil
}

// transportAvailable reports whether pool has a reaching transport leg with
// available capacity at era (AC-8's second cap): a leg whose
// availableFromTier <= era and whose data-loaded base capacity, after
// engine.traffic's congestion, still has room (>= 1 commuter). A pool with no
// available leg is NOT an error — it is simply not eligible; the caller
// decides whether to reject (Assign) or skip (SelectPool). A genuine failure
// (an unwired seam, a traffic seam error, or an invalid congestion figure)
// is returned as a registry-sourced error.
func (a *ExtCommuteAPI) transportAvailable(pool Pool, era int, traffic TrafficSeam) (bool, error) {
	for _, tr := range pool.transport {
		if tr.availableFromTier > era {
			continue
		}
		base := a.cfg.TransportCapacity[tr.channel]
		cong, err := traffic.Congestion(tr.channel)
		if err != nil {
			return false, errs.Wrap(ErrDependencyNotWired, a.correlationID, err, map[string]any{
				"dependency": "traffic", "channel": tr.channel, "cause": err.Error(),
			})
		}
		if !num.IsFinite(cong) || cong < 0 || cong > 1 {
			return false, errs.New(ErrInvalidInput, a.correlationID, map[string]any{
				"field": "congestion", "channel": tr.channel, "value": cong,
			})
		}
		if int64(float64(base)*(1-cong)) >= 1 {
			return true, nil
		}
	}
	return false, nil
}

// filledLocked counts the off-map assignments to poolID. Callers hold mu
// (read or write). The assignments map is the single source of truth for the
// filled-slot count (GR#3) — never a separately-maintained counter that could
// drift.
func (a *ExtCommuteAPI) filledLocked(poolID string) int {
	n := 0
	for _, as := range a.assignments {
		if as.poolID == poolID {
			n++
		}
	}
	return n
}
