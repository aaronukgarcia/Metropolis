// protocol-reconnect-r5.test.mjs -- FEAT-9995385597 inc1, round r5: "before the
// handshake reply" is decided on handshakeDone alone; live is entered only after
// the reply, at most once per socket (probe A1 of round r4, plus an orderings table).
//
// R5_CLIENT (a path relative to this file) points the file at another copy of the
// client; it exists only to show these tests RED against the pre-r5 client.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

const CLIENT_PATH = process.env.R5_CLIENT ?? '../src/sim/protocolClient.ts';
const { ProtocolClient } = await import(CLIENT_PATH);

class FakeSocket {
  constructor() {
    this.sent = [];
    this.closed = false;
    this.opened = false;
    this.listeners = { open: [], message: [], close: [], error: [] };
  }
  addEventListener(type, fn) { this.listeners[type].push(fn); }
  send(data) { this.sent.push(JSON.parse(data)); }
  close(code, reason) { this.closed = true; this.emit('close', { code, reason }); }
  emit(type, ev) { for (const fn of this.listeners[type]) fn(ev); }
  serverSends(msg) { this.emit('message', { data: JSON.stringify(msg) }); }
  serverCloses(code, reason) { this.closed = true; this.emit('close', { code, reason }); }
  open() { this.opened = true; this.emit('open', {}); }
  accept(extra = {}) {
    this.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3', capabilities: [], ...extra } });
  }
}

class FakeTimers {
  constructor() { this.now = 0; this.seq = 0; this.timers = new Map(); }
  setTimeout(fn, ms) { const id = ++this.seq; this.timers.set(id, { at: this.now + ms, fn, ms }); return id; }
  clearTimeout(id) { this.timers.delete(id); }
  advance(ms) {
    const target = this.now + ms;
    for (;;) {
      let nextId = null;
      let next = null;
      for (const [id, t] of this.timers) if (t.at <= target && (next === null || t.at < next.at)) { nextId = id; next = t; }
      if (!next) break;
      this.timers.delete(nextId);
      this.now = next.at;
      next.fn();
    }
    this.now = target;
  }
}

function make() {
  const timers = new FakeTimers();
  const sockets = [];
  const log = { live: [], liveSock: [], liveHs: [], states: [] };
  const ref = { client: null };
  let n = 0;
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion: 'v1.2.3',
    createSocket: () => { const s = new FakeSocket(); sockets.push(s); return s; },
    onStateChange: (s) => log.states.push(s),
    onLive: (v, nv, caps, info) => {
      log.live.push(info);
      log.liveSock.push(sockets.length - 1);
      log.liveHs.push(ref.client._testHandshakeDone);
    },
    newCorrelationId: () => `c${++n}`,
    lifecycle: { random: () => 0, timers, clientSessionId: 'sess-abc' },
  });
  ref.client = client;
  client.connect();
  return { client, timers, sockets, log, get socket() { return sockets.at(-1); } };
}

const DEDUP = { capabilities: ['command.dedup'] };
const W = (phase, pct = 50) => ({ jsonrpc: '2.0', method: 'server.waking', params: { phase, pct } });
const flush = () => new Promise((r) => setImmediate(r));
const frames = (s, kind) => s.sent.filter((m) => m.method === 'command' && m.params.kind === kind);
function track(p) {
  const t = { state: 'pending', err: null };
  p.then(() => (t.state = 'resolved'), (e) => { t.state = 'rejected'; t.err = e; });
  return t;
}
/** Live, a Subscribe issued, a dedup write in flight, then the socket drops and the retry socket opens (reply outstanding). */
function toReconnectSocket(h) {
  h.socket.open();
  h.socket.accept(DEDUP);
  h.client.subscribe('f2.finance');
  const t = track(h.client.sendCommand('Buy', {}));
  h.socket.serverCloses(1006, '');
  h.timers.advance(1000);
  h.socket.open();
  return t;
}

describe('r5 A1: waking(fetching) then waking(ready) BEFORE the reply on a reconnect', () => {
  test('not live before the reply; onLive once with reconnected; write re-sent once; one Subscribe; commands accepted when live', async () => {
    const h = make();
    const t = toReconnectSocket(h);
    const liveBefore = h.log.live.length; // 1: the first connection
    h.socket.serverSends(W('fetching', 30));
    h.socket.serverSends(W('ready', 100));
    assert.notEqual(h.client.getState(), 'live', 'live before the reply');
    assert.equal(h.log.live.length, liveBefore, 'onLive fired before the reply');
    const firstBuy = frames(h.sockets[0], 'Buy')[0];
    h.socket.accept(DEDUP);
    await flush();
    assert.equal(h.client.getState(), 'live');
    assert.equal(h.log.live.length, liveBefore + 1, 'onLive exactly once for the reconnect socket');
    assert.equal(h.log.live.at(-1).reconnected, true);
    assert.equal(frames(h.socket, 'Buy').length, 1, 'held write re-sent exactly once');
    assert.equal(frames(h.socket, 'Buy')[0].params.correlationId, firstBuy.params.correlationId, 'same correlationId');
    assert.equal(t.state, 'pending', 'the re-sent write is awaiting its result, not settled V937');
    assert.equal(frames(h.socket, 'Subscribe').length, 1, 'Subscribe re-sent exactly once');
    const p2 = track(h.client.sendCommand('Buy', {}));
    assert.equal(frames(h.socket, 'Buy').length, 2, 'a command is accepted as soon as live is shown');
    assert.equal(p2.state, 'pending');
  });

  test('after TWO such reconnects there is still exactly one Subscribe per view per reconnect', async () => {
    const h = make();
    h.socket.open();
    h.socket.accept(DEDUP);
    h.client.subscribe('f2.finance');
    for (let i = 0; i < 2; i++) {
      h.socket.serverCloses(1006, '');
      h.timers.advance(60000);
      h.socket.open();
      h.socket.serverSends(W('fetching', 30));
      h.socket.serverSends(W('ready', 100));
      h.socket.accept(DEDUP);
      await flush();
      assert.equal(h.client.getState(), 'live');
      assert.equal(frames(h.socket, 'Subscribe').length, 1, `reconnect ${i + 1}: one Subscribe`);
    }
    assert.equal(h.log.live.length, 3);
    assert.equal(new Set(h.log.liveSock).size, 3, 'one onLive per socket');
  });
});

describe('r5 orderings: live at most once per socket, only after the reply', () => {
  const R = (extra = {}) => ({ reply: true, extra });
  const cases = [
    ['reply then ready', [R(), W('ready', 100)]],
    ['ready then reply', [W('ready', 100), R()]],
    ['fetching, ready, reply', [W('fetching'), W('ready', 100), R()]],
    ['fetching, reply, ready', [W('fetching'), R(), W('ready', 100)]],
    ['reply (waking:true) then ready', [R({ waking: true }), W('ready', 100)]],
    ['two readies then reply', [W('ready', 100), W('ready', 100), R()]],
    ['reply then two readies', [R(), W('ready', 100), W('ready', 100)]],
    ['fetching, ready, reply(waking), ready', [W('fetching'), W('ready', 100), R({ waking: true }), W('ready', 100)]],
  ];
  for (const reconnect of [false, true]) {
    for (const [name, seq] of cases) {
      test(`${reconnect ? 'reconnect' : 'first connect'}: ${name}`, async () => {
        const h = make();
        let t = null;
        if (reconnect) t = toReconnectSocket(h);
        else h.socket.open();
        const base = h.log.live.length;
        const sock = h.socket;
        for (const step of seq) {
          if (step.reply) sock.accept({ ...DEDUP, ...step.extra });
          else sock.serverSends(step);
          if (!h.client._testHandshakeDone) {
            assert.notEqual(h.client.getState(), 'live', 'live before the reply');
            assert.equal(h.log.live.length, base, 'onLive before the reply');
          }
        }
        await flush();
        assert.equal(h.log.live.length, base + 1, 'onLive exactly once on this socket');
        assert.equal(h.client.getState(), 'live');
        assert.ok(h.log.liveHs.every((x) => x === true), 'never live with handshakeDone false');
        if (reconnect) {
          assert.equal(frames(sock, 'Buy').length, 1, 'held write re-sent once');
          assert.equal(t.state, 'pending');
          assert.equal(frames(sock, 'Subscribe').length, 1);
        }
      });
    }
  }

  test('a ready on the OLD socket after the reconnect is ignored', async () => {
    const h = make();
    toReconnectSocket(h);
    const old = h.sockets[0];
    const base = h.log.live.length;
    old.serverSends(W('ready', 100));
    assert.equal(h.log.live.length, base);
    assert.notEqual(h.client.getState(), 'live');
    h.socket.accept(DEDUP);
    await flush();
    old.serverSends(W('ready', 100));
    assert.equal(h.log.live.length, base + 1);
  });
});
