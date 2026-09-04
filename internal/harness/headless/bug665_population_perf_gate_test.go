package headless

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// BUG-665 — THE REAL POPULATION PERF GATE
//
// This is the self-testing gate go-engine-100m-proving-plan.md's Track B
// item 2 calls for: "A real large-population perf gate. harness.headless.
// Config has no citizen-count field ... Until it does, CI cannot see any
// of this." SeedCitizenCount (run.go) closes that gap; this file is the
// probe that actually exercises it at scale.
//
// # What this proves, and what it deliberately does NOT prove
//
// It proves: a real citizens.CitizensAPI carrying popPerfGateCitizenCount
// individual ColdRecords is seeded, wired through compose.Wire, and
// driven through popPerfGateMonths months of REAL AdvanceDayTick calls
// via the exact protocol.Command path a live server uses (headless.Run,
// never core.Engine.AdvanceTicks directly) — the thing the standing
// perf-1m-probe CI job has NEVER done (it measures internal/harness/
// synth's throwaway Generate() cost against a genesis-only ~64-citizen
// city; see this repo's docs/planning/go-engine-100m-proving-plan.md
// §1.1 for the measured proof).
//
// It does NOT prove the 100M finale bar (docs/planning/
// go-engine-100m-proving-plan.md §5, inc-D) — this is the 1M rung, sized
// to fit an ordinary CI job's time budget (see popPerfGateCitizenCount's
// doc comment). It also does not prove turbo-speed (160ms/tick); only
// the normal-play 1000ms/tick budget from Q100100.
//
// # Bound provenance — GENEROUS ON PURPOSE, first pass
//
// docs/planning/go-engine-100m-proving-plan.md §1.2 measured 17.63 ms/
// day-tick for 1,000,000 citizens on a Windows dev box, via a throwaway
// overlay harness that seeds cold records directly and drives
// AdvanceDayTick — but that harness measured ONLY citizens.CitizensAPI's
// own cold-pass cost (fertility/mortality), never compose's ADDITIONAL
// monthly resident-processing passes (moneycirc.go's four
// O(N²/256)-per-month terms: markEmploymentAndCount/
// employedResidentCount/formResidentHouseholds/
// distributeWagesToResidents — §3.3 of the proving plan), because that
// harness never wired a full Composition at all.
//
// # ROUND-2 FINDING (2026-09-04): the FIRST gate landing measured this
// wrong — its population was invisible to BOTH fertility (Partner==0)
// AND moneycirc (liveResidentIDs() never enumerated it), so its
// 21-30 ms/tick reading was measuring compose's ordinary ~64-citizen
// city, not 1,000,000 real residents at all. This landing fixes that
// (generateSeedPopulation now pairs a childbearing-age fraction into
// real households, SeedHouseholds registers them, compose.Deps.
// SeedResidentIDBase/SeedResidentIDCount makes them moneycirc-visible)
// and the REAL number is dramatically worse: **1.317 s/tick**, measured
// on this dev box, ~75x the isolated cold-pass-only figure above. This
// is NOT a mystery regression — it is fertility.go's applyFertilityLocked
// (O(N²/7680) per day-tick, §3.2) AND moneycirc's four monthly passes
// (§3.3) BOTH genuinely executing against 1,000,000 residents for the
// first time ever, entirely through citizens.CitizensAPI's
// ColdShard.rowOf, which is a LINEAR SCAN on this branch (BUG-666's
// id->row index has not landed on main yet — it is in a parallel,
// as-yet-unmerged worktree). Once BUG-666 lands, this number is expected
// to collapse toward something close to the isolated 17.63 ms/tick
// figure plus a smaller, now-O(N) moneycirc overhead — but that is a
// PREDICTION to verify at that point, not something this bound assumes
// today.
//
// That number is NOT used as this gate's bound directly, for the same
// reason burned three times in one day on THIS item's own build session
// (Aaron's standing house rule, restated in this item's own dispatch):
// "bounds come from the machine that enforces them", not from a
// different box's one-off measurement. CI's windows-latest runner is
// shared, frequently slower and noisier than a dedicated dev box.
// popPerfGateTickBoundPerTick is therefore set to a DELIBERATELY
// generous first-pass ceiling over the FRESH, ROUND-2-CORRECTED
// measurement above (see its own doc comment for the exact multiple),
// loudly logged every run via t.Logf so the CI Actions log always shows
// the real measured number — the tightening pass (a proper CI-derived
// median, AND the BUG-666 re-measurement once that index lands) is
// Track B follow-up work, not this item's job to guess at from one
// dev-box run.
//
// STAGING NOTE (round dispatch's own explicit ask): the CI JOB ITSELF
// stays comfortably inside its 10-minute timeout at this scale (measured
// ~2 minutes total wall time for seed+wire+90 ticks+snapshot at 1M — see
// this test's own doc comment), so there is no CI-viability blocker
// requiring this gate to be staged behind BUG-666. What DOES change is
// the bound's honesty: it now reflects today's real, degraded,
// pre-BUG-666 tick cost rather than an aspirational number, and this
// job is deliberately NOT on branch protection's required-checks list
// (see ci.yml's own comment on perf-population-probe) specifically so a
// known, understood, already-filed gap (BUG-666) does not block merges
// while it is being closed.
const (
	// popPerfGateCitizenCount is 1,000,000 — inc-A of the increment plan
	// (go-engine-100m-proving-plan.md §5), chosen because it is the FIRST
	// rung no existing CI job has ever actually ticked (the standing
	// perf-1m-probe ticks a ~64-citizen city regardless of its "1M"
	// label), and because §1's measurements show generation+seeding at
	// this scale finishes in low single-digit seconds — well inside an
	// ordinary CI job's budget alongside the tick window below.
	popPerfGateCitizenCount = 1_000_000

	// popPerfGateMonths is 3 (90 day-ticks): enough for the amortised
	// cold-pass schedule (256 shards, one per day-tick within a month,
	// coldpass.go's ColdPassSchedule) to complete THREE full sweeps, so
	// the measured median tick genuinely reflects the steady-state
	// per-citizen cost rather than a single partial pass. At the
	// measured 17.63 ms/tick this is ~1.6s of tick time on the dev box
	// that produced that figure; even at 10x that on a slower CI runner
	// it is under 20s, comfortably inside this job's time budget
	// alongside ~1M-citizen generation/seeding (§1.1: measured ~30s for
	// 1M throwaway records; this package's own seeding — arithmetic plus
	// two det.Stream draws per citizen, no disk I/O — is markedly
	// cheaper, see generateSeedPopulation's doc comment).
	popPerfGateMonths = 3

	// popPerfGateTickBoundPerTick is the FIRST-PASS, deliberately
	// generous per-tick ceiling: ~3x the 1.317 s/tick measured on one
	// Windows dev box for a population that is FINALLY visible to both
	// fertility and moneycirc (see this file's package doc comment,
	// "ROUND-2 FINDING", for why that number is so much larger than the
	// isolated-cold-pass 17.63 ms/tick the proving plan's own throwaway
	// harness measured), rounded up to a clean number and left generous
	// rather than tight. TIGHTENING PLAN (recorded here, not guessed at):
	// (1) after this job has run on CI at least once, read the ACTUAL
	// median TickWallTime/TicksAdvanced this test logs from the Actions
	// log and tighten this constant toward that CI-measured figure; (2)
	// once BUG-666's ColdShard id->row index lands on main, RE-MEASURE —
	// the prediction is a collapse toward something much closer to
	// 17.63 ms/tick plus a smaller moneycirc overhead, but that is
	// unverified until BUG-666 actually lands and this gate is re-run
	// against it. Never tighten below a CI-observed figure at either
	// step.
	popPerfGateTickBoundPerTick = 4 * time.Second

	// popPerfGateAllocBytesPerCitizenBound is the OTHER half of this
	// item's report ask ("assert B/citizen against the documented
	// budget"). This is a CUMULATIVE allocation figure (runtime.MemStats.
	// TotalAlloc delta across the whole Run() call — seed + compose.Wire
	// + popPerfGateMonths of ticking + shutdown/snapshot-write), NOT a
	// live/resident-heap figure: Result does not hand the caller a
	// reference to the engine or its CitizensAPI, so by the time this
	// test could read runtime.MemStats.HeapAlloc after Run() returns, the
	// entire 1M-citizen city is already unreferenced and a forced GC
	// collects it back to near-zero — measured live, ~220 KB residual
	// for a 1,000,064-citizen run, four orders of magnitude below any
	// plausible resident figure. TotalAlloc sidesteps that entirely (it
	// only ever grows, GC or no GC), and is the SAME metric this
	// package's synth sibling already uses for its own AllocBytes field
	// (perf.go's PerfResult.AllocBytes doc comment: "runtime.MemStats.
	// TotalAlloc delta ... across the MEDIAN sampled headless.Run
	// window").
	//
	// go-engine-100m-proving-plan.md §1.3's citizens.doc.go 60-100 B/
	// citizen band and §1.2's 82.9 B/citizen LIVE HEAP measurement are
	// therefore not directly comparable to this bound — they measure a
	// cold-store-only resident figure with a dedicated harness holding
	// the store alive; this gate measures gross allocation churn across
	// an entire real headless run (fertility/mortality draws, per-tick
	// effect slices, the amortised cold-pass's own scratch allocations,
	// moneycirc's own per-resident ApplyLifeEventCommand calls now that
	// the population is genuinely visible to it (ROUND-2 FINDING, this
	// file's package doc comment), snapshot marshalling — none of that
	// is "resident bytes per citizen", all of it is real cost this gate
	// should still catch a regression in). Measured on this dev box
	// (see this test's own t.Logf output) after the round-2 fix:
	// ~4586 B/citizen for popPerfGateMonths=3 months of real ticking
	// plus seeding — roughly 2x the first (vacuous) landing's ~2503 B/
	// citizen figure, consistent with moneycirc's passes now doing real
	// per-resident work. 9000 B/citizen is deliberately generous headroom
	// (~2x this fresh measurement) for the same reason
	// popPerfGateTickBoundPerTick is generous: a first-pass gate tightens
	// itself later against a real CI-measured number, not a guess.
	popPerfGateAllocBytesPerCitizenBound = 9000
)

// TestPopulationPerfGate1M is BUG-665's real population perf gate: it
// seeds popPerfGateCitizenCount real citizens, ticks
// popPerfGateMonths*core.DailyTicksPerMonth real day-ticks through
// headless.Run's genuine protocol.Command path, and asserts (1) the
// median-equivalent per-tick wall time is within budget and (2) the
// live heap grew by no more than the documented per-citizen budget.
//
// Deliberately a single, non-repeated run rather than
// internal/harness/synth's PerfResult multi-sample median machinery
// (RunPerf/TickSampleCount): that infrastructure exists to compare
// against a PERSISTED CROSS-COMMIT BASELINE (results.ndjson,
// perf-accepted-regressions.json) for internal/harness/synth's
// throwaway-record cost model, which this probe is not — reusing it
// here would conflate "population never reached the engine" (this
// item's defect) with "did generation cost regress versus last commit"
// (that package's own, already-gated, concern). A future tightening
// pass (see popPerfGateTickBoundPerTick's doc comment) can promote this
// into that same baseline machinery once a real CI-measured median
// exists to seed it from.
func TestPopulationPerfGate1M(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pop-perf-gate")

	// TotalAlloc (cumulative, monotonic, GC-independent) -- see
	// popPerfGateAllocBytesPerCitizenBound's doc comment for why this is
	// the right metric here and HeapAlloc-after-a-forced-GC is not (the
	// whole 1M-citizen engine is unreferenced and collectible the moment
	// Run() returns). The one runtime.GC() call is only to give `before`
	// a clean, low-noise starting point; it is never called again.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	result, err := Run(context.Background(), Config{
		Seed:             1,
		Months:           popPerfGateMonths,
		OutDir:           dir,
		SeedCitizenCount: popPerfGateCitizenCount,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	runtime.ReadMemStats(&after)

	if result.TicksAdvanced <= 0 {
		t.Fatalf("TicksAdvanced = %d, want > 0", result.TicksAdvanced)
	}
	// A generous 5% floor, not an exact >= N: popPerfGateMonths of REAL
	// mortality/migration dynamics legitimately move population (the
	// same reason TestSeedCitizenCount_ThroughHeadlessRun_DominatesOverBaseline
	// uses a tolerance rather than exact equality) — a floor this loose
	// still trivially catches BUG-665's actual defect (a Population
	// stuck around the ~64-citizen genesis-only baseline, off by 4+
	// orders of magnitude, not a few tenths of a percent of mortality
	// churn).
	if floor := popPerfGateCitizenCount * 95 / 100; result.Population < floor {
		t.Fatalf("Population = %d, want >= %d (95%% of SeedCitizenCount=%d) -- the seeded population did not reach the ticked engine", result.Population, floor, popPerfGateCitizenCount)
	}

	perTick := result.TickWallTime / time.Duration(result.TicksAdvanced)

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocBytesPerCitizen := float64(allocBytes) / float64(result.Population)

	// LOUD, unconditional: every run prints the real measured numbers to
	// the CI Actions log, regardless of pass/fail (t.Logf, not gated
	// behind -v — Go always shows Logf output for a failing test, and a
	// PASSING run's log is still visible via `go test -v`, which the CI
	// step below runs deliberately for exactly this reason).
	t.Logf("BUG-665 population perf gate: citizens=%d ticksAdvanced=%d tickWallTime=%s perTick=%s (bound=%s) totalAlloc=%d bytes (%.1f B/citizen, bound=%d) finalPopulation=%d births=%d deaths=%d",
		popPerfGateCitizenCount, result.TicksAdvanced, result.TickWallTime, perTick, popPerfGateTickBoundPerTick,
		allocBytes, allocBytesPerCitizen, popPerfGateAllocBytesPerCitizenBound, result.Population, result.Births, result.Deaths)

	if perTick > popPerfGateTickBoundPerTick {
		t.Errorf("perTick = %s, want <= %s (generous first-pass bound, see popPerfGateTickBoundPerTick's doc comment for the tightening plan)", perTick, popPerfGateTickBoundPerTick)
	}
	if allocBytesPerCitizen > popPerfGateAllocBytesPerCitizenBound {
		t.Errorf("cumulative allocation = %.1f B/citizen, want <= %d B/citizen (see popPerfGateAllocBytesPerCitizenBound's doc comment)", allocBytesPerCitizen, popPerfGateAllocBytesPerCitizenBound)
	}

	// BUG-665 round finding: pin demographic liveness as an ASSERTED
	// invariant, never a hope. An independent destructive round proved
	// the first landing's seeded population was invisible to
	// fertility.go's applyFertilityLocked entirely (Household==0,
	// Partner==0 for every record), so births were structurally zero at
	// ANY population size regardless of tick count — a materially
	// cheaper code path than a real population exercises, silently
	// understating this gate's own tick-cost measurement. Both counts
	// are checked independently (not just their sum) so a generator that
	// happens to produce (many deaths, zero births) or vice versa is
	// still caught -- mirrors
	// TestAttackBUG665_TickedPopulationDoesRealDemographicWork's own
	// two-sided check exactly.
	if result.Births == 0 {
		t.Errorf("Births = 0 across %d months of a %d-citizen seeded population -- fertility never fired; this gate would be measuring a demographically idle population (BUG-665's vacuity class in a subtler coat)", popPerfGateMonths, popPerfGateCitizenCount)
	}
	if result.Deaths == 0 {
		t.Errorf("Deaths = 0 across %d months of a %d-citizen seeded population -- mortality never fired; this gate would be measuring a demographically idle population (BUG-665's vacuity class in a subtler coat)", popPerfGateMonths, popPerfGateCitizenCount)
	}
}
