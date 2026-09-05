// bug-723-liveenginebadge-to-newsfeed.test.tsx — BUG-723 re-round finding
// P2 (opus-reround-bug723): MUT1 — deleting
// financeStatusTracker.setPayrollShortfall(...) from LiveEngineBadge.tsx's
// onDelta handler leaves every other suite green, because
// bug-723-newsfeed-component.test.tsx writes the tracker DIRECTLY rather
// than going through the real badge. This file closes that hole: it
// mounts LiveEngineBadge (feature flag ON) and NewsFeed TOGETHER, feeds a
// real decoded "f2.finance" delta through the badge's own ProtocolClient
// (a fake WebSocket transport, the same style protocol-client.test.mjs
// uses for ProtocolClient itself), and asserts the feed shows the
// resulting entry — proving the wiring through the REAL component, not a
// direct tracker write.
//
// Also (re-round instruction): this test's own comments describe the
// surface as "visible when the live-engine flag is on" — isLiveEngineEnabled
// defaults OFF and this test does not change that default; it merely
// forces the flag on for ITS OWN jsdom localStorage, the same way a real
// dev would via the documented LIVE_ENGINE_FLAG_KEY.

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

/** A minimal fake WebSocket matching protocolClient.ts's WireSocket surface
 *  (addEventListener/send/close) — mirrors protocol-client.test.mjs's own
 *  FakeSocket, but installed as the GLOBAL `WebSocket` constructor, because
 *  LiveEngineBadge.tsx constructs `new ProtocolClient({...})` with no
 *  createSocket override (unlike the protocolClient unit tests, which
 *  inject one directly) — protocolClient.ts's own fallback is
 *  `new WebSocket(url)`, so faking the global is the only seam available
 *  at the component level. Every constructed instance is pushed onto
 *  `instances` so the test can grab the one LiveEngineBadge created. */
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

test('MUT1 CLOSURE: LiveEngineBadge (flag on) feeding NewsFeed a real decoded f2.finance delta shows the entry end-to-end', async () => {
  const dom = installJsdom();
  const originalWebSocket = (globalThis as any).WebSocket;
  try {
    // Force the feature flag on for THIS test's jsdom localStorage only —
    // isLiveEngineEnabled's documented default (absent key = disabled)
    // and LiveEngineBadge.tsx's own default-OFF behaviour are UNCHANGED;
    // this surface is visible ONLY when the flag is on, exactly like a
    // real dev session that opts in via localStorage.
    const { LIVE_ENGINE_FLAG_KEY } = await import('../src/sim/liveEngineFlag.ts');
    dom.window.localStorage.setItem(LIVE_ENGINE_FLAG_KEY, '1');

    FakeWebSocket.instances = [];
    (globalThis as any).WebSocket = FakeWebSocket;
    (dom.window as any).WebSocket = FakeWebSocket;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimContext } = await import('../src/sim/simContext.ts');
    const { NewsFeed } = await import('../src/components/NewsFeed.tsx');
    const { LiveEngineBadge } = await import('../src/components/LiveEngineBadge.tsx');
    const { financeStatusTracker } = await import('../src/sim/financeStatusTracker.ts');
    const { FINANCE_SCHEMA_VERSION } = await import('../src/sim/wire.ts');

    financeStatusTracker.reset();

    const simCtx = {
      state: { notice: null, milestoneNotice: null, placeNotice: null, tick: 1 },
      dispatch: () => {},
      cityName: 'Attackville',
      listSaves: () => [],
      listRecent: () => [],
      saveGame: async () => true,
      saveGameAs: async () => ({ ok: true }),
      loadGame: async () => {},
      loadNamed: async () => {},
      renameCity: () => ({ ok: true }),
      exportCity: async () => true,
      importCity: async () => true,
    };

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(
        React.default.createElement(
          SimContext.Provider,
          { value: simCtx as any },
          React.default.createElement(
            React.default.Fragment,
            null,
            React.default.createElement(LiveEngineBadge),
            React.default.createElement(NewsFeed)
          )
        )
      );
    });

    assert.equal(FakeWebSocket.instances.length, 1, 'precondition: LiveEngineBadge must have opened exactly one WebSocket (flag on)');
    const socket = FakeWebSocket.instances[0];

    // Drive the REAL ProtocolClient handshake -> live -> subscribe, all
    // through the badge's own instance — nothing here talks to the
    // tracker directly.
    await act(async () => {
      socket.emit('open', {});
    });
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v0.0.0-test' } });
    });

    // A real decoded "f2.finance" delta carrying a starved payrollShortfall
    // section — the exact shape compose's finance_publish.go produces.
    await act(async () => {
      socket.serverSends({
        jsonrpc: '2.0',
        method: 'delta',
        params: {
          subscriptionId: 'sub-finance-1',
          tick: 42,
          seq: 1,
          patch: {
            schemaVersion: FINANCE_SCHEMA_VERSION,
            payrollShortfall: { month: 5, amountMicropounds: 300_000, months: 1 },
          },
        },
      });
    });

    function expandPanel() {
      const ticker = container.querySelector('.news-feed-ticker') as HTMLButtonElement | null;
      assert.ok(ticker, 'precondition: the news-feed-ticker button must be present');
      return act(async () => {
        ticker!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
      });
    }

    await expandPanel();
    const entries = [...container.querySelectorAll('.news-feed-entry')].map((li) => ({
      severity: [...li.classList].find((c) => c.startsWith('news-') && c !== 'news-feed-entry') ?? '',
      text: li.querySelector('.news-feed-text')?.textContent ?? '',
    }));

    assert.equal(
      entries.length,
      1,
      `expected exactly one feed entry after the real badge-fed delta, got ${entries.length}: ${JSON.stringify(entries)}`
    );
    assert.equal(entries[0].severity, 'news-warning');
    assert.match(entries[0].text, /shortfall/i);
    assert.match(entries[0].text, /£300\b/);

    await act(async () => {
      root.unmount();
    });
  } finally {
    (globalThis as any).WebSocket = originalWebSocket;
    const { financeStatusTracker } = await import('../src/sim/financeStatusTracker.ts');
    financeStatusTracker.reset();
    dom.window.close();
  }
});
