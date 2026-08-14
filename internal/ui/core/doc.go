// Package core is the TUI renderer core: retained front/back cell
// buffers with diff flushing, the T-INPUT/T-RENDER/T-VIEWS loop
// skeleton, a terminal capability probe, and minimum-size layout
// reflow. tcell screen access is single-goroutine (T-RENDER); this
// package owns that goroutine and enforces the rule with a runtime
// assertion (render.go's ownership guard), not just a comment
// (UI-SPEC §1: "tcell screen access is single-goroutine"; M0-ENG §1.1).
//
// Per Golden Rule #20 (Contract-First, Stub-Forever), packages under
// internal/ui must consume the engine ONLY via internal/protocol —
// importing internal/engine directly from here is lint-banned (see
// .golangci.yml's ui-must-not-import-engine rule). This package imports
// only internal/protocol, github.com/gdamore/tcell/v2 (the one
// sanctioned third-party dependency, MOD-009/UI-SPEC §1: "Go
// implementation: tcell for terminal I/O and events"), and the standard
// library.
//
// Module key: ui.core (see code.json; GUID 0a68ef61-6148-458f-8fa4-722119ba3d44)
// Spec ref:   UI-SPEC §1, §5; M0-ENG §1.1
//
// # Files
//
//   - buffer.go   — Buffer: a flat grid of styled Cells. Front/back
//     buffer pairs are two *Buffer values; nothing about Buffer itself
//     knows which one is "front."
//   - diff.go     — Flush: diffs a back Buffer against a front Buffer
//     and writes only the changed cell runs to a ScreenWriter (the tcell
//     Screen subset it needs), then updates front in place to match back
//     — never a Clear/Fill, so flicker is structurally impossible
//     (UI-SPEC §1).
//   - capability.go — Probe: selects a terminal Profile (Windows
//     Terminal truecolor+mouse vs conhost degraded 16-colour, no mouse)
//     from an injectable CapabilitySource, so the choice is testable
//     without a real terminal.
//   - layout.go   — minimum-size enforcement (120x30) and per-pane
//     reflow: a pane whose allotted Rect falls below its own minimum
//     collapses to a tab stub rather than rendering corrupted content.
//   - input.go    — T-INPUT: polls tcell events off an EventSource and
//     translates them to InputMsg, delivered over a non-blocking channel
//     (never blocks, ever — UI-SPEC §1/§5).
//   - render.go   — T-RENDER: the sole owner of the tcell.Screen. Runs a
//     10Hz ticker plus immediate-on-input, applies front view models
//     through caller-supplied DrawFuncs into the back Buffer, then
//     calls Flush. Carries the single-goroutine ownership assertion.
//   - views.go    — T-VIEWS: consumes protocol.Transport's Delta stream,
//     tracks per-subscription Seq gaps with protocol.SeqTracker (raising
//     a staleness flag on a gap, UI-SPEC §1's status-bar dot), and
//     publishes double-buffered ViewModels via an atomic pointer swap so
//     T-RENDER's reads can never tear (AC-4).
//   - screen.go   — NewScreen: tcell.Screen construction + Init with a
//     registry-sourced, non-panicking error path (AC-8).
//   - harness_test.go / *_test.go — tests, all headless via
//     tcell.NewSimulationScreen (no real terminal in CI).
//
// # Rules this package must not break
//
//   - tcell.Screen methods are called from exactly one place:
//     RenderLoop (render.go). AC-3's check greps for the literal
//     `screen.` receiver across this package's non-test files and
//     expects every match confined to render.go and screen.go (screen
//     construction, not screen mutation during a frame).
//   - T-INPUT only translates tcell events to InputMsg values; it never
//     touches the screen and never blocks (input.go).
//   - The UI holds view models built from Deltas only — never a
//     reference into engine-owned state (M0-ENG §1.1; enforced
//     structurally: this package has no dependency capable of handing
//     it one, since it doesn't import internal/engine).
//   - No wall-clock logic gates rendering correctness: the 10Hz tick is
//     a pacing mechanism, not a source of truth — a late/missing Delta
//     degrades to "last frame stands" plus a staleness flag, never a
//     panic or a blocked render (UI-SPEC §1).
//
// # Performance note (UI-SPEC §5)
//
// Flush and Buffer's Set/Get are written to be zero-allocation in
// steady state: Buffer is a single pre-sized []Cell slice (no per-cell
// interface boxing — Cell is a small value struct), and Flush's diff
// loop only indexes into it and calls ScreenWriter.SetContent, which
// itself takes a nil combining-rune slice on the hot path. The
// FlushBenchmark in diff_test.go asserts 0 allocs/op after warm-up
// against a no-op ScreenWriter (isolating this package's allocation
// behaviour from tcell's own Screen implementation, which is out of
// this package's control). Widget draw callbacks (DrawFunc) are NOT
// held to this bar yet — MOD-010 (ui.widgets) is where the
// escape-analysis gate arms for the wider draw path; this package's
// contract is that its OWN plumbing (buffer diff/flush, view-model
// publish) never becomes the allocation source.
package core
