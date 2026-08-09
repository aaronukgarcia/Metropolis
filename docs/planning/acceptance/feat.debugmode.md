BOW code: FEAT-008

# Acceptance criteria — feat.debugmode (FEAT-008)

**BOW code:** FEAT-008
**Spec refs:** §14 (Debug Mode, `docs/METROPOLIS-MASTER-v2.1.md` lines 257-260); M0-ENG §3 (Debug mode & the Info Panel, lines 853-865, esp. "debug-touched saves are flagged forever"); `int.serializer`'s `Header.DebugTouched`/`TouchDebug`/`MergeDebugTouched` (INT-002, already frozen Sprint 0).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1)
**Package under test:** `internal/engine/debug/` (confirm via `node claude-bow.js show FEAT-008` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/engine/debug/...`.

## User stories

- As **a developer**, I need debug to be a runtime feature switch (`--debug`, config, or `:debug on`) rather than a build flavour, so release builds always carry it (default off) and I never need a separate debug binary.
- As **the save-data hygiene contract** (M0-ENG §3), I need switching debug ON to sticky-flag the save header forever via `int.serializer`'s `Header.DebugTouched`, so debug-touched saves can never silently re-enter clean balance data.
- As **a developer with debug on**, I need 8× speed, cheats (free money, instant build, force milestone), an entity JSON inspector, fidelity-dial exposure, the console, and fixture record/replay controls unlocked, so I can test and demo without waiting on real economy pacing.
- As **`ui.screen.debug`** (F12), I need a single source of truth for "is debug on right now", so the panel's visibility and this module's unlocks never disagree.

## Scope

The runtime debug-mode switch: enable paths (`--debug` flag, config, `:debug on` palette command), the sticky `DebugTouched` save-header write, and the unlock set (8× speed, cheats, entity inspector, fidelity-dial exposure, console, fixture record/replay).

## Acceptance criteria

### Functional

- **AC-1.** A debug-state type/singleton exists exposing `IsOn() bool` (or equivalent) as the single source of truth every other module (`ui.screen.debug`, speed control, cheat commands) reads — a passing test asserts all three enable paths (flag, config, palette command) converge on the same state read by `IsOn()`.
- **AC-2.** Release builds carry debug support and default to **off** — a passing test asserts a freshly constructed debug-state (no flag, no config, no palette command issued) reports `IsOn() == false`.
- **AC-3.** Enabling debug at any point in a session calls `int.serializer`'s `Header.TouchDebug()` (or `MergeDebugTouched(true)`) on the active save header — a passing test enables debug mid-session and asserts the header's `DebugTouched` field is `true` immediately after, and remains `true` across a subsequent disable-then-save (the sticky invariant, carrying forward `int.serializer`'s own AC-8).
- **AC-4.** Disabling debug (`:debug off`, if supported) does **not** clear `DebugTouched` on the header — a passing test toggles debug on then off and asserts the header's flag is still `true`.
- **AC-5.** With debug on, speed control accepts 8× (in addition to the pause/1x/2x/4x `engine.core` already supports) — a passing test asserts `SetSpeed(8x)` is accepted when `IsOn()==true` and rejected (or clamped with a clear error) when `IsOn()==false`.
- **AC-6.** With debug on, cheat commands (free money, instant build, force milestone unlock) are available and, when invoked, apply their effect and emit a world-event/log entry documenting the cheat was used (balance-data hygiene: cheats must be visible in the record, not silent) — a passing test invokes each cheat and asserts both the state change and the logged event.
- **AC-7.** With debug on, an entity JSON inspector is available: given an entity reference (cell/citizen/firm/market), it dumps the entity as JSON — a passing test inspects a known fixture entity and asserts the JSON output round-trips the entity's known fields.
- **AC-8.** With debug on, the fidelity-dial (HOT radius, §5.2) becomes adjustable and its current cost is observable — a passing test asserts the dial's exposed range/current-value API is only reachable when `IsOn()==true`.
- **AC-9.** With debug off, none of AC-5 through AC-8's unlocks are reachable — a passing test asserts each attempted unlock action is rejected with a clear "debug mode required" error when `IsOn()==false`.
- **AC-10.** The console (`` ` ``) and fixture record/replay controls are gated behind debug the same way — a passing test asserts both are unreachable with debug off and reachable with it on.

### Error handling

- **AC-11.** Every action gated behind debug (AC-5 through AC-8, AC-10) returns a registry-sourced `errs.E` (via `foundation.errors`) when attempted with debug off — never a silent no-op and never a panic.
- **AC-12.** `Header.TouchDebug()` failing to persist (e.g. header write fails) is surfaced as a registry-sourced error to the caller that requested debug ON — the enable path must not report success if the sticky flag failed to record, since that would silently violate the hygiene contract.

### Determinism & safety

- **AC-13 (GR#21).** Enabling/disabling debug and invoking cheats does not retroactively change already-committed simulation history — a passing test asserts pre-existing world state (tick count, prior deltas) is unaffected by the act of toggling debug on/off itself (as opposed to a cheat's own intended effect, which legitimately changes state and is logged per AC-6).
- **AC-14.** `grep -rn "time.Now" internal/engine/debug/*.go` (excluding `_test.go`) returns no matches on the tick-affecting paths — any wall-clock use (e.g. timestamping the "cheat used" log entry) must route through `foundation.errors`' injectable clock, consistent with the rest of the engine domain's determinism discipline.

### Documentation

- **AC-15.** The package doc states module key `feat.debugmode`, cites §14 and M0-ENG §3, and explicitly documents the sticky `DebugTouched` contract (once true, forever true) so a future contributor doesn't "fix" it into a togglable flag.

## Out of scope

- F12's rendering of debug-mode state — `ui.screen.debug` (`FEAT-007`) reads `IsOn()` and the registry; this item only provides the switch and unlocks, not the panel.
- `int.serializer`'s `Header`/`DebugTouched` implementation itself — already frozen in Sprint 0 (`INT-002`); this item only calls it.
- The actual fixture record/replay file format — `harness.replay` (`MOD-013`, Sprint 2); this item only gates access to whatever controls that module exposes.

## Escalations

- None at draft time. `status: draft-ahead` — depends on `int.serializer` (already frozen, low risk) and the module registry (`MOD-005`, for `CanToggle`-style gating consistency with `ui.screen.debug`); refresh AC-3/AC-4's exact `Header` method names against `internal/foundation/serialize/header.go` at dispatch (already confirmed as `TouchDebug`/`MergeDebugTouched` in Sprint 0's landed code, low drift risk).
