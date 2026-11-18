// protocol-reconnect.test.mjs -- FEAT-9995385597 inc1 section E (AC-R) and the
// client half of G (AC-B) plus the client side of W/D/H: the browser
// reconnect state machine, driven against a fake WebSocket and a fake timer
// wheel with an injected scripted RNG. No real timers, no sleeps, no
// Date.now/Math.random on the tested path (AC-T1).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  ProtocolClient,
  ERR_ENGINE_UNREACHABLE,
  ERR_HANDSHAKE_TIMEOUT,
  ERR_COMMAND_OUTCOME_UNKNOWN,
  ERR_CITY_OPEN_ELSEWHERE,
  ERR_RECONNECT_GIVEUP,
  ERR_WAKE_TIMEOUT,
} from '../src/sim/protocolClient.ts';
import { backoffDelayMs, RECONNECT_DEFAULTS } from '../src/sim/reconnect.ts';

class FakeSocket {
  constructor() {
    this.sent = [];
    this.closed = false;
    this.closeArgs = null;
    this.listeners = { open: [], message: [], close: [], error: [] };
  }
  addEventListener(type, fn) {
    this.listeners[type].push(fn);
  }
  send(data) {
    this.sent.push(JSON.parse(data));
  }
  close(code, reason) {
    this.closed = true;
    this.closeArgs = { code, reason };
    this.emit('close', { code, reason });
  }
  emit(type, ev) {
    for (const fn of this.listeners[type]) fn(ev);
  }
  serverSends(msg) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
  /** Server-initiated close: delivers the close event only. */
  serverCloses(code, reason) {
    this.emit('close', { code, reason });
  }
  open() {
    this.emit('open', {});
  }
  accept(extra = {}) {
    this.serverSends({
      jsonrpc: '2.0',
      id: 1,
      result: { accepted: true, serverVersion: 'v1.2.3', capabilities: [], ...extra },
    });
  }
}

/** make() gives every command this backstop deadline unless a test overrides it
 * (a value no other timer uses, so the fake wheel can tell them apart). The
 * r4 backstop tests set the real default. */
const TEST_COMMAND_DEADLINE_MS = 7777777;

/** A hand-advanced timer wheel (AC-T1: no real timers). */
class FakeTimers {
  constructor() {
    this.now = 0;
    this.seq = 0;
    this.timers = new Map();
  }
  setTimeout(fn, ms) {
    const id = ++this.seq;
    this.timers.set(id, { at: this.now + ms, fn, ms });
    return id;
  }
  clearTimeout(id) {
    this.timers.delete(id);
  }
  /** Connection-machine timers armed (r4: the per-command backstop deadline
   * timers, recognised by the distinctive delay make() gives them, are counted
   * separately in commandTimers so the r3 invariants keep their meaning). */
  get pending() {
    return [...this.timers.values()].filter((t) => t.ms !== TEST_COMMAND_DEADLINE_MS).length;
  }
  get commandTimers() {
    return [...this.timers.values()].filter((t) => t.ms === TEST_COMMAND_DEADLINE_MS).length;
  }
  /** Delay of the single pending connection timer (asserts there is exactly one). */
  onlyDelay() {
    const conn = [...this.timers.values()].filter((t) => t.ms !== TEST_COMMAND_DEADLINE_MS);
    assert.equal(conn.length, 1, `expected exactly one pending timer, got ${conn.length}`);
    return conn[0].at - this.now;
  }
  advance(ms) {
    const target = this.now + ms;
    for (;;) {
      let nextId = null;
      let next = null;
      for (const [id, t] of this.timers) {
        if (t.at <= target && (next === null || t.at < next.at)) {
          nextId = id;
          next = t;
        }
      }
      if (!next) break;
      this.timers.delete(nextId);
      this.now = next.at;
      next.fn();
    }
    this.now = target;
  }
}

function make({ rng = 0, lifecycle = {}, opts = {} } = {}) {
  const timers = new FakeTimers();
  const sockets = [];
  const log = { states: [], refusals: [], deltas: [], live: [], progress: [] };
  let n = 0;
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion: 'v1.2.3',
    createSocket: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
    onStateChange: (s) => log.states.push(s),
    onRefused: (code, msg) => log.refusals.push({ code, msg }),
    onDelta: (delta, gap, handle) => log.deltas.push({ delta, gap, handle }),
    onLive: (v, nv, caps, info) => log.live.push({ v, caps, info }),
    onWakeProgress: (p) => log.progress.push({ ...p }),
    newCorrelationId: () => `c${++n}`,
    lifecycle: { random: () => rng, timers, clientSessionId: 'sess-abc', commandDeadlineMs: TEST_COMMAND_DEADLINE_MS, ...lifecycle },
    ...opts,
  });
  client.connect();
  return { client, timers, sockets, log, get socket() { return sockets.at(-1); } };
}

/** Drive a fresh socket to live. */
function goLive(h, extra = {}) {
  h.socket.open();
  h.socket.accept(extra);
}

const subscribeFrames = (s) => s.sent.filter((m) => m.method === 'command' && m.params.kind === 'Subscribe');

describe('AC-R1 backoff (pure)', () => {
  const cfg = RECONNECT_DEFAULTS;
  test('full jitter: random(0, min(30s, 1s*2^attempt)), floor 250 ms', () => {
    assert.equal(backoffDelayMs(0, () => 0, 0, cfg), 250, 'never below 250 ms');
    assert.equal(backoffDelayMs(0, () => 0.999999, 0, cfg), 999);
    assert.equal(backoffDelayMs(3, () => 0.5, 0, cfg), 4000);
    assert.equal(backoffDelayMs(3, () => 0.999999, 0, cfg), 7999);
  });
  test('the window caps at 30 s however large the attempt', () => {
    for (const a of [5, 6, 11, 40, 5000]) {
      assert.ok(backoffDelayMs(a, () => 0.999999999, 0, cfg) <= 30000, `attempt ${a}`);
    }
    assert.equal(backoffDelayMs(11, () => 0.999999999, 0, cfg), 29999);
  });
  test('never below the last reconnectAfterMs', () => {
    assert.equal(backoffDelayMs(0, () => 0, 4000, cfg), 4000);
    assert.equal(backoffDelayMs(0, () => 0.999999, 4000, cfg), 4000);
    assert.equal(backoffDelayMs(4, () => 0.999999, 4000, cfg), 15999);
  });
  test('a hostile random source cannot escape the window', () => {
    for (const r of [-5, NaN, Infinity, 7]) {
      const d = backoffDelayMs(2, () => r, 0, cfg);
      assert.ok(d >= 250 && d <= 4000, `r=${r} gave ${d}`);
    }
  });
});

describe('AC-R1/R2/R4/R11 reconnect schedule', () => {
  test('a lost live connection reconnects after the jittered delay, with the same clientSessionId and a fresh handshake', () => {
    const h = make({ rng: 0.5 });
    goLive(h);
    assert.equal(h.client.getState(), 'live');
    h.socket.serverCloses(1006, '');
    assert.equal(h.client.getState(), 'reconnecting');
    assert.equal(h.sockets.length, 1);
    assert.equal(h.timers.onlyDelay(), 500, 'attempt 0, r=0.5: 0.5 * 1000');
    h.timers.advance(499);
    assert.equal(h.sockets.length, 1, 'not before the delay');
    h.timers.advance(1);
    assert.equal(h.sockets.length, 2);
    h.socket.open();
    const hs = h.socket.sent[0];
    assert.equal(hs.method, 'handshake');
    assert.equal(hs.params.clientSessionId, 'sess-abc');
    assert.equal(h.sockets[0].sent[0].params.clientSessionId, 'sess-abc', 'same id on both connections');
    assert.deepEqual(h.log.refusals, [], 'a lost connection is quiet to the player');
    const connecting = h.log.states.indexOf('connecting', h.log.states.indexOf('reconnecting'));
    assert.ok(connecting > 0 && h.log.states[connecting + 1] === 'handshaking');
  });

  test('delays grow per consecutive failure and stay inside the window', () => {
    const h = make({ rng: 0.999999 });
    goLive(h);
    h.socket.serverCloses(1006);
    const delays = [];
    for (let i = 0; i < 6; i++) {
      delays.push(h.timers.onlyDelay());
      h.timers.advance(delays.at(-1));
      h.socket.serverCloses(1006); // the new attempt dies before opening
    }
    assert.deepEqual(delays, [999, 1999, 3999, 7999, 15999, 29999]);
  });

  test('server.draining {reconnectAfterMs: 4000}: first attempt no earlier than 4000 ms, shown as Reconnecting, no error reported', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 4000, reason: 'deploy' } });
    assert.equal(h.client.getState(), 'reconnecting');
    assert.equal(h.timers.onlyDelay(), 4000);
    h.timers.advance(3999);
    assert.equal(h.sockets.length, 1);
    h.timers.advance(1);
    assert.equal(h.sockets.length, 2);
    assert.deepEqual(h.log.refusals, [], 'AC-D8: no ERR_ENGINE_UNREACHABLE for a drain');
    assert.ok(!h.log.states.includes('error'), 'AC-B3: never flashes error');
  });

  test('a draining frame followed by its own 1012 close (and a repeated frame) is honoured exactly once: one timer, one new socket', () => {
    const h = make({ rng: 0 });
    goLive(h);
    const old = h.socket;
    old.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 4000, reason: 'deploy' } });
    old.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 4000, reason: 'deploy' } });
    old.serverCloses(1012, 'restart');
    assert.equal(h.timers.pending, 1, 'one retry timer, not one per signal');
    assert.equal(h.timers.onlyDelay(), 4000);
    h.timers.advance(4000);
    assert.equal(h.sockets.length, 2, 'exactly one new connection');
    assert.equal(h.timers.pending <= 1, true);
  });

  test('close 1012 with no draining frame takes the same quiet path', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverCloses(1012, 'restart');
    assert.equal(h.client.getState(), 'reconnecting');
    assert.deepEqual(h.log.refusals, []);
    assert.equal(h.timers.onlyDelay(), 250);
  });

  test('AC-R13: a proxy-limit close while healthy reconnects at attempt-0 jitter, Reconnecting only briefly, then live', () => {
    const h = make({ rng: 0.5 });
    goLive(h);
    h.timers.advance(60 * 60 * 1000); // an hour live: stable, attempt reset
    h.socket.serverCloses(1001, 'going away');
    const d = h.timers.onlyDelay();
    assert.ok(d <= 1000, 'attempt-0 window');
    h.timers.advance(d);
    goLive(h);
    assert.equal(h.client.getState(), 'live');
    assert.ok(d < 3000);
  });

  test('AC-R11: error then close for one socket arms exactly ONE timer and one attempt', () => {
    const h = make({ rng: 0.5 });
    goLive(h);
    h.socket.emit('error', {});
    h.socket.serverCloses(1006);
    assert.equal(h.timers.pending, 1, 'one retry timer');
    h.timers.advance(10000);
    assert.equal(h.sockets.length, 2, 'one new attempt, not two');
  });

  test('AC-R11: connect() during an attempt in flight, or with a retry pending, does not stack attempts', () => {
    const h = make({ rng: 0.5 });
    h.client.connect();
    h.client.connect();
    assert.equal(h.sockets.length, 1, 'connect while a socket is in flight is a no-op');
    goLive(h);
    h.socket.serverCloses(1006);
    assert.equal(h.timers.pending, 1);
    h.client.connect(); // manual retry superseding the timer
    assert.equal(h.sockets.length, 2);
    assert.equal(h.timers.pending, 1, 'only the new attempt connect timer: the retry timer was superseded, not left to double-fire');
    h.timers.advance(h.timers.onlyDelay());
    assert.equal(h.sockets.length, 2, 'the superseded retry did not fire a third attempt');
  });

  test('AC-R12: close() cancels a pending retry; no further attempt', () => {
    const h = make({ rng: 0.5 });
    goLive(h);
    h.socket.serverCloses(1006);
    assert.equal(h.timers.pending, 1);
    h.client.close();
    assert.equal(h.timers.pending, 0);
    h.timers.advance(10 * 60 * 1000);
    assert.equal(h.sockets.length, 1);
    assert.equal(h.client.getState(), 'closed');
  });

  test('AC-R1: only a live that LASTS 10 s resets the schedule', () => {
    const h = make({ rng: 0.999999 });
    goLive(h);
    h.socket.serverCloses(1006);
    assert.equal(h.timers.onlyDelay(), 999);
    h.timers.advance(999);
    goLive(h);
    h.timers.advance(9999); // live for just under 10 s
    h.socket.serverCloses(1006);
    assert.equal(h.timers.onlyDelay(), 1999, 'a flapping live did not reset the attempt counter');
    h.timers.advance(1999);
    goLive(h);
    h.timers.advance(10000); // stable
    h.socket.serverCloses(1006);
    assert.equal(h.timers.onlyDelay(), 999, 'after a stable live the schedule restarts at attempt 0');
  });
});

describe('AC-R3 give up, AC-R6 terminal refusals, AC-R7 busy', () => {
  test('12 failed attempts then E-RECONNECT-GIVEUP, "Offline - click to retry"; a click resets', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverCloses(1006);
    for (let i = 0; i < 12; i++) {
      assert.equal(h.client.getState(), 'reconnecting', `retry ${i + 1} scheduled`);
      h.timers.advance(h.timers.onlyDelay());
      assert.equal(h.sockets.length, i + 2);
      h.socket.serverCloses(1006);
    }
    assert.equal(h.client.getState(), 'error');
    assert.equal(h.client.getTerminal().code, ERR_RECONNECT_GIVEUP);
    assert.equal(h.log.refusals.at(-1).code, ERR_RECONNECT_GIVEUP);
    const count = h.sockets.length;
    h.timers.advance(60 * 60 * 1000);
    assert.equal(h.sockets.length, count, 'no further attempt after give-up');
    h.client.retry();
    assert.equal(h.sockets.length, count + 1, 'a click connects at once');
    assert.equal(h.client.getTerminal(), null);
    h.socket.serverCloses(1006);
    assert.equal(h.client.getState(), 'reconnecting', 'schedule reset: a fresh run of 12');
  });

  for (const code of ['MET-P011', 'MET-P020', 'MET-P030']) {
    test(`${code} is terminal: refused, no retry timer, no new socket`, () => {
      const h = make();
      h.socket.open();
      h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code, message: 'no' } });
      assert.equal(h.client.getState(), 'refused');
      assert.equal(h.log.refusals[0].code, code);
      assert.equal(h.timers.pending, 0);
      h.timers.advance(10 * 60 * 1000);
      assert.equal(h.sockets.length, 1);
    });
  }

  test('MET-P043 busy retries 5 times on the schedule, then terminal E-CITY-OPEN-ELSEWHERE', () => {
    const h = make({ rng: 0 });
    for (let i = 0; i < 5; i++) {
      h.socket.open();
      h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P043', message: 'busy' } });
      assert.equal(h.client.getState(), 'reconnecting', `busy refusal ${i + 1} retries`);
      assert.deepEqual(h.log.refusals, []);
      h.timers.advance(h.timers.onlyDelay());
    }
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P043', message: 'busy' } });
    assert.equal(h.client.getState(), 'refused');
    assert.equal(h.client.getTerminal().code, ERR_CITY_OPEN_ELSEWHERE);
    assert.equal(h.timers.pending, 0);
  });

  test('AC-G1: close 4000 on the owned socket is terminal open-elsewhere; a 4000 on a stale socket is ignored', () => {
    const h = make({ rng: 0 });
    goLive(h);
    const old = h.socket;
    old.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h);
    old.serverCloses(4000, 'superseded'); // the socket we no longer own
    assert.equal(h.client.getState(), 'live', 'stale 4000 ignored');
    h.socket.serverCloses(4000, 'superseded');
    assert.equal(h.client.getState(), 'refused');
    assert.equal(h.client.getTerminal().code, ERR_CITY_OPEN_ELSEWHERE);
    assert.equal(h.timers.pending, 0, 'no auto-reconnect');
  });

  test('a retryable refusal marker (error.data.retryable) retries; unmarked unknown codes are terminal', () => {
    const h = make({ rng: 0 });
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P999', message: 'draining', data: { retryable: true } } });
    assert.equal(h.client.getState(), 'reconnecting');
    h.timers.advance(h.timers.onlyDelay());
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P999', message: 'nope' } });
    assert.equal(h.client.getState(), 'refused');
  });
});

describe('AC-W waking, AC-H hibernating (client side)', () => {
  test('waking: accepted-not-ready shows waking with progress; subscriptions and commands wait for ready (AC-W5/W8)', async () => {
    const h = make();
    h.socket.open();
    h.socket.accept({ waking: true });
    assert.equal(h.client.getState(), 'waking');
    assert.deepEqual(h.client.getWakeProgress(), { phase: 'queued', pct: 0 });
    assert.equal(h.log.live.length, 0, 'onLive (the subscribe hook) has not fired');
    assert.equal(h.client.subscribe('v'), 'c1');
    assert.equal(subscribeFrames(h.socket).length, 0, 'nothing is sent while waking');
    await assert.rejects(h.client.sendCommand('SetSpeed', {}), (e) => e.code === ERR_ENGINE_UNREACHABLE && e.retryable === true && e.state === 'waking');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'replaying', pct: 62 } });
    assert.deepEqual(h.client.getWakeProgress(), { phase: 'replaying', pct: 62 });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'ready', pct: 100 } });
    assert.equal(h.client.getState(), 'live');
    assert.equal(h.log.live.length, 1);
    assert.equal(h.client.getWakeProgress(), null);
    assert.equal(h.timers.pending, 1, 'only the stable timer remains; the wake timer is gone');
  });

  test('AC-W9: 60 s of silence while waking = E-WAKE-TIMEOUT, close, reconnect; any waking message resets the clock', () => {
    const h = make({ rng: 0 });
    h.socket.open();
    h.socket.accept({ waking: true });
    h.timers.advance(59000);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'fetching', pct: 10 } });
    h.timers.advance(59000);
    assert.equal(h.client.getState(), 'waking', 'the message reset the timer');
    h.timers.advance(1000);
    assert.equal(h.client.getState(), 'reconnecting');
    assert.equal(h.sockets[0].closed, true);
    assert.ok(h.timers.pending === 1);
    assert.equal(ERR_WAKE_TIMEOUT, 'MET-V936');
  });

  test('AC-W9: a handshake reply that never arrives is MET-P012, a separate 10 s timer, then the reconnect schedule', () => {
    const h = make({ rng: 0 });
    h.socket.open();
    h.timers.advance(9999);
    assert.equal(h.client.getState(), 'handshaking');
    h.timers.advance(1);
    assert.equal(h.client.getState(), 'reconnecting');
    assert.equal(h.sockets[0].closed, true);
    assert.equal(ERR_HANDSHAKE_TIMEOUT, 'MET-P012');
  });

  test('AC-W6: waking{failed, MET-P030} is terminal; any other code takes the schedule', () => {
    const a = make();
    a.socket.open();
    a.socket.accept({ waking: true });
    a.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'failed', pct: 20, code: 'MET-P030' } });
    assert.equal(a.client.getState(), 'refused');
    assert.equal(a.log.refusals[0].code, 'MET-P030');
    assert.equal(a.timers.pending, 0);

    const b = make({ rng: 0 });
    b.socket.open();
    b.socket.accept({ waking: true });
    b.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'failed', pct: 20, code: 'MET-P777' } });
    assert.equal(b.client.getState(), 'refused', 'unknown unmarked code is terminal like a refusal');
  });

  test('AC-H3: server.hibernating enters asleep; no automatic reconnect, ever; only a click wakes', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'idle' } });
    assert.equal(h.client.getState(), 'asleep');
    h.socket.serverCloses(1000, 'hibernate'); // the follow-up close is a stale event
    h.timers.advance(24 * 60 * 60 * 1000);
    assert.equal(h.sockets.length, 1, 'visibility/focus/time never connect an asleep client');
    assert.equal(h.client.getState(), 'asleep');
    h.client.wakeFromAsleep();
    assert.equal(h.sockets.length, 2, 'a deliberate click connects through the normal path');
    assert.equal(h.client.getState(), 'connecting');
  });

  test('AC-H3: close 1000 reason hibernate (no frame) is asleep; the client-initiated visibility close is asleep', () => {
    const a = make();
    goLive(a);
    a.socket.serverCloses(1000, 'hibernate');
    assert.equal(a.client.getState(), 'asleep');
    const b = make();
    goLive(b);
    b.client.enterAsleep('hidden');
    assert.equal(b.client.getState(), 'asleep');
    assert.deepEqual(b.sockets[0].closeArgs, { code: 1000, reason: 'hidden' });
    assert.equal(b.timers.pending, 0);
  });

  test('AC-H4: close 1000 with another reason, 1001 and 1006 are lost connections, not asleep', () => {
    for (const [code, reason] of [[1000, 'bye'], [1001, ''], [1006, '']]) {
      const h = make();
      goLive(h);
      h.socket.serverCloses(code, reason);
      assert.equal(h.client.getState(), 'reconnecting', `${code}/${reason}`);
    }
  });

  test('AC-P4: an unknown notification method is ignored and the connection carries on', () => {
    const h = make();
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.future', params: { x: 1 } });
    assert.equal(h.client.getState(), 'live');
    assert.deepEqual(h.log.refusals, []);
  });

  test('malformed lifecycle params are ignored, not fatal', () => {
    const h = make();
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: -1, reason: 'deploy' } });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'hidden' } });
    assert.equal(h.client.getState(), 'live');
  });
});

describe('AC-R5 re-subscribe, AC-P6 seq restart', () => {
  test('N subscriptions are re-issued once, in order, with FRESH correlationIds; deltas map to the original handles; seq restarts', () => {
    const h = make({ rng: 0 });
    goLive(h);
    const hA = h.client.subscribe('viewA', { p: '1' });
    const hB = h.client.subscribe('viewB');
    const first = subscribeFrames(h.socket).map((m) => m.params.correlationId);
    assert.deepEqual(first, [hA, hB]);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'S1', tick: 1, seq: 1, patch: {}, correlationId: hA } });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'S2', tick: 1, seq: 1, patch: {}, correlationId: hB } });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'S1', tick: 2, seq: 2, patch: {} } });
    assert.deepEqual(h.log.deltas.map((d) => d.handle), [hA, hB, hA]);

    h.socket.serverCloses(1012);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h);
    const re = subscribeFrames(h.socket);
    assert.equal(re.length, 2, 'each exactly once');
    assert.deepEqual(re.map((m) => m.params.payload.viewName), ['viewA', 'viewB'], 'original order');
    assert.deepEqual(re[0].params.payload.params, { p: '1' });
    for (const m of re) {
      assert.ok(![hA, hB].includes(m.params.correlationId), 'a fresh correlationId, never the original');
    }
    assert.equal(h.log.live.at(-1).info.reconnected, true, 'onLive tells the caller not to subscribe again');

    // New SubscriptionIDs restart at Seq 1: no false gap/duplicate report.
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'T1', tick: 9, seq: 1, patch: {}, correlationId: re[0].params.correlationId } });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'T2', tick: 9, seq: 1, patch: {}, correlationId: re[1].params.correlationId } });
    const after = h.log.deltas.slice(3);
    assert.deepEqual(after.map((d) => [d.handle, d.gap]), [[hA, 0], [hB, 0]]);
  });

  test('a delta for a SubscriptionID the client does not hold is ignored, not fed to the tracker or the caller', () => {
    const h = make();
    goLive(h);
    h.client.subscribe('viewA');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'ghost', tick: 1, seq: 5, patch: {} } });
    assert.equal(h.log.deltas.length, 0);
  });

  test('an unsubscribed view is not re-issued by a later reconnect', () => {
    const h = make({ rng: 0 });
    goLive(h);
    const hA = h.client.subscribe('viewA');
    h.client.subscribe('viewB');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'S1', tick: 1, seq: 1, patch: {}, correlationId: hA } });
    h.client.unsubscribe('S1');
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h);
    assert.deepEqual(subscribeFrames(h.socket).map((m) => m.params.payload.viewName), ['viewB']);
  });

  test('a view whose re-subscribe is refused reports its own error and does not stop the others', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.client.subscribe('viewA');
    h.client.subscribe('viewB');
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h);
    const re = subscribeFrames(h.socket);
    h.socket.serverSends({ jsonrpc: '2.0', id: re[0].id, error: { code: 'MET-P011', message: 'bad view' } });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'T2', tick: 1, seq: 1, patch: {}, correlationId: re[1].params.correlationId } });
    assert.equal(h.client.getState(), 'live');
    assert.equal(h.log.deltas.length, 1, 'viewB still delivers');
  });
});

describe('AC-R9 in-flight commands, AC-R10 commands while down', () => {
  test('without command.dedup an in-flight command rejects E-COMMAND-OUTCOME-UNKNOWN and is NEVER re-sent', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: [] });
    const p = h.client.sendCommand('Buy', { x: 1 });
    const cid = h.socket.sent.find((m) => m.method === 'command').params.correlationId;
    h.socket.serverCloses(1006);
    // r3 ruling 2: sent without dedup, so rejected at the drop (never held).
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: [] });
    await assert.rejects(p, (e) => e.code === ERR_COMMAND_OUTCOME_UNKNOWN);
    const resent = h.socket.sent.filter((m) => m.method === 'command' && m.params.correlationId === cid);
    assert.equal(resent.length, 0, 'never retried, so never double-applied');
  });

  test('with command.dedup the command is re-sent ONCE after ready with the SAME correlationId; a duplicate result resolves', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: ['command.dedup'] });
    const p = h.client.sendCommand('Buy', { x: 1 });
    const orig = h.socket.sent.find((m) => m.method === 'command').params;
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: ['command.dedup'] });
    const resent = h.socket.sent.filter((m) => m.method === 'command' && m.params.kind === 'Buy');
    assert.equal(resent.length, 1);
    assert.equal(resent[0].params.correlationId, orig.correlationId, 'retries reuse the correlationId');
    h.socket.serverSends({ jsonrpc: '2.0', id: resent[0].id, result: { queued: true } });
    h.socket.serverSends({
      jsonrpc: '2.0',
      method: 'result',
      params: { correlationId: orig.correlationId, tick: 3, accepted: true, result: { duplicate: true, seq: 41 } },
    });
    const r = await p;
    assert.equal(r.accepted, true);
    assert.equal(r.result.duplicate, true);
    assert.equal(r.result.seq, 41);
  });

  test('a command dropped a SECOND time after its single re-send rejects outcome-unknown', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: ['command.dedup'] });
    const p = h.client.sendCommand('Buy', {});
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: ['command.dedup'] });
    h.socket.serverCloses(1006);
    await assert.rejects(p, (e) => e.code === ERR_COMMAND_OUTCOME_UNKNOWN);
  });

  test('queries are excluded from retry: reject retryable ERR_ENGINE_UNREACHABLE, even with dedup', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: ['command.dedup'] });
    const p = h.client.sendCommand('QueryCitizen', {});
    h.socket.serverCloses(1006);
    await assert.rejects(p, (e) => e.code === ERR_ENGINE_UNREACHABLE && e.retryable === true);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: ['command.dedup'] });
    assert.equal(h.socket.sent.filter((m) => m.method === 'command' && m.params.kind === 'QueryCitizen').length, 0);
  });

  test('AC-R10: sendCommand while not live rejects immediately with MET-P013 {retryable, state}; nothing is queued', async () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverCloses(1006);
    await assert.rejects(h.client.sendCommand('Buy', {}), (e) => e.code === ERR_ENGINE_UNREACHABLE && e.retryable === true && e.state === 'reconnecting');
    h.timers.advance(h.timers.onlyDelay());
    goLive(h);
    assert.equal(h.socket.sent.filter((m) => m.method === 'command').length, 0, 'not queued for later');
  });
});

describe('AC-R8 never the offline mock; AC-P5 session id; AC-B1 states', () => {
  test('every failure path ends in a client state, never a mock: no module reference to the mock sim exists in the client', () => {
    for (const f of ['../src/sim/protocolClient.ts', '../src/sim/reconnect.ts']) {
      const src = readFileSync(new URL(f, import.meta.url), 'utf8');
      // from '...', bare side-effect import '...', and dynamic import('...') all count.
      const imports = [...src.matchAll(/(?:from\s+|import\s*\(?\s*)['"]([^'"]+)['"]/g)].map((m) => m[1]);
      for (const i of imports) {
        assert.ok(!/engine\.ts$|store\.tsx$|snapshot|simWorker|devcity|mockSim/i.test(i), `${f} must not import the mock sim: ${i}`);
      }
      assert.ok(!/createInitialState|StubEngine|fallbackToMock|mockFallback/.test(src), `${f} references a mock fallback`);
    }
  });

  test('failure, wake timeout, give-up and refusal each stop in an explicit client state with no fallback signal', () => {
    const outcomes = [];
    {
      const h = make({ rng: 0 });
      h.socket.open();
      h.socket.accept({ waking: true });
      h.timers.advance(60000);
      outcomes.push(h.client.getState());
    }
    {
      const h = make();
      h.socket.open();
      h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P020', message: 'x' } });
      outcomes.push(h.client.getState());
    }
    assert.deepEqual(outcomes, ['reconnecting', 'refused']);
  });

  test('capabilities and clientSessionId: lifecycle clients declare the flags and send the page id; legacy clients send neither', () => {
    const h = make();
    h.socket.open();
    const p = h.socket.sent[0].params;
    assert.deepEqual(p.capabilities, ['lifecycle.waking', 'lifecycle.draining', 'lifecycle.hibernating', 'command.dedup']);
    assert.equal(p.clientSessionId, 'sess-abc');
    const legacySock = [];
    const legacy = new ProtocolClient({ url: 'ws://x', clientVersion: 'v', createSocket: () => { const s = new FakeSocket(); legacySock.push(s); return s; } });
    legacy.connect();
    legacySock[0].open();
    assert.deepEqual(legacySock[0].sent[0].params.capabilities, []);
    assert.equal('clientSessionId' in legacySock[0].sent[0].params, false);
  });

  test('AC-R8 behavioural: after N failed reconnects the client is offline with MET-V939 and nothing advances (no delta, no live, no timer, no socket)', () => {
    const h = make({ rng: 0 });
    goLive(h);
    const deltasBefore = h.log.deltas.length;
    const liveBefore = h.log.live.length;
    const progressBefore = h.log.progress.length;
    h.socket.serverCloses(1006);
    for (let i = 0; i < 12; i++) {
      h.timers.advance(h.timers.onlyDelay());
      h.socket.serverCloses(1006);
    }
    assert.equal(h.client.getState(), 'error');
    assert.equal(h.client.getTerminal().code, ERR_RECONNECT_GIVEUP);
    assert.equal(ERR_RECONNECT_GIVEUP, 'MET-V939');
    const sockets = h.sockets.length;
    h.timers.advance(24 * 60 * 60 * 1000);
    assert.equal(h.timers.pending, 0, 'no timer left armed that could swap anything in later');
    assert.equal(h.sockets.length, sockets, 'no further connection');
    assert.equal(h.log.deltas.length, deltasBefore, 'no simulation delta reached the caller from anywhere');
    assert.equal(h.log.live.length, liveBefore, 'the client never reported live again');
    assert.equal(h.log.progress.length, progressBefore);
    const afterDrop = h.log.states.slice(h.log.states.lastIndexOf('live') + 1);
    assert.ok(!afterDrop.includes('live'), 'no live state after the drop');
    assert.equal(afterDrop.at(-1), 'error', 'the only way out of reconnecting is the explicit error state');
    // A command is refused, not served by a stand-in engine.
    return assert.rejects(h.client.sendCommand('Buy', {}), (e) => e.code === ERR_ENGINE_UNREACHABLE);
  });

  test('AC-B3: a drain and quick reconnect never shows error', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 100, reason: 'deploy' } });
    h.timers.advance(250);
    goLive(h);
    assert.ok(!h.log.states.includes('error') && !h.log.states.includes('refused'));
    assert.equal(h.client.getState(), 'live');
  });
});

// ---------------------------------------------------------------------------
// FEAT-9995385597 r2 rework: settlement invariant (ruling 1), live-announcement
// order (2), stale-delta hot path (3), late result on the old socket (4).
// ---------------------------------------------------------------------------
const DEDUP = { capabilities: ['command.dedup'] };
const track = (p) => {
  const t = { state: 'pending', count: 0, err: null };
  p.then(
    () => { t.state = 'resolved'; t.count += 1; },
    (e) => { t.state = 'rejected'; t.count += 1; t.err = e; },
  );
  return t;
};
const flushMicro = () => new Promise((r) => setImmediate(r));
const sentOf = (s, kind) => s.sent.filter((m) => m.method === 'command' && m.params.kind === kind);

describe('r2 ruling 1: every held command settles exactly once on every exit', () => {
  test('give-up after the retries rejects MET-V937', async () => {
    const h = make({ rng: 0, lifecycle: { maxAttempts: 2 } });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    for (let i = 0; i < 5 && h.timers.pending > 0; i++) {
      h.timers.advance(h.timers.onlyDelay());
      h.socket.serverCloses(1006);
    }
    await flushMicro();
    assert.equal(h.client.getState(), 'error');
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.equal(t.count, 1);
  });

  test('terminal refusal on the reconnect handshake rejects MET-V937', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: 'MET-P020', message: 'floor' } });
    await flushMicro();
    assert.equal(h.client.getState(), 'refused');
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
  });

  test('client asleep rejects the in-flight command, and a wake does not resurrect it', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.client.enterAsleep();
    await flushMicro();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    h.client.wakeFromAsleep();
    goLive(h, DEDUP);
    await flushMicro();
    assert.equal(sentOf(h.socket, 'Buy').length, 0, 'a settled command is never re-sent');
    assert.equal(t.count, 1);
  });

  test('a server hibernating frame rejects a command re-sent after a loss', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'idle' } });
    await flushMicro();
    assert.equal(h.client.getState(), 'asleep');
    assert.equal(t.state, 'rejected');
    assert.equal(t.count, 1);
  });

  test('a server hibernating frame while a command is held rejects it', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    h.socket.open();
    h.socket.accept({ ...DEDUP, waking: true });
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'idle' } });
    await flushMicro();
    assert.equal(h.client.getState(), 'asleep');
    assert.equal(t.state, 'rejected');
    assert.equal(t.count, 1);
  });

  test('retry() after give-up leaves nothing pending and settles nothing twice', async () => {
    const h = make({ rng: 0, lifecycle: { maxAttempts: 1 } });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(h.client.getState(), 'error');
    h.client.retry();
    goLive(h, DEDUP);
    await flushMicro();
    assert.equal(sentOf(h.socket, 'Buy').length, 0);
    assert.equal(t.state, 'rejected');
    assert.equal(t.count, 1);
  });

  test('close() rejects a held command exactly once', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.client.close();
    await flushMicro();
    assert.equal(t.state, 'rejected');
    assert.equal(t.count, 1);
  });

  test('successful re-handshake with dedup: re-sent once, same correlationId; a second drop rejects MET-V937 and never re-sends', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    const first = sentOf(h.socket, 'Buy')[0].params.correlationId;
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    const again = sentOf(h.socket, 'Buy');
    assert.equal(again.length, 1);
    assert.equal(again[0].params.correlationId, first);
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    assert.equal(sentOf(h.socket, 'Buy').length, 0, 'never re-sent a second time');
  });

  test('capabilities are decided by the NEW handshake: dedup before the loss, none after, rejects', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    assert.deepEqual([...h.client.getCapabilities()], [], 'cleared on loss');
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: [] });
    await flushMicro();
    assert.equal(sentOf(h.socket, 'Buy').length, 0);
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
  });

  test('several consecutive losses before a live: the held command is still re-sent exactly once', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    h.socket.open();
    h.socket.serverCloses(1006); // second loss, handshake never completed
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    assert.equal(sentOf(h.socket, 'Buy').length, 1);
    assert.equal(t.state, 'pending');
  });
});

describe('r2 ruling 2: restore before the live announcement', () => {
  test('a command issued from onStateChange(live) during a reconnect is written exactly once', () => {
    let fire = false;
    let h;
    h = make({
      rng: 0,
      opts: { onStateChange: (s) => { if (s === 'live' && fire) { fire = false; h.client.sendCommand('Build', {}).catch(() => {}); } } },
    });
    goLive(h, DEDUP);
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    fire = true;
    goLive(h, DEDUP);
    assert.equal(sentOf(h.socket, 'Build').length, 1);
  });

  test('a subscription made from onStateChange(live) is sent once, not re-subscribed by the restore', () => {
    let fire = false;
    let h;
    h = make({
      rng: 0,
      opts: { onStateChange: (s) => { if (s === 'live' && fire) { fire = false; h.client.subscribe('f3.view'); } } },
    });
    goLive(h);
    h.client.subscribe('f2.finance');
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    fire = true;
    goLive(h);
    const subs = subscribeFrames(h.socket);
    assert.equal(subs.filter((m) => m.params.payload.viewName === 'f3.view').length, 1);
    assert.equal(subs.filter((m) => m.params.payload.viewName === 'f2.finance').length, 1);
  });
});

describe('r2 ruling 3: stale-subscription deltas stay off the storage hot path', () => {
  test('1000 stale deltas: counted, logged once, no per-delta storage write', () => {
    let writes = 0;
    const saved = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: { getItem: () => null, setItem: () => { writes += 1; }, removeItem: () => {}, key: () => null, length: 0, clear: () => {} },
    });
    try {
      const h = make({ rng: 0 });
      goLive(h);
      h.client.subscribe('f2.finance');
      const before = writes;
      for (let i = 1; i <= 1000; i++) {
        h.socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'GHOST', tick: i, seq: i, patch: {} } });
      }
      assert.equal(h.client.getStaleDeltaCount(), 1000);
      assert.ok(writes - before <= 2, `storage written ${writes - before} times for 1000 stale deltas`);
    } finally {
      if (saved) Object.defineProperty(globalThis, 'localStorage', saved);
      else delete globalThis.localStorage;
    }
  });
});

describe('r2 ruling 4: a late result on the OLD socket is ignored', () => {
  test('does not settle (or double-settle) a command re-sent on the new socket', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    const cid = sentOf(h.socket, 'Buy')[0].params.correlationId;
    const old = h.socket;
    old.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    old.serverSends({ jsonrpc: '2.0', method: 'result', params: { correlationId: cid, tick: 1, accepted: true } });
    await flushMicro();
    assert.equal(t.state, 'pending', 'a result from a dead socket must not settle the command');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'result', params: { correlationId: cid, tick: 2, accepted: true } });
    await flushMicro();
    assert.equal(t.state, 'resolved');
    assert.equal(t.count, 1);
  });
});

// ---------------------------------------------------------------------------
// FEAT-9995385597 r3 rework: connect timer + timer invariant (ruling 1), the
// hold rule (2), the waking overall deadline and the pending bound (r4: commandDeadlineMs)
// (3), restore with a dead socket (A1), retry() while live (A2), unknown Kind
// classified as a write (A4).
// ---------------------------------------------------------------------------
const TERMINAL_STATES = ['error', 'refused', 'asleep', 'closed'];

/** The r3 invariant, asserted after every step. A client in a non-live,
 * non-terminal state needs an armed timer; so does a command promise pending
 * while the client is NOT live (held for a reconnect). In `live` a pending
 * command is normal (the server answers); the stable timer is not a liveness
 * guarantee. */
function assertTimerInvariant(h, tracked, label) {
  const st = h.client.getState();
  const terminal = TERMINAL_STATES.includes(st);
  const heldPending = st !== 'live' && tracked.some((t) => t.state === 'pending');
  if ((st !== 'live' && !terminal) || heldPending) {
    assert.ok(h.timers.pending >= 1, `${label}: state=${st} pendingCmd=${heldPending} but the timer wheel is empty`);
  }
}

/** Drives the client in 1 s fake-time steps, applying `behave` to each new
 * socket / each step, asserting the invariant after EVERY step, until a
 * terminal/asleep/live state. */
async function driveToEnd(h, tracked, behave, label, maxSeconds = 100000) {
  let lastSock = null;
  for (let tick = 0; tick < maxSeconds; tick++) {
    if (h.socket !== lastSock) {
      lastSock = h.socket;
      behave.onNew?.(h, h.socket);
    }
    behave.each?.(h, tick);
    await flushMicro();
    assertTimerInvariant(h, tracked, `${label} t=${tick}`);
    const st = h.client.getState();
    if (TERMINAL_STATES.includes(st) || st === 'live') {
      for (let i = 0; i < 3; i++) await flushMicro();
      return tick;
    }
    h.timers.advance(1000);
    await flushMicro();
    assertTimerInvariant(h, tracked, `${label} after advance t=${tick}`);
  }
  assert.fail(`${label}: never reached live or a terminal state in ${maxSeconds} s of fake time`);
}

const wakingProgress = (h) => h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'replaying', pct: 40 } });

describe('r3 ruling 1: connect timer and the timer invariant', () => {
  test('a reconnect socket that never opens and never errors is a counted failed attempt; the held write settles (probe N10)', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('SetSpeed', { speed: 2 }));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    assert.equal(h.client.getState(), 'connecting');
    assert.ok(h.timers.pending >= 1, 'connect() armed a timer');
    h.timers.advance(24 * 3600 * 1000);
    await flushMicro();
    assert.equal(h.client.getState(), 'error', 'gave up');
    assert.equal(h.client.getTerminal().code, ERR_RECONNECT_GIVEUP);
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.equal(t.count, 1);
  });

  test('the first connect() also has a connect timer; open clears it and the handshake timer takes over', () => {
    const h = make({ rng: 0 });
    assert.equal(h.timers.pending, 1, 'first connect armed its timer');
    h.socket.open();
    assert.equal(h.timers.pending, 1, 'connect timer replaced by the handshake timer, not stacked');
    h.timers.advance(RECONNECT_DEFAULTS.handshakeTimeoutMs);
    assert.equal(h.client.getState(), 'reconnecting');
  });

  test('close() cancels the connect timer; a loss before open leaves only the retry timer', () => {
    const h = make({ rng: 0 });
    h.client.close();
    assert.equal(h.timers.pending, 0);
    const h2 = make({ rng: 0 });
    h2.socket.serverCloses(1006); // closed before it ever opened
    assert.equal(h2.timers.pending, 1, 'only the retry timer');
  });

  const scenarios = {
    'a socket that never opens': {},
    'a socket that opens and never answers the handshake': { onNew: (_h, s) => s.open() },
    'a waking that never finishes (progress every 30 s)': {
      onNew: (_h, s) => {
        s.open();
        s.accept({ waking: true });
      },
      each: (h, tick) => {
        if (tick % 30 === 0 && h.client.getState() === 'waking') wakingProgress(h);
      },
    },
  };
  for (const [name, behave] of Object.entries(scenarios)) {
    test(`invariant after every step, then terminal with nothing pending: ${name}`, async () => {
      const h = make({ rng: 0.5 });
      goLive(h, DEDUP);
      const t = track(h.client.sendCommand('Buy', {}));
      h.socket.serverCloses(1006);
      await driveToEnd(h, [t], behave, name);
      assert.equal(h.client.getState(), 'error');
      assert.equal(t.state, 'rejected');
      assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
      assert.equal(t.count, 1);
    });
  }

  test('invariant across a draining frame, then a socket that never opens', async () => {
    const h = make({ rng: 0.5 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 2000, reason: 'deploy' } });
    assertTimerInvariant(h, [t], 'right after draining');
    await driveToEnd(h, [t], {}, 'draining');
    assert.equal(h.client.getState(), 'error');
    assert.equal(t.state, 'rejected');
  });

  test('invariant at every step of a live -> loss -> waking -> ready cycle', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    assertTimerInvariant(h, [t], 'live');
    h.socket.serverCloses(1006);
    assertTimerInvariant(h, [t], 'lost');
    h.timers.advance(h.timers.onlyDelay());
    assertTimerInvariant(h, [t], 'connecting');
    h.socket.open();
    assertTimerInvariant(h, [t], 'handshaking');
    h.socket.accept({ ...DEDUP, waking: true });
    assertTimerInvariant(h, [t], 'waking');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'ready', pct: 100 } });
    assert.equal(h.client.getState(), 'live');
    assert.equal(sentOf(h.socket, 'Buy').length, 1);
  });
});

describe('r3 ruling 2: the hold rule snapshots command.dedup at send time', () => {
  test('sent WITHOUT dedup: rejected MET-V937 at the drop, before any reconnect, even if the next handshake has dedup', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: [] });
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(t.state, 'rejected', 'settled at the drop, not held');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    assert.equal(sentOf(h.socket, 'Buy').length, 0, 'never re-sent');
    assert.equal(t.count, 1);
  });

  test('sent WITH dedup: held at the drop (still pending), re-sent once on a dedup handshake', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(t.state, 'pending', 'held');
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, DEDUP);
    assert.equal(sentOf(h.socket, 'Buy').length, 1);
    assert.equal(t.state, 'pending');
  });

  test('sent WITH dedup, new handshake without dedup: rejected MET-V937, never re-sent', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: [] });
    await flushMicro();
    assert.equal(t.state, 'rejected');
    assert.equal(sentOf(h.socket, 'Buy').length, 0);
  });

  test('the snapshot is per send: a write sent on a dedup-less connection after a dedup one is not held', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const t1 = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: [] });
    const t2 = track(h.client.sendCommand('Zone', {}));
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(t1.state, 'rejected');
    assert.equal(t2.state, 'rejected', 'sent without dedup, rejected at the drop');
  });

  test('A4: an unknown Kind is classified as a write (held under dedup, V937 without); a read Kind is rejected retryably', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    const w = track(h.client.sendCommand('SomeFutureKind', {}));
    const r = track(h.client.sendCommand('QueryCitizen', {}));
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(w.state, 'pending', 'unknown kind held like a write');
    assert.equal(r.state, 'rejected');
    assert.equal(r.err.code, ERR_ENGINE_UNREACHABLE);
    h.timers.advance(h.timers.onlyDelay());
    goLive(h, { capabilities: [] });
    await flushMicro();
    assert.equal(w.state, 'rejected');
    assert.equal(w.err.code, ERR_COMMAND_OUTCOME_UNKNOWN, 'a write, not a read');
  });
});

describe('r3 ruling 3: waking has an overall deadline (the promise bound is commandDeadlineMs, r4)', () => {
  test('progress frames reset the silence timer but cannot extend the overall deadline', () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    h.socket.open();
    h.socket.accept({ ...DEDUP, waking: true });
    const dl = RECONNECT_DEFAULTS.wakeDeadlineMs;
    assert.ok(dl > RECONNECT_DEFAULTS.wakeTimeoutMs, 'the deadline is the longer of the two');
    const start = h.timers.now;
    while (h.timers.now - start < dl - 30000) {
      h.timers.advance(30000);
      assert.equal(h.client.getState(), 'waking', `still waking at ${h.timers.now - start} ms`);
      wakingProgress(h);
    }
    h.timers.advance(30000 + 1);
    assert.equal(h.client.getState(), 'reconnecting', 'overall deadline fired despite steady progress');
  });

  test('the single stated bound is commandDeadlineMs: a held write in a never-ready wake cycle settles V937 at it, from its first send', async () => {
    const D = RECONNECT_DEFAULTS.commandDeadlineMs;
    const h = make({ rng: 0.999999, lifecycle: { commandDeadlineMs: D } }); // maximum jitter every time
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    const start = h.timers.now;
    let settledAt = null;
    let driven = null;
    for (let tick = 0; tick < D / 1000 + 100 && settledAt === null; tick++) {
      if (h.socket !== driven) {
        driven = h.socket;
        h.socket.open();
        h.socket.accept({ ...DEDUP, waking: true });
      }
      if (tick % 30 === 0 && h.client.getState() === 'waking') wakingProgress(h); // steady progress, never ready
      await flushMicro();
      if (t.state !== 'pending') settledAt = h.timers.now - start;
      else h.timers.advance(1000);
    }
    assert.notEqual(settledAt, null, 'settled');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.ok(settledAt <= D + 1000, `pending ${settledAt} ms exceeds commandDeadlineMs ${D}`);
    assert.ok(settledAt >= D - 1000, `settled at ${settledAt} ms, long before the deadline ${D}: the scenario is not exercising the backstop`);
  });
});

describe('r3 A1: restore with a socket that died under it', () => {
  test('a Subscribe write that kills the socket during restore: the held write is rejected, never marked re-sent or left pending (probe N1)', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    h.client.subscribe('f2.finance');
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006);
    h.timers.advance(h.timers.onlyDelay());
    const s2 = h.socket;
    s2.send = function (d) {
      FakeSocket.prototype.send.call(this, d);
      const m = JSON.parse(d);
      if (m.params && m.params.kind === 'Subscribe') this.serverCloses(1006);
    };
    goLive(h, DEDUP);
    await flushMicro();
    assert.equal(t.state, 'rejected', 'not pending forever');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.equal(t.count, 1);
    assert.equal(sentOf(s2, 'Buy').length, 0, 'it was never written');
    h.timers.advance(20 * 3600 * 1000);
    await flushMicro();
    assert.equal(t.count, 1, 'settled exactly once');
  });
});

describe('r3 A2: retry() with a socket goes through the loss path', () => {
  test('retry() while live: the in-flight dedup write is held and re-sent once, subscriptions are restored (probe N8)', async () => {
    const h = make({ rng: 0 });
    goLive(h, DEDUP);
    h.client.subscribe('f2.finance');
    const t = track(h.client.sendCommand('Buy', {}));
    h.client.retry();
    goLive(h, DEDUP);
    await flushMicro();
    assert.equal(subscribeFrames(h.socket).length, 1, 'subscription restored on the new connection');
    assert.equal(sentOf(h.socket, 'Buy').length, 1, 'held write re-sent once');
    assert.equal(h.log.live.at(-1).info.reconnected, true);
    h.socket.serverCloses(1006);
    await flushMicro();
    assert.equal(t.state, 'rejected', 'a drop after the single re-send settles it');
    assert.equal(t.count, 1);
  });

  test('retry() while live without dedup: the in-flight write is rejected, nothing orphaned', async () => {
    const h = make({ rng: 0 });
    goLive(h, { capabilities: [] });
    const t = track(h.client.sendCommand('Buy', {}));
    h.client.retry();
    await flushMicro();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    goLive(h);
    h.timers.advance(60000);
    await flushMicro();
    assert.equal(t.count, 1);
  });
});
