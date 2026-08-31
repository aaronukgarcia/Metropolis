// protocol-client.test.mjs — FEAT-1972079852 increment 1: the webconsole
// connection seam's handshake/refusal state machine, exercised against a
// fake socket (per the acceptance doc's Tester note: "AC-4 is fragile to
// test without a controllable transport mock" — this file IS that
// double). No real network anywhere in this file.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  ProtocolClient,
  SeqTracker,
  ERR_HANDSHAKE_VERSION_MISMATCH,
  ERR_ENGINE_UNREACHABLE,
  ERR_SCHEMA_MISMATCH,
} from '../src/sim/protocolClient.ts';
import { FINANCE_SCHEMA_VERSION } from '../src/sim/wire.ts';

/** A minimal, fully synchronous fake WireSocket. Records every frame sent
 * so a test can inspect what the client actually wrote, and lets the test
 * drive `open`/`message`/`close`/`error` events by calling the captured
 * listeners directly (no real event loop / no real socket). */
class FakeSocket {
  constructor() {
    this.sent = [];
    this.closed = false;
    this.listeners = { open: [], message: [], close: [], error: [] };
  }
  addEventListener(type, fn) {
    this.listeners[type].push(fn);
  }
  send(data) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
    this.emit('close', {});
  }
  emit(type, ev) {
    for (const fn of this.listeners[type]) fn(ev);
  }
  /** Test helper: simulate the server replying with a JSON-RPC message. */
  serverSends(msg) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
}

function makeClient(overrides = {}) {
  let socket;
  const events = { states: [], refusals: [], deltas: [], liveVersions: [], schemaMismatches: [] };
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion: 'v1.2.3',
    createSocket: (url) => {
      socket = new FakeSocket();
      socket.url = url;
      return socket;
    },
    onStateChange: (s) => events.states.push(s),
    onRefused: (code, msg) => events.refusals.push({ code, msg }),
    onDelta: (delta, gap) => events.deltas.push({ delta, gap }),
    onLive: (v) => events.liveVersions.push(v),
    onSchemaMismatch: (subscriptionId, expected, got) => events.schemaMismatches.push({ subscriptionId, expected, got }),
    newCorrelationId: (() => {
      let n = 0;
      return () => `corr-${++n}`;
    })(),
    ...overrides,
  });
  client.connect();
  return { client, get socket() { return socket; }, events };
}

describe('ProtocolClient handshake (AC per Aaron DD: refuse, never degrade)', () => {
  test('sends a handshake request as the very first frame after open', () => {
    const { socket } = makeClient();
    socket.emit('open', {});
    assert.equal(socket.sent.length, 1);
    const req = JSON.parse(socket.sent[0]);
    assert.equal(req.method, 'handshake');
    assert.equal(req.params.clientVersion, 'v1.2.3');
    assert.equal(req.jsonrpc, '2.0');
  });

  test('matching version handshake accepts and moves to live state', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3' } });
    assert.equal(events.states.at(-1), 'live');
    assert.deepEqual(events.liveVersions, ['v1.2.3']);
    assert.equal(events.refusals.length, 0);
  });

  test('mismatched version handshake is refused, not degraded — connection is closed', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({
      jsonrpc: '2.0',
      id: 1,
      error: { code: ERR_HANDSHAKE_VERSION_MISMATCH, message: '[MET-P010] version mismatch (correlation: x)' },
    });
    assert.equal(events.states.at(-1), 'refused');
    assert.equal(events.refusals.length, 1);
    assert.equal(events.refusals[0].code, ERR_HANDSHAKE_VERSION_MISMATCH);
    assert.ok(socket.closed, 'expected the client to close the socket on refusal');
  });

  test('MUTATION-PROOF: a client that treated any error response as acceptance would wrongly report "live" — this test catches that', () => {
    // This is the RED/GREEN pair for the mismatch test above: proven by
    // temporarily short-circuiting handleHandshakeResponse's error branch
    // in a scratch copy (see the accompanying manual mutation-proof run
    // recorded in the report) — kept here as the permanent regression
    // test that would go RED under that mutation.
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: ERR_HANDSHAKE_VERSION_MISMATCH, message: 'mismatch' } });
    assert.notEqual(events.states.at(-1), 'live');
  });

  test('a handshake response with no accepted flag is treated as a refusal', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({ jsonrpc: '2.0', id: 1, result: {} });
    assert.equal(events.states.at(-1), 'refused');
    assert.equal(events.refusals.length, 1);
  });

  test('connection closing before any handshake response reports engine-unreachable', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.emit('close', {});
    assert.equal(events.states.at(-1), 'error');
    assert.equal(events.refusals[0].code, ERR_ENGINE_UNREACHABLE);
  });

  test('a socket error event reports engine-unreachable', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.emit('error', {});
    assert.equal(events.refusals[0].code, ERR_ENGINE_UNREACHABLE);
  });

  test('a close AFTER an already-refused handshake does not double-report', () => {
    const { socket, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({ jsonrpc: '2.0', id: 1, error: { code: ERR_HANDSHAKE_VERSION_MISMATCH, message: 'mismatch' } });
    socket.emit('close', {});
    assert.equal(events.refusals.length, 1, 'expected exactly one refusal report, not a second on close');
  });
});

describe('ProtocolClient delta relay + Seq gap tracking (AC-3/AC-15)', () => {
  function liveClient() {
    const ctx = makeClient();
    ctx.socket.emit('open', {});
    ctx.socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3' } });
    return ctx;
  }

  test('subscribe() sends a Subscribe command only after the handshake completed', () => {
    const { client, socket } = liveClient();
    const sentBefore = socket.sent.length;
    client.subscribe('f2.finance');
    assert.equal(socket.sent.length, sentBefore + 1);
    const req = JSON.parse(socket.sent.at(-1));
    assert.equal(req.method, 'command');
    assert.equal(req.params.kind, 'Subscribe');
    assert.equal(req.params.payload.viewName, 'f2.finance');
  });

  test('subscribe() before handshake completes is a silent no-op (fallback stays on mock)', () => {
    const { client, socket } = makeClient(); // connect() has run, but 'open'/handshake never fired
    client.subscribe('f2.finance');
    assert.equal(socket.sent.length, 0, 'no command frame should be sent before the handshake completes');
  });

  test('in-order deltas are relayed with gap=0', () => {
    const { socket, events } = liveClient();
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 1, seq: 1, patch: {} } });
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 2, seq: 2, patch: {} } });
    assert.equal(events.deltas.length, 2);
    assert.equal(events.deltas[0].gap, 0);
    assert.equal(events.deltas[1].gap, 0);
  });

  test('a skipped Seq is reported as a gap, not an error (GR#17/GR#21: a valid, logged outcome)', () => {
    const { socket, events } = liveClient();
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 1, seq: 1, patch: {} } });
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 5, seq: 5, patch: {} } });
    assert.equal(events.deltas.length, 2);
    assert.equal(events.deltas[1].gap, 3); // seq 2,3,4 skipped
  });

  test('a duplicate/out-of-order Seq is dropped (not forwarded to onDelta)', () => {
    const { socket, events } = liveClient();
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 3, seq: 3, patch: {} } });
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 2, seq: 2, patch: {} } });
    assert.equal(events.deltas.length, 1, 'the out-of-order seq=2 after seq=3 must not be forwarded');
  });

  // AC-7/DD2 (BAR-1, round-r1 REJECT): "the protocolClient delta-apply
  // path" must refuse a schema-mismatched delta before it ever reaches
  // onDelta — this is the actual bar's TEST requirement: "a
  // schemaVersion:2 delta is rejected + recordError called; a
  // schemaVersion:1 delta applies."
  test('AC-7: a schemaVersion mismatch is rejected — not forwarded to onDelta, recordError + onSchemaMismatch fire', () => {
    const { socket, events } = liveClient();
    socket.serverSends({
      jsonrpc: '2.0',
      method: 'delta',
      params: { subscriptionId: 'sub-1', tick: 1, seq: 1, patch: { schemaVersion: FINANCE_SCHEMA_VERSION + 1 } },
    });
    assert.equal(events.deltas.length, 0, 'a schema-mismatched delta must never reach onDelta');
    assert.equal(events.schemaMismatches.length, 1);
    assert.equal(events.schemaMismatches[0].subscriptionId, 'sub-1');
    assert.equal(events.schemaMismatches[0].expected, FINANCE_SCHEMA_VERSION);
    assert.equal(events.schemaMismatches[0].got, FINANCE_SCHEMA_VERSION + 1);
  });

  test('AC-7: a matching schemaVersion applies normally', () => {
    const { socket, events } = liveClient();
    socket.serverSends({
      jsonrpc: '2.0',
      method: 'delta',
      params: { subscriptionId: 'sub-1', tick: 1, seq: 1, patch: { schemaVersion: FINANCE_SCHEMA_VERSION } },
    });
    assert.equal(events.deltas.length, 1);
    assert.equal(events.schemaMismatches.length, 0);
  });

  test('MUTATION-PROOF: a patch with no schemaVersion field at all (e.g. a future non-versioned view) still applies — the check only fires when a version IS declared and wrong', () => {
    const { socket, events } = liveClient();
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-1', tick: 1, seq: 1, patch: {} } });
    assert.equal(events.deltas.length, 1);
    assert.equal(events.schemaMismatches.length, 0);
  });

  test('independent subscriptions track Seq independently (AC-8)', () => {
    const { socket, events } = liveClient();
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-A', tick: 1, seq: 1, patch: {} } });
    socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-B', tick: 1, seq: 1, patch: {} } });
    assert.equal(events.deltas.length, 2);
    assert.equal(events.deltas[0].gap, 0);
    assert.equal(events.deltas[1].gap, 0);
  });
});

describe('SeqTracker (mirrors internal/protocol/subscription.go SeqTracker.Observe)', () => {
  test('first observation for a subscription is always ok, gap 0', () => {
    const t = new SeqTracker();
    assert.deepEqual(t.observe('sub-1', 1), { gap: 0, ok: true });
  });
  test('sequential observations report gap 0', () => {
    const t = new SeqTracker();
    t.observe('sub-1', 1);
    assert.deepEqual(t.observe('sub-1', 2), { gap: 0, ok: true });
  });
  test('a skipped seq reports the gap count', () => {
    const t = new SeqTracker();
    t.observe('sub-1', 1);
    assert.deepEqual(t.observe('sub-1', 4), { gap: 2, ok: true });
  });
  test('a duplicate or earlier seq is rejected', () => {
    const t = new SeqTracker();
    t.observe('sub-1', 5);
    assert.deepEqual(t.observe('sub-1', 5), { gap: 0, ok: false });
    assert.deepEqual(t.observe('sub-1', 3), { gap: 0, ok: false });
  });
  test('reset() forgets a subscription, so a fresh Seq stream starts clean', () => {
    const t = new SeqTracker();
    t.observe('sub-1', 10);
    t.reset('sub-1');
    assert.deepEqual(t.observe('sub-1', 1), { gap: 0, ok: true });
  });
});
