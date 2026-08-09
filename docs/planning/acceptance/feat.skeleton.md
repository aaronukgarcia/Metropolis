BOW code: FEAT-006

# Acceptance criteria — feat.skeleton (FEAT-006)

**BOW code:** FEAT-006
**Spec refs:** M0-ENG §6.4 (Working agreement point 4, "Walking skeleton first", `docs/METROPOLIS-MASTER-v2.1.md` line 997); M0-ENG §2 (Harness strategy, lines 842-851); Sprint plan v1 §2 S1 exit gate (`docs/planning/sprint-plan-v1.md`: "Skeleton runs end-to-end on Folkestone-64; determinism gate green at 1 and 14 workers; F12 shows every module as stub with health OK").
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1) — **these criteria are INTEGRATION criteria**, verifying the wiring of already-independently-tested items, not new unit-level behaviour.
**Package under test:** `cmd/metropolis/` (the binary entry point wiring everything together; confirm via `node claude-bow.js show FEAT-006` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./cmd/metropolis/...` plus a full-repo `go build ./...`/`go test ./... -race -count=1` pass (this item's whole point is that everything above it still passes together).

## User stories

- As **the Sprint 1 exit gate**, I need "protocol + registry + stub-everything engine + H-STUB + determinism gate + F1 map on fixture" running end-to-end, so Sprint 2 can start on a proven vertical slice instead of untested independently-built pieces.
- As **Bill**, I need one command that launches a playable-looking-but-computes-nothing city on Folkestone-64, so I can visually confirm the whole stack (protocol → stub engine → UI) before any real model work begins.
- As **every later engine module**, I need the module registry booted and visible in F12 with every module correctly reporting `stub` status, so "one module real at a time" (M0-ENG §2's stubbing discipline) has a proven starting state to flip from.

## Scope

The integration of `int.protocol`, `foundation.errors`, `harness.stub`, `engine.core`, `feat.detgate`, `ui.core`, `ui.widgets`, `ui.screen.map`, and the module registry into one runnable binary (`cmd/metropolis`) that boots, runs Folkestone-64 end-to-end, and passes the determinism gate.

## Acceptance criteria

### Functional (integration)

- **AC-1.** `go run ./cmd/metropolis` (default, non-headless) launches, boots `harness.stub`'s `StubEngine` over `int.protocol`'s `Transport`, and renders F1 (`ui.screen.map`) showing the Folkestone-64 fixture — verified by a scripted-input UI test (headless UI harness driving key sequences against a recorded delta stream, per UI-SPEC §5's closing note) asserting the rendered cell buffer matches an expected snapshot of Folkestone-64's initial view.
- **AC-2.** The module registry (`MOD-005`, a Sprint-0/1 dependency of `engine.core`) boots with **every** registered module reporting status `stub` and health `ok` — a passing integration test queries the registry after boot and asserts zero modules report `real`, `off`, or a non-`ok` health.
- **AC-3.** F12 (`ui.screen.debug`, if landed; otherwise the registry's own API) surfaces this stub-everything state — the Sprint-1 exit gate's "F12 shows every module as stub with health OK" is checked either via `ui.screen.debug`'s rendered output (if that item has landed by the time this integration test runs) or, if not yet landed, via a lower-level registry API test that `ui.screen.debug` will later just render — Tester confirms which path was actually exercised and records it.
- **AC-4.** The determinism gate (`feat.detgate`) is green when run against this fully-wired binary/orchestrator at both `POOL-SIM=1` and `POOL-SIM=14` — `go test ./internal/engine/core/... -run TestDeterminismGate -race -count=1` (or the actual gate test) passes using the real wired-up `cmd/metropolis` boot path, not a synthetic harness bypassing the actual wiring.
- **AC-5.** `AdvanceTicks`, `SetSpeed`, `Pause`, `Resume`, `Subscribe`, `Unsubscribe` all work end-to-end through the full stack (UI key input → `int.protocol` Command → `harness.stub` → Delta → UI render) — a scripted-key integration test drives at least one of each and asserts the visible effect (e.g. pause stops tick advancement visible in F12/timing, subscribe causes F1 to start rendering fixture updates).
- **AC-6.** `-headless` mode (once `harness.headless` lands — if not yet landed at this item's dispatch time, this AC is satisfied by confirming `cmd/metropolis`'s flag-dispatch structure has a clear seam for it, not a full headless run) boots the same wired stack without the UI.

### Error handling

- **AC-7.** A boot-time failure in any wired component (e.g. `harness.stub` fixture fails to load) produces a clear, registry-sourced startup error and a non-zero exit — not a partial boot that silently renders a broken/blank screen.

### Determinism & safety

- **AC-8 (GR#21).** AC-4's determinism-gate pass is the authoritative determinism check for this item — no additional ad-hoc determinism assertions are needed here beyond confirming the gate runs against the *actual* wired binary, not a stand-in.
- **AC-9.** `go vet ./...` and `gofmt -l .` are clean across the **entire repo**, not just this item's own new files — since this item's job is proving everything fits together, a lint/format regression anywhere in the stack it wires is this item's problem to catch (the Tester should run the full-repo SG-1/SG-2/SG-3 gates, not scoped to `cmd/metropolis/` alone, for this one item).

### Documentation

- **AC-10.** `cmd/metropolis/`'s package doc (or a `docs/design/` walking-skeleton page) states module key `feat.skeleton`, cites M0-ENG §6.4, and lists exactly which Sprint 0/1 items it wires together, so a reader can trace "why does this file import X" back to the BOW.

## Out of scope

- Any new simulation/UI behaviour beyond wiring already-built (and already independently-accepted) items together — if an integration test reveals a bug in a dependency item (e.g. `harness.stub` mishandles a command), the fix belongs to that item's own junior/Tester loop, not to `feat.skeleton`, per the BA's standing rule (criteria are the contract each item is independently held to).
- Real simulation models, real terrain, real citizens — Sprint 1's skeleton is deliberately "a playable-looking nothing" (M0-ENG §6.4's own phrase).
- Sprint 2+ harnesses (`harness.replay`, `harness.synth`) — this item only needs `harness.stub` and `harness.headless`'s seam.

## Escalations

- None at draft time. `status: draft-ahead` — this item cannot actually be built or tested until essentially all other Sprint-1 items land; it is deliberately the LAST item dispatched within Sprint 1's `claude-bow.js ready` order (its BOW `depends on` list already enforces this). Refresh this file once the actually-landed dependency set (and any items still open at Sprint-1-close) is known, since AC-3/AC-6 above already hedge for two dependencies (`ui.screen.debug`, `harness.headless`) that may not have landed yet when this item starts.
