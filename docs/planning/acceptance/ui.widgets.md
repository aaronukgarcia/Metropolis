BOW code: MOD-010

# Acceptance criteria — ui.widgets (MOD-010)

**BOW code:** MOD-010
**Spec refs:** UI-SPEC §2 (The visual language — block text as a real instrument panel, `docs/METROPOLIS-MASTER-v2.1.md` lines 730-741); `ui.core` (MOD-009, the cell-buffer substrate these widgets draw on).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1)
**Package under test:** `internal/ui/widgets/` (confirm via `node claude-bow.js show MOD-010` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/ui/widgets/...`.

## User stories

- As **every F-screen**, I need a shared widget library (borders, sparklines, Braille charts, heatmaps, gauges, tables, queue lanes) drawing on `ui.core`'s cell buffer, so forty-four systems read as one consistent instrument panel instead of forty-four bespoke UIs (UI-SPEC §2).
- As **a colourblind player**, I need a colourblind-alternative palette shipped day one, so the semantic colour grammar (money green, water blue, power yellow, danger red…) doesn't exclude me.
- As **`ui.screen.map`**, I need heatmap overlays that carry a background-colour metric layer independent of the foreground structure glyph, so two data layers can render per cell.
- As **`ui.screen.debug`**'s phase-timing strip and any trending number, I need a 12-cell sparkline widget as a shared idiom, so every trending value (cash, road volume, school roll…) reads identically.

## Scope

The shared visual grammar widget set: box-drawing borders (focused/unfocused variants), semantic + colourblind palettes, 12-cell sparklines, Braille 2×4 sub-grid charts, background-ramp heatmaps, block-element gauges, big-number tiles with delta arrows, sortable/filterable/exportable tables, literal truck-queue lanes, 300ms threshold-pulse highlight.

## Acceptance criteria

### Functional

- **AC-1.** A border-drawing widget renders box-drawing characters (`─│┌┐└┘├┤`) with a heavy variant for the focused pane and a dim variant for unfocused panes — a passing snapshot test asserts the correct glyph set per focus state.
- **AC-2.** A semantic colour palette is defined (money green, water blue, power yellow, danger red, warning amber, decay grey-purple, selection inverse) **and** a colourblind-safe alternative palette ships alongside it, selectable at runtime — a passing test asserts both palettes exist, are distinct, and every semantic colour has an entry in both.
- **AC-3.** A sparkline widget renders using the block set `▁▂▃▄▅▆▇█` over a fixed 12-cell width representing the last 24 months of a series — a passing test feeds a known series and asserts the rendered glyph sequence matches an expected mapping (value → block height).
- **AC-4.** A Braille-chart widget renders using the 2×4 sub-grid-per-cell Braille pattern set, producing an effective 2×4× the cell-grid resolution for line/scatter charts and the population-pyramid idiom — a passing test asserts a known point set produces the expected Braille codepoints at the expected cell positions.
- **AC-5.** A heatmap widget renders a background-colour ramp independent of the foreground glyph — a passing test asserts foreground (structure/terrain glyph) and background (metric-driven colour) are set independently for the same cell, i.e. changing the metric never changes the foreground glyph and vice versa.
- **AC-6.** A gauge widget renders block-element fill levels (`█▓▒░`) proportional to a 0-1 (or min/max) input — a passing test asserts fill-glyph count/partial-glyph selection matches known fractional inputs (e.g. 0.5 fill on an 8-cell gauge produces 4 full blocks, or the correctly rounded partial-block glyph).
- **AC-7.** A big-number tile widget renders a large figure, a delta arrow (up/down/flat), a sparkline, and threshold-based colouring — a passing test asserts the delta-arrow direction and threshold colour are correctly derived from (previous, current, thresholds) inputs.
- **AC-8.** A table widget supports sort (cycling columns), inline filter query, and CSV export to the save folder — a passing test asserts sorting toggles correctly across at least 3 columns/cycles, filtering narrows rows by a substring/predicate match, and export produces valid CSV (parseable, correct row/column count) matching the filtered+sorted view.
- **AC-9.** A queue-lane widget renders cargo-coded glyphs growing leftward with a wait-time figure, per the junction-approach idiom (§19/UI-SPEC §2 "queues rendered literally") — a passing test asserts glyph count matches a given queue length and the wait-time figure is rendered.
- **AC-10.** A 300ms threshold-pulse highlight exists as a shared, reusable animation primitive (not per-widget bespoke code) — triggered when a value crosses a configured threshold; a passing test asserts the pulse state is active for the documented duration window after a threshold-cross event and inactive otherwise.

### Error handling

- **AC-11.** Every widget handles a nil/empty/degenerate input series without panicking, rendering a documented, specific fallback rather than an undefined "sane-looking" state: a zero-length sparkline series renders a blank sparkline (no glyphs plotted, not a crash or a stale/repeated last value); a gauge value outside `[0,1]` is clamped to the nearest bound (0 or 1) before rendering, not extrapolated past the fill bar's ends; a table with zero rows renders its header with an empty visible-row set (`VisibleRows` returns `nil`/empty, not a slice indexing panic). A passing test exercises each of these three named cases and asserts the specific stated outcome (blank sparkline, clamped gauge fill, empty-but-headered table) — not merely that the widget function returns without panicking.

### Determinism & safety

- **AC-12 (GR#21).** Widget rendering is a pure function of its input state — the same input state renders to identical cell-buffer output across repeated calls; no `time.Now()`-driven content (the 300ms pulse's *timing* is externally driven by the caller's tick, not internally sampled from the wall clock inside the widget) — `grep -rn "time.Now" internal/ui/widgets/*.go` (excluding `_test.go`) returns no matches.
- **AC-13.** Hot widgets (sparkline, gauge, heatmap — the ones redrawn every UI tick per UI-SPEC §5's budget) use zero-allocation draw paths with precomputed glyph/style lookup tables — a `-benchmem` benchmark on at least the sparkline widget reports `0 allocs/op` after warm-up, matching `ui.core`'s AC-7 pattern.

### Documentation

- **AC-14.** The package doc states module key `ui.widgets`, cites UI-SPEC §2, and documents the semantic-palette-plus-colourblind-alternative contract so future widgets are built palette-aware from the start rather than hardcoding colours.

## Out of scope

- The three diagram engines (chain diagrams, network schematics, text Sankey) — `MOD-037`, a separate later item built on this widget set.
- Dashboard composition/layout editor and the drill-through rule — `MOD-038`, separate item.
- Any specific F-screen assembling these widgets into a real layout — separate `ui.screen.*` items.

## Escalations

- None at draft time. `status: draft-ahead` — depends on `ui.core` (`MOD-009`); refresh the exact cell-buffer drawing API this item's widgets call against once `ui.core` lands.
