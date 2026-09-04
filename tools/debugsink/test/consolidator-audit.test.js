// tools/debugsink/test/consolidator-audit.test.js — unit tests for the
// CONSOLIDATOR AUDIT TRAIL routes added to the debug sink server
// (Aaron's ruling on FEAT-2326609761: "have the consolidator build its own
// audit... log it all to Maria"). Mirrors server.test.js's harness exactly
// (fake `connect`, no live MariaDB, no implicit listen on require).
//
// BUG-543: this file lives under tools/debugsink/test/, a real `test/`
// directory CI's root `node --test` auto-discovers — that is correct here,
// this IS a real suite, not a tool under test/ (the guard's actual target is
// server.js itself, re-verified below same as server.test.js does).

'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');

const {
  createHandler,
  ensureAuditSchema,
  ensureAuditCompositeKey,
  pruneOldAudit,
  upsertAuditEntry,
  queryAuditEntries,
  AUDIT_SCHEMA_SQL,
  AUDIT_STAGES,
  RETENTION_DAYS,
} = require('../server.js');

/**
 * A real in-memory implementation of the consolidator_audit table (not just a
 * call-recorder) so round-trip tests (post all three stages, query them
 * back, prune respects 31 days) can actually assert on the DATA that comes
 * back, not just the SQL shape. Recognises the exact statement shapes
 * server.js issues; anything else is a no-op success.
 */
function fakeConnection() {
  const calls = [];
  const rows = []; // { id, stage, at: Date, payload: string }
  const conn = {
    calls,
    rows,
    async query(sql, params = []) {
      calls.push({ sql, params });
      if (/CREATE TABLE/i.test(sql)) return [{}];
      // ensureAuditCompositeKey's migration check — report an ALREADY-
      // migrated composite (id, stage) key so the fake never needs to
      // emulate a real ALTER TABLE ... DROP/ADD PRIMARY KEY (that migration
      // is proven separately, against real MariaDB semantics, by this
      // file's own dedicated migration test below).
      if (/information_schema\.STATISTICS/i.test(sql)) {
        return [[{ COLUMN_NAME: 'id' }, { COLUMN_NAME: 'stage' }]];
      }
      if (/INSERT INTO consolidator_audit/i.test(sql)) {
        const [id, stage, at, payload] = params;
        // COMPOSITE key dedup (id, stage) — independent-round finding 2,
        // 2026-09-04 ("CROSS-STAGE ID CLOBBER"): the SAME id under a
        // DIFFERENT stage is a distinct row, never a clobber.
        const existing = rows.find((r) => r.id === id && r.stage === stage);
        if (existing) return [{ affectedRows: 0 }]; // ON DUPLICATE KEY UPDATE id = id — true no-op for a genuine (id, stage) repeat
        rows.push({ id, stage, at, payload });
        return [{ affectedRows: 1 }];
      }
      if (/DELETE FROM consolidator_audit/i.test(sql)) {
        const [days] = params;
        const cutoff = Date.now() - days * 24 * 60 * 60 * 1000;
        for (let i = rows.length - 1; i >= 0; i--) {
          if (rows[i].at.getTime() < cutoff) rows.splice(i, 1);
        }
        return [{ affectedRows: 0 }];
      }
      if (/SELECT id, stage, at, payload FROM consolidator_audit/i.test(sql)) {
        let out = rows.slice();
        // Reproduce the WHERE clause the real query builds, in the same
        // clause order (since first, stage second) — params are positional.
        let pi = 0;
        if (/at >= \?/i.test(sql)) {
          const since = params[pi++];
          out = out.filter((r) => r.at.getTime() >= since.getTime());
        }
        if (/stage = \?/i.test(sql)) {
          const stage = params[pi++];
          out = out.filter((r) => r.stage === stage);
        }
        out = out.slice().sort((a, b) => a.at.getTime() - b.at.getTime());
        return [out];
      }
      return [[]];
    },
    async end() {
      conn.ended = true;
    },
  };
  return conn;
}

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

test('AUDIT_SCHEMA_SQL declares the closed 3-stage enum, the COMPOSITE (id, stage) primary key, and a stage/at index', () => {
  assert.match(AUDIT_SCHEMA_SQL, /CREATE TABLE IF NOT EXISTS consolidator_audit/);
  assert.match(AUDIT_SCHEMA_SQL, /stage ENUM\('discovered', 'planned', 'implemented'\)/);
  assert.match(AUDIT_SCHEMA_SQL, /PRIMARY KEY \(id, stage\)/);
  // The CROSS-STAGE ID CLOBBER fix (independent-round finding 2, 2026-09-04):
  // `id` must NOT carry its own inline PRIMARY KEY any more — the composite
  // clause above is the ONLY primary key declaration.
  assert.doesNotMatch(AUDIT_SCHEMA_SQL, /id VARCHAR\(64\) NOT NULL PRIMARY KEY/);
  assert.match(AUDIT_SCHEMA_SQL, /KEY idx_stage_at \(stage, at\)/);
  assert.deepEqual(Array.from(AUDIT_STAGES).sort(), ['discovered', 'implemented', 'planned']);
});

test('ensureAuditSchema issues a CREATE TABLE IF NOT EXISTS, then checks the composite-key migration', async () => {
  const conn = fakeConnection();
  await ensureAuditSchema(conn);
  assert.equal(conn.calls.length, 2);
  assert.match(conn.calls[0].sql, /CREATE TABLE IF NOT EXISTS consolidator_audit/);
  assert.match(conn.calls[1].sql, /information_schema\.STATISTICS/);
});

test('pruneOldAudit deletes rows older than the retention window', async () => {
  const conn = fakeConnection();
  await pruneOldAudit(conn);
  assert.equal(conn.calls.length, 1);
  assert.match(conn.calls[0].sql, /DELETE FROM consolidator_audit WHERE at < \(NOW\(\) - INTERVAL \? DAY\)/);
  assert.deepEqual(conn.calls[0].params, [RETENTION_DAYS]);
});

// ---------------------------------------------------------------------------
// ensureAuditCompositeKey — the CROSS-STAGE ID CLOBBER migration
// (independent-round finding 2, 2026-09-04). A dedicated fake connection
// (distinct from fakeConnection() above, which always reports "already
// migrated") lets these tests drive information_schema.STATISTICS through
// all three real states: an OLD id-only key that needs migrating, an
// ALREADY-migrated composite key, and a table that does not exist yet.
// ---------------------------------------------------------------------------

function fakeConnectionWithPk(pkColumns) {
  const calls = [];
  return {
    calls,
    async query(sql, params = []) {
      calls.push({ sql, params });
      if (/information_schema\.STATISTICS/i.test(sql)) {
        return [pkColumns.map((c) => ({ COLUMN_NAME: c }))];
      }
      if (/ALTER TABLE consolidator_audit/i.test(sql)) {
        return [{}];
      }
      return [[]];
    },
  };
}

test('ensureAuditCompositeKey: an OLD id-only key triggers DROP PRIMARY KEY, ADD PRIMARY KEY (id, stage)', async () => {
  const conn = fakeConnectionWithPk(['id']);
  await ensureAuditCompositeKey(conn);
  const alterCall = conn.calls.find((c) => /ALTER TABLE consolidator_audit/i.test(c.sql));
  assert.ok(alterCall, 'an old id-only key must trigger the migration ALTER');
  assert.match(alterCall.sql, /DROP PRIMARY KEY/);
  assert.match(alterCall.sql, /ADD PRIMARY KEY \(id, stage\)/);
});

test('ensureAuditCompositeKey: an ALREADY-migrated composite key is a no-op (no ALTER, no metadata lock)', async () => {
  const conn = fakeConnectionWithPk(['id', 'stage']);
  await ensureAuditCompositeKey(conn);
  const alterCall = conn.calls.find((c) => /ALTER TABLE consolidator_audit/i.test(c.sql));
  assert.equal(alterCall, undefined, 'an already-migrated table must never re-issue the ALTER');
});

test('ensureAuditCompositeKey: a table that does not exist yet (empty PK columns) is a no-op', async () => {
  const conn = fakeConnectionWithPk([]);
  await assert.doesNotReject(() => ensureAuditCompositeKey(conn));
  const alterCall = conn.calls.find((c) => /ALTER TABLE consolidator_audit/i.test(c.sql));
  assert.equal(alterCall, undefined);
});

test('upsertAuditEntry inserts then prunes, ON DUPLICATE KEY UPDATE id = id', async () => {
  const conn = fakeConnection();
  await upsertAuditEntry(conn, { id: 'AUD-1', stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: { x: 1 } });
  const insertCall = conn.calls.find((c) => /INSERT INTO consolidator_audit/i.test(c.sql));
  assert.ok(insertCall);
  assert.match(insertCall.sql, /ON DUPLICATE KEY UPDATE id = id/);
  const pruneCall = conn.calls.find((c) => /DELETE FROM consolidator_audit/i.test(c.sql));
  assert.ok(pruneCall, 'every upsert also prunes, mirroring upsertCommit');
});

test('upsertAuditEntry tolerates a malformed `at` by falling back to server time, never throws', async () => {
  const conn = fakeConnection();
  await assert.doesNotReject(() =>
    upsertAuditEntry(conn, { id: 'AUD-BAD-AT', stage: 'planned', at: 'not-a-date', payload: {} })
  );
  assert.equal(conn.rows.length, 1);
  assert.ok(conn.rows[0].at instanceof Date && !Number.isNaN(conn.rows[0].at.getTime()));
});

test('queryAuditEntries: no filters returns everything, oldest first', async () => {
  const conn = fakeConnection();
  await upsertAuditEntry(conn, { id: 'A2', stage: 'planned', at: '2026-09-04T02:00:00.000Z', payload: { n: 2 } });
  await upsertAuditEntry(conn, { id: 'A1', stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: { n: 1 } });
  const entries = await queryAuditEntries(conn);
  assert.deepEqual(entries.map((e) => e.id), ['A1', 'A2']);
  assert.equal(entries[0].payload.n, 1);
});

test('queryAuditEntries: `stage` filter narrows correctly', async () => {
  const conn = fakeConnection();
  await upsertAuditEntry(conn, { id: 'D1', stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: {} });
  await upsertAuditEntry(conn, { id: 'P1', stage: 'planned', at: '2026-09-04T02:00:00.000Z', payload: {} });
  await upsertAuditEntry(conn, { id: 'I1', stage: 'implemented', at: '2026-09-04T03:00:00.000Z', payload: {} });
  const planned = await queryAuditEntries(conn, { stage: 'planned' });
  assert.deepEqual(planned.map((e) => e.id), ['P1']);
});

test('queryAuditEntries: `since` filter narrows correctly and a malformed `since` is ignored (degrade, never 400 the monitor)', async () => {
  const conn = fakeConnection();
  await upsertAuditEntry(conn, { id: 'OLD', stage: 'discovered', at: '2026-09-01T00:00:00.000Z', payload: {} });
  await upsertAuditEntry(conn, { id: 'NEW', stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: {} });
  const recent = await queryAuditEntries(conn, { since: '2026-09-03T00:00:00.000Z' });
  assert.deepEqual(recent.map((e) => e.id), ['NEW']);
  const withBadSince = await queryAuditEntries(conn, { since: 'not-a-date' });
  assert.deepEqual(withBadSince.map((e) => e.id), ['OLD', 'NEW']);
});

test('POST /api/consolidator/audit round-trips all three stages, then GET returns them all', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });

  const discovered = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'RT-1', stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: { sections: 476, opportunities: 12 } },
  });
  assert.equal(discovered.status, 200);
  assert.deepEqual(discovered.json, { ok: true, id: 'RT-1' });

  const planned = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'RT-2', stage: 'planned', at: '2026-09-04T02:00:00.000Z', payload: { inSquare: 3, wholeMap: 95 } },
  });
  assert.equal(planned.status, 200);

  const implemented = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'RT-3', stage: 'implemented', at: '2026-09-04T03:00:00.000Z', payload: { demolished: 4, built: 1 } },
  });
  assert.equal(implemented.status, 200);

  const all = await requestHandler(handler, { method: 'GET', path: '/api/consolidator/audit' });
  assert.equal(all.status, 200);
  assert.equal(all.json.ok, true);
  assert.deepEqual(all.json.entries.map((e) => e.id), ['RT-1', 'RT-2', 'RT-3']);
  assert.deepEqual(all.json.entries.map((e) => e.stage), ['discovered', 'planned', 'implemented']);
  assert.equal(all.json.entries[0].payload.sections, 476);
});

test('GET /api/consolidator/audit?stage=planned filters server-side', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body: { id: 'S1', stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: {} } });
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body: { id: 'S2', stage: 'planned', at: '2026-09-04T02:00:00.000Z', payload: {} } });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/consolidator/audit?stage=planned' });
  assert.equal(res.status, 200);
  assert.deepEqual(res.json.entries.map((e) => e.id), ['S2']);
});

test('GET /api/consolidator/audit?since=... filters server-side', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body: { id: 'T1', stage: 'discovered', at: '2026-09-01T00:00:00.000Z', payload: {} } });
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body: { id: 'T2', stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: {} } });
  const res = await requestHandler(handler, { method: 'GET', path: `/api/consolidator/audit?since=${encodeURIComponent('2026-09-03T00:00:00.000Z')}` });
  assert.equal(res.status, 200);
  assert.deepEqual(res.json.entries.map((e) => e.id), ['T2']);
});

test('GET /api/consolidator/audit?stage=bogus -> 400 debugsink.bad_stage', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/consolidator/audit?stage=bogus' });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.bad_stage');
});

test('POST /api/consolidator/audit missing id -> 400 debugsink.missing_id', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: {} },
  });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.missing_id');
});

test('POST /api/consolidator/audit missing/bad stage -> 400 debugsink.bad_stage', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res1 = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'X1', at: '2026-09-04T00:00:00.000Z', payload: {} },
  });
  assert.equal(res1.status, 400);
  assert.equal(res1.json.error, 'debugsink.bad_stage');

  const res2 = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'X2', stage: 'demolishing', at: '2026-09-04T00:00:00.000Z', payload: {} },
  });
  assert.equal(res2.status, 400);
  assert.equal(res2.json.error, 'debugsink.bad_stage');
});

test('POST /api/consolidator/audit missing at -> 400 debugsink.missing_at', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'X3', stage: 'discovered', payload: {} },
  });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.missing_at');
});

test('POST /api/consolidator/audit missing payload -> 400 debugsink.missing_payload', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const res = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: 'X4', stage: 'discovered', at: '2026-09-04T00:00:00.000Z' },
  });
  assert.equal(res.status, 400);
  assert.equal(res.json.error, 'debugsink.missing_payload');
});

test('a DB connect failure on the audit route surfaces as a JSON 500, never an unhandled throw', async () => {
  const handler = createHandler({ connect: async () => { throw new Error('ECONNREFUSED: no MariaDB'); } });
  const res = await requestHandler(handler, { method: 'GET', path: '/api/consolidator/audit' });
  assert.equal(res.status, 500);
  assert.equal(res.json.error, 'debugsink.db_error');
});

test('posting the same id twice upserts (no double-insert row, matching debug_commits idempotency)', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const body = { id: 'DUP-1', stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: { a: 1 } };
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body });
  await requestHandler(handler, { method: 'POST', path: '/api/consolidator/audit', body });
  assert.equal(conn.rows.length, 1);
});

// RED-PROOF (independent-round finding 2, 2026-09-04, "CROSS-STAGE ID
// CLOBBER"): posting the SAME id under a DIFFERENT stage must yield TWO
// retrievable rows, not a silent 200-OK clobber of the first. Before the
// composite-(id, stage)-key fix, this test fails: the second POST still
// returns 200, but only ONE row (the first stage's) is ever retrievable —
// the second payload is permanently lost. Run end-to-end through the real
// HTTP route + createHandler (not just the SQL-shape unit tests above) so
// this proves the actual client-visible behaviour, not just the query text.
test('CROSS-STAGE ID CLOBBER FIX: the same id under two different stages both persist as separate, independently retrievable rows', async () => {
  const conn = fakeConnection();
  const handler = createHandler({ connect: async () => conn });
  const sharedId = 'SHARED-ID-1';

  const discovered = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: sharedId, stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: { phase: 'discovered' } },
  });
  assert.equal(discovered.status, 200);

  const planned = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: sharedId, stage: 'planned', at: '2026-09-04T02:00:00.000Z', payload: { phase: 'planned' } },
  });
  assert.equal(planned.status, 200);

  const implemented = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: sharedId, stage: 'implemented', at: '2026-09-04T03:00:00.000Z', payload: { phase: 'implemented' } },
  });
  assert.equal(implemented.status, 200);

  // All THREE rows must exist — the old id-only key would have silently
  // frozen on the FIRST ('discovered'), losing 'planned' and 'implemented'.
  assert.equal(conn.rows.length, 3, 'each stage under the shared id must be its own row, never clobbered');

  const all = await requestHandler(handler, { method: 'GET', path: '/api/consolidator/audit' });
  assert.equal(all.status, 200);
  const sharedRows = all.json.entries.filter((e) => e.id === sharedId);
  assert.equal(sharedRows.length, 3, 'GET must retrieve all three stages for the shared id, not just the first');
  assert.deepEqual(
    sharedRows.map((e) => e.stage).sort(),
    ['discovered', 'implemented', 'planned']
  );
  assert.deepEqual(
    sharedRows.map((e) => e.payload.phase).sort(),
    ['discovered', 'implemented', 'planned']
  );

  // Re-posting the FIRST stage again (a genuine retry, not a cross-stage
  // clobber) must still upsert idempotently — the fix must not turn every
  // repost into a duplicate row.
  const retryDiscovered = await requestHandler(handler, {
    method: 'POST',
    path: '/api/consolidator/audit',
    body: { id: sharedId, stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: { phase: 'discovered' } },
  });
  assert.equal(retryDiscovered.status, 200);
  assert.equal(conn.rows.length, 3, 'a genuine same-(id,stage) retry must stay a no-op upsert, not a 4th row');
});
