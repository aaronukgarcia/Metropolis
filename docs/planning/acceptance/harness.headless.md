BOW code: MOD-015

# Acceptance criteria — harness.headless (MOD-015)

**BOW code:** MOD-015
**Spec refs:** M0-ENG §2.3 (Harness strategy — H-HEADLESS, `docs/METROPOLIS-MASTER-v2.1.md` line 850); §16 Roadmap point 3 (M2 balance harness, line 274); `engine.core` (MOD-012, the orchestrator this wraps).
**Date:** 2026-08-08
**Status:** done (closed 2026-08-10 via PR #1, 77a59f4)
**Package under test:** `internal/harness/headless/` and its `cmd/` entry point (confirm via `node claude-bow.js show MOD-015` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/harness/headless/...`.

## User stories

- As **the balance/Batch/CI workhorse** (M2, `MOD-036`), I need `metropolis -headless -seed N -months M -out <bundle-dir>`, so parameter sweeps can run unattended without a terminal UI.
- As **`feat.detgate`** and **`harness.synth`**, I need the headless binary to accept scenario scripts (JSON command lists) and emit per-phase timing + invariant reports every tick, so CI can drive scripted scenarios and assert on structured output rather than screen-scraping a TUI.
- As **a developer debugging a balance issue**, I need headless runs to be scriptable and reproducible from a seed + command log alone, so a reported bug can be replayed exactly.

## Corrections (BUG-033, 2026-08-10)

**ASM-158.** This file's AC-1 showed `-out snap.json` as its worked example, implying a single file, while AC-3 (same file, unchanged in substance) mandates the `int.serializer` bundle format — a **directory** (`header.json` plus `shards/`). The two cannot both be satisfied by one implementation. Bill's ruling (BOW MOD-015 comment, 2026-08-10): the bundle wins — it is what makes a headless run's output independently verifiable by `metctl` and `serialize.ValidateBundle`; a single opaque file leaves a headless run with no checkable artifact, which defeats the point of running headless at all. **AC-1 below is amended to match the already-built, already-reviewed code** (`internal/harness/headless/run.go`'s `Config.OutDir`/`writeBundle`, `cmd/metropolis/headless.go`'s `-out` flag description "bundle directory... must not already exist") rather than to the original example — this is a correction of the criteria to reality, not a new design decision. The user story above and AC-1/AC-3 below are updated accordingly; no other AC in this file assumed a single-file `-out`, so this correction is scoped to those two.

## Standing: an AC's check must be able to fail (liftable BA-template text, from dev-team-process.md v1.9 / BUG-033)

Every acceptance criterion in this file is a pair: a **rule** (what must be true) and a **check** (how a verifier proves it). The two can drift — a check written as a grep, a type sketch, or an example artifact name can be satisfiable by an implementation that reads the rule's prose and still violates its intent. This file's AC-1 (an example filename implying "file" while AC-3 required "directory") is a worked example of exactly that drift, caught only when a developer tried to build both halves at once.

**The standard, binding on this BA and every BA:**

- **A check must be capable of FAILING an implementation that satisfies the AC's prose but violates its rule.** Before writing a check, describe the laziest plausible implementation that technically reads as compliant, and confirm the check rejects it. If it wouldn't, the check is documentation, not verification.
- **Where a check is a grep**, state what a false pass looks like — a string existing is not the same as the string meaning what the rule needs (e.g. `grep -rn "time.Now"` finding a match that is real but not actually confined to the progress-reporting path the rule requires).
- **Where a check is a type/signature sketch**, say explicitly which part is binding (the information the type must be able to carry) and which is illustrative (field names, exact method signature, "e.g." on the shape itself).
- **Where a check names an artifact, name its shape** (file vs. directory, format, what makes it valid), **not an example filename** — `snap.json` implied "file" by accident, a reader built to the accident, and it cost a bounce. This file's AC-1/AC-3 now name the shape explicitly (`header.json` + `shards/`, validated via `serialize.ValidateBundle`) rather than an example path.
- **An AC with no "Check:" sentence at all is worse than a weak one** — it cannot be falsified by construction. AC-11/AC-12/AC-13 in this file had this gap; it is closed below as part of this same pass.

This section may be lifted verbatim (with the worked-example paragraph above trimmed or swapped for a new file's own instance) into the BA acceptance-criteria template once one exists.

## Scope

A CLI (`metropolis -headless ...`) and library wrapping `engine.core`'s orchestrator with no UI attached: seed/months/out flags, scenario-script command-list input, per-phase timing emission, and invariant reports every tick.

## Acceptance criteria

### Functional

- **AC-1 (amended per ASM-158 — see "Corrections" below).** `metropolis -headless -seed N -months M -out <dir>` runs to completion and writes a save-bundle **directory** at the given `-out` path: `-out` names a directory that does not already exist (created fresh, never merged into) and, on success, contains `<dir>/header.json` plus a non-empty `<dir>/shards/`, per `int.serializer`'s bundle format — see AC-3. `-out` never denotes a single file; there is no single-file output mode. Check: `go run ./cmd/metropolis -headless -seed 1 -months 1 -out <tmpdir>` (where `<tmpdir>` does not yet exist) exits 0, and `<tmpdir>/header.json` exists, `<tmpdir>/shards/` exists and contains at least one shard file, and `serialize.ValidateBundle(<tmpdir>)` returns a nil error. A check that only asserts "the output path exists and is non-empty" is insufficient and is the exact defect being corrected here — it would equally pass a single opaque JSON blob at `<tmpdir>`, which is not a bundle and is not what AC-3 requires.
- **AC-2.** `-seed` and `-months` are required/validated flags: omitting either produces a clear usage error (non-zero exit, message naming the missing flag), not a panic or a silent default.
- **AC-3.** The `-out` directory is written via `int.serializer`'s `StateSerializer`/bundle format (INT-002, "the save format IS the fixture format") — not a bespoke ad-hoc JSON dump — so headless output is itself a valid fixture readable by `metctl verify`. Check: after a successful headless run, `serialize.ValidateBundle(<out-dir>)` returns a nil error (rehashes every shard listed in `header.json`'s `ShardIndex` against its recorded SHA256/ByteSize) and `metctl verify <out-dir>` (or the package's equivalent verification entry point) exits 0 against the same directory. A check that merely confirms the `-out` path parses as JSON or has a plausible extension would pass a hand-rolled single-file JSON dump that happens to be well-formed — exactly not the bundle format this AC requires, and exactly the failure mode AC-1's prior wording invited.
- **AC-4.** Scenario scripts (JSON command lists) are accepted via a flag (e.g. `-scenario path.json`) and executed as `protocol.Command`s in file order before/interleaved with tick advancement as the scenario specifies. A passing test feeds a small scenario script and asserts the resulting world reflects the scripted commands (e.g. a `BuyLand`-equivalent command changes ownership state, verified in the output snapshot).
- **AC-5.** Per-phase timing is emitted every tick (e.g. to stdout as structured JSON, or to a log file) — check: running headless with a timing-output flag produces output containing timing data for each of `engine.core`'s fixed phases (production, logistics settlement, consumption & shortfall, population, land value & decay, finance).
- **AC-6.** Invariant reports are emitted every tick — at minimum a placeholder/stub invariant check runs and reports pass/fail per tick (the real invariant checker is `MOD-019`, a separate Sprint-3 item; this item only needs the reporting hook and a wired-in stub check, consistent with M0-ENG §2's "module stubbing" discipline).
- **AC-7.** Running the same `-seed`/`-months`/scenario twice produces byte-identical `-out` snapshots (carrying forward `int.serializer`'s byte-determinism AC and `engine.core`'s determinism guarantees) — a passing test runs headless twice into two temp files and asserts identical `sha256`.

### Error handling

- **AC-8 (GR#7).** An unreadable/malformed `-scenario` file (missing file, non-JSON-array content, an element that fails `protocol.DecodeCommand`, or an element that fails `Command.Validate()`) is reported as `ErrScenarioReadFailed` (`MET-H200`) and a non-zero exit — never a panic, and never a partial command list: `LoadScenario` returns either every command in the script or none, so no engine command is issued from a scenario that failed partway through. Check: `grep -n "ErrScenarioReadFailed" internal/harness/headless/errors.go` shows the `MET-H200` code; a passing test (`grep -rn "func Test.*[Ss]cenario" internal/harness/headless/*_test.go`) feeds each malformed-input shape and asserts the returned error's registry code is `MET-H200` AND that no commands were sent to the engine (e.g. the engine's tick/command count is unchanged from before the failed load) — not merely that a matching-named test function exists and exits non-zero.
- **AC-9 (GR#7).** A write failure on `-out` (bundle directory creation, shard writer open, or header write failing — e.g. an unwritable parent directory) is reported as `ErrOutputWriteFailed` (`MET-H201`) and a non-zero exit, and the run never reports success with a snapshot silently missing: no `header.json` is left behind claiming a complete bundle. Check: `grep -n "ErrOutputWriteFailed" internal/harness/headless/errors.go` shows the `MET-H201` code; a passing test (`grep -rn "func Test.*[Ww]rite\|func Test.*[Oo]utput" internal/harness/headless/*_test.go`) triggers an `-out` write failure and asserts both the returned error's registry code is `MET-H201` AND that no complete/valid `header.json` exists at the target path afterward — not merely that a matching-named test function exists and passes.

### Determinism & safety

- **AC-10 (GR#21; check tightened on BA-7 re-read — see "Corrections").** `grep -rn "time.Now\|time.Since" internal/harness/headless/*.go` (excluding `_test.go`) may only match inside the progress-reporting code path (currently `report.go`'s `reportWriter`), and every match site must carry a doc comment stating what the value feeds and that it never reaches the `-out` bundle. A bare grep count with no confinement check is not sufficient on its own — a match sitting anywhere else (e.g. seeding, scenario timing used as a tie-breaker, a value copied into `Result`) would satisfy "a match exists and could plausibly be progress reporting" without actually being confined to it. Binding check: (1) every match's enclosing function is named/documented as progress-reporting-only; (2) AC-7's determinism test (two runs, identical `-out` sha256) already proves no wall-clock value reaches the bundle — cite that test's name here as the check that actually falsifies a violation, since the grep alone only locates candidates, it does not confine them.
- **AC-11 (GR#21; check added on BA-7 re-read — see "Corrections").** No `range` over a Go map produces ordering-sensitive CLI/report output (e.g. flag parsing summaries, timing reports) that would make two runs' human-readable logs diverge in ways that could mask a real nondeterminism regression — timing/invariant report fields are emitted in a fixed, documented order. This AC previously named no check at all, which made it unfalsifiable by construction. Check: `grep -n "range " internal/harness/headless/report.go internal/harness/headless/run.go` (excluding `_test.go`) — any match ranging over a `map[...]...`-typed value whose iteration result reaches `Report`/stdout output is a violation; additionally, a passing test runs the report-emitting path twice against identical input and asserts the two output streams are byte-identical (not just "both contain the same set of lines" — set-equality would pass a shuffled-but-complete map iteration, which is exactly the failure this AC exists to catch).

### Documentation

- **AC-12 (check added on BA-7 re-read — see "Corrections").** `-headless -h`/`--help` output documents every flag (`-seed`, `-months`, `-out`, `-scenario`, and any timing/invariant-report flags) with a one-line description each. This AC previously named no check. Check: `go run ./cmd/metropolis -headless -h` (or `--help`) exits with help output, and that output, run through `grep -c` for each of `-seed`, `-months`, `-out`, `-scenario`, matches once per flag with non-empty trailing description text on the same or next line — a check that only confirms the help text is non-empty would pass output that documents three flags and omits the fourth.
- **AC-13 (check added on BA-7 re-read — see "Corrections").** The package doc states module key `harness.headless`, cites M0-ENG §2.3, and describes its role as "the balance/Batch/CI workhorse" verbatim from spec so future readers connect it to M2's balance harness. This AC previously named no check. Check: `grep -n "harness.headless" internal/harness/headless/doc.go`, `grep -n "M0-ENG" internal/harness/headless/doc.go` (or the literal section symbol), and `grep -n "balance/Batch/CI workhorse" internal/harness/headless/doc.go` all match — three separate greps rather than one, because a doc comment naming the module key without the spec citation (or vice versa) would satisfy a single combined "mentions this stuff somewhere" check while still leaving a future reader unable to trace the module back to spec.

## Out of scope

- The real invariant checker's actual conservation assertions (`MOD-019`) — this item wires the reporting hook and a stub check only.
- `harness.synth`'s synthetic world generation (`MOD-016`) — a separate item that will consume this harness's timing-report format, not build it.
- Azure Batch integration for running headless at scale — that is `MOD-069`/cloud path, unscheduled.

## Escalations

- None at draft time. `status: draft-ahead` — depends on `engine.core` (`MOD-012`); refresh AC-1/AC-3/AC-5 against `engine.core`'s actual exported orchestrator API once it lands, and against `int.serializer`'s finalized bundle-write API (already frozen in Sprint 0, low risk of drift).

- **ASM-850 (CC fold).** MOD-015 dedup: harness.headless.md and engine.headless.md both open with BOW code MOD-015. This split was ruled intentional in MOD-015 comments (ASM-119/ASM-120, Bill 2026-08-10), but neither file header cross-references the other, so a future dispatch-guard/codejson-audit pass may flag a duplicate-criteria/file-ownership overlap. Recommend a one-line cross-reference in each header.

- **ASM-860 (SF fold).** BOW MOD-015 Desc still shows the CLI example `-out snap.json` (single file), which ASM-158 (Bill 2026-08-10) ruled wrong: `-out` is a bundle DIRECTORY (header.json plus shards/). The acceptance file AC-1 was amended, but the BOW item description was never corrected to match; the BOW Desc needs updating to the bundle-directory form.
