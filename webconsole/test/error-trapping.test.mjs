// error-trapping.test.mjs — FEAT-1972079898 (GR#1): full-context error capture.
//
// Run with `npm test` (node --test). Exercises the SHIPPED backend.recordError
// path and its consumers: structured meta (type/stack/componentStack/correlation
// id/state summary), the render-crash component-stack capture, de-duplication,
// backward compatibility with the bare recordError(string) call, the extracted
// window 'error'/'unhandledrejection' handlers, and the richer debug.json errors[].
//
// errorLog is module-level singleton state shared across tests, so every test
// uses a UNIQUE message and looks its record up by message rather than assuming a
// clean log. Top-level node:test cases run sequentially, so ordering is stable.
//
// RED PROOF (documented, run via scratch-copy cp/mv — never git): temporarily
// break ErrorBoundary's componentStack pass-through OR backend's componentStack
// storage and the "render-crash records the component stack" test goes RED.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  recordError,
  recentErrors,
  errorListModel,
  reportWindowError,
  reportUnhandledRejection,
  updateLastKnownState,
} from '../src/sim/backend.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { initialState } from '../src/sim/engine.ts';

/** Find the (single) captured record for a unique message. */
function findByMsg(msg) {
  return recentErrors().filter((e) => e.msg === msg);
}

/** Non-sim inputs for buildDebugJson, mirroring debugjson.test.mjs. */
function testUi(overrides = {}) {
  return {
    appVersion: 'v9.9.9-test',
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: 42, showWater: true },
    errors: [],
    ...overrides,
  };
}

// ---------- structured meta ----------

test('recordError with structured meta stores type, stack, componentStack, correlation id, and a state summary', () => {
  updateLastKnownState({
    ...initialState(),
    tick: 123,
    funds: 4567,
    population: 89,
    speed: 2,
    policies: { tourismDrive: true },
  });
  const msg = 'meta-error-' + Math.random();
  recordError(msg, {
    type: 'render-crash',
    stack: 'Error: boom\n  at Foo (foo.ts:1:1)',
    componentStack: '\n    in App\n    in SimProvider',
    action: 'tick',
  });

  const rows = findByMsg(msg);
  assert.equal(rows.length, 1, 'exactly one record');
  const r = rows[0];
  assert.equal(r.type, 'render-crash');
  assert.equal(r.stack, 'Error: boom\n  at Foo (foo.ts:1:1)');
  assert.equal(r.componentStack, '\n    in App\n    in SimProvider');
  assert.equal(r.action, 'tick');
  assert.equal(typeof r.correlationId, 'number');
  assert.ok(r.correlationId > 0, 'correlation id assigned');
  assert.equal(r.count, 1);
  assert.ok(r.firstAt > 0 && r.lastAt > 0, 'timestamps set');
  // the heap: the bounded state snapshot at crash time
  assert.deepEqual(r.stateSummary, {
    tick: 123,
    funds: 4567,
    population: 89,
    speed: 2,
    policies: { tourismDrive: true },
  });
});

test('correlation ids are unique per distinct error', () => {
  const a = 'corr-a-' + Math.random();
  const b = 'corr-b-' + Math.random();
  recordError(a);
  recordError(b);
  const ra = findByMsg(a)[0];
  const rb = findByMsg(b)[0];
  assert.notEqual(ra.correlationId, rb.correlationId);
});

// ---------- render-crash component stack (the "what triggered it") ----------

test('a render-crash path records the component stack', () => {
  // Simulate ErrorBoundary.componentDidCatch: it passes error.stack AND
  // errorInfo.componentStack through recordError with type 'render-crash'.
  const err = new Error('useSim must be used inside SimProvider ' + Math.random());
  const componentStack = '\n    in DebugTab\n    in RightDock\n    in App';
  recordError(err.message, {
    type: 'render-crash',
    stack: err.stack,
    componentStack,
  });

  const r = findByMsg(err.message)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.componentStack, componentStack, 'component stack (the trigger) is retained');
  assert.equal(r.type, 'render-crash');
  assert.ok(r.stack && r.stack.includes('useSim'), 'JS stack retained');
});

// ---------- de-duplication ----------

test('de-dup: two identical errors collapse into one row with count 2 and first/last timestamps', () => {
  const msg = 'dup-spam-' + Math.random();
  const componentStack = '\n    in Consumer';
  recordError(msg, { type: 'render-crash', stack: 'first-stack', componentStack });
  recordError(msg, { type: 'render-crash', stack: 'second-stack', componentStack });

  const rows = findByMsg(msg);
  assert.equal(rows.length, 1, 'collapsed into a single row');
  const r = rows[0];
  assert.equal(r.count, 2, 'count incremented');
  assert.ok(r.firstAt <= r.lastAt, 'firstAt/lastAt both set and ordered');
  assert.equal(r.stack, 'first-stack', "first occurrence's stack is preserved (not lost)");
});

test('de-dup keys on message + component stack: different component stacks are distinct rows', () => {
  const msg = 'same-msg-' + Math.random();
  recordError(msg, { componentStack: '\n    in A' });
  recordError(msg, { componentStack: '\n    in B' });
  const rows = findByMsg(msg);
  assert.equal(rows.length, 2, 'different triggers → separate rows');
});

// ---------- backward compatibility ----------

test('backward-compat: recordError(string) still works and defaults type to app', () => {
  const msg = 'bare-call-' + Math.random();
  recordError(msg);
  const r = findByMsg(msg)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.type, 'app');
  assert.equal(r.count, 1);
  assert.equal(r.stack, undefined);
  assert.equal(r.componentStack, undefined);
});

// ---------- window 'error' / 'unhandledrejection' handlers ----------

test("window 'error' handler passes the message AND the stack", () => {
  const err = new Error('window boom ' + Math.random());
  reportWindowError({ message: err.message, error: err });
  const r = findByMsg(err.message)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.type, 'window-error');
  assert.ok(r.stack && r.stack.includes('window boom'), 'error.stack captured (was dropped before)');
});

test("window 'unhandledrejection' handler passes the reason's stack", () => {
  const reason = new Error('rejection boom ' + Math.random());
  reportUnhandledRejection(reason);
  const rows = recentErrors().filter((e) => e.msg.includes(reason.message));
  assert.equal(rows.length, 1);
  assert.equal(rows[0].type, 'unhandledrejection');
  assert.ok(rows[0].stack && rows[0].stack.includes('rejection boom'), 'reason stack captured');
});

// ---------- presentation model ----------

test('errorListModel exposes the full context for panel expansion', () => {
  const msg = 'panel-row-' + Math.random();
  recordError(msg, {
    type: 'render-crash',
    stack: 'S',
    componentStack: 'C',
  });
  const model = errorListModel(recentErrors());
  const row = model.rows.find((r) => r.msg === msg);
  assert.ok(row, 'row present');
  assert.equal(row.type, 'render-crash');
  assert.equal(row.stack, 'S');
  assert.equal(row.componentStack, 'C');
  assert.equal(typeof row.correlationId, 'number');
  assert.equal(row.count, 1);
  assert.equal(typeof row.firstTime, 'string');
  assert.equal(typeof row.lastTime, 'string');
});

test('errorListModel tolerates a legacy {at,msg} row (backward compatible)', () => {
  const at = Date.UTC(2026, 7, 26, 14, 30, 5);
  const model = errorListModel([{ at, msg: 'legacy' }]);
  assert.equal(model.rows.length, 1);
  assert.equal(model.rows[0].msg, 'legacy');
  assert.equal(model.rows[0].time, new Date(at).toLocaleTimeString());
  assert.notEqual(model.rows[0].time, 'Invalid Date');
});

// ---------- debug.json surfaces the richer fields ----------

test('debug.json errors[] carries the richer fields (correlation id, stack, component stack, state summary)', () => {
  const msg = 'debugjson-error-' + Math.random();
  updateLastKnownState({ ...initialState(), tick: 7, funds: 100, population: 3, speed: 1, policies: {} });
  recordError(msg, { type: 'render-crash', stack: 'DJ-stack', componentStack: 'DJ-cstack' });
  const captured = recentErrors().filter((e) => e.msg === msg);

  const dj = buildDebugJson(initialState(), testUi({ errors: captured }));
  assert.equal(dj.errors.length, 1);
  const e = dj.errors[0];
  assert.equal(e.msg, msg);
  assert.equal(e.type, 'render-crash');
  assert.equal(e.stack, 'DJ-stack');
  assert.equal(e.componentStack, 'DJ-cstack');
  assert.equal(typeof e.correlationId, 'number');
  assert.equal(e.count, 1);
  assert.ok(e.stateSummary && e.stateSummary.tick === 7, 'state summary heap attached');
});
