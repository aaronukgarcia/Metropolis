package freight

import (
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileFreight is data/freight.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir).
const fileFreight = "freight.json"

// stageState is the runtime per-stage record: the loaded config plus the
// stage's firm registration (AC-4 — currently the zero value while
// engine.firms is unbuilt, see doc.go).
type stageState struct {
	cfg  stageConfig
	firm Firm
}

// siteState is the runtime per-site record: the loaded config plus the
// site's per-commodity stock level (tonnes), the only persistent tonnage
// store (AC-6).
type siteState struct {
	cfg   siteConfig
	stock map[Commodity]int64
}

// movement is an in-transit freight movement (AC-7): tonnes en route from
// a source to a destination, resolved when its arrival tick is reached.
type movement struct {
	ID            uint64
	Commodity     Commodity
	Tonnes        int64
	From          SiteType
	To            SiteType
	Mode          Mode
	DepartureTick int64
	ArrivalTick   int64
}

// FreightAPI is code.json's "engine.freight" inbound interface (GUID
// 3ce6d7c8-1aa6-46f4-94d1-07a20cf656e3): the freight harbour, tonnes
// accounting and production-chain model — "chains from data; stages
// register as firms; tonnes conserved (invariant)". It exposes a query
// surface (port capacity/customs, chain stages, storage sites,
// balance-of-trade, conservation account) and a command surface (import,
// export, ship, store, advance-tick), and consumes engine.market and
// engine.logistics through their registered interfaces only (GR#20).
//
// The zero value is not usable; construct via [Load] or [LoadDefault]. A
// *FreightAPI is safe for concurrent use (AC-16): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.finance's
// FinanceAPI and engine.logistics' LogisticsAPI).
type FreightAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           config
	market        *market.MarketAPI
	logistics     *logistics.LogisticsAPI
	stages        map[StageID]*stageState
	sites         map[SiteType]*siteState

	tick int64

	// Per-tick tonnage ledgers (AC-10's independently-sourced terms),
	// reset at the end of each AdvanceTick. produced/consumed come from
	// the stage loop; exported/imported come from the departure/arrival
	// command paths; inTransitDelta is the net in-transit change
	// (Ship departures minus arrivals); customsDemanded accumulates
	// import+export tonnage.
	produced        map[Commodity]int64
	consumed        map[Commodity]int64
	imported        map[Commodity]int64
	exported        map[Commodity]int64
	inTransitDelta  map[Commodity]int64
	customsDemanded int64

	// storageOpening is the per-commodity total stock snapshot taken at the
	// end of the previous tick — the "opening" for the current tick's
	// StorageDelta term.
	storageOpening map[Commodity]int64

	// lastAccount is the conservation account captured at the end of the
	// most recent AdvanceTick, before the ledgers reset (AC-10).
	lastAccount ConservationAccount

	// movements is the in-transit ledger (AC-7/AC-10's InTransitDelta).
	movements      []movement
	nextMovementID uint64

	self atomic.Pointer[FreightAPI]
}

// Load reads and validates data/freight.json, data/market.json and
// data/logistics.json from dir, registers every chain stage and provisions
// every storage site, and returns a ready *FreightAPI. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every failure is a registry-sourced *errs.E — never a
// silent default substitution, never a panic.
func Load(dir, correlationID string) (*FreightAPI, error) {
	cfg, err := LoadConfig(filepath.Join(dir, fileFreight), correlationID)
	if err != nil {
		return nil, err
	}

	// Registered outbound edges (GR#20): engine.market for pricing/
	// availability (AC-8) and engine.logistics for the movement capacity
	// model (AC-7). engine.logistics.Load constructs its own *MarketAPI
	// internally, but freight needs its own reference for direct pricing,
	// so market.Load is called explicitly as well (a second stateless load
	// of data/market.json is harmless).
	marketAPI, err := market.Load(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrFreightDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	logisticsAPI, err := logistics.Load(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrFreightDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	api := &FreightAPI{
		correlationID: correlationID,
		cfg:           cfg,
		market:        marketAPI,
		logistics:     logisticsAPI,
		stages:        make(map[StageID]*stageState, len(cfg.stageConfigs)),
		sites:         make(map[SiteType]*siteState, len(cfg.Sites)),
	}
	api.self.Store(api) // armed exactly once, before api is returned (SEC-020)

	for _, sc := range cfg.stageConfigs {
		api.stages[sc.ID] = &stageState{cfg: sc}
	}
	for _, st := range allSiteTypes {
		api.sites[st] = &siteState{cfg: cfg.Sites[st], stock: make(map[Commodity]int64)}
	}
	api.resetLedgersLocked()
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*FreightAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *FreightAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — so it is
// safe to run before mu is ever touched.
func (f *FreightAPI) checkNotCopied(method string) error {
	if f.self.Load() != f {
		return errs.New(ErrCopiedValue, f.correlationID, map[string]any{"method": method})
	}
	return nil
}

// resetLedgersLocked zeroes the per-tick ledgers and snapshots the opening
// storage. The caller holds f.mu.
func (f *FreightAPI) resetLedgersLocked() {
	f.produced = make(map[Commodity]int64)
	f.consumed = make(map[Commodity]int64)
	f.imported = make(map[Commodity]int64)
	f.exported = make(map[Commodity]int64)
	f.inTransitDelta = make(map[Commodity]int64)
	f.customsDemanded = 0
	f.storageOpening = f.totalStockLocked()
}

// totalStockLocked returns the per-commodity total tonnes held across all
// storage sites. The caller holds f.mu.
func (f *FreightAPI) totalStockLocked() map[Commodity]int64 {
	total := make(map[Commodity]int64)
	for _, st := range allSiteTypes {
		site := f.sites[st]
		for c, t := range site.stock {
			total[c] = num.SatAdd(total[c], t)
		}
	}
	return total
}

// Tick returns the current daily-tick index.
func (f *FreightAPI) Tick() int64 {
	if err := f.checkNotCopied("Tick"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.tick
}

// AdvanceTick runs one daily tick of the freight model (AC-14's
// deterministic function of (tick, prior state, commands)):
//
//  1. resolve in-transit movements whose ArrivalTick is due;
//  2. run every chain stage in data order, producing into and consuming
//     from a this-tick pool (input availability bounds output, AC-5);
//  3. route the leftover pool into each commodity's canonical storage site;
//  4. compute the StorageDelta term from the storage accessor.
//
// The per-tick ledgers (produced/consumed/imported/exported) and the
// in-transit delta accumulate across the commands issued since the
// previous AdvanceTick; [ConservationAccount] exposes them so AC-10's
// identity can be checked. AdvanceTick itself never mutates the wall clock
// (AC-15) and iterates maps in sorted order (GR#21).
func (f *FreightAPI) AdvanceTick() error {
	if err := f.checkNotCopied("AdvanceTick"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advanceTickLocked()
	return nil
}

func (f *FreightAPI) advanceTickLocked() {
	f.resolveArrivalsLocked()
	f.runStagesLocked()
	// Capture the conservation account BEFORE resetLedgersLocked snapshots
	// the next opening — the StorageDelta term is closing - opening, and
	// opening is still the previous tick's snapshot here (AC-10).
	f.lastAccount = f.computeAccountLocked()
	f.resetLedgersLocked()
	f.tick++
}

// resolveArrivalsLocked delivers every movement whose ArrivalTick has been
// reached, in ascending movement ID order (deterministic), recording the
// delivered tonnes against the destination site's stock and netting them
// out of the in-transit delta (AC-10). The caller holds f.mu.
func (f *FreightAPI) resolveArrivalsLocked() {
	if len(f.movements) == 0 {
		return
	}
	kept := make([]movement, 0, len(f.movements))
	for _, m := range f.movements {
		if m.ArrivalTick <= f.tick {
			f.sites[m.To].stock[m.Commodity] = num.SatAdd(f.sites[m.To].stock[m.Commodity], m.Tonnes)
			f.inTransitDelta[m.Commodity] = num.SatSub(f.inTransitDelta[m.Commodity], m.Tonnes)
			continue
		}
		kept = append(kept, m)
	}
	f.movements = kept
}

// runStagesLocked runs every stage in data order (already topological),
// building a this-tick pool of produced tonnes that downstream stages draw
// from. The caller holds f.mu.
func (f *FreightAPI) runStagesLocked() {
	pool := make(map[Commodity]int64)
	for _, sc := range f.cfg.stageConfigs {
		scale := f.stageScaleLocked(sc, pool)
		for _, in := range sc.Inputs {
			consumedTons := scaleTonnes(in.TonnesPerDay, scale)
			pool[in.Commodity] = num.SatSub(pool[in.Commodity], consumedTons)
			f.consumed[in.Commodity] = num.SatAdd(f.consumed[in.Commodity], consumedTons)
		}
		for _, out := range sc.Outputs {
			producedTons := scaleTonnes(out.TonnesPerDay, scale)
			pool[out.Commodity] = num.SatAdd(pool[out.Commodity], producedTons)
			f.produced[out.Commodity] = num.SatAdd(f.produced[out.Commodity], producedTons)
		}
	}

	// Route the leftover pool (this tick's produced-but-unconsumed tonnes)
	// into each commodity's canonical storage site, in sorted commodity
	// order for determinism (GR#21).
	for _, c := range sortedCommodities(pool) {
		leftover := pool[c]
		if leftover == 0 {
			continue
		}
		site := f.cfg.canonicalSite[f.cfg.commodities[c].StorageClass]
		f.sites[site].stock[c] = num.SatAdd(f.sites[site].stock[c], leftover)
	}
}

// stageScaleLocked returns the input-availability scale in [0,1] for a
// stage given this tick's pool: 1 for a primary producer (no inputs),
// otherwise the minimum over inputs of available/required, floored at 0.
// The caller holds f.mu.
func (f *FreightAPI) stageScaleLocked(sc stageConfig, pool map[Commodity]int64) int64 {
	if len(sc.Inputs) == 0 {
		return 1_000_000 // full scale (fixed-point ×1e6), matching scaleTonnes
	}
	// scale is carried as a fixed-point fraction (×1e6) so the
	// availability bound is exact under int64 arithmetic (GR#16): the
	// documented output falls proportionally with input availability.
	scale := int64(1_000_000)
	for _, in := range sc.Inputs {
		available := pool[in.Commodity]
		if available <= 0 {
			return 0
		}
		if available < in.TonnesPerDay {
			// available/required in fixed point, floor.
			fraction := num.ClampInt64FromFloat(float64(available) / float64(in.TonnesPerDay) * 1e6)
			if fraction < scale {
				scale = fraction
			}
		}
		// available >= required: this input does not constrain (scale 1).
	}
	if scale > 1_000_000 {
		scale = 1_000_000
	}
	if scale < 0 {
		scale = 0
	}
	return scale
}

// scaleTonnes scales rate by the fixed-point scale (×1e6) and floors,
// so a constrained input produces a proportionally reduced output (AC-5).
func scaleTonnes(rate, scale int64) int64 {
	if scale >= 1_000_000 {
		return rate
	}
	if scale <= 0 {
		return 0
	}
	return num.ClampInt64FromFloat(float64(rate) * float64(scale) / 1e6)
}

// sortedCommodities returns m's keys in ascending order (deterministic map
// iteration, GR#21).
func sortedCommodities(m map[Commodity]int64) []Commodity {
	keys := make([]Commodity, 0, len(m))
	for c := range m {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
