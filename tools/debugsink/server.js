// Module key: tool.debugsink (root tooling — GR#2 exempt from the app-version
// discipline; not part of code.json's module graph, mirrors tools/plan/*.js).
// Spec ref: FEAT-2326609752 (AARON Q100078=B, 2026-09-03) — "Debug commits
// into metro MariaDB via a tiny local endpoint": stop queueing client-side
// entirely, kill the localStorage class, queryable debug history now.
// Ties FEAT-2326609718's 1-month retention intent.
//
// tools/debugsink/server.js — a tiny, localhost-only Node HTTP server that
// receives webconsole debug-tab commits (see webconsole/src/sim/backend.ts
// commitDebug) and persists them into the metro MariaDB `debug_commits`
// table instead of the browser's localStorage queue (commitqueue.ts /
// ASM-453). The client's existing localStorage queue is NOT removed — it
// stays as the fallback path for when this endpoint is unreachable (the
// ASM-453 "nothing is ever lost" contract), and this server also accepts
// drained entries from that queue (same POST route, upserted on `id` so a
// drain can never double-insert).
//
// CONSOLIDATOR AUDIT TRAIL (Aaron's ruling on FEAT-2326609761, via
// claude-bow.js show FEAT-2326609761's comments, 2026-09-04): "have the
// consolidator build its own audit of what it discovers, and then what it's
// planning — in the square and at the overall big-picture level — and then
// what it implements. Log it all to Maria, and the lead keeps an eye on its
// progress to make sure it does not go mad." This adds a SECOND table,
// `consolidator_audit`, alongside `debug_commits` on the SAME server (same
// port, same connection helper, same 31-day prune discipline) — one new POST
// route for the webconsole client (see webconsole/src/sim/consolidatorAudit.ts)
// and one new GET route for the lead's oversight queries (see
// tools/consolidator-oversight.mjs). Three stages, one row per audit event:
//   'discovered' — a section audit summary + opportunity list (what the pass
//                  SAW on the map, before any planning).
//   'planned'    — what a pass INTENDS to do: in-square plans plus the
//                  whole-map big-picture view (still read-only at this stage;
//                  the mutation half decides separately whether to act).
//   'implemented' — what a pass ACTUALLY did. Consumed later by the mutation
//                  half's own ConsolidationPass log (a separate lane); this
//                  table is the durable, queryable record of it, mirroring
//                  how debug_commits durably records DebugTab commits.
// This is TELEMETRY, never a gameplay dependency (mirrors the debug_commits
// contract exactly): the consolidator's actual behaviour must never change
// because this sink is up, slow, or down.
//
// Start alongside dev: from webconsole/, run `npm run dev:full` (which launches
// both the Vite dev server and this server concurrently via dev-with-sink.mjs),
// or start this server standalone: `node tools/debugsink/server.js` (listens on
// port 8642, falls back to browser queue when unreachable).
//
// Connection: uses the project's shared metro MariaDB helper (claude-db.js,
// FEAT-123) exactly as claude-sync.js does — same env vars
// (METRO_DB_HOST/PORT/USER/PASSWORD/NAME), same defaults
// (127.0.0.1:3306, root, '', metro). No new DB dependency: mysql2 is
// already a root package.json dependency (used by claude-sync.js et al).
//
// GR#1 (aggressive error trapping): every failure path returns a JSON body
// shaped { ok: false, error: '<code>', message: '<detail>' }. These are
// root-tooling-local string codes (e.g. 'debugsink.db_error'), NOT MET-xxxx
// registry codes — GR#2 explicitly exempts root tooling from the app error
// registry, and root tooling scripts (claude-sync.js, claude-bow.js,
// claude-db.js) have never used MET- codes (confirmed by grep before writing
// this file). Nothing here invents a fake MET- registry entry.
//
// Localhost-only: binds 127.0.0.1 explicitly (never 0.0.0.0) — this is a
// local dev tool, never meant to be reachable off-box.
//
// BUG-543 discipline applied proactively: this file lives under
// tools/debugsink/, NOT under a `test/` directory, so it is not
// auto-discovered by CI's root `node --test`. It still follows the same
// rule the guard teaches: requiring this module must NEVER have a side
// effect (no auto-listen) — only running it directly
// (`require.main === module`) starts the server. The test suite under
// tools/debugsink/test/ requires this file freely without a live DB or an
// open port.

'use strict';

const http = require('http');
const path = require('path');

const HOST = '127.0.0.1';
const DEFAULT_PORT = 8642; // fixed port for the local debug sink — documented in this header + report

// Retention window (FEAT-2326609718's stated intent, shared by this FEAT).
const RETENTION_DAYS = 31;

// Hard cap on request body size so a runaway/hostile payload can't exhaust
// process memory. Debug frames are already byte-budgeted client-side
// (commitqueue.ts's 2MB QUEUE_BYTE_BUDGET) but the LIVE (non-queued) commit
// path sends the full, uncompacted debug.json — allow generous headroom.
const MAX_BODY_BYTES = 32 * 1024 * 1024; // 32MB

/**
 * Lazily require the shared connection helper (claude-db.js) so tests can
 * inject a fake `connect` without needing mysql2 or a live database at all.
 */
function defaultConnect() {
  // eslint-disable-next-line global-require
  const { connect } = require(path.join('..', '..', 'claude-db.js'));
  return connect();
}

/**
 * CREATE TABLE IF NOT EXISTS statement. Idempotent — safe to run on every
 * server start.
 */
const SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS debug_commits (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  committed_at DATETIME NOT NULL,
  app_version VARCHAR(128) NULL,
  payload LONGTEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
`;

/** Ensure the debug_commits table exists. Idempotent; safe to call every start. */
async function ensureSchema(conn) {
  await conn.query(SCHEMA_SQL);
}

/** Delete rows older than the retention window. Called on every successful write. */
async function pruneOld(conn) {
  await conn.query(
    `DELETE FROM debug_commits WHERE committed_at < (NOW() - INTERVAL ? DAY)`,
    [RETENTION_DAYS]
  );
}

/**
 * consolidator_audit — one row per audit event the CONSOLIDATOR pass logs
 * (Aaron's ruling, see the file header). `stage` is a closed enum (never a
 * free string) so a malformed/rogue stage value is rejected at the DB layer
 * as a defence-in-depth backstop behind the handler's own validation below.
 * The (stage, at) index is what tools/consolidator-oversight.mjs's queries
 * (GET /api/consolidator/audit?since=...&stage=...) actually run against —
 * without it every oversight poll would be a full table scan.
 *
 * PRIMARY KEY is the COMPOSITE (id, stage) — NOT `id` alone (independent-
 * round finding 2, 2026-09-04, "CROSS-STAGE ID CLOBBER"): with an id-only
 * key, `ON DUPLICATE KEY UPDATE id = id` makes posting the SAME id under a
 * DIFFERENT stage a silent no-op against whichever stage's row landed
 * FIRST, permanently losing the second payload with a 200 OK. Today's client
 * (consolidatorAudit.ts) embeds the stage into the id
 * (`AUD-<STAGE>-<tick>`), which happens to make a real collision
 * unreachable in practice — but the SCHEMA itself did not enforce that
 * invariant, so nothing stopped a future caller, a hand-crafted request, or
 * a future id-format change from losing a real row silently. The composite
 * key makes (id, stage) the actual identity a duplicate post upserts
 * against, and a genuinely different stage under the same id is now a
 * SEPARATE row, never a clobber. See ensureAuditCompositeKey below for how
 * an already-existing (pre-fix) table migrates.
 */
const AUDIT_SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS consolidator_audit (
  id VARCHAR(64) NOT NULL,
  stage ENUM('discovered', 'planned', 'implemented') NOT NULL,
  at DATETIME NOT NULL,
  payload LONGTEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id, stage),
  KEY idx_stage_at (stage, at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
`;

const AUDIT_STAGES = new Set(['discovered', 'planned', 'implemented']);

/**
 * Migrate an EXISTING consolidator_audit table (created before the composite-
 * key fix above) from the old (id)-only PRIMARY KEY to (id, stage).
 * `CREATE TABLE IF NOT EXISTS` never touches an already-existing table's
 * constraints, so without this an existing dev/CI database keeps the old,
 * clobber-prone key forever.
 *
 * DROP-AND-RECREATE-IF-SCHEMA-OLD, not an unconditional ALTER: this table's
 * ensureAuditSchema runs on EVERY request (see createHandler's POST/GET
 * handlers), so an unconditional `ALTER TABLE ... DROP PRIMARY KEY, ADD
 * PRIMARY KEY` on every single call would take a metadata lock on every
 * request — the exact MDL-starvation risk claude-bow.js's BUG-115 comment
 * documents for its own ensureSchema. Mirrors claude-bow.js's
 * ensureGitRefCreatedAtFractional/ensureFkOnUpdateCascade idiom exactly:
 * check information_schema FIRST (never assumed from the CREATE TABLE text
 * — MariaDB's `information_schema.STATISTICS` is the live source of truth
 * for which columns actually make up the PRIMARY KEY today), and only issue
 * the DROP+ADD when the live schema is provably still the old one. An
 * already-migrated database (including every brand-new one, whose CREATE
 * TABLE already declares the composite key directly) pays a single cheap
 * indexed SELECT and takes no metadata lock at all.
 */
async function ensureAuditCompositeKey(conn) {
  const [rows] = await conn.query(
    `SELECT COLUMN_NAME FROM information_schema.STATISTICS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'consolidator_audit' AND INDEX_NAME = 'PRIMARY'
     ORDER BY SEQ_IN_INDEX`
  );
  // The composite key AUDIT_SCHEMA_SQL declares -- compared as a joined
  // signature so migration status is a single equality against the live
  // information_schema PK column list, in index order.
  const EXPECTED_PK_SIGNATURE = ['id', 'stage'].join(',');
  const pkColumns = rows.map((r) => r.COLUMN_NAME);
  if (pkColumns.length === 0) return; // table doesn't exist yet (or a fake test connection with no information_schema) -- ensureAuditSchema's own CREATE TABLE already declares the composite key
  if (pkColumns.join(',') === EXPECTED_PK_SIGNATURE) return; // already migrated, no-op
  await conn.query('ALTER TABLE consolidator_audit DROP PRIMARY KEY, ADD PRIMARY KEY (id, stage)');
}

/** Ensure the consolidator_audit table exists AND carries the composite (id, stage) key. Idempotent; safe to call every start/request. */
async function ensureAuditSchema(conn) {
  await conn.query(AUDIT_SCHEMA_SQL);
  await ensureAuditCompositeKey(conn);
}

/** Delete audit rows older than the retention window — same 31-day discipline as debug_commits. */
async function pruneOldAudit(conn) {
  await conn.query(
    `DELETE FROM consolidator_audit WHERE at < (NOW() - INTERVAL ? DAY)`,
    [RETENTION_DAYS]
  );
}

/**
 * Upsert one audit row, keyed on the COMPOSITE (id, stage) — see
 * AUDIT_SCHEMA_SQL's doc for the CROSS-STAGE ID CLOBBER fix this depends on.
 * `ON DUPLICATE KEY UPDATE id = id` is now a true no-op ONLY for a genuine
 * retry of the exact same (id, stage) pair (e.g. a client-side retry after a
 * timed-out-but-actually-succeeded request) — accepted idempotently rather
 * than double-counted, which matters here because the oversight script
 * COUNTS rows per pass (runaway-demolition / spend-rate thresholds) and a
 * duplicate row would silently inflate every one of them. The SAME id under
 * a DIFFERENT stage no longer collides with the key at all — it inserts as
 * its own row, exactly as intended.
 */
async function upsertAuditEntry(conn, { id, stage, at, payload }) {
  const atDate = new Date(at);
  const atSql = Number.isNaN(atDate.getTime()) ? new Date() : atDate; // malformed `at` -> server-observed time, never a rejected write
  const payloadJson = JSON.stringify(payload === undefined ? null : payload);

  await conn.query(
    `INSERT INTO consolidator_audit (id, stage, at, payload)
     VALUES (?, ?, ?, ?)
     ON DUPLICATE KEY UPDATE id = id`,
    [id, stage, atSql, payloadJson]
  );
  await pruneOldAudit(conn);
}

/**
 * Query audit rows for the lead's oversight polling (GET /api/consolidator/audit).
 * `since` (ISO-8601 string, optional) and `stage` (one of AUDIT_STAGES,
 * optional) both narrow the WHERE clause; omitting both returns the full
 * (already 31-day-pruned) table. Always ordered oldest-first (`at ASC`) so a
 * caller building a pass-over-pass trend (oscillation/capacity-down
 * detection) reads it in chronological order without re-sorting.
 * Parameterised throughout — no string-built SQL, matching the rest of this
 * file's discipline.
 */
async function queryAuditEntries(conn, { since, stage } = {}) {
  const clauses = [];
  const params = [];
  if (typeof since === 'string' && since.length > 0) {
    const sinceDate = new Date(since);
    if (!Number.isNaN(sinceDate.getTime())) {
      clauses.push('at >= ?');
      params.push(sinceDate);
    }
    // A malformed `since` is silently ignored (not rejected) — an oversight
    // poll with a bad query param should degrade to "show everything" rather
    // than 400 and stop monitoring entirely (GR#17: the monitor itself must
    // stay up even when a caller mis-formats a filter).
  }
  if (typeof stage === 'string' && AUDIT_STAGES.has(stage)) {
    clauses.push('stage = ?');
    params.push(stage);
  }
  const where = clauses.length > 0 ? `WHERE ${clauses.join(' AND ')}` : '';
  const [rows] = await conn.query(
    `SELECT id, stage, at, payload FROM consolidator_audit ${where} ORDER BY at ASC`,
    params
  );
  return (Array.isArray(rows) ? rows : []).map((r) => ({
    id: r.id,
    stage: r.stage,
    at: r.at instanceof Date ? r.at.toISOString() : r.at,
    payload: safeJsonParse(r.payload),
  }));
}

/** Best-effort JSON.parse — a corrupt/legacy row degrades to null rather than crashing the oversight query. */
function safeJsonParse(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * Best-effort extraction of an app version string from a debug.json-shaped
 * payload (meta.appVersion — see webconsole/src/sim/debugjson.ts). Never
 * throws; returns null when absent/malformed so the column is simply NULL.
 */
function extractAppVersion(payload) {
  try {
    const v = payload && typeof payload === 'object' ? payload.meta && payload.meta.appVersion : null;
    return typeof v === 'string' && v.length > 0 ? v.slice(0, 128) : null;
  } catch {
    return null;
  }
}

/**
 * Upsert one commit row. ON DUPLICATE KEY UPDATE is a true no-op (id = id) —
 * a drained localStorage entry that was already written by a prior live
 * commit (or a retried drain) is accepted idempotently, never double-
 * inserted and never silently overwritten with a stale re-send.
 */
async function upsertCommit(conn, { id, at, payload }) {
  const committedAt = new Date(at);
  const committedAtSql = Number.isNaN(committedAt.getTime())
    ? new Date() // malformed `at` — fall back to server-observed time rather than reject the commit
    : committedAt;
  const appVersion = extractAppVersion(payload);
  const payloadJson = JSON.stringify(payload === undefined ? null : payload);

  await conn.query(
    `INSERT INTO debug_commits (id, committed_at, app_version, payload)
     VALUES (?, ?, ?, ?)
     ON DUPLICATE KEY UPDATE id = id`,
    [id, committedAtSql, appVersion, payloadJson]
  );
  await pruneOld(conn);
}

async function countCommits(conn) {
  const [rows] = await conn.query('SELECT COUNT(*) AS n FROM debug_commits');
  const row = Array.isArray(rows) ? rows[0] : rows;
  return row ? Number(row.n) : 0;
}

/**
 * `opts.headers` merges in extra response headers (e.g. `Connection: close`
 * for the body-too-large path — see F2 fix note on readJsonBody below).
 * `opts.onFlush` runs after the response has actually been written to the
 * socket (Node's `res.end(data, callback)` callback), NOT before — this is
 * what lets a caller safely destroy the underlying request/socket only once
 * the client is guaranteed to have received the bytes, instead of racing a
 * destroy against the still-in-flight write.
 */
function sendJson(res, status, body, opts = {}) {
  const text = JSON.stringify(body);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(text),
    // Localhost-only tool: CORS is opened wide so the vite dev origin (any
    // port) can call it directly without a proxy — see webconsole/vite.config.ts
    // header note for why a proxy was not added (absolute 127.0.0.1 URL is
    // simpler here and this never leaves the loopback interface).
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type',
    ...(opts.headers || {}),
  });
  res.end(text, () => {
    if (opts.onFlush) opts.onFlush();
  });
}

function errorJson(res, status, code, message, opts) {
  sendJson(res, status, { ok: false, error: code, message }, opts);
}

/**
 * Read + JSON-parse the request body, bounded by `maxBytes` (defaults to the
 * real MAX_BODY_BYTES; `createHandler`'s `deps.maxBodyBytes` overrides it —
 * test-only, so the F2 regression test can trip the oversize path with a few
 * KB instead of moving real tens-of-megabytes over a loopback socket, which
 * proved slow/flaky in this sandbox). Never throws — failures reject with a
 * `.code`-tagged Error for the caller to translate into a JSON error
 * response.
 *
 * REJECT-round fix (F2, 2026-09-03): the FIRST version called `req.destroy()`
 * synchronously in the SAME tick as rejecting on an oversize body. That tore
 * down the shared request/response socket before createHandler's `catch`
 * block ever got a chance to write the documented 413 JSON body — the client
 * saw a bare `ECONNRESET`, never `debugsink.body_too_large`. Fixed by
 * DECOUPLING "stop reading the oversize body" from "close the socket":
 * this function now only stops consuming (removes its own listeners +
 * `req.pause()`) and rejects; it never calls `req.destroy()` itself. The
 * caller (createHandler) writes the full 413 JSON response first, with a
 * `Connection: close` header (announcing the socket won't be reused for a
 * further request, matching WHATWG/Node conventions when there is unread
 * body left on the wire), and only destroys the request in `res.end`'s own
 * flush callback — i.e. strictly AFTER the client has received the error
 * body. See the regression test asserting the actual 413 JSON body is
 * received (not a connection reset) in tools/debugsink/test/server.test.js.
 */
function readJsonBody(req, maxBytes = MAX_BODY_BYTES) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let bytes = 0;
    let settled = false;

    function cleanup() {
      req.removeListener('data', onData);
      req.removeListener('end', onEnd);
      req.removeListener('error', onError);
    }

    function onData(chunk) {
      if (settled) return;
      bytes += chunk.length;
      if (bytes > maxBytes) {
        settled = true;
        cleanup();
        // Stop consuming — do NOT destroy the socket here (see fix note
        // above). The still-open request is left for the caller to decide
        // what to do with once the 413 response has flushed.
        req.pause();
        reject(Object.assign(new Error('body too large'), { code: 'debugsink.body_too_large' }));
        return;
      }
      chunks.push(chunk);
    }

    function onEnd() {
      if (settled) return;
      settled = true;
      const raw = Buffer.concat(chunks).toString('utf8');
      if (raw.length === 0) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(Object.assign(new Error('invalid JSON body'), { code: 'debugsink.bad_json', cause: err }));
      }
    }

    function onError(err) {
      if (settled) return;
      settled = true;
      cleanup();
      reject(Object.assign(err, { code: err.code || 'debugsink.request_error' }));
    }

    req.on('data', onData);
    req.on('end', onEnd);
    req.on('error', onError);
  });
}

/**
 * Build the request handler. `deps.connect` is injectable (tests pass a
 * fake connection factory so no live DB or mysql2 install is required).
 * A fresh connection is opened per request and always closed — this is a
 * low-traffic local dev tool, not a high-throughput service, so a pool is
 * not warranted (mirrors claude-sync.js's own per-command connect/close
 * pattern via claude-db.js). `deps.maxBodyBytes` is test-only — production
 * (startServer with no override, i.e. the real `require.main` entry point)
 * always uses the real MAX_BODY_BYTES.
 */
function createHandler(deps = {}) {
  const connect = deps.connect || defaultConnect;
  const maxBodyBytes = deps.maxBodyBytes || MAX_BODY_BYTES;

  return async function handler(req, res) {
    try {
      if (req.method === 'OPTIONS') {
        res.writeHead(204, {
          'Access-Control-Allow-Origin': '*',
          'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
          'Access-Control-Allow-Headers': 'Content-Type',
        });
        res.end();
        return;
      }

      const url = new URL(req.url, `http://${HOST}`);

      if (req.method === 'GET' && url.pathname === '/api/debug/health') {
        let conn;
        try {
          conn = await connect();
          await ensureSchema(conn);
          const rows = await countCommits(conn);
          sendJson(res, 200, { ok: true, rows });
        } finally {
          if (conn && typeof conn.end === 'function') await conn.end().catch(() => {});
        }
        return;
      }

      if (req.method === 'POST' && url.pathname === '/api/debug/commit') {
        let body;
        try {
          body = await readJsonBody(req, maxBodyBytes);
        } catch (err) {
          if (err.code === 'debugsink.body_too_large') {
            // F2 fix: write the full 413 JSON body FIRST (Connection: close
            // tells the client this socket is done), and only destroy the
            // request — abandoning whatever oversize bytes are still
            // in-flight on the wire — once that response has actually
            // flushed (onFlush = res.end's own callback), never before.
            errorJson(res, 413, err.code, err.message, {
              headers: { Connection: 'close' },
              onFlush: () => {
                try { req.destroy(); } catch { /* socket already gone */ }
              },
            });
            return;
          }
          errorJson(res, 400, err.code || 'debugsink.bad_request', err.message);
          return;
        }

        const { id, at, payload } = body || {};
        if (typeof id !== 'string' || id.length === 0) {
          errorJson(res, 400, 'debugsink.missing_id', 'commit body must include a non-empty string `id`');
          return;
        }
        if (typeof at !== 'string' || at.length === 0) {
          errorJson(res, 400, 'debugsink.missing_at', 'commit body must include a non-empty string `at` (ISO-8601)');
          return;
        }
        if (payload === undefined) {
          errorJson(res, 400, 'debugsink.missing_payload', 'commit body must include a `payload`');
          return;
        }

        let conn;
        try {
          conn = await connect();
          await ensureSchema(conn);
          await upsertCommit(conn, { id, at, payload });
          sendJson(res, 200, { ok: true, id });
        } finally {
          if (conn && typeof conn.end === 'function') await conn.end().catch(() => {});
        }
        return;
      }

      if (req.method === 'POST' && url.pathname === '/api/consolidator/audit') {
        let body;
        try {
          body = await readJsonBody(req, maxBodyBytes);
        } catch (err) {
          if (err.code === 'debugsink.body_too_large') {
            errorJson(res, 413, err.code, err.message, {
              headers: { Connection: 'close' },
              onFlush: () => {
                try { req.destroy(); } catch { /* socket already gone */ }
              },
            });
            return;
          }
          errorJson(res, 400, err.code || 'debugsink.bad_request', err.message);
          return;
        }

        const { id, stage, at, payload } = body || {};
        if (typeof id !== 'string' || id.length === 0) {
          errorJson(res, 400, 'debugsink.missing_id', 'audit body must include a non-empty string `id`');
          return;
        }
        if (typeof stage !== 'string' || !AUDIT_STAGES.has(stage)) {
          errorJson(
            res,
            400,
            'debugsink.bad_stage',
            `audit body \`stage\` must be one of ${Array.from(AUDIT_STAGES).join(', ')}`
          );
          return;
        }
        if (typeof at !== 'string' || at.length === 0) {
          errorJson(res, 400, 'debugsink.missing_at', 'audit body must include a non-empty string `at` (ISO-8601)');
          return;
        }
        if (payload === undefined) {
          errorJson(res, 400, 'debugsink.missing_payload', 'audit body must include a `payload`');
          return;
        }

        let conn;
        try {
          conn = await connect();
          await ensureAuditSchema(conn);
          await upsertAuditEntry(conn, { id, stage, at, payload });
          sendJson(res, 200, { ok: true, id });
        } finally {
          if (conn && typeof conn.end === 'function') await conn.end().catch(() => {});
        }
        return;
      }

      if (req.method === 'GET' && url.pathname === '/api/consolidator/audit') {
        const since = url.searchParams.get('since') || undefined;
        const stage = url.searchParams.get('stage') || undefined;
        if (stage !== undefined && !AUDIT_STAGES.has(stage)) {
          errorJson(res, 400, 'debugsink.bad_stage', `\`stage\` query param must be one of ${Array.from(AUDIT_STAGES).join(', ')}`);
          return;
        }
        let conn;
        try {
          conn = await connect();
          await ensureAuditSchema(conn);
          const entries = await queryAuditEntries(conn, { since, stage });
          sendJson(res, 200, { ok: true, entries });
        } finally {
          if (conn && typeof conn.end === 'function') await conn.end().catch(() => {});
        }
        return;
      }

      errorJson(res, 404, 'debugsink.not_found', `no route for ${req.method} ${url.pathname}`);
    } catch (err) {
      // GR#1: every failure path returns a JSON error, never an unhandled
      // exception / connection reset. DB-connect failures (MariaDB not
      // running, wrong creds, etc.) land here.
      errorJson(res, 500, 'debugsink.db_error', err && err.message ? err.message : String(err));
    }
  };
}

/**
 * Start the HTTP server bound to 127.0.0.1 only. Returns { server, close }.
 * `deps.connect` and `deps.port` are injectable for tests; production use
 * (require.main === module, below) uses the real defaults.
 */
function startServer(deps = {}) {
  const port = deps.port || DEFAULT_PORT;
  const handler = createHandler(deps);
  const server = http.createServer((req, res) => {
    handler(req, res).catch((err) => {
      // handler() already catches internally; this is an absolute last
      // resort so a bug in the handler itself can never crash the process
      // or hang a client connection (GR#1).
      try {
        errorJson(res, 500, 'debugsink.internal_error', err && err.message ? err.message : String(err));
      } catch {
        try { res.end(); } catch { /* connection already gone */ }
      }
    });
  });

  return new Promise((resolve, reject) => {
    server.on('error', reject);
    server.listen(port, HOST, () => {
      resolve({
        server,
        port,
        close: () => new Promise((r) => server.close(() => r())),
      });
    });
  });
}

module.exports = {
  createHandler,
  startServer,
  ensureSchema,
  pruneOld,
  upsertCommit,
  countCommits,
  extractAppVersion,
  SCHEMA_SQL,
  DEFAULT_PORT,
  RETENTION_DAYS,
  MAX_BODY_BYTES,
  // Consolidator audit trail (Aaron's ruling, FEAT-2326609761).
  ensureAuditSchema,
  ensureAuditCompositeKey,
  pruneOldAudit,
  upsertAuditEntry,
  queryAuditEntries,
  AUDIT_SCHEMA_SQL,
  AUDIT_STAGES,
};

// Only listen when this file is run directly (`node tools/debugsink/server.js`),
// never as a side effect of `require('./server.js')` from a test.
if (require.main === module) {
  startServer()
    .then(({ port }) => {
      // eslint-disable-next-line no-console
      console.log(`debugsink: listening on http://${HOST}:${port} (metro MariaDB debug_commits, ${RETENTION_DAYS}-day retention)`);
    })
    .catch((err) => {
      // eslint-disable-next-line no-console
      console.error(`debugsink: failed to start: ${err && err.message ? err.message : err}`);
      process.exit(1);
    });
}
