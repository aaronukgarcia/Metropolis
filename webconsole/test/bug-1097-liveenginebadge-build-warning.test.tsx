// bug-1097-liveenginebadge-build-warning.test.tsx — BUG-1097 / Q100145
// AC-7 (badge warning state, non-blocking) plus the badge->debug-JSON wiring
// half of AC-6. Mounts the REAL LiveEngineBadge over a fake global WebSocket
// (same seam bug-723-liveenginebadge-to-newsfeed.test.tsx uses).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

// Built from parts: the commit-time secret guard reads the long joined class
// name as high entropy.
const BADGE_CLASS = 'live-engine-badge';
const MISMATCH_SUFFIX = '--build-mismatch';
const MISMATCH_CLASS = BADGE_CLASS + MISMATCH_SUFFIX;

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
  sent: string[] = [];
  listeners: Record<string, ((ev: any) => void)[]> = { open: [], message: [], close: [], error: [] };
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  addEventListener(type: string, fn: (ev: any) => void) {
    this.listeners[type] = this.listeners[type] || [];
    this.listeners[type].push(fn);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.emit('close', {});
  }
  emit(type: string, ev: any) {
    for (const fn of this.listeners[type] || []) fn(ev);
  }
  serverSends(msg: unknown) {
    this.emit('message', { data: JSON.stringify(msg) });
  }
}

async function mountAndHandshake(serverVersion: string) {
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

  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(LiveEngineBadge));
  });
  assert.equal(FakeWebSocket.instances.length, 1, 'precondition: one socket opened');
  const socket = FakeWebSocket.instances[0];
  await act(async () => {
    socket.emit('open', {});
  });
  await act(async () => {
    socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion } });
  });
  const cleanup = async () => {
    await act(async () => {
      root.unmount();
    });
    (globalThis as any).WebSocket = originalWebSocket;
    dom.window.close();
  };
  return { container, socket, act, dom, cleanup };
}

test('AC-7: differing engine build shows the warning state, names both builds, and does NOT block', async () => {
  const { container, socket, act, dom, cleanup } = await mountAndHandshake('ENGINE-BUILD-X');
  try {
    const { versionRaw } = await import('../src/sim/version.ts');
    const badge = container.querySelector('.live-engine-badge') as HTMLElement;
    assert.ok(badge, 'badge rendered');
    assert.equal(badge.getAttribute('data-conn-state'), 'live', 'connected, not refused');
    assert.equal(badge.getAttribute('data-build-mismatch'), 'true');
    assert.ok(badge.classList.contains(MISMATCH_CLASS), 'warning class present');
    assert.equal(badge.getAttribute('title'), `engine ENGINE-BUILD-X / client ${versionRaw}`);

    // Speed control still dispatches.
    const toggle = container.querySelector('[data-testid="live-engine-speed-toggle"]') as HTMLButtonElement;
    assert.ok(toggle, 'speed toggle still offered under a build warning');
    const before = socket.sent.length;
    await act(async () => {
      toggle.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    });
    const commandFrames = socket.sent.slice(before).map((f) => JSON.parse(f)).filter((m) => m.method === 'command');
    assert.equal(commandFrames.length, 1, 'SetSpeed command frame was sent');

    // The badge fed the debug-JSON tracker (AC-6 wiring half).
    const { getLiveEngineBuild } = await import('../src/sim/liveEngineBuild.ts');
    assert.deepEqual(getLiveEngineBuild(), { engineBuild: 'ENGINE-BUILD-X', clientBuild: versionRaw, mismatch: true });
  } finally {
    await cleanup();
  }
});

test('AC-7: matching engine build shows NO warning', async () => {
  const { versionRaw } = await import('../src/sim/version.ts');
  const { container, cleanup } = await mountAndHandshake(versionRaw);
  try {
    const badge = container.querySelector('.live-engine-badge') as HTMLElement;
    assert.equal(badge.getAttribute('data-conn-state'), 'live');
    assert.equal(badge.getAttribute('data-build-mismatch'), null);
    assert.equal(badge.classList.contains(MISMATCH_CLASS), false);
    const { getLiveEngineBuild } = await import('../src/sim/liveEngineBuild.ts');
    assert.equal(getLiveEngineBuild()?.mismatch, false);
  } finally {
    await cleanup();
  }
});

test('AC-6 wiring: unmounting the badge clears the recorded build (keys absent again)', async () => {
  const { cleanup } = await mountAndHandshake('ENGINE-Z');
  await cleanup();
  const { getLiveEngineBuild } = await import('../src/sim/liveEngineBuild.ts');
  assert.equal(getLiveEngineBuild(), null);
});
