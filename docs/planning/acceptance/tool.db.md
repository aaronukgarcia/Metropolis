BOW code: FEAT-123

# Acceptance criteria — tool.db (FEAT-123)

**BOW code:** FEAT-123
**code.json:** GUID `4c12a143-9345-4ae9-990d-ac16ee3e2baa`, key `tool.db` (seq 970, M0 tooling)
**Spec refs:** M0-ENG §4 (BOW); GR#3 (single source of truth — this file removes six hand-rolled copies of the same connection block)
**Date:** 2026-08-16
**Status:** retrospective — documenting the contract of already-committed code (`claude-db.js`), not forward-looking criteria for new work
**Package under test:** `claude-db.js` (repo root). There is **no dedicated `claude-db.test.js`**; behaviour is covered indirectly through caller suites (see AC-8, ASM-756).
**Standard gates:** Node.js — `node --check claude-db.js`; SG-6 (no Co-Authored-By). No Go gates apply.

## Scope

`claude-db.js` is the shared metro MariaDB connection helper (BUG-203, GR#3). Before it existed, six non-test `claude-*.js` scripts each hand-rolled their own `mysql2/promise` `createConnection` block, in two failure-semantics classes. This file collapses those into one module exposing **three functions** in two deliberate flavours, so a future caller does not re-derive connection parameters or failure posture from scratch:

- `connectionOptions(overrides)` — builds the parameter object.
- `connect(overrides)` — **throw-on-failure** flavour, for the PreToolUse/PostToolUse guards (caller decides fail-open vs fail-closed).
- `connectCLI(label, overrides)` — **fail-stop** flavour, for the interactive CLIs.

The six callers (verified in source): `claude-bow.js` and `claude-sync.js` use `connectCLI`; `claude-bow-autoref.js`, `claude-bow-ref-check.js`, `claude-destructive-guard.js`, and `claude-dispatch-guard.js` use `connect`.

## Acceptance criteria

### Behaviour

- **AC-1. `connectionOptions(overrides)` reads `METRO_DB_*` env vars at call time, with defaults.** It returns `{ host, port, user, password, database }` from `METRO_DB_HOST/PORT/USER/PASSWORD/NAME`, defaulting to `127.0.0.1:3306`, `root`, empty password, `metro`. The read happens inside the function — not at module load — so a caller that sets `METRO_DB_NAME` to a scratch database before invoking a consumer sees the right target (the `claude-bow.test.js` AC-12 / `claude-sync.test.js` scratch-DB pattern depends on this). Caller-supplied `overrides` are spread **last**, so they win over both env and defaults.
- **AC-2. `connect(overrides)` is the throw flavour.** It `require`s `mysql2/promise` **lazily inside the function** (not at module load) and calls `.createConnection` looked up at call time, then returns the connection. On connection failure it **throws** and does not catch — the caller owns the failure decision. The lazy-require-plus-lookup-at-call-time shape is deliberate: a caller that monkey-patches `require('mysql2/promise').createConnection` for a test (`claude-dispatch-guard.test.js` AC-8) still sees its patch, because Node caches one shared module object.
- **AC-3. `connectCLI(label, overrides)` is the fail-stop flavour.** It awaits `connect()`; on failure it prints a labelled error (`<label>: cannot connect to metro MariaDB: <message>`) plus a hint to verify the MariaDB service is running and the `metro` database exists, then calls `process.exit(1)`. An interactive CLI cannot proceed without a database, so the user is told and the process stopped.
- **AC-4. Port coercion and defaults are explicit.** `port` is `Number(...)`-coerced from env or the `3306` default; `host`/`user`/`password`/`database` are string passthroughs from env or their defaults. No other options are injected by this module — `connectTimeout` is **caller-supplied**, not set here (see AC-7).

### Fail-open posture

- **AC-5. `connect()` imposes no posture — it throws and lets the caller decide.** The guards that consume it either fail-open (allow the action with a stderr note) or fail-closed (deny) according to their own mandate. This is the whole point of the two-flavour split: the failure **semantics** live with the caller, and this module must not smuggle a posture in that a future caller would inherit by accident. A future edit that adds a `try/catch`-and-exit inside `connect()` would silently change every guard's behaviour and must not happen.
- **AC-6. `connectCLI()` is fail-stop by construction.** The only acceptable outcome of a CLI-facing connection failure is a clear message and a non-zero exit — never a half-initialised interactive tool continuing without its backing store.
- **AC-7. A dead DB must not hang a commit.** The guards pass `connectTimeout: 4000` so a MariaDB outage times out instead of blocking a commit; `claude-db.js` itself sets no default timeout, which is correct — only the caller knows the right bound for its own latency budget (a guard needs a hard bound; an interactive CLI may not).

### Tests

- **AC-8. Coverage is indirect, through caller suites — there is no `claude-db.test.js`.** `claude-bow.test.js` and `claude-sync.test.js` run against a scratch database by setting `METRO_DB_NAME` before requiring/executing their consumer, which exercises the read-at-call-time env contract (AC-1) end-to-end. `claude-dispatch-guard.test.js` AC-8 monkey-patches `mysql2/promise`'s `createConnection` and asserts the options object reaches it, which exercises the lazy-require + call-time-lookup contract (AC-2). `connectCLI`'s `process.exit(1)` path and the exact env-var precedence/defaults of `connectionOptions` are **not** directly regression-tested (ASM-756).

### Determinism

- **AC-9. Connection-option derivation is deterministic.** Given the same environment, `connectionOptions()` returns the same object; there is no randomness and no wall-clock use in the derivation (any timing sensitivity is confined to the database layer's own connect timeout, which is caller-supplied).

## Out of scope

- Each guard's individual fail-open/fail-closed decision and its `permissionDecision` output — those live in the guard files themselves, not here.
- Schema creation/migration and BOW CRUD — that is `claude-bow.js`'s (and `claude-sync.js`'s) concern; this module only returns connections.
- Any Go-side database access — this is the Node tooling layer's helper, unrelated to the game engine.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-756 (P1)** — no dedicated `claude-db.test.js`; `connect`/`connectCLI`/`connectionOptions` are covered only indirectly through caller suites. In particular `connectCLI`'s fail-stop (`process.exit(1)`) and the env-var precedence/defaults are documented from source reading, not directly regression-tested.

## Escalations

- **ASM-756 is worth closing with a small dedicated test file** (e.g. assert `connectionOptions` precedence/defaults directly, and exercise `connectCLI`'s exit path via a subprocess spawn), but it is not a blocker: the two load-bearing contracts (read-at-call-time env, lazy-require/createConnection lookup) are already pinned by `claude-bow.test.js` and `claude-dispatch-guard.test.js`. Flagged for Bill's prioritisation against the game-first lane policy.
