// Command metropolis is the game's entrypoint: the TUI process that hosts
// the UI process-domain and (in-process, v1) the engine domain (M0-ENG
// §1.1 process & thread topology).
//
// This is the Sprint 1 walking skeleton (M0-ENG §6.4's "walking skeleton
// first" working agreement, M0-ENG §2's stub-everything harness strategy):
// one command launches a playable-looking-but-computes-nothing city on
// Folkestone-64, proving that every one of this Sprint's dependencies
// actually fits together end to end, not just in isolation:
//
//   - int.protocol      — the Command/Delta/Transport envelope everything
//     below speaks (internal/protocol).
//   - foundation.errors — every boot-time failure below is a registry-
//     sourced *errs.E (data/errors.json's MET-E900), never an ad hoc
//     string (GR#7).
//   - harness.stub      — StubEngine, H-STUB: drives the real Folkestone-64
//     fixture over int.protocol's Transport (internal/engine/stub).
//   - engine.core       — the real tick orchestrator; its own determinism
//     is verified independently by feat.detgate's gate test
//     (go test ./internal/engine/detgate/... -run TestDeterminismGate),
//     which boots its own engine.core.Engine instances rather than this
//     binary's live StubEngine-driven path — this binary does not
//     separately boot an engine.core.Engine for rendering (see boot.go).
//   - feat.detgate      — see engine.core note above; not wired into this
//     binary's runtime, verified via its own CI-gated test suite.
//   - ui.core           — RenderLoop/InputLoop/ViewsLoop/ViewStore, the
//     tcell.Screen owner (internal/ui/core).
//   - ui.widgets        — widgets.DefaultPalette, the two-layer semantic
//     colour source F1 paints with (internal/ui/widgets).
//   - ui.screen.map     — F1, the map screen rendering Folkestone-64
//     (internal/ui/screens/map).
//   - foundation.registry — the module registry: every module named above
//     is registered at boot, reporting status stub / health ok (AC-2),
//     visible to F12 (ui.screen.debug, FEAT-007) or directly via the
//     registry's own API (see registerSkeletonModules in boot.go).
//
// Deliberately NOT wired here (Sprint-2 dependencies, tracked as
// follow-ups against their owning modules per this item's acceptance doc,
// docs/planning/acceptance/feat.skeleton.md):
//
//   - ui.keys (MOD-011) — no key event is translated into a protocol.Command
//     anywhere in this package. Process-lifecycle key handling (Ctrl+C/Esc/q
//     to quit the running binary, run.go's isQuitInput) is NOT that
//     translation — it never constructs or sends a protocol.Command, it
//     only stops the render/input loops and unwinds the process. See
//     run.go's isQuitInput doc comment.
//   - ui.harness / harness.replay (MOD-014/MOD-013) — the keystroke-level
//     "real terminal in, real Command out" leg (this item's AC-1b/AC-5b).
//   - harness.headless (MOD-015) — see the -headless flag's seam in run.go
//     (AC-6, supplementary).
//
// # What this binary does NOT prove (read before demoing it)
//
// Watching this run, you are watching harness.stub's StubEngine render
// Folkestone-64. You are NOT watching engine.core's tick orchestrator —
// it is registered in the module registry, but this binary never
// constructs a core.Engine, and nothing on screen would look any
// different if it did. The two claims below are both true, both
// valuable, and NOT the same claim:
//
//   - PROVEN end to end, in this binary: int.protocol -> harness.stub ->
//     ui.core -> ui.screen.map, with the module registry booting clean
//     (every module stub / health ok).
//   - PROVEN only in isolation, elsewhere: engine.core's determinism,
//     via feat.detgate's gate, which builds its own Engine instances and
//     never enters this package.
//
// That is the correct intended state for Sprint 1 — M0-ENG §2's
// stub-everything discipline says one module goes real at a time, and
// engine.core is not supposed to drive anything yet. It is recorded
// here, with its own heading, because "the Sprint 1 exit gate passed"
// invites a stronger reading than the wiring supports, and a reader who
// was not there when it was built has no way to see the difference.
// Tracked as ASM-001 in the Book of Work; resolve when engine.core
// actually drives this binary.
//
// Module key: feat.skeleton (see code.json; GUID aedcd472-ec92-4d21-a0ff-4a5dcc7916f4)
// Spec ref:   M0-ENG §6.4 (line 997); M0-ENG §2 (lines 842-851)
// Acceptance: docs/planning/acceptance/feat.skeleton.md (FEAT-006)
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
