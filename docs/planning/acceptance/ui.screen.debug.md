BOW code: FEAT-007

# Acceptance criteria — ui.screen.debug (FEAT-007)

**BOW code:** FEAT-007
**Spec refs:** M0-ENG §3 (Debug mode & the Info Panel, `docs/METROPOLIS-MASTER-v2.1.md` lines 853-865); `foundation.errors` (MOD-002, `Recent()`/error-tail source — Sprint 0, in_progress); `ui.widgets` (MOD-010, the widget set this screen assembles); module registry (`MOD-005`).
**Date:** 2026-08-08
**Status:** active (Sprint 1) — refreshed against landed APIs 2026-08-09 by BA-1, see Escalations
**Package under test:** `internal/ui/screens/debug/` (confirm via `node claude-bow.js show FEAT-007` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/debug/...`.

## User stories

- As **a developer**, I need F12 to show build/runtime stats (version, commit, uptime, memory vs budget, GC pauses, goroutine count, channel depths, input-echo p99) whenever debug is on, so I can diagnose problems without leaving the game.
- As **`feat.skeleton`**'s Sprint-1 exit gate, I need F12's module registry view to show every module's name, status (real/stub/off), and health, so "F12 shows every module as stub with health OK" is checkable by looking at the actual screen.
- As **a developer**, I need F12's error tail to read `foundation.errors`' `Recent()` and pretty-print the last 50 warn/error entries live, so I never have to `tail -f` a log file by hand.
- As **a developer with `CanToggle`-safe modules**, I need guarded ON/OFF/STUB toggles in the registry view, so I can flip a module to stub mid-session to isolate a suspected regression.

## Scope

The F12 Info Panel: build/code info, runtime stats vs the §1.3 memory budget table, module registry rows with guarded toggles, last-50 error tail from `foundation.errors.Recent()`, per-phase µs sparkline (60-tick window), and a read-only BoW tab.

## Acceptance criteria

### Functional

- **AC-1.** F12 renders build/code info sourced from `-ldflags`-injected values (version via `git describe --tags --dirty`, commit hash, branch, build UTC timestamp, Go version, build host) — **never hand-maintained** (M0-ENG §3 explicit). A passing test injects known ldflags-equivalent values and asserts they render verbatim, and a source scan confirms no hardcoded version/commit string literal exists in the package (GR#15).
- **AC-2.** F12 renders runtime stats: uptime, sim date vs real elapsed, speed, tick number, memory (heap in-use, sys, GC pause p99, arena occupancy) against the §1.3 budget table, goroutine count, channel depths (input/delta/persist queues), and input-echo latency p99 — a passing test with mocked/injected runtime metrics asserts each field renders and, for the memory fields, is displayed against its named budget (not a bare number with no context).
- **AC-3.** F12's module registry view shows one row per registered module, matching `registry.ModuleEntry`'s fields (`internal/foundation/registry/registry.go`): `Key` (name), `Semver`, `Status` (`StatusReal`/`StatusStub`/`StatusOff`), `Health` (`HealthOK`/`HealthDegraded`/`HealthError`), `LastTickCostMicros`, `FlagSource` — sourced from `Registry.List()` (sorted, deterministic order per its own AC-10) — a passing test against a registry containing N modules (some stub, some real, one degraded) asserts N rows render with correct per-field values.
- **AC-4.** Registry rows offer REAL/OFF/STUB toggles **only** where the entry's `CanToggle` field is true, calling `Registry.SetStatus(key, target, confirmToken)` — which requires `confirmToken == key` (the F12 UI's confirm step must have the user re-enter/re-confirm the exact module key, not just click a generic "yes") before the status actually changes — a passing test asserts a non-`CanToggle` module's row has no active toggle control, and a `CanToggle` module's toggle requires the confirm interaction (with the correct key value) before `SetStatus` succeeds and the registry's status actually changes.
- **AC-5.** Toggling a module emits a world-event visible in debug (ticker-equivalent, e.g. "Crime module → STUB") — a passing test asserts the toggle action produces the documented event/log entry.
- **AC-6.** The error tail reads `errs.Recent() []Entry` (`internal/foundation/errs/log.go` — confirmed as the real, already-landed public retrieval function name and signature; no drift from the draft name) and pretty-prints the last 50 warn/error entries live, updating as new entries arrive — a passing test feeds >50 entries into the `errs` package's sink and asserts F12's tail view shows the last 50 in order (note: `Recent()` itself returns up to the ring buffer's full 200-entry capacity, oldest-first; F12 is responsible for taking the last 50 of that slice), matching `foundation.errors`' own AC-8 contract.
- **AC-7.** `Enter` on a tail entry opens the full log (per M0-ENG §3) — a passing test asserts selecting an entry surfaces its complete `Entry` fields (`ts, level, code, correlationId, module, msg, ctx`), not just the truncated tail-line summary.
- **AC-8.** A per-phase µs sparkline renders across the last 60 ticks, one sparkline per `engine.core` phase, in `engine/core.MonthlyPhaseOrder`'s fixed order (`internal/engine/core/phase.go`, confirmed unchanged from draft): production, logistics-settlement, consumption-shortfall, population, land-value-decay, finance — reusing `ui.widgets`' `Sparkline(buf *core.Buffer, rect core.Rect, series []float64, style tcell.Style)` function (`internal/ui/widgets/sparkline.go`, AC-3 of that item's acceptance doc — confirmed correct citation), not a bespoke reimplementation. Per-phase timing is sourced from `registry.Registry.TickCostHistory(key)` (the same registry AC-3 reads), which already retains the last 60 `RecordTickCost` samples per module — F12 does not need its own ring buffer for this. A passing test feeds 60 ticks of known per-phase timing and asserts the sparkline reflects it.
- **AC-9.** A read-only BoW tab shows open-item counts by priority and what's `in_progress` — sourced from a read-only query against the metro BOW (or a mocked equivalent for the test), matching the checkin startup summary's shape described in `CLAUDE.md`.
- **AC-10.** F12 is visible **only** when debug is on (a runtime feature switch, `feat.debugmode`) — this screen itself does not decide whether debug is on; it reads the switch's current state and renders/hides accordingly. A passing test toggles the injected debug-state flag and asserts F12's visibility follows it.

### Error handling

- **AC-11.** If the module registry or `errs.Recent()` is unavailable at render time (e.g. not yet booted), F12 shows a clear "unavailable" state for that pane rather than a blank panel or a panic.
- **AC-12.** A toggle action that fails (module rejects the toggle, or the confirm is cancelled) leaves the registry state unchanged and surfaces why, rather than silently no-op'ing with no feedback.

### Determinism & safety

- **AC-13.** F12's own rendering is a pure function of the (registry snapshot, error-tail snapshot, runtime-metrics snapshot, phase-timing snapshot) inputs at a given tick — the same snapshot set renders identically across repeated calls. `grep -rn "time.Now" internal/ui/screens/debug/*.go` (excluding `_test.go`) — any match must be confined to *displaying* a wall-clock-derived uptime/elapsed value (which is legitimately wall-clock, since it's real elapsed time, not sim time) and must not affect any other rendered field's content.
- **AC-14.** `go test ./internal/ui/screens/debug/... -race -count=1` passes with no data race between the live-updating error-tail/registry poll and the render path.

### Documentation

- **AC-15.** The package doc states module key `ui.screen.debug`, cites M0-ENG §3, and documents which upstream packages each pane reads from (`foundation.errors.Recent()`, the module registry, `engine.core`'s phase timing) so the dependency chain is traceable.

## Out of scope

- The module registry's own implementation (`MOD-005`) — this screen is a *view* of it, per M0-ENG §3's own framing ("the panel is a view of it, not a parallel system. One registry, two consumers"), not a reimplementation.
- `metctl errors` offline review — a separate CLI tool (`int.serializer`/tooling), not part of this live-panel item.
- Cheats, 8× speed, console, fixture record/replay controls unlocked by debug mode — those are `feat.debugmode`'s (`FEAT-008`) behavioural unlocks; F12 only needs to *render* the panel, not implement what debug mode unlocks elsewhere.

## Escalations

- **2026-08-09, BA-1, dispatch refresh (no escalation needed — all references confirmed against landed code):**
  - `node claude-bow.js show FEAT-007` confirms `path: internal/ui/screens/debug/`, matching this doc's "Package under test" — no drift.
  - AC-6: `errs.Recent() []Entry` (`internal/foundation/errs/log.go:240`) is the real, already-correctly-named function — no change to the name, tightened the wording to drop the "or equivalent" hedge and note it returns up to 200 entries so F12 must itself take the last 50.
  - AC-3/AC-4: registry API confirmed against `internal/foundation/registry/registry.go`. Found and fixed one real drift: AC-4 said toggles are "ON/OFF/STUB" but the registry's actual `Status` enum is `StatusReal`/`StatusStub`/`StatusOff` (no "ON") — reworded to "REAL/OFF/STUB" and named the real guard mechanism (`Registry.SetStatus(key, target, confirmToken)`, which requires `confirmToken == key`, not a generic confirm flag).
  - AC-8: `engine.core`'s `MonthlyPhaseOrder` (`internal/engine/core/phase.go`) is production → logistics-settlement → consumption-shortfall → population → land-value-decay → finance — **unchanged from the draft's order**, no fix needed there. Named the real sparkline function (`widgets.Sparkline`, `internal/ui/widgets/sparkline.go`) and its real per-phase-cost source (`Registry.TickCostHistory`, which already retains the last-60 samples — F12 shouldn't duplicate that ring buffer).
  - AC-2: the §1.3 memory budget table figures (8 GB citizen shards, 4 GB world cells/networks/route cache, 1.5 GB firms/markets/logistics, 2 GB scratch arenas, 0.15 GB UI, 2 GB snapshot COW, ~2 GB OS/slack) are spec-sourced (`docs/METROPOLIS-MASTER-v2.1.md` §1.3, lines 828-838), not invented — GR#15 satisfied as drafted, no change needed.
  - Net: no criterion required softening or escalation. All three upstream modules (MOD-002, MOD-005, MOD-010) provide everything AC-1/AC-3/AC-4/AC-6/AC-8 need.
- **ASM-093 (confirm-and-close, FEAT-084).** SEC-020 copy-guard policy for `debug.Screen`/`map.MapScreen`: every exported method that reads/writes a receiver field is `checkNotCopied`-guarded (confirmed live: `Collect`, `LastToggleError`, `recordToggleFailure` etc. in `internal/ui/screens/debug/screen.go`); `TailEntry` (line 408) is the sole deliberate exception, explicitly marked at line 399 ("deliberately NOT checkNotCopied-guarded") because it touches zero receiver fields.
