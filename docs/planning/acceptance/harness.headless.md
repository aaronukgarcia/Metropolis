BOW code: MOD-015

# Acceptance criteria — harness.headless (MOD-015)

**BOW code:** MOD-015
**Spec refs:** M0-ENG §2.3 (Harness strategy — H-HEADLESS, `docs/METROPOLIS-MASTER-v2.1.md` line 848); §16 Roadmap point 3 (M2 balance harness, line 272); `engine.core` (MOD-012, the orchestrator this wraps).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1)
**Package under test:** `internal/harness/headless/` and its `cmd/` entry point (confirm via `node claude-bow.js show MOD-015` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/harness/headless/...`.

## User stories

- As **the balance/Batch/CI workhorse** (M2, `MOD-036`), I need `metropolis -headless -seed N -months M -out snap.json`, so parameter sweeps can run unattended without a terminal UI.
- As **`feat.detgate`** and **`harness.synth`**, I need the headless binary to accept scenario scripts (JSON command lists) and emit per-phase timing + invariant reports every tick, so CI can drive scripted scenarios and assert on structured output rather than screen-scraping a TUI.
- As **a developer debugging a balance issue**, I need headless runs to be scriptable and reproducible from a seed + command log alone, so a reported bug can be replayed exactly.

## Scope

A CLI (`metropolis -headless ...`) and library wrapping `engine.core`'s orchestrator with no UI attached: seed/months/out flags, scenario-script command-list input, per-phase timing emission, and invariant reports every tick.

## Acceptance criteria

### Functional

- **AC-1.** `metropolis -headless -seed N -months M -out snap.json` runs to completion and writes a snapshot file at the given `-out` path. Check: `go run ./cmd/metropolis -headless -seed 1 -months 1 -out <tmpfile>` exits 0 and the output file exists and is non-empty.
- **AC-2.** `-seed` and `-months` are required/validated flags: omitting either produces a clear usage error (non-zero exit, message naming the missing flag), not a panic or a silent default.
- **AC-3.** The `-out` file is written via `int.serializer`'s `StateSerializer`/bundle format (INT-002, "the save format IS the fixture format") — not a bespoke ad-hoc JSON dump — so headless output is itself a valid fixture readable by `metctl verify`.
- **AC-4.** Scenario scripts (JSON command lists) are accepted via a flag (e.g. `-scenario path.json`) and executed as `protocol.Command`s in file order before/interleaved with tick advancement as the scenario specifies. A passing test feeds a small scenario script and asserts the resulting world reflects the scripted commands (e.g. a `BuyLand`-equivalent command changes ownership state, verified in the output snapshot).
- **AC-5.** Per-phase timing is emitted every tick (e.g. to stdout as structured JSON, or to a log file) — check: running headless with a timing-output flag produces output containing timing data for each of `engine.core`'s fixed phases (production, logistics settlement, consumption & shortfall, population, land value & decay, finance).
- **AC-6.** Invariant reports are emitted every tick — at minimum a placeholder/stub invariant check runs and reports pass/fail per tick (the real invariant checker is `MOD-019`, a separate Sprint-3 item; this item only needs the reporting hook and a wired-in stub check, consistent with M0-ENG §2's "module stubbing" discipline).
- **AC-7.** Running the same `-seed`/`-months`/scenario twice produces byte-identical `-out` snapshots (carrying forward `int.serializer`'s byte-determinism AC and `engine.core`'s determinism guarantees) — a passing test runs headless twice into two temp files and asserts identical `sha256`.

### Error handling

- **AC-8.** An unreadable/malformed `-scenario` file produces a clear, registry-sourced error (`foundation.errors`) and a non-zero exit, not a panic or a partial run.
- **AC-9.** A write failure on `-out` (e.g. unwritable directory) surfaces a clear error and non-zero exit rather than silently succeeding with no file written.

### Determinism & safety

- **AC-10 (GR#21).** `grep -rn "time.Now" internal/harness/headless/*.go` (excluding `_test.go`) — any match must be confined to wall-clock **progress reporting** (e.g. "elapsed real time so far" printed to the operator), never feeding simulation state or the `-out` snapshot's content.
- **AC-11 (GR#21).** No `range` over a Go map produces ordering-sensitive CLI output (e.g. flag parsing summaries, timing reports) that would make two runs' human-readable logs diverge in ways that could mask a real nondeterminism regression — timing/invariant report fields are emitted in a fixed, documented order.

### Documentation

- **AC-12.** `-headless -h`/`--help` output documents every flag (`-seed`, `-months`, `-out`, `-scenario`, and any timing/invariant-report flags) with a one-line description each.
- **AC-13.** The package doc states module key `harness.headless`, cites M0-ENG §2.3, and describes its role as "the balance/Batch/CI workhorse" verbatim from spec so future readers connect it to M2's balance harness.

## Out of scope

- The real invariant checker's actual conservation assertions (`MOD-019`) — this item wires the reporting hook and a stub check only.
- `harness.synth`'s synthetic world generation (`MOD-016`) — a separate item that will consume this harness's timing-report format, not build it.
- Azure Batch integration for running headless at scale — that is `MOD-069`/cloud path, unscheduled.

## Escalations

- None at draft time. `status: draft-ahead` — depends on `engine.core` (`MOD-012`); refresh AC-1/AC-3/AC-5 against `engine.core`'s actual exported orchestrator API once it lands, and against `int.serializer`'s finalized bundle-write API (already frozen in Sprint 0, low risk of drift).
