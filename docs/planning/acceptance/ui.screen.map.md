BOW code: FEAT-005

# Acceptance criteria — ui.screen.map (FEAT-005)

**BOW code:** FEAT-005
**Spec refs:** §13-F1 (UI, F1 Map bullet, `docs/METROPOLIS-MASTER-v2.1.md` line 244: "scrollable viewport + minimap; overlays (`o` cycles): ownership, land value, zoning, utilities, traffic, pollution, decay, coverage per service. Inspect any cell/building/citizen (`enter`), follow a citizen (`f`)."); UI-SPEC §2 (visual language — heatmaps, foreground/background two-layer cells, line 736); `int.protocol` view-subscription contract (INT-001).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1)
**Package under test:** `internal/ui/screens/map/` (confirm via `node claude-bow.js show FEAT-005` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/screens/map/...`.

## User stories

- As **the player**, I need a scrollable map viewport plus minimap on F1, driven entirely by `int.protocol` view subscriptions against the running engine (stub, in Sprint 1), so I can navigate the city without the UI ever touching engine internals.
- As **the player**, I need an overlay cycle (ownership, land value, zoning, utilities, traffic, pollution, decay, service coverage, parking occupancy, vitality) with `o`/`O` key controls, so I can inspect any city metric as a heatmap without leaving F1.
- As **the player**, I need `Enter` to inspect any cell/building/citizen and `f` to follow a citizen, so the drill-through rule (UI-SPEC §4) starts working from the very first screen built.
- As **`feat.skeleton`**, I need F1 fully wired against `harness.stub`'s Folkestone-64 fixture, so the walking skeleton has one real, playable-looking screen end-to-end before any real model exists.

## Scope

The F1 map screen: scrollable viewport + minimap, overlay cycle, cell inspect, citizen follow — all sourced via `int.protocol` view subscriptions against `harness.stub` (Sprint 1) and, unchanged, a real engine later.

## Acceptance criteria

### Functional

- **AC-1.** F1 subscribes to a named view (per `int.protocol`'s `ValidateViewName` grammar, e.g. `f1.viewport`) and renders exclusively from the resulting `Delta` stream — no direct call into any engine-internal type. Check: `go list -deps ./internal/ui/screens/map/...` shows no import of `internal/engine/...`.
- **AC-2.** The viewport scrolls (pan) and a minimap shows the full Folkestone-64 extent with the current viewport rectangle indicated — a passing test asserts panning changes the rendered viewport origin and the minimap's indicator rectangle moves correspondingly.
- **AC-3.** The overlay cycle (`o` forward, `O` reverse) steps through at minimum: ownership, land value, zoning, utilities, traffic, pollution, decay, per-service coverage, parking occupancy, vitality — a passing test asserts cycling through all overlays returns to the starting overlay after N steps (N = overlay count) in each direction.
- **AC-4.** Each overlay renders as a background-colour heatmap layer while the foreground glyph continues to show structure/terrain — carrying forward `ui.widgets`' heatmap AC-5 (independent foreground/background) applied at the screen level: a passing test asserts switching overlays changes only background colour data, never foreground glyphs, for unchanged cells.
- **AC-5.** `Enter` on a selected cell/building opens an inspect view showing that entity's available fields (from the subscribed view's data — on Folkestone-64/H-STUB this is fixture data, not live simulation) — a passing test selects a known fixture cell and asserts the inspect view surfaces its known fixture attributes.
- **AC-6.** `f` on an inspected citizen (where the fixture provides citizen entities) switches the viewport to follow that citizen's position across subsequent deltas — a passing test asserts the viewport's tracked-entity state updates correctly and the viewport recentres as the followed entity's position changes in the delta stream.
- **AC-7.** F1 runs against `harness.stub`'s Folkestone-64 fixture with no code changes required when the underlying `Transport` is later swapped for a real engine (per `int.protocol`'s contract — the UI "cannot tell the difference"). This is checked structurally via AC-1 (no engine-internal imports) rather than by actually swapping engines in this item's own test suite (that swap is `feat.skeleton`'s integration concern).

### Error handling

- **AC-8.** A delta for an unknown/stale subscription (already unsubscribed) is dropped and logged (registry-sourced, via `foundation.errors`) rather than applied or causing a panic.
- **AC-9.** Inspecting a cell/entity that has vanished from the latest delta (e.g. demolished) shows a clear "no longer available" state rather than stale or corrupted data.

### Determinism & safety

- **AC-10.** F1's rendering is a pure function of (current view-model state, viewport position, active overlay) — the same inputs render identically across repeated calls; no `time.Now()`-driven content beyond the shared 300ms threshold-pulse primitive from `ui.widgets` (reused, not reimplemented). `grep -rn "time.Now" internal/ui/screens/map/*.go` (excluding `_test.go`) returns no matches.
- **AC-11.** `go test ./internal/ui/screens/map/... -race -count=1` passes with no data race between the delta-applying goroutine and the render path.

### Documentation

- **AC-12.** The package doc states module key `ui.screen.map`, cites §13-F1 and UI-SPEC §2, and documents the view-subscription name(s) F1 depends on.

## Out of scope

- Real engine data — Sprint 1's F1 runs against `harness.stub`'s fixture only; real terrain/citizens/traffic overlays populate once those engine modules land (Sprint 3+).
- F2-F12 screens — separate items.
- The drill-through rule's *general* framework (dashboards, other screens) — `MOD-038`; F1 only needs to demonstrate the pattern for its own entities per AC-5/AC-6.

## Escalations

- None at draft time. `status: draft-ahead` — depends on `ui.widgets` (`MOD-010`) and `harness.stub` (`MOD-008`); refresh the exact view-subscription name(s) and fixture entity shapes against `harness.stub`'s actual implementation once it lands. Note: the BOW item's overlay list (`parking occupancy`, `vitality`) extends §13's line-244 list (which stops at "coverage per service") with entries drawn from later catalogue sections (§38 Parking, §41 Café Culture vitality) — AC-3 is worded "at minimum" to cover both without conflict.
