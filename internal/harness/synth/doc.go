// Package synth is H-SYNTH (MOD-016, module key harness.synth): a
// parametric synthetic-city generator (population, sprawl, network
// shape, seeded/deterministic) plus the perf-measurement machinery a CI
// job uses to graph tick-time vs. scale per commit and fail a build that
// regresses monthly-tick time more than 10% at the 1M-citizen synthetic.
//
// Module key: harness.synth (see code.json)
// Spec ref:   M0-ENG §2.4 ("H-SYNTH — synthetic world generator.
//
//	Parametric cities (population, sprawl, network shape) for
//	perf/scale testing: we must know the 10M-citizen tick cost in
//	month 3 of development, not month 30. Perf CI graphs tick-time vs
//	scale per commit."); M0-ENG §6 point 5 ("Perf is a test, not a
//	hope: H-SYNTH perf runs in CI; a commit that regresses monthly-
//	tick time >10% at the 1M-citizen synthetic fails."); §5.3
//	(100M-citizen memory/storage envelope, region shards, cold pass
//	streaming); §1 (target hardware: i7 8C/16T, 20GB RAM, RTX
//	3050-class 4GB VRAM).
//
// # The two named scale presets (AC-3)
//
// M0-ENG names exactly two citizen-count scale points this package must
// expose as individually addressable, named presets (presets.go):
// [OneMillionCitizens] (1,000,000 — the figure the perf CI regression
// gate itself runs against, §6 point 5) and [TenMillionCitizens]
// (10,000,000 — the figure §2.4 asks the team to know the tick cost of
// "in month 3 of development, not month 30"). [Preset1M] and [Preset10M]
// pair each with this package's own documented default sprawl/network-
// shape choice (presets.go) for a caller that only cares about the
// citizen-count scale point.
//
// # The 10% regression threshold (AC-6, AC-10)
//
// [RegressionThreshold] is 0.10 (10%), taken verbatim from M0-ENG §6
// point 5's own words: "a commit that regresses monthly-tick time >10%
// at the 1M-citizen synthetic fails." cmd/perfci is the CI-runnable
// command that generates the 1M-citizen synthetic, drives it a
// caller-chosen number of simulated months, compares the resulting
// monthly-tick time against the stored baseline for the current branch's
// history (results.go/baseline.go), and exits non-zero — never merely a
// logged warning (AC-10) — when that comparison regresses more than
// RegressionThreshold. See baseline.go's CompareToBaseline doc comment
// for the full account of how this avoids repeating BUG-031 (a hardcoded
// absolute wall-clock ceiling that a correct build blew past on a busy
// shared runner): the gate is RELATIVE to a stored baseline, guarded by
// a noise floor below which a percentage comparison is skipped rather
// than trusted, and every persisted measurement carries work-based
// counters (allocations, tick counts) alongside the wall-clock figure so
// a flagged regression can be cross-checked, not just believed.
//
// # Status: MOD-015 (harness.headless) landed mid-dispatch — this package now calls it for real
//
// This item's acceptance criteria (docs/planning/acceptance/
// harness.synth.md) name AC-2/AC-4/AC-6 in terms of "harness.headless"
// specifically, and the criteria document's own header said this item
// "remains blocked and is not in the Sprint 2 ready queue" pending
// MOD-015. At the START of this dispatch, internal/harness/headless/ had
// no buildable Go package on disk (only its code.json entry and a
// pre-registered MET-H200..MET-H203 error range existed) — MOD-015 was
// confirmed actively in progress the same day (see MOD-015's BOW
// comments). Rather than build nothing, or fake compliance with AC-2/
// AC-4/AC-6's literal wording, this package was FIRST built with a
// same-shape stand-in (a driveEngineMonths helper driving engine.core
// directly through the real protocol.Command/RunCommandLoop path — the
// same seam engine.detgate's RunGate already uses for the identical
// "no harness.headless yet" reason), logged as an assumption against
// this item's BOW record and escalated to Bill rather than presented as
// literal AC-2/AC-4/AC-6 compliance.
//
// MOD-015 landed later the same dispatch. headless_seam.go was rewritten
// to call the real harness.headless.Run directly — the stand-in is
// GONE, not merely deprecated, because keeping two implementations of
// one seam alive is exactly the drift GR#3 forbids. RunPerf's per-phase
// timing is now reconstructed from headless.Run's own -report NDJSON
// stream (report.go's phaseTimingRecord) rather than a second
// core.WithPhaseObserver of this package's own — one phase-timing
// implementation, not two. AC-2/AC-4/AC-6 are therefore built as
// literally specified, as of this dispatch's end state; see
// headless_seam.go's package-level comment for the mechanics.
//
// # Generated content is a Sprint-1-skeleton stand-in (assumption, logged)
//
// engine.core (MOD-012, done) is a walking skeleton with zero registered
// PhaseHooks — no citizen, finance, or land-value module exists yet to
// give a "10M-citizen tick" real simulated cost. Generate's citizen
// records (generator.go's synthCitizen) are therefore a small,
// deterministic placeholder shape scaled to exercise the SAME cost shape
// (O(citizenCount) allocation and generation work) a real citizen store
// will have, not a claim about what engine.citizens' eventual record
// shape will be. This means today's RunPerf numbers measure generation
// cost (which DOES scale with citizenCount, meaningfully, right now) and
// the walking-skeleton's per-tick dispatch overhead (which does NOT yet
// vary with citizenCount, because nothing reads the generated citizens)
// — the acceptance doc's own escalation flags this exact gap ("H-SYNTH's
// 10M preset necessarily runs against Sprint 1's stub/skeleton citizen
// model") and asks the owning BA to confirm at dispatch whether AC-3/
// AC-9's 10M preset is meaningful yet or should scope down to 1M until
// Sprint 3. This dispatch did not receive that confirmation before
// build, so both presets are built and tested, and the gap is restated
// here rather than silently assumed resolved.
//
// # Determinism (GR#21)
//
// Generate never calls math/rand or the wall clock on its generation
// path (grep -rn "time\.Now|math/rand" internal/harness/synth/generator.go
// returns no matches) — every citizen record is derived from
// det.NewStream(seed, 0, 0, "synth-citizen"), drawn in a fixed,
// documented order per NetworkShape (generator.go's placeCitizen), and
// serialize.NDJSONSerializer.WriteShard's own determinism guarantee
// (pinned gzip header fields, records emitted in next()'s exact order)
// carries that through to byte-identical output (AC-9,
// determinism_test.go). Generation is single-threaded by design — no
// goroutines, no worker pool of its own — so there is no worker-pool-
// size axis for Generate itself to vary across; see generator.go's
// Generate doc comment for the full argument and why
// determinism_test.go still exercises different GOMAXPROCS settings as
// defence in depth (AC-11's "if generation is single-threaded by design,
// doc.go states that explicitly" clause, satisfied here).
//
// # Files
//
//   - params.go — Params, NetworkShape enum, ValidateParams (AC-7b).
//   - limits.go — MaxSyntheticCitizens (ASM-083: reuses
//     solver.LocalCitizenCeilingHigh, GR#15), sprawl domain,
//     RegressionThreshold, MinMeasurableDuration.
//   - generator.go — Generate, the deterministic citizen-record stream.
//   - presets.go — Preset1M/Preset10M (AC-3).
//   - headless_seam.go — runHeadless: this package's single call site
//     into the real harness.headless.Run, plus parsePhaseTimings, which
//     reconstructs per-phase timing from headless.Run's -report stream.
//   - perf.go — RunPerf: per-phase timing + work counters (AC-4).
//   - results.go — AppendResult/LoadLatestBaseline: the NDJSON
//     per-commit results schema (AC-5).
//   - baseline.go — CompareToBaseline: the BUG-031-hardened regression
//     gate (AC-6, AC-8, AC-10).
//   - errors.go — MET-H3xx registry codes (GR#7).
//   - cmd/perfci — the CI-runnable perf gate command (AC-6).
//
// # Out of scope
//
// engine.core's own phase pipeline and shard worker pool (this package
// generates inputs and measures, it does not implement the tick pipeline
// being measured); balance.harness's (MOD-036, Sprint 8) parameter-sweep
// orchestration (a later consumer of this package's Generator, not built
// here); real terrain/geology-derived synthetic cities (engine.world,
// Sprint 3+ — NetworkShape is an abstract/procedural approximation, not
// an OS Terrain 50 import); wiring cmd/perfci into an actual GitHub
// Actions job beyond what .github/workflows/ci.yml already carries as of
// this dispatch (see that file's own comments for current status).
package synth
