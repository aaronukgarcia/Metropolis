// bug-1097-build-warning.test.mjs — BUG-1097 / Q100145 (AC-6): the client
// records the engine/client build pair in the debug JSON and the GR#27
// pre-wipe capture; a build difference is a warning, never a block.

import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { ProtocolClient } from '../src/sim/protocolClient.ts';
import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';
import {
  captureBeforeWipe,
  readPreWipeArchive,
} from '../src/sim/captureBeforeWipe.ts';
import {
  setLiveEngineBuild,
  getLiveEngineBuild,
  resetLiveEngineBuild,
} from '../src/sim/liveEngineBuild.ts';
import { initialState } from '../src/sim/engine.ts';

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

function connect(clientVersion, serverVersion) {
  let socket;
  const events = { states: [], refusals: [] };
  const client = new ProtocolClient({
    url: 'ws://fake/ws',
    clientVersion,
    createSocket: () => (socket = new FakeSocket()),
    onStateChange: (s) => events.states.push(s),
    onRefused: (code, msg) => events.refusals.push({ code, msg }),
  });
  client.connect();
  socket.emit('open', {});
  socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion } });
  return { client, socket, events };
}

function ui() {
  return {
    appVersion: 'v9.9.9-test',
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: null, showWater: true },
    errors: [],
  };
}

function memStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
  };
}

beforeEach(() => resetLiveEngineBuild());

describe('ProtocolClient: build difference is a warning, never a block', () => {
  test('differing build string goes live, exposes serverVersion and buildMismatch', () => {
    const { client, events } = connect('v0.3.0-609-g3b657a70', 'v0.3.0-609-g3b657a7');
    assert.equal(client.getState(), 'live', 'a differing build must not refuse');
    assert.equal(events.refusals.length, 0);
    assert.equal(client.getServerVersion(), 'v0.3.0-609-g3b657a7');
    assert.equal(client.isBuildMismatch(), true);
  });

  test('matching build (modulo -dirty) is not flagged', () => {
    const { client } = connect('v1.2.3', 'v1.2.3-dirty');
    assert.equal(client.getState(), 'live');
    assert.equal(client.isBuildMismatch(), false);
  });

  test('before any handshake: serverVersion null, mismatch false', () => {
    const client = new ProtocolClient({ url: 'ws://x', clientVersion: 'a', createSocket: () => new FakeSocket() });
    assert.equal(client.getServerVersion(), null);
    assert.equal(client.isBuildMismatch(), false);
  });
});

describe('AC-6: debug JSON carries the build pair', () => {
  test('mismatch: engineBuild = server string, clientBuild = client string, flag true', () => {
    setLiveEngineBuild({ engineBuild: 'ENGINE-7', clientBuild: 'CLIENT-8', mismatch: true });
    const meta = JSON.parse(debugJsonText(buildDebugJson(initialState(), ui()))).meta;
    assert.equal(meta.engineBuild, 'ENGINE-7');
    assert.equal(meta.clientBuild, 'CLIENT-8');
    assert.equal(meta.engineBuildMismatch, true);
  });

  test('match: flag is false (present)', () => {
    setLiveEngineBuild({ engineBuild: 'SAME', clientBuild: 'SAME', mismatch: false });
    const meta = JSON.parse(debugJsonText(buildDebugJson(initialState(), ui()))).meta;
    assert.equal(meta.engineBuildMismatch, false);
    assert.equal(meta.engineBuild, 'SAME');
  });

  test('no live-engine session: all three keys ABSENT (not null, not false)', () => {
    const meta = JSON.parse(debugJsonText(buildDebugJson(initialState(), ui()))).meta;
    for (const k of ['engineBuild', 'clientBuild', 'engineBuildMismatch']) {
      assert.equal(Object.prototype.hasOwnProperty.call(meta, k), false, `${k} must be absent`);
    }
  });

  test('driven by a real ProtocolClient handshake pair through the tracker', () => {
    const { client } = connect('CLIENT-A', 'ENGINE-B');
    setLiveEngineBuild({ engineBuild: client.getServerVersion(), clientBuild: 'CLIENT-A', mismatch: client.isBuildMismatch() });
    const b = getLiveEngineBuild();
    assert.deepEqual(b, { engineBuild: 'ENGINE-B', clientBuild: 'CLIENT-A', mismatch: true });
  });

  test('GR#27 pre-wipe capture carries the same three values', () => {
    setLiveEngineBuild({ engineBuild: 'ENGINE-7', clientBuild: 'CLIENT-8', mismatch: true });
    const storage = memStorage();
    captureBeforeWipe(initialState(), 'v9.9.9-test', storage, 1_701_234_567_890);
    const entries = readPreWipeArchive(storage);
    assert.equal(entries.length, 1);
    const meta = entries[0].debug.meta;
    assert.equal(meta.engineBuild, 'ENGINE-7');
    assert.equal(meta.clientBuild, 'CLIENT-8');
    assert.equal(meta.engineBuildMismatch, true);
  });

  test('pre-wipe capture with no session: keys absent', () => {
    const storage = memStorage();
    captureBeforeWipe(initialState(), 'v9.9.9-test', storage, 1_701_234_567_890);
    const meta = readPreWipeArchive(storage)[0].debug.meta;
    assert.equal('engineBuildMismatch' in meta, false);
    assert.equal('engineBuild' in meta, false);
  });
});
