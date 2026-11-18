// protocol-lifecycle-badge.test.tsx -- FEAT-9995385597 section G (AC-B2) under
// a REAL React.StrictMode mount (the mount double-invokes the effect: the first
// client is closed, a second takes over). Proves the waking label, the asleep
// click-to-wake and that no timer the client armed is left outstanding after
// unmount (AC-R12). The socket is a fake WebSocket installed on the global;
// timers are observed by wrapping the global setTimeout/clearTimeout.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).localStorage = window.localStorage;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame?.bind(window) ?? ((cb: any) => setTimeout(cb, 0));
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame?.bind(window) ?? ((id: any) => clearTimeout(id));
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  url: string;
  sent: any[] = [];
  closed = false;
  listeners: Record<string, ((ev: any) => void)[]> = { open: [], message: [], close: [], error: [] };
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  addEventListener(type: string, fn: (ev: any) => void) {
    this.listeners[type].push(fn);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close(code?: number, reason?: string) {
    this.closed = true;
    this.emit('close', { code, reason });
  }
  emit(type: string, ev: any) {
    for (const fn of this.listeners[type]) fn(ev);
  }
  serverSends(msg: unknown) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
}

/** Wraps the global timers and tracks the set of timers armed through the
 * wrapper (cleared or fired ones are removed). */
function trackTimers() {
  const realSet = globalThis.setTimeout;
  const realClear = globalThis.clearTimeout;
  const outstanding = new Set<unknown>();
  (globalThis as any).setTimeout = (fn: (...a: any[]) => void, ms?: number, ...rest: any[]) => {
    const h: any = realSet(
      (...a: any[]) => {
        outstanding.delete(h);
        fn(...a);
      },
      ms,
      ...rest,
    );
    outstanding.add(h);
    return h;
  };
  (globalThis as any).clearTimeout = (h: any) => {
    outstanding.delete(h);
    realClear(h);
  };
  return {
    outstanding,
    restore: () => {
      (globalThis as any).setTimeout = realSet;
      (globalThis as any).clearTimeout = realClear;
    },
  };
}

async function mountStrict() {
  const dom = installJsdom();
  const originalWebSocket = (globalThis as any).WebSocket;
  const { LIVE_ENGINE_FLAG_KEY } = await import('../src/sim/liveEngineFlag.ts');
  dom.window.localStorage.setItem(LIVE_ENGINE_FLAG_KEY, '1');
  FakeWebSocket.instances = [];
  (globalThis as any).WebSocket = FakeWebSocket;
  (dom.window as any).WebSocket = FakeWebSocket;
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { LiveEngineBadge } = await import('../src/components/LiveEngineBadge.tsx');
  const { resetLiveEngineBuild } = await import('../src/sim/liveEngineBuild.ts');
  resetLiveEngineBuild();

  const timers = trackTimers();
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  await act(async () => {
    root.render(React.createElement(React.StrictMode, null, React.createElement(LiveEngineBadge)));
  });
  const open = () => FakeWebSocket.instances.filter((s) => !s.closed);
  const cleanup = async () => {
    timers.restore();
    (globalThis as any).WebSocket = originalWebSocket;
    dom.window.close();
  };
  return { container, act, dom, timers, open, root, cleanup };
}

const accept = (extra: Record<string, unknown> = {}) => ({
  jsonrpc: '2.0',
  id: 1,
  result: { accepted: true, serverVersion: 'v0.0.0-test', capabilities: [], ...extra },
});

test('StrictMode: the double-invoked mount leaves ONE open socket (the first client was closed)', async () => {
  const { open, act, root, cleanup } = await mountStrict();
  try {
    assert.equal(FakeWebSocket.instances.length, 2, 'StrictMode runs the effect twice');
    assert.equal(open().length, 1, 'exactly one connection is live; the first was closed by the effect cleanup');
    assert.equal(FakeWebSocket.instances[0].closed, true);
  } finally {
    await act(async () => root.unmount());
    await cleanup();
  }
});

test('AC-B2: waking shows "Waking city N%" with the phase in the title; ready then live', async () => {
  const { container, act, open, root, cleanup } = await mountStrict();
  try {
    const socket = open()[0];
    await act(async () => socket.emit('open', {}));
    await act(async () => socket.serverSends(accept({ waking: true })));
    const badge = () => container.querySelector('.live-engine-badge') as HTMLElement;
    assert.equal(badge().getAttribute('data-conn-state'), 'waking');
    await act(async () => socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'replaying', pct: 62 } }));
    assert.match(badge().textContent ?? '', /Waking city 62%/);
    assert.match(badge().getAttribute('title') ?? '', /replaying/);
    assert.equal(badge().getAttribute('role'), null, 'waking is not clickable');
    await act(async () => socket.serverSends({ jsonrpc: '2.0', method: 'server.waking', params: { phase: 'ready', pct: 100 } }));
    assert.equal(badge().getAttribute('data-conn-state'), 'live');
  } finally {
    await act(async () => root.unmount());
    await cleanup();
  }
});

test('AC-B2/AC-H3: asleep shows "City asleep - click to wake", is a button, and a click (only) reconnects', async () => {
  const { container, act, dom, open, root, cleanup } = await mountStrict();
  try {
    const socket = open()[0];
    await act(async () => socket.emit('open', {}));
    await act(async () => socket.serverSends(accept()));
    await act(async () => socket.serverSends({ jsonrpc: '2.0', method: 'server.hibernating', params: { reason: 'idle' } }));
    const badge = () => container.querySelector('.live-engine-badge') as HTMLElement;
    assert.equal(badge().getAttribute('data-conn-state'), 'asleep');
    assert.match(badge().textContent ?? '', /City asleep - click to wake/);
    assert.equal(badge().getAttribute('role'), 'button');
    const before = FakeWebSocket.instances.length;
    await act(async () => socket.emit('close', { code: 1000, reason: 'hibernate' }));
    assert.equal(FakeWebSocket.instances.length, before, 'no automatic reconnect while asleep');
    await act(async () => {
      badge().dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    });
    assert.equal(FakeWebSocket.instances.length, before + 1, 'the click opened exactly one new connection');
    assert.notEqual(badge().getAttribute('data-conn-state'), 'asleep');
  } finally {
    await act(async () => root.unmount());
    await cleanup();
  }
});

test('AC-R12: after unmount no timer armed by the client is outstanding and no socket is left open', async () => {
  const { act, timers, open, root, cleanup } = await mountStrict();
  try {
    const socket = open()[0];
    await act(async () => socket.emit('open', {}));
    // A lost connection arms a reconnect timer (the handshake timer is cleared).
    socket.closed = true; // the server end is gone
    await act(async () => socket.emit('close', { code: 1006 }));
    assert.ok(timers.outstanding.size > 0, 'precondition: a reconnect timer is armed');
    await act(async () => root.unmount());
    assert.equal(timers.outstanding.size, 0, 'no timer survives unmount');
    assert.equal(open().length, 0, 'no socket survives unmount');
    const sockets = FakeWebSocket.instances.length;
    await new Promise((r) => setImmediate(r));
    assert.equal(FakeWebSocket.instances.length, sockets);
  } finally {
    await cleanup();
  }
});

test('r4 ruling 2: a refused state is a button, and a click starts exactly one new connection attempt (StrictMode mount)', async () => {
  const { container, act, dom, open, root, cleanup } = await mountStrict();
  try {
    const socket = open()[0];
    await act(async () => socket.emit('open', {}));
    await act(async () => socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: false } }));
    const badge = () => container.querySelector('.live-engine-badge') as HTMLElement;
    assert.equal(badge().getAttribute('data-conn-state'), 'refused');
    assert.equal(badge().getAttribute('role'), 'button', 'a refused state is a button');
    assert.match(badge().textContent ?? '', /click to retry/);
    const before = FakeWebSocket.instances.length;
    await new Promise((r) => setImmediate(r));
    assert.equal(FakeWebSocket.instances.length, before, 'no automatic reconnect from a refused state');
    await act(async () => {
      badge().dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    });
    assert.equal(FakeWebSocket.instances.length, before + 1, 'the click started exactly one new connection attempt');
    assert.notEqual(badge().getAttribute('data-conn-state'), 'refused');
  } finally {
    await act(async () => root.unmount());
    await cleanup();
  }
});
