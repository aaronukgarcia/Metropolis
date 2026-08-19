BOW code: MOD-008

> **See also:** `BUG-020.md` (StubEngine.Run silent-exit bug) carries its
> own acceptance criteria for this package — see README.md's
> cross-reference convention.

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

- **AC-9 (GR#7).** An unrecognized `Command.Kind` returns a `CommandResult` carrying a registry-sourced `errs.E` with code `MET-P090` (`codeUnknownKind`); a payload failing `Command.Validate()` returns one with code `MET-P091` (`codeInvalidPayload`) — either way via `internal/foundation/errs`, never a panic and never a bare `error`. Check: `grep -n "codeUnknownKind\s*=\|codeInvalidPayload\s*=" internal/engine/stub/codes.go` shows `MET-P090`/`MET-P091`; a passing test (`grep -rn "func Test.*[Uu]nknownKind\|func Test.*[Ii]nvalidPayload" internal/engine/stub/*_test.go`) sends each malformed command and asserts the returned `CommandResult`'s error carries the matching registry code AND that no state change (e.g. no tick advance, no subscription created) resulted from the rejected command — not merely that a matching-named test function exists and passes.
- **AC-10 (GR#7-adjacent — sentinel, not a registry code).** Invalid chaos-knob configuration (e.g. a negative delay) is rejected by `ChaosConfig.Validate`/`validateChaos`, and `WithChaos` propagates that failure as an error wrapping the package sentinel `ErrInvalidChaosConfig` (`errors.Is`-checkable), never silently clamped or defaulted — `StubEngine` construction fails outright rather than starting with a silently-corrected chaos config. Check: `grep -n "ErrInvalidChaosConfig" internal/engine/stub/chaos.go` shows the sentinel; a passing test (`grep -rn "func Test.*[Cc]haos.*[Ii]nvalid\|func Test.*[Ii]nvalid.*[Cc]haos" internal/engine/stub/*_test.go`) constructs a `StubEngine` with an invalid `ChaosConfig` (e.g. negative delay) and asserts both that construction returns an error satisfying `errors.Is(err, ErrInvalidChaosConfig)` AND that the resulting engine is not usable with the invalid value silently substituted for a valid default — not merely that a matching-named test function exists and passes.

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

## Spec-fold amendments (FEAT-084 SF wave, 2026-08-18)

> Substantive AC amendments folded from the FEAT-084 ASM disposition (class SF).

### ASM-089 — Run's caller contract is cancel-then-join-then-close (amends the Run AC)
`Run`'s caller contract is **cancel-then-join-then-close**; `cancel(); Close()` without a join is a caller error, not something `Run`'s logic can reliably distinguish — the race makes it fundamentally impossible (`ctx.Done()` and `Commands()` can both be observably ready by the time `Run`'s select runs, with no ordering guarantee that cancellation is seen first). The fix states the join requirement explicitly in `Run`'s doc comment, matching `cmd/metropolis/boot.go`'s existing `cancel(); wg.Wait(); Close()` ordering; a grace window was rejected because it would reintroduce the "silent death is invisible" risk BUG-020 exists to close. Check: `Run`'s doc comment states the cancel-then-join-then-close contract, and the premature-close alarm is guaranteed not to fire only for callers that join.

### ASM-066 — StubEngine.World() is deliberately unguarded (no checkNotCopied)
`World()` (`internal/engine/stub/engine.go` line ~163 comment, method body confirms no `checkNotCopied` call) intentionally omits the copy-guard because the `*World` it returns is a plain, never-reassigned field set once at construction (`GenerateFolkestone64`) with no shared mutable state to protect — analogous to `engine.core`'s `Registry()`/`WorldSeed()`/`PoolSize()`, not to the guarded `Results()`/`Events()`/`Deltas()`/`Commands()` accessors. Falsifier (live constraint, re-check before closing): if any future code path mutates a `*World` after construction, or a caller mutates the returned `*World`, this decision is wrong and `World()` needs the same guard as the seven `mu.Lock()` sites.

### ASM-067 — advanceSubscriptionScriptLocked / emitDeltaLocked are deliberately unguarded (single already-checked call site)
Both `mu`-already-held helpers (`internal/engine/stub/engine.go` lines ~639, ~657) omit their own `checkNotCopied` call because their only call sites (`handleAdvanceTicks`, `handleSubscribe`) already ran both a pre-lock and post-lock identity check before calling them (confirmed: no `checkNotCopied` invocation inside either helper). Falsifier (live constraint): if a future call path reaches either helper without going through `handleAdvanceTicks`/`handleSubscribe`'s existing checks (new handler, promoted test helper, `chaos.go`'s delayed-delta goroutine calling back in directly), both helpers need their own guard.
