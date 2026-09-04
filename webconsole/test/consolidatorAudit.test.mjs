// consolidatorAudit.test.mjs — CONSOLIDATOR AUDIT TRAIL client half (Aaron's
// ruling on FEAT-2326609761, see src/sim/consolidatorAudit.ts's file header).
//
// Covers: never-throws when the sink is down, posts correctly (URL/method/
// body shape) when it's up, and the per-stage once-per-simulated-month
// throttle (never per tick).
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  postConsolidatorAudit,
  isAuditDue,
  resetConsolidatorAuditThrottle,
} from '../src/sim/consolidatorAudit.ts';

/** Install a fake global.fetch for the duration of the wrapped test — mirrors debugtab.test.mjs's withFakeFetch idiom exactly. */
function withFakeFetch(impl, fn) {
  return async (...args) => {
    const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'fetch');
    const calls = [];
    Object.defineProperty(globalThis, 'fetch', {
      value: async (url, init) => {
        calls.push({ url, init });
        return impl(url, init);
      },
      configurable: true,
      writable: true,
    });
    try {
      await fn(calls, ...args);
    } finally {
      if (originalDescriptor) {
        Object.defineProperty(globalThis, 'fetch', originalDescriptor);
      } else {
        delete globalThis.fetch;
      }
      resetConsolidatorAuditThrottle();
    }
  };
}

// ---------- isAuditDue (pure throttle gate) ----------

test('isAuditDue: due by default (never posted this session)', () => {
  resetConsolidatorAuditThrottle();
  assert.equal(isAuditDue('discovered', { twelfth: 3 }), true);
});

// ---------- never throws ----------

test(
  'postConsolidatorAudit: a rejecting fetch resolves false, never throws',
  withFakeFetch(
    async () => {
      throw new Error('network is down');
    },
    async () => {
      resetConsolidatorAuditThrottle();
      const ok = await postConsolidatorAudit('discovered', { twelfth: 0 }, 100, { x: 1 }, '2026-09-04T00:00:00.000Z');
      assert.equal(ok, false);
    }
  )
);

test(
  'postConsolidatorAudit: a non-2xx response resolves false, never throws',
  withFakeFetch(
    async () => ({ ok: false, status: 500 }),
    async () => {
      resetConsolidatorAuditThrottle();
      const ok = await postConsolidatorAudit('planned', { twelfth: 0 }, 100, { x: 1 }, '2026-09-04T00:00:00.000Z');
      assert.equal(ok, false);
    }
  )
);

test('postConsolidatorAudit: no global fetch at all resolves false, never throws', async () => {
  resetConsolidatorAuditThrottle();
  const original = globalThis.fetch;
  delete globalThis.fetch;
  try {
    const ok = await postConsolidatorAudit('implemented', { twelfth: 0 }, 100, {}, '2026-09-04T00:00:00.000Z');
    assert.equal(ok, false);
  } finally {
    if (original) globalThis.fetch = original;
  }
});

// ---------- posts correctly when up ----------

test(
  'postConsolidatorAudit: a 2xx POSTs the exact route with {id, stage, at, payload}',
  withFakeFetch(
    async () => ({ ok: true, status: 200 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      const ok = await postConsolidatorAudit(
        'discovered',
        { twelfth: 5 },
        42,
        { sections: 476, opportunities: 12 },
        '2026-09-04T12:00:00.000Z'
      );
      assert.equal(ok, true);
      assert.equal(calls.length, 1);
      assert.equal(calls[0].url, 'http://127.0.0.1:8642/api/consolidator/audit');
      assert.equal(calls[0].init.method, 'POST');
      assert.equal(calls[0].init.headers['Content-Type'], 'application/json');
      const sent = JSON.parse(calls[0].init.body);
      assert.equal(sent.stage, 'discovered');
      assert.equal(sent.at, '2026-09-04T12:00:00.000Z');
      assert.deepEqual(sent.payload, { sections: 476, opportunities: 12 });
      assert.match(sent.id, /^AUD-DISC-42$/);
    }
  )
);

test(
  'postConsolidatorAudit: id is derived from stage+tick (deterministic, no clock read)',
  withFakeFetch(
    async () => ({ ok: true, status: 200 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      await postConsolidatorAudit('implemented', { twelfth: 1 }, 777, {}, '2026-09-04T00:00:00.000Z');
      const sent = JSON.parse(calls[0].init.body);
      assert.equal(sent.id, 'AUD-IMPL-777');
    }
  )
);

// ---------- throttle: once per month, never per tick ----------

test(
  'throttle: a second post for the SAME stage+twelfth is skipped (no second fetch call)',
  withFakeFetch(
    async () => ({ ok: true, status: 200 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      const scope = { twelfth: 2 };
      const ok1 = await postConsolidatorAudit('planned', scope, 10, { a: 1 }, '2026-09-04T00:00:00.000Z');
      const ok2 = await postConsolidatorAudit('planned', scope, 11, { a: 2 }, '2026-09-04T00:00:01.000Z'); // simulates "same month, one tick later"
      assert.equal(ok1, true);
      assert.equal(ok2, false, 'throttled — not an error, just already posted this simulated month');
      assert.equal(calls.length, 1, 'exactly one network call, never per-tick flooding');
    }
  )
);

test(
  'throttle: a post for a DIFFERENT twelfth (the next simulated month) is allowed through',
  withFakeFetch(
    async () => ({ ok: true, status: 200 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      await postConsolidatorAudit('planned', { twelfth: 2 }, 10, {}, '2026-09-04T00:00:00.000Z');
      const ok = await postConsolidatorAudit('planned', { twelfth: 3 }, 5000, {}, '2026-10-04T00:00:00.000Z');
      assert.equal(ok, true);
      assert.equal(calls.length, 2);
    }
  )
);

test(
  'throttle: stages are gated INDEPENDENTLY — a "discovered" post does not block "planned" in the same month',
  withFakeFetch(
    async () => ({ ok: true, status: 200 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      const scope = { twelfth: 0 };
      const okDiscovered = await postConsolidatorAudit('discovered', scope, 1, {}, '2026-09-04T00:00:00.000Z');
      const okPlanned = await postConsolidatorAudit('planned', scope, 1, {}, '2026-09-04T00:00:00.000Z');
      assert.equal(okDiscovered, true);
      assert.equal(okPlanned, true);
      assert.equal(calls.length, 2);
    }
  )
);

test(
  'throttle: a FAILED post does not consume the throttle — the next call in the same month retries',
  withFakeFetch(
    async () => ({ ok: false, status: 503 }),
    async (calls) => {
      resetConsolidatorAuditThrottle();
      const scope = { twelfth: 0 };
      const ok1 = await postConsolidatorAudit('discovered', scope, 1, {}, '2026-09-04T00:00:00.000Z');
      assert.equal(ok1, false);
      assert.equal(isAuditDue('discovered', scope), true, 'a failed post must not falsely mark the month as already audited');
    }
  )
);
