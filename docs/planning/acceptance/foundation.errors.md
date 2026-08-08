BOW code: MOD-002

# Acceptance criteria — foundation.errors (MOD-002)

**BOW code:** MOD-002
**Spec refs:** GR#1 (aggressive error trapping); GR#7 (registry-sourced errors, no exceptions); M0-ENG §3 (F12 Info Panel & error log tail, `docs/METROPOLIS-MASTER-v2.1.md` lines 853-865); code.json `foundation.errors` entry (inbound pattern `errs.New(code, correlationId, ctx) — the only legal error constructor`); code.json `conventions.errorHandling`; `data/errors.json` header block (`ranges`/`codes`).
**Date:** 2026-08-08
**Status:** active
**Package under test:** `internal/foundation/errs/` (path from `node claude-bow.js show MOD-002`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/foundation/errs/...`.

## Scope

The error registry, correlation-ID propagation, and structured NDJSON logging foundation every other Metropolis module builds its error handling on.

## Acceptance criteria

### Functional

- **AC-1.** `data/errors.json` exists, parses as valid JSON (`go test ./internal/foundation/errs/... -race -count=1` includes registry loading and passes), and its `ranges.format` field states the pattern `MET-<layer><NNN>`.
- **AC-2.** Every key under `data/errors.json`'s `codes` object matches the regex `^MET-[A-Z][0-9]{3}$`. Check: `grep -Eo '"MET-[A-Za-z0-9]+":' data/errors.json | grep -Ev '^"MET-[A-Z][0-9]{3}":$'` outputs nothing.
- **AC-3.** `go doc ./internal/foundation/errs` lists exactly two exported functions that construct and return `*E`: `New(code, correlationID string, ctx map[string]any) *E` and `Wrap(code, correlationID string, cause error, ctx map[string]any) *E`. No other exported constructor of `*E` exists in the package (`grep -n "^func New\|^func Wrap" internal/foundation/errs/errs.go` shows exactly these two signatures; no third `func` in the package returns `*E` other than internal, unexported helpers).
- **AC-4.** Calling `errs.New` with a code absent from `data/errors.json` returns a non-nil `*E` with `Code == "MET-F003"` and does not panic. Check: `go test ./internal/foundation/errs/... -race -count=1 -v` passes and the test source contains an assertion against `"MET-F003"` (`grep -rn "MET-F003" internal/foundation/errs/*_test.go` finds at least one match).
- **AC-5.** Calling `errs.New`/`errs.Wrap` with an empty `correlationID` logs a `MET-F004` warning and the returned `*E.CorrelationID` is the visible placeholder, never a silent empty string. Check: `grep -rn "MET-F004" internal/foundation/errs/*_test.go` finds at least one match, and the referenced test passes.
- **AC-6.** Every constructed `*E` produces a structured NDJSON log entry with exactly the fields `{ts, level, code, correlationId, module, msg, ctx}` (per code.json `conventions.errorHandling.logging`). Check: `grep -n '"ts"\|"level"\|"code"\|"correlationId"\|"module"\|"msg"\|"ctx"' internal/foundation/errs/log.go` (or the file defining the log entry's JSON tags) shows all seven field names.
- **AC-7.** Logs are written to `logs/engine.ndjson` and `logs/ui.ndjson` with rotation. Check: `grep -rn "engine.ndjson\|ui.ndjson" internal/foundation/errs/*.go` finds both path literals (or a configurable-path mechanism that defaults to them), and `grep -rn "rotat" internal/foundation/errs/*.go` finds a rotation implementation/reference.
- **AC-8.** The package exposes a way to retrieve at least the last 50 warn/error entries in memory (M0-ENG §3: "last 50 warn/error entries"), independent of whether the configured sink write succeeded. Check: `go test ./internal/foundation/errs/... -race -count=1 -v` includes a passing test that inserts >50 entries and asserts the retrieval function (e.g. `Recent`) returns at least the last 50 in order.
- **AC-9.** `SetClock` (or equivalent) allows overriding the package's time source, and the default is `time.Now`. Check: `grep -n "SetClock\|func now()" internal/foundation/errs/errs.go` shows both the setter and the wrapper used internally instead of calling `time.Now()` directly elsewhere.

### Error handling

- **AC-10.** A registry that fails to load (missing file, invalid JSON, or a validation failure such as a duplicate/malformed code) produces `MET-F001` or `MET-F002` rather than a panic or an unhandled error propagating out of `New`/`Wrap`. Check: `grep -rn "MET-F001\|MET-F002" internal/foundation/errs/*_test.go` finds coverage for both, and `go test ./internal/foundation/errs/... -race -count=1` passes.
- **AC-11.** A failed NDJSON write (e.g. `logs/` unwritable) does not lose the error entry: it remains retrievable via the in-memory buffer (AC-8's retrieval function). Check: `grep -rn "MET-F010" internal/foundation/errs/*_test.go` finds a test covering a failed write, and it passes.
- **AC-12.** `errs.New`/`errs.Wrap` never panic for any combination of empty code, empty correlationID, or nil ctx. Check: `go test ./internal/foundation/errs/... -race -count=1` passes with these inputs exercised (grep test source for a nil-`ctx` case: `grep -n "ctx: nil\|nil,\s*// ctx\|New(.*nil)" internal/foundation/errs/*_test.go`).

### Determinism & safety

- **AC-13 (SG-7 scoped; GR#21).** Check: `grep -rn "time\.Now" internal/foundation/errs/*.go | grep -v _test.go | grep -vE ':[0-9]+:[[:space:]]*//'` (the trailing filter drops comment-only lines — doc prose like "Defaults to time.Now." is not a code call and must not be counted) — this must return matches from **exactly three** legitimate injectable-clock default sites, and no others:
  (a) `errs.go` — the package-wide `clock` variable's default assignment (`clock = time.Now` inside the `var ( clockMu ...; clock = time.Now )` block);
  (b) `log.go` — `NewLogger`'s `now: time.Now` struct-literal field default;
  (c) `log.go` — `NewFileLogger`'s `now: time.Now` struct-literal field default.
  Every one of these three is a *default value for an overridable field/var* (`SetClock` on the package and on `*Logger` respectively let callers replace it) — that is what makes it a legitimate injectable-clock site rather than a wall-clock call on the tick path. Any code-level match outside these three — in particular `correlation.go`, which must read time exclusively through the package-level `now()` helper (see `degradedCorrelationID`), never `time.Now()` directly — is a FAIL. Expected result today (verified 2026-08-08 against the current tree): the filtered command prints exactly these 3 lines —
  ```
  internal/foundation/errs/errs.go:16:	clock   = time.Now
  internal/foundation/errs/log.go:46:	return &Logger{w: w, now: time.Now}
  internal/foundation/errs/log.go:69:		now:        time.Now,
  ```
  (exact line numbers may drift as the file changes; match on file+content, not line number) — zero lines from `correlation.go`, `registry.go`, or any other file in the package. A 4th match, or any match attributed to a file other than `errs.go`/`log.go`, fails this AC — including doc-comment prose that gets miscounted because the filter above wasn't applied; the Tester must run the filtered command, not the bare `grep -rn "time.Now"` from earlier drafts of this criterion.
- **AC-14 (GR#21).** Concurrent construction is safe: `go test ./internal/foundation/errs/... -race -count=1` reports no data race when multiple goroutines call `New`/`Wrap`/`SetClock` concurrently (a concurrency test exists — `grep -n "go func()" internal/foundation/errs/*_test.go` finds at least one goroutine-based test). A data race here is itself a determinism hazard (GR#21) — treat any `-race` failure as an auto-P0, not a routine bug.
- **AC-15.** The registry is loaded and validated once and cached, not re-read from disk on every `New`/`Wrap` call. Check: `grep -n "sync.Once\|sync.RWMutex" internal/foundation/errs/registry.go` shows a caching/synchronization mechanism guarding the load.

### Documentation

- **AC-16.** `internal/foundation/errs/doc.go`'s package comment states the module key and spec refs. Check: `grep -n "foundation.errors" internal/foundation/errs/doc.go` and `grep -n "GR#1" internal/foundation/errs/doc.go` and `grep -n "GR#7" internal/foundation/errs/doc.go` all match.
- **AC-17.** `data/errors.json`'s `ranges` block documents the `MET-<layer><NNN>` format and the layer-letter table (F/P/E/U/T). Check: `grep -n '"F":\|"P":\|"E":\|"U":\|"T":' data/errors.json` finds all five layer entries.

## Out of scope

- F12 Info Panel UI rendering of the error tail — that is `FEAT-007` (depends on this item), a separate UI-layer build.
- `metctl errors` offline review subcommand — tooling built against this package, not part of it.
- Wiring the engine's simulation clock into `SetClock` at boot — that is `engine.core`'s responsibility when it exists; this item only needs to provide the injectable seam.
- Blob/cloud log sync (GDD §15 cloud path) — out of v1 scope entirely.
- Any actual error codes beyond the `foundation.errors` reserved range (`MET-F000`-`MET-F099`) being fully populated for modules that don't exist yet.

## Escalations

- **Resolved.** The BA's earlier escalation questioned whether GR#20/GR#21 exist, since they weren't visible in the copy of `CLAUDE.md` in context. Bill confirmed they exist (`CLAUDE.md` lines 51-52; enacted commit 26d6dbc; detail in `docs/golden-rules-detail.md` "Metropolis Amendments"): GR#20 Contract-First, Stub-Forever; GR#21 Red Determinism Gate Stops the Line. AC-13/AC-14 above now cite GR#21 where they enforce determinism. GR#20 (interface-only consumption / stub-forever) is not independently cited in this file beyond the universal `* -> foundation.errors` edge in code.json, since `foundation.errors` has no consumed-interface or Stub-implementation surface of its own — it is the thing every other module's GR#20 contract consumes, not a GR#20 consumer itself. No further action needed. (Same resolution applies to `int.protocol.md`, `int.serializer.md`, `int.solver.md`.)
