// Package widgets is the shared visual-grammar widget library every
// F-screen draws through, so forty-four systems read as one instrument
// panel instead of forty-four bespoke UIs (UI-SPEC §2, "The visual
// language — block text as a real instrument panel").
//
// Module key: ui.widgets (see code.json)
// Spec ref:   UI-SPEC §2 (docs/METROPOLIS-MASTER-v2.1.md lines 730-741),
//
//	§4 (tables, drill-through); depends on ui.core (MOD-009).
//
// Per Golden Rule #20 (Contract-First, Stub-Forever), packages under
// internal/ui must consume the engine ONLY via internal/protocol —
// importing internal/engine directly from here is lint-banned (see
// .golangci.yml's ui-must-not-import-engine rule, which denies exactly
// that one package; it does not ban tcell). This package imports
// internal/ui/core, github.com/gdamore/tcell/v2, and the standard
// library only — no internal/engine, no internal/protocol (widgets are
// pure render functions over core.Buffer + plain Go state; they have no
// need of the protocol's Delta/Transport types at all).
//
// # Mismatch note vs the draft-ahead acceptance criteria
//
// The MOD-010 acceptance doc's dispatch instructions describe this
// package's imports as "internal/ui/core + stdlib ONLY — no tcell
// directly." That is not achievable against ui.core's actual, delivered
// API: core.Cell.Style and core.Buffer.Set/Get are typed directly as
// tcell.Style (see internal/ui/core/buffer.go), and tcell.Style/Color
// have no ui.core-owned equivalent or wrapper. Any widget that wants to
// colour a cell — which is all of them — must therefore construct a
// tcell.Style, which means importing tcell. Per the dispatch's own
// escalation rule ("if its API differs from AC assumptions, the
// working-tree code wins, note mismatches"), this package imports tcell
// for style/colour construction only, never for Screen/terminal I/O
// (no tcell.Screen, tcell.NewSimulationScreen, or event types appear
// here — that stays exclusively in ui.core's render.go/screen.go per
// UI-SPEC §1's single-goroutine rule). The GR#20 lint rule itself only
// denies internal/engine from internal/ui/**, so this does not violate
// the enforced contract, only the acceptance doc's paraphrase of it.
//
// # Files
//
//   - doc.go       — this file.
//   - palette.go   — Palette: semantic colour tokens (money/water/
//     power/danger/warning/decay/selection) as data, with a default
//     truecolor palette and a colourblind-safe alternate, both
//     satisfying UI-SPEC §2's "colourblind alternative palettes ship
//     day one" line. Screens pick a Palette at capability-probe time
//     (core.Profile does not carry colour-vision preference itself —
//     that is a player setting, out of ui.core's scope — so callers
//     thread a chosen Palette into every widget call here).
//   - border.go    — Border: box-drawing panel borders, heavy variant
//     for the focused pane, dim (light glyphs + dim attribute) for
//     unfocused, with a title slot.
//   - sparkline.go — Sparkline: the 12-cell ▁▂▃▄▅▆▇█ trend idiom, one
//     shared normalisation for every trending number in the game.
//   - braille.go   — BrailleCanvas + BrailleChart: the 2x4 dot
//     sub-grid-per-cell line-chart substrate (population pyramid /
//     projection idiom), with a solid-history / dim-projection
//     two-series mode.
//   - heatmap.go   — Heatmap: a background-colour ramp over a rect,
//     independent of whatever foreground glyph already occupies each
//     cell.
//   - gauge.go     — Gauge: block-element (█▓▒░) fill bars with
//     threshold colouring, at quarter-cell fill resolution.
//   - bignum.go     — BigNum: a dashboard tile — large figure, delta
//     arrow, embedded sparkline, threshold-derived colour.
//   - table.go     — Table: sortable/filterable/scrollable row
//     rendering plus CSV export (UI-SPEC §4's "player who wants to
//     spreadsheet their city"). Render only — key handling to drive
//     sort/filter/scroll is ui.keys' job (out of scope here).
//   - queuelane.go — QueueLane: the signature literal truck-queue —
//     cargo-coded glyph runs growing leftward, wait-time figure.
//   - pulse.go     — PulseState: the shared 300ms threshold-pulse
//     primitive (UI-SPEC §2's "300ms highlight pulse on any value that
//     just crossed a threshold"), driven by caller ticks, never by an
//     internally sampled wall clock (GR#21 — see the "Determinism" note
//     below).
//
// # Determinism (GR#21)
//
// Every widget in this package is a pure function of (Buffer rect,
// state): no goroutines, no channels, no wall-clock reads. The 300ms
// pulse is a state *flag* (pulse.go's PulseState), advanced by the
// caller's own tick delta — nothing in this package samples the wall
// clock internally; searching this package's non-test .go files for
// the standard library's clock-read call (package "time", function
// "Now") returns no matches, matching ui.core's own "last frame
// stands, never a wall-clock gate on correctness" rule (UI-SPEC §1).
//
// # Performance (UI-SPEC §5)
//
// Sparkline, Gauge, and Heatmap are the widgets UI-SPEC §5's budget
// redraws every UI tick; their draw paths avoid heap allocation
// (precomputed glyph tables, fixed-size stack arrays, no closures on
// the hot path) — see sparkline_test.go's BenchmarkSparkline for the
// 0 allocs/op assertion, mirroring ui.core's own FlushBenchmark
// pattern (buffer/diff.go's doc comment).
package widgets
