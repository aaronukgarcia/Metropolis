// Package synth is H-SYNTH (MOD-016, module key harness.synth): a
// parametric synthetic-city generator (population, sprawl, network
// shape, seeded/deterministic) plus the perf-measurement machinery a CI
// job uses to graph tick-time vs. scale per commit and fail a build that
// regresses monthly-tick time more than 10% at the 1M-citizen synthetic.
//
// Module key: harness.synth (see code.json; GUID 2cabd726-8b86-4254-a07d-ab202f6a6a75)
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
// # BUG-034: what this package's baseline currently proves, and does not
//
// Read this before trusting any number this package has ever recorded,
// or will record before Sprint 3:
//
//   - PROVES the plumbing is real end to end: a generated 1M-citizen
//     synthetic city IS consumable by harness.headless with no
//     translation step, RunPerf DOES drive it through the real
//     protocol.Command path, results ARE persisted and compared, and a
//     regression DOES fail the CI job non-zero (AC-2/AC-4/AC-6/AC-10, all
//     verified against a real, if walking-skeleton, engine — not mocked).
//   - PROVES Generate's own O(citizenCount) generation cost at real
//     scale — GenerationTime/GenerationAllocBytes/GenerationAllocCount
//     (perf.go) are genuine, scale-sensitive measurements today, and a
//     regression in world-generation cost is a real regression this gate
//     can already catch once it runs at real 1M scale (see
//     .github/workflows/ci.yml's perf-1m-probe job).
//   - DOES NOT prove anything about simulated per-citizen tick cost.
//     TickTime/PerMonthTick are real wall-clock numbers, but every
//     PerfResult also carries PhaseHookCount (phasehooks.go), which is 0
//     for every run this package has ever produced — engine.core has
//     zero registered PhaseHooks as of this sprint, so PerMonthTick
//     today measures walking-skeleton dispatch overhead, not simulation.
//     A "1M-citizen tick cost" quoted from this package's history before
//     PhaseHookCount is nonzero is exactly the "pure walking-skeleton
//     overhead wearing a simulation label" BUG-034 named as the risk to
//     defend against — check PhaseHookCount before trusting any quoted
//     tick figure, always.
//   - NOW includes a CI-runner-measured baseline, so the "the 1M baseline
//     is not recorded" gap is closed. BUG-034's perf-1m-probe job
//     (.github/workflows/ci.yml) ran for real on windows-latest and
//     recorded the first 1M baseline (PerMonthTick 488.866us, run
//     31539765424, commit 303d3ac, 6.73s wall / 43.2MB peak), and the
//     gate flip (commit 5bfc381, 2026-08-12) then made perf-1m-probe a
//     REQUIRED push/PR merge check running the real 1M preset. Every one
//     of those records is still a walking-skeleton measurement
//     (PhaseHookCount is 0) — see the bullet above — but the baseline is
//     genuinely recorded and CI-runner-measured now, not pending. See
//     limits.go's MinMeasurableDuration doc comment for the noise-floor
//     re-derivation against both local and CI-runner jitter.
//
// # Files
//
//   - params.go — Params, NetworkShape enum, ValidateParams (AC-7b).
//   - limits.go — MaxSyntheticCitizens (ASM-083: reuses
//     solver.LocalCitizenCeilingHigh, GR#15), sprawl domain,
//     RegressionThreshold, CumulativeRegressionThreshold (BUG-083: the
//     second, anchor-based drift check), MinMeasurableDuration
//     (BUG-034: re-derived against sampled local jitter data, see that
//     constant's doc
//     comment).
//   - generator.go — Generate, the deterministic citizen-record stream.
//   - presets.go — Preset1M/Preset10M (AC-3).
//   - headless_seam.go — runHeadless: this package's single call site
//     into the real harness.headless.Run, plus parsePhaseTimings, which
//     reconstructs per-phase timing from headless.Run's -report stream.
//   - perf.go — RunPerf: per-phase timing + work counters (AC-4), plus
//     BUG-034's PhaseHookCount and generation-side alloc counters
//     (GenerationAllocBytes/GenerationAllocCount, kept separate from the
//     tick-side AllocBytes/AllocCount for the same reason
//     GenerationTime/TickTime are kept separate), and BUG-055's Measured
//     provenance flag (true only on a PerfResult RunPerf itself
//     produced).
//   - phasehooks.go — PhaseHookCountInHeadlessPath (BUG-034): the
//     manually-asserted fact every PerfResult carries, guarded by an
//     AST-level scan (upgraded from a plain-text grep by BUG-053 after a
//     live-verified method-value bypass) — read its doc comment for
//     exactly what it does and does not prove, and phasehooks_test.go's
//     doc comment for the honest verdict on what a source-level scan can
//     never fully guarantee.
//   - results.go — AppendResult/LoadLatestBaseline: the NDJSON
//     per-commit results schema (AC-5). AppendResult rejects a record
//     whose Result.Measured is false (BUG-055, MET-H308), whose values
//     are structurally implausible (BUG-085, MET-H310, negative
//     CitizenCount/Months/PerMonthTick, or — BUG-096 — an implausibly
//     GIGANTIC PerMonthTick), or whose AcceptedRegression override
//     carries no reason (BUG-083, MET-H311). LoadLatestBaseline skips
//     and reports (never silently swallows, GR#17), rather than
//     aborting on, a malformed/torn line, so a good later baseline still
//     recovers (BUG-054); it now also RECONSTRUCTS the baseline by
//     replaying CompareToBaseline forward through history rather than
//     trusting whatever was appended last (BUG-083) — see its own doc
//     comment for the live-verified 30-commit, 13.27x, zero-signal
//     ratchet this closes. AcceptedRegression is honoured ONLY when
//     corroborated by accepted.go's git-committed AcceptedRegistry
//     (BUG-095) — the record's own fields are no longer sufficient on
//     their own, because a hand-injected "accepted" record was
//     live-verified to fully bypass BUG-083's fix otherwise.
//   - accepted.go — AcceptedRegistry/LoadAcceptedRegistry (BUG-095): the
//     git-committed {preset, commitHash, reason} acceptance evidence
//     that lives OUTSIDE the results file and its cache-persisted,
//     forgeable-by-a-second-writer channel — see its own doc comment for
//     why this is the control rather than one more check on the record.
//   - baseline.go — CompareToBaseline: the BUG-031-hardened regression
//     gate (AC-6, AC-8, AC-10), now with a second, independent check
//     (CumulativeRegressionThreshold, limits.go) against a FIXED anchor
//     reference point — BUG-083: a relative gate compared only against
//     a moving reference point cannot see sustained drift, no matter
//     how the moving point is advanced.
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
