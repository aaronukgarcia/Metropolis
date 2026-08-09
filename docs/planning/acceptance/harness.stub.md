BOW code: MOD-008

# Acceptance criteria — harness.stub (MOD-008)

**BOW code:** MOD-008
**Spec refs:** M0-ENG §2.1/§2 (Harness strategy, `docs/METROPOLIS-MASTER-v2.1.md` lines 842-851, esp. "H-STUB — StubEngine"); `docs/design/protocol.md` (int.protocol freeze-review page — message flow, Command/Event/Delta shapes); `int.protocol` (INT-001, frozen v1 contract this item implements against).
**Date:** 2026-08-08
**Status:** draft-ahead (Sprint 1; refresh at dispatch if `int.protocol`'s frozen schema moves before then)
**Package under test:** `internal/engine/stub/` (confirm exact path via `node claude-bow.js show MOD-008` at dispatch time)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/engine/stub/...`.

## User stories

- As **`ui.core`/`ui.screen.map`**, I need a `StubEngine` that speaks the full `int.protocol` contract with canned behaviour, so that every UI screen and the key grammar can be built and regression-tested from week one, before one line of real simulation exists (M0-ENG §2.1).
- As the **UI latency test suite** (UI-SPEC §5), I need chaos knobs (delayed deltas, burst deltas) on `StubEngine`, so that fluidity budgets can be proven under stress before a real engine ever misbehaves.
- As **`feat.skeleton`**, I need `StubEngine` wired to a live `int.protocol` `Transport`, so the walking skeleton runs end-to-end before any real model exists.
- As **`harness.replay`** (a later item), I need `StubEngine`'s scripted delta streams to already be shaped as recordable/replayable data, so H-REPLAY fixtures can be captured from stub runs without a rework.

## Scope

`StubEngine`: a full `int.protocol` implementation with canned behaviour — the static 64×64 "Folkestone-64" fixture world, scripted/recorded delta streams, fake-tick speed controls, and chaos knobs. A **permanent fixture** (never deleted, per M0-ENG §2.1).

## Acceptance criteria

### Functional

- **AC-1.** A `StubEngine` type exists in the package, constructible and drivable through `int.protocol`'s `Transport` interface (the same seam a real engine uses) — `go doc ./internal/engine/stub` shows a constructor and confirms `StubEngine` (or its wiring helper) produces/consumes `protocol.Command`/`protocol.CommandResult`/`protocol.Event`/`protocol.Delta` values, not a private ad-hoc API.
- **AC-2.** Every v1 `Command` `Kind` returned by `protocol.KnownKinds()` is handled — a table-driven test iterates `KnownKinds()` and asserts a well-formed payload of each kind produces a `CommandResult` (not an "unsupported kind" error).
- **AC-3.** A handcrafted 64×64 "Folkestone-64" fixture world exists at a documented path (e.g. `internal/engine/stub/fixtures/`) and loads into a 64×64 grid — a passing test asserts the loaded fixture's dimensions.
- **AC-4.** `AdvanceTicks`/`SetSpeed` commands produce **fake ticks**: issuing `AdvanceTicks(n)` advances the stub's `Tick` counter by exactly `n` and is reflected in subsequently emitted `Delta.Tick` values, without any real per-tick computation.
- **AC-5.** `Subscribe`/`Unsubscribe` work against `int.protocol`'s view-subscription contract: `Subscribe` returns a `SubscriptionID` and the stub begins pushing `Delta`s with monotonically increasing per-subscription `Seq` starting at 1 (carrying forward `int.protocol`'s AC-2); `Unsubscribe` stops further deltas for that subscription (carrying forward `int.protocol`'s AC-3).
- **AC-6.** Deltas/events are sourced from a scripted/recorded stream (an in-code slice or a loaded fixture file), not computed — the package documents and tests loading a canned sequence and replaying it against ticks.
- **AC-7.** Chaos knobs exist and are independently togglable: (a) a delayed-delta mode introducing artificial latency before a `Delta` is pushed, (b) a burst-delta mode pushing several `Delta`s in a tight cluster. Each has a passing test asserting the configured behaviour is observable (timing gap for (a), batch size/spacing for (b)).
- **AC-8.** The package doc states `StubEngine` is a **permanent fixture** (M0-ENG §2.1: "permanent fixtures... they never get deleted; they become the test estate") — not scaffolding to be removed once a real engine exists.

### Error handling

- **AC-9.** An unrecognized `Command.Kind` or a payload failing `Command.Validate()` returns a `CommandResult` carrying a registry-sourced `errs.E` (via `internal/foundation/errs`, per code.json's universal `* -> foundation.errors` edge) rather than panicking or returning a bare `error`.
- **AC-10.** Invalid chaos-knob configuration (e.g. a negative delay) fails loudly — a constructor/setter returns an error or the invalid value is rejected with a logged/registry-sourced error, never silently clamped without any signal.

### Determinism & safety

- **AC-11 (GR#21).** Given the same scripted delta stream and the same command sequence, two runs of `StubEngine` produce byte-identical `Delta`/`Event` content and ordering (only the chaos-knobs' artificial *timing*, if enabled, may vary — the payload bytes and Seq/Tick values must not).
- **AC-12 (SG-7 scoped; GR#21).** `grep -rn "time.Now" internal/engine/stub/*.go` (excluding `_test.go`) — every match must be confined to the chaos-delay implementation (scheduling artificial latency), never inside tick/delta content generation or fixture loading.

### Documentation

- **AC-13.** The package doc states the module key `harness.stub`, cites M0-ENG §2.1, and states the permanent-fixture status (AC-8).

## Out of scope

- Any real simulation logic — `StubEngine` computes nothing; all output is canned/scripted.
- `harness.replay` (`MOD-013`)'s own fixture-recording file format and tooling — a separate item, even though it may reuse `StubEngine`'s scripted-stream shape.
- Real Folkestone terrain data (OS Terrain 50 import) — Folkestone-64 is a handcrafted 64×64 fixture, not real geography (that's `MOD-017`, Sprint 3).

## Escalations

- None at draft time. This file is `status: draft-ahead` — refresh against `int.protocol`'s actually-frozen v1 schema (see `docs/design/protocol.md`, currently "awaiting freeze review") before the junior is dispatched; if the frozen schema's `Command`/`Delta` shapes differ from what's cited here, update AC-1/AC-2/AC-5 accordingly rather than treating a mismatch as a spec conflict.
