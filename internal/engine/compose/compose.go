package compose

import (
	"errors"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// baseline-one stub-mechanics constants. These are NOT player-facing
// balance numbers (GR#15 / the balance-number regime): they are the
// cheapest coarse knobs that keep the loop alive for FEAT-083, and every
// AC that depends on them is directional (population grew, money moved,
// conservation held) — never a hardcoded target.
const (
	// defaultStartCoordX/Y place the start tile at the centre of the 30x30
	// expansion grid. The real Folkestone start-tile placement is a world
	// (MOD-017) concern; this is a documented baseline-one placeholder.
	defaultStartCoordX = 15
	defaultStartCoordY = 15

	seedCitizenCount    = 64 // baseline-one seed population (AC-8's non-zero seed)
	monthlyBirths       = 8  // citizens hook, per month
	monthlyNetMigration = 2  // attract hook, net per month

	initialTreasury      = 10_000_000 // micropounds (10 pounds)
	initialCitizenWealth = 5_000_000  // micropounds (5 pounds)
	monthlyWages         = 1_000_000  // finance stub, per month (1 pound)
	monthlyTax           = 1_000_000  // finance stub, per month (1 pound; budget closes)
)

// Deps carries the real module dependencies Wire composes. A nil *Deps
// (the common boot case) means "construct the defaults" — world, citizens
// and market are built fresh inside Wire. Callers that need to observe or
// pre-build a dependency (tests, a future save/reload path) inject it
// here.
type Deps struct {
	// CorrelationID roots every registry-sourced error this composition
	// constructs. Empty mints a fresh one.
	CorrelationID string

	// World, Citizens and Market are the three REAL module dependencies.
	// A nil field means "construct the default". (A caller that wants to
	// prove a required module is absent uses the LoadMarket seam below —
	// the only one of the three whose default construction can genuinely
	// fail.)
	World    *world.WorldAPI
	Citizens *citizens.CitizensAPI
	Market   *market.MarketAPI

	// LoadMarket overrides market construction (defaults to
	// market.LoadDefault). It is the AC-4 test seam: a caller injects a
	// failing loader and asserts Wire returns ErrModuleFailed naming
	// "market" with zero hooks left behind.
	LoadMarket func(correlationID string) (*market.MarketAPI, error)

	// InvariantOpts are threaded straight into invariant.WireDaily. Tests
	// use invariant.WithLogSink / invariant.WithPanicFunc to observe
	// conservation violations (AC-10).
	InvariantOpts []invariant.HookOption
}

// moduleRegistration is one fixed slot in the composition order.
type moduleRegistration struct {
	name  string
	phase core.PhaseKind
	// hook builds this module's PhaseHook against the wired state. nil
	// means "wired via a dedicated path" (the invariant, via
	// invariant.WireDaily).
	hook func(st *simState) core.PhaseHook
}

// registrationOrder is the fixed, documented composition order (AC-2). It
// is a slice, NEVER a map: iteration order IS the contract, and nothing in
// this package ranges over a registration map (GR#21). The order is:
// world, citizens, market, consumption, finance, build, attract, then the
// invariant on PhaseDailyTick. Two modules share a phase in two places —
// market then consumption (both PhaseConsumptionShortfall) and citizens
// then attract (both PhasePopulation) — and this slice order is what
// determines their intra-phase run order.
var registrationOrder = []moduleRegistration{
	{name: "world", phase: core.PhaseProduction, hook: func(st *simState) core.PhaseHook { return noopHook{name: "world", st: st} }},
	{name: "citizens", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook { return &spawnHook{st: st, name: "citizens", count: monthlyBirths} }},
	{name: "market", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return noopHook{name: "market", st: st} }},
	{name: "consumption", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return noopHook{name: "consumption", st: st} }},
	{name: "finance", phase: core.PhaseFinance, hook: func(st *simState) core.PhaseHook { return &financeHook{st: st} }},
	{name: "build", phase: core.PhaseLandValueDecay, hook: func(st *simState) core.PhaseHook { return noopHook{name: "build", st: st} }},
	{name: "attract", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook {
		return &spawnHook{st: st, name: "attract", count: monthlyNetMigration}
	}},
	{name: "invariant", phase: core.PhaseDailyTick, hook: nil},
}

// RegistrationOrder returns a defensive copy of the fixed composition
// order (module names), in the order Wire registers them. Exported so a
// test can bind the documented order mechanically (AC-2), and so
// harness.synth can read the real hook count without hand-asserting a
// stale literal (AC-14).
func RegistrationOrder() []string {
	out := make([]string, len(registrationOrder))
	for i, r := range registrationOrder {
		out[i] = r.name
	}
	return out
}

// BaselineOneHookCount returns the number of PhaseHooks Wire registers
// (one per moduleRegistration). The runtime ground truth is
// core.Engine.HookCount() after Wire; this mirrors it for callers that
// need the declared figure without constructing an engine (AC-14).
func BaselineOneHookCount() int { return len(registrationOrder) }

// Composition is the read-only handle Wire returns once the baseline-one
// hook set is registered. It exposes the composition's own live state
// (population, money flows) so a headless driver or test can assert the
// directional liveness ACs without reaching into unexported state.
type Composition struct {
	state *simState
}

// Population returns the current total citizen population (all fidelity
// tiers), read live from the citizens store.
func (c *Composition) Population() int {
	return c.state.citizens.TotalPopulation(c.state.cid)
}

// MoneyFlows returns the cumulative gross money flow (wages + tax) the
// finance stub has emitted — the AC-9 "money moved" figure, distinct from
// the conserved net total. Read-only; safe to call after a run completes
// (single-goroutine, see simState's doc comment).
func (c *Composition) MoneyFlows() int64 {
	return c.state.moneyFlows
}

// Treasury returns the current treasury balance (micropounds).
func (c *Composition) Treasury() int64 {
	return c.state.treasury
}

// CitizenWealth returns the current aggregate citizen wealth (micropounds).
func (c *Composition) CitizenWealth() int64 {
	return c.state.citizenWealth
}

// PopulationHash returns the citizens store's deterministic population
// fingerprint (citizens.PopulationHash) — the AC-11 determinism probe: two
// composed runs at the same seed must produce the identical hash.
func (c *Composition) PopulationHash() [32]byte {
	return c.state.citizens.PopulationHash(c.state.cid)
}

// Wire registers the full baseline-one hook set against e in the fixed,
// documented order (world -> citizens -> market -> consumption -> finance
// -> build -> attract, invariant on PhaseDailyTick). It is the single
// wiring path every runnable top reaches real hooks through (AC-1/AC-13);
// no other package calls core.Engine.RegisterPhaseHook for the real
// modules.
//
// Wire fails loudly, never silently:
//   - a second call on an already-composed engine returns ErrAlreadyComposed
//     (AC-3);
//   - a required module whose construction fails returns ErrModuleFailed
//     naming the module, with zero hooks left behind (AC-4);
//   - a call after the engine has sealed returns ErrWiringAfterSeal wrapping
//     core.ErrEngineSealed (AC-6).
//
// deps may be nil (construct defaults). The returned *Composition exposes
// the live population/money state; callers that only need the engine (the
// headless driver, cmd/metropolis) may ignore it.
func Wire(e *core.Engine, deps *Deps) (*Composition, error) {
	if e == nil {
		return nil, errs.New(ErrRequiredModuleMissing, errs.NewCorrelationID(), map[string]any{"module": "engine"})
	}
	if deps == nil {
		deps = &Deps{}
	}
	cid := deps.CorrelationID
	if cid == "" {
		cid = errs.NewCorrelationID()
	}

	// AC-3: compose is the only real hook registrar, so any pre-existing
	// hook means this engine was already composed (or tampered with).
	// Reject rather than silently append duplicates.
	if e.HookCount() > 0 {
		return nil, errs.New(ErrAlreadyComposed, cid, nil)
	}

	// Resolve every required dependency BEFORE the first registration, so
	// a construction failure never leaves a partially-wired engine (AC-4).
	w := deps.World
	if w == nil {
		w = world.NewWorldAPI(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY})
	}

	c := deps.Citizens
	if c == nil {
		var err error
		c, err = citizens.NewCitizensAPI(e.WorldSeed(), cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens"})
		}
	}

	m := deps.Market
	if m == nil {
		loader := deps.LoadMarket
		if loader == nil {
			loader = market.LoadDefault
		}
		var err error
		m, err = loader(cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "market"})
		}
	}

	invReg := invariant.NewRegistry()
	for _, inv := range []invariant.Invariant{
		invariant.NewPeopleInvariant(),
		invariant.NewMoneyInvariant(),
		invariant.NewGoodsInvariant(),
		invariant.NewVehicleInvariant(),
	} {
		if err := invReg.Register(inv); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "invariant"})
		}
	}

	st := &simState{
		e:             e,
		cid:           cid,
		seed:          e.WorldSeed(),
		citizens:      c,
		world:         w,
		market:        m,
		treasury:      initialTreasury,
		citizenWealth: initialCitizenWealth,
		nextCitizenID: 1,
	}

	// Establish the non-zero seed population (AC-8's precondition).
	if err := st.spawnCitizens(0, seedCitizenCount); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens"})
	}
	st.peopleOpening = int64(st.citizens.TotalPopulation(cid))
	st.moneyOpening = num.SatAdd(st.treasury, st.citizenWealth)

	// Register in the fixed, documented order (AC-2). The slice order IS
	// the contract — nothing here ranges over a map.
	for _, reg := range registrationOrder {
		if reg.name == "invariant" {
			if err := invariant.WireDaily(e, invReg, st.snapshot, deps.InvariantOpts...); err != nil {
				return nil, wrapSeal(cid, err, "invariant")
			}
			continue
		}
		if err := e.RegisterPhaseHook(reg.phase, reg.hook(st)); err != nil {
			return nil, wrapSeal(cid, err, reg.name)
		}
	}

	return &Composition{state: st}, nil
}

// wrapSeal translates a registration failure into the compose-level error.
// A sealed-engine failure (core.ErrEngineSealed, reachable through
// invariant's own ErrWiringAfterSeal wrap) becomes ErrWiringAfterSeal so
// the caller can distinguish "sealed" from any other registration failure
// (AC-6), and every other failure becomes ErrModuleFailed naming the
// module.
func wrapSeal(cid string, err error, module string) error {
	if errors.Is(err, &errs.E{Code: core.ErrEngineSealed}) {
		return errs.Wrap(ErrWiringAfterSeal, cid, err, map[string]any{"module": module})
	}
	return errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": module})
}

// simState is the composition's shared state. The people/money ledgers
// implement the conservation accounting the invariant checks every tick:
// each ledger records the opening total at the last daily check plus the
// tracked delta accumulated since, and the invariant's SnapshotProvider
// (snapshot below) verifies Closing - Opening == TrackedDelta against the
// live store, then closes the period.
//
// # No mutex, by the same discipline as invariant.Hook
//
// simState holds no sync.Mutex. Every access is single-goroutine by
// construction: only shard 0 of each hook's RunShard touches it (the
// invariant's SnapshotProvider, the spawn/finance ApplyEffect barrier
// work), and the phase pipeline runs phases sequentially — the daily
// phase's det.RunPhase joins its workers before the monthly phases start.
// A mutex here would be a copy hazard with no copy risk to guard (and
// would make this type an astgate SEC-020 candidate for nothing).
type simState struct {
	e    *core.Engine
	cid  string
	seed uint64

	citizens *citizens.CitizensAPI
	world    *world.WorldAPI
	market   *market.MarketAPI

	// people conservation ledger
	peopleOpening int64
	peopleDelta   int64

	// money conservation ledger (total money = treasury + citizen wealth)
	moneyOpening  int64
	moneyDelta    int64
	treasury      int64
	citizenWealth int64

	// cumulative gross money flow (AC-9)
	moneyFlows int64

	nextCitizenID uint64
}

// snapshot implements invariant.SnapshotProvider: it builds this tick's
// conservation Snapshot and closes the ledgers. Called from the invariant
// hook's RunShard (shard 0 only), so it is the single reader/writer of the
// ledger on the daily-tick path — no map iteration, no wall clock.
func (st *simState) snapshot(tick int64) invariant.Snapshot {
	closingPop := int64(st.citizens.TotalPopulation(st.cid))

	s := invariant.NewSnapshot(tick)
	s.Readings[invariant.StockPeople] = invariant.StockReading{
		Registered:   true,
		Opening:      st.peopleOpening,
		Closing:      closingPop,
		TrackedDelta: st.peopleDelta,
	}
	st.peopleOpening = closingPop
	st.peopleDelta = 0

	totalMoney := num.SatAdd(st.treasury, st.citizenWealth)
	s.Readings[invariant.StockMoney] = invariant.StockReading{
		Registered:   true,
		Opening:      st.moneyOpening,
		Closing:      totalMoney,
		TrackedDelta: st.moneyDelta,
	}
	st.moneyOpening = totalMoney
	st.moneyDelta = 0

	// goods and vehicles are genuinely zero in baseline one (market has no
	// goods flow yet, traffic does not exist). Report them registered at
	// zero so the full suite runs and balances every tick (AC-10) rather
	// than being skipped.
	s.Readings[invariant.StockGoods] = invariant.StockReading{Registered: true}
	s.Readings[invariant.StockVehicles] = invariant.StockReading{Registered: true}

	return s
}

// spawnCitizens births count citizens at the given sim month, deterministically
// (sequential IDs, personality drawn from the world seed via
// citizens.InitPersonality). It is the only citizen-mutation path the
// composition uses, routed through CitizensAPI's command surface (GR#20).
func (st *simState) spawnCitizens(month int64, count int) error {
	for i := 0; i < count; i++ {
		id := st.nextCitizenID
		st.nextCitizenID++
		cit := citizens.Citizen{
			ID:          id,
			BirthMonth:  int32(month),
			Personality: citizens.InitPersonality(st.seed, id, month, citizens.Personality{}, citizens.Personality{}),
		}
		if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: st.cid,
			Kind:          citizens.LifeEventBirth,
			Citizen:       cit,
		}); err != nil {
			return err
		}
	}
	return nil
}

// noopHook is the PhaseHook for a real module whose tick behaviour is not
// yet built (world: terrain/ownership store; market: price registry;
// consumption/build: baseline-one stub slots). It satisfies core.PhaseHook
// with zero work: RunShard touches only shard-local scratch (nothing),
// ApplyEffect is a no-op, both deterministic. Documented in doc.go's
// STUB-FOR-BASELINE section.
type noopHook struct {
	name string
	st   *simState
}

func (noopHook) RunShard(shard int) ([]core.Effect, error) { return nil, nil }
func (noopHook) ApplyEffect(core.Effect)                   {}

// spawnEffect carries a citizens/attract monthly spawn instruction from
// RunShard (shard 0) to ApplyEffect (the single-goroutine barrier).
type spawnEffect struct {
	month int64
	count int
}

// spawnHook is the PhaseHook for citizens (births) and attract (net
// migration). Only shard 0 emits the effect; ApplyEffect births the
// citizens and records the tracked people delta. Deterministic for a given
// seed + month (AC-19).
type spawnHook struct {
	st    *simState
	name  string
	count int
}

func (h *spawnHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: spawnEffect{month: clock.Month(), count: h.count}}}, nil
}

func (h *spawnHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(spawnEffect)
	if !ok {
		return
	}
	if err := h.st.spawnCitizens(p.month, p.count); err != nil {
		// A valid baseline-one spawn cannot fail; log loudly rather than
		// swallow (GR#1). ApplyEffect has no error return, so the failure
		// is surfaced through the error registry's log sink.
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": h.name, "cause": err.Error()})
		return
	}
	h.st.peopleDelta = num.SatAdd(h.st.peopleDelta, int64(p.count))
}

// financeEffect is the monthly finance stub's tick marker.
type financeEffect struct{}

// financeHook is the baseline-one finance stub: a budget-closing wage/tax
// transfer. Wages move treasury -> citizen wealth; tax moves citizen
// wealth -> treasury. The net money change is zero (the budget closes), so
// the conserved total is unchanged while the gross flow (AC-9) grows.
type financeHook struct {
	st *simState
}

func (h *financeHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: financeEffect{}}}, nil
}

func (h *financeHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(financeEffect); !ok {
		return
	}
	st := h.st

	// pay wages: treasury -> citizens
	st.treasury = num.SatSub(st.treasury, monthlyWages)
	st.citizenWealth = num.SatAdd(st.citizenWealth, monthlyWages)
	// collect tax: citizens -> treasury (budget closes: wages == tax)
	st.citizenWealth = num.SatSub(st.citizenWealth, monthlyTax)
	st.treasury = num.SatAdd(st.treasury, monthlyTax)

	// gross flow (AC-9 "money moved"); net delta is zero by construction
	// but tracked so the invariant verifies it against the store.
	st.moneyFlows = num.SatAdd(st.moneyFlows, num.SatAdd(monthlyWages, monthlyTax))
	st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(monthlyTax, monthlyWages))
}
