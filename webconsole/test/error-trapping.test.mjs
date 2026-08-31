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
import { readFileSync } from 'node:fs';
import {
  recordError,
  recentErrors,
  errorListModel,
  reportWindowError,
  reportUnhandledRejection,
  updateLastKnownState,
  normalizeThrowable,
  tapConsoleError,
  ERROR_RING_CAP,
  codedError,
  installConsoleTap,
  withTapSuppressed,
  setAppVersion,
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

// ---------- BUG-442: non-Error normalization ----------

test('normalizeThrowable: Error pass-through', () => {
  const err = new Error('original');
  const norm = normalizeThrowable(err);
  assert.equal(norm, err, 'Error objects pass through unchanged');
  assert.equal(norm.message, 'original');
});

test('normalizeThrowable: string → Error with type tag', () => {
  const norm = normalizeThrowable('string error ' + Math.random());
  assert.ok(norm instanceof Error, 'string coerces to Error');
  assert.equal(norm.type, 'string');
  assert.ok(norm.message.includes('string error'));
});

test('normalizeThrowable: null → Error with type tag', () => {
  const norm = normalizeThrowable(null);
  assert.ok(norm instanceof Error, 'null coerces to Error');
  assert.equal(norm.type, 'null');
  assert.equal(norm.message, 'null');
});

test('normalizeThrowable: undefined → Error with type tag', () => {
  const norm = normalizeThrowable(undefined);
  assert.ok(norm instanceof Error, 'undefined coerces to Error');
  assert.equal(norm.type, 'undefined');
  assert.equal(norm.message, 'undefined');
});

test('normalizeThrowable: object without message → Error with type tag', () => {
  const norm = normalizeThrowable({ error: 'bad thing' });
  assert.ok(norm instanceof Error, 'object coerces to Error');
  assert.equal(norm.type, 'object');
  assert.ok(norm.message);
});

// ---------- FEAT-1972079916: error codes and persistence ----------

test('recordError with code stores the registry code in the record', () => {
  const msg = 'coded-error-' + Math.random();
  recordError(msg, { type: 'app', code: 'MET-V800' });
  const r = findByMsg(msg)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.code, 'MET-V800');
  assert.ok(r.appVersion, 'appVersion is set when code is provided');
  assert.equal(typeof r.timestamp, 'number', 'timestamp is set');
});

test('recordError without code has undefined code', () => {
  const msg = 'no-code-error-' + Math.random();
  recordError(msg);
  const r = findByMsg(msg)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.code, undefined);
});

test('tap console.error records error calls in the ring', () => {
  const msg = 'console-tap-' + Math.random();
  const err = new Error(msg);
  tapConsoleError(err);
  const r = findByMsg(msg)[0];
  assert.ok(r, 'console.error tap recorded the error');
  assert.equal(r.type, 'app');
  assert.equal(r.code, 'MET-V804'); // ConsoleErrorTrap
});

test('tap console.error normalizes string arguments', () => {
  const msg = 'console-string-' + Math.random();
  tapConsoleError(msg);
  const r = findByMsg(msg)[0];
  assert.ok(r, 'console.error tap recorded the string');
  assert.equal(r.code, 'MET-V804');
});

test('ERROR_RING_CAP is exported and is a positive integer', () => {
  assert.equal(typeof ERROR_RING_CAP, 'number');
  assert.ok(ERROR_RING_CAP > 0);
  assert.ok(ERROR_RING_CAP >= 100, 'recommended cap of ~100');
});

test('report window error with code', () => {
  const msg = 'window-coded-' + Math.random();
  const err = new Error(msg);
  reportWindowError({ message: msg, error: err });
  const r = findByMsg(msg)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.type, 'window-error');
  assert.equal(r.code, 'MET-V802'); // WindowError
});

test('report unhandled rejection with code', () => {
  const msg = 'rejection-coded-' + Math.random();
  const err = new Error(msg);
  reportUnhandledRejection(err);
  const r = recentErrors().find((e) => e.msg.includes(msg));
  assert.ok(r, 'record captured');
  assert.equal(r.type, 'unhandledrejection');
  assert.equal(r.code, 'MET-V803'); // UnhandledRejection
});

// ---------- round-r1 BAR-F1: codedError + named codes win over generics ----------

test('BAR-F1: codedError builds an Error carrying a real .code property', () => {
  const err = codedError('MET-V800', 'useSim must be used inside SimProvider');
  assert.ok(err instanceof Error);
  assert.equal(err.code, 'MET-V800');
  assert.equal(err.message, 'useSim must be used inside SimProvider');
});

test('BAR-F1: a normalized error carrying .code wins over the window-error generic MET-V802', () => {
  const named = codedError('MET-V800', 'named-window-' + Math.random());
  reportWindowError({ message: named.message, error: named });
  const r = findByMsg(named.message)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.code, 'MET-V800', 'the NAMED code must win over the generic MET-V802 fallback');
});

test('BAR-F1: a normalized error carrying .code wins over the unhandledrejection generic MET-V803', () => {
  const named = codedError('MET-V806', 'named-rejection-' + Math.random());
  reportUnhandledRejection(named);
  const r = recentErrors().find((e) => e.msg.includes(named.message));
  assert.ok(r, 'record captured');
  assert.equal(r.code, 'MET-V806', 'the NAMED code must win over the generic MET-V803 fallback');
});

test('BAR-F1: a normalized error carrying .code wins over the console-tap generic MET-V804', () => {
  const named = codedError('MET-V807', 'named-console-' + Math.random());
  tapConsoleError(named);
  const r = findByMsg(named.message)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.code, 'MET-V807', 'the NAMED code must win over the generic MET-V804 fallback');
});

// ---------- round-r1 BAR-F2: normalizeThrowable must NEVER throw ----------

test('BAR-F2: normalizeThrowable survives an evil toString() object', () => {
  const evil = { toString() { throw new Error('evil toString'); } };
  const norm = normalizeThrowable(evil);
  assert.ok(norm instanceof Error, 'still returns an Error');
  assert.ok(norm.message.length > 0, 'a fallback message is present');
});

test('BAR-F2: normalizeThrowable survives a Proxy that throws on every get trap', () => {
  const evilProxy = new Proxy(
    {},
    {
      get() {
        throw new Error('proxy get trap fired');
      },
    },
  );
  const norm = normalizeThrowable(evilProxy);
  assert.ok(norm instanceof Error, 'still returns an Error');
  assert.ok(norm.message.length > 0, 'a fallback message is present');
});

test('BAR-F2: normalizeThrowable survives a bare Symbol', () => {
  const norm = normalizeThrowable(Symbol('boom'));
  assert.ok(norm instanceof Error, 'still returns an Error');
  assert.equal(norm.type, 'symbol');
});

test('BAR-F2: reportWindowError/reportUnhandledRejection/tapConsoleError never throw on an evil-toString value', () => {
  const evil = { toString() { throw new Error('evil toString in trap channel'); } };
  assert.doesNotThrow(() => reportWindowError({ message: undefined, error: evil }));
  assert.doesNotThrow(() => reportUnhandledRejection(evil));
  assert.doesNotThrow(() => tapConsoleError(evil));
  // and each still recorded a well-formed entry (proves no silent swallow either)
  const rows = recentErrors();
  assert.ok(rows.length > 0);
});

test('BAR-F2: reportWindowError/reportUnhandledRejection/tapConsoleError never throw on a throwing Proxy', () => {
  const evilProxy = new Proxy(
    {},
    {
      get() {
        throw new Error('proxy trap in trap channel');
      },
    },
  );
  assert.doesNotThrow(() => reportWindowError({ message: undefined, error: evilProxy }));
  assert.doesNotThrow(() => reportUnhandledRejection(evilProxy));
  assert.doesNotThrow(() => tapConsoleError(evilProxy));
});

// ---------- round-r1 BAR-F3: real appVersion, not the require() hack ----------
//
// version.ts (the real version module) is exercised end-to-end from the TSX
// side in error-boundary.test.tsx (tsx's resolver handles version.ts's own
// extensionless `../generated/version` import; a plain `node --test` run of
// this .mjs suite cannot — this is exactly why backend.ts uses the setter
// pattern below instead of a static import of version.ts). Here we verify the
// SETTER contract itself: once set, recordError stamps that exact value, and
// 'unknown' is never hit once a real version has been wired.

test('BAR-F3: setAppVersion wires a real version string; recordError with a code stamps EXACTLY that value', () => {
  const fakeRealVersion = 'v9.9.9-151-gdeadbee-real-' + Math.random();
  setAppVersion(fakeRealVersion);
  const msg = 'appver-check-' + Math.random();
  recordError(msg, { code: 'MET-V800' });
  const r = findByMsg(msg)[0];
  assert.ok(r, 'record captured');
  assert.equal(r.appVersion, fakeRealVersion, 'appVersion must equal exactly what setAppVersion wired in');
  assert.notEqual(r.appVersion, 'unknown', 'the unknown fallback must NOT be hit once a real version is wired');
});

test('BAR-F3: main.tsx wires setAppVersion(versionRaw) from the real version module (wiring tripwire)', () => {
  const src = readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8');
  assert.match(src, /import\s*\{[^}]*versionRaw[^}]*\}\s*from\s*'\.\/sim\/version'/, 'main.tsx must import versionRaw from ./sim/version');
  assert.match(src, /setAppVersion\(versionRaw\)/, 'main.tsx must call setAppVersion(versionRaw)');
});

// ---------- round-r1 BAR-F4: real cap (ERROR_RING_CAP, not a hardcoded literal) ----------

test('BAR-F4: inserting 120 unique errors caps the ring at exactly ERROR_RING_CAP, newest kept, oldest gone', () => {
  // This sandbox's global `localStorage` exists but its setItem throws
  // ("localStorage.setItem is not a function" under this Node build), which
  // would otherwise make EVERY recordError() also mint a second, unrelated
  // MET-V805 "write failed" ring row via persistErrorRing's catch path,
  // masking which of the two cap sites (recordError's own vs
  // persistErrorRing's catch) actually enforces the cap. Install a WORKING
  // in-memory localStorage stub for the duration of this test so
  // persistErrorRing succeeds cleanly and the cap we observe is purely
  // recordError's own — the exact site this bar's mutation-proof targets.
  const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const mem = new Map();
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (k) => (mem.has(k) ? mem.get(k) : null),
      setItem: (k, v) => void mem.set(k, String(v)),
    },
    configurable: true,
  });

  try {
    const tag = 'ring-cap-' + Math.random();
    for (let i = 0; i < 120; i++) {
      recordError(`${tag}-${i}`);
    }
    const all = recentErrors();
    assert.equal(all.length, ERROR_RING_CAP, `ring must cap at exactly ERROR_RING_CAP (${ERROR_RING_CAP})`);
    assert.equal(all[0].msg, `${tag}-119`, 'the newest inserted error is at the front');
    assert.equal(findByMsg(`${tag}-0`).length, 0, 'the oldest of the 120 inserted errors must have been evicted');
  } finally {
    if (originalDescriptor) {
      Object.defineProperty(globalThis, 'localStorage', originalDescriptor);
    } else {
      delete globalThis.localStorage;
    }
  }
});

// ---------- round-r1 BAR-F6 / BAR-K4b: console tap install + suppression ----------

test('BAR-K4b: installConsoleTap patches a fake console — records to the ring and still calls the original', () => {
  const calls = [];
  const fakeConsole = { error: (...args) => calls.push(args) };
  installConsoleTap(fakeConsole);
  const msg = 'tap-wiring-' + Math.random();
  fakeConsole.error(new Error(msg));
  assert.equal(calls.length, 1, 'the original console.error must still be called');
  const r = findByMsg(msg)[0];
  assert.ok(r, 'installConsoleTap recorded the error into the ring');
  assert.equal(r.code, 'MET-V804');
});

test('BAR-F6: withTapSuppressed prevents a diagnostic console.error from re-entering the tap', () => {
  const calls = [];
  const fakeConsole = { error: (...args) => calls.push(args) };
  installConsoleTap(fakeConsole);
  const msg = 'suppressed-diag-' + Math.random();
  withTapSuppressed(() => {
    fakeConsole.error(msg);
  });
  assert.equal(calls.length, 1, 'the ORIGINAL console.error must still run under suppression');
  assert.equal(findByMsg(msg).length, 0, 'a suppressed console.error must NOT create a ring row');
});

test('BAR-K4b wiring tripwire: main.tsx calls installConsoleTap(console)', () => {
  const src = readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8');
  assert.match(src, /installConsoleTap\(console\)/, 'main.tsx must wire installConsoleTap to the real console');
});
