/**
 * claude-dispatch-log.test.js — unit tests for claude-dispatch-log.js
 * (FEAT-076, BOW mkey: tool.agentlog).
 *
 * Pure-logic only, no DB — every DB-touching function here (insertEvent,
 * resolveTargetLanes) is exercised against a mock `{ query() }` stub, never
 * a live MariaDB connection, per docs/planning/acceptance/tool.agentlog.md
 * AC-24. Mirrors claude-dispatch-guard.test.js's plain node:test style.
 *
 * Run: node --test claude-dispatch-log.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  DEFAULT_CAP_MS,
  DEFAULT_TARGET_LANES,
  buildDispatchEvent,
  buildStopEvent,
  insertEvent,
  sweepConcurrency,
  currentRunning,
  bucketHours,
  resolveTargetLanes,
  formatUtilTable,
  buildNag,
} = require('./claude-dispatch-log.js');

// ── buildDispatchEvent / buildStopEvent (AC-3 / AC-10 row shapes) ──────────

test('buildDispatchEvent produces the full row shape with bowCodes array joined', () => {
  const row = buildDispatchEvent({
    sessionId: 'sess-1',
    name: 'bill',
    subagentType: 'general-purpose',
    description: 'build the thing',
    bowCodes: ['FEAT-076', 'BUG-010'],
    promptChars: 1234,
  });
  assert.equal(row.event, 'dispatch');
  assert.equal(row.session_id, 'sess-1');
  assert.equal(row.name, 'bill');
  assert.equal(row.subagent_type, 'general-purpose');
  assert.equal(row.description, 'build the thing');
  assert.equal(row.bow_codes, 'FEAT-076,BUG-010');
  assert.equal(row.prompt_chars, 1234);
});

test('buildDispatchEvent truncates description to 255 chars (VARCHAR(255) column)', () => {
  const long = 'x'.repeat(300);
  const row = buildDispatchEvent({ sessionId: 's', name: 'bill', description: long });
  assert.equal(row.description.length, 255);
  assert.equal(row.description, 'x'.repeat(255));
});

test('buildDispatchEvent truncates name to 16 chars (VARCHAR(16) column)', () => {
  const row = buildDispatchEvent({ sessionId: 's', name: 'a-very-long-identity-name' });
  assert.equal(row.name.length, 16);
});

test('buildDispatchEvent leaves promptChars null when not a finite number', () => {
  const row = buildDispatchEvent({ sessionId: 's', name: 'bill', promptChars: undefined });
  assert.equal(row.prompt_chars, null);
});

test('buildStopEvent produces a stop row with name always null (identity not known at stop time)', () => {
  const row = buildStopEvent({ sessionId: 'sess-1', subagentType: 'general-purpose', description: 'done' });
  assert.equal(row.event, 'stop');
  assert.equal(row.session_id, 'sess-1');
  assert.equal(row.name, null);
  assert.equal(row.subagent_type, 'general-purpose');
  assert.equal(row.description, 'done');
  assert.equal(row.bow_codes, null);
  assert.equal(row.prompt_chars, null);
});

// ── insertEvent (mock conn only — AC-24) ────────────────────────────────────

test('insertEvent issues one INSERT against sync_dispatch_events with the row\'s own fields, in column order', async () => {
  const calls = [];
  const conn = { query: async (sql, params) => { calls.push({ sql, params }); return [{}]; } };
  const row = buildDispatchEvent({ sessionId: 's1', name: 'bill', subagentType: 'x', description: 'y', bowCodes: ['FEAT-076'], promptChars: 5 });
  await insertEvent(conn, row);
  assert.equal(calls.length, 1);
  assert.match(calls[0].sql, /INSERT INTO sync_dispatch_events/);
  assert.deepEqual(calls[0].params, ['dispatch', 's1', 'bill', 'x', 'y', 'FEAT-076', 5]);
});

// ── sweepConcurrency / currentRunning (AC-12 / AC-13) ───────────────────────

test('AC-12: two overlapping same-session dispatches of different durations produce an exact running count regardless of FIFO pairing ambiguity', () => {
  // Session s1: dispatch@0, dispatch@10 (both now open), stop@30, stop@50.
  // Whichever specific dispatch each stop is "really" closing, the OPEN
  // COUNT at any instant is unambiguous: 1 at [0,10), 2 at [10,30), 1 at
  // [30,50), 0 after 50.
  const rows = [
    { event: 'dispatch', ts: 0, session_id: 's1', name: 'bill' },
    { event: 'dispatch', ts: 10, session_id: 's1', name: 'bill' },
    { event: 'stop', ts: 30, session_id: 's1' },
    { event: 'stop', ts: 50, session_id: 's1' },
  ];
  assert.equal(currentRunning(rows, { now: 5 }).running, 1);
  assert.equal(currentRunning(rows, { now: 20 }).running, 2);
  assert.equal(currentRunning(rows, { now: 40 }).running, 1);
  assert.equal(currentRunning(rows, { now: 60 }).running, 0);
});

test('AC-13: an orphan stop (no open dispatch) never drives the counter negative', () => {
  const rows = [{ event: 'stop', ts: 0, session_id: 's1' }];
  const { intervals, unmatchedCount } = sweepConcurrency(rows, {});
  assert.equal(intervals.length, 0);
  assert.equal(unmatchedCount, 0);
  assert.equal(currentRunning(rows, { now: 100 }).running, 0);
});

test('AC-13: an unmatched dispatch (no stop ever) is closed synthetically at +capMs and counted', () => {
  const rows = [{ event: 'dispatch', ts: 0, session_id: 's2', name: 'bob' }];
  const capMs = 1000;
  const { intervals, unmatchedCount } = sweepConcurrency(rows, { capMs });
  assert.equal(unmatchedCount, 1);
  assert.equal(intervals.length, 1);
  assert.equal(intervals[0].start, 0);
  assert.equal(intervals[0].end, capMs);
  assert.equal(intervals[0].matched, false);
  assert.equal(currentRunning(rows, { capMs, now: 500 }).running, 1);
  assert.equal(currentRunning(rows, { capMs, now: 1500 }).running, 0);
});

test('sweepConcurrency defaults capMs to DEFAULT_CAP_MS (2h) when not supplied', () => {
  const rows = [{ event: 'dispatch', ts: 0, session_id: 's3', name: 'ben' }];
  const { intervals } = sweepConcurrency(rows, {});
  assert.equal(intervals[0].end, DEFAULT_CAP_MS);
});

test('currentRunning gives per-identity breakdown, floored at 0 per identity', () => {
  const rows = [
    { event: 'dispatch', ts: 0, session_id: 's1', name: 'bill' },
    { event: 'dispatch', ts: 0, session_id: 's2', name: 'bob' },
    { event: 'stop', ts: 5, session_id: 's2' },
  ];
  const { running, byName } = currentRunning(rows, { now: 10 });
  assert.equal(running, 1);
  assert.equal(byName.bill, 1);
  assert.equal(byName.bob, 0);
});

// ── bucketHours (AC-14) ──────────────────────────────────────────────────

const H = 60 * 60 * 1000;
// Fixed UTC hour boundary, arbitrary but stable — 2026-08-13T10:00:00.000Z.
const H10 = Date.UTC(2026, 7, 13, 10, 0, 0);

test('AC-14: an interval spanning an hour boundary contributes time-weighted average to EACH bucket it overlaps', () => {
  // bill: dispatch at 09:30, stop at 10:30 -> 30min in [09:00,10:00) and
  // 30min in [10:00,11:00).
  const rows = [
    { event: 'dispatch', ts: H10 - 30 * 60 * 1000, session_id: 's1', name: 'bill' },
    { event: 'stop', ts: H10 + 30 * 60 * 1000, session_id: 's1' },
  ];
  const now = H10 + H; // 11:00:00.000 — exactly on an hour boundary
  const buckets = bucketHours(rows, { hours: 2, now, targetLanes: 2 });

  const hour0900 = buckets.filter((r) => r.hourLabel === '2026-08-13 09:00');
  const hour1000 = buckets.filter((r) => r.hourLabel === '2026-08-13 10:00');

  const bill0900 = hour0900.find((r) => r.who === 'bill');
  const bill1000 = hour1000.find((r) => r.who === 'bill');
  assert.ok(bill0900, 'bill should have a row in the 09:00 bucket');
  assert.ok(bill1000, 'bill should have a row in the 10:00 bucket');
  assert.equal(bill0900.avg, 0.5);
  assert.equal(bill1000.avg, 0.5);
  assert.equal(bill0900.disp, 1); // dispatch itself started in this bucket
  assert.equal(bill0900.done, 0);
  assert.equal(bill1000.disp, 0);
  assert.equal(bill1000.done, 1); // stop landed in this bucket

  const team0900 = hour0900.find((r) => r.who === 'TEAM');
  const team1000 = hour1000.find((r) => r.who === 'TEAM');
  assert.equal(team0900.avg, 0.5);
  assert.equal(team1000.avg, 0.5);
  assert.equal(team0900.util, 0.25); // avg 0.5 / targetLanes 2
});

test('AC-14: hours with zero events still yield a TEAM zero row (sags must be visible)', () => {
  const rows = [
    { event: 'dispatch', ts: H10 - 30 * 60 * 1000, session_id: 's1', name: 'bill' },
    { event: 'stop', ts: H10 + 30 * 60 * 1000, session_id: 's1' },
  ];
  const now = H10 + H;
  // hours: 3 -> buckets 08:00, 09:00, 10:00. 08:00 has no events at all.
  const buckets = bucketHours(rows, { hours: 3, now, targetLanes: 2 });
  const hour0800 = buckets.filter((r) => r.hourLabel === '2026-08-13 08:00');
  assert.equal(hour0800.length, 1); // TEAM only, no per-identity rows
  assert.equal(hour0800[0].who, 'TEAM');
  assert.equal(hour0800[0].disp, 0);
  assert.equal(hour0800[0].done, 0);
  assert.equal(hour0800[0].peak, 0);
  assert.equal(hour0800[0].avg, 0);
});

test('bucketHours peak reflects true simultaneous overlap, not a sum of individual identities', () => {
  // Three agents dispatched in the same hour, but only bill+bob ever overlap
  // (minutes 5-40 and 10-45); ben runs alone afterwards (minutes 50-55). A
  // buggy "peak = distinct dispatch count" implementation would report 3;
  // the true max-simultaneous-overlap is 2.
  const rows = [
    { event: 'dispatch', ts: H10 + 5 * 60 * 1000, session_id: 's1', name: 'bill' },
    { event: 'stop', ts: H10 + 40 * 60 * 1000, session_id: 's1' },
    { event: 'dispatch', ts: H10 + 10 * 60 * 1000, session_id: 's2', name: 'bob' },
    { event: 'stop', ts: H10 + 45 * 60 * 1000, session_id: 's2' },
    { event: 'dispatch', ts: H10 + 50 * 60 * 1000, session_id: 's3', name: 'ben' },
    { event: 'stop', ts: H10 + 55 * 60 * 1000, session_id: 's3' },
  ];
  const now = H10 + H;
  const buckets = bucketHours(rows, { hours: 1, now, targetLanes: 12 });
  const team = buckets.find((r) => r.who === 'TEAM');
  assert.equal(team.peak, 2); // bill+bob overlap; ben never overlaps either — sum would wrongly be 3
  assert.equal(team.disp, 3);
  assert.equal(team.done, 3);
});

// ── resolveTargetLanes (AC-15, mock conn) ───────────────────────────────────

test('resolveTargetLanes reads project_meta.dispatch_target_lanes when present', async () => {
  const conn = { query: async () => [[{ meta_value: '20' }]] };
  const result = await resolveTargetLanes(conn);
  assert.equal(result.target, 20);
  assert.equal(result.source, 'project_meta.dispatch_target_lanes');
});

test('resolveTargetLanes defaults to 12 when the row is absent', async () => {
  const conn = { query: async () => [[]] };
  const result = await resolveTargetLanes(conn);
  assert.equal(result.target, DEFAULT_TARGET_LANES);
  assert.match(result.source, /default/);
});

test('resolveTargetLanes defaults to 12 when project_meta does not exist yet (ER_NO_SUCH_TABLE)', async () => {
  const conn = { query: async () => { const e = new Error('no such table'); e.code = 'ER_NO_SUCH_TABLE'; throw e; } };
  const result = await resolveTargetLanes(conn);
  assert.equal(result.target, DEFAULT_TARGET_LANES);
  assert.match(result.source, /default/);
});

test('resolveTargetLanes propagates a genuine (non-missing-table) DB error rather than silently defaulting', async () => {
  const conn = { query: async () => { throw new Error('connection reset'); } };
  await assert.rejects(() => resolveTargetLanes(conn), /connection reset/);
});

// ── formatUtilTable (AC-16, golden string) ──────────────────────────────────

test('formatUtilTable renders a fixed, padded table (golden string)', () => {
  const rows = [
    { hourLabel: '2026-08-13 09:00', who: 'TEAM', disp: 3, done: 2, peak: 2, avg: 1.4, util: 0.1167 },
    { hourLabel: '2026-08-13 09:00', who: 'bill', disp: 3, done: 2, peak: 2, avg: 1.4, util: 0.1167 },
  ];
  const expected =
    'HOUR            WHO     DISP DONE PEAK AVG   UTIL% \n' +
    '2026-08-13 09:00TEAM    3    2    2    1.4   11.7% \n' +
    '2026-08-13 09:00bill    3    2    2    1.4   11.7% ';
  assert.equal(formatUtilTable(rows), expected);
});

test('formatUtilTable on an empty row list is just the header', () => {
  const out = formatUtilTable([]);
  assert.equal(out, 'HOUR            WHO     DISP DONE PEAK AVG   UTIL% ');
});

// ── buildNag (AC-20) ─────────────────────────────────────────────────────

test('buildNag emits the exact PostToolUse hookSpecificOutput shape when under target with ready work', () => {
  const nag = buildNag(3, 12, 5);
  assert.deepEqual(nag, {
    hookSpecificOutput: {
      hookEventName: 'PostToolUse',
      additionalContext:
        'UTILISATION: 3/12 lanes running, 5 ready BOW items — standing order is load up, never hold. Dispatch until saturated or name the blocker.',
    },
  });
});

test('buildNag returns null when running >= target (already saturated)', () => {
  assert.equal(buildNag(12, 12, 5), null);
  assert.equal(buildNag(15, 12, 5), null);
});

test('buildNag returns null when there is no ready work', () => {
  assert.equal(buildNag(0, 12, 0), null);
});
