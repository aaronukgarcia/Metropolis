// protocol-reconnect-r4.test.mjs -- FEAT-9995385597 inc1, round r4 (structural):
// ONE settle function, ONE exit function, the per-command BACKSTOP deadline, the
// draining-floor clamp, and the three attacker probes of round r3 (P7 = B1,
// P4/P4b = B2, P9, P10) re-created as the lane's own tests, plus 500 seeded
// random walks over the fake socket and the client API.
//
// Driven against a fake WebSocket and a hand-advanced timer wheel (AC-T1: no
// real timers, no Date.now/Math.random on the tested path).
//
// R4_CLIENT (a path relative to this file) points the whole file at another copy
// of the client; it exists only to show these tests RED against the pre-r4 client.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { QueueDepthTracker, PROTOCOL_ENGINE_KEY } from '../src/sim/queueDepth.ts';
import { backoffDelayMs, RECONNECT_DEFAULTS } from '../src/sim/reconnect.ts';

const CLIENT_PATH = process.env.R4_CLIENT ?? '../src/sim/protocolClient.ts';
const { ProtocolClient, ERR_COMMAND_OUTCOME_UNKNOWN, ERR_ENGINE_UNREACHABLE, ERR_HANDSHAKE_INVALID } = await import(CLIENT_PATH);

class FakeSocket {
  constructor() {
    this.sent = [];
    this.closed = false;
    this.opened = false;
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
    this.emit('close', { code, reason });
  }
  emit(type, ev) {
    for (const fn of this.listeners[type]) fn(ev);
  }
  serverSends(msg) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
  serverSendsRaw(raw) {
    this.emit('message', { data: raw });
  }
  serverCloses(code, reason) {
    this.closed = true;
    this.emit('close', { code, reason });
  }
  open() {
    this.opened = true;
    this.emit('open', {});
  }
  accept(extra = {}) {
    this.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3', capabilities: [], ...extra } });
  }
}

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
  get pending() {
    return this.timers.size;
  }
  count(ms) {
    return [...this.timers.values()].filter((t) => t.ms === ms).length;
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

class SpyTracker extends QueueDepthTracker {
  constructor() {
    super();
    this.inc = 0;
    this.dec = 0;
  }
  increment(e) {
    this.inc += 1;
    super.increment(e);
  }
  decrement(e) {
    this.dec += 1;
    super.decrement(e);
  }
}

function make({ rng = 0, lifecycle = {}, opts = {} } = {}) {
  const timers = new FakeTimers();
  const sockets = [];
  const tracker = new SpyTracker();
  const log = { states: [], refusals: [], live: [], liveSock: [], liveHs: [] };
  let n = 0;
  const ref = { client: null };
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
    onLive: (v, nv, caps, info) => {
      log.live.push({ v, caps, info });
      log.liveSock.push(sockets.length - 1);
      log.liveHs.push(ref.client?._testHandshakeDone);
    },
    newCorrelationId: () => `c${++n}`,
    queueTracker: tracker,
    lifecycle: { random: () => rng, timers, clientSessionId: 'sess-abc', ...lifecycle },
    ...opts,
  });
  ref.client = client;
  client.connect();
  return { client, timers, sockets, tracker, log, get socket() { return sockets.at(-1); } };
}

const DEDUP = { capabilities: ['command.dedup'] };
const H = 24 * 3600 * 1000;
const flush = () => new Promise((r) => setImmediate(r));
const goLive = (h, extra = {}) => {
  h.socket.open();
  h.socket.accept(extra);
};
/** Tracks a promise: state() is 'pending' | 'resolved' | 'rejected'; err() the rejection. */
function track(p) {
  const t = { state: 'pending', err: null, at: null };
  p.then(
    () => (t.state = 'resolved'),
    (e) => {
      t.state = 'rejected';
      t.err = e;
    },
  );
  return t;
}
const buys = (h) => h.sockets.flatMap((s) => s.sent).filter((m) => m.method === 'command' && m.params.kind === 'Buy');
const hib = { jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'idle' } };

describe('r4 ruling 1 (B1): a re-sent write whose ack is an ERROR settles exactly once', () => {
  test('P7: the error ack of the re-send rejects the promise with that error; nothing left behind', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006, '');
    h.timers.advance(1000);
    goLive(h, DEDUP);
    const resend = h.socket.sent.find((m) => m.method === 'command' && m.params.kind === 'Buy');
    assert.ok(resend, 'the write was re-sent on the new socket');
    h.socket.serverSends({ jsonrpc: '2.0', id: resend.id, error: { code: 'MET-X1', message: 'nope' } });
    await flush();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, 'MET-X1');
    assert.equal(h.tracker.inc, 1);
    assert.equal(h.tracker.dec, 1, 'queue depth decremented exactly once');
    assert.equal(h.tracker.depthOf(PROTOCOL_ENGINE_KEY), 0);
    h.timers.advance(H);
    await flush();
    assert.equal(h.tracker.dec, 1, 'no later second decrement');
  });
});

describe('r4 ruling 2 (B2): a lifecycle frame before the handshake reply is that frame, never a refusal', () => {
  test('P4: server.hibernating while handshaking on a reconnect with a held dedup write -> asleep, write settled V937, zero timers', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006, '');
    h.timers.advance(1000);
    h.socket.open(); // socket 2: handshake reply outstanding
    h.socket.serverSends(hib);
    await flush();
    assert.equal(h.client.getState(), 'asleep');
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.deepEqual(h.log.refusals, [], 'not a refusal');
    h.timers.advance(H);
    await flush();
    assert.equal(h.client.getState(), 'asleep', 'stays asleep');
    assert.equal(h.timers.pending, 0);
  });

  test('P4b: server.hibernating while handshaking on the FIRST connect -> asleep (clickable), zero timers, no refusal', () => {
    const h = make();
    h.socket.open();
    h.socket.serverSends(hib);
    assert.equal(h.client.getState(), 'asleep');
    h.timers.advance(H);
    assert.equal(h.client.getState(), 'asleep');
    assert.equal(h.timers.pending, 0);
    assert.deepEqual(h.log.refusals, []);
  });

  test('server.draining before the reply is a loss that honours the (clamped) floor, not a refusal', () => {
    const h = make({ rng: 0 });
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 20000, reason: 'deploy' } });
    assert.equal(h.client.getState(), 'reconnecting');
    assert.deepEqual(h.log.refusals, []);
    assert.equal(h.timers.pending, 1);
    h.timers.advance(19999);
    assert.equal(h.sockets.length, 1, 'floor honoured');
    h.timers.advance(2);
    assert.equal(h.sockets.length, 2);
  });

  test('server.waking before the reply enters waking; the reply then goes live on ready; no refusal', () => {
    const h = make();
    h.socket.open();
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'fetching', pct: 30 } });
    assert.equal(h.client.getState(), 'waking');
    assert.equal(h.client.getWakeProgress().pct, 30);
    h.socket.accept({ waking: true });
    assert.equal(h.client.getState(), 'waking');
    assert.equal(h.client.getWakeProgress().pct, 30, 'progress not reset by the reply');
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'ready', pct: 100 } });
    assert.equal(h.client.getState(), 'live');
    assert.deepEqual(h.log.refusals, []);
  });

  test('a genuinely not-accepted reply after a reconnect ends REFUSED through the exit function: held write settled, one close, zero timers, a way out (retry)', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006, '');
    h.timers.advance(1000);
    h.socket.open();
    const sock2 = h.socket;
    sock2.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: false } });
    await flush();
    assert.equal(h.client.getState(), 'refused');
    assert.equal(h.client.getTerminal().code, ERR_HANDSHAKE_INVALID);
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.ok(sock2.closed, 'the socket was closed');
    assert.equal(h.timers.pending, 0);
    assert.equal(h.log.refusals.length, 1, 'reported once');
    // a way out: retry() connects again
    const socketsBefore = h.sockets.length;
    h.client.retry();
    assert.equal(h.sockets.length, socketsBefore + 1);
    assert.equal(h.client.getState(), 'connecting');
  });
});

describe('r4 ruling 3: the BACKSTOP deadline bounds every command promise', () => {
  const D = RECONNECT_DEFAULTS.commandDeadlineMs;
  test('the default is a named config of 120000 ms', () => {
    assert.equal(D, 120000);
  });

  test('P1 live half-open (no result, no close, no error): a write rejects MET-V937 at commandDeadlineMs, not before', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.timers.advance(D - 1);
    await flush();
    assert.equal(t.state, 'pending');
    h.timers.advance(1);
    await flush();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.equal(h.tracker.dec, 1);
    assert.equal(h.timers.count(D), 0);
  });

  test('a read gets the same timer and its existing code (retryable MET-P013)', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('QueryCitizen', {}));
    h.timers.advance(D);
    await flush();
    assert.equal(t.err.code, ERR_ENGINE_UNREACHABLE);
    assert.equal(t.err.retryable, true);
  });

  test('the deadline is NOT restarted by a hold, a re-send or a reconnect: held then re-sent, it still ends D after its first send', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.timers.advance(60000);
    h.socket.serverCloses(1006, '');
    h.timers.advance(1000);
    goLive(h, DEDUP); // re-sent here, ~61 s after the first send
    assert.equal(buys(h).length, 2);
    h.timers.advance(D - h.timers.now - 1);
    await flush();
    assert.equal(t.state, 'pending', 'still pending just before D after the FIRST send');
    h.timers.advance(1);
    await flush();
    assert.equal(t.state, 'rejected');
    assert.equal(t.err.code, ERR_COMMAND_OUTCOME_UNKNOWN);
    assert.equal(h.timers.count(D), 0);
  });

  test('a write held across a long reconnect cycle settles at D from its first send (waking, never ready)', async () => {
    const h = make({ rng: 0.999 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006, '');
    let elapsed = 0;
    let last = h.socket;
    while (t.state === 'pending' && elapsed < 4 * D) {
      if (h.socket !== last && h.client.getState() === 'connecting') {
        last = h.socket;
        h.socket.open();
        h.socket.accept({ ...DEDUP, waking: true });
      }
      h.timers.advance(1000);
      elapsed += 1000;
      await flush();
    }
    assert.equal(t.state, 'rejected');
    assert.ok(elapsed <= D + 1000, `settled after ${elapsed} ms, bound ${D}`);
  });

  test('P9: the draining floor of 1 h is clamped; a held write settles within the backstop, not hours later', async () => {
    const h = make({ rng: 0.999 });
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 3600000, reason: 'deploy' } });
    h.timers.advance(D + 1000);
    await flush();
    assert.equal(t.state, 'rejected');
    assert.ok(h.sockets.length >= 2, 'a reconnect attempt was made within the clamped floor, not after 1 h');
  });
});

describe('r4 ruling 4: the draining floor is clamped; retry() keeps the schedule while a write is held', () => {
  test('backoffDelayMs clamps a 1 h floor to maxFloorMs (default 60 s); NaN counts as no floor', () => {
    assert.equal(RECONNECT_DEFAULTS.maxFloorMs, 60000);
    assert.equal(backoffDelayMs(0, () => 0, 3600000), 60000);
    assert.equal(backoffDelayMs(0, () => 0, NaN), RECONNECT_DEFAULTS.minDelayMs);
    assert.equal(backoffDelayMs(0, () => 0, 5000), 5000);
  });

  test('client: a server.draining reconnectAfterMs of 1 h reconnects within 60 s', () => {
    const h = make({ rng: 0 });
    goLive(h);
    h.socket.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 3600000, reason: 'deploy' } });
    h.timers.advance(59999);
    assert.equal(h.sockets.length, 1);
    h.timers.advance(1);
    assert.equal(h.sockets.length, 2);
  });

  test('P10: a caller retry() loop with a held write never resets the attempt counter; the cycle gives up and the write settles', async () => {
    const h = make();
    goLive(h, DEDUP);
    const t = track(h.client.sendCommand('Buy', {}));
    h.socket.serverCloses(1006, '');
    let maxAttempt = 0;
    for (let i = 0; i < 500 && t.state === 'pending'; i++) {
      h.client.retry();
      maxAttempt = Math.max(maxAttempt, h.client.getReconnectAttempt());
      if (h.socket && !h.socket.closed) h.socket.serverCloses(1006, '');
      h.timers.advance(1000);
      await flush();
    }
    assert.equal(t.state, 'rejected', 'the loop did not keep the write alive');
    assert.ok(maxAttempt >= RECONNECT_DEFAULTS.maxAttempts - 1, `attempt counter climbed to ${maxAttempt}`);
  });

  test('retry() with NOTHING held still resets the schedule', () => {
    const h = make();
    goLive(h);
    h.socket.serverCloses(1006, '');
    assert.equal(h.client.getReconnectAttempt(), 1);
    h.client.retry();
    h.socket.serverCloses(1006, '');
    assert.equal(h.client.getReconnectAttempt(), 1, 'reset to 0 by retry(), then 1 for the new loss');
  });
});

describe('r4 ruling 1 & 2: structure (source scan)', () => {
  const src = readFileSync(new URL(CLIENT_PATH, import.meta.url), 'utf8');
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/\s\/\/.*$/gm, '');
  const fnBody = (name) => {
    const a = code.indexOf(`private ${name}(`);
    assert.ok(a >= 0, `${name} exists`);
    const b = code.indexOf('\n  }\n', a);
    return { a, b: b + 4, body: code.slice(a, b + 4) };
  };

  test('pendingResults.delete appears exactly once, inside settleCommand', () => {
    const { a, b, body } = fnBody('settleCommand');
    const outside = code.slice(0, a) + code.slice(b);
    assert.equal((body.match(/pendingResults\.delete/g) ?? []).length, 1);
    assert.equal((outside.match(/pendingResults\.(delete|clear)/g) ?? []).length, 0, 'no other code removes a pending command');
  });

  test('a pending command is resolved/rejected (.resolve( / .reject() only inside settleCommand', () => {
    const { a, b, body } = fnBody('settleCommand');
    const outside = code.slice(0, a) + code.slice(b);
    assert.ok(/\.resolve\(/.test(body) && /\.reject\(/.test(body));
    assert.deepEqual(outside.match(/\.(resolve|reject)\(/g) ?? [], []);
  });

  test('the connection is left only through exitConnection: no other code closes the socket or settles by hand', () => {
    const { a, b } = fnBody('exitConnection');
    const outside = code.slice(0, a) + code.slice(b);
    // Legacy (non-lifecycle) mode closes its own socket; every lifecycle path goes through exitConnection.
    assert.equal((outside.match(/dropSocket|settleAllOnExit|failPendingOnLoss/g) ?? []).length, 0);
    assert.equal((outside.match(/\bsock(et)?\.close\(/g) ?? []).length, 0, 'no direct socket close outside the exit function');
    // The three remaining are the LEGACY (non-lifecycle, no reconnect machine) paths: close() and its two handshake refusals.
    assert.equal((outside.match(/this\.socket\??\.close\(/g) ?? []).length, 3, 'only the legacy paths');
  });
});

// ---------------------------------------------------------------------------
// Random walks
// ---------------------------------------------------------------------------

function mulberry32(a) {
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const GARBAGE = [
  'not json', 'null', '42', '"x"', '[]', '{}', '{"method":"delta"}', '{"method":"result"}',
  '{"method":"server.waking"}', '{"method":"server.waking","params":{"phase":"zzz"}}', '{"method":"server.draining"}',
  '{"id":1,"result":{"accepted":false}}', '{"id":1,"result":null}', '{"id":99,"error":{"code":"MET-X","message":"m"}}',
  '{"method":"nope.unknown","params":{}}',
];
const KINDS = ['Buy', 'Build', 'SetSpeed', 'Pause', 'AdvanceTicks', 'QueryCitizen', 'InspectEntity', 'Mystery'];
const WALK_DEADLINE = 123456;
/** Coverage of the walks: events that happened while a written command was in flight. */
const COV = { wire: 0, close: 0, errorAck: 0, lateOld: 0, lifecycleBeforeHandshake: 0, retry: 0, closeCall: 0, earlyReady: 0 };

async function walk(seed) {
  const rnd = mulberry32(seed);
  const pick = (a) => a[Math.floor(rnd() * a.length)];
  const h = make({ rng: rnd(), lifecycle: { commandDeadlineMs: WALK_DEADLINE, random: () => rnd() } });
  const promises = [];
  const firstSeen = new Map();
  const pr = () => h.client.pendingResults;
  const sentFrames = () => h.sockets.flatMap((s) => s.sent.filter((m) => m.method === 'command'));
  const steps = 25 + Math.floor(rnd() * 40);
  for (let i = 0; i < steps; i++) {
    const cur = h.socket;
    const live = cur && cur.opened && !cur.closed;
    const caps = rnd() < 0.7 ? DEDUP : {};
    // Bias: most steps first bring the current connection towards live, so commands reach the wire.
    if (rnd() < 0.6) {
      if (cur && !cur.opened && !cur.closed) cur.open();
      else if (live && h.client.getState() === 'handshaking' && rnd() < 0.5) cur.accept({ ...caps });
    }
    if (h.client.isLive() && rnd() < 0.5) promises.push(track(h.client.sendCommand(pick(KINDS), {})));
    if (['asleep', 'refused', 'terminal', 'gaveUp'].includes(h.client.getState()) && rnd() < 0.5) h.client.wakeFromAsleep();
    const inFlight = pr().size > 0;
    const hs = h.client.getState() === 'handshaking';
    // Lifecycle frame before the handshake reply (the r3 B2 shape), injected often.
    if (live && hs && inFlight && rnd() < 0.3) {
      COV.lifecycleBeforeHandshake++;
      cur.serverSends(pick([hib, { jsonrpc: '2.0', method: 'server.waking', params: { phase: 'fetching', pct: 10 } }, { jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: 500, reason: 'deploy' } }]));
    }
    if (live && hs && rnd() < 0.25) {
      // r5: early waking frames INCLUDING ready before the reply (the A1 shape)
      COV.earlyReady++;
      cur.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'fetching', pct: 30 } });
      cur.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'ready', pct: 100 } });
    }
    switch (Math.floor(rnd() * 26)) {
      case 0: case 1: case 2: case 3: case 4:
        // progress the current connection: open it, then accept its handshake
        if (cur && !cur.opened && !cur.closed) cur.open();
        else if (live && h.client.getState() === 'handshaking') cur.accept({ ...caps, ...(rnd() < 0.25 ? { waking: true } : {}) });
        break;
      case 5:
        if (cur && !cur.closed) {
          if (inFlight) COV.close++;
          cur.serverCloses(pick([1006, 1012, 1000, 4000, 1001]), pick(['', 'hibernate', 'x']));
        }
        break;
      case 6:
        if (cur && !cur.closed) {
          if (inFlight) COV.close++;
          cur.emit('error', {});
        }
        break;
      case 7:
        if (live) cur.serverSendsRaw(pick(GARBAGE));
        break;
      case 8: case 9: case 10: case 11: case 24: case 25:
        promises.push(track(h.client.sendCommand(pick(KINDS), {})));
        break;
      case 12:
        if (live) {
          const f = pick(sentFrames().concat([{ params: { correlationId: 'unknown' } }]));
          cur.serverSends({ jsonrpc: '2.0', method: 'result', params: rnd() < 0.7 ? { correlationId: f.params.correlationId, accepted: true } : { correlationId: f.params.correlationId, accepted: false, error: { code: 'MET-X9', display: 'no' } } });
        }
        break;
      case 13:
        if (live) {
          const f = pick(sentFrames().concat([{ id: 424242 }]));
          if (inFlight && f.id !== 424242) COV.errorAck++;
          cur.serverSends({ jsonrpc: '2.0', id: f.id, error: { code: 'MET-X2', message: 'ack err' } });
        }
        break;
      case 14:
        if (live) {
          const f = pick(sentFrames().concat([{ id: 424242 }]));
          cur.serverSends({ jsonrpc: '2.0', id: f.id, result: { queued: true } });
        }
        break;
      case 15:
        if (live && inFlight && hs) COV.lifecycleBeforeHandshake++;
        if (live) cur.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: pick(['queued', 'fetching', 'replaying', 'ready', 'failed']), pct: Math.floor(rnd() * 100), code: 'MET-P030' } });
        break;
      case 16:
        if (live && inFlight && hs) COV.lifecycleBeforeHandshake++;
        if (live) cur.serverSends({ jsonrpc: '2.0', method: 'server.draining', params: { reconnectAfterMs: pick([0, 500, 20000, 3600000]), reason: 'deploy' } });
        break;
      case 17:
        if (live && inFlight && hs) COV.lifecycleBeforeHandshake++;
        if (live) cur.serverSends(hib);
        break;
      case 18:
        if (live) cur.serverSends({ jsonrpc: '2.0', id: 1, error: { code: pick(['MET-P011', 'MET-P043', 'MET-P010', 'MET-P030']), message: 'refused', data: rnd() < 0.5 ? { retryable: true } : undefined } });
        break;
      case 19:
        if (h.sockets.length > 1) {
          const old = pick(h.sockets.slice(0, -1));
          if (inFlight) COV.lateOld++;
          old.serverSends({ jsonrpc: '2.0', method: 'result', params: { correlationId: pick(sentFrames().concat([{ params: { correlationId: 'zz' } }])).params.correlationId, accepted: true } });
        }
        break;
      case 20:
        if (inFlight) COV.retry++;
        h.client.retry();
        break;
      case 21:
        if (rnd() < 0.3) {
          if (inFlight) COV.closeCall++;
          h.client.close();
        }
        break;
      case 22:
        if (rnd() < 0.3) h.client.wakeFromAsleep();
        else h.client.enterAsleep('hidden');
        break;
      case 23:
        if (rnd() < 0.5) h.client.connect();
        break;
      default:
        break;
    }
    h.timers.advance(Math.floor(rnd() * (rnd() < 0.85 ? 3000 : 60000)));
    // Bound, checked at every step: no pending command is older than the deadline,
    // and every pending command has an armed deadline timer.
    for (const [cid, p] of pr()) {
      if (!firstSeen.has(cid)) firstSeen.set(cid, h.timers.now);
      assert.ok(h.timers.now - firstSeen.get(cid) <= WALK_DEADLINE, `seed ${seed}: ${cid} pending ${h.timers.now - firstSeen.get(cid)} ms`);
      assert.ok(p.deadline !== null && h.timers.timers.has(p.deadline), `seed ${seed}: ${cid} has no armed deadline timer`);
    }
    if (i % 5 === 0) await Promise.resolve();
  }
  h.timers.advance(WALK_DEADLINE + 1000);
  await flush();
  const pendingPromises = promises.filter((p) => p.state === 'pending').length;
  assert.equal(pendingPromises, 0, `seed ${seed}: ${pendingPromises} promises still pending`);
  assert.equal(pr().size, 0, `seed ${seed}: pendingResults not empty`);
  assert.equal(h.timers.count(WALK_DEADLINE), 0, `seed ${seed}: armed command timers`);
  assert.equal(h.tracker.dec, h.tracker.inc, `seed ${seed}: queue inc ${h.tracker.inc} != dec ${h.tracker.dec} (a command settled twice, or never)`);
  assert.equal(h.tracker.depthOf(PROTOCOL_ENGINE_KEY), 0, `seed ${seed}: queue depth`);
  // r5: onLive at most once per socket, and never with handshakeDone false.
  const perSock = new Map();
  for (const k of h.log.liveSock) perSock.set(k, (perSock.get(k) ?? 0) + 1);
  for (const [k, c] of perSock) assert.ok(c <= 1, `seed ${seed}: onLive fired ${c} times on socket ${k}`);
  for (const hs of h.log.liveHs) assert.notEqual(hs, false, `seed ${seed}: live entered with handshakeDone false`);
  COV.wire += h.tracker.inc;
  return { promises: promises.length, counted: h.tracker.inc };
}

describe('r4 ruling 3: random walks (seeded)', () => {
  test('500 walks over socket events and client calls: every promise settles exactly once, within the deadline, nothing armed, depth 0', async () => {
    let promises = 0;
    let counted = 0;
    for (let seed = 1; seed <= 500; seed++) {
      const r = await walk(seed);
      promises += r.promises;
      counted += r.counted;
    }
    console.log(`random walks: 500 walks, ${promises} command promises (${counted} reached the wire), all settled once, zero armed command timers, queue depth 0`);
    console.log(`walk coverage (events while a written command was in flight): ${JSON.stringify(COV)}`);
    assert.ok(counted >= 1000, `at least 1000 promises reached the wire (${counted})`);
    for (const k of ['close', 'errorAck', 'lateOld', 'lifecycleBeforeHandshake', 'retry', 'closeCall', 'earlyReady']) {
      assert.ok(COV[k] >= 50, `coverage: ${k} occurred ${COV[k]} times while a command was in flight (need 50)`);
    }
  });
});
