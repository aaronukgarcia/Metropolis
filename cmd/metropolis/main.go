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
//   - harness.stub      — StubEngine, H-STUB: still exists for its own
//     fixtures/tests, but is NO LONGER the engine this binary boots
//     (FEAT-082 flipped boot.go off StubEngine onto a real engine.core
//     wired by internal/engine/compose).
//   - engine.core       — the real tick orchestrator; FEAT-082 flipped
//     this binary's boot path off StubEngine onto a real core.Engine wired
//     by internal/engine/compose (the composition root), so the interactive
//     binary and the -headless driver now reach the same simulation through
//     the same compose.Wire.
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
// FEAT-082 flipped this binary off StubEngine, so boot.go now constructs a
// real core.Engine wired by the composition root (internal/engine/compose)
// — the same compose.Wire the -headless driver reaches. Two claims are
// still worth keeping distinct:
//
//   - PROVEN end to end, in this binary: int.protocol -> engine.core ->
//     ui.core -> ui.screen.map, with the module registry booting clean.
//   - NOT yet proven here: the map screen RENDERING the simulated world.
//     engine.core v1 serves exactly one view ("engine.status"); the map
//     screen's "f1.viewport" Subscribe is issued and honestly rejected
//     (ErrUnknownView) — so what you watch is an empty map, not a canned
//     Folkestone-64 fixture. The real viewport rendering is the map
//     screen's own follow-up, not this flip's scope.
//
// The walking-skeleton doc note that used to live here ("you are watching
// StubEngine render Folkestone-64, not the tick orchestrator") was true
// for Sprint 1 and is now resolved by FEAT-082/ASM-001: the tick
// orchestrator really is wired into this binary.
//
// Module key: feat.skeleton (see code.json; GUID aedcd472-ec92-4d21-a0ff-4a5dcc7916f4)
// Spec ref:   M0-ENG §6.4 (line 997); M0-ENG §2 (lines 842-851)
// Acceptance: docs/planning/acceptance/feat.skeleton.md (FEAT-006)
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
