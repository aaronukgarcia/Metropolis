package compose

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
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
	want := []string{"world", "traffic", "citizens", "market", "consumption", "finance", "build", "attract", "invariant"}
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
	// market/consumption->consumption-shortfall, attract->population,
	// finance->finance). PhaseLandValueDecay still fires here — the phase
	// observer runs once per phase regardless of hook count — but neither
	// build nor citizens has a hook registered against it any more: BUG-268
	// moved build to PhaseDailyTick (asserted separately by
	// TestBUG268_BuildAdvancesDaily) and FEAT-169 moved citizens there too
	// (asserted separately below).
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
		"world": true, "traffic": true, "citizens": true, "market": true, "consumption": true,
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
	// FEAT-1972079927 inc1 (Q4 consumption spend): money now legitimately
	// crosses OUT of the treasury+households pair into AcctFirms (and
	// back again via commercial/industrial tax) — a real circulation the
	// old wage/tax-only stub never had. Treasury+CitizenWealth alone is no
	// longer the full conserved total; TotalMoneyInCirculation (which sums
	// EVERY RoleMoney account, including firms) is the AC-10 conservation
	// figure that must stay unchanged — the budget still closes, the money
	// just now visibly passes through firms on its way around the loop.
	wantTotal := int64(initialTreasury) + int64(initialCitizenWealth)
	if got := int64(comp.state.finance.TotalMoneyInCirculation()); got != wantTotal {
		t.Fatalf("TotalMoneyInCirculation = %d, want conserved %d", got, wantTotal)
	}
	// Treasury+CitizenWealth must have CHANGED from the opening total —
	// proof that money actually moved to firms (Q4) rather than staying a
	// closed treasury<->households loop.
	if got := comp.Treasury() + comp.CitizenWealth(); got == wantTotal {
		t.Fatalf("Treasury+CitizenWealth = %d, unchanged from opening %d — consumption spend never left the treasury/households pair", got, wantTotal)
	}
}

// TestHeadless_TwelveMonthsLedgerMatchesSimState is BUG-355: the
// FinanceAPI treasury the F2 screen reads must equal Composition.Treasury
// after a real run. Before the fix the hook mutated simState only and
// AccountBalance(AcctTreasury) stayed 0.
func TestHeadless_TwelveMonthsLedgerMatchesSimState(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	led := ledgerBalance(comp.state.finance, finance.AcctTreasury)
	if led != comp.Treasury() {
		t.Fatalf("FinanceAPI treasury = %d, Composition.Treasury = %d — two money pots (BUG-355)", led, comp.Treasury())
	}
	if led == 0 {
		t.Fatal("FinanceAPI treasury is 0 after 12 months — F2 would still render a zero sheet")
	}
	hh := ledgerBalance(comp.state.finance, finance.AcctHouseholds)
	if hh != comp.CitizenWealth() {
		t.Fatalf("FinanceAPI households = %d, Composition.CitizenWealth = %d", hh, comp.CitizenWealth())
	}
}

// TestBUG355_WagesPosted_IsPerMonthNotCumulative probes WagesPosted —
// the aggregate attract's HousingAffordability divides by household count
// (attract/api.go snapshotTerms) — at each month boundary of a real run.
// With no production BeginMonth caller nothing ever clears FinanceAPI's
// per-tick transaction log, so the probe reads an ALL-TIME cumulative sum
// growing linearly forever (1e6 -> 2e6 -> 3e6 ...): migrant attractiveness
// drifts up every month and the log leaks memory unboundedly. The compose
// finance hook must open the monthly tick, keeping the figure at exactly
// one month's wage bill.
func TestBUG355_WagesPosted_IsPerMonthNotCumulative(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	for m := int64(1); m <= 3; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
			t.Fatalf("AdvanceTicks(month %d): %v", m, err)
		}
		if got := int64(comp.state.finance.WagesPosted()); got != monthlyWages {
			t.Fatalf("WagesPosted after month %d = %d, want exactly one month's wage bill %d (per-month, not cumulative)", m, got, monthlyWages)
		}
	}
}

// TestBUG355_PostRejection_SimStateMirrorsLedgerAtMonthEnd forces a
// PostWages rejection (treasury drained below the wage bill — trivially
// reachable given the opening grant vs £1/month wage) and proves the
// failure path keeps the two pots IDENTICAL at month end. The old legacy
// fallback mutated simState only; the next successful syncMoneyFromLedger
// silently wiped those deltas and a drained ledger froze F2 on diverged
// figures.
func TestBUG355_PostRejection_SimStateMirrorsLedgerAtMonthEnd(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	drain := initialTreasury - monthlyWages + 1 // leaves 999_999 < one wage bill
	if _, err := comp.state.finance.SettleConstruction(finance.Money(drain)); err != nil {
		t.Fatalf("SettleConstruction(drain): %v", err)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if led, st := ledgerBalance(comp.state.finance, finance.AcctTreasury), comp.Treasury(); led != st {
		t.Fatalf("month end after rejected post: FinanceAPI treasury = %d, Composition.Treasury = %d — pots diverged", led, st)
	}
	if led, st := ledgerBalance(comp.state.finance, finance.AcctHouseholds), comp.CitizenWealth(); led != st {
		t.Fatalf("month end after rejected post: FinanceAPI households = %d, Composition.CitizenWealth = %d — pots diverged", led, st)
	}
}

// TestBUG355_PartialPost_TaxRejectionStillMirrorsLedger pins the
// non-atomic PostWages->CollectTax pair against its thinnest reachable
// pot: households drained to one micropound short of the wage bill before
// the month runs. The pair stays all-or-nothing today BECAUSE the tax
// debit is computed on the wages credited moments earlier (rate <= 100%,
// so the CollectTax leg can never overdraft households) — this test
// freezes that boundary: month end must still find the two pots
// identical, at the ledger's own exact figures.
func TestBUG355_PartialPost_TaxRejectionStillMirrorsLedger(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	drain := initialCitizenWealth - monthlyWages + 1 // leaves households monthlyWages-1
	if _, err := comp.state.finance.Post(finance.Transaction{
		Description: "test drain of household wealth",
		Entries: []finance.Entry{
			{Account: finance.AcctHouseholds, Side: finance.SideDebit, Amount: finance.Money(drain), Category: finance.Category("opening.capital")},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(drain), Category: finance.Category("opening.capital")},
		},
	}); err != nil {
		t.Fatalf("Post(household drain): %v", err)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	// FEAT-1972079927 inc1 adds two more household->elsewhere legs this
	// same month (Q4 consumption spend + the flat council tax), both of
	// which still fit comfortably in the wage/tax pair's thinnest-pot
	// balance (monthlyWages-1) and so post successfully too — the pair's
	// own all-or-nothing property (this test's real subject) is
	// unaffected; only the arithmetic the new legs add needs updating.
	wantHH := int64(monthlyWages - 1 - monthlyConsumptionSpendMicropounds - monthlyCouncilTaxMicropounds)
	if led, st := ledgerBalance(comp.state.finance, finance.AcctHouseholds), comp.CitizenWealth(); led != st || led != wantHH {
		t.Fatalf("month end: FinanceAPI households = %d, Composition.CitizenWealth = %d, want both %d", led, st, wantHH)
	}
	// Treasury gains the commercial+industrial tax on the posted spend,
	// plus the flat council tax (the old wage/tax pair nets zero on
	// treasury, unchanged from before this increment).
	commercial := monthlyConsumptionSpendMicropounds * commercialTaxRateBp / 10_000
	industrial := monthlyConsumptionSpendMicropounds * industrialTaxRateBp / 10_000
	wantTr := int64(initialTreasury + commercial + industrial + monthlyCouncilTaxMicropounds)
	if led, st := ledgerBalance(comp.state.finance, finance.AcctTreasury), comp.Treasury(); led != st || led != wantTr {
		t.Fatalf("month end: FinanceAPI treasury = %d, Composition.Treasury = %d, want both %d", led, st, wantTr)
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
		&trafficTickHook{st: st},
		&coldPassHook{st: st},
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

// --- FEAT-208 increment 3: the SetFunding pilot command seam ------------

// TestGameplay_SetFundingAcceptedAndAppliesToEngineState is the seam's
// positive proof-of-failure: issuing a real protocol.KindSetFunding
// command through the SAME e.HandleCommand path every other gameplay
// command uses must (a) be Accepted and (b) actually change
// ServicesAPI's live FundingLevel for that service — a handler that
// merely accepted without forwarding to the engine (or a handler that
// silently no-oped) would pass an Accepted-only assertion but fail this
// one, which is the point.
func TestGameplay_SetFundingAcceptedAndAppliesToEngineState(t *testing.T) {
	cid := errs.NewCorrelationID()
	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := servicesAPI.RegisterService(services.ServiceSpec{
		ID:          "clinic-1",
		Kind:        services.ServiceHealthcare,
		CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	e := core.NewEngine(core.WithPoolSize(1))
	if _, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	before, err := servicesAPI.FundingLevel("clinic-1")
	if err != nil {
		t.Fatalf("FundingLevel (before): %v", err)
	}
	if before == 0.75 {
		t.Fatalf("test setup: default FundingLevel already 0.75 — the assertion below would not distinguish applied-vs-not")
	}

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-setfunding"),
		Kind:            protocol.KindSetFunding,
		Payload:         protocol.SetFundingPayload{ServiceID: "clinic-1", Level: 0.75},
	}
	res := e.HandleCommand(cmd)
	if !res.Accepted {
		t.Fatalf("SetFunding rejected: %+v", res.Error)
	}
	if res.CorrelationID != cmd.CorrelationID {
		t.Errorf("CommandResult.CorrelationID = %q, want %q (echoed verbatim)", res.CorrelationID, cmd.CorrelationID)
	}

	after, err := servicesAPI.FundingLevel("clinic-1")
	if err != nil {
		t.Fatalf("FundingLevel (after): %v", err)
	}
	if after != 0.75 {
		t.Fatalf("FundingLevel after accepted SetFunding = %v, want 0.75 (handleGameplay must forward to ServicesAPI.SetFunding, not just accept)", after)
	}
}

// TestGameplay_SetFundingRejectionSurfacesRegistryCode is the seam's
// negative proof-of-failure: a level outside ServicesAPI.SetFunding's
// [0,1] domain must reject through the SAME real path, carrying a
// registry-sourced ErrorRef (GR#7) rather than being silently accepted or
// panicking — and must NOT have mutated FundingLevel.
func TestGameplay_SetFundingRejectionSurfacesRegistryCode(t *testing.T) {
	cid := errs.NewCorrelationID()
	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := servicesAPI.RegisterService(services.ServiceSpec{
		ID:          "clinic-1",
		Kind:        services.ServiceHealthcare,
		CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	e := core.NewEngine(core.WithPoolSize(1))
	if _, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	before, err := servicesAPI.FundingLevel("clinic-1")
	if err != nil {
		t.Fatalf("FundingLevel (before): %v", err)
	}

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-setfunding-reject"),
		Kind:            protocol.KindSetFunding,
		Payload:         protocol.SetFundingPayload{ServiceID: "clinic-1", Level: 1.5},
	}
	res := e.HandleCommand(cmd)
	if res.Accepted {
		t.Fatalf("SetFunding(1.5) accepted, want rejected (outside ServicesAPI's [0,1] domain)")
	}
	if res.Error == nil || res.Error.Code == "" {
		t.Fatalf("rejected CommandResult has no registry ErrorRef (GR#7): %+v", res)
	}
	if strings.Contains(res.Error.Display, "{") {
		t.Errorf("Error.Display = %q, contains an unrendered template placeholder", res.Error.Display)
	}

	after, err := servicesAPI.FundingLevel("clinic-1")
	if err != nil {
		t.Fatalf("FundingLevel (after): %v", err)
	}
	if after != before {
		t.Fatalf("FundingLevel changed after a REJECTED SetFunding: before=%v after=%v, want unchanged", before, after)
	}
}

// TestGameplay_SetFundingUnregisteredServiceRejected proves an
// unregistered ServiceID is rejected through the real seam too — not just
// the two [0,1]-domain edges above (services.ErrServiceNotRegistered is a
// distinct rejection path inside ServicesAPI.SetFunding).
func TestGameplay_SetFundingUnregisteredServiceRejected(t *testing.T) {
	e, _ := newTestEngine(t, 42)
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("gameplay-setfunding-unregistered"),
		Kind:            protocol.KindSetFunding,
		Payload:         protocol.SetFundingPayload{ServiceID: "no-such-service", Level: 0.5},
	}
	res := e.HandleCommand(cmd)
	if res.Accepted {
		t.Fatalf("SetFunding for an unregistered service accepted, want rejected")
	}
	if res.Error == nil || res.Error.Code == "" {
		t.Fatalf("rejected CommandResult has no registry ErrorRef (GR#7): %+v", res)
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
	// drives daily via BuildAPI.Tick, BUG-268) until the order completes. compose's
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

// --- BUG-268: build must advance per sim-DAY, not per sim-MONTH -----------

// dwellingBaseLeadTimeDays is data/buildings.json's
// zones[dwelling].baseLeadTimeDays (45), mirrored here ONLY as a sanity
// floor for the test's tolerance arithmetic below — never as the expected
// per-order lead time itself. The ACTUAL per-order lead time
// (effectiveLeadTime = ceil(base/seasonalMultiplier), build/numeric.go) is
// read back from the real BuildAPI's queue after submission, because §9's
// winter construction-speed multiplier can lengthen it beyond the raw data
// value depending on the submission month — asserting against the raw
// data constant would be asserting a value the real engine never promises.
const dwellingBaseLeadTimeDays = 45

// wireBuildTestEngine builds a real composed engine with a generously
// provisioned logistics stock (so the materials gate never blocks — the
// test isolates the lead-time cadence, not the materials draw) and submits
// Buy->Zone->Build for a dwelling at cell, exactly through the real
// gameplay-command seam (e.HandleCommand), the same path every runnable top
// uses. Returns the engine/composition, the (tile, local) key to poll
// BuildAPI.Structure with, and the order's ACTUAL initial leadTimeRemaining
// (post seasonal multiplier) so callers assert against ground truth rather
// than the raw data constant.
func wireBuildTestEngine(t *testing.T, seed uint64, cell protocol.CellRef) (e *core.Engine, comp *Composition, tile world.TileCoord, local world.CellLocal, initialLeadTime int64) {
	t.Helper()
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	e = core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err = Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("bug268-buy"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("bug268-zone"),
		Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Zone rejected: %+v", res.Error)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("bug268-build"),
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}

	tile = world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	local = world.CellLocal{Row: cell.Y, Col: cell.X}

	orders := comp.state.buildAPI.Queue()
	if len(orders) != 1 {
		t.Fatalf("build queue has %d orders right after Build, want 1", len(orders))
	}
	initialLeadTime = orders[0].LeadTimeRemaining
	if initialLeadTime < dwellingBaseLeadTimeDays {
		// The seasonal multiplier only ever LENGTHENS the lead time
		// (winter < 1.0 -> longer, §9); a value below the raw data floor
		// would mean effectiveLeadTime's math went the wrong way.
		t.Fatalf("dwelling initial leadTimeRemaining = %d, want >= %d (base data value; seasonal multiplier only lengthens)", initialLeadTime, dwellingBaseLeadTimeDays)
	}
	return e, comp, tile, local, initialLeadTime
}

// TestBUG268_BuildAdvancesDaily is the core regression: it drives Buy->
// Zone->Build for a dwelling (baseLeadTimeDays=45) through the REAL
// composed engine, then calls the REAL e.AdvanceTicks day by day (the
// exact entry point cmd/metropolis and the headless harness use — not a
// direct BuildAPI.Tick loop, which would bypass the phase-hook cadence
// this bug is about). Before the fix, the build hook was wired against
// PhaseLandValueDecay (a monthly phase), so the queue only advanced once
// per 30 daily ticks — a ~45-day dwelling would still be lead-time-pending
// after far more than 45 daily ticks (it would need leadTime*30). The fix
// re-wires the hook onto PhaseDailyTick so one day of lead/labour/materials
// elapses per AdvanceTicks(...,1) call, and the dwelling completes within
// its actual per-order lead time (materials complete in a single tick given
// the generous provision above).
func TestBUG268_BuildAdvancesDaily(t *testing.T) {
	e, comp, tile, local, leadTime := wireBuildTestEngine(t, 11, protocol.CellRef{X: 2, Y: 2})

	// Proof #1: the structure must NOT exist before the lead time can
	// possibly have elapsed (leadTime-1 daily ticks). If the hook cadence
	// were wrong in the OTHER direction (firing faster than once/day) this
	// would catch it.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), leadTime-1); err != nil {
		t.Fatalf("AdvanceTicks(%d): %v", leadTime-1, err)
	}
	if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
		t.Fatalf("dwelling structure already exists after %d daily ticks, want not-yet (leadTime=%d)", leadTime-1, leadTime)
	}

	// Proof #2 (the BUG-268 assertion): a handful more daily ticks — days,
	// not months — completes it. A small tolerance (+3 ticks beyond the
	// exact lead time) absorbs legitimate off-by-one framing, but nothing
	// close to the old bug's leadTime*30 daily ticks (leadTime months).
	completedAtTick := int64(-1)
	for i := int64(0); i < 5; i++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("AdvanceTicks(1) at day %d: %v", leadTime-1+i+1, err)
		}
		if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
			completedAtTick = leadTime - 1 + i + 1
			break
		}
	}
	if completedAtTick < 0 {
		t.Fatalf("dwelling structure did not complete within %d daily ticks of its %d-day lead time — build queue is not advancing per sim-day (BUG-268 regressed)", leadTime-1+5, leadTime)
	}
	t.Logf("dwelling (leadTime=%d days) completed at daily tick %d", leadTime, completedAtTick)

	// Sanity ceiling: the old (monthly-phase) bug would need ~leadTime
	// MONTHS (leadTime*30 daily ticks). Completing this early proves the
	// daily cadence, independent of the exact tick this build's
	// labour/materials gates happen to clear on.
	if completedAtTick >= leadTime*int64(core.DailyTicksPerMonth) {
		t.Fatalf("dwelling took %d daily ticks to complete, which is monthly-cadence territory (>= %d) — BUG-268 regressed", completedAtTick, leadTime*int64(core.DailyTicksPerMonth))
	}
}

// TestBUG268_BuildHookOnDailyPhase asserts, mechanically rather than via
// timing, that Wire() no longer registers the build hook against the
// monthly PhaseLandValueDecay slot: it drives a phase observer over exactly
// one daily tick (no month boundary crossed) and requires the build hook's
// effect to have already applied — i.e. PhaseDailyTick fired with build's
// hook attached — which is only possible if build is registered on
// PhaseDailyTick (monthly phases do not run mid-month).
func TestBUG268_BuildHookOnDailyPhase(t *testing.T) {
	e, comp, tile, local, leadTime := wireBuildTestEngine(t, 23, protocol.CellRef{X: 3, Y: 3})

	// One single daily tick — deliberately NOT a month boundary (30 would
	// trip monthlyPhaseOrder too, which would mask a regression back onto
	// a monthly phase for a lead time short enough to complete in one
	// month). leadTimeRemaining must have decremented by exactly one day,
	// which can only happen if BuildAPI.Tick ran on this very tick.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks(1): %v", err)
	}
	orders := comp.state.buildAPI.Queue()
	if len(orders) != 1 {
		t.Fatalf("build queue has %d orders after Build, want 1", len(orders))
	}
	if orders[0].LeadTimeRemaining != leadTime-1 {
		t.Fatalf("leadTimeRemaining after 1 daily tick = %d, want %d (BuildAPI.Tick must fire once per daily tick, not once per month)", orders[0].LeadTimeRemaining, leadTime-1)
	}
	if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
		t.Fatalf("dwelling completed after just 1 daily tick — leadTime=%d days cannot have elapsed", leadTime)
	}
}

// TestBUG268_Determinism proves the daily-phase build hook is still
// deterministic run-over-run (AC-2/GR#21's concern, re-verified because
// this bug moved a hook's phase registration): two same-seeded engines
// driven through the identical Buy/Zone/Build/AdvanceTicks sequence must
// produce byte-identical population hashes and money state, and the
// dwelling must complete on the exact same daily tick in both runs.
func TestBUG268_Determinism(t *testing.T) {
	run := func() (uint64, [32]byte, int64, int64) {
		e, comp, tile, local, leadTime := wireBuildTestEngine(t, 31, protocol.CellRef{X: 4, Y: 4})
		completedAtTick := int64(-1)
		for i := int64(1); i <= leadTime+5; i++ {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
				t.Fatalf("AdvanceTicks(1) at day %d: %v", i, err)
			}
			if _, ok := comp.state.buildAPI.Structure(tile, local); ok {
				completedAtTick = i
				break
			}
		}
		if completedAtTick < 0 {
			t.Fatalf("dwelling never completed within %d daily ticks", leadTime+5)
		}
		return e.TicksCompleted(), comp.PopulationHash(), comp.MoneyFlows(), completedAtTick
	}

	ticks1, hash1, flows1, day1 := run()
	ticks2, hash2, flows2, day2 := run()

	if ticks1 != ticks2 {
		t.Fatalf("TicksCompleted differs across same-seed runs: %d vs %d", ticks1, ticks2)
	}
	if hash1 != hash2 {
		t.Fatalf("population hash differs across same-seed runs:\n%x\n%x", hash1, hash2)
	}
	if flows1 != flows2 {
		t.Fatalf("money flows differ across same-seed runs: %d vs %d", flows1, flows2)
	}
	if day1 != day2 {
		t.Fatalf("dwelling completed on different daily ticks across same-seed runs: %d vs %d", day1, day2)
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

// --- FEAT-169: citizens cold pass (real mortality + FEAT-160 fertility) wired into the live tick ---

// advanceInChunks drives e for n total ticks, split into
// core.MaxAdvanceTicksPerCall-sized chunks (AdvanceTicks itself rejects a
// single call above that bound, AC-11) — a plain test-harness convenience,
// not a new production code path.
func advanceInChunks(t *testing.T, e *core.Engine, n int64) {
	t.Helper()
	for n > 0 {
		chunk := n
		if chunk > core.MaxAdvanceTicksPerCall {
			chunk = core.MaxAdvanceTicksPerCall
		}
		if err := e.AdvanceTicks(errs.NewCorrelationID(), chunk); err != nil {
			t.Fatalf("AdvanceTicks(%d): %v", chunk, err)
		}
		n -= chunk
	}
}

// mkFertilityColdRecord builds a minimal valid ColdRecord for the compose
// package's black-box seeding (SeedColdRecords is citizens' own bulk-load
// command path, exported for exactly this — see registry.go). Every field
// besides ID/BirthMonth is left at its zero value, which is a valid
// ColdRecord per ValidateColdRecord (zero is within every enum/range
// domain checked there).
func mkFertilityColdRecord(id uint64, birthMonth int64) citizens.ColdRecord {
	return citizens.ColdRecord{ID: id, BirthMonth: birthMonth}
}

// feat169CoupleSeed/feat169CoupleBirthMonth/feat169CoupleRunMonths are a
// fixed, VERIFIED-deterministic (seed, household, month) triple (mirrors
// citizens/fertility_test.go's TestFertilityBirthOccursForEligibleCouple's
// own "verified" pattern): household 1 is the FIRST household
// LifeEventPartner ever forms on a fresh CitizensAPI (nextHouseholdID
// starts at 1), and month 334 is where
// citizens.CoupleBirth(feat169CoupleSeed, 1, month,
// citizens.FertilityHazard(month, month, cfg)) draws true for
// data/fertility.json's CURRENT placeholder rates — a couple both born at
// sim month 0 has age-in-months == the current sim month, so
// FertilityHazard's two age arguments are just `month` here. This is
// deterministic, not flaky: it either always passes or always fails for
// this data file's rates, exactly like the citizens-package test it
// mirrors (found by direct search over citizens.FertilityHazard/CoupleBirth,
// the same exported functions the production fertility pass itself calls —
// not a hand-picked guess).
const (
	feat169CoupleSeed       uint64 = 1
	feat169CoupleBirthMonth int64  = 334
	feat169CoupleRunMonths  int64  = feat169CoupleBirthMonth + 1
)

// buildFertilityCoupleAPI constructs a fresh CitizensAPI seeded with one
// partnered couple (ids 90000/90001, both born at sim month 0 so their age
// in months tracks the sim month 1:1) at feat169CoupleSeed — the FIRST
// household LifeEventPartner ever forms, so it lands on household id 1,
// matching the verified (seed, household, month) triple above.
func buildFertilityCoupleAPI(t *testing.T) *citizens.CitizensAPI {
	t.Helper()
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(feat169CoupleSeed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	a := mkFertilityColdRecord(90000, 0)
	b := mkFertilityColdRecord(90001, 0)
	if err := api.SeedColdRecords([]citizens.ColdRecord{a, b}, cid); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: cid, Kind: citizens.LifeEventPartner, CitizenID: 90000, PartnerID: 90001,
	}); err != nil {
		t.Fatalf("ApplyLifeEventCommand(LifeEventPartner): %v", err)
	}
	if hh, ok := api.HouseholdOf(90000, cid); !ok || hh.ID != 1 {
		t.Fatalf("couple household = %+v (ok=%v), want household id 1 (the verified triple assumes this)", hh, ok)
	}
	return api
}

// TestFEAT169_LiveBirths_RealFertility drives the REAL engine through the
// couple's guaranteed-birth month (the verified triple above) and asserts
// a birth actually happened via CitizensAPI.VitalEvents — real fertility,
// not the old spawnHook fake (which births exactly 8/month, every month,
// regardless of demographics; see TestFEAT169_NoFakeFlatBirths for the
// contrasting zero-eligible-couples case).
func TestFEAT169_LiveBirths_RealFertility(t *testing.T) {
	api := buildFertilityCoupleAPI(t)
	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(feat169CoupleSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens: api,
		InvariantOpts: []invariant.HookOption{
			invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	advanceInChunks(t, e, feat169CoupleRunMonths*int64(core.DailyTicksPerMonth))

	if got := comp.VitalBirths(); got <= 0 {
		t.Fatalf("VitalBirths() = %d after %d months, want > 0 (the couple's verified guaranteed-birth month %d has completed)", got, feat169CoupleRunMonths, feat169CoupleBirthMonth)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations while a real birth landed, want 0", got)
	}
	// The child must be a real, addressable citizen — not just a counter
	// bump — proving VitalEvents reflects an actual cold-store mutation.
	childID := citizens.FertilityChildIDBase
	if _, ok := comp.state.citizens.CitizenAt(childID, comp.state.cid); !ok {
		t.Fatalf("expected fertility child %d to exist in the cold store", childID)
	}
}

// TestFEAT169_NoFakeFlatBirths is the contrasting case: the DEFAULT
// baseline-one seed population (64 singles, no partners — see
// simState.spawnCitizens) has zero fertility-eligible couples, so real
// fertility must report exactly zero births over a run the old spawnHook
// fake would have birthed 8*testMonths citizens over. This is the
// behavioural proof the flat-8 fake is gone: births now vary with
// demographics (0 here, >0 in TestFEAT169_LiveBirths_RealFertility above)
// rather than being a hardcoded constant.
func TestFEAT169_NoFakeFlatBirths(t *testing.T) {
	e, comp := newTestEngine(t, 7)
	advanceInChunks(t, e, testTicks)

	if got := comp.VitalBirths(); got != 0 {
		t.Fatalf("VitalBirths() = %d over %d months with zero partnered couples in the seed population, want exactly 0 (a real fertility model, not the old flat monthlyBirths=8/month fake)", got, testMonths)
	}
}

// TestFEAT169_LiveDeaths_RealMortality seeds the citizens cold pass with an
// already-ancient population (age pre-advanced to ~200 sim years, WELL
// past the point Gompertz-Makeham's hazard clamps to 1.0 — see
// mortality.go's MortalityHazard) BEFORE wiring it into compose: every
// citizen the composition root then seeds (spawnCitizens always passes
// BirthMonth=0) is therefore already at that clamped age the moment the
// cold pass first runs, so death is not merely likely but GUARANTEED,
// deterministically — no probabilistic flake risk.
//
// FEAT-087 (mkey feat.deathwave) inc1.5 amendment: hazard-selected deaths
// are now realised through the live death-queue smoothing budget
// (data/mortality.json's monthlyDeathBudget, 25/month as of this writing),
// not removed the instant the hazard hits — that immediate-removal
// behaviour is the exact one-month population cliff FEAT-087 exists to
// kill (AC-1). seedCitizenCount=64 exceeds the budget, so this test now
// drives enough months to fully DRAIN the queue (ceil(64/25)=3, plus one
// month of headroom) rather than asserting completeness within month 1.
//
// Two things changed about what this test can assert once other live
// composition systems are given that extra time to run (found the hard
// way, 2026-09-01): engine.attract's migration (admission + AC-6's
// ambition-weighted emigration, both routed through the SAME citizens.
// LifeEventDeath command mortality now shares) is wired unconditionally
// by Wire — over multiple months it admits and emigrates its own
// citizens, so comp.VitalDeaths() (a package-wide mortality tally) is no
// longer pinned to exactly seedCitizenCount once migrant churn is in the
// mix. What FEAT-087 actually guarantees, and what this test asserts
// instead:
//   - AC-1 (the cliff itself): the FIRST month's population delta for the
//     original ancient cohort is bounded by the data-file budget — proven
//     directly by counting how many of ids [1, seedCitizenCount) are
//     already gone after month 1 alone.
//   - AC-2 (conservation, never dropped nor duplicated): every one of the
//     ORIGINAL seedCitizenCount ancient citizens (guaranteed hazard=1) is
//     eventually gone by the end of the drain window — checked directly
//     via CitizenAt on ids [1, seedCitizenCount), independent of whatever
//     migrant churn also happened alongside them.
//   - Zero conservation violations throughout (the invariant hook), which
//     is what actually proves the LifeEventDeath/death-queue reconciliation
//     fix (an emigrating citizen who was still queued for mortality must
//     not be double-counted or leave a stale queue entry) holds under a
//     real mixed mortality+migration workload, not just in isolation.
func TestFEAT169_LiveDeaths_RealMortality(t *testing.T) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(3, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// Advance ~200 sim years of empty-store months so the hazard curve is
	// already clamped to 1.0 (MortalityHazard's doc comment: "clamped to
	// [0, 1]") before compose ever seeds a single citizen into it. The
	// store is empty throughout this loop (no cold records yet), so it
	// costs nothing beyond preAgeMonths*30 no-op day-ticks (~0.3s measured).
	//
	// 2403, not the rounder 2400 (FEAT-087 inc2, mkey feat.deathwave,
	// 2026-09-01): compose.go now wires engine.season into citizens
	// (SetSeason) so AC-6's weather-emergency suspension is LIVE end to
	// end, and month 2400 % 12 == 0 == January -- a winter month under
	// data/mortality.json's thresholds, which would suspend the very
	// smoothing budget this test's first assertion (month 1's realised
	// deaths <= budget) exists to prove. 2403 % 12 == 3 == April, a mild
	// month under those same thresholds (see citizens/weatheremergency_
	// test.go's monthApril fixture), so this test again exercises ORDINARY
	// (non-emergency) smoothing -- exactly its original intent -- while
	// AC-6 itself is proven separately and live-wired (citizens'
	// TestEmergencyDoesNotAffectHazardSelection/TestUnwiredCitizensAPI...
	// and this package's own TestFEAT169LiveDeaths... sibling, if the
	// emergency-suspension case needs a compose-level proof too).
	const preAgeMonths = 2403
	for i := 0; i < preAgeMonths; i++ {
		if err := api.AdvanceMonth(cid); err != nil {
			t.Fatalf("pre-age AdvanceMonth: %v", err)
		}
	}

	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(3), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens: api,
		InvariantOpts: []invariant.HookOption{
			invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if before := comp.Population(); before != seedCitizenCount {
		t.Fatalf("seed population = %d, want %d", before, seedCitizenCount)
	}

	// countOriginalCohortAlive counts how many of the ORIGINAL ids
	// [1, seedCitizenCount] (spawnCitizens mints them sequentially from 1,
	// compose.go's nextCitizenID) are still resident — independent of
	// whatever migrant ids (attract.MigrantIDBase-and-above) also churn
	// through the population alongside them.
	countOriginalCohortAlive := func() int {
		alive := 0
		for id := uint64(1); id <= uint64(seedCitizenCount); id++ {
			if _, ok := api.CitizenAt(id, cid); ok {
				alive++
			}
		}
		return alive
	}

	mcfg, err := citizens.LoadDefaultMortalityConfig(cid)
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	budget := mcfg.MonthlyDeathBudget()

	// ColdPassSchedule guarantees every cold shard is processed exactly
	// once per calendar month (AC-6/AC-7), so all seedCitizenCount ancient
	// citizens are hazard-SELECTED (enqueued) within the first month — but
	// FEAT-087's live death-queue budget (data/mortality.json) now bounds
	// how many of them are actually REALISED that same month (AC-1).
	//
	// Checked via comp.VitalDeaths() specifically, NOT via how many of the
	// original cohort are simply gone: engine.attract's own emigration
	// (AC-6, ambition-weighted, routed through the SAME LifeEventDeath
	// command mortality uses) can ALSO remove an original-cohort citizen
	// in month 1 — a real, separate mechanism, correctly uncapped by the
	// mortality smoothing budget. VitalDeaths() is fed exclusively from
	// AdvanceDayTick's own Realise() return (coldPassHook.ApplyEffect),
	// never from the LifeEventDeath command emigration issues directly, so
	// it isolates the mortality-specific realisation count AC-1 actually
	// bounds.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	if got := comp.VitalDeaths(); got > int64(budget) {
		t.Fatalf("VitalDeaths() = %d after month 1 alone, want <= budget=%d (AC-1: the cohort cliff must be smoothed, not immediate)", got, budget)
	}

	// Drive enough MORE months to fully drain the queue: ceil(64/25)=3
	// total, plus one month of headroom in case a citizen's shard-visit
	// ordering pushed its selection a tick later than expected (mirrors
	// coldpass_deathwave_test.go's TestLiveColdPassConservationAcrossDrain
	// pattern in the citizens package itself).
	const monthsToFullyDrain = 4
	for i := 1; i < monthsToFullyDrain; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}

	// AC-2: every one of the ORIGINAL guaranteed-hazard citizens must
	// eventually be gone — smoothing defers, it never drops a death.
	// Checked against the ORIGINAL cohort specifically (not the aggregate
	// comp.VitalDeaths(), which now also folds in engine.attract's own
	// migration-driven admissions/emigrations over these several months).
	if stillAlive := countOriginalCohortAlive(); stillAlive != 0 {
		t.Fatalf("%d of the original %d ancient seed citizens are still alive after %d months (enough to fully drain the death queue) — FEAT-087 AC-2 requires every hazard-selected death to eventually realise", stillAlive, seedCitizenCount, monthsToFullyDrain)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations across the drain (mortality smoothing + migration churn both active), want 0", got)
	}
}

// TestFEAT169_DeterministicAcrossPoolSizes proves the live-wired cold pass
// carries determinism through the composition (mirrors
// TestHeadless_DeterministicAcrossRuns, but at pool sizes 1 and 4, and with
// a real birth-bearing population rather than the default all-young seed
// set): two runs at the same seed, one at pool size 1 and one at pool size
// 4, must produce byte-identical PopulationHash and identical VitalEvents
// totals.
func TestFEAT169_DeterministicAcrossPoolSizes(t *testing.T) {
	run := func(poolSize int) ([32]byte, int64, int64) {
		api := buildFertilityCoupleAPI(t)
		e := core.NewEngine(core.WithWorldSeed(feat169CoupleSeed), core.WithPoolSize(poolSize))
		comp, err := Wire(e, &Deps{Citizens: api})
		if err != nil {
			t.Fatalf("Wire (pool %d): %v", poolSize, err)
		}
		advanceInChunks(t, e, feat169CoupleRunMonths*int64(core.DailyTicksPerMonth))
		return comp.PopulationHash(), comp.VitalBirths(), comp.VitalDeaths()
	}

	hash1, births1, deaths1 := run(1)
	hash4, births4, deaths4 := run(4)

	if hash1 != hash4 {
		t.Fatalf("population hash differs across pool sizes 1/4:\n%x\n%x", hash1, hash4)
	}
	if births1 != births4 {
		t.Fatalf("VitalBirths differs across pool sizes 1/4: %d vs %d", births1, births4)
	}
	if deaths1 != deaths4 {
		t.Fatalf("VitalDeaths differs across pool sizes 1/4: %d vs %d", deaths1, deaths4)
	}
	if births1 <= 0 {
		t.Fatalf("VitalBirths = %d, want > 0 (the determinism check needs a real birth in play)", births1)
	}
}

// TestFEAT169_IDNamespaceSeamGuard proves the ID-SEAM guard (ICD §12 open
// decision 2, amended by destructive review) actually fires rather than
// being an inert comment: it drives simState.nextCitizenID to sit exactly
// at attract.MigrantIDBase (compose's OWN range boundary — NOT
// citizens.FertilityChildIDBase; destructive review found the ORIGINAL
// guard here was bounded against the wrong constant, which would have let
// compose's counter silently drift into attract's migrant range first) and
// asserts the very next mint is rejected with ErrCitizenIDNamespaceSeam
// rather than silently minting a colliding id.
func TestFEAT169_IDNamespaceSeamGuard(t *testing.T) {
	_, comp := newTestEngine(t, 5)
	comp.state.nextCitizenID = attract.MigrantIDBase

	err := comp.state.spawnCitizens(0, 1)
	if err == nil {
		t.Fatal("spawnCitizens at the migrant id namespace boundary returned nil, want ErrCitizenIDNamespaceSeam")
	}
	var e2 *errs.E
	if !errors.As(err, &e2) {
		t.Fatalf("spawnCitizens error %v is not a *errs.E", err)
	}
	if e2.Code != ErrCitizenIDNamespaceSeam {
		t.Fatalf("spawnCitizens error code = %q, want %q (ErrCitizenIDNamespaceSeam)", e2.Code, ErrCitizenIDNamespaceSeam)
	}
	// The guard must fire BEFORE the counter is bumped or the citizen is
	// minted — no partial mutation on a rejected mint.
	if comp.state.nextCitizenID != attract.MigrantIDBase {
		t.Fatalf("nextCitizenID = %d after a rejected mint, want unchanged at %d", comp.state.nextCitizenID, attract.MigrantIDBase)
	}
}

// TestFEAT169_WireRejectsOverlappingIDRanges proves the Wire-time
// cross-check (ErrIDNamespaceRangesOverlap) actually REJECTS the historical
// bug's exact values, and ACCEPTS the shipped ones — the destructive-review
// finding's SECOND defense (distinct from the per-mint guard, which only
// defends compose's own range). Exercises idNamespaceRangesDisjoint
// directly (the pure function Wire's check is built from) since the real
// constants cannot be overridden to drive Wire itself down the rejecting
// branch.
func TestFEAT169_WireRejectsOverlappingIDRanges(t *testing.T) {
	const buggyFertilityBase = uint64(1) << 62 // FEAT-169's ORIGINAL (pre-fix) value
	const buggyMigrantBase = uint64(1) << 62   // engine.attract's real, unchanged value — the actual collision
	if idNamespaceRangesDisjoint(buggyFertilityBase, buggyMigrantBase) {
		t.Fatalf("idNamespaceRangesDisjoint(%d, %d) = true, want false (this is the historical bug's exact overlapping values)", buggyFertilityBase, buggyMigrantBase)
	}

	// The REAL, shipped constants must satisfy the same check (the
	// regression guarantee: this is what actually ships, and it is what
	// Wire actually calls this function with).
	if !idNamespaceRangesDisjoint(citizens.FertilityChildIDBase, attract.MigrantIDBase) {
		t.Fatalf("idNamespaceRangesDisjoint(citizens.FertilityChildIDBase=%d, attract.MigrantIDBase=%d) = false, want true (the shipped constants must be disjoint)", citizens.FertilityChildIDBase, attract.MigrantIDBase)
	}

	// End-to-end: Wire itself must succeed against the real, shipped
	// constants (proving the check is actually wired in, not just present
	// as a standalone function nothing calls).
	if _, err := Wire(core.NewEngine(core.WithPoolSize(1)), nil); err != nil {
		t.Fatalf("Wire with the real (disjoint) id-range constants: %v", err)
	}
}

// TestFEAT169_CrossModuleIDCollisionRegression is the destructive-review
// finding's regression test proper: it drives the REAL wired engine with
// BOTH real attractiveness-driven migration admits (engine.attract) AND a
// real fertility birth (engine.citizens, FEAT-160) live in the same run —
// exactly the combination that collided under the pre-fix 1<<62/1<<62
// bases — and proves every id involved is unique and correctly ranged.
func TestFEAT169_CrossModuleIDCollisionRegression(t *testing.T) {
	// Structural precondition: the shipped constants must actually be
	// disjoint (re-verifies Wire's own Wire-time assertion directly against
	// the exported constants, not merely trusting Wire didn't error below).
	if citizens.FertilityChildIDBase < 2*attract.MigrantIDBase {
		t.Fatalf("citizens.FertilityChildIDBase (%d) < 2*attract.MigrantIDBase (%d): the three id ranges are not disjoint", citizens.FertilityChildIDBase, attract.MigrantIDBase)
	}

	api := buildFertilityCoupleAPI(t)
	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(feat169CoupleSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		Citizens: api,
		InvariantOpts: []invariant.HookOption{
			invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	// Drive long enough for BOTH: the couple's verified guaranteed birth
	// (sim month 334, feat169CoupleBirthMonth) AND real attractiveness-
	// driven migration admits — the baseline-one attract config keeps
	// A > A_world from month 0 (see TestHeadless_MigrationIsAttractivenessDriven),
	// so migration admits happen essentially every month of this run.
	advanceInChunks(t, e, feat169CoupleRunMonths*int64(core.DailyTicksPerMonth))

	if got := comp.NetMigration(); got <= 0 {
		t.Fatalf("NetMigration() = %d, want > 0 (this regression needs REAL migrant ids in play, not just fertility)", got)
	}
	if got := comp.VitalBirths(); got <= 0 {
		t.Fatalf("VitalBirths() = %d, want > 0 (this regression needs a REAL fertility child id in play)", got)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations while both migration admits and a fertility birth were live, want 0", got)
	}

	// The fertility child must exist, addressable, and distinct.
	childID := citizens.FertilityChildIDBase
	child, ok := comp.state.citizens.CitizenAt(childID, comp.state.cid)
	if !ok {
		t.Fatalf("expected fertility child %d to exist", childID)
	}

	// Walk attract's sequential migrant-id counter space directly
	// (attract.MigrantIDBase+1, +2, ...) and collect every id that
	// resolves to a REAL citizen — proving actual migrant ids exist in
	// this run (not just that NetMigration is positive) and that none of
	// them collides with the fertility child id or each other.
	seen := map[uint64]bool{childID: true}
	migrantsFound := 0
	const probeBound = 20000 // must exceed the true migrant-id count with headroom — checked below, not merely assumed
	for i := uint64(1); i <= probeBound; i++ {
		id := attract.MigrantIDBase + i
		if _, ok := comp.state.citizens.CitizenAt(id, comp.state.cid); !ok {
			continue
		}
		migrantsFound++
		if seen[id] {
			t.Fatalf("migrant id %d collides with an id already seen (the fertility child id or another migrant)", id)
		}
		seen[id] = true
		// Every migrant id found must fall strictly inside attract's own
		// range, never reaching into citizens' fertility range.
		if id >= citizens.FertilityChildIDBase {
			t.Fatalf("migrant id %d falls at or past citizens.FertilityChildIDBase (%d) — range collision", id, citizens.FertilityChildIDBase)
		}
	}
	if migrantsFound == 0 {
		t.Fatalf("no migrant citizen found in attract.MigrantIDBase+[1,%d] after %d months with NetMigration=%d — cannot prove disjointness against a REAL migrant id", probeBound, feat169CoupleRunMonths, comp.NetMigration())
	}
	// Fail loudly rather than silently under-count: hitting the probe
	// ceiling means the true migrant-id count may exceed it, which would
	// make "zero collisions found" an unproven claim, not a verified one
	// (a gate that cannot evaluate the full range must not report success).
	if migrantsFound >= probeBound-10 {
		t.Fatalf("migrantsFound=%d is within 10 of probeBound=%d — the probe range is too small to trust a full scan; raise probeBound", migrantsFound, probeBound)
	}

	// The fertility child id itself must never fall inside attract's
	// migrant range.
	if childID >= attract.MigrantIDBase && childID < citizens.FertilityChildIDBase {
		t.Fatalf("fertility child id %d falls inside attract's migrant range [%d, %d) — collision", childID, attract.MigrantIDBase, citizens.FertilityChildIDBase)
	}
	_ = child
	t.Logf("cross-module regression: %d unique migrant ids + 1 fertility child id, zero collisions, zero conservation violations", migrantsFound)
}
