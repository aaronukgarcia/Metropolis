BOW code: MOD-014

# Acceptance criteria — ui.harness (MOD-014)

**BOW code:** MOD-014
**Spec refs:** UI-SPEC §5 (`docs/METROPOLIS-MASTER-v2.1.md` lines 765-777: performance budget table + "Headless UI tests drive the widget layer with scripted key sequences against recorded delta streams and assert cell-buffer snapshots — the UI gets the same regression rigour as the sim."); M0-ENG §5 working agreement point 5 (line 998: "UI budgets (UI-SPEC §5) are asserted in the headless UI harness"); UI-SPEC §1 (`docs/METROPOLIS-MASTER-v2.1.md` lines 722-728: retained cell-buffer renderer, diff flushing, decoupled input/render loops); code.json `ui.harness` entry (consumes `ui.core` MOD-009 and `harness.replay` MOD-013).
**Date:** 2026-08-08
**Status:** draft-ahead
**Package under test:** `internal/harness/uitest/` (path from `node claude-bow.js show MOD-014`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/harness/uitest/...`.

## User stories

- **US-1.** As the UI test harness, I need to drive the widget layer with scripted key sequences (not a real keyboard) so that every screen can be regression-tested headlessly in CI (UI-SPEC §5).
- **US-2.** As the UI test harness, I need to feed recorded delta streams from H-REPLAY into the widget layer so that snapshot assertions run against stable, reproducible data rather than a live/nondeterministic engine (M0-ENG §2.2/§5).
- **US-3.** As CI, I need every UI-SPEC §5 latency budget (keystroke echo <10ms, screen switch <30ms, diff flush <3ms typical/<8ms worst, pane focus <5ms, map pan <8ms, delta apply <15ms) asserted automatically per commit, so that a regression is caught before Bill's review, not after (M0-ENG §5).
- **US-4.** As a future junior building an F-screen, I need cell-buffer snapshot assertions so that a screen's rendered output is provably unchanged (or intentionally changed) across a refactor, the same regression rigour the sim gets from the determinism gate (UI-SPEC §5).

## Scope

Headless driving of `ui.core`'s widget layer via scripted key sequences and `harness.replay` fixtures, cell-buffer snapshot assertions, and CI-enforced UI-SPEC §5 latency budgets.

## Acceptance criteria

### Functional

- **AC-1.** A `Harness` (or equivalent) exists that can construct the widget layer (`ui.core`) headlessly — without a real terminal — and inject synthetic key events programmatically. Check: `go doc ./internal/harness/uitest Harness` shows a constructor and a method accepting key events (e.g. `SendKeys(seq string)` or `[]tcell.EventKey`).
- **AC-2.** Scripted key sequences can be expressed as a compact string/DSL (e.g. `"b r s"` for the build-road-street leader sequence, per UI-SPEC §3) rather than requiring hand-built event structs per keystroke. Check: `go doc ./internal/harness/uitest` shows a script-parsing function/type, and a passing test exercises a multi-key leader sequence (`grep -rn "func Test.*[Ss]equence\|func Test.*[Ll]eader" internal/harness/uitest/*_test.go`).
- **AC-3.** The harness can attach a `harness.replay` fixture as the delta source feeding the widget layer's view models, in place of a live `Transport`. Check: `grep -n "replay\.\(Player\|UIPlayer\)" internal/harness/uitest/*.go` matches.
- **AC-4.** Cell-buffer snapshot assertions exist: given a scripted sequence + a fixture, the harness captures the resulting cell buffer (styled runes) and compares it against a stored golden snapshot, reporting a cell-level diff on mismatch. Check: `go doc ./internal/harness/uitest AssertSnapshot` (or equivalent) exists; a passing test asserts a deliberately mutated buffer produces a non-empty diff (`grep -rn "func Test.*[Ss]napshot" internal/harness/uitest/*_test.go`).
- **AC-5.** Golden snapshots are stored as files under the package (e.g. `testdata/snapshots/*.golden`) and are human-diffable (plain text render of the cell buffer, not a binary blob), so a reviewer can read a snapshot diff in a PR. Check: `Get-ChildItem internal/harness/uitest/testdata/snapshots` (or equivalent) is non-empty and files are text.
- **AC-6.** A `-update` (or equivalent) flag/mode regenerates golden snapshots deliberately, distinct from a normal test run so goldens can't drift silently. Check: `grep -n "update.*snapshot\|UPDATE_GOLDEN" internal/harness/uitest/*.go` (or `*_test.go`) matches.

### Error handling

- **AC-7 (GR#7).** Sending a key sequence that decodes to no registered action (an unmapped or malformed script token) returns a typed/registry-sourced error rather than silently no-op'ing, so a typo'd test script fails the test instead of passing vacuously. Check: `grep -n "MET-" internal/harness/uitest/*.go` finds a registry code reference; a passing test covers this path (`grep -rn "func Test.*[Uu]nmapped\|func Test.*[Ii]nvalid" internal/harness/uitest/*_test.go`).
- **AC-8.** A missing or unreadable golden snapshot file produces a clear "no golden exists — run with -update to create" failure, distinguishable from a mismatch diff. Check: passing test coverage (`grep -rn "func Test.*[Mm]issing.*[Gg]olden\|func Test.*[Nn]o.*[Gg]olden" internal/harness/uitest/*_test.go`).

### Determinism & safety

- **AC-9 (GR#21).** Running the same scripted sequence against the same fixture twice produces byte-identical cell-buffer output both times (no nondeterministic rendering — e.g. no time-based animation cell touched by a snapshot test). Check: a passing test runs the same script twice and asserts identical captured buffers (`grep -rn "func Test.*[Dd]eterminis" internal/harness/uitest/*_test.go`).
- **AC-10 (UI-SPEC §5; M0-ENG §5).** CI-runnable latency assertions exist for at least: keystroke→echo (<10ms), screen switch (<30ms), full-terminal diff flush (<3ms typical, <8ms worst on resize). Check: `go test ./internal/harness/uitest/... -race -count=1 -v` shows tests named for each budget (`grep -rn "func Test.*[Ll]atency\|func Test.*[Bb]udget" internal/harness/uitest/*_test.go` finds at least 3 distinct budget tests), and each asserts against the numeric threshold from UI-SPEC §5 (not an arbitrary looser number).
- **AC-11 (M0-ENG §5, GR#21 "perf is a test, not a hope").** A CI job (or documented `go test` target) fails the build when a latency budget from AC-10 is exceeded — the assertion is a hard test failure, not a logged warning. Check: the test functions in AC-10 use `t.Fatalf`/`t.Errorf` on budget breach, not `t.Log`.
- **AC-12.** `go test ./internal/harness/uitest/... -race -count=1` passes with no data race — the harness's key-injection and delta-consumption paths run on separate goroutines mirroring the real T-INPUT/T-VIEWS split (UI-SPEC §1) and must be race-clean under that split. Check: `grep -n "go func()" internal/harness/uitest/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-13.** `internal/harness/uitest/doc.go` states the module key `ui.harness`, cites UI-SPEC §5, and lists the specific latency budgets it asserts (with their numeric thresholds transcribed, not just "see spec"). Check: `grep -n "ui.harness" internal/harness/uitest/doc.go` and `grep -n "10ms\|10 ms\|<10" internal/harness/uitest/doc.go` both match.
- **AC-14.** A short README or doc-comment explains how to add a new screen's snapshot test (script + golden regeneration workflow), so juniors building F-screens in later sprints have a template. Check: file exists and references the `-update` mechanism from AC-6.

## Out of scope

- The widget layer itself (`ui.core`, `MOD-009`) and individual F-screens (`ui.screen.*`) — this item only drives and asserts against them.
- Real terminal capability probing / conhost degraded-profile testing — that is exercised manually or in `ui.core`'s own tests, not headlessly here.
- Mouse-event scripting — UI-SPEC §3 makes mouse strictly optional with a key-path equivalent for everything; this harness scripts keys only.
- Recording new fixtures — that is `harness.replay`'s (MOD-013) job; this item only consumes fixtures.

## Escalations

- **Assumption flagged (per BA instructions §3).** This item depends on `MOD-009` (ui.core, Sprint 1) and `MOD-013` (harness.replay, this sprint, seq 200 before ui.harness's seq 210) both existing by dispatch. If ui.core's cell-buffer API shape (what exactly constitutes a "capturable" buffer for AC-4/AC-5) differs from what UI-SPEC §1 describes, the owning BA must refresh AC-4/AC-5 at dispatch time.
- **For Bill.** UI-SPEC §5's budget table gives "typical"/"worst" bands for diff flush but single numbers for the others; AC-10/AC-11 treat every budget as a hard CI gate at the stated number. If any budget should instead be a statistical/percentile gate (e.g. p99 rather than every single sample), that is a spec clarification for Bill, not a BA judgment call.
