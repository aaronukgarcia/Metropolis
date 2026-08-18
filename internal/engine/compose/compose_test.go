package compose

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

const testMonths = 12
const testTicks = testMonths * int64(core.DailyTicksPerMonth)

// newTestEngine builds a composed engine at the given seed, wiring any
// supplied invariant options. It is the shared harness for the headless
// liveness tests below.
func newTestEngine(t *testing.T, seed uint64, opts ...invariant.HookOption) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{InvariantOpts: opts})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

// --- AC-2: deterministic, documented registration order ---

func TestRegistrationOrder_MatchesDocumented(t *testing.T) {
	want := []string{"world", "citizens", "market", "consumption", "finance", "build", "attract", "invariant"}
	got := RegistrationOrder()
	if len(got) != len(want) {
		t.Fatalf("RegistrationOrder() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RegistrationOrder()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if BaselineOneHookCount() != len(want) {
		t.Fatalf("BaselineOneHookCount() = %d, want %d", BaselineOneHookCount(), len(want))
	}
}

func TestWire_RegistersAllHooksInDocumentedOrder(t *testing.T) {
	var phases []core.PhaseKind
	e := core.NewEngine(core.WithPoolSize(1), core.WithPhaseObserver(func(kind core.PhaseKind, _, _ int64) {
		phases = append(phases, kind)
	}))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp == nil {
		t.Fatal("Wire returned a nil Composition")
	}

	if got := e.HookCount(); got != BaselineOneHookCount() {
		t.Fatalf("HookCount() = %d, want %d", got, BaselineOneHookCount())
	}

	// Drive one month: 30 daily ticks + one monthly phase pipeline.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	// PhaseDailyTick must appear once per tick (AC-5: invariant every tick).
	daily := 0
	for _, p := range phases {
		if p == core.PhaseDailyTick {
			daily++
		}
	}
	if daily != int(core.DailyTicksPerMonth) {
		t.Fatalf("PhaseDailyTick visited %d times over %d ticks, want %d", daily, core.DailyTicksPerMonth, core.DailyTicksPerMonth)
	}

	// The monthly phases must appear in the fixed engine.core order,
	// proving the hooks landed on the documented phases (world->production,
	// market/consumption->consumption-shortfall, citizens/attract->population,
	// build->land-value-decay, finance->finance).
	var monthly []core.PhaseKind
	for _, p := range phases {
		if p != core.PhaseDailyTick {
			monthly = append(monthly, p)
		}
	}
	wantMonthly := []core.PhaseKind{
		core.PhaseProduction, core.PhaseLogisticsSettlement, core.PhaseConsumptionShortfall,
		core.PhasePopulation, core.PhaseLandValueDecay, core.PhaseFinance,
	}
	if len(monthly) != len(wantMonthly) {
		t.Fatalf("monthly phase sequence = %v, want %v", monthly, wantMonthly)
	}
	for i := range wantMonthly {
		if monthly[i] != wantMonthly[i] {
			t.Fatalf("monthly phase %d = %q, want %q (full: %v)", i, monthly[i], wantMonthly[i], monthly)
		}
	}
}

// --- AC-3: no double-register ---

func TestWire_DoubleComposeReturnsAlreadyComposed(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	if _, err := Wire(e, nil); err != nil {
		t.Fatalf("first Wire: %v", err)
	}
	before := e.HookCount()

	_, err := Wire(e, nil)
	if err == nil {
		t.Fatal("second Wire returned nil, want ErrAlreadyComposed")
	}
	if !errors.Is(err, &errs.E{Code: ErrAlreadyComposed}) {
		t.Fatalf("second Wire error = %v, want ErrAlreadyComposed", err)
	}
	if after := e.HookCount(); after != before {
		t.Fatalf("HookCount changed on the rejected second Wire: %d -> %d", before, after)
	}
}

// --- AC-4: a missing/failing module fails loudly ---

func TestWire_FailingMarketModuleReturnsRegistryError(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	_, err := Wire(e, &Deps{
		LoadMarket: func(correlationID string) (*market.MarketAPI, error) {
			return nil, errs.New("MET-G801", correlationID, map[string]any{"module": "market", "cause": "injected failure"})
		},
	})
	if err == nil {
		t.Fatal("Wire returned nil for a failing market module")
	}
	var e2 *errs.E
	if !errors.As(err, &e2) {
		t.Fatalf("Wire error %v is not a *errs.E", err)
	}
	if e2.Code != ErrModuleFailed {
		t.Fatalf("Wire error code = %q, want %q (ErrModuleFailed)", e2.Code, ErrModuleFailed)
	}
	if e2.Ctx["module"] != "market" {
		t.Fatalf("Wire error ctx[module] = %v, want %q", e2.Ctx["module"], "market")
	}
}

func TestWire_PartialWiringNotLeftBehind(t *testing.T) {
	// A failing module must not leave a partially-wired engine behind
	// (AC-4: "does NOT leave a partially-wired engine that still succeeds").
	e := core.NewEngine(core.WithPoolSize(1))
	_, err := Wire(e, &Deps{
		LoadMarket: func(correlationID string) (*market.MarketAPI, error) {
			return nil, errs.New("MET-G801", correlationID, map[string]any{"module": "market", "cause": "injected failure"})
		},
	})
	if err == nil {
		t.Fatal("Wire returned nil for a failing market module")
	}
	if got := e.HookCount(); got != 0 {
		t.Fatalf("engine left with %d hooks after a failed Wire, want 0 (no partial wiring)", got)
	}
}

func TestRequiredModules_CoversFullBaselineOne(t *testing.T) {
	// The declared required-module list must be the full baseline-one set;
	// a module silently absent from it would be a quiet N-1 success (AC-4).
	want := map[string]bool{
		"world": true, "citizens": true, "market": true, "consumption": true,
		"finance": true, "build": true, "attract": true, "invariant": true,
	}
	got := RegistrationOrder()
	if len(got) != len(want) {
		t.Fatalf("required-module list has %d entries, want %d", len(got), len(want))
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("required-module list contains unexpected module %q", name)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("required-module list is missing %v", want)
	}
}

// --- AC-5: invariant wired every tick ---

func TestWire_InvariantFiresEveryTick(t *testing.T) {
	var daily atomic.Int64
	e := core.NewEngine(core.WithPoolSize(1), core.WithPhaseObserver(func(kind core.PhaseKind, _, _ int64) {
		if kind == core.PhaseDailyTick {
			daily.Add(1)
		}
	}))
	if _, err := Wire(e, nil); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	const n = 7
	if err := e.AdvanceTicks(errs.NewCorrelationID(), n); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if got := daily.Load(); got != n {
		t.Fatalf("invariant fired %d times over %d ticks, want %d (once per tick)", got, n, n)
	}
}

// --- AC-6: sealing discipline ---

func TestWire_SealedReturnsWiringAfterSeal(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	// Seal the engine by driving one tick with zero hooks registered.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks (to seal): %v", err)
	}

	_, err := Wire(e, nil)
	if err == nil {
		t.Fatal("Wire on a sealed Engine returned nil")
	}
	var e2 *errs.E
	if !errors.As(err, &e2) {
		t.Fatalf("Wire error %v is not a *errs.E", err)
	}
	if e2.Code != ErrWiringAfterSeal {
		t.Fatalf("Wire error code = %q, want %q (ErrWiringAfterSeal)", e2.Code, ErrWiringAfterSeal)
	}
	if !errors.Is(err, &errs.E{Code: core.ErrEngineSealed}) {
		t.Fatalf("Wire error does not unwrap to core.ErrEngineSealed: %v", err)
	}
}

// --- AC-8 / AC-9 / AC-10: headless N months, a living city ---

func TestHeadless_TwelveMonthsPopulationGrows(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if seed := comp.Population(); seed != seedCitizenCount {
		t.Fatalf("seed population = %d, want %d", seed, seedCitizenCount)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if got, start := comp.Population(), seedCitizenCount; got <= start {
		t.Fatalf("population after %d months = %d, want strictly greater than seed %d", testMonths, got, start)
	}
}

func TestHeadless_TwelveMonthsMoneyMoves(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if got := comp.MoneyFlows(); got <= 0 {
		t.Fatalf("cumulative money flow after %d months = %d, want non-zero (money moved)", testMonths, got)
	}
	// The conserved total must be unchanged (the budget closes).
	wantTotal := int64(initialTreasury) + int64(initialCitizenWealth)
	if got := comp.Treasury() + comp.CitizenWealth(); got != wantTotal {
		t.Fatalf("total money = %d, want conserved %d", got, wantTotal)
	}
}

func TestHeadless_TwelveMonthsConservationHoldsEveryTick(t *testing.T) {
	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(42), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{InvariantOpts: []invariant.HookOption{
		invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
	}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp == nil {
		t.Fatal("Wire returned nil Composition")
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations over %d ticks, want 0 on every tick", got, testTicks)
	}
}

// --- AC-11: determinism carried through the composition ---

func TestHeadless_DeterministicAcrossRuns(t *testing.T) {
	run := func() ([32]byte, int64, int64) {
		e := core.NewEngine(core.WithWorldSeed(12345), core.WithPoolSize(1))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
		return comp.PopulationHash(), comp.MoneyFlows(), comp.Treasury()
	}

	h1, f1, tr1 := run()
	h2, f2, tr2 := run()
	if h1 != h2 {
		t.Fatalf("population hash differs across same-seed runs:\n%x\n%x", h1, h2)
	}
	if f1 != f2 || tr1 != tr2 {
		t.Fatalf("money state differs across same-seed runs: flows %d/%d treasury %d/%d", f1, f2, tr1, tr2)
	}
}

// --- AC-19: stub PhaseHook contract (shard-safety + determinism) ---

func TestStubHooks_ShardSafetyAndDeterminism(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(7), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	st := comp.state

	hooks := []core.PhaseHook{
		noopHook{name: "world", st: st},
		&spawnHook{st: st, name: "citizens", count: monthlyBirths},
		noopHook{name: "market", st: st},
		&consumptionHook{st: st},
		&financeHook{st: st},
		&buildHook{st: st},
		&attractHook{st: st},
	}
	for _, h := range hooks {
		// Non-zero shards must produce no effects (shard-local discipline).
		for _, shard := range []int{1, 5, 255} {
			effects, err := h.RunShard(shard)
			if err != nil {
				t.Fatalf("%T.RunShard(%d): %v", h, shard, err)
			}
			if len(effects) != 0 {
				t.Fatalf("%T.RunShard(%d) returned %d effects, want 0 (shard-local only)", h, shard, len(effects))
			}
		}
		// Shard 0 must be deterministic: two calls produce identical effects.
		a, errA := h.RunShard(0)
		b, errB := h.RunShard(0)
		if errA != nil || errB != nil {
			t.Fatalf("%T.RunShard(0): %v / %v", h, errA, errB)
		}
		if len(a) != len(b) {
			t.Fatalf("%T.RunShard(0) effect count differs: %d vs %d", h, len(a), len(b))
		}
		for i := range a {
			if a[i].Sequence != b[i].Sequence {
				t.Fatalf("%T.RunShard(0) effect %d sequence differs: %d vs %d", h, i, a[i].Sequence, b[i].Sequence)
			}
		}
	}
}

// --- FEAT-083: the real hooks are wired, not stubs ---

func TestHeadless_ConsumptionDraws(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	got := comp.ConsumptionDelivered()
	t.Logf("consumption delivered after %d months = %f (population %d)", testMonths, got, comp.Population())
	if got <= 0 {
		t.Fatalf("consumption delivered after %d months = %f, want > 0 (real draw, not the old noop)", testMonths, got)
	}
}

func TestHeadless_MigrationIsAttractivenessDriven(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	got := comp.NetMigration()
	t.Logf("net migration after %d months = %d (old +2/month stub would be %d; population %d)", testMonths, got, 2*testMonths, comp.Population())
	if got <= 0 {
		t.Fatalf("net migration after %d months = %d, want > 0 (A > A_world inflow)", testMonths, got)
	}
	// The old attract stub was a hardcoded +2/month; the real attract hook
	// must not reproduce that fixed figure.
	if got == int64(2*testMonths) {
		t.Fatalf("net migration after %d months = %d, exactly the hardcoded +2/month stub — migration is not attractiveness-driven", testMonths, got)
	}
}

func TestGameplay_ZoneAndBuildAccepted(t *testing.T) {
	e, _ := newTestEngine(t, 42)

	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-buy"),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: protocol.CellRef{X: 0, Y: 0}},
	}
	if res := e.HandleCommand(buy); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}

	zone := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-zone"),
		Kind:            protocol.KindZone,
		Payload:         protocol.ZonePayload{Cell: protocol.CellRef{X: 0, Y: 0}, ZoneType: "dwelling"},
	}
	if res := e.HandleCommand(zone); !res.Accepted {
		t.Fatalf("Zone rejected (want no MET-E009): %+v", res.Error)
	}

	buildCmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-build"),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: protocol.CellRef{X: 0, Y: 0}, BuildingType: "dwelling"},
	}
	if res := e.HandleCommand(buildCmd); !res.Accepted {
		t.Fatalf("Build rejected (want no MET-E009): %+v", res.Error)
	}
}

// --- BUG-266: demolish compensation must move money, not vanish ---------

// TestGameplay_DemolishCreditsCompensation drives Buy -> Zone -> Build ->
// (Tick the build queue to completion) -> Demolish through the REAL
// handleGameplay path (e.HandleCommand, the same seam every runnable top
// uses) and asserts the returned DemolishResult.Compensation actually moves
// money: citizenWealth increases by exactly the compensation and treasury
// decreases by exactly the compensation (a transfer, mirroring financeHook's
// SatAdd/SatSub idiom), proving the destructive finding (SubmitDemolishCommand's
// result silently discarded, GR#1 "money conservation") is fixed.
func TestGameplay_DemolishCreditsCompensation(t *testing.T) {
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	// Provision the build module's materials draw generously so the order
	// completes within the Tick loop below, rather than sitting
	// materials-pending forever against an empty default stock.
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(7), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	cell := protocol.CellRef{X: 3, Y: 3}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("demolish-buy"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("demolish-zone"),
		Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Zone rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("demolish-build"),
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}

	// Advance the build queue directly (same *build.BuildAPI the buildHook
	// drives monthly via BuildAPI.Tick) until the order completes. compose's
	// own test file has package-level access to simState's unexported
	// fields, same as build's own tests loop Tick against a provisioned
	// logistics stock.
	tile := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	local := world.CellLocal{Row: cell.Y, Col: cell.X}
	completed := false
	for i := int64(0); i < 300; i++ {
		if err := comp.state.buildAPI.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatalf("build order never completed after 300 ticks — cannot exercise demolish")
	}

	wantCompensation, err := comp.state.buildAPI.PurchasePrice(tile, local)
	if err != nil {
		t.Fatalf("PurchasePrice: %v", err)
	}
	if wantCompensation <= 0 {
		t.Fatalf("PurchasePrice = %d, want > 0 (test cannot detect a missing credit against a zero figure)", wantCompensation)
	}

	treasuryBefore := comp.Treasury()
	wealthBefore := comp.CitizenWealth()

	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("demolish-demolish"),
		Kind: protocol.KindDemolish, Payload: protocol.DemolishPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Demolish rejected: %+v", res.Error)
	}

	gotWealthDelta := comp.CitizenWealth() - wealthBefore
	if gotWealthDelta != wantCompensation {
		t.Fatalf("citizenWealth delta after demolish = %d, want exactly %d (the compensation) — DemolishResult.Compensation was discarded", gotWealthDelta, wantCompensation)
	}
	gotTreasuryDelta := treasuryBefore - comp.Treasury()
	if gotTreasuryDelta != wantCompensation {
		t.Fatalf("treasury delta after demolish = %d, want exactly %d (the compensation)", gotTreasuryDelta, wantCompensation)
	}
}

// --- BUG-267: a re-wrapped rejection must render, not leak placeholders ---

// TestGameplay_BuyRejectionDisplayHasNoLiteralPlaceholders drives a Buy
// rejection (purchasing an already-owned tile) through the REAL
// handleGameplay path and asserts the rendered Display string contains no
// literal "{" — proving MET-E404's {tile}/{cause} placeholders (or
// whichever mechanism replaces the re-wrap) actually substituted, rather
// than compose re-wrapping engine.world's rejection under the SAME code
// with a mismatched ctx key ("display") that never matches the template's
// real placeholders.
func TestGameplay_BuyRejectionDisplayHasNoLiteralPlaceholders(t *testing.T) {
	e, _ := newTestEngine(t, 99)

	cell := protocol.CellRef{X: 5, Y: 5}
	first := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("dup-buy-1"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}
	if res := e.HandleCommand(first); !res.Accepted {
		t.Fatalf("first Buy rejected: %+v", res.Error)
	}

	// Re-buying the same, now-owned tile must be rejected (engine.world's
	// ErrPurchaseRejected / MET-E404, cause "already owned").
	second := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("dup-buy-2"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}
	res := e.HandleCommand(second)
	if res.Accepted {
		t.Fatalf("second Buy on an already-owned tile was accepted, want rejected")
	}
	if res.Error == nil {
		t.Fatalf("second Buy rejected with a nil Error")
	}
	// MET-E404's own template placeholders are {tile} and {cause}; a
	// TileCoord's default fmt formatting ("{15 15}") also contains braces,
	// so the failure signature to check for is the LITERAL unrendered
	// placeholder tokens, not "any brace in the string".
	for _, placeholder := range []string{"{tile}", "{cause}"} {
		if strings.Contains(res.Error.Display, placeholder) {
			t.Fatalf("rejected Buy Display = %q, contains the unrendered template placeholder %q", res.Error.Display, placeholder)
		}
	}
	// The rendered text must still carry the real cause through, proving
	// this is a genuine pass-through of world's Display and not an empty
	// or unrelated string.
	if !strings.Contains(res.Error.Display, "already owned") {
		t.Fatalf("rejected Buy Display = %q, want it to contain the underlying cause %q", res.Error.Display, "already owned")
	}
}
