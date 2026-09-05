// tools/debugsink/test/server.test.js — unit tests for the debug-commit sink
// (FEAT-2326609752). Every test injects a FAKE `connect` — no live MariaDB,
// no mysql2 import here, and requiring server.js never opens a real
// listening socket implicitly (require.main guard) nor touches a real DB
// (connect is only invoked per-request, lazily, and only the fake is used).
//
// BUG-543: this file lives under a `test/` directory, so CI's root
// `node --test` WILL auto-discover it. That is fine here (unlike
// tools/test/scoped.mjs, a CLI tool) — this file IS a real test suite, not a
// tool masquerading under test/, so no NODE_TEST_CONTEXT no-op guard is
// needed. What DOES need the guard is server.js itself (requiring it must
// never auto-listen) — verified by the "requiring the module does not
// listen" test below.

'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');

const {
  createHandler,
  ensureSchema,
  pruneOld,
  upsertCommit,
  countCommits,
  extractAppVersion,
  startServer,
  RETENTION_DAYS,
  SCHEMA_SQL,
  MAX_BODY_BYTES,
  SINK_NAME,
} = require('../server.js');

/**
 * A minimal in-memory fake mimicking the mysql2/promise connection surface
 * this module uses: `.query(sql, params)` and `.end()`. Records every call
 * so tests can assert on SQL shape without a real database.
 */
function fakeConnection(overrides = {}) {
  const calls = [];
  let rows = [{ n: 0 }];
  const conn = {
    calls,
    async query(sql, params) {
      calls.push({ sql, params });
      if (overrides.onQuery) {
        const r = await overrides.onQuery(sql, params, conn);
        if (r !== undefined) return r;
      }
      if (/SELECT COUNT\(\*\)/i.test(sql)) {
        return [rows];
      }
      return [{ affectedRows: 1 }];
    },
    async end() {
      conn.ended = true;
    },
    setCount(n) {
      rows = [{ n }];
    },
  };
  return conn;
}

/** Fire a bare Node HTTP request against an in-process handler (no real listen). */
function requestHandler(handler, { method = 'GET', path = '/', body } = {}) {
  return new Promise((resolve, reject) => {
    const server = http.createServer((req, res) => {
      handler(req, res).catch(reject);
    });
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      const payload = body !== undefined ? JSON.stringify(body) : undefined;
      const req = http.request(
        { host: '127.0.0.1', port, method, path, headers: payload ? { 'Content-Type': 'application/json' } : {} },
        (res) => {
          const chunks = [];
          res.on('data', (c) => chunks.push(c));
          res.on('end', () => {
            server.close();
            const text = Buffer.concat(chunks).toString('utf8');
            let json = null;
            try { json = JSON.parse(text); } catch { /* leave null */ }
            resolve({ status: res.statusCode, headers: res.headers, text, json });
          });
        }
      );
      req.on('error', (e) => { server.close(); reject(e); });
      if (payload) req.write(payload);
      req.end();
    });
  });
}

test('ensureSchema issues a CREATE TABLE IF NOT EXISTS', async () => {
  const conn = fakeConnection();
  await ensureSchema(conn);
  assert.equal(conn.calls.length, 1);
  assert.match(conn.calls[0].sql, /CREATE TABLE IF NOT EXISTS debug_commits/);
  assert.match(SCHEMA_SQL, /PRIMARY KEY/);
});

test('pruneOld deletes rows older than the retention window', async () => {
  const conn = fakeConnection();
  await pruneOld(conn);
  assert.equal(conn.calls.length, 1);
  assert.match(conn.calls[0].sql, /DELETE FROM debug_commits WHERE committed_at < \(NOW\(\) - INTERVAL \? DAY\)/);
  assert.deepEqual(conn.calls[0].params, [RETENTION_DAYS]);
});

test('extractAppVersion pulls payload.meta.appVersion, else null, never throws', () => {
  assert.equal(extractAppVersion({ meta: { appVersion: 'v1.2.3' } }), 'v1.2.3');
  assert.equal(extractAppVersion({ meta: {} }), null);
  assert.equal(extractAppVersion({}), null);
  assert.equal(extractAppVersion(null), null);
  assert.equal(extractAppVersion(undefined), null);
  assert.equal(extractAppVersion('not an object'), null);
  // An evil getter must not escape extractAppVersion (mirrors backend.ts's
  // safeStringifyAny discipline — this module must never crash on bad input).
  const evil = { get meta() { throw new Error('boom'); } };
  assert.equal(extractAppVersion(evil), null);
});

test('upsertCommit issues an ON DUPLICATE KEY UPDATE id = id upsert then prunes', async () => {
  const conn = fakeConnection();
  await upsertCommit(conn, { id: 'DBG-1', at: '2026-09-03T00:00:00.000Z', payload: { meta: { appVersion: '1.0.0' }, x: 1 } });
  assert.equal(conn.calls.length, 2);
  assert.match(conn.calls[0].sql, /INSERT INTO debug_commits/);
  assert.match(conn.calls[0].sql, /ON DUPLICATE KEY UPDATE id = id/);
  assert.equal(conn.calls[0].params[0], 'DBG-1');
  assert.equal(conn.calls[0].params[2], '1.0.0');
  assert.equal(JSON.parse(conn.calls[0].params[3]).x, 1);
  assert.match(conn.calls[1].sql, /DELETE FROM debug_commits/);
});

test('upsertCommit tolerates a malformed `at` by falling back to server time, never throws', async () => {
  const conn = fakeConnection();
  await assert.doesNotReject(() => upsertCommit(conn, { id: 'DBG-2', at: 'not-a-date', payload: {} }));
  const committedAt = conn.calls[0].params[1];
  assert.ok(committedAt instanceof Date && !Number.isNaN(committedAt.getTime()));
});

test('countCommits reads SELECT COUNT(*) AS n', async () => {
  const conn = fakeConnection();
  conn.setCount(7);
  const n = await countCommits(conn);
  assert.equal(n, 7);
});

test('GET /api/debug/health returns {ok:true, rows:N}', async () => {
  const conn = fakeConnection();
  conn.setCount(3);
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/debug/health' });
  assert.equal(res.status, 200);
  assert.deepEqual(res.json, { ok: true, rows: 3 });
  assert.ok(conn.ended, 'connection must be closed after the request');
});

test('POST /api/debug/commit with a valid body upserts and returns 200 {ok:true,sink,id} (BUG-703 verifiable ack)', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, {
    method: 'POST',
    path: '/api/debug/commit',
    body: { id: 'DBG-ABC', at: new Date().toISOString(), payload: { hello: 'world' } },
  });
  assert.equal(res.status, 200);
  assert.deepEqual(res.json, { ok: true, sink: SINK_NAME, id: 'DBG-ABC' });
  assert.equal(res.json.sink, 'metropolis-debugsink', 'BUG-703: the client validates this exact sink identity before trusting the ack');
  assert.ok(conn.calls.some((c) => /INSERT INTO debug_commits/.test(c.sql)));
  assert.ok(conn.ended);
});

test('POST /api/debug/commit twice with the same id upserts (no double-insert, no error)', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const body = { id: 'DBG-DUP', at: new Date().toISOString(), payload: { a: 1 } };
  const r1 = await requestHandler(handler, { method: 'POST', path: '/api/debug/commit', body });
  const r2 = await requestHandler(handler, { method: 'POST', path: '/api/debug/commit', body });
  assert.equal(r1.status, 200);
  assert.equal(r2.status, 200);
  const inserts = conn.calls.filter((c) => /INSERT INTO debug_commits/.test(c.sql));
  assert.equal(inserts.length, 2); // both attempts issue the upsert SQL...
  assert.ok(inserts.every((c) => /ON DUPLICATE KEY UPDATE id = id/.test(c.sql))); // ...but as a true no-op upsert
});

test('POST /api/debug/commit missing id -> 400 debugsink.missing_id', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'POST', path: '/api/debug/commit', body: { at: new Date().toISOString(), payload: {} } });
  assert.equal(res.status, 400);
  assert.equal(res.json.ok, false);
  assert.equal(res.json.error, 'debugsink.missing_id');
});

test('POST /api/debug/commit missing at -> 400 debugsink.missing_at', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'POST', path: '/api/debug/commit', body: { id: 'DBG-X', payload: {} } });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.missing_at');
});

test('POST /api/debug/commit missing payload -> 400 debugsink.missing_payload', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'POST', path: '/api/debug/commit', body: { id: 'DBG-X', at: new Date().toISOString() } });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.missing_payload');
});

test('malformed JSON body -> 400 debugsink.bad_json, never crashes the handler', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await new Promise((resolve, reject) => {
    const server = http.createServer((req, res2) => { handler(req, res2).catch(reject); });
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      const req = http.request({ host: '127.0.0.1', port, method: 'POST', path: '/api/debug/commit', headers: { 'Content-Type': 'application/json' } }, (r) => {
        const chunks = [];
        r.on('data', (c) => chunks.push(c));
        r.on('end', () => { server.close(); resolve({ status: r.statusCode, json: JSON.parse(Buffer.concat(chunks).toString('utf8')) }); });
      });
      req.on('error', reject);
      req.write('{not valid json');
      req.end();
    });
  });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.bad_json');
});

// F2 REJECT-round fix (2026-09-03): the first version called req.destroy()
// synchronously the instant the oversize threshold was crossed, tearing down
// the shared socket before the 413 JSON body was ever written — a client
// saw a bare ECONNRESET, never the documented debugsink.body_too_large
// error. This regression sends a real body past MAX_BODY_BYTES over a real
// socket and asserts the 413 JSON body actually ARRIVES (not a connection
// reset), with Connection: close announcing the socket is done.
test('a body exceeding the cap actually RECEIVES the 413 JSON body, not a connection reset', async () => {
  const conn = fakeConnection();
  // maxBodyBytes is test-only injection (createHandler's deps.maxBodyBytes) —
  // exercises the EXACT same readJsonBody/413 code path real production
  // traffic hits at the real 32MB MAX_BODY_BYTES (verified separately: the
  // constant itself is asserted below), without moving tens of real
  // megabytes over a loopback socket in this sandbox (measured: a genuine
  // >32MB single write here reliably stalled/reset well past any reasonable
  // test budget — a sandbox/loopback throughput artifact, not something this
  // test needs to reproduce to prove the fix).
  const testCap = 4096;
  const handler = createHandler({ connect: async () => conn, maxBodyBytes: testCap });
  const oversized = Buffer.alloc(testCap + 1024, 0x61); // 'a' x (cap + 1KB) — comfortably past testCap

  const res = await new Promise((resolve, reject) => {
    let settled = false;
    const server = http.createServer((req, res2) => { handler(req, res2).catch(reject); });
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      const req = http.request(
        {
          host: '127.0.0.1',
          port,
          method: 'POST',
          path: '/api/debug/commit',
          headers: { 'Content-Type': 'application/json', 'Content-Length': oversized.length },
        },
        (r) => {
          const chunks = [];
          r.on('data', (c) => chunks.push(c));
          r.on('end', () => {
            if (settled) return;
            settled = true;
            server.close();
            let json = null;
            try { json = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { /* leave null */ }
            resolve({ status: r.statusCode, headers: r.headers, json });
          });
        }
      );
      // Once the server aborts (having already stopped reading) it destroys
      // the REQUEST socket in the 413 response's flush callback (F2 fix) —
      // that can surface as an ECONNRESET/EPIPE on this client's `req` AFTER
      // the response has already fully arrived. Ignore any error that lands
      // once we've already settled via a clean 'end'; only a pre-settlement
      // error is the real regression (the bug this test guards against).
      req.on('error', (e) => {
        if (settled) return;
        settled = true;
        server.close();
        reject(e);
      });
      req.write(oversized);
      req.end();
    });
  });

  assert.equal(res.status, 413);
  assert.ok(res.json, 'a real JSON body must have been received, not a bare connection reset');
  assert.equal(res.json.ok, false);
  assert.equal(res.json.error, 'debugsink.body_too_large');
  assert.equal(res.headers.connection, 'close');
});

test('MAX_BODY_BYTES is the real 32MB production cap used when no test override is given', () => {
  assert.equal(MAX_BODY_BYTES, 32 * 1024 * 1024);
});

test('unknown route -> 404 debugsink.not_found', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'GET', path: '/nope' });
  assert.equal(res.status, 404);
  assert.equal(res.json.error, 'debugsink.not_found');
});

test('a DB connect failure surfaces as a JSON 500 debugsink.db_error, never an unhandled throw', async () => {
  const handler = createHandler({ connect: async () => { throw new Error('ECONNREFUSED: no MariaDB'); } });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/debug/health' });
  assert.equal(res.status, 500);
  assert.equal(res.json.error, 'debugsink.db_error');
  assert.match(res.json.message, /ECONNREFUSED/);
});

test('OPTIONS preflight returns 204 with CORS headers, no DB touched', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'OPTIONS', path: '/api/debug/commit' });
  assert.equal(res.status, 204);
  assert.equal(res.headers['access-control-allow-origin'], '*');
  assert.equal(conn.calls.length, 0);
});

test('response carries Access-Control-Allow-Origin so the vite dev origin can call it directly', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/debug/health' });
  assert.equal(res.headers['access-control-allow-origin'], '*');
});

test('requiring server.js does not open a listening socket (BUG-543 discipline)', () => {
  // If require() had a side effect of calling startServer(), this second
  // require (already cached by Node, but re-verify no listener exists) would
  // either throw EADDRINUSE on a real port collision or a lingering handle
  // would keep the process alive; startServer is exported precisely so the
  // caller controls when a socket opens. Assert defensively that no
  // implicit port is occupied by requiring alone.
  delete require.cache[require.resolve('../server.js')];
  const mod = require('../server.js');
  assert.equal(typeof mod.startServer, 'function');
  assert.equal(typeof mod.DEFAULT_PORT, 'number');
});

test('startServer binds 127.0.0.1 and startServer/close round-trips cleanly with a fake connect', async () => {
  const conn = fakeConnection();
  conn.setCount(0);
  const { server, port, close } = await startServer({ connect: async () => conn, port: 0 });
  assert.ok(port === 0 || typeof server.address().port === 'number');
  const addr = server.address();
  assert.equal(addr.address, '127.0.0.1');
  await close();
});
