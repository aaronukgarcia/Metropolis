package compose

import (
	"errors"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/extcommute"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
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

	// attract master-dial inputs. A_world (40) is a neutral-ish placeholder.
	// All seven §11 terms are now real, computed per month: Safety/
	// LeisureFit/Environment by safetyTerm/leisureFitTerm/environmentTerm
	// (FEAT-167 wave 1), ServiceCoverage/JobAvailability by
	// serviceCoverageTerm/jobAvailabilityTerm (servicesfirms_wire.go,
	// FEAT-167 completion), HousingAffordability/Reputation inside
	// attract itself. The old flat baselineOneTermValue=50.0 placeholder
	// (docs/planning/icd/engine.attract-terms.md §3/§12 open decision 3)
	// is gone: nothing in this package references it any more (its last
	// use was the two ServiceCoverage/JobAvailability SetTermInputs
	// fields below and the tripwire test that guarded them, both replaced
	// — see servicesfirms_wire_test.go's proof that no term reads the old
	// flat value after a warmed-up run).
	baselineOneAWorld        = 40.0
	baselineOneMigrationRate = 1.0
	baselineOneMonthlyRent   = 0 // micropounds; vacant city rent placeholder

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

// FEAT-167 (docs/planning/icd/engine.attract-terms.md): baseline one has no
// district/cell topology yet, so the Safety/Environment terms are computed
// against ONE compose-owned aggregate citywide district/cell — not a
// player-facing balance number, just the coarsest identity that lets
// engine.crime/engine.refuse's real per-district/per-cell accounting run at
// all before topology exists.
const (
	citywideCrimeDistrict = crime.DistrictID(0)
	citywideRefuseCellID  = "citywide"
)

// refuseStreams is the fixed, documented iteration order for summing
// engine.refuse's three §25 waste streams into the Environment term's
// composite (environmentTerm below) — a slice, never a map range (GR#21).
var refuseStreams = []refuse.Stream{refuse.StreamGeneral, refuse.StreamRecycling, refuse.StreamFood}

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

	// Crime, Leisure and Refuse override construction of the three
	// FEAT-167 attract-term source modules (default: crime.New /
	// leisure.LoadDefault / refuse.LoadDefault). nil is the common boot
	// case; the test seam lets a caller inject a pre-configured instance
	// to prove a term value actually moves when its source module's state
	// changes (docs/planning/icd/engine.attract-terms.md §11).
	Crime   *crime.CrimeAPI
	Leisure *leisure.LeisureAPI
	Refuse  *refuse.RefuseAPI

	// ExtCommute overrides construction of the FEAT-207 off-map
	// external-commuting module (default: extcommute.LoadDefault). nil is
	// the common boot case; the test seam lets a caller inject a
	// pre-configured instance (docs/planning/icd/engine.extcommute-compose.md
	// §11's end-to-end unblock test).
	ExtCommute *extcommute.ExtCommuteAPI

	// Services overrides construction of the FEAT-167-completion
	// ServiceCoverage source module (default: services.LoadDefault). nil
	// is the common boot case; the test seam lets a caller inject a
	// pre-registered instance to prove ServiceCoverage actually moves
	// when its source module's state changes
	// (docs/planning/icd/engine.services-coverage.md §11).
	Services *services.ServicesAPI

	// Firms overrides construction of the FEAT-167-completion
	// JobAvailability source module (default: firms.LoadDefault). nil is
	// the common boot case; the test seam lets a caller inject a
	// pre-registered instance to prove JobAvailability actually moves
	// when its source module's state changes
	// (docs/planning/icd/engine.firms-labourmarket.md §11).
	Firms *firms.FirmsAPI

	// LoadTraffic overrides construction of the FEAT-206 engine.traffic
	// dependency (defaults to loadDefaultTraffic — traffic.New() +
	// LoadConfig against the resolved data/ dir, mirroring LoadMarket's
	// shape above). nil is the common boot case; the test seam lets a
	// caller inject a failing loader and assert Wire returns
	// ErrModuleFailed naming "traffic" with zero hooks left behind (AC-4's
	// discipline, docs/planning/icd/engine.traffic-tick.md §2/§8).
	LoadTraffic func(correlationID string) (*traffic.TrafficAPI, error)
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
// market then consumption (both PhaseConsumptionShortfall), and citizens,
// build, then invariant (all three PhaseDailyTick — FEAT-169 moved citizens
// onto the daily tick alongside build/invariant, see below) — this slice
// order is what determines their intra-phase run order. attract remains
// alone on PhasePopulation now that citizens has moved off it.
var registrationOrder = []moduleRegistration{
	{name: "world", phase: core.PhaseProduction, hook: func(st *simState) core.PhaseHook { return noopHook{name: "world", st: st} }},
	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): traffic's
	// AdvanceTick registers on PhaseDailyTick, and MUST come before every
	// other PhaseDailyTick registration below it in this slice (citizens,
	// build, invariant) — traffic's own doc.go "Day-boundary contract"
	// requires the reset to run "before that day's demand-generating
	// systems... run their own tick logic for the day". Placed
	// immediately after "world" (a different, monthly phase — its
	// position relative to traffic carries no ordering meaning) so the
	// slice reads world -> traffic -> citizens -> ... and, restricted to
	// just the PhaseDailyTick subset, traffic -> citizens -> build ->
	// invariant.
	{name: "traffic", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &trafficTickHook{st: st} }},
	// FEAT-169: citizens registers on the DAILY tick (not PhasePopulation)
	// because CitizensAPI.AdvanceDayTick is itself a once-per-day-tick call
	// (its own amortised 1/30-shards-per-day cold pass) and the ICD's T0
	// update class requires the resulting births/deaths land in peopleDelta
	// the SAME tick they are computed — never queued past it. Registered
	// BEFORE build/invariant in this slice so citizens' births/deaths are
	// folded into peopleDelta before invariant's same-tick conservation
	// check observes it (the same ordering discipline BUG-268 established
	// for build -> invariant).
	{name: "citizens", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &coldPassHook{st: st} }},
	{name: "market", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return noopHook{name: "market", st: st} }},
	{name: "consumption", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return &consumptionHook{st: st} }},
	{name: "finance", phase: core.PhaseFinance, hook: func(st *simState) core.PhaseHook { return &financeHook{st: st} }},
	// BUG-268: build was wired against the monthly PhaseLandValueDecay slot,
	// so BuildAPI.Tick's one-simulation-DAY-per-call cadence (build.go's
	// daysPerTick) only ever fired once per simulation MONTH — a 45-day
	// dwelling took 45 months, not 45 days. Moved to the daily
	// PhaseDailyTick (the only daily phase this package's fixed phase set
	// offers) so one sim-day of lead/materials/labour elapses per sim-day,
	// matching data/buildings.json's own "labourPerTick"/"leadTimeUnit"
	// documentation (both already written in daily terms). Registered
	// before "invariant" below so the queue advances, then the conservation
	// check observes the same day's result — deterministic intra-phase
	// order via this slice's iteration order (GR#21).
	{name: "build", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &buildHook{st: st} }},
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

// viewRegistration is one fixed slot in the FEAT-208 view-publishing
// order, parallel to moduleRegistration above (AC-2's "zero hooks left
// behind" discipline extended to "zero views left behind"). fn resolves
// this view's ViewPatchFunc against the wired simState — it is not
// called until Wire actually registers it, mirroring
// moduleRegistration.hook's deferred-construction shape.
type viewRegistration struct {
	name string
	fn   func(st *simState) core.ViewPatchFunc
}

// viewRegistrationOrder is the fixed, documented FEAT-208 view
// registration order (AC-2 extended) — a slice, NEVER a map: this
// package never ranges a view registration table (GR#21), matching
// registrationOrder's own discipline above. Increment 1 (the FEAT-208
// design's §6 recommended first slice) registered exactly one view:
// "f4.services", serving only its capacityDemand sub-view
// (buildServicesCapacityDemandPatch, services_publish.go). Increment 2
// adds "f2.finance", serving only its balanceSheet sub-view
// (buildFinanceBalanceSheetPatch, finance_publish.go) — the design's §6
// fast-follow list's next entry, chosen because engine.finance is
// already composed and ui.screen.finance's ApplyDelta already exists.
// BUG-323 adds "f1.viewport", serving the start tile's real
// engine.world terrain (buildViewportPatch, viewport_publish.go) — the
// design's §6 fast-follow list's next entry, pulled forward to P0
// because F1 is the DEFAULT screen at boot and, with no view registered
// here, engine.core rejected its Subscribe and it rendered entirely
// blank. Later increments (f8.districts, f5.trade, f7.projections) are
// documented, deliberate fast-follows — see the design's §6 — each
// adding one more entry here, in the SAME slice, never a new
// registration mechanism.
var viewRegistrationOrder = []viewRegistration{
	{name: servicesViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildServicesCapacityDemandPatch }},
	{name: financeViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildFinanceBalanceSheetPatch }},
	// BUG-324: "chrome.topbar" — the global chrome's top-bar figures
	// (chrome_publish.go). Not one of the design's §6 F-screen
	// fast-follows: it is the view the ALWAYS-visible chrome renders
	// from, and internal/ui/screens/chrome could not be registered in
	// cmd/metropolis at all until it existed, because an unregistered
	// view's Subscribe is rejected and the top bar would have rendered
	// permanently empty — the same failure mode "f1.viewport" shows
	// today.
	{name: chromeViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildChromeTopBarPatch }},
	{name: viewportViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildViewportPatch }},
}

// RegisteredViewNames returns a defensive copy of the fixed view
// registration order (view names), in the order Wire registers them —
// mirrors RegistrationOrder() above, for a test or future harness that
// needs the declared FEAT-208 view set without constructing an engine.
func RegisteredViewNames() []string {
	out := make([]string, len(viewRegistrationOrder))
	for i, r := range viewRegistrationOrder {
		out[i] = r.name
	}
	return out
}

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

// VitalBirths returns the cumulative real fertility births (FEAT-160) the
// citizens cold pass has produced and folded into peopleDelta so far — the
// "births are real, not the old flat-8/month fake" observable. Zero is a
// legitimate value (no eligible couples yet), unlike the old spawnHook
// fake, which was never zero after the first month.
func (c *Composition) VitalBirths() int64 {
	return c.state.vitalBirths
}

// VitalDeaths returns the cumulative real per-citizen mortality deaths the
// citizens cold pass has produced and folded into peopleDelta so far.
func (c *Composition) VitalDeaths() int64 {
	return c.state.vitalDeaths
}

// ExtCommute returns the wired FEAT-207 off-map external-commuting handle
// (docs/planning/icd/engine.extcommute-compose.md). Baseline one routes no
// gameplay command to Assign/Release/InCommute yet (ICD §12 open decision
// 4 — command routing is a later, separate item); this accessor is the
// seam a future gameplay handler, or a test driving the end-to-end
// assign/release proof, reaches it through.
func (c *Composition) ExtCommute() *extcommute.ExtCommuteAPI {
	return c.state.extCommute
}

// Traffic returns the wired FEAT-206 engine.traffic handle
// (docs/planning/icd/engine.traffic-tick.md). Baseline one routes no other
// demand-generating module (engine.shopping, engine.dispatch — neither
// exists in this codebase yet) through it besides this package's own
// AdvanceTick hook and extcommute's read-only Congestion seam
// (traffic_wire.go); this accessor is the seam a future demand-generating
// module's SetTraffic wiring, and today's tests (the AC-required
// unbounded-demand regression and day-boundary ordering proofs), reach the
// composed instance through — mirrors ExtCommute() above.
func (c *Composition) Traffic() *traffic.TrafficAPI {
	return c.state.traffic
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

	// FEAT-169 id-namespace-seam Wire-time assertion (destructive-review
	// REJECT finding): the ORIGINAL FEAT-169 build only guarded compose's
	// own counter against citizens.FertilityChildIDBase (spawnCitizens'
	// per-mint check below) — that defends compose's [1, 2^62) range but
	// says nothing about the boundary between engine.attract's migrant
	// range [2^62, 2^63) and citizens' fertility range [2^63, ...), which
	// independently collided (both started life at 1<<62). Both sides are
	// compile-time constants today, so this can never actually fail unless
	// a future edit to either package's base breaks the convention — but a
	// silent overlap there is exactly the class of bug that shipped once
	// already, so this checks it explicitly, every Wire call, rather than
	// leaving it to a comment nobody re-reads. See citizens/doc.go and
	// this package's doc.go for the full three-range id map.
	if !idNamespaceRangesDisjoint(citizens.FertilityChildIDBase, attract.MigrantIDBase) {
		return nil, errs.New(ErrIDNamespaceRangesOverlap, cid, map[string]any{
			"fertilityChildIDBase": citizens.FertilityChildIDBase,
			"migrantIDBase":        attract.MigrantIDBase,
		})
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
	if err := seedOpeningBalances(financeAPI, initialTreasury, initialCitizenWealth); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "finance", "step": "seedOpeningBalances"})
	}
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

	// FEAT-167 (docs/planning/icd/engine.attract-terms.md): construct the
	// three real Safety/LeisureFit/Environment source modules, resolved
	// BEFORE the first hook registers like every other required module
	// above (AC-4 — no partially-wired engine on a construction failure).
	crimeAPI := deps.Crime
	if crimeAPI == nil {
		var cErr error
		crimeAPI, cErr = crime.New(e.WorldSeed(), cid)
		if cErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, cErr, map[string]any{"module": "crime"})
		}
	}

	leisureAPI := deps.Leisure
	if leisureAPI == nil {
		var lErr error
		leisureAPI, lErr = leisure.LoadDefault(cid)
		if lErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, lErr, map[string]any{"module": "leisure"})
		}
	}

	refuseAPI := deps.Refuse
	if refuseAPI == nil {
		var rErr error
		refuseAPI, rErr = refuse.LoadDefault(cid)
		if rErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, rErr, map[string]any{"module": "refuse"})
		}
	}
	// RegisterCell is an upsert (refuse/generate.go): safe to call every
	// Wire, including against an injected/pre-registered *RefuseAPI.
	if err := refuseAPI.RegisterCell(citywideRefuseCellID, refuse.LandUseResidential, "citywide"); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "refuse"})
	}

	attractTerms, err := loadAttractTermsData(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract_terms_data"})
	}

	// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
	// docs/planning/icd/engine.firms-labourmarket.md): construct the two
	// remaining §11 term source modules. Resolved BEFORE the first hook
	// registers, like every other required module above (AC-4).
	servicesAPI := deps.Services
	if servicesAPI == nil {
		var sErr error
		servicesAPI, sErr = services.LoadDefault(cid)
		if sErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, sErr, map[string]any{"module": "services"})
		}
	}

	firmsAPI := deps.Firms
	if firmsAPI == nil {
		var fErr error
		firmsAPI, fErr = firms.LoadDefault(e.WorldSeed(), cid)
		if fErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, fErr, map[string]any{"module": "firms"})
		}
	}
	// JobAvailability's LabourMarket() fails closed (MET-G1409) without
	// citizens wired (labourmarket.go's TotalVacancies alone needs no
	// dependency, but LabourMarket's Workforce side does). Finance/market/
	// build are wired too — cheap given all three are already constructed
	// above, and required for any future firm-lifecycle work this module
	// owns beyond the JobAvailability aggregate (out of this ICD's scope).
	if err := firmsAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetFinance(financeAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetMarket(m); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetBuild(buildAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}

	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): construct the
	// real engine.traffic dependency BEFORE extcommute below, so its
	// TrafficSeam adapter (extCommuteTrafficSeamAdapter, traffic_wire.go)
	// can be built against a live instance instead of the old free-flow
	// stub. Resolved before the first hook registers, like every other
	// required module above (AC-4 — no partially-wired engine on a
	// construction failure).
	loadTraffic := deps.LoadTraffic
	if loadTraffic == nil {
		loadTraffic = loadDefaultTraffic
	}
	trafficAPI, err := loadTraffic(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "traffic"})
	}
	trafficSeam, err := newExtCommuteTrafficSeamAdapter(trafficAPI, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "traffic"})
	}

	// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): the
	// Wire-time identity-map cross-check MUST run before extcommute's
	// citizens-seam adapter is ever exercised (§3/§11 "identity-map
	// conformance") — checked here, before extcommute is even constructed,
	// so a drift fails loudly with zero hooks left behind (AC-4's
	// discipline extended to this assertion).
	if err := extCommuteEmploymentStatesIdentical(cid); err != nil {
		return nil, err
	}
	extCommuteAPI := deps.ExtCommute
	if extCommuteAPI == nil {
		var xErr error
		extCommuteAPI, xErr = extcommute.LoadDefault(cid)
		if xErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, xErr, map[string]any{"module": "extcommute"})
		}
	}
	if err := extCommuteAPI.SetSeed(e.WorldSeed()); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	if err := extCommuteAPI.SetCitizensSeam(&extCommuteCitizensSeam{api: c, cid: cid}); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	// FEAT-206: TrafficSeam is now the real derivation off the composed
	// *traffic.TrafficAPI (traffic_wire.go's extCommuteTrafficSeamAdapter),
	// replacing the old always-0.0 extCommuteTrafficSeamStub free-flow
	// placeholder (ICD §12 open decision 2 is now closed for this seam;
	// extCommuteTrafficSeamStub itself is left in extcommute_wire.go,
	// unused by Wire, as the documented historical baseline
	// TestExtCommute_TrafficSeamStub_IsFreeFlow still pins).
	if err := extCommuteAPI.SetTrafficSeam(trafficSeam); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	if err := extCommuteAPI.SetFinanceSeam(&extCommuteFinanceSeam{
		api: financeAPI,
		cid: cid,
		monthFn: func() int64 {
			clock, cErr := e.Clock()
			if cErr != nil {
				return 0
			}
			return clock.Month()
		},
	}); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
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
		e:                       e,
		cid:                     cid,
		seed:                    e.WorldSeed(),
		citizens:                c,
		world:                   w,
		market:                  m,
		consumption:             consumptionAPI,
		waterNet:                waterNet,
		powerNet:                powerNet,
		gasNet:                  gasNet,
		buildAPI:                buildAPI,
		attract:                 attractAPI,
		finance:                 financeAPI,
		crime:                   crimeAPI,
		leisure:                 leisureAPI,
		refuse:                  refuseAPI,
		services:                servicesAPI,
		firms:                   firmsAPI,
		traffic:                 trafficAPI,
		extCommute:              extCommuteAPI,
		attractTerms:            attractTerms,
		leisureVenuesRegistered: make(map[uint64]bool),
		treasury:                ledgerBalance(financeAPI, finance.AcctTreasury),
		citizenWealth:           ledgerBalance(financeAPI, finance.AcctHouseholds),
		nextCitizenID:           1,
	}
	// treasury is seeded through setTreasury (never assigned directly)
	// so the BUG-324 publish mirror is correct from before the engine
	// ever ticks. BUG-355: the pot itself comes from the FinanceAPI
	// ledger (seedOpeningBalances above), so the mirror is seeded from
	// that same ledger value, not a literal — a bar that reads £0 for
	// the first frame and then jumps is the same class of wrong number
	// as one that always reads £0.
	st.setTreasury(st.treasury)

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

	// FEAT-208: register every view compose publishes, in the fixed,
	// documented viewRegistrationOrder — same "resolve every producer
	// before the first RegisterView call" discipline the phase-hook loop
	// above already applies (AC-4's "zero hooks left behind" extended to
	// "zero views left behind"): every fn(st) closure above was already
	// built when viewRegistrationOrder's literal was constructed, so a
	// RegisterView failure here (e.g. a duplicate name — cannot happen
	// today, the slice's names are all distinct literals) never leaves a
	// partially-registered view table any more than a phase-hook failure
	// leaves a partially-wired engine.
	for _, reg := range viewRegistrationOrder {
		if err := e.RegisterView(reg.name, reg.fn(st)); err != nil {
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

// idNamespaceRangesDisjoint is Wire's id-namespace-seam cross-check (FEAT-169
// destructive-review REJECT finding), extracted as a pure function so it is
// independently unit-testable against synthetic values — the real
// constants (citizens.FertilityChildIDBase, attract.MigrantIDBase) cannot
// be overridden to exercise the REJECTING branch of this check any other
// way. Reports whether the fertility child-id range starts at least twice
// as far out as the migrant-id range starts, which — given both ranges
// extend to infinity and migrantBase is itself compose's own lower-range
// boundary starting at 1 — is exactly the condition that keeps
// [migrantBase, fertilityBase) and [fertilityBase, ...) disjoint from
// [1, migrantBase) AND from each other. The historical bug (both bases
// independently at 1<<62) fails this: with migrantBase=2^62,
// fertilityBase(2^62) is NOT >= 2*migrantBase(2^63).
func idNamespaceRangesDisjoint(fertilityChildIDBase, migrantIDBase uint64) bool {
	return fertilityChildIDBase >= 2*migrantIDBase
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
// simState holds no sync.Mutex. Every access to simState's OWN plain
// fields (treasury, citizenWealth, peopleDelta, moneyDelta, ...) is
// single-goroutine by construction: only shard 0 of each hook's RunShard
// touches them (the invariant's SnapshotProvider, the spawn/finance
// ApplyEffect barrier work), and the phase pipeline runs phases
// sequentially — the daily phase's det.RunPhase joins its workers before
// the monthly phases start.
//
// CORRECTION (F3, independent round r1, FEAT-208 increment 1): this
// "single-goroutine by construction" property does NOT extend to
// buildServicesCapacityDemandPatch (services_publish.go) or any future
// view-publishing method this file adds — those run on the subscription
// pump goroutine (engine/core.StartSubscriptionPump), CONCURRENTLY with
// the phase-pipeline goroutines this comment describes, not sequenced
// with them. That is safe ONLY because those methods read through the
// held module's OWN synchronization (st.services is a
// *services.ServicesAPI, and every accessor it exposes — ServiceIDs,
// Capacity, Demand — takes its own sync.RWMutex internally); they never
// touch simState's own unguarded plain fields. See
// engine/core.ViewPatchFunc's doc comment (subscribe.go) for the general
// contract this specific case satisfies. A future ViewPatchFunc-backed
// method that read one of simState's own plain fields directly (e.g.
// st.treasury) WOULD be a real, unguarded data race against the phase
// pipeline — this file's discipline (§3.3 of the design) of only ever
// reading through an already-guarded *XxxAPI accessor is load-bearing,
// not incidental.
//
// A mutex on simState itself would be a copy hazard with no copy risk to
// guard (and would make this type an astgate SEC-020 candidate for
// nothing) — the fix for the concurrency gap above is "read through the
// module's own lock," never "add a lock here."
//
// BUG-324 addendum: the top bar needs the player's MONEY, and the
// correction above is precisely why it could not simply read
// st.treasury. It also could not read engine.finance's AcctTreasury
// ledger account instead: baseline one never funds that account (Wire
// seeds st.treasury, and financeHook moves st.treasury — the finance
// ledger's own accounts stay at zero, which is why "f2.finance"'s
// balance sheet publishes zeros today too). So the money the player
// actually has lives in an unguarded plain field, and both honest
// alternatives — publish a zero, or drop money from the bar — were
// worse than making the real figure safely readable. treasuryPub below
// is that: a publish-only atomic MIRROR of st.treasury, written by
// setTreasury alongside every st.treasury write, read lock-free by the
// subscription pump. It is not a second source of truth (nothing in the
// simulation ever reads it) and it cannot drift, because st.treasury is
// never assigned outside setTreasury.
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

	// finance is the shared *finance.FinanceAPI instance constructed in
	// Wire and handed to attract (SetFinance) and, since FEAT-207, to
	// extcommute's FinanceSeam adapter (extCommuteFinanceSeam). Stored here
	// too so any future compose-owned poster (and tests) can reach the
	// same ledger without re-threading it through every hook constructor.
	finance *finance.FinanceAPI

	// FEAT-167 (docs/planning/icd/engine.attract-terms.md): the three real
	// Safety/LeisureFit/Environment source modules, plus this integration's
	// one new data-driven balance file. attractTerms is read-only after
	// Wire (loadAttractTermsData runs once, at construction) — never
	// re-read per tick.
	crime        *crime.CrimeAPI
	leisure      *leisure.LeisureAPI
	refuse       *refuse.RefuseAPI
	attractTerms attractTermsData

	// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
	// docs/planning/icd/engine.firms-labourmarket.md): the two remaining
	// §11 term source modules, constructed in Wire alongside
	// crime/leisure/refuse above.
	services *services.ServicesAPI
	firms    *firms.FirmsAPI

	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): the composed
	// engine.traffic dependency (traffic_wire.go). trafficTickHook calls
	// AdvanceTick on it once per simulated day; extCommuteTrafficSeamAdapter
	// (constructed in Wire, held by extCommute's TrafficSeam field, not
	// here) reads its CommuteHours live. Stored here too — mirroring
	// finance's own doc comment above — so a future demand-generating
	// hook this package adds, and today's tests via Composition.Traffic(),
	// can reach the same instance without re-threading it.
	traffic *traffic.TrafficAPI

	// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): the
	// off-map external-commuting module, wired with its three seam
	// adapters (extcommute_wire.go). Baseline one routes no gameplay
	// command to Assign/Release/InCommute yet (ICD §12 open decision 4 —
	// out of this ICD's scope); this field exists so a future gameplay
	// seam, and today's tests, can reach it.
	extCommute *extcommute.ExtCommuteAPI

	// leisureVenuesRegistered tracks which completed engine.build
	// ZoneEntertainment order IDs are CURRENTLY bridged into an open
	// engine.leisure venue (registerLeisureVenues below) — membership is
	// removed again when the underlying structure is demolished (destructive
	// round r1 F2 fix), so this is a live "still open" set, not a
	// once-true-forever registration log.
	leisureVenuesRegistered map[uint64]bool

	// people conservation ledger
	peopleOpening int64
	peopleDelta   int64

	// money conservation ledger (total money = treasury + citizen wealth)
	moneyOpening  int64
	moneyDelta    int64
	treasury      int64
	citizenWealth int64

	// treasuryPub is the BUG-324 publish-only mirror of treasury — see
	// this type's own doc comment for why it exists and why it cannot
	// drift. Write it ONLY via setTreasury; read it ONLY from a
	// ViewPatchFunc (publishedTreasury). The simulation itself must keep
	// reading treasury, so that a stale/forgotten mirror can never
	// change a simulated outcome, only a displayed one.
	treasuryPub atomic.Int64

	// cumulative gross money flow (AC-9)
	moneyFlows int64

	// cumulative consumption delivered (liveness evidence) and net
	// migration applied (liveness evidence) — the "consumption draws" and
	// "migration is attractiveness-driven" observables.
	consumptionDelivered float64
	netMigration         int64

	nextCitizenID uint64

	// vitalBirths/vitalDeaths are the cumulative real fertility/mortality
	// totals folded into peopleDelta so far (liveness evidence, mirrors
	// consumptionDelivered/netMigration above) — the "births/deaths are
	// real, not the old flat-8 fake" observable (FEAT-169). Folded one
	// day-tick's own totals at a time, straight from AdvanceDayTick's
	// return values — see coldPassHook.ApplyEffect's doc comment for why
	// this is NOT batched to the month boundary via VitalEvents.
	vitalBirths int64
	vitalDeaths int64

	// lastClosedTick tracks the last tick for which ledgers were closed (BUG-288).
	// snapshot() uses this to ensure ledger closing (opening/delta reset) happens
	// exactly once per tick, at the START of that tick's snapshot call.
	lastClosedTick int64

	// Previous closing values from the last snapshot, used to set opening for the
	// current tick before any deltas are recorded.
	previousClosingPop   int64
	previousClosingMoney int64
}

// closeLedgerForTick closes the ledger for the given tick at the START of
// snapshot, before reading state. Uses previousClosing values (set by the
// previous snapshot) as opening for the current tick. Ensures ledger closing
// happens exactly once per tick despite snapshot being called twice on the
// same tick (BUG-288).
func (st *simState) closeLedgerForTick(tick int64) {
	if st.lastClosedTick >= tick {
		return
	}
	// Set opening for this tick to the previous tick's closing.
	// Deltas are reset AFTER reading the snapshot, not here.
	st.peopleOpening = st.previousClosingPop
	st.moneyOpening = st.previousClosingMoney
	st.lastClosedTick = tick
}

// snapshot implements invariant.SnapshotProvider: it builds this tick's
// conservation Snapshot. PURE: calling it twice on same tick returns identical
// snapshot (ledger closing happens once at snapshot START via closeLedgerForTick).
// Called from the invariant hook's RunShard (shard 0 only) — single reader/writer,
// no map iteration, no wall clock (BUG-288).
func (st *simState) snapshot(tick int64) invariant.Snapshot {
	st.closeLedgerForTick(tick)
	closingPop := int64(st.citizens.TotalPopulation(st.cid))

	s := invariant.NewSnapshot(tick)
	s.Readings[invariant.StockPeople] = invariant.StockReading{
		Registered:   true,
		Opening:      st.peopleOpening,
		Closing:      closingPop,
		TrackedDelta: st.peopleDelta,
	}

	totalMoney := num.SatAdd(st.treasury, st.citizenWealth)
	s.Readings[invariant.StockMoney] = invariant.StockReading{
		Registered:   true,
		Opening:      st.moneyOpening,
		Closing:      totalMoney,
		TrackedDelta: st.moneyDelta,
	}

	// goods and vehicles are genuinely zero in baseline one (market has no
	// goods flow yet, traffic does not exist). Report them registered at
	// zero so the full suite runs and balances every tick (AC-10) rather
	// than being skipped.
	s.Readings[invariant.StockGoods] = invariant.StockReading{Registered: true}
	s.Readings[invariant.StockVehicles] = invariant.StockReading{Registered: true}

	// Store closing values for use as opening in the next tick's snapshot.
	// This must happen after reading the snapshot but before returning,
	// so that the NEXT snapshot call can use these values via closeLedgerForTick.
	st.previousClosingPop = closingPop
	st.previousClosingMoney = totalMoney

	// Reset deltas for the next tick. This happens AFTER reading the snapshot
	// so that the snapshot includes the deltas accumulated during this tick.
	// (Deltas are accumulated during the tick via life events, transactions, etc.)
	st.peopleDelta = 0
	st.moneyDelta = 0

	return s
}

// spawnCitizens births count citizens at the given sim month, deterministically
// (sequential IDs, personality drawn from the world seed via
// citizens.InitPersonality). It is the only citizen-mutation path the
// composition uses, routed through CitizensAPI's command surface (GR#20).
func (st *simState) spawnCitizens(month int64, count int) error {
	for i := 0; i < count; i++ {
		id := st.nextCitizenID
		// FEAT-169 ID-SEAM GUARD: id must stay inside compose's own range
		// of the three-way disjoint id map — [1, attract.MigrantIDBase) —
		// never reaching either engine.attract's migrant range
		// [MigrantIDBase, FertilityChildIDBase) or engine.citizens'
		// fertility range [FertilityChildIDBase, ...). Bounded against
		// MigrantIDBase (2^62), NOT FertilityChildIDBase (2^63):
		// destructive-review REJECT found the ORIGINAL guard here checked
		// only the fertility boundary, which would have let compose's
		// counter silently drift into attract's migrant range first
		// without ever tripping. The three id spaces are a documented,
		// verified-disjoint CONTRACT (ICD §12 open decision 2, amended),
		// not a shared allocator. Checked on every mint (cheap: one uint64
		// comparison), including the seed population minted at Wire time —
		// so this single check doubles as both the "startup check" and the
		// "every mint" assertion the ICD calls for, rather than two
		// separate code paths.
		if id >= attract.MigrantIDBase {
			return errs.New(ErrCitizenIDNamespaceSeam, st.cid, map[string]any{
				"id":   id,
				"base": attract.MigrantIDBase,
			})
		}
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

// SingleShard implements core.SingleShardHook (BUG-269): RunShard is a
// nil-op for every shard including 0, so it trivially only "does work"
// on shard 0 (none at all).
func (noopHook) SingleShard() bool { return true }

// coldPassEffect is the daily citizens cold-pass tick marker (FEAT-169).
// Carries no payload data of its own — AdvanceDayTick/VitalEvents derive
// everything they need from citizens' own internal state — it exists only
// to move the "run the cold pass" instruction from RunShard (shard 0) to
// ApplyEffect (the single-goroutine barrier), the same shape every other
// hook in this file uses.
type coldPassEffect struct{}

// coldPassHook is the PhaseHook for citizens' REAL cold pass — per-citizen
// mortality plus FEAT-160 fertility, via CitizensAPI.AdvanceDayTick — REPLACING
// the old spawnHook fake (a flat monthlyBirths=8 births/month with no
// connection to demographics, mortality, or eligibility). Registered
// against core.PhaseDailyTick (see registrationOrder's comment and
// doc.go's "Live-tick wiring" section): AdvanceDayTick already runs
// unconditionally once per day-tick internally to citizens (its own
// amortised 1/30-shards-per-day schedule), and the ICD's T0 update class
// requires the resulting births/deaths land in peopleDelta the SAME tick
// they are computed. Only shard 0 emits the effect; ApplyEffect drives the
// cold pass and folds THAT TICK's own births/deaths (AdvanceDayTick's
// return values, not VitalEvents' monthly-completed totals — see
// ApplyEffect's doc comment for why) into the people conservation ledger
// every day-tick — exactly the role attractHook plays for migration
// admits, just at daily rather than monthly granularity.
type coldPassHook struct {
	st *simState
}

func (h *coldPassHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: coldPassEffect{}}}, nil
}

func (h *coldPassHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(coldPassEffect); !ok {
		return
	}
	st := h.st
	births, deaths, err := st.citizens.AdvanceDayTick(st.cid)
	if err != nil {
		// AdvanceDayTick's only real failure mode is a copied-handle
		// rejection (MET-G004), which cannot happen given compose's
		// single-owner st.citizens field; log loudly rather than swallow
		// (GR#1) instead of a silent no-op.
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "cause": err.Error()})
		return
	}

	// Fold THIS TICK's own births/deaths into peopleDelta immediately — NOT
	// batched to the month boundary via VitalEvents. ICD deviation, with
	// reason (docs/planning/icd/engine.citizens-coldpass.md §4/§5): the
	// ICD's own §4 floated "pull VitalEvents at the month boundary" as one
	// option, but AdvanceDayTick's mortality/fertility mutations land on
	// the cold store incrementally, one amortised shard-slice per day-tick
	// (A2's amortised cold pass) — so batching the peopleDelta credit to
	// month-end would defer it past the tick the population actually
	// changed on, violating §5's T0 same-tick requirement. This was not
	// theoretical: the deferred-batch design was built first and the
	// invariant's daily conservation check (WithLogSink) caught real
	// violations on every day a death/birth landed outside the last day of
	// the month. AdvanceDayTick's return values (this call's own totals)
	// fix that at the source — see its doc comment.
	st.peopleDelta = num.SatAdd(st.peopleDelta, int64(births))
	st.peopleDelta = num.SatSub(st.peopleDelta, int64(deaths))
	st.vitalBirths = num.SatAdd(st.vitalBirths, int64(births))
	st.vitalDeaths = num.SatAdd(st.vitalDeaths, int64(deaths))
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0. This matches the ICD's §6 Shard
// Scope: AdvanceDayTick is single-call/opaque from compose's point of
// view even though citizens fans its own internal parallel mortality pass
// and sequential fertility pass across many cold shards INSIDE that one
// call — entirely invisible to this hook.
func (h *coldPassHook) SingleShard() bool { return true }

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

	// BUG-355: the ledger F2 reads is FinanceAPI. Open the monthly
	// finance tick FIRST (finance.BeginMonth resets the per-tick
	// transaction log that WagesPosted/TaxRevenue aggregate over) — with
	// no BeginMonth caller the log never cleared, WagesPosted read an
	// ALL-TIME cumulative sum, and attract's HousingAffordability divided
	// that ever-growing figure by households: migrant attractiveness grew
	// linearly forever and the log leaked memory unboundedly. PhaseFinance
	// is the LAST monthly phase (core.MonthlyPhaseOrder), so the tick
	// opened here holds exactly this month's posts when NEXT month's
	// population phase reads WagesPosted — always one month's wage bill.
	var flowed int64
	if st.finance != nil {
		clock, cErr := st.e.Clock()
		if cErr != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "cause": cErr.Error()})
			return
		}
		if err := st.finance.BeginMonth(clock.Month()); err != nil {
			// BeginMonth only fails on a copied handle, which compose's
			// single-owner field makes unreachable; log loudly rather
			// than swallow (GR#1) and skip this month's posting rather
			// than post into a stale tick window.
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "cause": err.Error()})
			return
		}
		if _, err := st.finance.PostWages(finance.Money(monthlyWages)); err == nil {
			flowed = monthlyWages
			if _, err := st.finance.CollectTax(finance.TaxRates{IncomeRate: 10000}, finance.Money(monthlyWages), 0, 0); err == nil {
				flowed += monthlyTax
			}
		}
	}
	// Mirror the LEDGER unconditionally — on success and on rejection
	// alike. A rejected Post leaves the ledger unchanged by contract
	// (finance.Post validates before mutating, never a partial post), so
	// syncing is honest on every path: simState can never diverge from
	// the pot F2 actually reads, and a future partial post (a leg landing
	// without its pair) would be mirrored exactly as it landed rather
	// than replayed from stale fallback deltas. The pair is all-or-nothing
	// today because CollectTax's debit is computed on the wages credited
	// moments earlier (rate <= 100% can never overdraft households).
	st.syncMoneyFromLedger()

	// gross flow (AC-9 "money moved") counts only what actually posted;
	// net delta is zero by construction (both legs are internal transfers)
	// but tracked so the invariant verifies it against the store.
	if flowed > 0 {
		st.moneyFlows = num.SatAdd(st.moneyFlows, flowed)
	}
	st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(monthlyTax, monthlyWages))
}

// seedOpeningBalances posts the baseline-one opening grant into the
// FinanceAPI ledger so F2 is not a permanent zero sheet (BUG-355).
// External is the outside-world source; it is not part of the money stock.
func seedOpeningBalances(f *finance.FinanceAPI, treasury, households int64) error {
	if treasury > 0 {
		if _, err := f.Post(finance.Transaction{
			Description: "baseline-one opening treasury",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(treasury), Category: finance.Category("opening.capital")},
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(treasury), Category: finance.Category("opening.capital")},
			},
		}); err != nil {
			return err
		}
	}
	if households > 0 {
		if _, err := f.Post(finance.Transaction{
			Description: "baseline-one opening household wealth",
			Entries: []finance.Entry{
				{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(households), Category: finance.Category("opening.capital")},
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(households), Category: finance.Category("opening.capital")},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func ledgerBalance(f *finance.FinanceAPI, id finance.AccountID) int64 {
	if f == nil {
		return 0
	}
	bal, ok := f.AccountBalance(id)
	if !ok {
		return 0
	}
	return int64(bal)
}

func (st *simState) syncMoneyFromLedger() {
	if st.finance == nil {
		return
	}
	// BUG-324: treasury is written ONLY through setTreasury so the
	// publish-only mirror cannot drift from the simulated pot.
	st.setTreasury(ledgerBalance(st.finance, finance.AcctTreasury))
	st.citizenWealth = ledgerBalance(st.finance, finance.AcctHouseholds)
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *financeHook) SingleShard() bool { return true }

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

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *consumptionHook) SingleShard() bool { return true }

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

// buildEffect is the daily build-queue tick marker.
type buildEffect struct {
	month int64
}

// buildHook is the baseline-one build hook (MOD-026, real; cadence fixed by
// BUG-268): it advances the build queue one simulation day per simulation
// day via BuildAPI.Tick, registered against PhaseDailyTick so its cadence
// matches BuildAPI.Tick's own one-day-per-call contract (build.go's
// daysPerTick). Passing clock.Month() every day is safe — build.go's Tick
// only uses its month parameter for >=0 validation, never as a "did the
// month change" gate. Zone/Build commands themselves arrive through the
// gameplay-command seam (handleGameplay), not this phase hook — this hook
// only elapses the queue.
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
	h.st.registerLeisureVenues()
}

// SingleShard implements core.SingleShardHook (BUG-269 — this is the
// hook the regression report named directly): RunShard returns (nil,
// nil) for every shard except 0 (see above) — the only Effect ever
// emitted comes from shard 0.
func (h *buildHook) SingleShard() bool { return true }

// registerLeisureVenues bridges engine.build's completed ZoneEntertainment
// orders into engine.leisure's venue registry (FEAT-167 ICD §12 open
// decision 4's "fourth edge gap" — mediated entirely by compose; no direct
// engine.build -> engine.leisure edge is registered in code.json). Called
// once per day-tick after buildAPI.Tick (buildHook.ApplyEffect, above): a
// completed entertainment-zone build order becomes exactly one leisure
// venue, opened once (idempotent via leisureVenuesRegistered) at the
// data-driven bridge capacity (data/attract_terms.json's
// leisure.bridgeVenueCapacityUnits, GR#15) in the community category — a
// deliberately coarse composite (engine.build's zone catalogue carries one
// generic "entertainment" type today, with no venue-category sub-signal),
// never an invented per-building capacity. Iterates buildAPI.Queue()'s
// insertion-order slice, never a map (GR#21).
//
// Destructive round r1 (F2) fix: BuildAPI.Queue() keeps every order
// FOREVER, including one whose structure a later Demolish command already
// deleted (SubmitDemolishCommand clears zoneState/structures, never the
// queue entry) — an order snapshot still reporting
// complete+ZoneEntertainment is therefore NOT proof a venue should exist.
// The only live-truth signal is BuildAPI.Structure(tile, local): whether
// THIS order's ID is still the standing structure on its cell. Every
// completed entertainment order is reconciled against that truth every
// call: currently-standing-but-not-yet-registered opens a venue,
// registered-but-no-longer-standing (demolished, or replaced by a later
// order on the same cell) removes it — so demolishing an entertainment
// zone measurably lowers LeisureFit again, and a later rebuild
// re-registers cleanly (idempotent both ways).
func (st *simState) registerLeisureVenues() {
	for _, order := range st.buildAPI.Queue() {
		if order.Zone != build.ZoneEntertainment || order.Status != build.OrderComplete {
			continue
		}
		venueID := uint64(order.ID)
		structID, standing := st.buildAPI.Structure(order.Tile, order.Local)
		stillStanding := standing && structID == order.ID

		switch {
		case stillStanding && !st.leisureVenuesRegistered[venueID]:
			v := leisure.Venue{
				ID:       venueID,
				Category: leisure.CategoryCommunity,
				District: 0,
				Capacity: st.attractTerms.Leisure.BridgeVenueCapacityUnits,
			}
			if err := st.leisure.OpenVenue(v, st.cid); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "leisure", "cause": err.Error()})
				continue
			}
			st.leisureVenuesRegistered[venueID] = true
		case !stillStanding && st.leisureVenuesRegistered[venueID]:
			if err := st.leisure.RemoveVenue(venueID, st.cid); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "leisure", "cause": err.Error()})
				continue
			}
			delete(st.leisureVenuesRegistered, venueID)
		}
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

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *attractHook) SingleShard() bool { return true }

// applyMigration pushes all five compose-owned §11 terms, then runs one
// monthly migration step. Safety/LeisureFit/Environment are real, computed
// this same month from engine.crime/engine.leisure/engine.refuse (FEAT-167
// wave 1, docs/planning/icd/engine.attract-terms.md). ServiceCoverage/
// JobAvailability are ALSO now real (FEAT-167 completion,
// docs/planning/icd/engine.services-coverage.md /
// engine.firms-labourmarket.md), computed from engine.services/
// engine.firms — see serviceCoverageTerm/jobAvailabilityTerm
// (servicesfirms_wire.go) for the honest scope-limit each carries (no
// automatic build->services/firm-founding bridge is wired into compose
// yet, so both read their formula's zero-signal edge case until that
// separate integration lands). HousingVacancy/JunctionThroughput are
// unbounded placeholders until households/logistics produce real capacity
// signals.
func (st *simState) applyMigration(month int64) (attract.MigrationResult, error) {
	safety, err := st.safetyTerm(month)
	if err != nil {
		return attract.MigrationResult{}, err
	}
	leisureFit, err := st.leisureFitTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	environment, err := st.environmentTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	serviceCoverage, err := st.serviceCoverageTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	jobAvailability, err := st.jobAvailabilityTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}

	if err := st.attract.SetTermInputs(attract.TermInputs{
		JobAvailability:        jobAvailability,
		ServiceCoverage:        serviceCoverage,
		Environment:            environment,
		LeisureFit:             leisureFit,
		Safety:                 safety,
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

// safetyTerm advances engine.crime one month against the single citywide
// district (population-driven EligiblePool half only — ICD §12 open
// decision 2: every other DistrictInput driver has no compose-owned real
// source yet, so it stays at its documented zero-neutral default) and
// returns the resulting [0,100] SafetyTerm — higher population -> larger
// EligiblePool -> more generation -> lower Safety, a real monotonic
// dependency, never a flat constant.
func (st *simState) safetyTerm(month int64) (float64, error) {
	population := int64(st.citizens.TotalPopulation(st.cid))
	in := crime.DistrictInput{
		District:     citywideCrimeDistrict,
		EligiblePool: population,
	}
	if err := st.crime.AdvanceMonth(month, []crime.DistrictInput{in}, crime.SecurityInput{}); err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "crime"})
	}
	safety, err := st.crime.SafetyTerm(citywideCrimeDistrict)
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "crime"})
	}
	return safety, nil
}

// leisureFitTerm queries engine.leisure's citywide LeisureFitAggregate
// (venue mix vs the would-be-migrant taste distribution, leisure's own
// data-loaded Config.DefaultTaste — no new data file needed, ICD §3) and
// scales its [0,1] result to attract's [0,100] term scale. Zero registered
// venues yields a low aggregate; the registerLeisureVenues bridge (above)
// is what makes this move as the player builds entertainment zones.
func (st *simState) leisureFitTerm() (float64, error) {
	taste := st.leisure.PopulationTaste(st.cid)
	fit, err := st.leisure.LeisureFitAggregate(taste, st.cid)
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "leisure"})
	}
	return 100 * fit, nil
}

// environmentTerm generates one month's waste into the single citywide
// refuse cell (population-driven, mirroring safetyTerm's EligiblePool
// half) and folds the resulting uncollected+disposal-backlog tonnage
// (summed across engine.refuse's three §25 streams, refuseStreams) through
// the data-driven half-saturation curve (data/attract_terms.json's
// environment.pollutionHalfSaturationKg, GR#15) — the same curve shape
// engine.crime's own SafetyTerm uses. Baseline one never wires a refuse
// collection round (no engine.logistics/engine.services dependency is
// injected into refuse here), so the generated waste is always starved of
// collection: uncollected tonnage — and therefore this term's degradation —
// grows monotonically with population, a real dependency never a constant.
func (st *simState) environmentTerm() (float64, error) {
	population := float64(st.citizens.TotalPopulation(st.cid))
	if err := st.refuse.Generate(citywideRefuseCellID, population); err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
	}
	var outstanding int64
	for _, s := range refuseStreams {
		uncollected, err := st.refuse.TonnesUncollected(s)
		if err != nil {
			return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
		}
		backlog, err := st.refuse.TonnesDisposalBacklog(s)
		if err != nil {
			return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
		}
		outstanding = num.SatAdd(outstanding, num.SatAdd(uncollected, backlog))
	}
	half := st.attractTerms.Environment.PollutionHalfSaturationKg
	total := float64(outstanding)
	pressure := total / (total + half)
	if pressure < 0 {
		pressure = 0
	} else if pressure > 1 {
		pressure = 1
	}
	return 100 * (1 - pressure), nil
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

// --- gameplay command seam (Buy/Zone/Build/Demolish/SetFunding -> build/world/services) ---

// handleGameplay is the injected core.GameplayCommandHandler. It maps the
// gameplay-intent protocol commands onto the owning modules' command
// surfaces: Buy -> world.PurchaseTile, Zone/Build/Demolish ->
// BuildAPI.Submit*Command, SetFunding -> ServicesAPI.SetFunding (FEAT-208
// increment 3, the pilot command promoting services.set-funding off
// protocol.KindDebug's no-op escape hatch onto this real seam — see
// ui/screens/services/doc.go's gating note, now closed). A nil return
// accepts the command (core turns it into an Accepted CommandResult); a
// non-nil registry error rejects it with that code. This is the ONE place
// gameplay intent meets the real modules (AC-1/GR#20): no runnable path
// routes these kinds around compose.
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
		// wealth. BUG-355: post the same transfer through FinanceAPI so
		// the ledger F2 reads moves with the sim. Fallback keeps the
		// simState pots consistent if the post is rejected (demolish
		// already landed in build); treasury writes go through
		// setTreasury so the BUG-324 publish mirror never drifts.
		if res.Compensation > 0 && st.finance != nil {
			if _, err := st.finance.Post(finance.Transaction{
				Description: "demolish compensation",
				Entries: []finance.Entry{
					{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(res.Compensation), Category: finance.Category("demolish.compensation")},
					{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(res.Compensation), Category: finance.Category("demolish.compensation")},
				},
			}); err == nil {
				st.syncMoneyFromLedger()
			} else {
				st.setTreasury(num.SatSub(st.treasury, res.Compensation))
				st.citizenWealth = num.SatAdd(st.citizenWealth, res.Compensation)
			}
		} else {
			st.setTreasury(num.SatSub(st.treasury, res.Compensation))
			st.citizenWealth = num.SatAdd(st.citizenWealth, res.Compensation)
		}
		st.moneyFlows = num.SatAdd(st.moneyFlows, res.Compensation)
		st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(res.Compensation, res.Compensation))
		return nil
	case protocol.KindSetFunding:
		p, ok := cmd.Payload.(protocol.SetFundingPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		// FEAT-208 increment 3, the pilot command (lead ruling): forwards
		// verbatim to ServicesAPI.SetFunding — no validation duplicated
		// here (GR#3's "the engine validates once"; api.go's SetFunding
		// already hard-rejects non-finite/out-of-[0,1] levels, an
		// unregistered ServiceID, and a not-yet-unlocked service's
		// milestone gate). SetFunding's own errors are already
		// *errs.E values built via serviceErr against this codebase's
		// registered error registry (GR#7), so returning err directly
		// (rather than re-wrapping under a compose-owned code) preserves
		// the already-rendered registry code/display verbatim on the
		// CommandResult — core/commands.go's toErrorRef type-asserts
		// *errs.E directly, exactly the same shape Zone/Build/Demolish's
		// own pass-through errors already take above.
		return st.services.SetFunding(services.ServiceID(p.ServiceID), p.Level)
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
