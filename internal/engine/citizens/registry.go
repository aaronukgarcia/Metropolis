package citizens

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// CitizensAPI is code.json's "engine.citizens" inbound contract (AC-1b):
// the read/query surface over hot, warm and cold citizen state, plus a
// command-only mutation path. It is the ONLY way a consumer reaches
// citizen state — never a direct exported field-set on Citizen/ColdShard.
//
// The cold store (256 columnar shards) is the single source of truth for
// every citizen's persistent record; the hot map is a small elevation
// cache holding a rich AoS record for the citizens currently HOT/WARM
// (viewport + followed + the rotating sample). A citizen is always in the
// cold store (for sampling and as the persistent record) and, when
// elevated, additionally in the hot map. The cold monthly pass skips
// elevated citizens so they are never double-advanced.
type CitizensAPI struct {
	seed    uint64
	month   int64
	dayTick int

	// workers is the POOL-SIM worker count used by the cold pass. It
	// affects only wall-clock cost, never results (AC-17).
	workers int

	cold [numColdShards]*ColdShard
	hot  map[uint64]*Citizen

	households      map[uint64]*Household
	nextHouseholdID uint64

	// fertilityCfg is the loaded data/fertility.json balance config
	// (FEAT-160), read once at construction (GR#15: every magnitude lives
	// in data, never a Go literal).
	fertilityCfg FertilityConfig
	// nextFertilityChildID is the monotonic counter fertility-born child
	// ids are minted from (fertilityChildIDBase + this), disjoint from the
	// composition root's own sequential migrant/seed id counter — see
	// fertility.go's fertilityChildIDBase doc comment.
	nextFertilityChildID uint64

	// curMonthBirths/curMonthDeaths accumulate the current (in-progress)
	// calendar month's fertility births and mortality deaths across its 30
	// day-ticks; lastMonthBirths/lastMonthDeaths hold the totals for the
	// most recently COMPLETED month, snapshotted at the month boundary
	// (mirrors monthParams's own per-month recompute). VitalEvents exposes
	// the completed-month totals — the conservation accounting surface a
	// future composition-root wiring feeds into invariant.PeopleInvariant's
	// TrackedDelta exactly the way migration admits already are (see
	// registry.go's AdvanceDayTick and compose.go's peopleDelta).
	curMonthBirths  int
	curMonthDeaths  int
	lastMonthBirths int
	lastMonthDeaths int

	// monthParams is the sample-derived cold-pass parameter set, computed
	// once at the start of each month and applied across its 30 day-ticks.
	monthParams ColdPassParams

	mu sync.RWMutex

	// self is the SEC-020 copyguard (atomic.Pointer, mirroring
	// engine.world's World.self / engine.mining's DepositMap.self). It is
	// stored exactly once, at the end of NewCitizensAPI, before the value
	// is returned to any caller.
	self atomic.Pointer[CitizensAPI]
}

// NewCitizensAPI constructs an empty citizen registry for a fixed world
// seed. correlationID is attached to every error this (and the returned
// API's methods) construct (GR#1).
func NewCitizensAPI(seed uint64, correlationID string) (*CitizensAPI, error) {
	fertilityCfg, err := LoadDefaultFertilityConfig(correlationID)
	if err != nil {
		return nil, err
	}
	c := &CitizensAPI{
		seed:            seed,
		workers:         1,
		hot:             make(map[uint64]*Citizen),
		households:      make(map[uint64]*Household),
		nextHouseholdID: 1, // 0 is the "no household" sentinel
		fertilityCfg:    fertilityCfg,
		monthParams:     ColdPassParams{MortalityMultiplier: 1.0},
	}
	for i := range c.cold {
		c.cold[i] = newColdShard(0) // epoch 0 = world genesis
	}
	c.self.Store(c)
	return c, nil
}

// checkNotCopied rejects a method call on a struct copy of the
// *CitizensAPI NewCitizensAPI returned (SEC-020 family). mu is a
// sync.RWMutex VALUE while hot/households (maps) and cold (an array of
// pointers) are reference types a copy ALIASES — an unrejected copy is a
// second, independent lock over the same referents. Lock-free: a single
// atomic.Pointer.Load, safe to run before mu is ever touched.
func (c *CitizensAPI) checkNotCopied(correlationID string, method string) error {
	if c.self.Load() != c {
		return errs.New(ErrAPICopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// SeedColdRecords bulk-loads cold citizen records (the harness.synth path)
// into their id-hash shards. It is a command-path mutation, not an
// exported field-set on ColdShard.
func (c *CitizensAPI) SeedColdRecords(records []ColdRecord, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "SeedColdRecords"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range records {
		// Reject out-of-contract seed records BEFORE they reach the column
		// narrowing in append (AC-13/GR#16: the cold store is the single
		// source of truth — never silently narrow a wrapped value).
		if err := ValidateColdRecord(r, correlationID); err != nil {
			return err
		}
		shard := det.ShardForEntity(r.ID)
		if err := validateShardIndex(shard, numColdShards, correlationID); err != nil {
			return err
		}
		c.cold[shard].append(r)
	}
	return nil
}

// CitizenAt returns the citizen's current record — the rich hot record if
// the citizen is elevated, otherwise a widened view of their cold record.
func (c *CitizensAPI) CitizenAt(id uint64, correlationID string) (Citizen, bool) {
	if err := c.checkNotCopied(correlationID, "CitizenAt"); err != nil {
		return Citizen{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cit, ok := c.hot[id]; ok {
		return cloneCitizen(*cit), true
	}
	r, ok := c.coldRecord(id)
	if !ok {
		return Citizen{}, false
	}
	return coldRecordToHot(r, c.month), true
}

// FidelityOf returns the citizen's HOT/WARM/COLD tier (AC-4).
func (c *CitizensAPI) FidelityOf(id uint64, correlationID string) Fidelity {
	if err := c.checkNotCopied(correlationID, "FidelityOf"); err != nil {
		return FidelityCold
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cit, ok := c.hot[id]; ok {
		return cit.Fidelity
	}
	if _, ok := c.coldRecord(id); ok {
		return FidelityCold
	}
	return FidelityCold
}

// HouseholdOf returns the household a citizen belongs to. The lookup is
// done against the locked state directly (never via CitizenAt, which would
// re-acquire c.mu.RLock and deadlock against a pending writer).
func (c *CitizensAPI) HouseholdOf(id uint64, correlationID string) (Household, bool) {
	if err := c.checkNotCopied(correlationID, "HouseholdOf"); err != nil {
		return Household{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var householdID uint64
	if cit, ok := c.hot[id]; ok {
		householdID = cit.Household
	} else if r, ok := c.coldRecord(id); ok {
		householdID = uint64(r.Household)
	} else {
		return Household{}, false
	}
	h, ok := c.households[householdID]
	if !ok {
		return Household{}, false
	}
	return cloneHousehold(*h), true
}

// Household returns a household by id.
func (c *CitizensAPI) Household(id uint64, correlationID string) (Household, bool) {
	if err := c.checkNotCopied(correlationID, "Household"); err != nil {
		return Household{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	h, ok := c.households[id]
	if !ok {
		return Household{}, false
	}
	return cloneHousehold(*h), true
}

// HouseholdIDs returns every currently-registered household id (ASM-247,
// FEAT-1972079927 Q1/Q2), ascending-sorted (GR#21 — the underlying store is
// a map, so callers must never range over it directly; this is the single
// sorted-enumeration seam). Used by the composition root to build the
// household-id set engine.households' HousingAffordability/DemandByType
// query surface needs — CitizensAPI exposes per-id queries only, never a
// map iterator, so this is the one place a caller can discover "every
// household that exists right now".
func (c *CitizensAPI) HouseholdIDs(correlationID string) []uint64 {
	if err := c.checkNotCopied(correlationID, "HouseholdIDs"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]uint64, 0, len(c.households))
	for id := range c.households {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TotalPopulation returns the number of citizens in the cold store (the
// single source of truth for every citizen, elevated or not).
func (c *CitizensAPI) TotalPopulation(correlationID string) int {
	if err := c.checkNotCopied(correlationID, "TotalPopulation"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, s := range c.cold {
		n += s.count()
	}
	return n
}

// BuildSample builds the A7 stratified rotating sample from the full cold
// population (the single source of truth), independent of the viewport —
// this is what makes cold-pass parameter estimates camera-invariant.
func (c *CitizensAPI) BuildSample(correlationID string) *StratifiedSample {
	if err := c.checkNotCopied(correlationID, "BuildSample"); err != nil {
		return &StratifiedSample{counts: map[Stratum]int{}}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	records := c.allColdRecordsLocked()
	return BuildStratifiedSample(records, c.month, c.seed, 1)
}

// ColdParams derives the cold-pass parameters from the sample (AC-8/AC-9).
func (c *CitizensAPI) ColdParams(correlationID string) ColdPassParams {
	if err := c.checkNotCopied(correlationID, "ColdParams"); err != nil {
		return ColdPassParams{}
	}
	return DeriveColdPassParams(c.BuildSample(correlationID))
}

// ApplyFidelityCommand promotes/demotes a citizen across the fidelity
// tiers (AC-4). COLD→HOT/WARM reconstructs the rich hot record from the
// cold store; HOT/WARM→COLD drops the elevation cache (the cold record
// remains the persistent source of truth).
func (c *CitizensAPI) ApplyFidelityCommand(cmd FidelityCommand) error {
	if err := c.checkNotCopied(cmd.CorrelationID, "ApplyFidelityCommand"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	id := cmd.CitizenID
	_, isHot := c.hot[id]
	_, isCold := c.coldRecord(id)

	switch cmd.Target {
	case FidelityHot:
		if isHot {
			c.hot[id].Fidelity = FidelityHot
			return nil
		}
		if !isCold {
			return nil // unknown citizen: no-op, not a corruption
		}
		r, _ := c.coldRecord(id)
		cit := coldRecordToHot(r, c.month)
		cit.Fidelity = FidelityHot
		c.hot[id] = &cit
	case FidelityWarm:
		if isHot {
			c.hot[id].Fidelity = FidelityWarm
			return nil
		}
		if !isCold {
			return nil
		}
		r, _ := c.coldRecord(id)
		cit := coldRecordToHot(r, c.month)
		cit.Fidelity = FidelityWarm
		c.hot[id] = &cit
	case FidelityCold:
		if isHot {
			delete(c.hot, id)
		}
		// A cold citizen is already cold; nothing further to do.
	}
	return nil
}

// ApplyLifeEventCommand applies a life-event mutation (AC-1b's command
// surface). It is the only mutation path for citizen/household state.
func (c *CitizensAPI) ApplyLifeEventCommand(cmd LifeEventCommand) error {
	if err := c.checkNotCopied(cmd.CorrelationID, "ApplyLifeEventCommand"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	switch cmd.Kind {
	case LifeEventBirth:
		// Validate BEFORE any mutation (AC-13): an invalid record is
		// rejected with a registry-sourced error and nothing is persisted.
		if err := ValidateCitizen(cmd.Citizen, c.householdExistsLocked, cmd.CorrelationID); err != nil {
			return err
		}
		// FEAT-169 destructive-review REJECT, defense-in-depth: a
		// LifeEventBirth whose id already exists (cold OR hot) must never
		// be appended as a silent second row. engine.attract's migrant ids
		// and this package's own fertility-child ids partition the same
		// high-bit id space by CONVENTION (fertilityChildIDBase's doc
		// comment), not by a shared allocator, so this is the last-resort
		// catch if that convention is ever violated: TotalPopulation's
		// row-count-based conservation view cannot see an aliased id (the
		// row count stays right; only per-id lookups silently start
		// returning the wrong citizen from then on).
		if _, ok := c.hot[cmd.Citizen.ID]; ok {
			return errs.New(ErrDuplicateCitizenID, cmd.CorrelationID, map[string]any{"id": cmd.Citizen.ID, "fidelity": "hot"})
		}
		if _, ok := c.coldRecord(cmd.Citizen.ID); ok {
			return errs.New(ErrDuplicateCitizenID, cmd.CorrelationID, map[string]any{"id": cmd.Citizen.ID, "fidelity": "cold"})
		}
		r := hotToColdRecord(cmd.Citizen, districtOf(cmd.Citizen.Home))
		c.cold[det.ShardForEntity(cmd.Citizen.ID)].append(r)
		if cmd.Citizen.Fidelity != FidelityCold {
			cp := cmd.Citizen
			cp.Month = c.month
			c.hot[cmd.Citizen.ID] = &cp
		}
	case LifeEventPartner:
		// Round-3 fix (P1 data-integrity, post-F1): a citizen re-partnering
		// after a prior pairing dissolved (e.g. a widowed survivor, F1's
		// scenario) still carries a stale, non-zero Household reference to
		// their OLD household -- F1 deliberately leaves it intact on death
		// so the survivor keeps living there. FormHousehold below mints a
		// BRAND NEW household and setHouseholdLocked/setColdHouseholdLocked
		// overwrite each citizen's Household field to the new id, but never
		// touched the OLD household's Members list -- so the old household
		// kept listing the citizen as a member forever (a leaked household,
		// and the citizen double-counted: once via the stale Members entry,
		// once via their own Household field pointing elsewhere). A citizen
		// must belong to exactly ONE household at a time, so BOTH incoming
		// partners are detached from any prior household FIRST.
		//
		// Orphan rule (documented per the fix's requirement): detachment
		// only prunes the DEPARTING citizen from their old household's
		// Members (mirroring removeHouseholdMemberLocked's F1 prune-then-
		// maybe-delete pattern) -- it does NOT carry their children along
		// into the new pairing. If the old household still holds other
		// members (e.g. the survivor's children from the dissolved
		// marriage), it persists exactly as F1 already established a
		// household persists "as long as ANY member remains" -- it simply
		// now holds only the children (a childless-adult orphan household),
		// and is deleted only once fully empty. This is the coherent
		// extension of the existing F1 invariant, not a new rule: no
		// household is ever left listing a member who no longer belongs.
		c.detachFromHouseholdLocked(cmd.CitizenID)
		c.detachFromHouseholdLocked(cmd.PartnerID)

		h := FormHousehold(c.nextHouseholdID, cmd.CitizenID, cmd.PartnerID, 2)
		c.nextHouseholdID++
		c.households[h.ID] = &h
		// Update BOTH the elevation cache (hot map, if either partner is
		// elevated) and the cold store (the single source of truth).
		c.setHouseholdLocked(cmd.CitizenID, h.ID, cmd.PartnerID)
		c.setHouseholdLocked(cmd.PartnerID, h.ID, cmd.CitizenID)
		c.setColdHouseholdLocked(cmd.CitizenID, safeUint32(h.ID), safeUint32(cmd.PartnerID))
		c.setColdHouseholdLocked(cmd.PartnerID, safeUint32(h.ID), safeUint32(cmd.CitizenID))
	case LifeEventDeath:
		// A departure (mortality or emigration) unwires the citizen's
		// household membership (the inverse of LifeEventPartner's wiring):
		// the household's Members list is pruned and, if the departed
		// citizen was one half of an adult pairing, the SURVIVING partner's
		// Partner reference is cleared (the pairing dissolves; the
		// household itself persists as long as any member remains -- see
		// household.go's dissolution-invariant doc, F1 fix). Both the
		// household id AND the departed citizen's own Partner id must be
		// resolved BEFORE the citizen is removed from hot+cold (no record
		// remains to read them back from afterwards).
		var householdID, partnerID uint64
		if cit, ok := c.hot[cmd.CitizenID]; ok {
			householdID = cit.Household
			partnerID = cit.Partner
		} else if r, ok := c.coldRecord(cmd.CitizenID); ok {
			householdID = uint64(r.Household)
			partnerID = uint64(r.Partner)
		}
		delete(c.hot, cmd.CitizenID)
		c.removeColdLocked(cmd.CitizenID)
		c.removeHouseholdMemberLocked(cmd.CitizenID, householdID, partnerID)
	case LifeEventEducation:
		// Education drifts the personality (good schooling widens ambition/
		// novelty-seeking). The cold store is the single source of truth, so
		// the drift must write through to the cold columns, not only the
		// hot elevation cache.
		var newP Personality
		if cit, ok := c.hot[cmd.CitizenID]; ok {
			newP = ApplyEducationEffect(cit.Personality, cit.Education.Attainment)
			cit.Personality = newP
			refreshDerivedLeisure(cit) // personality changed → re-derive leisure
		} else if shard, row, ok := c.coldRowLocked(cmd.CitizenID); ok {
			s := c.cold[shard]
			newP = ApplyEducationEffect(widenPersonality(s.personalityAt(row)), int32(s.attainment[row]))
		} else {
			return nil // unknown citizen: no-op, not a corruption
		}
		c.setColdPersonalityLocked(cmd.CitizenID, newP)
	case LifeEventEmployment:
		// Validate the enum domains through the shared per-field validators
		// BEFORE mutating either store — an out-of-domain state/sector would
		// otherwise pack to an invalid (15,15) value in the cold column, and
		// an EmploymentState(6) would persist a value outside the 0-5 domain
		// (0-5 since EmploymentOffMap=5 widened it — FEAT-198).
		if err := validateEmploymentState(cmd.CitizenID, cmd.Employment, cmd.CorrelationID); err != nil {
			return err
		}
		if err := validateSector(cmd.CitizenID, cmd.Sector, cmd.CorrelationID); err != nil {
			return err
		}
		if cit, ok := c.hot[cmd.CitizenID]; ok {
			cit.Employment = Employment{State: cmd.Employment, Sector: cmd.Sector}
		}
		c.mutateColdLocked(cmd.CitizenID, func(s *ColdShard, row int) {
			s.employment[row] = packEmployment(cmd.Employment, cmd.Sector)
		})
	case LifeEventHealth:
		// Same shared validator: HealthBand(255) or HealthBand(6) must be
		// rejected before reaching the cold column, exactly as birth rejects it.
		if err := validateHealthBand(cmd.CitizenID, cmd.HealthBand, cmd.CorrelationID); err != nil {
			return err
		}
		if cit, ok := c.hot[cmd.CitizenID]; ok {
			cit.HealthBand = cmd.HealthBand
		}
		c.mutateColdLocked(cmd.CitizenID, func(s *ColdShard, row int) {
			s.healthBands[row] = uint8(cmd.HealthBand)
		})
	case LifeEventWealth:
		if cit, ok := c.hot[cmd.CitizenID]; ok {
			cit.Wealth = cmd.Wealth
		}
		c.mutateColdLocked(cmd.CitizenID, func(s *ColdShard, row int) {
			s.wealth[row] = cmd.Wealth
		})
	}
	return nil
}

// AdvanceDayTick advances one logistics day-tick (AC-6/AC-7): it processes
// the amortised 1/30 slice of cold shards for the current day, then
// advances the day. When 30 day-ticks elapse, the month increments and the
// next month's sample-derived parameters are computed. Wall-clock time is
// never read (AC-20); the day/month are internal sim state.
//
// Returns THIS CALL's own births/deaths (FEAT-169) — not the cumulative
// month-to-date or completed-month totals VitalEvents reports. This is the
// live-tick composition root's (internal/engine/compose) T0 conservation
// seam: the ICD (docs/planning/icd/engine.citizens-coldpass.md §5) requires
// a births/deaths delta land in the caller's own conservation ledger the
// SAME TICK it is computed, but this pass's mortality/fertility mutations
// land on the cold store incrementally, one amortised shard-slice per
// day-tick — VitalEvents' monthly-completed-totals granularity would defer
// that credit past the tick the mutation actually happened on (a real
// conservation violation the daily invariant check WOULD catch, and did,
// during FEAT-169's build: same-tick per-day totals were added precisely
// because the deferred-batch design failed that check). VitalEvents is
// unchanged and still reports the completed-month totals for any consumer
// that wants a monthly view; it is simply no longer the compose-side
// conservation seam.
func (c *CitizensAPI) AdvanceDayTick(correlationID string) (births, deaths int, err error) {
	if err := c.checkNotCopied(correlationID, "AdvanceDayTick"); err != nil {
		return 0, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.dayTick == 0 {
		c.monthParams = c.coldParamsLocked(correlationID)
	}

	shards := ColdPassSchedule(c.dayTick)
	hotSet := c.hotIDSetLocked()
	params := c.monthParams
	month := c.month
	seed := c.seed

	// Each goroutine writes only its own shard's slot (same discipline as
	// results itself), so the per-shard death slices never alias.
	deathsByShard := make([][]coldDeath, numColdShards)
	results := runShardsParallel(c.workers, shards, func(shard int) passTotals {
		tot, deaths := c.cold[shard].applyMonthly(seed, month, params, func(id uint64) bool {
			return hotSet[id]
		})
		deathsByShard[shard] = deaths
		return tot
	})

	// Sum in ascending shard order (deterministic merge), ignoring slots for
	// shards not scheduled this day.
	var tot passTotals
	for _, t := range results {
		tot = tot.add(t)
	}
	c.curMonthDeaths += tot.deaths

	// BUG-369: route this pass's cold deaths through the SAME household-
	// dissolution path LifeEventDeath uses — a bare removeAt left the
	// surviving partner's Partner reference dangling on the dead id, leaked
	// the dead member into the household forever, and let
	// householdChildCountLocked count the unresolvable dead member as a
	// child against MaxChildrenPerHousehold. Applied SEQUENTIALLY after the
	// parallel pass has fully completed (never inside runShardsParallel's
	// goroutines): removeHouseholdMemberLocked touches the shared
	// households map and potentially cross-shard survivors — the same
	// sequential-after-parallel discipline applyFertilityLocked documents.
	// Ascending schedule order and within-shard death order are both fixed,
	// so the merge is deterministic (AC-17).
	for _, shard := range shards {
		for _, d := range deathsByShard[shard] {
			// BUG-270: an ELEVATED citizen's death (the cold pass now draws
			// mortality for hot/warm citizens too) must also drop them from the
			// hot elevation cache, exactly as LifeEventDeath's delete(c.hot,...)
			// does -- otherwise a dead id lingers in c.hot pointing at a removed
			// cold record. A no-op for a citizen who was cold. Done before the
			// household unwiring, mirroring LifeEventDeath's order (the survivor,
			// not the departed id, is the one removeHouseholdMemberLocked reads).
			delete(c.hot, d.citizenID)
			c.removeHouseholdMemberLocked(d.citizenID, d.householdID, d.partnerID)
		}
	}

	// Fertility (FEAT-160): a deterministic SEQUENTIAL pass over the same
	// scheduled shards, run only after the parallel mortality/education/job
	// pass above has fully completed — see applyFertilityLocked's doc
	// comment for why a couple's cross-shard partner read cannot safely run
	// inside runShardsParallel's goroutines. dayBirths is THIS call's own
	// count (also accumulated into curMonthBirths -- birthChildLocked
	// increments it directly -- for VitalEvents' completed-month view).
	dayBirths := c.applyFertilityLocked(seed, month, shards, correlationID)

	c.dayTick++
	if c.dayTick == DaysPerMonth {
		c.dayTick = 0
		c.month++
		// Keep every elevated citizen's derived age in step with the sim
		// clock (the hot record's Month must not freeze at the promotion
		// month). Setting Month to the same value for every hot citizen is
		// order-independent, so map iteration order cannot affect results.
		c.syncHotMonthLocked()
		// The calendar month just completed: snapshot its births/deaths
		// totals for VitalEvents and reset the in-progress accumulators for
		// the new month (mirrors monthParams's own per-month recompute).
		c.lastMonthBirths = c.curMonthBirths
		c.lastMonthDeaths = c.curMonthDeaths
		c.curMonthBirths = 0
		c.curMonthDeaths = 0
	}
	return dayBirths, tot.deaths, nil
}

// VitalEvents returns the fertility births and mortality deaths tallied
// across the most recently COMPLETED calendar month (FEAT-160). This is the
// conservation-accounting surface: TotalPopulation after a completed month
// equals TotalPopulation before it, plus VitalEvents' births, minus its
// deaths — exactly invariant/people.go's PeopleInvariant identity
// (Closing - Opening == TrackedDelta), with births/deaths as this
// package's own tracked delta terms, the same role migration admits play
// at the composition root (compose.go's peopleDelta). A partially-advanced
// (mid-month) call returns the PREVIOUS completed month's totals, never a
// half-counted in-progress figure.
func (c *CitizensAPI) VitalEvents(correlationID string) (births, deaths int) {
	if err := c.checkNotCopied(correlationID, "VitalEvents"); err != nil {
		return 0, 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastMonthBirths, c.lastMonthDeaths
}

// AdvanceMonth advances a full calendar month (30 day-ticks). Convenience
// for tests and the perf harness.
func (c *CitizensAPI) AdvanceMonth(correlationID string) error {
	if err := c.checkNotCopied(correlationID, "AdvanceMonth"); err != nil {
		return err
	}
	for d := 0; d < DaysPerMonth; d++ {
		if _, _, err := c.AdvanceDayTick(correlationID); err != nil {
			return err
		}
	}
	return nil
}

// PopulationHash returns a deterministic sha256 over the entire citizen
// population and household set, in fixed order — the AC-17 shard/worker-
// count invariance fingerprint.
func (c *CitizensAPI) PopulationHash(correlationID string) [32]byte {
	_ = c.checkNotCopied(correlationID, "PopulationHash")
	c.mu.RLock()
	defer c.mu.RUnlock()

	h := sha256.New()
	var buf [8]byte
	putU64 := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	putI64 := func(v int64) { putU64(uint64(v)) }

	putU64(c.seed)
	putI64(c.month)
	putU64(uint64(c.dayTick))

	for shard := 0; shard < numColdShards; shard++ {
		s := c.cold[shard]
		putU64(uint64(s.count()))
		for i := 0; i < s.count(); i++ {
			r := s.recordAt(i)
			putU64(r.ID)
			putI64(r.BirthMonth)
			h.Write([]byte{byte(r.Sex), byte(r.HealthBand), r.Access, byte(r.Stage)})
			putU64(uint64(r.Household))
			putU64(uint64(r.Partner))
			putU64(uint64(r.Home))
			putU64(uint64(r.District))
			putU64(uint64(r.Wealth))
			h.Write([]byte{byte(r.EmploymentState), byte(r.Sector)})
			for _, a := range r.Personality {
				h.Write([]byte{byte(a)})
			}
			h.Write([]byte{
				byte(s.monthlyUpdates[i]),
				byte(r.SatHousing), byte(r.SatServices), byte(r.SatEnvironment),
				byte(r.SatLeisureFit), byte(r.SatCommute),
			})
		}
	}

	// Hot set + households, in sorted-id order (never map order).
	hotIDs := make([]uint64, 0, len(c.hot))
	for id := range c.hot {
		hotIDs = append(hotIDs, id)
	}
	sort.Slice(hotIDs, func(i, j int) bool { return hotIDs[i] < hotIDs[j] })
	for _, id := range hotIDs {
		putU64(id)
		h.Write([]byte{byte(c.hot[id].Fidelity)})
	}

	hhIDs := make([]uint64, 0, len(c.households))
	for id := range c.households {
		hhIDs = append(hhIDs, id)
	}
	sort.Slice(hhIDs, func(i, j int) bool { return hhIDs[i] < hhIDs[j] })
	for _, id := range hhIDs {
		hh := c.households[id]
		putU64(id)
		h.Write([]byte{hh.DwellingRooms})
		for _, m := range hh.Members {
			putU64(m)
		}
	}

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// --- internal helpers (all called with c.mu held) ---

func (c *CitizensAPI) householdExistsLocked(id uint64) bool {
	_, ok := c.households[id]
	return ok
}

func (c *CitizensAPI) hotIDSetLocked() map[uint64]bool {
	set := make(map[uint64]bool, len(c.hot))
	for id := range c.hot {
		set[id] = true
	}
	return set
}

func (c *CitizensAPI) allColdRecordsLocked() []ColdRecord {
	var out []ColdRecord
	for _, s := range c.cold {
		for i := 0; i < s.count(); i++ {
			out = append(out, s.recordAt(i))
		}
	}
	return out
}

func (c *CitizensAPI) coldRecord(id uint64) (ColdRecord, bool) {
	shard := det.ShardForEntity(id)
	s := c.cold[shard]
	if row := s.rowOf(id); row >= 0 {
		return s.recordAt(row), true
	}
	return ColdRecord{}, false
}

func (c *CitizensAPI) removeColdLocked(id uint64) {
	shard := det.ShardForEntity(id)
	s := c.cold[shard]
	if row := s.rowOf(id); row >= 0 {
		s.removeAt(row)
	}
}

func (c *CitizensAPI) setHouseholdLocked(citizenID, householdID, partnerID uint64) {
	if cit, ok := c.hot[citizenID]; ok {
		cit.Household = householdID
		cit.Partner = partnerID
	}
}

// removeHouseholdMemberLocked unwires a departed citizen from their
// household (LifeEventDeath's inverse of LifeEventPartner's wiring). F1 fix
// (destructive-review REJECT on FEAT-160): dissolution/unwiring keys on the
// PAIRING, not raw member count -- see household.go's dissolution-invariant
// doc. The departed citizen is dropped from the household's Members list.
// partnerID is the departed citizen's own (pre-removal) Partner id: if
// non-zero, the departed citizen WAS one half of an adult pairing, so the
// SURVIVING partner's Partner reference is cleared (the pairing dissolves --
// the survivor may legitimately re-partner later) while their Household
// reference is left untouched, because the household persists as long as
// any member remains (surviving parent + children, or a lone childless
// survivor still living in the same dwelling). The household is deleted
// only once its Members list is fully empty.
func (c *CitizensAPI) removeHouseholdMemberLocked(citizenID, householdID, partnerID uint64) {
	if householdID == 0 {
		return // unpaired citizen: nothing to unwire
	}
	h, ok := c.households[householdID]
	if !ok {
		return // absent household: nothing to prune
	}
	kept := h.Members[:0]
	for _, m := range h.Members {
		if m != citizenID {
			kept = append(kept, m)
		}
	}
	h.Members = kept

	if partnerID != 0 {
		// The departed citizen was paired: dissolve the pairing on the
		// survivor's side only (Partner -> 0), never touching their
		// Household -- the survivor (and any children) keep the household.
		c.clearPartnerOnlyLocked(partnerID)
	}

	if len(h.Members) == 0 {
		delete(c.households, householdID)
	}
}

// detachFromHouseholdLocked unwires citizenID from whatever household they
// CURRENTLY belong to (round-3 fix, P1: LifeEventPartner's re-partnering
// leak). Called BEFORE FormHousehold mints a fresh pairing, so a citizen
// re-partnering after a prior pairing dissolved (a widowed survivor, F1's
// scenario) is never left double-listed: once in their stale old
// household's Members (never pruned by F1's death path, which deliberately
// leaves the survivor's household intact) and once via their own Household
// field pointing at the new pairing. A no-op for a citizen with no current
// household (Household == 0, e.g. a first-time pairing). Reuses
// removeHouseholdMemberLocked's exact prune-then-maybe-delete pattern
// (prune from Members; if a live Partner reference resolves the OTHER
// member of the pairing, clear their Partner side too, mirroring F1's
// dissolution invariant; delete the household once fully empty), then
// additionally clears the DEPARTING citizen's own stale Household/Partner
// fields (which removeHouseholdMemberLocked's original LifeEventDeath
// caller never had to do, because that path deletes the departing citizen's
// record entirely -- here the citizen is NOT being removed, they are about
// to be re-wired into a brand new household by the caller).
func (c *CitizensAPI) detachFromHouseholdLocked(citizenID uint64) {
	var householdID, partnerID uint64
	if cit, ok := c.hot[citizenID]; ok {
		householdID = cit.Household
		partnerID = cit.Partner
	} else if r, ok := c.coldRecord(citizenID); ok {
		householdID = uint64(r.Household)
		partnerID = uint64(r.Partner)
	}
	if householdID == 0 {
		return // no current household: nothing to detach
	}
	c.removeHouseholdMemberLocked(citizenID, householdID, partnerID)
	if cit, ok := c.hot[citizenID]; ok {
		cit.Household = 0
		cit.Partner = 0
	}
	c.setColdHouseholdLocked(citizenID, 0, 0)
}

// clearPartnerOnlyLocked resets a citizen's Partner reference to 0 (the "no
// partner" sentinel) in BOTH the hot elevation cache and the cold store (the
// single source of truth), WITHOUT touching their Household reference. Used
// when a partner departs (death/emigration): the pairing dissolves but the
// household itself persists (F1 fix) -- this is the pairing-only half of
// the setHouseholdLocked/setColdHouseholdLocked wiring LifeEventPartner
// performs.
func (c *CitizensAPI) clearPartnerOnlyLocked(citizenID uint64) {
	var householdID uint64
	if cit, ok := c.hot[citizenID]; ok {
		cit.Partner = 0
		householdID = cit.Household
	} else if r, ok := c.coldRecord(citizenID); ok {
		householdID = uint64(r.Household)
	}
	c.setColdHouseholdLocked(citizenID, safeUint32(householdID), 0)
}

// setColdHouseholdLocked updates a citizen's household/partner columns in
// the cold store (the single source of truth). A partnering event must
// reach the cold record, not only the elevation cache.
func (c *CitizensAPI) setColdHouseholdLocked(citizenID uint64, householdID, partnerID uint32) {
	shard := det.ShardForEntity(citizenID)
	s := c.cold[shard]
	if row := s.rowOf(citizenID); row >= 0 {
		s.households[row] = householdID
		s.partners[row] = partnerID
	}
}

// coldRowLocked returns the (shard, row) of a citizen's cold record.
func (c *CitizensAPI) coldRowLocked(id uint64) (int, int, bool) {
	shard := det.ShardForEntity(id)
	row := c.cold[shard].rowOf(id)
	if row < 0 {
		return 0, 0, false
	}
	return shard, row, true
}

// mutateColdLocked applies fn to a citizen's cold record (the single source
// of truth), so every life-event mutation writes through to the cold store,
// never only the hot elevation cache.
func (c *CitizensAPI) mutateColdLocked(id uint64, fn func(s *ColdShard, row int)) {
	shard := det.ShardForEntity(id)
	if row := c.cold[shard].rowOf(id); row >= 0 {
		fn(c.cold[shard], row)
	}
}

// setColdPersonalityLocked writes a rich personality back into a citizen's
// cold personality columns (used by the education-drift write-through).
func (c *CitizensAPI) setColdPersonalityLocked(id uint64, p Personality) {
	c.mutateColdLocked(id, func(s *ColdShard, row int) {
		s.setPersonality(row, p)
	})
}

// syncHotMonthLocked advances every elevated (HOT/WARM) citizen's Month to
// the current sim month AND re-derives every month-derived field, so a hot
// citizen stays observationally identical to the cold record it shadows:
// Age() tracks the sim clock rather than freezing at the promotion month,
// and the age-derived Leisure re-derives at the new month rather than going
// stale. Setting Month (and re-deriving Leisure from it) to the same value
// for every hot citizen is order-independent, so map iteration order
// cannot affect results.
func (c *CitizensAPI) syncHotMonthLocked() {
	for _, cit := range c.hot {
		cit.Month = c.month
		refreshDerivedLeisure(cit)
	}
}

// refreshDerivedLeisure re-derives a hot citizen's Leisure from its current
// Personality × education × age, so a derived field never goes stale when
// one of its inputs changes (age on clock sync, personality on education).
func refreshDerivedLeisure(cit *Citizen) {
	cit.Leisure = DeriveLeisureWeights(cit.Personality, cit.Education.Attainment, cit.Age())
}

func (c *CitizensAPI) coldParamsLocked(correlationID string) ColdPassParams {
	records := c.allColdRecordsLocked()
	return DeriveColdPassParams(BuildStratifiedSample(records, c.month, c.seed, 1))
}

// --- conversions ---

// widenPersonality widens a compressed int8-axis personality back into the
// rich int32 Personality (the inverse of ColdShard.setPersonality).
func widenPersonality(p8 [NumPersonalityAxes]int8) Personality {
	var p Personality
	for a := 0; a < NumPersonalityAxes; a++ {
		p[a] = int32(p8[a])
	}
	return p
}

// coldRecordToHot widens a compressed cold record back into a rich hot
// citizen (fidelity is set by the caller). The cold store compresses the
// child list to a count and the education stage history to the current
// stage (a documented compression — the full child list is reconstructed
// from household membership on life-write), so the widened record carries
// their compressed equivalents: Children is nil, and Education.Stages is a
// single ongoing entry for the current stage. Every OTHER field is carried
// losslessly — including School, which shares no column with Workplace.
func coldRecordToHot(r ColdRecord, month int64) Citizen {
	var p Personality
	for a := 0; a < NumPersonalityAxes; a++ {
		p[a] = int32(r.Personality[a])
	}
	att := int32(r.Attainment)
	var stages []StageEntry
	if r.Stage != StageNone {
		// The current stage is stored as a scalar; reconstruct a single
		// ongoing entry so currentStage() reads it back unchanged.
		stages = []StageEntry{{Stage: r.Stage, StartMonth: 0, EndMonth: -1}}
	}
	return Citizen{
		ID:          r.ID,
		BirthMonth:  int32(r.BirthMonth),
		Sex:         r.Sex,
		Household:   uint64(r.Household),
		Partner:     uint64(r.Partner),
		Home:        r.Home,
		Workplace:   uint64(r.Workplace),
		School:      uint64(r.School),
		Personality: p,
		Education: Education{
			Stages:          stages,
			Attainment:      att,
			SchoolingMonths: int32(r.Schooling),
		},
		Leisure:    DeriveLeisureWeights(p, att, month-r.BirthMonth),
		HealthBand: r.HealthBand,
		Wealth:     r.Wealth,
		Employment: Employment{State: r.EmploymentState, Sector: r.Sector},
		Satisfaction: Satisfaction{
			int32(r.SatHousing), int32(r.SatServices), int32(r.SatEnvironment),
			int32(r.SatLeisureFit), int32(r.SatCommute),
		},
		Month: month,
	}
}

// hotToColdRecord compresses a hot citizen into a cold record (the
// promotion/demotion and birth paths). The personality axes are clamped to
// the int8 range (they are already 0-100 by validation).
func hotToColdRecord(c Citizen, district uint16) ColdRecord {
	var p [NumPersonalityAxes]int8
	for a := 0; a < NumPersonalityAxes; a++ {
		p[a] = int8(c.Personality[a])
	}
	return ColdRecord{
		ID:              c.ID,
		BirthMonth:      int64(c.BirthMonth),
		Sex:             c.Sex,
		Household:       safeUint32(c.Household),
		Partner:         safeUint32(c.Partner),
		ChildCount:      safeUint8(len(c.Children)),
		Home:            c.Home,
		District:        district,
		Workplace:       safeUint32(c.Workplace),
		School:          safeUint32(c.School),
		Personality:     p,
		Attainment:      safeInt16(c.Education.Attainment),
		Stage:           currentStage(c.Education),
		Schooling:       safeInt16(c.Education.SchoolingMonths),
		HealthBand:      c.HealthBand,
		Wealth:          c.Wealth,
		EmploymentState: c.Employment.State,
		Sector:          c.Employment.Sector,
		SatHousing:      int32(c.Satisfaction[SatHousing]),
		SatServices:     int32(c.Satisfaction[SatServices]),
		SatEnvironment:  int32(c.Satisfaction[SatEnvironment]),
		SatLeisureFit:   int32(c.Satisfaction[SatLeisureFit]),
		SatCommute:      int32(c.Satisfaction[SatCommute]),
	}
}

// currentStage returns the latest stage in the education history, or
// StageNone if empty.
func currentStage(e Education) Stage {
	if len(e.Stages) == 0 {
		return StageNone
	}
	return e.Stages[len(e.Stages)-1].Stage
}

// districtOf maps a home cell to its district (a deterministic placeholder
// until the real district model lands). Pure, no wall clock.
func districtOf(home CellRef) uint16 {
	return uint16(home >> 16)
}

func cloneCitizen(c Citizen) Citizen {
	c.Children = append([]uint64(nil), c.Children...)
	c.Education.Stages = append([]StageEntry(nil), c.Education.Stages...)
	return c
}

func cloneHousehold(h Household) Household {
	h.Members = append([]uint64(nil), h.Members...)
	return h
}

// --- command types ---

// LifeEventKind enumerates the life-event commands (AC-1b's command
// surface).
type LifeEventKind uint8

const (
	LifeEventBirth      LifeEventKind = 0
	LifeEventPartner    LifeEventKind = 1
	LifeEventDeath      LifeEventKind = 2
	LifeEventEducation  LifeEventKind = 3
	LifeEventEmployment LifeEventKind = 4
	LifeEventHealth     LifeEventKind = 5
	LifeEventWealth     LifeEventKind = 6
)

// LifeEventCommand is the command-based mutation path for life events
// (AC-1b). Consumers never write citizen fields directly; they submit
// this command.
type LifeEventCommand struct {
	CorrelationID string
	Kind          LifeEventKind
	Citizen       Citizen         // LifeEventBirth: the record to create (validated)
	CitizenID     uint64          // all non-birth events
	PartnerID     uint64          // LifeEventPartner
	Employment    EmploymentState // LifeEventEmployment
	Sector        Sector          // LifeEventEmployment
	HealthBand    HealthBand      // LifeEventHealth
	Wealth        int64           // LifeEventWealth
}

// FidelityCommand is the command-based mutation path for fidelity
// promotion/demotion (AC-4).
type FidelityCommand struct {
	CorrelationID string
	CitizenID     uint64
	Target        Fidelity
}
