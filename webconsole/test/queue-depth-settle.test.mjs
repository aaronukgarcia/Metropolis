// queue-depth-settle.test.mjs — FEAT-1972079938: proves protocolClient.ts's
// Queue Depth HUD instrumentation counts every sendCommand() ask correctly,
// with NO leak, across all five settle paths an independent destructive
// round required:
//   1. a successful "result" notification (accepted:true)
//   2. an engine-rejected "result" notification (accepted:false)
//   3. an ack-level JSON-RPC error
//   4. rejectAllPending (socket drop mid-flight, via client.close())
//   5. THE LEAK FIX: a synchronous socket.send() throw (the reason the
//      prior attempt was REJECTed — increment() ran before send(), so a
//      throw settled the caller's Promise while leaving the tracker's
//      count stranded above 0 forever)
//
// Drives the REAL ProtocolClient against a fake WireSocket (no real
// network — same double convention as protocol-client.test.mjs) and an
// ISOLATED QueueDepthTracker instance injected via the queueTracker/
// queueEngineKey options, so this file never touches the module-level
// singleton other suites (queue-depth.test.mjs, the HUD) might also be
// observing.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { ProtocolClient } from '../src/sim/protocolClient.ts';
import { QueueDepthTracker } from '../src/sim/queueDepth.ts';

const ENGINE_KEY = 'test-protocol';

/** Minimal, fully synchronous fake WireSocket — mirrors
 * protocol-client.test.mjs's FakeSocket exactly, with one addition:
 * `throwOnSend` lets a test make the NEXT send() call throw synchronously,
 * reproducing a real WebSocket's InvalidStateError on a CONNECTING/CLOSING/
 * CLOSED socket. */
class FakeSocket {
  constructor() {
    this.sent = [];
    this.closed = false;
    this.throwOnSend = false;
    this.listeners = { open: [], message: [], close: [], error: [] };
  }
  addEventListener(type, fn) {
    this.listeners[type].push(fn);
  }
  send(data) {
    if (this.throwOnSend) {
      this.throwOnSend = false;
      throw new DOMException('InvalidStateError: socket is not OPEN', 'InvalidStateError');
    }
    this.sent.push(data);
  }
  close() {
    this.closed = true;
    this.emit('close', {});
  }
  emit(type, ev) {
    for (const fn of this.listeners[type]) fn(ev);
  }
  serverSends(msg) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
}

function makeLiveClient(tracker) {
  let socket;
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion: 'v1.2.3',
    createSocket: (url) => {
      socket = new FakeSocket();
      socket.url = url;
      return socket;
    },
    queueTracker: tracker,
    queueEngineKey: ENGINE_KEY,
    newCorrelationId: (() => {
      let n = 0;
      return () => `corr-${++n}`;
    })(),
  });
  client.connect();
  socket.emit('open', {});
  socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3' } });
  return { client, get socket() { return socket; } };
}

describe('protocolClient.ts Queue Depth HUD instrumentation (FEAT-1972079938)', () => {
  test('path 1: depth returns to 0 after a successful "result" notification', async () => {
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    const promise = client.sendCommand('SetSpeed', { speed: 2 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 1, 'expected depth 1 immediately after dispatch');
    const req = JSON.parse(socket.sent.at(-1));
    socket.serverSends({ jsonrpc: '2.0', id: req.id, result: { queued: true } });
    socket.serverSends({
      jsonrpc: '2.0',
      method: 'result',
      params: { correlationId: req.params.correlationId, tick: 5, accepted: true },
    });
    await promise;
    assert.equal(tracker.depthOf(ENGINE_KEY), 0, 'depth must return to 0 after a successful result');
  });

  test('path 2: depth returns to 0 after an engine-rejected "result" notification (accepted:false)', async () => {
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    const promise = client.sendCommand('SetSpeed', { speed: 99 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 1);
    const req = JSON.parse(socket.sent.at(-1));
    socket.serverSends({ jsonrpc: '2.0', id: req.id, result: { queued: true } });
    socket.serverSends({
      jsonrpc: '2.0',
      method: 'result',
      params: {
        correlationId: req.params.correlationId,
        tick: 5,
        accepted: false,
        error: { code: 'MET-E099', display: 'invalid speed' },
      },
    });
    await assert.rejects(promise);
    assert.equal(tracker.depthOf(ENGINE_KEY), 0, 'depth must return to 0 after an engine rejection');
  });

  test('path 3: depth returns to 0 after an ack-level JSON-RPC error', async () => {
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    const promise = client.sendCommand('SetSpeed', { speed: 2 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 1);
    const req = JSON.parse(socket.sent.at(-1));
    socket.serverSends({
      jsonrpc: '2.0',
      id: req.id,
      error: { code: 'MET-P011', message: 'command decode failed' },
    });
    await assert.rejects(promise);
    assert.equal(tracker.depthOf(ENGINE_KEY), 0, 'depth must return to 0 after an ack-level error');
  });

  test('path 4: depth returns to 0 for every pending ask when the socket drops mid-flight (rejectAllPending)', async () => {
    const tracker = new QueueDepthTracker();
    const { client } = makeLiveClient(tracker);
    const p1 = client.sendCommand('SetSpeed', { speed: 2 });
    const p2 = client.sendCommand('SetSpeed', { speed: 3 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 2, 'expected depth 2 with two in-flight asks');
    client.close(); // triggers rejectAllPending
    await assert.rejects(p1);
    await assert.rejects(p2);
    assert.equal(tracker.depthOf(ENGINE_KEY), 0, 'depth must return to 0 for every stranded ask on socket drop');
  });

  test('path 5 (THE LEAK FIX): depth returns to 0 when socket.send() throws synchronously', async () => {
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    socket.throwOnSend = true;
    const promise = client.sendCommand('SetSpeed', { speed: 2 });
    await assert.rejects(promise, (err) => {
      assert.equal(err.code, 'MET-P013');
      return true;
    });
    assert.equal(
      tracker.depthOf(ENGINE_KEY),
      0,
      'a send() throw must never leave depth stranded above 0 — the ask never reached the wire, ' +
        'so it must never have been counted as in-flight',
    );
    // A subsequent, successful ask still works and still settles cleanly —
    // proves the throw path didn't corrupt the pending-map bookkeeping for
    // asks that come after it.
    const p2 = client.sendCommand('SetSpeed', { speed: 4 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 1);
    const req2 = JSON.parse(socket.sent.at(-1));
    socket.serverSends({ jsonrpc: '2.0', id: req2.id, result: { queued: true } });
    socket.serverSends({
      jsonrpc: '2.0',
      method: 'result',
      params: { correlationId: req2.params.correlationId, tick: 1, accepted: true },
    });
    await p2;
    assert.equal(tracker.depthOf(ENGINE_KEY), 0);
  });

  test('MUTATION-PROOF: removing the send-throw decrement/cleanup would strand depth above 0', async () => {
    // This test documents and re-proves the exact mutation an independent
    // destructive round found in the prior attempt: incrementing BEFORE
    // socket.send() with no try/catch around the throw. Manually reverting
    // sendCommand() to that shape (increment before the `try`, no catch
    // block deleting the pending entries) and re-running this file turns
    // path 5 above RED — `tracker.depthOf(ENGINE_KEY)` reports 1, not 0,
    // because the increment already ran but the throw settles the Promise
    // via the surrounding Promise executor's synchronous-throw-rejects
    // behaviour without ever reaching a decrement call. This test is the
    // permanent regression guard for that class of leak: it asserts the
    // SAME invariant (depth 0 after a send-throw) that path 5 asserts, and
    // additionally checks the tracker's high-water mark stays sane —
    // proving no phantom increment survives the ask that never sent.
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    socket.throwOnSend = true;
    const promise = client.sendCommand('SetSpeed', { speed: 2 });
    await assert.rejects(promise);
    assert.equal(tracker.depthOf(ENGINE_KEY), 0);
    assert.equal(
      tracker.highWaterMarkOf(ENGINE_KEY),
      0,
      'a send-throw ask must never have registered an increment at all, so HWM must stay 0',
    );
  });

  test('isolated tracker option: instrumenting one client never touches the module-level singleton', async () => {
    // Imported lazily so a failure importing the singleton module doesn't
    // mask the isolated-tracker assertions above.
    const { queueDepthTracker, PROTOCOL_ENGINE_KEY } = await import('../src/sim/queueDepth.ts');
    const before = queueDepthTracker.depthOf(PROTOCOL_ENGINE_KEY);
    const tracker = new QueueDepthTracker();
    const { client, socket } = makeLiveClient(tracker);
    client.sendCommand('SetSpeed', { speed: 2 });
    assert.equal(tracker.depthOf(ENGINE_KEY), 1);
    assert.equal(
      queueDepthTracker.depthOf(PROTOCOL_ENGINE_KEY),
      before,
      'the module-level singleton must be untouched when a client is given its own queueTracker',
    );
  });
});
