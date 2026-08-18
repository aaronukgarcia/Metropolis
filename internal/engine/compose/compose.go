package compose

import (
	"errors"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
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

	seedCitizenCount = 64 // baseline-one seed population (AC-8's non-zero seed)
	monthlyBirths    = 8  // citizens hook, per month

	initialTreasury      = 10_000_000 // micropounds (10 pounds)
	initialCitizenWealth = 5_000_000  // micropounds (5 pounds)
	monthlyWages         = 1_000_000  // finance stub, per month (1 pound)
	monthlyTax           = 1_000_000  // finance stub, per month (1 pound; budget closes)
)

// baseline-one real-module placeholders. Like the block above, these are
// NOT player-facing balance numbers (GR#15 / the balance-number regime):
// they are the cheapest coarse knobs that let the REAL modules tick in the
// loop for FEAT-083. They are documented placeholders, not spec-transcribed
// figures, and they will be replaced by data-loading / real topology once
// their owning modules (utility networks, world pool) supply it.
const (
	// playerOwnerID is the single baseline-one player/owner identity. The
	// build module's §7 ownership gate keys on world ownership; a real
	// multi-player/owner model is a later sprint.
	playerOwnerID = uint32(1)

	// consumption source capacities (units/tick). The city's real utility
	// topology does not exist yet (build has built no networks); these
	// coarse single-source networks keep the consumption solve drawing.
	baselineOneWaterCapacity = 1_000_000.0
	baselineOnePowerCapacity = 1_000_000.0
	baselineOneGasCapacity   = 1_000_000.0

	// attract master-dial inputs. A_world (40) and the five pushed terms
	// (50) are neutral-ish placeholders; with the computed housing term
	// reading 100 (vacant city), A ≈ 52 > A_world so migration is net
	// positive and reputation momentum carries it upward from there.
	baselineOneAWorld        = 40.0
	baselineOneMigrationRate = 1.0
	baselineOneTermValue     = 50.0 // the five pushed §11 terms
	baselineOneMonthlyRent   = 0    // micropounds; vacant city rent placeholder

	// attract capacity constraints (people / dwelling units). Unbounded
	// placeholders — the real housing-vacancy and junction-throughput
	// signals come from households/logistics once those are wired to
	// produce them.
	baselineOneHousingVacancy     = int64(1_000_000)
	baselineOneJunctionThroughput = int64(1_000_000)

	// attract reputation-momentum parameters (asymmetric: fall faster than
	// rise — §11's Detroit-trap mechanic). Same shape the attract module's
	// own S6 scenario uses.
	baselineOneRepRise = 0.2
	baselineOneRepFall = 0.8
	baselineOneRepMax  = 100.0
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

	// Logistics overrides construction of the engine.logistics dependency
	// build's Tick draws construction materials against (defaults to
	// logistics.LoadDefault). A nil field is the common boot case; the
	// BUG-266 regression test seam: a caller injects a pre-Provisioned
	// LogisticsAPI so a build order can actually complete (unprovisioned
	// stock never fulfils a materials draw) and drive a real demolish
	// through handleGameplay.
	Logistics *logistics.LogisticsAPI

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
	{name: "consumption", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return &consumptionHook{st: st} }},
	{name: "finance", phase: core.PhaseFinance, hook: func(st *simState) core.PhaseHook { return &financeHook{st: st} }},
	{name: "build", phase: core.PhaseLandValueDecay, hook: func(st *simState) core.PhaseHook { return &buildHook{st: st} }},
	{name: "attract", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook { return &attractHook{st: st} }},
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

// ConsumptionDelivered returns the cumulative utility quantity the
// consumption hook has delivered (litres + kWh summed across water/power/
// gas) over the run so far. Non-zero proves the real consumption solve
// drew against the coarse networks rather than no-op'ing — the
// "consumption actually draws" liveness observable.
func (c *Composition) ConsumptionDelivered() float64 {
	return c.state.consumptionDelivered
}

// NetMigration returns the cumulative signed net migration (inflow −
// outflow) the attract hook has applied. It is the "migration is
// attractiveness-driven" observable: driven by AttractAPI.ApplyMigration's
// g(A − A_world), never a hardcoded +N/month.
func (c *Composition) NetMigration() int64 {
	return c.state.netMigration
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

	// FEAT-083: construct the real baseline-one modules that replace the
	// three original stub slots (consumption/build/attract), plus the
	// finance/households APIs attract's HousingAffordability term consumes.
	// Each is resolved BEFORE the first hook registers, so a construction
	// failure never leaves a partially-wired engine (AC-4).
	consumptionAPI, err := consumption.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	waterNet, err := baselineOneNetwork(consumption.UtilityWater, consumption.SourceReservoir, baselineOneWaterCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	powerNet, err := baselineOneNetwork(consumption.UtilityPower, consumption.SourceSellindgeGrid, baselineOnePowerCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	gasNet, err := baselineOneNetwork(consumption.UtilityGas, consumption.SourceOffMapPipeline, baselineOneGasCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}

	seasonAPI, err := season.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "season"})
	}
	logisticsAPI := deps.Logistics
	if logisticsAPI == nil {
		var err error
		logisticsAPI, err = logistics.LoadDefault(cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "logistics"})
		}
	}
	buildAPI, err := build.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetWorld(w); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetSeason(seasonAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetLogistics(logisticsAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}

	financeAPI := finance.NewFinanceAPI(cid)
	householdsAPI, err := households.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "households"})
	}
	if err := householdsAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "households"})
	}
	attractAPI, err := attract.New(baselineOneAttractConfig(), e.WorldSeed(), cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetFinance(financeAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetHouseholds(householdsAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
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
		consumption:   consumptionAPI,
		waterNet:      waterNet,
		powerNet:      powerNet,
		gasNet:        gasNet,
		buildAPI:      buildAPI,
		attract:       attractAPI,
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

	// Route the four gameplay-intent commands (Buy/Zone/Build/Demolish)
	// onto the build/world command surfaces through core's injected seam.
	// This is the single wiring point — the same AC-1 discipline as the
	// phase hooks: no runnable path bypasses compose to reach these.
	if err := e.SetGameplayCommandHandler(st.handleGameplay); err != nil {
		return nil, wrapSeal(cid, err, "build")
	}

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

	// real baseline-one modules (FEAT-083): consumption/build/attract
	// replace the three original stub slots. (finance/households are also
	// constructed in Wire and handed to attract via its SetFinance/
	// SetHouseholds seam — attract holds those references, so simState does
	// not re-store them.)
	consumption *consumption.UtilityAPI
	waterNet    *consumption.Network
	powerNet    *consumption.Network
	gasNet      *consumption.Network

	buildAPI *build.BuildAPI
	attract  *attract.AttractAPI

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

	// cumulative consumption delivered (liveness evidence) and net
	// migration applied (liveness evidence) — the "consumption draws" and
	// "migration is attractiveness-driven" observables.
	consumptionDelivered float64
	netMigration         int64

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
// yet built (world: terrain/ownership store; market: price registry). It
// satisfies core.PhaseHook with zero work: RunShard touches only
// shard-local scratch (nothing), ApplyEffect is a no-op, both
// deterministic. Documented in doc.go's STUB-FOR-BASELINE section.
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

// spawnHook is the PhaseHook for citizens (births). Only shard 0 emits the
// effect; ApplyEffect births the citizens and records the tracked people
// delta. Deterministic for a given seed + month (AC-19).
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

// --- consumption hook (MOD-021, real) ---

// consumptionEffect is the monthly consumption tick marker.
type consumptionEffect struct {
	month int64
}

// consumptionHook is the baseline-one consumption hook (MOD-021, real): it
// draws the whole city's residential utility demand (water/power/gas)
// against the three coarse baseline-one networks, via UtilityAPI's
// SolveDailyTick, and accumulates the delivered quantity. Only shard 0
// emits the effect; ApplyEffect is the single-goroutine barrier that runs
// the solve (which mutates each network's last-solve state).
type consumptionHook struct {
	st *simState
}

func (h *consumptionHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: consumptionEffect{month: clock.Month()}}}, nil
}

func (h *consumptionHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(consumptionEffect)
	if !ok {
		return
	}
	if err := h.st.drawConsumption(p.month); err != nil {
		// A valid baseline-one draw cannot fail; log loudly rather than
		// swallow (GR#1). ApplyEffect has no error return, so the failure
		// is surfaced through the error registry's log sink.
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "consumption", "cause": err.Error()})
		return
	}
}

// drawConsumption solves the residential demand (one entity: the whole
// city's population at the §17.1 per-person baseline) against water/power/
// gas and accumulates the delivered quantity. A monthly approximation of
// the module's per-day solve (PhaseConsumptionShortfall runs once per
// month) — the real per-day cadence is the module's own daily-tick concern.
func (st *simState) drawConsumption(month int64) error {
	pop := float64(st.citizens.TotalPopulation(st.cid))
	opts := consumption.DemandOptions{MonthIndex: month, GasNetworkPresent: true}
	entities := []consumption.DemandEntity{{EntityRef: "residential", Population: pop}}

	// Slice (not a map) so the network solve order is deterministic (GR#21).
	networks := []*consumption.Network{st.waterNet, st.powerNet, st.gasNet}
	var delivered float64
	for _, net := range networks {
		res, err := st.consumption.SolveDailyTick(net, entities, opts)
		if err != nil {
			return err
		}
		delivered += res.Delivered
	}
	st.consumptionDelivered += delivered
	return nil
}

// --- build hook (MOD-026, real) ---

// buildEffect is the monthly build-queue tick marker.
type buildEffect struct {
	month int64
}

// buildHook is the baseline-one build hook (MOD-026, real): it advances the
// build queue one simulation day per month via BuildAPI.Tick. Zone/Build
// commands themselves arrive through the gameplay-command seam
// (handleGameplay), not this phase hook — this hook only elapses the queue.
type buildHook struct {
	st *simState
}

func (h *buildHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: buildEffect{month: clock.Month()}}}, nil
}

func (h *buildHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(buildEffect)
	if !ok {
		return
	}
	if err := h.st.buildAPI.Tick(p.month); err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "build", "cause": err.Error()})
		return
	}
}

// --- attract hook (MOD-029, real) ---

// attractEffect is the monthly migration tick marker.
type attractEffect struct {
	month int64
}

// attractHook is the baseline-one attract hook (MOD-029, real): it runs one
// monthly AttractAPI.ApplyMigration step. Net migration is g(A − A_world) —
// signed, reputation-momentum-amplified, capacity-capped — never a
// hardcoded +N. The applied net population change is tracked in the people
// conservation ledger so the invariant balances.
type attractHook struct {
	st *simState
}

func (h *attractHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: attractEffect{month: clock.Month()}}}, nil
}

func (h *attractHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(attractEffect)
	if !ok {
		return
	}
	res, err := h.st.applyMigration(p.month)
	if err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "attract", "cause": err.Error()})
		return
	}
	h.st.peopleDelta = num.SatAdd(h.st.peopleDelta, res.NetApplied())
	h.st.netMigration = num.SatAdd(h.st.netMigration, res.NetApplied())
}

// applyMigration pushes the five §11 terms the composition root owns
// (jobAvailability/serviceCoverage/environment/leisureFit/safety are
// pushed input per engine.attract's ASM-243 — no registered edge exists to
// their real source modules yet), then runs one monthly migration step.
// HousingVacancy/JunctionThroughput are unbounded placeholders until
// households/logistics produce real capacity signals.
func (st *simState) applyMigration(month int64) (attract.MigrationResult, error) {
	if err := st.attract.SetTermInputs(attract.TermInputs{
		JobAvailability:        baselineOneTermValue,
		ServiceCoverage:        baselineOneTermValue,
		Environment:            baselineOneTermValue,
		LeisureFit:             baselineOneTermValue,
		Safety:                 baselineOneTermValue,
		HouseholdIDs:           nil, // vacant baseline-one city: no households formed yet
		MonthlyRentMicroPounds: baselineOneMonthlyRent,
	}); err != nil {
		return attract.MigrationResult{}, err
	}
	return st.attract.ApplyMigration(attract.MigrationCommand{
		Month:              month,
		ResidentIDs:        st.residentIDs(),
		HousingVacancy:     baselineOneHousingVacancy,
		JunctionThroughput: baselineOneJunctionThroughput,
	})
}

// residentIDs returns the citizen-id set eligible for personality-weighted
// emigration: every sequentially-minted id (seed + births). Migrant ids
// (minted by attract with a high-bit prefix) are not yet enumerated here —
// a documented baseline-one limitation that only matters on the decline
// branch (emigration), which baseline one does not reach.
func (st *simState) residentIDs() []uint64 {
	ids := make([]uint64, 0, st.nextCitizenID-1)
	for id := uint64(1); id < st.nextCitizenID; id++ {
		ids = append(ids, id)
	}
	return ids
}

// currentMonth returns the engine clock's current simulation month.
func (st *simState) currentMonth() (int64, error) {
	clock, err := st.e.Clock()
	if err != nil {
		return 0, err
	}
	return clock.Month(), nil
}

// --- gameplay command seam (Buy/Zone/Build/Demolish -> build/world) ---

// handleGameplay is the injected core.GameplayCommandHandler. It maps the
// four gameplay-intent protocol commands onto the build/world command
// surfaces: Buy -> world.PurchaseTile, Zone/Build/Demolish ->
// BuildAPI.Submit*Command. A nil return accepts the command (core turns it
// into an Accepted CommandResult); a non-nil registry error rejects it with
// that code. This is the ONE place gameplay intent meets the real modules
// (AC-1/GR#20): no runnable path routes these kinds around compose.
func (st *simState) handleGameplay(cmd protocol.Command) error {
	switch cmd.Kind {
	case protocol.KindBuy:
		p, ok := cmd.Payload.(protocol.BuyPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, _, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		res := st.world.PurchaseTile(world.PurchaseCommand{CorrelationID: st.cid, Tile: tile, BuyerID: playerOwnerID})
		if res.Accepted {
			return nil
		}
		if res.Error == nil {
			return errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "world", "cause": "purchase rejected without an error"})
		}
		// BUG-267: res.Error.Code/Display were already rendered against
		// engine.world's OWN registry template (e.g. MET-E404's
		// "PurchaseTile rejected for tile {tile}: {cause}"). Re-wrapping
		// under that SAME code with a ctx keyed "display" left {tile}/
		// {cause} literal in the message — the ctx key didn't match the
		// template's placeholders. ErrGameplayRejectionPassthrough's
		// template is exactly "{display}", so the already-rendered string
		// passes through intact instead of being re-rendered.
		return errs.New(ErrGameplayRejectionPassthrough, st.cid, map[string]any{"display": res.Error.Display})
	case protocol.KindZone:
		p, ok := cmd.Payload.(protocol.ZonePayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		return st.buildAPI.SubmitZoneCommand(build.ZoneCommand{Tile: tile, Local: local, OwnerID: playerOwnerID, Zone: build.ZoneType(p.ZoneType)})
	case protocol.KindBuild:
		p, ok := cmd.Payload.(protocol.BuildPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		month, err := st.currentMonth()
		if err != nil {
			return err
		}
		// Baseline-one seam note: the protocol's BuildingType maps onto the
		// build module's zone catalogue (build builds zones, not a separate
		// building catalogue yet).
		_, err = st.buildAPI.SubmitBuildCommand(build.BuildCommand{Tile: tile, Local: local, OwnerID: playerOwnerID, Zone: build.ZoneType(p.BuildingType), Month: month})
		return err
	case protocol.KindDemolish:
		p, ok := cmd.Payload.(protocol.DemolishPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		res, err := st.buildAPI.SubmitDemolishCommand(build.DemolishCommand{Tile: tile, Local: local, OwnerID: playerOwnerID})
		if err != nil {
			return err
		}
		// BUG-266: demolish returns a LandPrice-sourced Compensation
		// (build.go's SubmitDemolishCommand doc: "never a bare deletion
		// with no financial consequence"). Credit it treasury -> citizen
		// wealth, the same transfer idiom financeHook uses for wages/tax:
		// SatAdd/SatSub on the two pots, gross flow tallied in moneyFlows,
		// net delta tracked in moneyDelta so the invariant verifies it
		// against the store. The city compensates the owner for the
		// demolished structure's land value; total money is unchanged (a
		// transfer, not a creation), so moneyDelta's net contribution is 0.
		st.treasury = num.SatSub(st.treasury, res.Compensation)
		st.citizenWealth = num.SatAdd(st.citizenWealth, res.Compensation)
		st.moneyFlows = num.SatAdd(st.moneyFlows, res.Compensation)
		st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(res.Compensation, res.Compensation))
		return nil
	default:
		return st.gameplayReject(cmd.Kind, "unhandled gameplay kind")
	}
}

// gameplayReject builds the registry-sourced error for a gameplay command
// this composition cannot map (a defensive branch — core's HandleCommand
// only reaches here for the four gameplay kinds, and Validate already
// guarantees the payload type matches).
func (st *simState) gameplayReject(kind protocol.Kind, cause string) error {
	return errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "build", "kind": string(kind), "cause": cause})
}

// cellFromRef maps a protocol CellRef {x,y} onto a world tile+local cell.
// Baseline-one placeholder: the whole playable extent is the single start
// tile, and the {x,y} grid maps onto its 200x200 local cells. The real
// multi-tile mapping is a world/UI concern (a later sprint).
func (st *simState) cellFromRef(ref protocol.CellRef) (world.TileCoord, world.CellLocal, error) {
	if ref.X < 0 || ref.X >= world.TileSizeCells || ref.Y < 0 || ref.Y >= world.TileSizeCells {
		return world.TileCoord{}, world.CellLocal{}, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "build", "cause": "cell out of bounds",
		})
	}
	return world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}, world.CellLocal{Row: ref.Y, Col: ref.X}, nil
}

// baselineOneNetwork builds one coarse single-source utility network for
// the consumption draw (the real topology arrives once build constructs
// actual networks).
func baselineOneNetwork(kind consumption.Utility, sourceType consumption.SourceType, capacity float64, cid string) (*consumption.Network, error) {
	n := consumption.NewNetwork(kind, cid)
	if err := n.AddSource(consumption.Source{ID: string(kind), Type: sourceType, Capacity: capacity}); err != nil {
		return nil, err
	}
	return n, nil
}

// baselineOneAttractConfig builds attract's runtime Config from the
// documented baseline-one placeholders (the attract module has no data file
// yet; the S6 scenario constructs the same shape inline).
func baselineOneAttractConfig() attract.Config {
	return attract.Config{
		Weights: attract.Weights{
			JobAvailability:      0.2,
			HousingAffordability: 0.2,
			ServiceCoverage:      0.15,
			Environment:          0.1,
			LeisureFit:           0.1,
			Safety:               0.1,
			Reputation:           0.15,
		},
		World:         attract.NewStaticWorldPool(baselineOneAWorld),
		MigrationRate: baselineOneMigrationRate,
		Reputation:    attract.ReputationConfig{RiseRate: baselineOneRepRise, FallRate: baselineOneRepFall, Max: baselineOneRepMax},
	}
}
