package compose

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): tests for the daily
// AdvanceTick wiring and the extcommute TrafficSeam adapter that replaces
// extCommuteTrafficSeamStub (traffic_wire.go). Every test here is a
// proof-of-failure: run it against the pre-FEAT-206 tree (traffic never
// constructed, Wire's extCommuteAPI.SetTrafficSeam(extCommuteTrafficSeamStub{}))
// and it fails to compile or fails at runtime — see each test's comment for
// which.

const trafficTestChannel = "motorway"

// --- Wire failure: bad traffic config = zero hooks (AC-4 discipline) ------

// TestWire_FailingTrafficModuleReturnsRegistryError proves a failing
// LoadTraffic seam fails Wire loudly with ErrModuleFailed naming "traffic",
// mirroring TestWire_FailingMarketModuleReturnsRegistryError's shape exactly
// (compose_test.go). Proof-of-failure: without the LoadTraffic seam and its
// error-wrapping call site in Wire, this test cannot even inject the
// failure (Deps.LoadTraffic would not exist), or Wire would swallow the
// error instead of returning it.
func TestWire_FailingTrafficModuleReturnsRegistryError(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	injected := errs.New("MET-G4502", "test", map[string]any{"cause": "injected failure"})
	_, err := Wire(e, &Deps{
		LoadTraffic: func(correlationID string) (*traffic.TrafficAPI, error) {
			return nil, injected
		},
	})
	if err == nil {
		t.Fatal("Wire returned nil for a failing traffic module")
	}
	var e2 *errs.E
	if !errors.As(err, &e2) {
		t.Fatalf("Wire error %v is not a *errs.E", err)
	}
	if e2.Code != ErrModuleFailed {
		t.Fatalf("Wire error code = %q, want %q (ErrModuleFailed)", e2.Code, ErrModuleFailed)
	}
	if e2.Ctx["module"] != "traffic" {
		t.Fatalf("Wire error ctx[module] = %v, want %q", e2.Ctx["module"], "traffic")
	}
	if got := e.HookCount(); got != 0 {
		t.Fatalf("engine left with %d hooks after a failed Wire, want 0 (no partial wiring)", got)
	}
}

// --- AC / ICD §11: ordering / day-boundary ----------------------------------

// TestFEAT206_AdvanceTickResetsPriorDayDemand is the ICD §11
// "ordering / day-boundary" test: demand added before a day-tick (the
// "prior day"'s demand, per traffic/doc.go's contract) must be wiped by
// that tick's AdvanceTick call, observed via CommuteHours returning to
// exactly the pre-demand baseline. Proof-of-failure: with no
// trafficTickHook registered (the pre-FEAT-206 tree), AdvanceTick is never
// called and this assertion fails — "after" would still reflect the
// injected demand.
func TestFEAT206_AdvanceTickResetsPriorDayDemand(t *testing.T) {
	e, comp := newTestEngine(t, 1)
	tr := comp.Traffic()
	const cid = "test"

	base, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (base): %v", err)
	}

	if err := tr.AddDemand(999, 500_000); err != nil {
		t.Fatalf("AddDemand: %v", err)
	}
	before, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (before tick): %v", err)
	}
	if before <= base {
		t.Fatalf("AddDemand did not raise CommuteHours before the tick: before=%v base=%v (test setup broken)", before, base)
	}

	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	after, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (after tick): %v", err)
	}
	if after != base {
		t.Fatalf("AdvanceTick did not reset demand across the day boundary: after=%v want base=%v", after, base)
	}
}

// TestFEAT206_SameDayDemandSurvivesUntilNextAdvanceTick is the ICD §11
// day-boundary test's other half: demand added AFTER a day-tick returns
// (this day's own demand generation, per doc.go's contract) must survive,
// visible to queries, until the NEXT AdvanceTick call — never wiped early.
func TestFEAT206_SameDayDemandSurvivesUntilNextAdvanceTick(t *testing.T) {
	e, comp := newTestEngine(t, 2)
	tr := comp.Traffic()
	const cid = "test"

	base, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (base): %v", err)
	}

	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	// This tick's own demand, added AFTER the tick returns — belongs to the
	// day just ticked and must survive until the NEXT AdvanceTick.
	if err := tr.AddDemand(1234, 500_000); err != nil {
		t.Fatalf("AddDemand: %v", err)
	}
	got, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (same day, post-demand): %v", err)
	}
	if got <= base {
		t.Fatalf("same-day demand did not survive to a same-day query: got=%v want > base=%v", got, base)
	}
}

// --- AC / ICD §11: the MOD-023 unbounded-demand regression (THE test) -----

// TestFEAT206_UnboundedDemandRegression_MOD023 is the original MOD-023
// defect's end-to-end closure proof, run through the COMPOSED engine (not
// just traffic's own package tests) across 100+ simulated days: each day
// adds a fixed amount of new demand (the day's own commute generation) and
// asserts CommuteHours stays at the SAME bounded figure every day, proving
// AdvanceTick fires exactly once per day (not zero times — demand would
// accumulate and CommuteHours would grow monotonically; not more than once
// per day — irrelevant here since a double-fire on an already-empty map is
// a no-op, but the same-day-survival test above independently rules out an
// early/extra fire wiping a day's own demand before it's queraible).
// Proof-of-failure: reverting trafficTickHook's registration (or reverting
// to the old free-flow-stub-only Wire that never constructs traffic at
// all) makes CommuteHours grow without bound across the 150 simulated
// days — exactly the "AdvanceTick is dead code" failure mode traffic's own
// doc.go describes as the still-open defect before this integration.
func TestFEAT206_UnboundedDemandRegression_MOD023(t *testing.T) {
	e, comp := newTestEngine(t, 3)
	tr := comp.Traffic()
	const cid = "test"
	const days = 150
	const dailyDemand = 10_000

	base, err := tr.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		t.Fatalf("CommuteHours (base): %v", err)
	}
	var wantSteady float64

	for d := 0; d < days; d++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("day %d: AdvanceTicks: %v", d, err)
		}
		// This day's own demand — a fresh destination id every day so
		// SatAdd never collapses two days' demand onto one key.
		if err := tr.AddDemand(uint64(1_000_000+d), dailyDemand); err != nil {
			t.Fatalf("day %d: AddDemand: %v", d, err)
		}
		got, err := tr.CommuteHours(congestionProbeCitizenID, cid)
		if err != nil {
			t.Fatalf("day %d: CommuteHours: %v", d, err)
		}
		if d == 0 {
			wantSteady = got
			if wantSteady <= base {
				t.Fatalf("day 0: CommuteHours = %v, want > base %v (test setup broken: demand did not register)", got, base)
			}
			continue
		}
		if got != wantSteady {
			t.Fatalf("day %d: CommuteHours = %v, want the steady-state figure %v (day 0's) — demand accumulated "+
				"across day boundaries instead of being reset each day; the MOD-023 unbounded-growth defect is NOT "+
				"closed end-to-end", d, got, wantSteady)
		}
	}
}

// --- AC / ICD §7: determinism -----------------------------------------------

// TestFEAT206_Determinism proves two identically-seeded composed runs that
// inject the identical daily-demand pattern produce byte-identical
// CommuteHours readings at every day boundary — the ICD §7 determinism
// guarantee extended end-to-end through the composed engine.
func TestFEAT206_Determinism(t *testing.T) {
	const days = 40
	run := func() []float64 {
		e, comp := newTestEngineSeed(t, 99)
		tr := comp.Traffic()
		out := make([]float64, 0, days)
		for d := 0; d < days; d++ {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
				t.Fatalf("day %d: AdvanceTicks: %v", d, err)
			}
			if err := tr.AddDemand(uint64(d), 777); err != nil {
				t.Fatalf("day %d: AddDemand: %v", d, err)
			}
			got, err := tr.CommuteHours(congestionProbeCitizenID, "test")
			if err != nil {
				t.Fatalf("day %d: CommuteHours: %v", d, err)
			}
			out = append(out, got)
		}
		return out
	}

	r1 := run()
	r2 := run()
	if len(r1) != len(r2) {
		t.Fatalf("run lengths differ: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("day %d: CommuteHours differs across same-seed runs: %v vs %v", i, r1[i], r2[i])
		}
	}
}

// --- extcommute TrafficSeam adapter: reads LIVE traffic state -------------

// TestFEAT206_ExtCommuteCongestionMovesWithDemand proves the new
// extCommuteTrafficSeamAdapter's Congestion reads LIVE traffic demand
// state — the seam the old extCommuteTrafficSeamStub could never pass
// (TestExtCommute_TrafficSeamStub_IsFreeFlow, extcommute_wire_test.go,
// proves the OLD stub is always exactly 0.0 regardless of any state
// change). Proof-of-failure: constructing the adapter against a stub or
// reverting Wire's SetTrafficSeam call to the old stub makes congestion
// stay at 0.0 even after demand is injected, failing this assertion.
func TestFEAT206_ExtCommuteCongestionMovesWithDemand(t *testing.T) {
	_, comp := newTestEngineSeed(t, 5)
	tr := comp.Traffic()

	adapter, err := newExtCommuteTrafficSeamAdapter(tr, "test")
	if err != nil {
		t.Fatalf("newExtCommuteTrafficSeamAdapter: %v", err)
	}

	before, err := adapter.Congestion(trafficTestChannel)
	if err != nil {
		t.Fatalf("Congestion (before demand): %v", err)
	}
	if before != 0 {
		t.Fatalf("Congestion (before demand) = %v, want 0 (no demand accumulated yet)", before)
	}

	if err := tr.AddDemand(42, 50_000); err != nil {
		t.Fatalf("AddDemand: %v", err)
	}
	after, err := adapter.Congestion(trafficTestChannel)
	if err != nil {
		t.Fatalf("Congestion (after demand): %v", err)
	}
	if after <= before {
		t.Fatalf("Congestion did not move with demand: before=%v after=%v (seam still reads stale/stub state)", before, after)
	}
	if after < 0 || after > 1 {
		t.Fatalf("Congestion = %v, out of the [0,1] range extcommute's transportAvailable requires", after)
	}

	// The channel argument is documented as ignored (every channel reads the
	// same citywide figure) — prove that explicitly rather than just
	// asserting it in a comment.
	other, err := adapter.Congestion("rail")
	if err != nil {
		t.Fatalf("Congestion (other channel): %v", err)
	}
	if other != after {
		t.Fatalf("Congestion differs by channel (%q=%v, %q=%v) — the adapter's documented citywide-only derivation is not what it implements", trafficTestChannel, after, "rail", other)
	}
}

// TestFEAT206_ExtCommuteCongestionBoundedAcrossManyDays proves the adapter's
// congestion signal stays within [0,1) even after the same sustained-demand
// pattern the MOD-023 regression test above drives — i.e. the congestion
// derivation does not itself reintroduce an unbounded figure just because
// the underlying demandMultiplier this adapter derives from grows with
// undischarged demand within a single day.
func TestFEAT206_ExtCommuteCongestionBoundedAcrossManyDays(t *testing.T) {
	e, comp := newTestEngineSeed(t, 6)
	tr := comp.Traffic()
	adapter, err := newExtCommuteTrafficSeamAdapter(tr, "test")
	if err != nil {
		t.Fatalf("newExtCommuteTrafficSeamAdapter: %v", err)
	}

	for d := 0; d < 100; d++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("day %d: AdvanceTicks: %v", d, err)
		}
		if err := tr.AddDemand(uint64(d), 200_000); err != nil {
			t.Fatalf("day %d: AddDemand: %v", d, err)
		}
		got, err := adapter.Congestion(trafficTestChannel)
		if err != nil {
			t.Fatalf("day %d: Congestion: %v", d, err)
		}
		if got < 0 || got >= 1 {
			t.Fatalf("day %d: Congestion = %v, want in [0,1)", d, got)
		}
	}
}

// --- shard-safety / SingleShard conformance --------------------------------

// TestTrafficTickHook_SingleShardAndDeterministic mirrors
// TestStubHooks_ShardSafetyAndDeterminism's per-hook assertions for
// trafficTickHook specifically (compose_test.go already includes it in that
// table too; this is the standalone proof for this file's own reading).
func TestTrafficTickHook_SingleShardAndDeterministic(t *testing.T) {
	_, comp := newTestEngineSeed(t, 8)
	h := &trafficTickHook{st: comp.state}

	for _, shard := range []int{1, 5, 255} {
		effects, err := h.RunShard(shard)
		if err != nil {
			t.Fatalf("RunShard(%d): %v", shard, err)
		}
		if len(effects) != 0 {
			t.Fatalf("RunShard(%d) returned %d effects, want 0", shard, len(effects))
		}
	}
	a, errA := h.RunShard(0)
	b, errB := h.RunShard(0)
	if errA != nil || errB != nil {
		t.Fatalf("RunShard(0): %v / %v", errA, errB)
	}
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("RunShard(0) effect count = %d / %d, want 1 / 1", len(a), len(b))
	}
	if a[0].Sequence != b[0].Sequence {
		t.Fatalf("RunShard(0) sequence differs: %d vs %d", a[0].Sequence, b[0].Sequence)
	}
	if !h.SingleShard() {
		t.Fatal("SingleShard() = false, want true (BUG-269 fast path)")
	}
}

// --- test helpers ------------------------------------------------------------

// newTestEngineSeed mirrors newTestEngine (compose_test.go) but without the
// variadic invariant.HookOption parameter, for tests in this file that only
// need a seed.
func newTestEngineSeed(t *testing.T, seed uint64) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}
