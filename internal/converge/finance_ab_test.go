package converge

import (
	"path/filepath"
	"reflect"
	"testing"
)

// finance_ab_test.go is FEAT-1972079936 Phase-3 inc2's finance A/B parity
// gate: it loads the REAL webconsole-emitted fixture
// (testdata/finance-webconsole-v1.json, produced by
// webconsole/test/converge-fixture-emit.mjs from the canonical action list
// converge-finance-actions.json — never hand-authored, mirrors fixture.go's
// SaveFixture discipline), runs the SAME action list against the composed
// Go engine (finance_ab_actions.go's RunFinanceActionsComposed, the P2
// bridge), and reports parity.
//
// # Field mapping (this test's Contract, distinct from FinanceDomain's)
//
// FinanceDomain's own Contract (finance_domain.go) names treasury/reserves/
// debt/netWorth because FinanceDomain drives *finance.FinanceAPI directly
// and can read all four off it. THIS test drives the full composed engine
// (internal/engine/compose) instead, and compose.Composition's PUBLIC API
// exposes exactly one money-stock accessor: Composition.Treasury(). No
// compose.go edit was made to add Reserves()/Debt()/NetWorth() accessors
// (out of this increment's scope — see the AB report's "not compared"
// section) — so this test's Contract, financeABContract, covers ONLY
// "treasury":
//
//	Go candidate value:  compose.Composition.Treasury() — already Money
//	                      (int64 milli-pounds, finance/money.go's
//	                      MicropoundsPerPound=1000 since BUG-452).
//	TS reference value:  state.funds (whole GBP) × 1000, truncated toward
//	                      zero (converge-fixture-emit.mjs's toMilliPounds).
//
// Both sides are genuinely money-stock quantities at the same nominal
// scale, so the field IS meaningfully comparable — it is expected to FAIL
// Compare's TierExact bar today because the two engines are still
// different MODELS (docs/planning/phase3-convergence-plan.md §1b: the Go
// finance hook is a stub that posts flat monthly wage/tax/consumption
// legs, while the TS engine derives its funds from ~15 bespoke per-tick
// revenue/expense lines keyed off zone counts and policies) — see
// docs/planning/phase3-finance-ab-report-2026-09-01.md for the full
// divergence breakdown, and TestFinanceAB_KnownDivergence_NonEmpty below
// for the honesty-requirement proof that this test is not rigged to pass.
var financeABContract = Contract{
	"treasury": {Tier: TierExact},
}

const (
	// NOT ".../test/fixtures/..." -- that directory name is exclusively
	// owned by webconsole/test/serve-bundle.test.mjs's rmSync-based
	// setup/teardown scratch-space lifecycle; a shared name meant this
	// fixture got silently deleted out from under this increment's own
	// gate run (discovered live, no code change between two runs).
	// "converge-fixtures" is a dedicated namespace this increment owns.
	actionsListPath   = "../../webconsole/test/converge-fixtures/converge-finance-actions.json"
	webconsoleFixture = "testdata/finance-webconsole-v1.json"
)

// --- Hard-fail gate: meta-properties that CAN hold today ------------------
//
// These do NOT go through Compare (Compare's TierExact bar on "treasury"
// is known-red today — see the KnownDivergence test below). They assert
// properties of the BRIDGE and the composed engine's OWN behaviour that
// the honesty requirement in this increment's dispatch brief calls out as
// things that genuinely hold pre-de-stubbing: tick alignment, sign
// conventions, and monotonic-ish behaviour under zero gameplay activity.

// TestFinanceAB_TickAlignment_Holds proves the bridge's own bookkeeping:
// the composed-engine trajectory reports samples at EXACTLY the canonical
// logical ticks the action list declares (30/60/90), matching the
// webconsole fixture's own checkpoint ticks one-for-one. This is a
// structural property of the bridge (finance_ab_actions.go's
// sampleAfterTick cross-check), not of either engine's internal model, and
// is exactly the kind of thing that would break FIRST if the two action
// lists (TS's converge-fixture-emit.mjs, Go's finance_ab_actions.go) ever
// drifted out of sync with each other or with the shared JSON.
func TestFinanceAB_TickAlignment_Holds(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	_, tsTraj, err := LoadFixture(webconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	wantTicks := []int64{30, 60, 90}
	assertTicks := func(label string, traj Trajectory) {
		if len(traj) != len(wantTicks) {
			t.Fatalf("%s: got %d samples, want %d: %+v", label, len(traj), len(wantTicks), traj)
		}
		for i, want := range wantTicks {
			if traj[i].Tick != want {
				t.Fatalf("%s: sample %d has Tick=%d, want %d", label, i, traj[i].Tick, want)
			}
		}
	}
	assertTicks("go composed trajectory", goTraj)
	assertTicks("ts webconsole fixture", tsTraj)
}

// TestFinanceAB_SignConvention_Holds proves both trajectories report
// finite, non-saturated, positive-scale money values under this journal
// (which never drives either side into overdraft/debt-service territory)
// — i.e. neither side's int64 arithmetic has wrapped or saturated to a
// MinInt64/MaxInt64 garbage value, and both report the SAME sign
// direction (increasing treasury reads as a positive number on both
// sides — no accidental TS-cost-vs-Go-transfer sign inversion of the kind
// docs/planning/phase3-convergence-plan.md §1b calls out for wages
// specifically).
func TestFinanceAB_SignConvention_Holds(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	_, tsTraj, err := LoadFixture(webconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	const saturationGuard = int64(1) << 60 // comfortably below MaxInt64, comfortably above any plausible baseline-one figure
	for _, s := range goTraj {
		v := s.Values["treasury"]
		if v <= 0 {
			t.Fatalf("go treasury at tick %d = %d, want > 0 (this journal never drives the ledger into deficit)", s.Tick, v)
		}
		if v > saturationGuard {
			t.Fatalf("go treasury at tick %d = %d exceeds the saturation guard %d — looks like overflow garbage, not a real figure", s.Tick, v, saturationGuard)
		}
	}
	for _, s := range tsTraj {
		v := s.Values["treasury"]
		if v <= 0 {
			t.Fatalf("ts treasury at tick %d = %d, want > 0 under this journal", s.Tick, v)
		}
		if v > saturationGuard {
			t.Fatalf("ts treasury at tick %d = %d exceeds the saturation guard %d", s.Tick, v, saturationGuard)
		}
	}
}

// TestFinanceAB_GoTreasuryBounded_ZeroActivityMonth proves a real,
// currently-holding invariant of the Go composed engine specifically: over
// the first checkpoint (tick 0->30, zero gameplay commands — the action
// list's "Month 1" segment), the treasury cannot have moved further than
// the sum of everything financeHook is capable of posting in one month
// (compose.go's monthlyWages/monthlyTax close net-zero by design; only the
// consumption/tax legs — FEAT-1972079927 Q4 — can move the tracked stock,
// and they are bounded by the citizen population this early in the run).
// This is the composed engine's OWN "monotonic-ish under zero activity"
// property — distinct from, and provable independently of, the cross-model
// Compare() gate below.
func TestFinanceAB_GoTreasuryBounded_ZeroActivityMonth(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	if len(goTraj) == 0 {
		t.Fatal("expected at least one sample")
	}
	const initialTreasuryMilliPounds = 1_500_000_000 // compose.go's initialTreasury constant, mirrored here as a literal since it is package-private
	first := goTraj[0].Values["treasury"]
	delta := first - initialTreasuryMilliPounds
	if delta < 0 {
		delta = -delta
	}
	// Bound: within half the opening treasury after ONE zero-gameplay
	// month. Loose enough to never be a flaky false-fail as the consumption/
	// tax legs' magnitude shifts with future tuning, tight enough that a
	// genuine runaway (e.g. a resurrected all-time-cumulative bug of
	// financeHook's own BUG-355 class) still fails it.
	bound := int64(initialTreasuryMilliPounds / 2)
	if delta > bound {
		t.Fatalf("go treasury moved by %d milli-pounds in one zero-gameplay month (from %d to %d) — exceeds the bound %d; this looks like a runaway, not the documented net-zero-plus-consumption-legs behaviour", delta, initialTreasuryMilliPounds, first, bound)
	}
}

// TestFinanceAB_ComposedRun_Deterministic proves GR#21 for the bridge
// itself: two independent RunFinanceActionsComposed calls over the same
// action list produce a reflect.DeepEqual Trajectory (mirrors
// finance_domain_test.go's TestFinanceDomain_DeterministicTrajectory for
// inc1's own adapter).
func TestFinanceAB_ComposedRun_Deterministic(t *testing.T) {
	first, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, _, err := RunFinanceActionsComposed(actionsListPath)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d: trajectory differs.\nfirst=%+v\ngot=%+v", i, first, got)
		}
	}
}

// TestFinanceAB_SkippedOps_DocumentedAndOnlyUtilityPlacements proves the
// bridge's coverage-gap reporting: exactly the two "place_utility_ts_only"
// actions are skipped (never a wider, silent skip of something this
// bridge SHOULD have translated), each with a non-empty Reason.
func TestFinanceAB_SkippedOps_DocumentedAndOnlyUtilityPlacements(t *testing.T) {
	_, skipped, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected exactly 2 skipped ops (the pow_wind/wat_clean utility placements), got %d: %+v", len(skipped), skipped)
	}
	for _, s := range skipped {
		if s.Op != "place_utility_ts_only" {
			t.Fatalf("unexpected skipped op %q at index %d — only place_utility_ts_only should ever be skipped", s.Op, s.Index)
		}
		if s.Reason == "" {
			t.Fatalf("skipped op at index %d has no Reason — coverage gaps must be documented, never silent", s.Index)
		}
	}
}

// --- Report-only: the full Compare() diff, proven honest ------------------

// TestFinanceAB_KnownDivergence_NonEmpty is this increment's HONESTY
// REQUIREMENT proof: the finance A/B gate is NOT rigged to pass. Compare()
// under financeABContract's TierExact "treasury" bar is run for real
// against the real composed-engine trajectory and the real webconsole
// fixture, and this test asserts the resulting Report is NON-EMPTY —
// i.e. the two models genuinely diverge today, exactly as
// docs/planning/phase3-convergence-plan.md §1b predicts (Go's finance hook
// is still a stub). The full diff is logged via t.Log (report-only, never
// t.Fatal) so `go test -v` surfaces every divergent tick/field without
// failing the build on them.
//
// This test is EXPECTED TO GO RED the day the Go finance hook stops being
// a stub and starts producing numbers that genuinely track the TS model's
// treasury under this journal — that is the intended trip-wire: a
// maintainer who makes financeHook "more real" and accidentally makes
// TestFinanceAB_KnownDivergence_NonEmpty fail is being told, mechanically,
// "the finance parity contract needs to be revisited/tightened now",
// rather than the divergence silently going unnoticed.
func TestFinanceAB_KnownDivergence_NonEmpty(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	_, tsTraj, err := LoadFixture(webconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	report := Compare("finance", goTraj, tsTraj, financeABContract)
	if report.Pass {
		t.Fatal("HONESTY VIOLATION: the finance A/B gate reports Pass=true against the real TS fixture. " +
			"Per docs/planning/phase3-convergence-plan.md §1b the Go finance hook is still a stub and " +
			"CANNOT genuinely match the TS model's treasury trajectory under this journal — a passing " +
			"report here means either the contract was quietly weakened to something vacuous, or the " +
			"fixture/bridge stopped exercising a real divergence. Investigate before trusting this gate.")
	}
	t.Logf("finance A/B report: domain=%s pass=%v diffs=%d", report.Domain, report.Pass, len(report.Diffs))
	for _, d := range report.Diffs {
		t.Logf("  %s", d.String())
	}
}

// --- prove-can-fail regression for the RED-proof mechanism itself --------

// TestFinanceAB_KnownDivergence_GreenIfFixturesMatch is the flip side of
// KnownDivergence's honesty proof: pointed at a Go trajectory saved AS a
// fixture and then re-loaded (so ref and candidate are IDENTICAL), Compare
// reports Pass=true. This proves TestFinanceAB_KnownDivergence_NonEmpty's
// "non-empty" assertion is a REAL check (capable of being green), not a
// tautology that can never pass — the go-red/go-green pairing the dispatch
// brief's honesty requirement calls for ("it must go green→red
// appropriately").
func TestFinanceAB_KnownDivergence_GreenIfFixturesMatch(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "go-self-fixture.json")
	if err := SaveFixture(path, "finance", goTraj); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}
	_, selfCandidate, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	report := Compare("finance", goTraj, selfCandidate, financeABContract)
	if !report.Pass {
		t.Fatalf("expected Compare(go, go) to pass (identical trajectories), got diffs: %v", report.Diffs)
	}
}
