BOW code: MOD-006

# Acceptance criteria — foundation.data (MOD-006)

**BOW code:** MOD-006
**Spec refs:** §24 (Config Data Files, `docs/METROPOLIS-MASTER-v2.1.md` line 400 — the named file set at line 401: `data/consumption.json`, `modes.json`, `buildings.json`, `unlock_trees.json`, `naming_corpus.json`, `seasonal.json`, `external_world.json`, `policies.json`, `errors.json`); GR#15 (Validators Derive From Data, `CLAUDE.md` line 46); M0-ENG §3 (debug as a runtime feature switch, for the hot-reload trigger's scope, lines 855-857).
**Date:** 2026-08-09
**Status:** done (closed 2026-08-09)
**Package under test:** `internal/foundation/data/` (confirm via `node claude-bow.js show MOD-006` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/foundation/data/...`.

## User stories

- As **every balance-dependent engine module** (`engine.consumption`, `engine.roads`, `engine.unlocks`, `engine.season`, …), I need typed, validated loaders for the §24 config files, so I read a Go struct, not a raw `map[string]any` I have to re-validate myself.
- As **the M2 Balance harness**, I need every one of these files to be the actual balance surface — no coefficient hardcoded in Go — so a parameter sweep only ever needs to edit JSON, never recompile.
- As **a developer in debug mode**, I need config files to hot-reload without a restart, so I can tune a coefficient and see its effect in the same session.
- As **GR#15**, I need every validator that checks an "expected" count or value against these files to read that expectation *from the file*, never from a hardcoded constant in the validator's own source — so the loader package must make the correct (data-derived) pattern the easy one.

## Scope

Typed JSON loaders + schema validation + a debug-mode hot-reload hook for the §24 file set: `consumption.json`, `modes.json`, `buildings.json`, `unlock_trees.json`, `naming_corpus.json`, `seasonal.json`, `external_world.json`, `policies.json`, `errors.json` (the last already owned/loaded by `foundation.errors`, MOD-002 — this item's relationship to it is clarified in Out of scope).

## Acceptance criteria

### Functional

- **AC-1.** A typed loader function/type exists for each of the eight non-`errors.json` files named in §24 (`consumption.json`, `modes.json`, `buildings.json`, `unlock_trees.json`, `naming_corpus.json`, `seasonal.json`, `external_world.json`, `policies.json`) — `go doc ./internal/foundation/data` lists one exported loader per file (e.g. `LoadConsumption(path string) (Consumption, error)`), each returning a Go struct with named, typed fields (not a bare `map[string]interface{}`).
- **AC-2.** Each loaded struct's fields are validated against a documented schema at load time: required fields present, correct types, values within any documented range (e.g. a percentage field in [0,100], a non-negative capacity) — a passing test per file feeds a valid fixture and asserts successful load, and an invalid fixture (missing required field, wrong type, out-of-range value) and asserts a validation error naming the offending field.
- **AC-3.** A single entry point exists to load the **whole** config set at once (e.g. `LoadAll(dir string) (*Config, error)`), returning one struct aggregating all eight loaded files, for callers (like `engine.core`'s boot sequence) that want everything up front — a passing test loads a fixture directory containing all eight files and asserts every sub-struct is populated.
- **AC-4.** A hot-reload hook exists, gated to debug mode only (per M0-ENG §3's "debug is a runtime feature switch"): when enabled, a file-watch or explicit-reload-command mechanism reloads a changed config file and atomically swaps it into whatever holds the current config (without a process restart) — a passing test modifies a fixture file, triggers the reload path, and asserts subsequent reads reflect the new content while a concurrent reader mid-read never observes a half-written/torn struct (the swap is atomic, e.g. via a pointer swap under a mutex/atomic.Value, not in-place field mutation).
- **AC-5.** Hot-reload is a no-op (or explicitly disabled/erroring) outside debug mode — a passing test asserts that with the debug switch off, the reload trigger either does nothing or returns a clear "debug mode required" error, matching `feat.debugmode`'s gating pattern.
- **AC-6.** The package exposes a way for a validator elsewhere in the codebase to read an "expected value" from loaded config data rather than hardcoding it (GR#15) — concretely, this means the loaded structs' fields are the *only* sanctioned source for such expectations, and the package's own doc/README states this explicitly as the pattern other modules must follow (this AC is satisfied by the loader's public API shape plus the documentation requirement in AC-14, not by a runtime enforcement mechanism — GR#15 compliance in *other* modules is those modules' own acceptance criteria to prove, not this package's job to police).
- **AC-7.** Config file paths resolve relative to a documented root (e.g. `data/` at the repo root, or an overridable env var / constructor argument) so the loader works both from `go test` (working directory varies) and from the built binary — a passing test asserts loading succeeds when invoked from a non-repo-root working directory (simulating `go test`'s per-package cwd behaviour).

### Error handling

- **AC-8.** A missing config file produces a clear, registry-sourced error (`foundation.errors`) naming the expected path — not a panic, not a silently-empty struct.
- **AC-9.** Malformed JSON (syntax error) produces a clear, registry-sourced error including the underlying JSON parse error detail (line/offset if the stdlib provides it) — not a panic.
- **AC-10.** A schema-validation failure (AC-2's invalid-fixture case) is a registry-sourced error identifying the specific field and the rule it violated (e.g. "consumption.json: field waterLitresPerPersonPerDay must be >= 0, got -5"), not a generic "validation failed."
- **AC-11.** A hot-reload that fails (malformed replacement file) leaves the previously-loaded, still-valid config in place and reports the reload failure — it must never leave the running system with a half-applied or nil config. A passing test triggers a reload with a broken fixture and asserts the pre-reload config is still readable and correct afterward.

### Determinism & safety

- **AC-12 (GR#21).** Loading the same config file twice (no hot-reload in between) produces byte-for-byte-equal decoded structs (field order in a Go struct is fixed by definition, but for any map-typed sub-fields the loader must not introduce iteration-order variance into anything downstream that gets hashed/serialized) — a passing test loads twice and asserts deep-equality.
- **AC-13 (GR#21).** `go test ./internal/foundation/data/... -race -count=1` passes with no data race between a concurrent hot-reload swap and concurrent readers (the atomic-swap requirement from AC-4, proven under `-race`).
- **AC-14.** `grep -rn "time.Now" internal/foundation/data/*.go` (excluding `_test.go`) — any match must be confined to hot-reload's *file-watch polling interval* bookkeeping (legitimately wall-clock, since it's a real-time developer-convenience feature, not simulation logic) and must never affect the *content* of a loaded config struct.

### Documentation

- **AC-15.** The package doc states module key `foundation.data`, cites §24 and GR#15, lists all eight files this item loads, and states explicitly (per AC-6) that engine-module validators must read expected values from these loaded structs rather than hardcoding them — making this the canonical GR#15 reference other modules' documentation can point back to.

## Out of scope

- `errors.json`'s own loading — already owned and implemented by `foundation.errors` (`MOD-002`, Sprint 0, its own `registry.go`); this item does not duplicate that loader. If a unified `LoadAll` (AC-3) is desired to also surface the error registry for convenience, it may *reference* `foundation.errors`' existing loader rather than reimplementing it — confirm with the junior/Bill whether `LoadAll` should include it at all, since `foundation.errors` already loads it independently at its own package init/first-use.
- The actual balance content of any of the eight files (real coefficients, building catalogue entries, unlock trees) — those are populated by the engine modules that own each domain (`FEAT-010` for `buildings.json`'s full catalogue, `MOD-021` for consumption coefficients, etc.); this item only needs valid, schema-conformant **fixture** data to test against.
- A production-grade filesystem-event-based watcher (inotify/ReadDirectoryChangesW) — a simple poll-interval or explicit-reload-command mechanism satisfies AC-4; a real fs-event watcher is an acceptable but not required implementation choice.

## Escalations

- None at draft time. No spec/brief conflict found. One overlap noted (not a conflict) in Out of scope above: `errors.json` is named in §24's file list but already has its own dedicated loader in `foundation.errors` (Sprint 0, landed) — this item's `LoadAll` should decide deliberately whether to include it, and Bill/the junior should record that decision rather than silently duplicating or silently omitting it.
