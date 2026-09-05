package citizens

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
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

	// mortalityCfg is the loaded data/mortality.json death-queue smoothing
	// budget (FEAT-087 mkey feat.deathwave), read once at construction
	// (GR#15: the monthly budget is data, never a Go literal). deathQueue
	// is the LIVE citywide smoothing buffer applyMonthly's mortality draw
	// enqueues into and AdvanceDayTick's realisation step drains from, once
	// per completed month, bounded by mortalityCfg.MonthlyDeathBudget()
	// (AC-1/AC-2 — see deathwave.go and AdvanceDayTick's doc comment).
	mortalityCfg MortalityConfig
	deathQueue   *DeathQueue

	// season is the OPTIONAL injected engine.season dependency (FEAT-087
	// inc2, mkey feat.deathwave, AC-6/AC-7): consumed only to declare a
	// weather emergency (weatheremergency.go's IsWeatherEmergency) at the
	// once-per-completed-month realisation step below. Wired post-
	// construction via SetSeason (mirroring engine.build/engine.cafe/
	// engine.education's own SetSeason(*season.SeasonAPI) precedent) rather
	// than a NewCitizensAPI constructor argument, so every existing caller
	// (~80 call sites across the repo) keeps compiling unchanged. A nil
	// season is a documented no-op: IsWeatherEmergency always returns false
	// for a CitizensAPI that was never wired to engine.season, so ordinary
	// (non-emergency) smoothing behaves exactly as inc1/inc1.5 built it.
	season *season.SeasonAPI

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

	// pages is the OPTIONAL disk-backed paging seam (BUG-664, paging.go),
	// wired in only via EnableDiskPaging. nil (the default for every
	// existing NewCitizensAPI caller, ~80 call sites) means every shard
	// stays permanently resident in cold, byte-for-byte the pre-BUG-664
	// behaviour -- paging is opt-in and changes nothing for a caller that
	// never enables it.
	pages *PageStore
	// maxResidentShards bounds how many of the numColdShards slots stay
	// non-nil in cold at once once paging is enabled. Meaningless while
	// pages == nil.
	maxResidentShards int
	// pageOrder is shardAt's LEGACY LRU order of shard indices, kept ONLY
	// for resetForLoad/EnableDiskPaging's doc-comment continuity; the real
	// eviction order now comes from pageList (BUG-664 round-2 P2: O(len)
	// linear scan+splice replaced by an O(1)-amortised doubly-linked list,
	// see pageList's own doc comment). Deliberately still never a map for
	// ORDER purposes (GR#21): pageList's traversal order is exactly touch
	// history, never Go's randomised map iteration order -- pageElem below
	// is used only for O(1) NODE LOOKUP, never to decide eviction order.
	pageOrder []int
	// pageList is the deterministic LRU order of shard indices currently
	// resident in cold (paging-enabled mode only): Front() is the
	// least-recently-touched resident, Back() the most recent.
	// touchShardLocked moves a shard's node to the back in O(1) via
	// pageElem's lookup (container/list itself never allocates a hidden
	// hash map -- Go's map only backs pageElem here, for finding a node by
	// shard index, and is never iterated to establish an ORDER). Kept
	// alongside pageOrder (a plain slice) for now so resetForLoad's
	// existing reseed idiom keeps compiling; both are rebuilt together.
	pageList *list.List
	// pageElem is pageList's O(1) node lookup: shard index -> its
	// *list.Element, so touchShardLocked/evictOverBudgetLocked never do a
	// linear scan over the resident set (BUG-664 round-2 P2). Guarded by
	// pagingMu, exactly like pageList and pageOrder.
	pageElem map[int]*list.Element
	// residentCount is an O(1)-maintained mirror of "how many of the
	// numColdShards slots in cold are non-nil right now" (BUG-664 round-2
	// P2: evictOverBudgetLocked used to recompute this with a fresh
	// O(numColdShards) scan on EVERY shardAt call). Incremented exactly
	// once per nil->resident transition (the paged-in miss branch of
	// loadShardLocked, plus EnableDiskPaging/resetForLoad's initial seed),
	// decremented exactly once per resident->nil transition
	// (evictOverBudgetLocked's successful Store+nil). Guarded by pagingMu.
	residentCount int
	// shardPins is the BUG-664 round-2 P0 fix: a per-shard in-use
	// refcount, guarded by pagingMu. acquireShard increments the count
	// for the caller's shard BEFORE releasing pagingMu (so the pointer it
	// hands back is provably still resident for as long as the caller
	// holds the pin); releaseShard decrements it. evictOverBudgetLocked
	// NEVER selects a shard with shardPins[i] > 0 as an eviction victim --
	// see its own doc comment for the "all candidates pinned" policy.
	// Plain shardAt (used by every OTHER call site in this package, all
	// of which run strictly sequentially under c.mu -- see shardAt's own
	// doc comment for why only runShardsParallel's goroutines are ever
	// concurrent) does not pin at all, so it stays the pre-existing
	// zero-ceremony accessor those 17 call sites already use.
	shardPins [numColdShards]int32
	// pagingMu guards cold's nil<->resident slot transitions and every
	// PageStore.Load/Store call, INDEPENDENT of mu. shardAt/acquireShard
	// are called from inside runShardsParallel's per-shard goroutines
	// (AdvanceDayTick, c.mu already held there) as well as from ordinary
	// c.mu.RLock query paths (coldRecord and friends) -- a second,
	// dedicated mutex here means shardAt is safe under either of mu's two
	// lock modes without a nested-acquire deadlock or lock-order hazard,
	// since pagingMu is never held while acquiring mu (only the reverse).
	pagingMu sync.Mutex

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
	mortalityCfg, err := LoadDefaultMortalityConfig(correlationID)
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
		mortalityCfg:    mortalityCfg,
		deathQueue:      NewDeathQueue(),
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

// EnableDiskPaging wires BUG-664's disk-backed LRU PageStore in as the real
// residency manager for cold (A7, doc.go's "NVMe and paging" section):
// beyond maxResident of the numColdShards shards, the least-recently-used
// resident shards are evicted to dir and reloaded on demand, so resident
// memory stops growing past the residency ceiling regardless of city size.
// Optional and idempotent-to-call-once, mirroring SetSeason/
// SetDeathDrainCapacity's post-construction wiring precedent -- a
// CitizensAPI that never calls this keeps every shard permanently resident,
// exactly pre-BUG-664 behaviour. maxResident < 1 is rejected (evicting
// every shard on every access would defeat every other read/write path in
// this package, which assumes shardAt returns a stable, currently-in-use
// shard for the duration of the call).
func (c *CitizensAPI) EnableDiskPaging(dir string, maxResident int, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "EnableDiskPaging"); err != nil {
		return err
	}
	if maxResident < 1 {
		return errs.New(ErrInvalidPagingBudget, correlationID, map[string]any{"maxResident": maxResident})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pages = NewPageStore(dir, maxResident)
	c.maxResidentShards = maxResident
	c.seedPageBookkeepingLocked()
	return nil
}

// seedPageBookkeepingLocked (re)builds pageOrder/pageList/pageElem/
// residentCount/shardPins from the CURRENT contents of cold. Called from
// EnableDiskPaging (first wiring) and resetForLoad (a load target rebuilds
// cold from scratch, so any pre-reset LRU/pin history is meaningless and
// must never survive into the loaded city -- see resetForLoad's own doc
// comment). Every shard starts resident already (construction/restore
// populate cold directly), so this seeds the LRU order with the
// currently-resident set: without it, the very first shardAt-driven
// eviction would have no real touch history to work from and would evict
// shard 0 first by pure accident of iteration order. Caller holds c.mu
// (not pagingMu -- this only runs during construction-time wiring or a
// full-registry reset, never concurrently with a live shardAt/acquireShard
// call, so pagingMu is not needed here).
func (c *CitizensAPI) seedPageBookkeepingLocked() {
	c.pageOrder = c.pageOrder[:0]
	c.pageList = list.New()
	c.pageElem = make(map[int]*list.Element, numColdShards)
	c.residentCount = 0
	for i := range c.cold {
		c.shardPins[i] = 0
		if c.cold[i] != nil {
			c.pageOrder = append(c.pageOrder, i)
			c.pageElem[i] = c.pageList.PushBack(i)
			c.residentCount++
		}
	}
}

// shardAt returns the ColdShard resident at index shard, transparently
// reloading it from c.pages if paging is enabled and the shard has been
// paged out (cold[shard] == nil). Every direct cold[shard] read/write in
// this package (registry.go/fertility.go/participant.go) goes through this
// accessor instead -- the single choke point BUG-664 was missing -- so a
// citizen lookup, a life-event mutation, and the amortised cold pass all
// see the SAME transparently-paged view of the store, regardless of
// whether the caller holds mu as a reader or a writer (pagingMu is
// independent of mu, see its own doc comment on the struct).
//
// When paging is disabled (the default, c.pages == nil) this is exactly
// the pre-BUG-664 `c.cold[shard]` expression -- zero lock overhead, zero
// behavioural change, so every existing determinism/perf test is
// unaffected by paging's mere existence in the package.
func (c *CitizensAPI) shardAt(shard int) *ColdShard {
	if c.pages == nil {
		return c.cold[shard]
	}
	c.pagingMu.Lock()
	defer c.pagingMu.Unlock()
	return c.loadShardLocked(shard)
}

// acquireShard is shardAt's PINNED sibling (BUG-664 round-2 P0 fix): it
// returns the resident shard at index shard exactly like shardAt, but also
// increments shardPins[shard] BEFORE releasing pagingMu, and the caller
// MUST call releaseShard(shard) (via defer, at every call site) once done
// with the pointer. While a shard is pinned, evictOverBudgetLocked will
// NEVER select it as an eviction victim, no matter how far over budget
// residency runs.
//
// This is the fix for the round's P0: runShardsParallel fans AdvanceDayTick
// out over c.workers goroutines, each processing a DIFFERENT shard
// concurrently under the SAME c.mu.Lock (a write lock only serialises
// against OTHER top-level API calls, not against sibling goroutines inside
// this one call) -- plain shardAt hands back a pointer and releases
// pagingMu immediately, so nothing stopped a second worker's shardAt call
// from choosing the FIRST worker's still-in-use shard as its eviction
// victim and Store()-ing (reading every column of) a shard that worker was
// concurrently mutating. acquireShard/releaseShard close that window: a
// pinned shard is provably still resident for the caller's whole work
// window, regardless of what any sibling goroutine does concurrently.
//
// Every OTHER shardAt call site in this package (registry.go/fertility.go/
// participant.go) runs strictly SEQUENTIALLY -- either under c.mu.Lock
// outside of runShardsParallel, or under c.mu.RLock query paths that never
// overlap a write-locked AdvanceDayTick/AdvanceMonth call -- so they keep
// using the plain, unpinned shardAt (see its own doc comment): adding pin
// bookkeeping there would be pure overhead with no correctness benefit,
// and (per the round's brief) plain shardAt's signature must stay
// single-return so the attacker's own regression test
// (TestAttackBug664EvictionPersistsInPlaceMutations, which calls
// `api.shardAt(shard)` directly) keeps compiling unchanged.
func (c *CitizensAPI) acquireShard(shard int) *ColdShard {
	if c.pages == nil {
		return c.cold[shard]
	}
	c.pagingMu.Lock()
	defer c.pagingMu.Unlock()
	s := c.loadShardLocked(shard)
	c.shardPins[shard]++
	return s
}

// releaseShard releases one pin acquireShard placed on shard. A no-op when
// paging is disabled (mirrors acquireShard's own zero-overhead fast path).
// Safe to call even if the pin count is already zero (defensive: a bug
// that double-releases must never underflow into a negative refcount that
// would look like "never pinned" to evictOverBudgetLocked and reopen the
// P0 race — GR#1). After releasing, opportunistically re-runs
// evictOverBudgetLocked so a shard that was only kept resident by the
// all-candidates-pinned policy (see evictOverBudgetLocked's doc comment)
// gets paged back out promptly, instead of waiting for the next unrelated
// shardAt/acquireShard call to notice the budget is still exceeded.
func (c *CitizensAPI) releaseShard(shard int) {
	if c.pages == nil {
		return
	}
	c.pagingMu.Lock()
	defer c.pagingMu.Unlock()
	if c.shardPins[shard] > 0 {
		c.shardPins[shard]--
	}
	c.evictOverBudgetLocked(-1) // -1: no shard to protect, everything unpinned is fair game
}

// loadShardLocked returns the resident shard at index shard, transparently
// reloading it from c.pages if paging is enabled and the shard has been
// paged out (cold[shard] == nil), touching its LRU position and running
// the eviction check. Caller holds pagingMu. Shared by shardAt (unpinned)
// and acquireShard (which additionally pins after this returns) so both
// accessors see the exact same load/touch/evict discipline.
func (c *CitizensAPI) loadShardLocked(shard int) *ColdShard {
	if s := c.cold[shard]; s != nil {
		// EnableDiskPaging can be (and usually is) called against a
		// CitizensAPI whose 256 shards are ALL already resident from
		// NewCitizensAPI's construction loop -- every one of them starts
		// non-nil, never paged out, so a cache HIT must still run the
		// eviction check. Without this, the very first EnableDiskPaging
		// call would never shrink residency down to maxResidentShards at
		// all: nothing would ever be nil, so the cache-MISS reload branch
		// below (the only other place evictOverBudgetLocked used to run)
		// would never fire, and the whole paging seam would silently stay
		// a no-op forever.
		c.touchShardLocked(shard)
		c.evictOverBudgetLocked(shard)
		return s
	}
	// Paged out: reload from disk. A miss (ok == false) means this shard
	// has never been persisted under paging (e.g. it was evicted before
	// ever being Store()'d, structurally unreachable since
	// evictOverBudgetLocked always Stores before nilling) -- fail safe
	// with a fresh empty shard rather than a nil-deref crash (GR#1) if it
	// is ever hit.
	s, ok := c.pages.Load(shard)
	if !ok {
		s = newColdShard(0)
	}
	c.cold[shard] = s
	c.residentCount++
	c.touchShardLocked(shard)
	c.evictOverBudgetLocked(shard)
	return s
}

// touchShardLocked moves shard to the most-recently-used end of pageList
// (caller holds pagingMu). O(1) amortised via pageElem's node lookup
// (BUG-664 round-2 P2: this used to be an O(len(pageOrder)) linear
// scan+splice on every single shardAt call) -- pageOrder is kept alongside
// as a plain append-only mirror purely for resetForLoad/doc continuity,
// never consulted for eviction order any more.
func (c *CitizensAPI) touchShardLocked(shard int) {
	if e, ok := c.pageElem[shard]; ok {
		c.pageList.MoveToBack(e)
		return
	}
	c.pageElem[shard] = c.pageList.PushBack(shard)
}

// evictOverBudgetLocked pages the least-recently-used resident, UNPINNED
// shard(s) out to disk until the resident count is back within
// maxResidentShards, never evicting keep (the shard the caller just
// touched and is about to use) and NEVER evicting a shard with
// shardPins[i] > 0 (BUG-664 round-2 P0: a pinned shard is one some
// runShardsParallel worker is mid-flight on via acquireShard -- evicting
// it out from under that goroutine is exactly the data race the round
// caught). Pass keep = -1 when there is no shard to protect (e.g.
// releaseShard's post-release retry).
//
// POLICY (round brief, explicit choice): if every over-budget resident
// candidate is either keep or pinned, this returns having evicted nothing
// further, temporarily exceeding maxResidentShards rather than blocking or
// evicting an in-use shard. This is intentional and bounded in practice by
// c.workers (at most one shard pinned per live goroutine at a time) -- a
// budget of 2 with 16 workers can genuinely run up to 16 shards resident
// at once under heavy concurrent pressure, which trades a temporary
// memory-residency overshoot for correctness, never the reverse.
//
// Caller holds pagingMu. Every eviction Stores the shard's CURRENT
// in-memory state immediately before dropping the reference -- unlike
// PageStore's own doc comment ("its data is already persisted by Store"),
// this never assumes an earlier Store call is still fresh, because a
// shard's rows can be mutated in place after being loaded/made-resident
// and before being evicted (applyMonthly, append, removeAt, ... all mutate
// through the pointer shardAt/acquireShard handed out, not through a Store
// call) -- a stale disk copy at eviction time would silently lose exactly
// that mutation the next time this shard is paged back in.
func (c *CitizensAPI) evictOverBudgetLocked(keep int) {
	for c.residentCount > c.maxResidentShards {
		var victim = -1
		for e := c.pageList.Front(); e != nil; e = e.Next() {
			v := e.Value.(int)
			if v == keep || c.cold[v] == nil || c.shardPins[v] > 0 {
				continue
			}
			victim = v
			c.pageList.Remove(e)
			delete(c.pageElem, v)
			break
		}
		if victim < 0 {
			return // nothing evictable: only keep and/or pinned shards remain over budget
		}
		s := c.cold[victim]
		if err := c.pages.Store(victim, s); err != nil {
			// A disk write failure must never silently discard a resident
			// shard's data (GR#1) -- keep it resident rather than evict on
			// a failed persist. The caller already has what it needs from
			// this call; this failure just means the residency budget is
			// temporarily exceeded, corrected on the next shardAt/
			// acquireShard/releaseShard call once the transient I/O issue
			// clears. victim's pageList/pageElem entry was already removed
			// above; touchShardLocked will transparently re-seed it the
			// next time this shard is touched, exactly as before this
			// rework (pageOrder had the same "removed, not re-added on
			// failure" behaviour).
			return
		}
		c.cold[victim] = nil
		c.residentCount--
	}
}

// SetSeason wires the engine.season dependency inc2's weather-emergency
// declaration consumes (FEAT-087 mkey feat.deathwave, AC-6/AC-7) — the
// registered feat.deathwave -> engine.season outbound edge (code.json).
// Mirrors engine.build/engine.cafe/engine.education's own SetSeason
// precedent: an optional post-construction wire, never a constructor
// argument (so the ~80 existing NewCitizensAPI call sites are unaffected).
// A CitizensAPI never wired via SetSeason simply never declares a weather
// emergency (see weatheremergency.go's IsWeatherEmergency nil-season
// no-op) — ordinary smoothing is unaffected either way.
func (c *CitizensAPI) SetSeason(s *season.SeasonAPI, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "SetSeason"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.season = s
	return nil
}

// SetDeathDrainCapacity wires FEAT-088's injected funeral-throughput
// capacity into the live death queue (FEAT-087 inc3, AC-11, ASM-580).
// Optional -- a CitizensAPI that never calls this keeps drain capacity
// UNLIMITED, exactly inc1/inc1.5/inc2's existing behaviour (see
// deathwave.go's [DeathQueue.RealiseDrained] doc). Passing nil restores
// that default. Mirrors SetSeason's post-construction wiring precedent
// (no NewCitizensAPI constructor argument, so every existing call site
// keeps compiling unchanged).
func (c *CitizensAPI) SetDeathDrainCapacity(d DrainCapacity, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "SetDeathDrainCapacity"); err != nil {
		return err
	}
	// Belt-and-suspenders SEC-020 guard (astgate): c.deathQueue.SetDrainCapacity
	// below already rejects a struct-copy call on its own, but this function's
	// body itself accesses the c.deathQueue field chain, so it checks the
	// DeathQueue candidate directly too -- mirrors weatheremergency.go's
	// EmergencyRealise, which does the identical double-check for the same
	// reason (astgate's syntactic scan cannot see through the delegated call).
	if err := c.deathQueue.checkNotCopied(correlationID, "SetDeathDrainCapacity"); err != nil {
		return err
	}
	return c.deathQueue.SetDrainCapacity(d, correlationID)
}

// DeathHandoff returns FEAT-088's ordered, flagged handoff stream so far
// (FEAT-087 inc3, AC-9/AC-10): every death realised through the live
// AdvanceDayTick realisation step (which now calls
// [DeathQueue.RealiseDrained], see AdvanceDayTick's doc), FIFO by release
// order, each carrying (citizenId, deathMonth, emergencyFlag).
func (c *CitizensAPI) DeathHandoff(correlationID string) ([]RealisedDeath, error) {
	if err := c.checkNotCopied(correlationID, "DeathHandoff"); err != nil {
		return nil, err
	}
	// Belt-and-suspenders SEC-020 guard (astgate) -- see SetDeathDrainCapacity's
	// identical comment above for why this direct check is needed alongside
	// RealisedDeaths' own internal checkNotCopied.
	if err := c.deathQueue.checkNotCopied(correlationID, "DeathHandoff"); err != nil {
		return nil, err
	}
	return c.deathQueue.RealisedDeaths(correlationID), nil
}

// DeathHandoffSince is the CitizensAPI-level surface for BUG-483 F3's
// paging safety valve over the handoff stream (see
// [DeathQueue.DeathHandoffSince]'s doc for the full contract: cursor is
// the caller's own running consumed-count, a negative cursor clamps to 0,
// and a caught-up cursor returns an empty slice, never an error). Mirrors
// DeathHandoff exactly except for the cursor parameter -- DeathHandoff's
// full-cumulative-list behaviour is completely unchanged by this addition.
func (c *CitizensAPI) DeathHandoffSince(cursor int, correlationID string) ([]RealisedDeath, error) {
	if err := c.checkNotCopied(correlationID, "DeathHandoffSince"); err != nil {
		return nil, err
	}
	// Belt-and-suspenders SEC-020 guard (astgate) -- see SetDeathDrainCapacity's
	// identical comment above for why this direct check is needed alongside
	// DeathHandoffSince's own internal checkNotCopied.
	if err := c.deathQueue.checkNotCopied(correlationID, "DeathHandoffSince"); err != nil {
		return nil, err
	}
	return c.deathQueue.DeathHandoffSince(cursor, correlationID), nil
}

// SeedColdRecords bulk-loads cold citizen records (the harness.synth path)
// into their id-hash shards. It is a command-path mutation, not an
// exported field-set on ColdShard.
//
// BUG-666's round (TestBUG666DuplicateIDDivergence, F1 follow-up) found
// that, unlike ApplyLifeEventCommand's LifeEventBirth path, this method
// accepted a duplicate id outright: two rows landed in the same shard
// under the same id, and BUG-666's id->row index — being one entry per id
// by construction — can then only ever resolve ONE of them, so removing
// that row leaves the other permanently unreachable (invisible to
// fertility, money, and the death-realisation loop). Rejected here for the
// same reason LifeEventBirth already rejects it (ErrDuplicateCitizenID,
// AC-13/GR#16): a second row under an id already present in this batch OR
// already resident in the cold store would silently alias two logically
// distinct citizens. Checked against the shard's O(1) id->row index
// (BUG-666), so this guard costs nothing beyond the append it protects —
// no reintroduction of a per-record linear scan.
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
		// Duplicate-id guard (BUG-666 F1): a row already resident under
		// this id -- whether from an earlier record in THIS batch or a
		// prior SeedColdRecords call -- is rejected rather than silently
		// admitted as a second, unreachable-after-removal row.
		if c.shardAt(shard).rowOf(r.ID) >= 0 {
			return errs.New(ErrDuplicateCitizenID, correlationID, map[string]any{"id": r.ID, "fidelity": "cold", "path": "SeedColdRecords"})
		}
		c.shardAt(shard).append(r)
	}
	return nil
}

// SeedHouseholds bulk-registers REAL households for a bulk-seeded
// population (the harness.headless/harness.synth path, mirroring
// SeedColdRecords' own doc comment) — a pure O(len(records)) map
// registration, no ColdShard.rowOf lookup, so it stays cheap even at the
// 100,000,000-citizen scale SeedColdRecords itself already targets.
//
// # Why this exists as its own call, separate from SeedColdRecords
//
// SeedColdRecords' own ValidateColdRecord does NOT check household
// existence (unlike ValidateCitizen, the command-path validator
// LifeEventBirth/LifeEventPartner use, which rejects a non-zero Household
// that has no c.households entry — see ValidateCitizen's doc comment). So
// a bulk-seed caller can write ANY Household/Partner column values via
// SeedColdRecords with zero registration side effects — exactly the gap
// an independent destructive round proved against BUG-665's harness
// seeding: a raw ColdRecord.Household write alone made every subsequent
// birth attempt into that "household" fail ValidateCitizen's
// householdExists check (fertility.go's birthChildLocked), because no
// c.households entry backed it. Folding household registration directly
// INTO SeedColdRecords was deliberately rejected as the fix: SeedColdRecords
// is called by over a dozen existing test files in this package using
// incidental, non-partnership Household values (e.g. this package's own
// mkRecord test helper) that never intended to form real households —
// silently promoting every non-zero Household column into a live
// c.households entry would risk changing THEIR behaviour too. This method
// is additive and opt-in instead: nothing calls it except a caller that
// explicitly wants its bulk-seeded Household columns to become real,
// ValidateCitizen-visible households.
//
// Every record with Household != 0 is grouped by that value: a
// not-yet-seen Household id gets a fresh entry with this record's id as
// its first member; an already-registered one gets this record's id
// appended (AddMember) — so calling this with BOTH partners of a pair
// (sharing the same Household value, e.g. headless's own
// generateSeedPopulation pairing pass) produces exactly the two-member
// household FormHousehold itself would have built via the command path,
// without paying that path's own ColdShard.rowOf-driven cost
// (detachFromHouseholdLocked/setColdHouseholdLocked). A record with
// Household==0 (the universal "no household" sentinel) is skipped.
//
// c.nextHouseholdID is advanced past the highest id this call registers,
// so a LATER real household mint via the command path
// (ApplyLifeEventCommand(LifeEventPartner), e.g. compose's own
// formResidentHouseholds) can never collide with a bulk-seeded household
// id — the same disjoint-id-space discipline this package's citizen ids
// already enforce (FEAT-169's ErrCitizenIDNamespaceSeam), applied here to
// the separate household-id space.
func (c *CitizensAPI) SeedHouseholds(records []ColdRecord, correlationID string) error {
	if err := c.checkNotCopied(correlationID, "SeedHouseholds"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range records {
		if r.Household == 0 {
			continue
		}
		if h, ok := c.households[r.Household]; ok {
			h.AddMember(r.ID)
		} else {
			nh := Household{ID: r.Household, Members: []uint64{r.ID}}
			c.households[r.Household] = &nh
		}
		if r.Household >= c.nextHouseholdID {
			c.nextHouseholdID = r.Household + 1
		}
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
	for shard := range c.cold {
		n += c.shardAt(shard).count()
	}
	return n
}

// FertilityChildrenBorn returns the cumulative COUNT of fertility-born
// children ever minted (nextFertilityChildID — see fertility.go's
// nextFertilityChildID doc comment: it starts at 0 and increments by
// exactly 1 per minted child, so this IS the count, not an id).
// BUG-529/BUG-535: the composition root needs this to reconstruct the
// exact set of fertility-child ids it should treat as live residents for
// wage/employment/household-formation purposes ([FertilityChildIDBase+0,
// FertilityChildIDBase+FertilityChildrenBorn()-1] — BUG-541 fixed the
// caller's off-by-one that previously enumerated [+1, +count]).
// This is a LIVE read of citizens' own already-correctly-persisted counter
// (participant.go's NextFertilityChildID field), never a value the caller
// tracks or caches itself, so it stays correct across a save/LoadAt
// round-trip.
func (c *CitizensAPI) FertilityChildrenBorn(correlationID string) uint64 {
	if err := c.checkNotCopied(correlationID, "FertilityChildrenBorn"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nextFertilityChildID
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
			return errs.New(ErrDuplicateCitizenID, cmd.CorrelationID, map[string]any{"id": cmd.Citizen.ID, "fidelity": "hot", "path": "LifeEventBirth"})
		}
		if _, ok := c.coldRecord(cmd.Citizen.ID); ok {
			return errs.New(ErrDuplicateCitizenID, cmd.CorrelationID, map[string]any{"id": cmd.Citizen.ID, "fidelity": "cold", "path": "LifeEventBirth"})
		}
		r := hotToColdRecord(cmd.Citizen, districtOf(cmd.Citizen.Home))
		c.shardAt(det.ShardForEntity(cmd.Citizen.ID)).append(r)
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
		c.setColdHouseholdLocked(cmd.CitizenID, h.ID, cmd.PartnerID)
		c.setColdHouseholdLocked(cmd.PartnerID, h.ID, cmd.CitizenID)
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
		//
		// FEAT-087 (mkey feat.deathwave) inc1.5 integration fix: this is a
		// GENERIC departure command (predates the death queue -- engine.
		// attract's emigration path uses it directly, LifeEventCommand's
		// own doc), independent of the cold-pass mortality hazard draw. A
		// citizen can now be QUEUED (hazard-selected, not yet realised --
		// still an ordinary resident per ASM-581) at the moment SOME OTHER
		// departure (e.g. emigration) removes them via this path. Left
		// unreconciled, the death queue would carry a permanently STALE
		// pending entry for an id no longer resident: it would keep
		// occupying a monthly budget slot forever (never draining), and
		// the eventual Realise() that releases it would report a "death"
		// for a citizen already gone, double-counting the departure and
		// breaking AC-2's totalRealised==totalSelected conservation
		// invariant. RealiseByID force-closes the queue entry HERE (a
		// no-op if the citizen was never queued) -- it marks the selection
		// realised in the queue's own bookkeeping WITHOUT feeding
		// AdvanceDayTick's returned deaths count (only the AdvanceDayTick-
		// driven Realise call below in this file does that), so this
		// reconciliation never double-counts against compose's vitalDeaths
		// ledger; it only keeps the death queue itself honest.
		if _, queued := c.deathQueue.IsQueued(cmd.CitizenID, cmd.CorrelationID); queued {
			_ = c.deathQueue.RealiseByID(cmd.CitizenID, c.month, cmd.CorrelationID)
		}
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
			s := c.shardAt(shard)
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
// SAME TICK it is computed. Fertility mutations still land incrementally,
// one amortised shard-slice per day-tick, and dayBirths reports exactly
// that tick's own births. Mortality (FEAT-087 mkey feat.deathwave, inc1.5)
// is different since the death-queue wiring landed: a hazard-selected
// death is ENQUEUED on its shard's one scheduled day-tick but not REMOVED
// until the death queue's bounded monthly budget releases it, which this
// method does once per completed month (on the day-tick that schedules the
// LAST shard, DaysPerMonth-1 — see the realisation block below) rather
// than incrementally. The returned `deaths` is that realisation's own
// count, reported on the SAME tick the removal actually happens (the T0
// requirement still holds — it is just that most day-ticks in a month
// legitimately return deaths=0 and one returns the month's whole batch,
// instead of a same-sized trickle every tick). VitalEvents is unchanged
// and still reports the completed-month totals for any consumer that wants
// a monthly view; it is simply not the compose-side conservation seam.
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

	// BUG-663 REWORK (destructive-round REJECT on the original
	// QueuedSnapshot fix -- 127.9ms/day-tick at 1M pending, 70x worse than
	// pre-fix): NO per-day-tick snapshot is taken any more. Each shard's
	// applyMonthly now queries c.deathQueue's per-shard O(1) index directly
	// ([DeathQueue.IsQueuedInShard]) instead of a whole-queue map copy or a
	// single global-mutex dq.IsQueued call -- see deathwave.go's shardIndex
	// doc comment for why this costs neither an allocation nor cross-shard
	// lock contention: each of runShardsParallel's goroutines below is
	// pinned to exactly one shard index for the whole call, and
	// IsQueuedInShard locks only that same shard's own DeathQueue mutex.
	//
	// FEAT-087 (mkey feat.deathwave) inc1.5: applyMonthly ENQUEUES a
	// hazard-selected death into c.deathQueue rather than removing it
	// immediately -- deathQueue is safe for concurrent Enqueue from every
	// shard goroutine (it holds its own mutex plus a per-shard index);
	// Enqueue is called only for the (small) number of citizens actually
	// hazard-selected this month, not once per citizen, so this is no
	// longer the parallelism-killing bottleneck the pre-BUG-663 dq.IsQueued
	// call was.
	// BUG-664 round-2 P0 FIX: this is the ONLY place in the package where
	// shardAt/acquireShard is ever called concurrently across DIFFERENT
	// shard indices (runShardsParallel fans out c.workers goroutines,
	// c.mu.Lock only serialises this whole AdvanceDayTick call against
	// OTHER top-level API calls, not against these sibling goroutines) --
	// so this is the one call site that pins via acquireShard/
	// releaseShard rather than the plain unpinned shardAt every other
	// (strictly sequential) call site in this package still uses.
	results := runShardsParallel(c.workers, shards, func(shard int) passTotals {
		s := c.acquireShard(shard)
		defer c.releaseShard(shard)
		return s.applyMonthly(seed, month, params, func(id uint64) bool {
			return hotSet[id]
		}, c.deathQueue, shard, correlationID)
	})

	// Sum in ascending shard order (deterministic merge), ignoring slots for
	// shards not scheduled this day. tot.selected is informational only
	// (this call's new hazard SELECTIONS, i.e. enqueues) -- it is not a
	// population change and must never feed curMonthDeaths or this call's
	// returned deaths count; see passTotals' doc comment.
	var tot passTotals
	for _, t := range results {
		tot = tot.add(t)
	}

	// FEAT-087 inc1.5/inc2 — REALISATION (AC-1/AC-2/AC-6, the live
	// cliff-kill and the emergency major-event release): drain the death
	// queue by at most mortalityCfg.MonthlyDeathBudget() of its oldest
	// entries, but only once every scheduled shard has had its one
	// day-tick this month (ColdPassSchedule assigns shard 255 -- the last
	// -- to day 29 = DaysPerMonth-1; ascending shard order across the whole
	// month covers every shard by then, doc.go's "amortised cold pass"
	// section). Realising exactly once per COMPLETED month, not per
	// day-tick, keeps "monthly budget" a literal monthly quantity: every
	// hazard selection from every shard this month has already been
	// enqueued by the time Realise runs, so a same-birthMonth cohort's
	// cliff is bounded to at most the budget in the SAME month it was
	// selected, never smeared thinner by re-deriving a daily fraction.
	//
	// inc2 (mkey feat.deathwave, AC-6/AC-7/AC-8): BEFORE realising, declare
	// whether this completed month is a weather emergency
	// (weatheremergency.go's IsWeatherEmergency, consumed through the
	// registered feat.deathwave -> engine.season edge via c.season -- a nil
	// c.season, i.e. a CitizensAPI never wired via SetSeason, always
	// declares false and behaves exactly as inc1/inc1.5). A declared
	// emergency SUSPENDS the ordinary budget for this one release, producing
	// the major non-smoothed death event AC-6 requires; it never touches
	// the hazard SELECTIONS already enqueued above (AC-8 -- selection
	// happened unconditionally in applyMonthly, entirely independent of
	// season/emergency state).
	//
	// inc3 (AC-9/AC-10/AC-11): the release itself now goes through
	// [DeathQueue.RealiseDrained] rather than [EmergencyRealise] directly --
	// same budget/emergency logic (RealiseDrained's own doc), PLUS ASM-580's
	// injected drain-capacity knob and the AC-9/AC-10 (citizenId, deathMonth,
	// emergencyFlag) handoff stream FEAT-088 will drain. c.deathQueue's
	// drain capacity defaults to nil (unlimited) until a consumer calls
	// [CitizensAPI.SetDeathDrainCapacity], so this call is a behavioural
	// no-op for every world with no FEAT-088 consumer wired yet --
	// EmergencyRealise itself is left untouched (still directly tested) as
	// the reference implementation this wiring must stay byte-identical to.
	var realisedDeaths int
	if c.dayTick == DaysPerMonth-1 {
		emergency, emErr := IsWeatherEmergency(c.season, month, c.mortalityCfg, correlationID)
		if emErr != nil {
			// A season-curve lookup failure (e.g. a negative month index,
			// structurally unreachable here since month only ever
			// increases from 0) must never be silently treated as "no
			// emergency" and swallowed -- surface it rather than guess.
			return 0, 0, emErr
		}
		realisedHandoff := c.deathQueue.RealiseDrained(c.mortalityCfg, emergency, month, correlationID)
		realisedDeaths = len(realisedHandoff)
		realised := make([]uint64, len(realisedHandoff))
		for i, rd := range realisedHandoff {
			realised[i] = rd.CitizenID
		}

		// BUG-369/BUG-270 parity, at REALISATION time (not selection time):
		// dissolution fires only now, because ASM-581 keeps a queued citizen
		// a full living household member up to the instant they are
		// actually removed -- so the household/partner snapshot MUST be
		// read HERE, immediately before removeAt, never cached from the
		// (possibly much earlier) month the citizen was selected in. Applied
		// sequentially (this whole block runs under c.mu, after the
		// parallel shard pass has completed), in the queue's own FIFO
		// realisation order -- itself a pure function of queue contents,
		// never of Enqueue call order (AC-4/AC-15), so this dissolution
		// sequence is deterministic and worker-count invariant too.
		for _, id := range realised {
			shard := det.ShardForEntity(id)
			row := c.shardAt(shard).rowOf(id)
			if row < 0 {
				// Structurally unreachable: a realised id was Enqueue'd from
				// this exact shard (det.ShardForEntity is a pure function of
				// id) and, being queued, was never removed until this very
				// realisation loop -- so it must still be resident. Never
				// silently drop a real death's dissolution; skip defensively
				// rather than index a negative row, but this indicates a
				// genuine invariant violation if it is ever hit.
				continue
			}
			shardStore := c.shardAt(shard)
			d := coldDeath{
				citizenID:   id,
				householdID: uint64(shardStore.households[row]),
				partnerID:   uint64(shardStore.partners[row]),
			}
			// BUG-270: an ELEVATED citizen's death must also drop them from
			// the hot elevation cache, exactly as LifeEventDeath's
			// delete(c.hot,...) does -- a no-op for a citizen who was cold.
			// Done before the household unwiring, mirroring LifeEventDeath's
			// order (the survivor, not the departed id, is the one
			// removeHouseholdMemberLocked reads).
			delete(c.hot, d.citizenID)
			shardStore.removeAt(row)
			c.removeHouseholdMemberLocked(d.citizenID, d.householdID, d.partnerID)
		}
	}
	c.curMonthDeaths += realisedDeaths

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
	return dayBirths, realisedDeaths, nil
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
		s := c.shardAt(shard)
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
	for shard := range c.cold {
		s := c.shardAt(shard)
		for i := 0; i < s.count(); i++ {
			out = append(out, s.recordAt(i))
		}
	}
	return out
}

func (c *CitizensAPI) coldRecord(id uint64) (ColdRecord, bool) {
	shard := det.ShardForEntity(id)
	s := c.shardAt(shard)
	if row := s.rowOf(id); row >= 0 {
		return s.recordAt(row), true
	}
	return ColdRecord{}, false
}

func (c *CitizensAPI) removeColdLocked(id uint64) {
	shard := det.ShardForEntity(id)
	s := c.shardAt(shard)
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
	c.setColdHouseholdLocked(citizenID, householdID, 0)
}

// setColdHouseholdLocked updates a citizen's household/partner columns in
// the cold store (the single source of truth). A partnering event must
// reach the cold record, not only the elevation cache. householdID/partnerID
// are full-width uint64 (widened from uint32 — births-unblock lane,
// 2026-09-02): narrowing them here was the births blocker (see coldshard.go's
// ColdShard doc comment).
func (c *CitizensAPI) setColdHouseholdLocked(citizenID uint64, householdID, partnerID uint64) {
	shard := det.ShardForEntity(citizenID)
	s := c.shardAt(shard)
	if row := s.rowOf(citizenID); row >= 0 {
		s.households[row] = householdID
		s.partners[row] = partnerID
	}
}

// coldRowLocked returns the (shard, row) of a citizen's cold record.
func (c *CitizensAPI) coldRowLocked(id uint64) (int, int, bool) {
	shard := det.ShardForEntity(id)
	row := c.shardAt(shard).rowOf(id)
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
	s := c.shardAt(shard)
	if row := s.rowOf(id); row >= 0 {
		fn(s, row)
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

// coldParamsLocked measures this month's ColdPassParams from the A7
// stratified sample WITHOUT first materialising the whole population into
// one []ColdRecord slice (BUG-663 §3.5 of the 100M proving plan: the old
// allColdRecordsLocked()-then-BuildStratifiedSample shape allocated a
// >12GB transient slice, once per month, at 100M). Instead it streams the
// population through stratifiedSampleBuilder ONE SHARD AT A TIME, reusing
// a single per-shard scratch buffer across all 256 shards — peak transient
// allocation drops from O(N) to O(N/numColdShards), ~256x smaller, with
// byte-identical output (stratifiedSampleBuilder's grouping is
// order-independent and build() fully re-sorts, exactly as
// BuildStratifiedSample always has — see that type's doc comment).
func (c *CitizensAPI) coldParamsLocked(correlationID string) ColdPassParams {
	b := newStratifiedSampleBuilder(c.month, c.seed, 1)
	var buf []ColdRecord
	for shard := range c.cold {
		s := c.shardAt(shard)
		n := s.count()
		if cap(buf) < n {
			buf = make([]ColdRecord, n)
		}
		buf = buf[:n]
		for i := 0; i < n; i++ {
			buf[i] = s.recordAt(i)
		}
		b.addRecords(buf)
	}
	return DeriveColdPassParams(b.build())
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
		Household:       c.Household,
		Partner:         c.Partner,
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
