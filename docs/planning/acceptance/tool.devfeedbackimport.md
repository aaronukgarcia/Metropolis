BOW code: FEAT-124

# Acceptance criteria — tool.devfeedbackimport (FEAT-124)

**BOW code:** FEAT-124
**code.json:** GUID `973878d4-d1fc-46e7-a18f-664660db0a00`, key `tool.devfeedbackimport` (seq 971, M0 tooling)
**Spec refs:** FEAT-065 (`feat.devmode`) AC-DM10/AC-DM11 (the file-drop → BOW mechanism) and AC-DM17 (GR#1 error trapping); ASM-477 (per-record source-mkey attribution); BUG-126 (per-record kind); BUG-090 (never inline `--desc`); GR#3 (single source of truth — the schema is owned by `internal/engine/debug/feedback.go`, not this file)
**Date:** 2026-08-16
**Status:** retrospective — documenting the contract of already-committed code (`claude-devfeedback-import.js`), not forward-looking criteria for new work
**Package under test:** `claude-devfeedback-import.js` (repo root); exercised by `claude-devfeedback-import.test.js`. The game-side writer it consumes is `internal/engine/debug/feedback.go` (`SubmitFeedback` / `FeedbackRecord`, `FeedbackSchemaVersion = 1`), which never talks to the BOW/MariaDB directly — it only writes JSON records to a well-known inbox.
**Standard gates:** Node.js — `node --check claude-devfeedback-import.js`; SG-6 (no Co-Authored-By). No Go gates apply to this file (the Go writer it consumes has its own gates under FEAT-065).

## Scope

One deliverable, already committed: `claude-devfeedback-import.js` is the separate Node-side process that turns the game's file-drop feedback into real BOW items. It polls `data/devfeedback/inbox/` for `*.json` records, validates each against the `FeedbackRecord` schema, and for each well-formed record invokes `claude-bow.js add` via `spawnSync` with an **argv array** (`shell: false`, the project-wide convention) and `--desc-file` pointed at the record itself — never an inline `--desc` built from the free-text body (BUG-090). On success the record moves to `data/devfeedback/processed/` (never deleted); on failure it stays in `inbox/` with a `.error` sidecar. The importer is the single importer for every writer sharing the inbox (GR#3: parametrise, do not fork).

## Acceptance criteria

### Behaviour

- **AC-1. `runImport` scans the inbox for `*.json` only, deterministically, and summarises.** It reads `inboxDir` (default `data/devfeedback/inbox/`), filters to names ending `.json` — excluding `.tmp` partial writes and `.error` sidecars — sorts them for deterministic processing order, and returns `{ imported, malformed, failed, total }`. A **missing** inbox directory (nothing has ever been submitted) is a legitimate no-op returning a zeroed summary, not an error (AC-DM11).
- **AC-2. `validateRecord` enforces the `FeedbackRecord` shape strictly.** A record must be a JSON object with `schemaVersion === 1` (matching `feedback.go`'s `FeedbackSchemaVersion`), non-empty-string `timestamp`/`correlationId`/`body`, finite-number `tick`, and boolean `debugTouched`. `sourceMkey` and `kind` are **optional strings**: absent entirely is the expected shape for pre-ASM-477/pre-BUG-126 records (backward compatibility is load-bearing, not merely convenient), but present-with-the-wrong-type is still rejected as malformed. Valid JSON of the wrong shape is exactly as untrustworthy as non-JSON — the validation is deliberately strict.
- **AC-3. `importOne` validates, derives title/attribution/kind, then invokes `claude-bow.js add` via `spawnSync`.** The title is a single-line, whitespace-collapsed, 80-char-truncated derivation from the body (`Dev feedback: <body>`, never the raw unbounded body). The invocation is `[bowScript, 'add', <kind>, <title>, '--desc-file', <recordPath>, '--code-path', <codePath>, '--codejson', <codejson>]` run as `spawnSync(process.execPath, args, { cwd, encoding: 'utf8', timeout: 30000 })` — an argv array with `shell: false`, so a body containing a backtick, `$(...)`, an embedded quote, or a newline can never reach shell-interpreted parsing (BUG-090). `--desc-file` carries the record's own path, never an inline `--desc` (BUG-090, load-bearing).
- **AC-4. Success moves the record into `processed/` and clears any stale sidecar.** The move is a `rename` (never a delete — auditability, GR#1) into `processedDir` after `mkdirSync(..., { recursive: true })`; a prior failed attempt's `.error` sidecar is then removed. `processedDir` defaults to `data/devfeedback/processed/`.
- **AC-5. Failure leaves the record re-attemptable, never silently lost.** On a read failure, a validation failure, a non-zero/error `claude-bow.js` exit, or a post-success move failure, the record stays in `inbox/` and a `.error` sidecar is written next to it naming the reason. Re-running the script re-attempts anything still in `inbox/` — a stale `.error` sidecar does not suppress a retry. A malformed record fails validation identically every time (so it never reaches the BOW call and AC-DM11's "no duplicate BOW items" holds trivially); a transient `claude-bow.js` failure gets a real chance to self-heal on the next run.
- **AC-6. Attribution is per-record from `sourceMkey` (ASM-477).** `deriveAttribution` maps a present, non-empty `sourceMkey` to `--codejson <sourceMkey>` and a `--code-path` from `SOURCE_CODE_PATHS` (known writers) or a generically-derived `<sourceMkey> (feedback submission)` (unknown future writers — so a new writer attributes correctly with zero changes here). A missing/empty `sourceMkey` falls back to `DEFAULT_SOURCE_MKEY` (`feat.devmode`) and the default code path, preserving FEAT-065's original behaviour exactly.
- **AC-7. Kind is per-record (BUG-126).** `deriveKind` maps a `kind` in `VALID_KINDS` (`bug`, `finding`, `assumption`) to the `claude-bow.js add <kind>` verb; anything missing/empty/unrecognised falls back to `DEFAULT_KIND` (`bug`), exactly the pre-BUG-126 hardcoded behaviour. Because `claude-bow.js add finding` requires `--class` from a closed list the record has no basis to self-classify against, a `finding`-kind record additionally passes `--class other` (`FINDING_DEFAULT_CLASS`).
- **AC-8. The exit-code contract.** The process exits 0 even when individual records were malformed or failed (those are reported, not fatal to the run); it exits 1 only on an unexpected crash in the script itself (e.g. the inbox exists but is unreadable for a reason other than "doesn't exist yet").

### Fail-open posture

- **AC-9. The importer is a consumer-side companion, not a gate.** No record-level failure is fatal to the run — the design guarantee is "a submission is never silently lost" (every failure leaves the record in `inbox/` with a `.error` sidecar), not "every submission becomes a BOW item in one pass". This is a deliberate, recoverability-first posture: a transient DB outage self-heals on the next run instead of requiring a human to notice and manually re-trigger.
- **AC-10. Every failure path emits structured, correlation-ID-bearing errors (AC-DM17 / GR#1).** `logError(code, correlationId, message, extra)` writes one JSON line to stderr with `level`/`code`/`correlationId`/`message`/`at` — never a bare `console.error(err)` and never a swallowed `catch{}`. A distinct `correlationId` (`crypto.randomUUID()`) is minted per `importOne` call, and each failure class has a stable code (`devfeedback-read-failed`, `devfeedback-malformed`, `devfeedback-bow-add-failed`, `devfeedback-move-failed`, `devfeedback-sidecar-write-failed`, `devfeedback-fatal`). Even a secondary failure (writing the `.error` sidecar itself) is logged, not swallowed.

### Tests

- **AC-11. `claude-devfeedback-import.test.js` (run: `node --test claude-devfeedback-import.test.js`) proves the behaviour ACs.** It covers: `validateRecord` accept/reject (invalid JSON, wrong `schemaVersion`, missing `body`, non-numeric `tick`, non-string `sourceMkey`/`kind`); `deriveTitle` truncation and no-raw-newline; AC-DM10 well-formed → exactly one `add` invocation with `--desc-file` (and never `--desc`) plus move to `processed/`; AC-DM10 malformed → stays in `inbox/` + `.error` sidecar + zero BOW calls; a mixed one-good/one-bad fixture; AC-DM11 re-run-no-duplicate (exactly one total BOW call across two runs) and empty/nonexistent-inbox no-op; a `claude-bow.js` non-zero exit leaving the record in place with a sidecar; a transient-failure-then-success recovery that clears the stale sidecar; ASM-477 attribution (`feat.metricsdash` → `--codejson feat.metricsdash`, explicit `feat.devmode`, no-field fallback); BUG-126 kind (`finding`/`assumption` verbs, `--class`, no-field fallback to `bug`, unknown-kind fallback); and multiple submissions never interleaving.
- **AC-12. Tests never touch the real metro DB or the real `claude-bow.js`.** The module guards side effects behind `require.main === module` (require is side-effect-free); every test wires its own tmp inbox/processed dirs and a stubbed `spawnSyncFn` recording each invocation, so the suite runs with no MariaDB instance reachable. The "exactly one `claude-bow.js` invocation" assertions are what make AC-DM10's "ran without throwing" trap rejectable — a test that only asserts "the script ran" proves nothing.

### Determinism

- **AC-13. Processing order is deterministic.** `runImport` sorts the `*.json` entries before processing, so given the same inbox contents the same sequence of BOW calls results; the only non-determinism is the per-call `correlationId`, which is a logging identifier, not a decision input.

## Out of scope

- **Mechanically preventing the post-success-rename duplicate** (see ASM-766 below): the importer marks that failure mode distinctly in its `.error` sidecar but does not yet prevent a re-run from double-filing. A real fix is a two-phase mark-then-move or a pre-add correlationId lookup.
- The game-side writer (`internal/engine/debug/feedback.go`) and its schema ownership — this file validates against, does not own, `FeedbackRecord`.
- `feat.metricsdash`'s `LogNote` writer (`internal/harness/metricsdash/feedback.go`) — it shares this importer per ASM-477, but is not this file's scope.
- Any HTTP/exec/DB path from the running game — the architecture decision is explicit that the game never talks to the BOW stack; this file is the only bridge.

## Assumptions

Logged via `node claude-bow.js add assumption` (plus one carried over and cited):

- **ASM-477 (P2, existing — Bill's ruling)** — the importer derives attribution from the submitting tool's `sourceMkey` rather than hardcoding `feat.devmode`; this file is the fix. Cited here because the importer's `deriveAttribution` is the direct implementation of that ruling.
- **ASM-757 (P2)** — `SCHEMA_VERSION = 1` is hardcoded in the importer and must stay in lockstep with `feedback.go`'s `FeedbackSchemaVersion` (they match today, but the lockstep is by convention, not enforcement); a `feedback.go` schema bump that forgets to bump the importer in the same commit makes the importer silently reject every new record as malformed (fail-safe, never corrupt, but a coordination trap).
- **ASM-766 (P2)** — the post-success-rename failure can double-file a BOW item on a later run; documented in the header, not solved.

## Escalations

- **ASM-766's duplicate risk** — escalated to Bill/Aaron as a possible future follow-up: the header itself flags it as rare (a rename failing immediately after a successful adjacent write), but if feedback volume rises, the two-phase commit (mark-then-move) or a pre-add correlationId lookup becomes worth building. Not a blocker for documenting this file's committed contract.
- **ASM-757's schema lockstep** — worth a cross-file note in `feedback.go` (or a shared constant import) so the two `= 1` values cannot drift silently; a single-source-of-truth import would close it, but the current convention of a matching comment on each side is acceptable for now.
