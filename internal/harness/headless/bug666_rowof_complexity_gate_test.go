package headless

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// FEAT-2326609830 (BUG-666 residue) -- COUNTER-BASED COMPLEXITY GATE.
//
// BUG-666 made ColdShard.rowOf a map lookup, but nothing recorded the
// scaling SHAPE afterwards, and the only 1M gate (bug665_population_perf_
// gate_test.go) bounded wall time, which cannot see a regression back to
// O(N^2). This gate counts OPERATIONS instead: rowOf calls and rowOf probe
// steps (citizens.EnableRowOfProbe), and TotalAlloc deltas, over the TICK
// PHASE ONLY (preTickHook zeroes the counters after seed + Wire). These
// are operation counts and scaling shape; the probe records 1 per rowOf
// call, so it does NOT itself redden on a scan regression. That detection
// is citizens.TestAttackFEAT830RowOfIsIndexOnly (plus the index-vs-linear
// oracle in attack_bug666_round_test.go).
//
// Composition: a LIVE city -- Config.SeedCitizenCount builds a paired,
// childbearing-age population registered via SeedHouseholds and made
// moneycirc-visible via Deps.SeedResidentIDBase/Count (headless.Run, the
// real compose.Wire path), 3 months = 90 day-ticks so every monthly pass
// fires three times, births AND deaths asserted non-zero (a run with
// either at zero is a fixture failure, not a pass).
//
// NO wall-clock assertion exists in this file. Wall time is not read at
// all here (Result.TickWallTime is printed by the sibling gate).

const rowOfGateMonths = 3

type rowOfRun struct {
	n          int64
	calls      uint64
	probes     uint64
	maxPerCall uint64
	tickAlloc  uint64
	births     int64
	deaths     int64
	population int
	hash       [32]byte
}

func runRowOfLive(t *testing.T, n int64) rowOfRun {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "rowof-gate")
	var before runtime.MemStats
	preTickHook = func() {
		citizens.EnableRowOfProbe(true)
		runtime.GC()
		runtime.ReadMemStats(&before)
	}
	defer func() {
		preTickHook = nil
		citizens.EnableRowOfProbe(false)
	}()
	res, err := Run(context.Background(), Config{Seed: 1, Months: rowOfGateMonths, OutDir: dir, SeedCitizenCount: n})
	if err != nil {
		t.Fatalf("Run(n=%d): %v", n, err)
	}
	snap := citizens.ReadRowOfProbe()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return rowOfRun{
		n: n, calls: snap.Calls, probes: snap.Probes, maxPerCall: snap.MaxPerCall,
		tickAlloc: after.TotalAlloc - before.TotalAlloc,
		births:    res.Births, deaths: res.Deaths, population: res.Population, hash: res.PopulationHash,
	}
}

func assertRowOfLive(t *testing.T, r rowOfRun) {
	t.Helper()
	perLookup := float64(r.probes) / float64(r.calls)
	t.Logf("rowOf gate N=%d months=%d population=%d births=%d deaths=%d lookups=%d probes=%d probes/lookup=%.3f maxProbesPerCall=%d tickPhaseAlloc=%d B",
		r.n, rowOfGateMonths, r.population, r.births, r.deaths, r.calls, r.probes, perLookup, r.maxPerCall, r.tickAlloc)
	if r.births == 0 || r.deaths == 0 {
		t.Fatalf("fixture not live: births=%d deaths=%d at N=%d (a demographically idle city measures nothing)", r.births, r.deaths, r.n)
	}
	if r.calls == 0 {
		t.Fatalf("no rowOf lookups counted at N=%d -- the probe is not wired to the real path", r.n)
	}
	// AC-1: exactly one probe per lookup, and no single lookup took more.
	if r.probes != r.calls || r.maxPerCall != 1 {
		t.Errorf("rowOf probes/lookup = %.3f (max per call %d), want exactly 1.000 / 1 -- rowOf is no longer a single index probe", perLookup, r.maxPerCall)
	}
}

// ratioBound reports whether got/base is within bound.
func ratioWithin(got, base uint64, bound float64) (float64, bool) {
	r := float64(got) / float64(base)
	return r, r <= bound
}

// TestRowOfComplexityGate_100k_300k covers AC-1, AC-2 (3N bound) and AC-7
// at the default-run points. The 1M point is TestRowOfComplexityGate_1M.
func TestRowOfComplexityGate_100k_300k(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-scale live-composition gate is too slow for -short")
	}
	a := runRowOfLive(t, 100_000)
	assertRowOfLive(t, a)
	b := runRowOfLive(t, 300_000)
	assertRowOfLive(t, b)

	rl, okL := ratioWithin(b.calls, a.calls, 3.3)
	ra, okA := ratioWithin(b.tickAlloc, a.tickAlloc, 3.6)
	t.Logf("ratio table 300k/100k: lookups x%.3f (bound 3.3), tick-phase alloc x%.3f (bound 3.6)", rl, ra)
	if !okL {
		t.Errorf("lookups(3N)/lookups(N) = %.3f, want <= 3.3 (operation count grew super-linearly)", rl)
	}
	if !okA {
		t.Errorf("tick-phase alloc(3N)/alloc(N) = %.3f, want <= 3.6", ra)
	}

	// AC-7: same seed twice -> byte-identical PopulationHash.
	a2 := runRowOfLive(t, 100_000)
	if a2.hash != a.hash {
		t.Errorf("PopulationHash differs between two identical runs at N=100000: %x vs %x", a.hash, a2.hash)
	}
	if a2.calls != a.calls {
		t.Errorf("lookup count not deterministic at N=100000: %d vs %d", a.calls, a2.calls)
	}
}

// TestRowOfComplexityGate_1M is the 10N point (AC-2: lookups(10N)/lookups(N)
// <= 11). Env-gated behind METRO_PERF_CURVES=1 (same switch as
// internal/engine/citizens/tickperf_test.go) because a 1M live run takes
// minutes.
//
// UPDATE: measured at about 35 s total on a dev box, so it is NOT gated
// behind METRO_PERF_CURVES; it runs by default and skips only under -short.
func TestRowOfComplexityGate_1M(t *testing.T) {
	if testing.Short() {
		t.Skip("1M live-composition point is heavy (about 35 s); skipped under -short")
	}
	a := runRowOfLive(t, 100_000)
	assertRowOfLive(t, a)
	m := runRowOfLive(t, 1_000_000)
	assertRowOfLive(t, m)
	rl, okL := ratioWithin(m.calls, a.calls, 11)
	ra, okA := ratioWithin(m.tickAlloc, a.tickAlloc, 12)
	t.Logf("ratio table 1M/100k: lookups x%.3f (bound 11), tick-phase alloc x%.3f (bound 12)", rl, ra)
	if !okL {
		t.Errorf("lookups(10N)/lookups(N) = %.3f, want <= 11", rl)
	}
	if !okA {
		t.Errorf("tick-phase alloc(10N)/alloc(N) = %.3f, want <= 12", ra)
	}
}
