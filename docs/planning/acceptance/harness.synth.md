BOW code: MOD-016

# Acceptance criteria — harness.synth (MOD-016)

**BOW code:** MOD-016
**Spec refs:** M0-ENG §2.4 (`docs/METROPOLIS-MASTER-v2.1.md` line 849: "H-SYNTH — synthetic world generator. Parametric cities (population, sprawl, network shape) for perf/scale testing: we must know the 10M-citizen tick cost in month 3 of development, not month 30. Perf CI graphs tick-time vs scale per commit."); M0-ENG §6 point 5 (working agreement, line 998: "Perf is a test, not a hope: H-SYNTH perf runs in CI; a commit that regresses monthly-tick time >10% at the 1M-citizen synthetic fails."); §5.3 (100M-citizen memory/storage envelope, region shards, cold pass streaming, lines 181-182); M0-ENG §1 (target hardware: i7 8C/16T, 20GB RAM, RTX 3050-class 4GB VRAM, line 787); code.json `harness.synth` entry (consumes `engine.core` MOD-012 and `harness.headless` MOD-015).
**Date:** 2026-08-08
**Status:** draft-ahead
**Package under test:** `internal/harness/synth/` (path from `node claude-bow.js show MOD-016`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/harness/synth/...`.

## User stories

- **US-1.** As the development team, I need to know the 10M-citizen monthly-tick cost in month 3 of development (Sprint 2), not month 30 (M0-ENG §2.4), so that a scale problem is discovered while there is still runway to pull the GPU sidecar forward per the sprint plan's guard-rail.
- **US-2.** As CI, I need parametric synthetic cities (population, sprawl, network shape) generated deterministically from a seed and a size parameter, so that every commit can be perf-tested against the same reproducible scale points without needing a hand-built save.
- **US-3.** As the perf CI job, I need tick-time-vs-scale graphed per commit and a hard fail when monthly-tick time regresses >10% at the 1M-citizen synthetic, so that performance is a test, not a hope (M0-ENG §6 point 5).
- **US-4.** As `balance.harness` (a later, Sprint 8 consumer per code.json), I need H-SYNTH's generated worlds to be usable as its own parameter-sweep inputs, so that balance tuning and perf testing share one synthetic-city generator rather than two.

## Scope

Parametric synthetic-city generation (population, sprawl, network shape, seeded/deterministic) and the perf CI job that graphs tick-time vs. scale per commit and fails on >10% regression at the 1M-citizen synthetic.

## Acceptance criteria

### Functional

- **AC-1.** A `Generator` (or equivalent) exists that produces a synthetic world/save given at minimum: target citizen count, a seed, and sprawl/network-shape parameters. Check: `go doc ./internal/harness/synth Generator` (or top-level `Generate` function) shows these parameters.
- **AC-2.** Generated worlds are consumable by `harness.headless` (`H-HEADLESS`, `metropolis -headless -seed N -months M -out snap.json` per M0-ENG §2.3) without a translation step — i.e. `Generate` produces the same save/bundle shape `int.serializer`/`engine.core` already expect. Check: a passing integration test feeds a generated world into the headless harness's run entry point (`grep -rn "headless\." internal/harness/synth/*_test.go` finds it).
- **AC-3.** At least these named scale presets exist and are individually addressable: 1M-citizen and 10M-citizen synthetics (the two figures the spec names explicitly). Check: `grep -n "1_000_000\|1000000\|10_000_000\|10000000" internal/harness/synth/*.go` finds both, ideally as named constants (`grep -n "OneMillion\|TenMillion\|Preset1M\|Preset10M"`).
- **AC-4.** A perf-measurement entry point exists that runs a synthetic city for a fixed number of simulated months under `harness.headless` and records per-phase and total monthly-tick timing. Check: `go doc ./internal/harness/synth RunPerf` (or equivalent) exists; output includes per-phase timing (matches M0-ENG §2.3's "per-phase timing + invariant reports every tick").
- **AC-5.** Timing results are persisted per-commit in a form a CI graphing step can consume (e.g. JSON/CSV keyed by commit hash and scale preset), not only printed to stdout. Check: `grep -n "json.Marshal\|csv.Writer" internal/harness/synth/perf.go` (or equivalent) matches, and a passing test asserts the output file's schema (`grep -rn "func Test.*[Pp]erf.*[Oo]utput\|func Test.*[Rr]esult" internal/harness/synth/*_test.go`).
- **AC-6.** A CI-runnable command/script (e.g. `go run ./internal/harness/synth/cmd/perfci` or documented `go test` target) exists that: generates the 1M-citizen synthetic, runs N months headless, compares monthly-tick time against a stored baseline for the current branch's parent commit, and exits non-zero on a >10% regression. Check: the command/script exists and its `--help`/doc-comment states the 10% threshold explicitly (`grep -n "10%\|0.10\|1.10" internal/harness/synth/*.go`).

### Error handling

- **AC-7 (GR#7).** Requesting an out-of-range or nonsensical parameter combination (e.g. negative population, sprawl parameter outside its documented domain) returns a registry-sourced error rather than generating a corrupt or silently-clamped world. Check: `grep -n "MET-" internal/harness/synth/*.go` finds a registry code reference; a passing test covers invalid input (`grep -rn "func Test.*[Ii]nvalid\|func Test.*[Oo]utOfRange" internal/harness/synth/*_test.go`).
- **AC-8.** A missing perf baseline (first run on a new scale preset, or a fresh CI cache) does not fail the build — it records a new baseline and reports "no prior baseline to compare" rather than treating "no baseline" as a 10% regression. Check: passing test coverage (`grep -rn "func Test.*[Bb]aseline" internal/harness/synth/*_test.go`).

### Determinism & safety

- **AC-9 (GR#21).** The same `(seed, citizenCount, sprawl params)` tuple always generates a byte-identical world across repeated runs and across worker-pool sizes (consistent with §5's counter-based hash-stream determinism rule and the shard-count invariance property the sprint plan's S3 exit gate later depends on). Check: a passing test generates the same synthetic twice (and, if feasible at this stage, at two different `GOMAXPROCS`/worker settings) and asserts identical output bytes or hash (`grep -rn "func Test.*[Dd]eterminis" internal/harness/synth/*_test.go`).
- **AC-10 (M0-ENG §6 point 5; GR#21 "perf is a test, not a hope").** The perf CI command from AC-6 is wired to actually fail the build (non-zero exit) on regression — not merely log a warning. Check: `grep -n "os.Exit(1)\|log.Fatal" internal/harness/synth/cmd/perfci/*.go` (or equivalent) matches on the regression path.
- **AC-11.** `go test ./internal/harness/synth/... -race -count=1` passes with no data race in generation or perf-measurement code that uses the shard worker pool. Check: `grep -n "go func()" internal/harness/synth/*_test.go` finds at least one concurrency test, if the generator itself is parallelised; if generation is single-threaded by design, `doc.go` states that explicitly and this AC is satisfied by SG-4 passing with `-race` and no goroutine use.

### Documentation

- **AC-12.** `internal/harness/synth/doc.go` states the module key `harness.synth`, cites M0-ENG §2.4 and §5, and documents the 1M/10M scale presets and the 10% regression threshold in prose (not only as bare constants). Check: `grep -n "harness.synth" internal/harness/synth/doc.go` and `grep -n "10%" internal/harness/synth/doc.go` both match.
- **AC-13.** A short doc explains how the perf CI job's baseline is stored/updated across commits (so a future BA/Tester/QA can audit whether the gate is actually wired into CI, per the sprint plan's S2 exit gate "perf CI graphs tick-time vs 1M/10M synthetic"). Check: file exists and names the storage location (e.g. a CI artifact path or a checked-in baseline file).

## Out of scope

- `engine.core`'s (MOD-012) actual phase pipeline and shard worker pool — this item generates inputs and measures, it does not implement the tick pipeline being measured.
- `balance.harness`'s (MOD-036, Sprint 8) parameter-sweep orchestration — that item consumes H-SYNTH's generator later; this item only needs to expose a usable, documented API for it.
- Real terrain/geology-derived synthetic cities (`engine.world`, Sprint 3) — H-SYNTH's "network shape" parameter is an abstract/procedural approximation, not an OS Terrain 50 import.
- Wiring the perf CI job into an actual CI runner configuration (GitHub Actions YAML, etc.) if no CI runner exists yet at build time — the command/script must exist and be locally runnable per AC-6; actual CI wiring may be a follow-up BOW item if the repo's CI infrastructure isn't ready.

## Escalations

- **Assumption flagged (per BA instructions §3).** This item depends on `MOD-012` (engine.core) and `MOD-015` (harness.headless), both Sprint 1 deliverables. The 10M-citizen synthetic in particular exercises the citizen store design that only lands for real in Sprint 3 (`engine.citizens`) — at Sprint 2 build time, H-SYNTH's 10M preset necessarily runs against Sprint 1's stub/skeleton citizen model, not the real Option B store. The BA flags this as expected (M0-ENG §2.4's whole point is knowing the cost "in month 3 of development" against whatever exists then) but the owning BA should confirm at dispatch that AC-3/AC-9's 10M preset is meaningful against the Sprint-1 skeleton, or scope it down to 1M until Sprint 3 lands citizens for real.
- **For Bill.** M0-ENG §6.5 is cited in the BOW item's `specRef` but no header numbered "6.5" exists in the master doc — the content is drawn from working-agreement point 5 (line 998) instead. Flagging the spec_ref/document mismatch for the freeze-review record, consistent with the same class of discrepancy noted in `int.protocol.md`'s header.
