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
