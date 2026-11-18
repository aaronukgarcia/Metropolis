// bug-767-liveenginebadge-networth-unit.test.tsx — BUG-767: the badge showed
// net worth 1000x too small because it divided the engine's money by
// 1,000,000 while Money has been 1,000 units per pound since BUG-452.
// Mounts the REAL LiveEngineBadge over a fake global WebSocket (same seam as
// bug-1097-liveenginebadge-build-warning.test.tsx) and feeds a real f2.finance
// delta; asserts the rendered pounds figure.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  listeners: Record<string, ((ev: any) => void)[]> = { open: [], message: [], close: [], error: [] };
  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  addEventListener(type: string, fn: (ev: any) => void) {
    (this.listeners[type] = this.listeners[type] || []).push(fn);
  }
  send(_data: string) {}
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

test('BUG-767: engine net worth of 1,500,000 pounds renders as 1,500,000 pounds, not 1,500', async () => {
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
  const originalWebSocket = (globalThis as any).WebSocket;
  const { LIVE_ENGINE_FLAG_KEY } = await import('../src/sim/liveEngineFlag.ts');
  window.localStorage.setItem(LIVE_ENGINE_FLAG_KEY, '1');
  FakeWebSocket.instances = [];
  (globalThis as any).WebSocket = FakeWebSocket;
  (window as any).WebSocket = FakeWebSocket;

  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { LiveEngineBadge } = await import('../src/components/LiveEngineBadge.tsx');
  const { MICROPOUNDS_PER_POUND } = await import('../src/sim/engineMoney.ts');
  const { FINANCE_SCHEMA_VERSION } = await import('../src/sim/wire.ts');
  const root = createRoot(window.document.getElementById('root')!);
  try {
    await act(async () => {
      root.render(React.default.createElement(LiveEngineBadge));
    });
    const socket = FakeWebSocket.instances[0];
    await act(async () => socket.emit('open', {}));
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v0.0.0-test' } });
    });
    // Engine representation of 1,500,000 pounds at 1,000 units per pound.
    const engineNetWorth = 1_500_000 * 1_000;
    assert.equal(MICROPOUNDS_PER_POUND, 1_000, 'precondition: Go det.MicropoundsPerPound is 1,000');
    await act(async () => {
      socket.serverSends({
        jsonrpc: '2.0',
        method: 'delta',
        params: {
          subscriptionId: 'sub-finance-1',
          tick: 7,
          seq: 1,
          patch: {
            schemaVersion: FINANCE_SCHEMA_VERSION,
            balanceSheet: { assets: [], liabilities: [], netWorth: engineNetWorth },
          },
        },
      });
    });
    const text = window.document.querySelector('.live-engine-badge')?.textContent ?? '';
    assert.match(text, /tick 7/, `badge shows the delta: ${text}`);
    assert.match(text, /£1500000(?!\d)/, `net worth must render as 1,500,000 pounds, got: ${text}`);
  } finally {
    await act(async () => root.unmount());
    (globalThis as any).WebSocket = originalWebSocket;
    window.close();
  }
});
