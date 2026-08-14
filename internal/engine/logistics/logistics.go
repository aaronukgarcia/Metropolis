package logistics

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// BufferPolicy is US-10/AC-3's per-commodity, player-tunable
// replenishment safety-buffer tier. The exact multipliers are balance
// data (data/logistics.json's bufferPolicies map), never Go literals
// (GR#15); these constants are the two tier NAMES the data file must
// define, not their numeric values.
type BufferPolicy string

const (
	// BufferLean is the smaller-buffer tier: lower holding cost, higher
	// stockout risk (AC-3).
	BufferLean BufferPolicy = "lean"
	// BufferFat is the larger-buffer tier: higher holding cost, lower
	// stockout risk (AC-3).
	BufferFat BufferPolicy = "fat"
)

// ConsumerClass tags a [LogisticsAPI.Draw] so the shortfall hook
// (AC-10/US-7) can route the event to the right downstream consumer —
// satisfaction/health for households, production-stoppage for firms,
// construction-stall for engine.build (AC-8/US-8). It is a typed
// category carried on the event, never a per-class code path in this
// package.
type ConsumerClass string

const (
	ConsumerHousehold    ConsumerClass = "household"
	ConsumerFirm         ConsumerClass = "firm"
	ConsumerService      ConsumerClass = "service"
	ConsumerConstruction ConsumerClass = "construction"
)

// Stock is the read-only snapshot of one (district, commodity) local
// stock record (AC-2): a shelf with a capacity, a per-unit holding cost,
// and a per-commodity shelf life (0 = legal non-perishable value, not an
// error), plus the current level and the active buffer policy. It is the
// value [LogisticsAPI.Stock] and [LogisticsAPI.Provision] return — the
// ONLY exported view of the package's internal, unexported stock state
// (AC-1).
type Stock struct {
	District     string
	Commodity    market.CommodityType
	Level        int64
	Capacity     int64
	HoldingCost  int64 // micro-pounds per unit per tick (M0-ENG §1.2)
	ShelfLife    int64 // ticks; 0 = non-perishable
	BufferPolicy BufferPolicy
}

// DrawResult is [LogisticsAPI.Draw]'s return value: what was requested,
// what was actually fulfilled from the shelf, and the shortfall
// (requested - fulfilled, never negative, never a boolean — AC-2/AC-9).
type DrawResult struct {
	Requested int64
	Fulfilled int64
	Shortfall int64
}

// Delivery is [LogisticsAPI.Deliverable]'s return value: the requested
// quantity, the effective per-tick throughput after the shortfall factor,
// the quantity actually deliverable this tick, and the shortfall
// (requested - delivered) — the "how much of commodity X can actually be
// delivered to a district this tick" answer behind a single coarse
// capacity/shortfall number.
type Delivery struct {
	Requested  int64
	Throughput int64
	Delivered  int64
	Shortfall  int64
}

// ShortfallEvent is the typed, subscribed shortfall delivery (AC-10/
// US-7): commodity, magnitude, and consumer-class, fired exactly once
// per [LogisticsAPI.Draw] that runs short. Downstream modules attach via
// [LogisticsAPI.SubscribeShortfalls]; this package never polls its own
// events and never asks a consumer to.
type ShortfallEvent struct {
	District      string
	Commodity     market.CommodityType
	ConsumerClass ConsumerClass
	Requested     int64
	Fulfilled     int64
	Shortfall     int64
}

// ShortfallHandler is the subscription callback type for
// [LogisticsAPI.SubscribeShortfalls].
type ShortfallHandler func(ShortfallEvent)

// requiredCommodities is every commodity key this package's baseline loop
// needs present in data/logistics.json's "commodities" map — the nine §6
// commodities, sourced from engine.market's own exported constants (the
// single source of truth for the commodity identities, GR#3) rather than
// re-spelled here as string literals. Checked explicitly at Load time
// (AC-14) because foundation.data's schema validation has no notion of
// which commodity keys a particular consumer requires.
var requiredCommodities = []market.CommodityType{
	market.Water, market.Power, market.Gas, market.FoodStaples, market.FoodFresh,
	market.Fuel, market.ConstructionMaterials, market.ConsumerGoods, market.Waste,
}

// stockState is the unexported mutable per-(district, commodity) stock
// record (AC-1): the only way another package reaches a stock's level is
// through LogisticsAPI's exported methods — this struct (and every other
// unexported type in this package) is never part of the exported surface,
// so a consumer can never write to a stock/order-book/ledger field
// directly.
type stockState struct {
	level        int64
	capacity     int64
	holdingCost  int64
	shelfLife    int64
	bufferPolicy BufferPolicy
}

// commodityConfig is the unexported, Load-populated per-commodity coarse
// delivery model, copied out of data.LogisticsFile into a form the query
// methods use without touching the shared file struct.
type commodityConfig struct {
	throughput          int64
	shortfallFactor     float64
	shelfLifeTicks      int64
	holdingCost         int64
	defaultBufferPolicy BufferPolicy
}

// stockKey is the unexported (district, commodity) identity of one
// stock record.
type stockKey struct {
	district  string
	commodity market.CommodityType
}

// LogisticsAPI is code.json's "engine.logistics" inbound interface
// (GUID 26abfa77-3b11-43d0-a631-539a04e3a5f8): the coarse per-district/
// per-commodity delivery-and-shortfall model — "order book + movement
// scheduler; queue states subscribable per junction" at its full depth,
// reduced here to the STUB-FOR-BASELINE subset (FEAT-083): a single
// deterministic throughput/shortfall number per (district, commodity),
// plus a capacity-bounded local stock shelf. See doc.go's "Stub-for-
// baseline depth" section for exactly what is deferred.
//
// The zero value is not usable; construct via [Load] or [LoadDefault].
// A *LogisticsAPI is safe for concurrent use (AC-17): every mutable field
// is guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.finance's
// FinanceAPI and engine.world's World).
type LogisticsAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           data.LogisticsFile
	market        *market.MarketAPI
	stocks        map[stockKey]*stockState
	subs          []ShortfallHandler

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller. A struct copy (`cp := *l`)
	// gets its own independently-zeroed mu but ALIASES stocks/subs/cfg and
	// keeps the ORIGINAL's self pointer, so checkNotCopied can detect the
	// copy with a single lock-free atomic load — mirroring engine.world's
	// World.self (ASM-427) and engine.finance's FinanceAPI.self.
	self atomic.Pointer[LogisticsAPI]
}

// Load reads and validates data/logistics.json (via foundation/data.
// LoadLogisticsFile) and data/market.json (via engine.market.Load, the
// registered code.json outbound edge — AC-12), checks that all nine §6
// commodities are present in logistics.json, and returns a ready
// *LogisticsAPI. correlationID is attached to every error this call (and
// the returned API's methods) construct (GR#1). Every failure is a
// registry-sourced *errs.E — never a silent default substitution, never
// a panic (AC-14).
func Load(dir, correlationID string) (*LogisticsAPI, error) {
	lf, err := data.LoadLogisticsFile(dir, correlationID)
	if err != nil {
		// MET-G400's registered template has a "{cause}" placeholder —
		// populate it from the wrapped error's own text (BUG-099/BUG-191's
		// shared weakness class) so the rendered message names the real
		// failure instead of leaving the literal "{cause}".
		return nil, errs.Wrap(ErrLogisticsDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	marketAPI, err := market.Load(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrLogisticsDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	// Completeness: all nine §6 commodities must be present (sorted,
	// deterministic order — requiredCommodities is already an ordered
	// slice, not a map).
	for _, c := range requiredCommodities {
		if _, ok := lf.Commodities[string(c)]; !ok {
			return nil, errs.New(ErrMissingCommodity, correlationID, map[string]any{
				"commodity": string(c),
				"dir":       dir,
			})
		}
	}

	api := &LogisticsAPI{
		correlationID: correlationID,
		cfg:           lf,
		market:        marketAPI,
		stocks:        make(map[stockKey]*stockState),
	}
	// Armed exactly once, before api is returned to any caller (SEC-020).
	api.self.Store(api)
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*LogisticsAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *LogisticsAPI
// (SEC-020 family, mirroring engine.finance's FinanceAPI.checkNotCopied).
// Lock-free — a single atomic.Pointer.Load — and therefore safe to run
// before mu is ever touched.
func (l *LogisticsAPI) checkNotCopied(method string) error {
	if l.self.Load() != l {
		return errs.New(ErrCopiedValue, l.correlationID, map[string]any{"method": method})
	}
	return nil
}

// requireCommodity validates that c is one of this package's registered
// commodities (present in the loaded data/logistics.json), returning
// ErrUnknownCommodity otherwise — never a panic, never a silently-created
// zero-value entry (AC-13).
func (l *LogisticsAPI) requireCommodity(c market.CommodityType) error {
	if err := l.checkNotCopied("requireCommodity"); err != nil {
		return err
	}
	if _, ok := l.cfg.Commodities[string(c)]; !ok {
		return errs.New(ErrUnknownCommodity, l.correlationID, map[string]any{
			"commodity": string(c),
		})
	}
	return nil
}

// commodityConfigFor resolves c's loaded coarse model into a
// commodityConfig, after [requireCommodity] has validated it is
// registered.
func (l *LogisticsAPI) commodityConfigFor(c market.CommodityType) (commodityConfig, error) {
	if err := l.checkNotCopied("commodityConfigFor"); err != nil {
		return commodityConfig{}, err
	}
	if err := l.requireCommodity(c); err != nil {
		return commodityConfig{}, err
	}
	rec := l.cfg.Commodities[string(c)]
	return commodityConfig{
		throughput:          rec.Throughput,
		shortfallFactor:     rec.ShortfallFactor,
		shelfLifeTicks:      rec.ShelfLifeTicks,
		holdingCost:         rec.HoldingCostMicropoundsPerUnitPerTick,
		defaultBufferPolicy: BufferPolicy(rec.DefaultBufferPolicy),
	}, nil
}

// snapshotStock builds the exported Stock view from an internal
// stockState. A free function (not a method) — it touches no LogisticsAPI
// state, so it carries no copy-guard concern.
func snapshotStock(district string, c market.CommodityType, st *stockState) Stock {
	return Stock{
		District:     district,
		Commodity:    c,
		Level:        st.level,
		Capacity:     st.capacity,
		HoldingCost:  st.holdingCost,
		ShelfLife:    st.shelfLife,
		BufferPolicy: st.bufferPolicy,
	}
}

// Provision creates (or resets) the local stock record for
// (district, commodity) with the given shelf capacity and initial level,
// seeding its holding cost, shelf life, and default buffer policy from
// data/logistics.json. initialLevel is clamped to [0, capacity]; a
// negative capacity or initial level is treated as zero (the same
// "negative means zero" stance MarketAPI.Availability takes). Returns the
// resulting [Stock] snapshot.
func (l *LogisticsAPI) Provision(district string, c market.CommodityType, capacity, initialLevel int64) (Stock, error) {
	if err := l.checkNotCopied("Provision"); err != nil {
		return Stock{}, err
	}
	cfg, err := l.commodityConfigFor(c)
	if err != nil {
		return Stock{}, err
	}
	if capacity < 0 {
		capacity = 0
	}
	if initialLevel < 0 {
		initialLevel = 0
	}
	if initialLevel > capacity {
		initialLevel = capacity
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	st := &stockState{
		level:        initialLevel,
		capacity:     capacity,
		holdingCost:  cfg.holdingCost,
		shelfLife:    cfg.shelfLifeTicks,
		bufferPolicy: cfg.defaultBufferPolicy,
	}
	l.stocks[stockKey{district: district, commodity: c}] = st
	return snapshotStock(district, c, st), nil
}

// Stock returns the read-only snapshot of the (district, commodity)
// stock record (AC-2/AC-5: queryable without mutating it). Returns
// ErrUnknownCommodity for an unregistered commodity and ErrUnknownDistrict
// for a district never Provisioned.
func (l *LogisticsAPI) Stock(district string, c market.CommodityType) (Stock, error) {
	if err := l.checkNotCopied("Stock"); err != nil {
		return Stock{}, err
	}
	if err := l.requireCommodity(c); err != nil {
		return Stock{}, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	st, ok := l.stocks[stockKey{district: district, commodity: c}]
	if !ok {
		return Stock{}, errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	return snapshotStock(district, c, st), nil
}

// Draw takes requested units of c from the (district, commodity) shelf on
// behalf of a consumer class (AC-2/AC-8: construction draws through the
// exact same mechanism as any household/firm/service — there is no
// bespoke materials path). It returns the partial/complete fill and the
// shortfall (requested - fulfilled). When the shortfall is nonzero it
// fires exactly one [ShortfallEvent] to every subscribed handler
// (AC-9/AC-10).
func (l *LogisticsAPI) Draw(district string, c market.CommodityType, requested int64, class ConsumerClass) (DrawResult, error) {
	if err := l.checkNotCopied("Draw"); err != nil {
		return DrawResult{}, err
	}
	if err := l.requireCommodity(c); err != nil {
		return DrawResult{}, err
	}
	if requested < 0 {
		requested = 0
	}

	l.mu.Lock()
	st, ok := l.stocks[stockKey{district: district, commodity: c}]
	if !ok {
		l.mu.Unlock()
		return DrawResult{}, errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	fulfilled := requested
	if fulfilled > st.level {
		fulfilled = st.level
	}
	st.level -= fulfilled
	shortfall := requested - fulfilled
	l.mu.Unlock()

	if shortfall > 0 {
		l.fireShortfall(ShortfallEvent{
			District:      district,
			Commodity:     c,
			ConsumerClass: class,
			Requested:     requested,
			Fulfilled:     fulfilled,
			Shortfall:     shortfall,
		})
	}

	return DrawResult{Requested: requested, Fulfilled: fulfilled, Shortfall: shortfall}, nil
}

// Restock adds quantity units to the (district, commodity) shelf, capped
// at the stock's capacity, and returns the number actually added (a
// partial restock when the shelf cannot hold it all — never a silent
// overflow of the capacity bound). Returns ErrUnknownCommodity or
// ErrUnknownDistrict as applicable.
func (l *LogisticsAPI) Restock(district string, c market.CommodityType, quantity int64) (int64, error) {
	if err := l.checkNotCopied("Restock"); err != nil {
		return 0, err
	}
	if err := l.requireCommodity(c); err != nil {
		return 0, err
	}
	if quantity < 0 {
		quantity = 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.stocks[stockKey{district: district, commodity: c}]
	if !ok {
		return 0, errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	added := quantity
	if headroom := st.capacity - st.level; added > headroom {
		added = headroom
	}
	if added < 0 {
		added = 0
	}
	st.level += added
	return added, nil
}

// Deliverable answers "how much of commodity c can actually be delivered
// to district this tick" — the coarse capacity/shortfall number the
// Baseline One loop needs (engine.market's live availability resolution
// and engine.build's materials flow). It is a pure query (no mutation):
//
//	effectiveThroughput = floor(min(localThroughput, marketCeiling) * shortfallFactor)
//	delivered           = min(requested, effectiveThroughput)
//	shortfall           = requested - delivered
//
// where localThroughput and shortfallFactor come from data/logistics.json
// (GR#15) and marketCeiling is the commodity's configured import-capacity
// ceiling, read through the registered engine.market edge
// ([market.MarketAPI.Availability], AC-12) so the two capacity concepts
// stay related-but-distinct. The district is accepted for API uniformity
// with [LogisticsAPI.Draw] and future per-district throughput; at stub
// depth throughput is commodity-global (a documented coarse
// simplification — see doc.go).
func (l *LogisticsAPI) Deliverable(district string, c market.CommodityType, requested int64) (Delivery, error) {
	if err := l.checkNotCopied("Deliverable"); err != nil {
		return Delivery{}, err
	}
	if district == "" {
		return Delivery{}, errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	cfg, err := l.commodityConfigFor(c)
	if err != nil {
		return Delivery{}, err
	}
	if requested < 0 {
		requested = 0
	}

	// Market boundary (AC-12): read the static import-capacity ceiling
	// through the registered interface; never write any market state.
	avail, err := l.market.Availability(c, requested)
	if err != nil {
		return Delivery{}, err
	}
	localCap := cfg.throughput
	if avail.CapacityCeiling < localCap {
		localCap = avail.CapacityCeiling
	}

	effective := num.ClampInt64FromFloat(math.Floor(float64(localCap) * cfg.shortfallFactor))
	delivered := requested
	if delivered > effective {
		delivered = effective
	}
	shortfall := requested - delivered

	return Delivery{
		Requested:  requested,
		Throughput: effective,
		Delivered:  delivered,
		Shortfall:  shortfall,
	}, nil
}

// SetBufferPolicy sets the (district, commodity) stock's safety-buffer
// tier to p (US-10/AC-3). Rejects an unknown policy with
// ErrInvalidBufferPolicy and an unprovisioned district/commodity as
// documented on the other methods.
func (l *LogisticsAPI) SetBufferPolicy(district string, c market.CommodityType, p BufferPolicy) error {
	if err := l.checkNotCopied("SetBufferPolicy"); err != nil {
		return err
	}
	if err := l.requireCommodity(c); err != nil {
		return err
	}
	if _, ok := l.cfg.BufferPolicies[string(p)]; !ok {
		return errs.New(ErrInvalidBufferPolicy, l.correlationID, map[string]any{
			"policy": string(p),
		})
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.stocks[stockKey{district: district, commodity: c}]
	if !ok {
		return errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	st.bufferPolicy = p
	return nil
}

// OrderSize sizes a replenishment order from a forecast demand plus the
// (district, commodity) stock's current safety-buffer tier (AC-3):
//
//	order = ceil(forecastDemand * (1 + safetyBuffer))
//
// where safetyBuffer is read from data/logistics.json's bufferPolicies
// map for the stock's active policy. A "fat" policy therefore produces a
// strictly larger order than "lean" for identical forecast demand.
func (l *LogisticsAPI) OrderSize(district string, c market.CommodityType, forecastDemand int64) (int64, error) {
	if err := l.checkNotCopied("OrderSize"); err != nil {
		return 0, err
	}
	if err := l.requireCommodity(c); err != nil {
		return 0, err
	}
	if forecastDemand < 0 {
		forecastDemand = 0
	}

	l.mu.RLock()
	st, ok := l.stocks[stockKey{district: district, commodity: c}]
	if !ok {
		l.mu.RUnlock()
		return 0, errs.New(ErrUnknownDistrict, l.correlationID, map[string]any{
			"district":  district,
			"commodity": string(c),
		})
	}
	policy := st.bufferPolicy
	l.mu.RUnlock()

	mult := l.cfg.BufferPolicies[string(policy)].SafetyBuffer
	return num.ClampInt64FromFloat(math.Ceil(float64(forecastDemand) * (1 + mult))), nil
}

// SubscribeShortfalls registers h to receive a [ShortfallEvent] for every
// nonzero-shortfall [LogisticsAPI.Draw] (AC-10). A nil handler is ignored.
// Handlers are invoked synchronously on the drawing goroutine, AFTER the
// shelf mutation's lock is released, so a handler may safely call back
// into this API without deadlocking.
func (l *LogisticsAPI) SubscribeShortfalls(h ShortfallHandler) {
	if l.checkNotCopied("SubscribeShortfalls") != nil {
		return
	}
	if h == nil {
		return
	}
	l.mu.Lock()
	l.subs = append(l.subs, h)
	l.mu.Unlock()
}

// fireShortfall delivers ev to every subscribed handler. It copies the
// subscriber slice under the lock and invokes handlers outside it, so a
// handler that subscribes/unsubscribes or calls back into the API cannot
// deadlock or corrupt the subscriber list.
func (l *LogisticsAPI) fireShortfall(ev ShortfallEvent) {
	if err := l.checkNotCopied("fireShortfall"); err != nil {
		return
	}
	l.mu.RLock()
	handlers := make([]ShortfallHandler, len(l.subs))
	copy(handlers, l.subs)
	l.mu.RUnlock()
	for _, h := range handlers {
		h(ev)
	}
}
