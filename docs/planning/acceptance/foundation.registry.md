BOW code: MOD-005

# Acceptance criteria — foundation.registry (MOD-005)

**BOW code:** MOD-005
**Spec refs:** M0-ENG §2 (Harness strategy — "Module stubbing inside the real engine", `docs/METROPOLIS-MASTER-v2.1.md` line 851); M0-ENG §3 (Debug mode & the Info Panel — module registry rows, lines 860, 865, esp. "the module registry is the same mechanism the engine uses to boot — the panel is a view of it, not a parallel system. One registry, two consumers.").
**Date:** 2026-08-09
**Status:** active
**Package under test:** `internal/foundation/registry/` (confirm via `node claude-bow.js show MOD-005` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/foundation/registry/...`.

## User stories

- As **`engine.core`**, I need to boot with any mix of real and stub modules, so the walking skeleton runs end-to-end from month one of development and modules go real one at a time in BOW seq order (M0-ENG §2).
- As **`ui.screen.debug`** (F12), I need to read the same registry the engine boots from — not a parallel copy — so the info panel's module rows can never drift from what's actually running.
- As **a developer using F12's guarded toggles**, I need each registry entry to declare whether it `CanToggle` safely at runtime, so the UI never offers a toggle that would corrupt a module mid-tick.
- As **`engine.core`'s phase pipeline**, I need each module's last-tick cost recorded in the registry, so F12's per-phase timing strip and the perf CI harness have a single source of per-module cost data.

## Scope

The module registry: registration API (name, semver, spec_ref/feature-flag source), mandatory `Stub` implementation contract, real/stub/off status, health (ok/degraded/error), last-tick-cost recording, `CanToggle` declaration and the guarded toggle operation itself, and the boot mechanism that lets `engine.core` start with any real/stub mix.

## Acceptance criteria

### Functional

- **AC-1.** A `Registry` type exists with a `Register(entry ModuleEntry)` (or equivalent) method taking at minimum: module key (matching the `code.json`/BOW `key` convention, e.g. `engine.traffic`), semver string, initial status (`real`/`stub`/`off`), a `CanToggle bool`, and a feature-flag source identifier. Check: `go doc ./internal/foundation/registry` shows `Register` and the entry struct's fields.
- **AC-2.** Every registered module **must** supply a `Stub` implementation satisfying a common interface (M0-ENG §2: "every simulation module registers behind an interface with a mandatory Stub implementation") — `Register` (or a companion `RegisterWithStub`) rejects registration if no stub is supplied, either at compile time (the interface requires it structurally) or at runtime with a registry-sourced error. A passing test asserts attempting to register a module with a nil/missing stub fails.
- **AC-3.** The registry supports booting with **any mix** of real and stub modules: a passing test constructs a registry with some modules registered `status: real` and others `status: stub`, boots it, and asserts both groups are queryable and neither blocks the other from being present.
- **AC-4.** Each registry entry exposes: name, semver, status (real/stub/off), health (ok/degraded/error), last-tick-cost (a settable/updatable numeric field, µs), and feature-flag source — `go doc ./internal/foundation/registry ModuleEntry` (or equivalent) lists all six fields, matching M0-ENG §3's F12 row spec verbatim.
- **AC-5.** A method to update a module's last-tick cost exists and is callable once per module per tick by the orchestrator (`engine.core`) without requiring the registry to know anything about phases itself — a passing test calls the update method N times with different values and asserts the entry reflects the most recent value (not an accumulating sum, unless a separate rolling-window API is also provided for the 60-tick sparkline case, in which case both are tested).
- **AC-6.** A method to update a module's health (ok/degraded/error) exists, independent of status — a passing test asserts a module can be `status: real` and `health: degraded` simultaneously (the two are orthogonal fields).
- **AC-7.** `CanToggle` is a declared, per-module boolean set at registration time (not inferred) — a passing test asserts a module registered with `CanToggle: false` cannot be toggled via the toggle API (returns a registry-sourced error), while one registered `CanToggle: true` can.
- **AC-8.** A guarded toggle operation (`SetStatus`/`Toggle`) changes a module's status (real ↔ stub ↔ off) only when `CanToggle` is true, and emits a world-event / callback hook (so `ui.screen.debug`'s AC-5 "Crime module → STUB" ticker event has something to subscribe to) — a passing test asserts the toggle both changes state and fires the hook exactly once.
- **AC-9.** The registry is queryable as a whole (list all entries) and by key (get one entry) — `go doc ./internal/foundation/registry` shows both a `List() []ModuleEntry` (or iterator) and a `Get(key string) (ModuleEntry, bool)`-shaped method, matching what both `engine.core`'s boot loop and `ui.screen.debug`'s F12 rendering need from "one registry, two consumers."
- **AC-10.** Registry query results are returned in a **stable, deterministic order** (e.g. sorted by key), not Go map iteration order — a passing test calls `List()` multiple times and asserts identical ordering every time.

### Error handling

- **AC-11.** Registering a duplicate module key returns a registry-sourced error rather than silently overwriting the earlier registration or panicking — a passing test asserts this.
- **AC-12.** Querying (`Get`) an unregistered key returns `(zero-value, false)` (Go's standard ok-idiom) rather than panicking — a passing test asserts this, distinguishing it from a toggle/status-update call on an unregistered key, which should return a registry-sourced error (since that's an active mutation attempt, not a lookup).
- **AC-13.** A toggle attempt on a `CanToggle: false` module returns a clear registry-sourced error naming the module and stating it cannot be toggled — not a silent no-op.

### Determinism & safety

- **AC-14 (GR#21).** `go test ./internal/foundation/registry/... -race -count=1` passes with no data race when multiple goroutines concurrently update different modules' last-tick-cost/health fields while another goroutine calls `List()` (the F12 render path reading concurrently with `engine.core`'s tick-path writes — exactly the "one registry, two consumers" concurrent-access pattern this module exists to make safe).
- **AC-15 (GR#21).** `grep -rn "time.Now" internal/foundation/registry/*.go` (excluding `_test.go`) — any match must be confined to a health/staleness *display* concern if present at all (e.g. "last updated" wall-clock bookkeeping for F12's own convenience), never affecting boot order, status transitions, or any value read by `engine.core`'s boot/tick logic.
- **AC-16.** Boot order (the sequence in which registered modules are exposed to `engine.core`'s boot loop) is deterministic given the same set of `Register` calls in the same order — carrying forward AC-10's stable-ordering guarantee into the boot path specifically, since nondeterministic boot order could produce nondeterministic phase-registration side effects downstream.

### Documentation

- **AC-17.** The package doc states module key `foundation.registry`, cites M0-ENG §2/§3, and states explicitly "one registry, two consumers" (the engine boot path and the F12 info panel) so a future contributor doesn't fork a second copy for a new consumer instead of reading this one.

## Out of scope

- `ui.screen.debug`'s actual F12 rendering of registry rows — `FEAT-007`, a separate item; this package only needs to be queryable in the shape that item's tests already assume (per this file's AC-4/AC-9, cross-checked against `ui.screen.debug.md`'s AC-3/AC-4).
- Any specific engine module's real or stub implementation content — this package defines the registration/status/health *contract*, not any module's actual simulation logic.
- Feature-flag *evaluation* logic (reading env vars/config to decide initial status) — this package stores and exposes the feature-flag *source* string per entry; resolving that source into an actual boot decision may live in `engine.core`'s boot sequencing instead, confirm at dispatch if ambiguous.

## Escalations

- None at draft time. No spec/brief conflict found. Minor note for Bill: M0-ENG §3's rolling "phase timing strip: per-phase µs sparkline across last 60 ticks" could be read as requiring this registry to retain a 60-tick rolling window itself (AC-5 above), or as requiring only the latest value with the 60-tick history owned by `ui.screen.debug`/`engine.core` instead. AC-5 is written to accept either design (single-latest-value API, with an optional rolling-window API tested if the junior implements one) rather than mandating one — flagging so Bill can confirm which layer should own the history if a strict interpretation is preferred.
- **ASM-874 (confirm-and-close).** Copy-guard + defensive-copy wrappers live in foundation/registry as a reusable generic (F100-F106 taken, F107+ free), not a new foundation/copyguard package.
- **ASM-1019 (confirm-and-close).** MOD-079 CloneMap/CloneSlice are documented **shallow**, so nested reference-value aliasing after a clone is expected and not a defect; the exported `Bind` re-arm after a byte-copy is out of the accidental-copy threat model and is recorded as an observation only, not a rejection.

- **ASM-882 (CC fold).** Copy-guard/defensive-copy wrappers home in foundation.registry (Proposal A1); a dedicated package is escalated, not decided. No existing hand-rolled checkNotCopied site is migrated - the wrapper is proven via fixtures only. If Bill wanted a real migration or a different package, that is a mechanical rename or under-delivery.

## Spec-fold amendments (FEAT-084 SF wave, 2026-08-18)

> Substantive AC amendments folded from the FEAT-084 ASM disposition (class SF).

### ASM-069 — SetStatus copy-guard lives in setStatusLocked (amends the copy-guard AC)
`SetStatus` (exported) never touches `r.mu` directly — it delegates entirely to `setStatusLocked` (unexported), the actual, sole `r.mu.Lock()` site. The pre-lock/post-lock `checkNotCopied` guard belongs on `setStatusLocked` rather than being duplicated on `SetStatus`, which has no lock-touching work of its own to protect. If a future refactor adds pre-lock work directly inside `SetStatus` before it calls `setStatusLocked`, that new code would run unguarded on a copy — add a guard directly in `SetStatus` at that point. Check: the guard sits at the single `mu.Lock()` site, and a test confirms a copied `Registry` is rejected there.
