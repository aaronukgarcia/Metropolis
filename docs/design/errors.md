# foundation.errors — error registry, correlation IDs, structured logging

Module key: `foundation.errors` · GUID `04773af2-1d23-4b2c-8a19-fd682052091f` ·
BoW code `MOD-002` · path `internal/foundation/errs/` · spec ref GR#1; GR#7;
M0-ENG §3 (log tail).

Sprint-0 freeze review page. Covers the API, the registry file, the
resolution/rotation/auto-log policies, and open questions.

Status: **awaiting freeze review.**

## Why this module exists

Two Golden Rules pin this down completely:

- **GR#7 — Registry-Sourced Errors.** Every error MUST be created from the
  error registry, no exceptions. `errs.New`/`errs.Wrap` are, per code.json's
  inbound contract for `foundation.errors`, *the only legal error
  constructor* in the codebase.
- **GR#1 — Aggressive Error Trapping.** Every error carries a type (its
  registry code), a correlation ID, is logged (never printed-and-lost), and
  has a selectable user-visible display string.

Every other module in the monorepo depends on this one (code.json: `* ->
foundation.errors`).

## The registry file — `data/errors.json`

```json
{
  "version": 1,
  "ranges": { ... },
  "codes": {
    "MET-F001": {
      "severity": "fatal",
      "module": "foundation.errors",
      "message": "error registry failed to load from {path}: {cause}",
      "remedy": "..."
    }
  }
}
```

Code format: `MET-<layer><NNN>` — one uppercase layer letter, three digits.
Layers: `F` foundation, `P` protocol, `E` engine, `U` ui, `T` tooling.

The foundation layer reserves sub-ranges per module so future modules don't
collide: F000-F009 self-errors (always available even if the registry
fails), F010-F099 `foundation.errors` itself, F100-F199 `foundation.registry`,
F200-F299 `foundation.det`, F300-F399 `foundation.serialize`, F400-F499
`foundation.solver`, F500-F599 `foundation.repo`.

`tools/plan/generate.js` currently raises its own inline `MET-T0xx` string
codes (see its header comment) and is **not** wired to this registry — it
has to run before code.json/data/errors.json can be trusted to exist.
Migrating it to read from `data/errors.json` is out of scope for this module
and tracked as future work in the `ranges.toolingNote` field of the file
itself, so the note travels with the data rather than living only here.

Seeded codes (13 total, ~10-15 as scoped): MET-F001/F002 (registry load /
validation failure), MET-F003 (unregistered code — the GR#7 enforcement
error), MET-F004 (missing correlation ID), MET-F010-F013 (log write
failure, rotation failure, can't-open-file, non-marshalable ctx), MET-F020
(malformed correlation ID). This is deliberately narrow: only the errors the
`errs` package itself can raise about its own operation. Every other
module's codes belong in their own F1xx/F2xx/... ranges when they're built.

## Package API — `internal/foundation/errs`

```go
func New(code, correlationID string, ctx map[string]any) *E
func Wrap(code, correlationID string, cause error, ctx map[string]any) *E

type E struct {
    Code, CorrelationID, Module, Msg string
    Ctx     map[string]any
    Time    time.Time
    Wrapped error
}

func (e *E) Error() string    // Display() + ": " + wrapped cause, if any
func (e *E) Display() string  // "[CODE] msg (correlation: id)" — GR#1 one-liner
func (e *E) Unwrap() error
func (e *E) Is(target error) bool // matches any *E with the same Code
```

`New`/`Wrap` never panic. If `code` isn't in the loaded registry — including
the case where the registry itself failed to load entirely — they return a
well-formed `MET-F003` error instead, with the originally-requested code and
constructor name folded into its message. This is the runtime enforcement of
GR#7: a programming mistake (typo'd code, forgot to seed a new code) becomes
a visible, loggable, still-typed error, never a crash and never a silent
no-op.

`(*E).Is` matches by `Code` alone, so callers can do
`errors.Is(err, &errs.E{Code: "MET-F003"})`-style sentinel comparisons
without needing a fully constructed, registry-backed error just to compare
against.

## Registry loading — `registry.go`

Loaded once per process via `sync.Once`. Resolution order:

1. `$METROPOLIS_ERRORS_PATH`, if set — used verbatim.
2. Walking upward from the running executable's directory.
3. Walking upward from the current working directory.

Step 3 is what makes `go test ./internal/foundation/errs/` work from the
package directory with no configuration: it walks up until it finds
`data/errors.json` at the repo root (or gives up at the filesystem root).
Both walks share one `findUpward` helper.

Validation on load: every code matches `^MET-[A-Z]\d{3}$`; every entry has
all four required fields (severity, module, message, remedy) non-empty; and
— the one validation step that needs more than a plain `map` unmarshal —
codes are unique. `encoding/json` silently resolves a duplicate JSON object
key to "last value wins," which would hide exactly the mistake this check
exists to catch, so `decodeCodes` walks the `codes` object with a streaming
`json.Decoder` token-by-token and flags any key seen twice.

## Correlation IDs — `correlation.go`

`NewCorrelationID()` mints a UUIDv4 via `crypto/rand`, formatted by hand
(stdlib only, no uuid package). `crypto/rand.Read` failing is effectively
unreachable on any platform Metropolis targets, but per GR#1 that path still
degrades to a deterministic-looking, still UUIDv4-shaped ID derived from
wall-clock nanoseconds rather than panicking or returning an error the
caller has to handle.

`ContextWithCorrelationID`/`CorrelationIDFromContext` propagate an ID
through a `context.Context` for code that already threads one (protocol
handlers). Code that doesn't use contexts just passes the string explicitly.

## Structured logging — `log.go`

NDJSON, one `Entry{ts, level, code, correlationId, module, msg, ctx}` object
per line, matching M0-ENG §3's `logs/engine.ndjson` / `logs/ui.ndjson`
shape exactly.

- `NewLogger(io.Writer)` — arbitrary writer, no rotation.
- `NewFileLogger(path, maxBytes, maxBackups)` — rotating file sink. On
  reaching `maxBytes`, renames `path.2→path.3`, `path.1→path.2`, `path→path.1`
  and reopens `path` fresh; `maxBackups` files (default 3, per the task
  brief's "keep N=3") are the max retained, oldest dropped.
- Every `Logger` has an injectable `Now func() time.Time` (`SetClock`),
  defaulting to `time.Now`. This is the concrete answer to M0-ENG §1.1: the
  engine tick path must never call the wall clock, so engine-owned loggers
  (`logs/engine.ndjson`) get the sim clock injected at boot; UI/tooling
  loggers keep the default.

### Auto-log-on-construct

`errs.New`/`errs.Wrap` call a package-level `logEntry` on every construction
— errors are stored the moment they're made, never printed-and-lost. The
sink is `errs.SetSink(*Logger)`, configured once at process boot (main
wires `logs/engine.ndjson` or `logs/ui.ndjson` in). With no sink configured,
**or whenever a configured sink's `Log` call returns an error**, entries
fall back to an in-memory 200-entry ring buffer, exposed via `errs.Recent()
[]Entry` — the primitive the F12 debug info panel's error tail (FEAT-007,
not yet built) will read.

This was a deliberate broadening of the brief's literal wording ("fallback
ring buffer... when no file sink is configured"): a *configured* sink whose
write is currently failing (disk full, permissions) is functionally the
same as "no sink" from GR#1's point of view — the error must not be lost —
so a failed write also falls back to the ring rather than being dropped.
See Open Questions below for the tension this creates with M0-ENG §3's
description of the panel tailing the *file*.

The package-wide `*E.Time` clock is a second, separate injectable
(`errs.SetClock`), also defaulting to `time.Now`, so engine bootstrap can
override both the error timestamps and its logger's timestamps from one
sim-clock source without the two ever drifting from wall-clock by different
amounts.

## Testing

`go test ./internal/foundation/errs/ -race -count=1` covers: registry load
success/failure (missing file, malformed JSON, bad code format, missing
field, duplicate code, caching-after-first-load, loading the real
`data/errors.json`); unregistered-code fallback for both `New` and `Wrap`,
including total registry unavailability; `errors.Is`/`As`/`Unwrap`;
`Display()` format; missing-correlation-ID handling; injectable clock;
NDJSON line shape (parsed back through `encoding/json`); multi-line NDJSON;
rotation triggering and the N=3 retention cap; ring-buffer fallback
(no-sink and failed-write cases) and its 200-entry cap; UUIDv4 format and
1000-sample uniqueness; and concurrent writers on both `Logger.Log` and
`errs.New` under `-race`.

## Open questions for the freeze review

1. **Ring buffer vs. file-tail for the F12 panel.** M0-ENG §3 describes the
   panel tailing `logs/engine.ndjson`/`logs/ui.ndjson` directly; this
   module's task brief asked for `Recent()` as "what the F12 tail will
   read." I implemented both a real rotating file sink *and* an in-memory
   ring that's authoritative only in the no-sink/failed-write case. FEAT-007
   needs to decide: does the panel read `Recent()` always (simpler, works
   even before any module wires a file sink, but bounded to 200 and reset on
   process restart), read the file always (matches spec text, survives
   restart, needs its own tailing/rotation-aware reader), or read `Recent()`
   with a periodic snapshot also fed from file rotation? I'd lean towards
   `Recent()` as the live tail source (cheap, no file-watching) with
   `metctl errors` (M0-ENG §3, not yet built) doing full-file offline review.
2. **MET-F0xx range exhaustion.** F010-F099 is reserved for this module but
   only 13 codes are seeded; is that range generous enough once `metctl
   errors` and any future registry-management commands need their own
   codes, or should reserved ranges be narrower/wider?
3. **Severity vocabulary.** I used `fatal`/`error`/`warn` freely in
   `data/errors.json` without the registry enforcing an enum — should
   `registry.go` validate `severity` against a fixed set (and should NDJSON
   `level` reuse exactly that set, or map through)?
4. **`tools/plan/generate.js` migration.** Confirmed out of scope here and
   flagged in the registry's own `ranges.toolingNote`, but no BoW item
   currently tracks it as future work — worth opening one so it doesn't get
   lost.
5. **ctx JSON-marshalability.** `errs.New`/`Wrap` accept `map[string]any`
   for `Ctx` with no validation at construction time; a non-marshalable
   value (chan, func) only surfaces as a `MET-F013`-shaped failure inside
   `Logger.Log`'s `json.Marshal` call, after the error has already been
   returned to the caller looking fine. Worth a lint rule or a construction-
   time check once real call sites exist to see what patterns show up.
