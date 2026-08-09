BOW code: MOD-009

# Acceptance criteria — ui.core (MOD-009)

**BOW code:** MOD-009
**Spec refs:** UI-SPEC §1 (Rendering architecture, `docs/METROPOLIS-MASTER-v2.1.md` lines 722-729); UI-SPEC §5 (Performance budget, lines 765-777); M0-ENG §1.1 (process/thread topology, lines 792-817); `int.protocol` (INT-001, the seam this consumes).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1); **Escalation flagged below — tcell dependency**
**Package under test:** `internal/ui/core/` (confirm via `node claude-bow.js show MOD-009` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/core/...`.

## User stories

- As **the player**, I need keystrokes to echo in under 10ms regardless of what the engine is doing, so the game feels instrument-panel-fast, not laggy (UI-SPEC §1/§5).
- As **any F-screen** (`ui.screen.map`, `ui.screen.debug`, …), I need a retained cell-buffer renderer with diff flushing, so screen updates never flicker and only changed cells cost render time.
- As **`T-VIEWS`**, I need a decoupled delta-subscription client applying `int.protocol` deltas to double-buffered view models, so the render loop never blocks on or races with incoming engine state.
- As **any terminal environment** (Windows Terminal or conhost), I need a capability probe selecting the right colour/mouse profile automatically, so the game degrades gracefully instead of rendering garbage on a less-capable terminal.

## Scope

`tcell`-backed TUI core: front/back styled cell buffers with diff flushing, `T-INPUT`/`T-RENDER`/`T-VIEWS` loop topology, capability probe (Windows Terminal vs conhost), minimum-size layout/reflow, zero-allocation hot draw paths.

## Acceptance criteria

### Functional

- **AC-1.** Front/back cell buffers exist covering the full terminal size; a flush diffs back against front and emits only changed cell runs as ANSI — a passing test renders two frames differing in a handful of cells and asserts the flush call count/byte output is proportional to the changed region, not the whole screen.
- **AC-2.** `T-INPUT` translates `tcell` events to internal input messages and **never blocks** — a passing test/benchmark demonstrates the input-handling function returns without waiting on any render or engine operation (e.g. it enqueues to a channel and returns immediately).
- **AC-3.** `T-RENDER` runs on a 10Hz UI tick plus immediate-on-input, and is the **sole** goroutine touching the `tcell` screen (UI-SPEC §1: "tcell screen access is single-goroutine"). Check: `grep -rn "screen\." internal/ui/core/*.go` (the `tcell.Screen` calls) shows they are confined to the file(s)/goroutine implementing `T-RENDER`, and a doc comment states this constraint explicitly.
- **AC-4.** `T-VIEWS` applies `int.protocol` `Delta`s to double-buffered view models: `T-RENDER` reads the front view-model buffer while `T-VIEWS` writes the back one, swapping between ticks — a passing concurrency test drives both loops simultaneously (`-race`) and asserts no torn reads.
- **AC-5.** A capability probe selects Windows Terminal (truecolor, mouse, full Unicode) vs conhost (16-colour palette map, no mouse) automatically at startup — a passing test with a mocked/injected terminal-capability source asserts the correct profile is selected for each case.
- **AC-6.** Minimum terminal size is enforced at 120×30; layout reflows on resize; a pane below its minimum size collapses to a tab stub rather than rendering corrupted/overlapping content — a passing test resizes below a pane's minimum and asserts the collapsed-stub state, not a crash or garbled buffer.
- **AC-7.** Hot draw paths (the cell-buffer diff/flush loop) are zero-allocation in steady state — a `go test -bench . -benchmem` run reports `0 allocs/op` for the flush benchmark after warm-up (matching `engine.core`'s AC-9 pattern; if not yet achievable, the escape-analysis-gate fallback applies identically).

### Error handling

- **AC-8.** A `tcell` initialization failure (e.g. no compatible terminal) produces a clear, user-facing error at startup rather than a panic or silent blank screen.
- **AC-9.** A malformed/unexpected `Delta` (fails to apply to any known view model) is logged via `foundation.errors` (registry-sourced) and dropped, without crashing the render loop or corrupting other view models' state.

### Determinism & safety

- **AC-10.** No shared memory between the UI process-domain and engine domain — the UI holds *view models built from deltas only*, never a direct reference into engine-owned state. Check: `go list -deps ./internal/ui/core/...` shows no import of `internal/engine/...`, only `internal/protocol` (mirrors `int.protocol`'s AC-14, applied from the consumer side — GR#20).
- **AC-11.** `go test ./internal/ui/core/... -race -count=1` passes with no data race across `T-INPUT`/`T-RENDER`/`T-VIEWS` running concurrently.

### Documentation

- **AC-12.** The package doc states module key `ui.core`, cites UI-SPEC §1/§5 and M0-ENG §1.1, and documents the single-goroutine `tcell` access rule (AC-3) prominently, since violating it is a correctness bug that won't show up until real terminal races occur.

## Out of scope

- Individual widgets (sparklines, Braille charts, etc.) — `ui.widgets` (`MOD-010`), a separate item built on top of this core.
- The key-grammar/leader-sequence input language — `MOD-011`, separate item; `ui.core` only needs to deliver translated input events, not interpret the grammar.
- Any specific F-screen's content — `ui.screen.map`, `ui.screen.debug`, etc., separate items.

## Escalations

- **tcell is the one sanctioned third-party Go dependency this item introduces** (per §15/UI-SPEC §1: "Go implementation: tcell for terminal I/O and events"). `go.mod` gains its first non-stdlib runtime dependency here. Flagging per the lead's Sprint-1 brief instruction — this is expected and pre-approved by the master spec, not a scope violation, but the Tester's forbidden-touch gate (SG-5) must allow `go.mod`/`go.sum` changes for this item specifically (they would otherwise be flagged as touching files outside `internal/ui/core/`). Bill: confirm SG-5's allowed-path list should be extended for this one item, or that `go.mod`/`go.sum` are implicitly always allowed for any item introducing a spec-sanctioned dependency.
- Otherwise none at draft time. `status: draft-ahead` — refresh against `int.protocol`'s frozen v1 schema before dispatch.
