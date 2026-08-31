// protocol-client-negotiation.test.mjs — FEAT-1972079936 Phase 0
// increment 1: the connect-time negotiation handshake shape (AC-1/AC-2),
// exercised against a fake socket, mirroring
// internal/protocol/wsserver/handshake_negotiation_test.go's Go-side
// coverage of the same shape. No real network anywhere in this file.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { ProtocolClient } from '../src/sim/protocolClient.ts';
import { CURRENT_WIRE_VERSION, parseWireVersion, intersectCapabilities } from '../src/sim/wire.ts';

/** Mirrors protocol-client.test.mjs's own FakeSocket exactly — kept as an
 * independent copy in this file rather than a shared import, consistent
 * with wire.ts's own "each side/each test file keeps its own copy"
 * convention for wire-shape fixtures. */
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
  serverSends(msg) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
}

function makeClient(overrides = {}) {
  let socket;
  const events = { liveArgs: [] };
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion: 'v1.2.3',
    createSocket: (url) => {
      socket = new FakeSocket();
      socket.url = url;
      return socket;
    },
    onLive: (serverVersion, negotiatedVersion, capabilities) => {
      events.liveArgs.push({ serverVersion, negotiatedVersion, capabilities });
    },
    ...overrides,
  });
  client.connect();
  return { client, get socket() { return socket; }, events };
}

describe('ProtocolClient handshake request shape (FEAT-1972079936 AC-1/AC-2)', () => {
  test('the handshake request declares clientMinVersion/clientMaxVersion == CURRENT_WIRE_VERSION and an empty capabilities array', () => {
    const { socket } = makeClient();
    socket.emit('open', {});
    const req = JSON.parse(socket.sent[0]);
    assert.deepEqual(req.params.clientMinVersion, CURRENT_WIRE_VERSION);
    assert.deepEqual(req.params.clientMaxVersion, CURRENT_WIRE_VERSION);
    assert.deepEqual(req.params.capabilities, []);
    // Diagnostic build string field is untouched by this increment.
    assert.equal(req.params.clientVersion, 'v1.2.3');
  });
});

describe('ProtocolClient handshake response consumption (FEAT-1972079936 AC-1/AC-2/AC-5)', () => {
  test('an accepted handshake with negotiatedVersion/capabilities exposes both via getters and onLive', () => {
    const { socket, client, events } = makeClient();
    socket.emit('open', {});
    socket.serverSends({
      jsonrpc: '2.0',
      id: 1,
      result: {
        accepted: true,
        serverVersion: 'v1.2.3',
        negotiatedVersion: { major: 1, minor: 0 },
        capabilities: ['foo.bar'],
      },
    });

    assert.deepEqual(client.getNegotiatedVersion(), { major: 1, minor: 0 });
    assert.deepEqual(client.getCapabilities(), ['foo.bar']);
    assert.equal(client.hasCapability('foo.bar'), true);
    assert.equal(client.hasCapability('nope'), false);
    assert.deepEqual(events.liveArgs, [
      { serverVersion: 'v1.2.3', negotiatedVersion: { major: 1, minor: 0 }, capabilities: ['foo.bar'] },
    ]);
  });

  test('an OLD-shape server response (no negotiatedVersion/capabilities fields) falls back to this client\'s own current version and an empty capability set — never throws, never blocks the live transition', () => {
    const { socket, client } = makeClient();
    socket.emit('open', {});
    socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v1.2.3' } });

    assert.equal(client.getState(), 'live');
    assert.deepEqual(client.getNegotiatedVersion(), CURRENT_WIRE_VERSION);
    assert.deepEqual(client.getCapabilities(), []);
  });

  test('before any handshake completes, getNegotiatedVersion is null and getCapabilities is empty', () => {
    const { client } = makeClient();
    assert.equal(client.getNegotiatedVersion(), null);
    assert.deepEqual(client.getCapabilities(), []);
    assert.equal(client.hasCapability('anything'), false);
  });
});

describe('wire.ts WireVersion helpers (mirrors internal/protocol/version.go)', () => {
  test('parseWireVersion parses a well-formed "major.minor" string', () => {
    assert.deepEqual(parseWireVersion('1.0'), { major: 1, minor: 0 });
    assert.deepEqual(parseWireVersion('2.13'), { major: 2, minor: 13 });
  });

  test('parseWireVersion rejects malformed input', () => {
    for (const bad of ['', '1', '1.0.0', 'a.b', '-1.0', '1.-1', 'v1.0', '1.0-dirty']) {
      assert.equal(parseWireVersion(bad), null, `expected null for ${JSON.stringify(bad)}`);
    }
  });

  test('CURRENT_WIRE_VERSION mirrors PROTOCOL_VERSION', () => {
    assert.deepEqual(CURRENT_WIRE_VERSION, { major: 1, minor: 0 });
  });

  // AC-5's exact three-way discrimination: intersection must differ from
  // union and from either side's raw set when both sides have something
  // unique.
  test('intersectCapabilities returns the intersection, not the union nor either side alone', () => {
    const got = intersectCapabilities(['A', 'B', 'C'], ['B', 'C', 'D']);
    assert.deepEqual(got, ['B', 'C']);
  });

  test('intersectCapabilities with an empty client set yields an empty (non-null) array', () => {
    assert.deepEqual(intersectCapabilities(['A', 'B', 'C'], []), []);
  });

  test('intersectCapabilities de-duplicates', () => {
    assert.deepEqual(intersectCapabilities(['A'], ['A', 'A', 'A']), ['A']);
  });
});
