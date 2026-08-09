BOW code: FEAT-004

# Acceptance criteria — feat.detgate (FEAT-004)

**BOW code:** FEAT-004
**Spec refs:** §1.2.5 (Deterministic parallelism / CI determinism gate, `docs/METROPOLIS-MASTER-v2.1.md` line 826, "CI runs the determinism gate on every merge"); A8 (Mechanical enforcement, line 1368); M0-ENG §6.4 (working agreement, determinism-gate-first TDD order); GR#21 (Red Determinism Gate Stops the Line, `CLAUDE.md` line 52).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1)
**Package under test:** `internal/engine/core/` (tests) + CI config (confirm exact path via `node claude-bow.js show FEAT-004` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/engine/core/...` plus the CI workflow file(s) this item adds/modifies.

## User stories

- As **the project's determinism doctrine** (§1.2, A8), I need a mechanical CI check that same-seed-same-commands produces a bit-identical world regardless of worker count, so that "same seed ⇒ same city" is a proven property, not an assumption anyone can quietly break.
- As **`engine.core`**, I need this gate written and passing **on the stub engine, before any real simulation logic exists** (M0-ENG §6.4's explicit TDD order), so the gate defines the determinism contract rather than inheriting the code's assumptions.
- As **Bill** (the lead, under GR#21), I need any red determinism-gate run to be mechanically flagged auto-P0, so a nondeterministic merge can never poison every fixture recorded after it.

## Scope

The determinism CI gate itself: same seed, 120 simulated months, run twice — `sha256(worldSnapshot)` must match; then again comparing `POOL-SIM=1` vs `POOL-SIM=14`. Built and green against `harness.stub`'s `StubEngine` + `engine.core`'s orchestrator, before any real model lands.

## Acceptance criteria

### Functional

- **AC-1.** A test (Go test or a dedicated CLI/CI script) exists that: (a) constructs a world with a fixed seed, (b) advances it 120 simulated months via `engine.core`'s orchestrator, (c) computes `sha256` of the resulting world snapshot, (d) repeats (a)-(c) with the identical seed and command log, (e) asserts the two hashes are equal. Check: `go test ./internal/engine/core/... -run TestDeterminismGate -race -count=1 -v` (or the actual name) passes.
- **AC-2.** The same test (or a sibling) repeats the run at `POOL-SIM=1` and at `POOL-SIM=14` (or the configured worker-count knob's equivalent) with the identical seed/commands and asserts the two `sha256` hashes are equal to each other and to AC-1's baseline hash.
- **AC-3.** The gate runs against `harness.stub`'s `StubEngine`-driven orchestration (not a real simulation model) — since no real model should exist yet when this gate first lands, per M0-ENG §6.4's explicit ordering ("determinism gate is built and passing on the stub engine BEFORE any simulation logic").
- **AC-4.** The gate is wired into CI so it runs **on every merge** (§1.2 point 5) — check: the CI workflow file (e.g. `.github/workflows/*.yml` or equivalent) includes a step invoking this test/script, not just a local-only script that nobody runs automatically.
- **AC-5.** A worldSnapshot hashing function is well-defined and documented: it hashes the full committed world state after 120 months (not a partial/sampled view), using a canonical serialization (reuse `int.serializer`'s byte-deterministic NDJSON path, per that item's own AC-5, rather than inventing a second ad-hoc encoding).
- **AC-6.** 120 months completes "in seconds" per the broader M2 target framing (§16 Roadmap point 2, "runs 300 game-years in seconds") — at minimum, the gate must complete within CI's timeout budget on the stub engine; an explicit CI timeout value is documented (not left at a default that silently passes/fails for unrelated reasons).

### Error handling

- **AC-7.** A hash mismatch fails the CI job with a non-zero exit code and a clear message identifying which two runs disagreed (e.g. "seed X, 120mo, run1 vs run2 mismatch" or "POOL-SIM=1 vs POOL-SIM=14 mismatch") — not a silent pass-through or an ambiguous generic test failure.
- **AC-8.** Any determinism-gate failure is documented (in the gate's own output or a linked doc) as **auto-P0** per GR#21 — check: the CI failure message or accompanying runbook text explicitly states this severity, so a human triaging red CI does not have to know the rule from memory.

### Determinism & safety

- **AC-9 (GR#21).** The gate itself introduces no nondeterminism: it does not read wall-clock time to seed anything, does not depend on filesystem iteration order, and does not depend on Go map iteration order anywhere in its own hashing/comparison code — `grep -rn "time.Now" <gate test file>` returns no matches, and a manual scan finds no map-range loops feeding the hash input.
- **AC-10 (GR#21).** The gate is the literal reference implementation other later modules' shard-count-invariance tests (per M0-ENG §6's "Definition of Done: determinism-relevant modules also add a shard-count invariance test") are expected to follow — its structure (fixed seed, two runs, hash compare, worker-count variant) is documented clearly enough to be copied, not bespoke per module.

### Documentation

- **AC-11.** The gate's doc comment/README cites §1.2.5, A8, and M0-ENG §6.4, and states plainly it must exist and pass **before** any real simulation logic is merged (the TDD-order requirement, so a future contributor doesn't accidentally think it's optional tooling added after the fact).

## Out of scope

- Real-model determinism (citizens, traffic, etc.) — those modules add their *own* shard-count-invariance tests when they land (Sprint 3+); this item only proves the gate mechanism works on the stub.
- Escape-analysis (`-gcflags="-m"`) and `gctrace` perf gates — related A8 mechanical-enforcement items but distinct CI checks, not part of this determinism-hash gate.
- Camera-invariance (A7) testing — a separate invariant (`MOD-018`/`engine.citizens`, Sprint 3), not part of this gate.

## Escalations

- None at draft time. `status: draft-ahead` — this item depends on `engine.core` (`MOD-012`) and `harness.stub` (`MOD-008`) both landing first; refresh this file's AC-1/AC-3 against their actual exported hooks (snapshot-hash function name, `StubEngine` construction API) once those items are built, rather than the illustrative names used here.
