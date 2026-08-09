// Package debug is F12, the debug/info panel: build & code identity,
// runtime stats against the §1.3 memory budget table, a view of the
// module registry with guarded toggles, the last-50 error tail, a
// per-phase µs sparkline, and a read-only BoW tab.
//
// Module key: ui.screen.debug (see code.json)
// Spec ref:   M0-ENG §3 (Debug mode & the Info Panel,
//
//	docs/METROPOLIS-MASTER-v2.1.md lines 853-865), §1.3 (memory budget
//	table, lines 828-838)
//
// # Upstream sources, one per pane
//
//   - Build & code:  internal/foundation/buildinfo (Version/Commit/Branch/
//     BuildTime, all -ldflags-injected — see buildinfo.go's own doc
//     comment) plus runtime.Version() for the Go toolchain version. This
//     package never hand-maintains a version/commit/branch literal
//     (M0-ENG §3, AC-1) — collectBuildInfo in build.go is the single
//     place those fields are read.
//   - Runtime stats: caller-injected via WithRuntimeSource (a
//     RuntimeMetricsFunc); DefaultRuntimeMetricsProvider (runtime.go)
//     fills what internal/foundation/registry does not already track
//     (heap/sys/GC-pause/goroutines) directly from the Go runtime, plus
//     uptime from an injected start time — the only two places this
//     package's non-test code calls time.Now (AC-13's carve-out: display
//     of a wall-clock-derived elapsed value, nothing else). Fields with
//     no live source yet in Sprint 1 (sim date, speed, tick number,
//     channel depths, input-echo latency) default to their zero value
//     and are rendered plainly rather than invented — a later item wires
//     a real provider once those subsystems exist.
//   - Module registry: internal/foundation/registry.Registry — this
//     package is a *view* of it (Registry.List, Registry.SetStatus), not
//     a parallel implementation (M0-ENG §3: "the panel is a view of it,
//     not a parallel system. One registry, two consumers").
//   - Error tail: internal/foundation/errs.Recent() — up to 200 entries,
//     oldest-first; Collect (screen.go) takes the last 50 itself.
//   - Phase timing: internal/foundation/registry.Registry.TickCostHistory,
//     keyed by phase name — the same ring buffer the module registry
//     view reads, never a second one (AC-8's explicit "F12 does not need
//     its own ring buffer for this").
//   - BoW tab: an injected BoWSource (WithBoWSource) — a read-only query
//     against the metro BOW in production, a mock in tests (AC-9).
//
// # GR#20 note: phase order is a local mirror, not an import
//
// AC-8 names internal/engine/core.MonthlyPhaseOrder as the canonical
// phase sequence, but internal/ui packages may not import
// internal/engine in non-test code (GR#20, mechanically enforced by
// golangci-lint's depguard — see .golangci.yml's
// "ui-must-not-import-engine" rule). phase.go therefore carries its own
// monthlyPhaseOrder constant, a literal copy of the six PhaseKind string
// values from internal/engine/core/phase.go; determinism_test.go imports
// internal/engine/core (test-only exemption, deliberate and verified per
// CLAUDE.md) solely to assert the two orders never drift apart.
//
// # Layout note
//
// This package renders every pane (build/code, runtime, registry, error
// tail, phase timing, BoW) into one composite view via Render — it does
// not implement tab-switching. Per this item's "out of scope" section,
// key binding to drive tabs/selection is ui.keys' later job, the same
// division ui.screen.map's doc.go documents for its own key handling.
//
// # Determinism (AC-13)
//
// Render is a pure function of (buf, rect, Snapshot): the same Snapshot
// renders identically across repeated calls. Snapshot is built by
// Collect, which is where the wall clock and any live source may be
// read; Render itself never samples anything live.
package debug
