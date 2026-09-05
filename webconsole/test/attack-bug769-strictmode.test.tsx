// bug-769-liveenginebadge-to-newsfeed.test.tsx — BUG-769: mirrors
// bug-723-liveenginebadge-to-newsfeed.test.tsx's exact end-to-end shape
// for the OTHER real, already-consumed BUG-759 gap (FinanceAPI.
// InsolvencyMonths()/IsInsolvent()). Mounts LiveEngineBadge (feature flag
// ON) and NewsFeed TOGETHER, feeds a real decoded "f2.finance" delta
// carrying insolvencyMonths/insolvent through the badge's own
// ProtocolClient (a fake WebSocket transport), and asserts the feed shows
// the resulting entry — proving the wiring through the REAL component,
// not a direct financeStatusTracker.setInsolvency write.

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

test('ATTACK BUG-769 StrictMode: real StrictMode mount records the insolvency entry EXACTLY once', async () => {
  const dom = installJsdom();
  const originalWebSocket = (globalThis as any).WebSocket;
  try {
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
          React.default.StrictMode,
          null,
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
        )
      );
    });

    // StrictMode double-invokes the badge's effect, so it opens two sockets
    // (a pre-existing badge property, not BUG-769's). Drive the LIVE one.
    assert.ok(FakeWebSocket.instances.length >= 1, 'precondition: LiveEngineBadge must have opened a WebSocket (flag on)');
    const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];

    await act(async () => {
      socket.emit('open', {});
    });
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', id: 1, result: { accepted: true, serverVersion: 'v0.0.0-test' } });
    });

    // A real decoded "f2.finance" delta carrying a 3-consecutive-month
    // insolvent reading — the exact shape compose's finance_publish.go
    // produces (BUG-769's flat insolvencyMonths/insolvent fields).
    await act(async () => {
      socket.serverSends({
        jsonrpc: '2.0',
        method: 'delta',
        params: {
          subscriptionId: 'sub-finance-1',
          tick: 90,
          seq: 1,
          patch: {
            schemaVersion: FINANCE_SCHEMA_VERSION,
            insolvencyMonths: 3,
            insolvent: true,
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
      `expected exactly one feed entry after the real badge-fed insolvency delta, got ${entries.length}: ${JSON.stringify(entries)}`
    );
    assert.equal(entries[0].severity, 'news-error');
    assert.match(entries[0].text, /insolvent/i);
    assert.match(entries[0].text, /3\b/);

    // ATTACK: a SECOND identical insolvent delta must not duplicate.
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-finance-1', tick: 120, seq: 2,
        patch: { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: 4, insolvent: true } } });
    });
    await act(async () => {});
    let n = container.querySelectorAll('.news-feed-entry').length;
    assert.equal(n, 1, `a second insolvent delta duplicated the entry (got ${n})`);

    // ATTACK: a delta with the insolvency fields ABSENT (disconnect/unknown)
    // must fire NOTHING — no phantom "resolved".
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-finance-1', tick: 150, seq: 3,
        patch: { schemaVersion: FINANCE_SCHEMA_VERSION } } });
    });
    await act(async () => {});
    n = container.querySelectorAll('.news-feed-entry').length;
    assert.equal(n, 1, `an absent-fields (unknown) delta fired a spurious entry (got ${n})`);

    // ATTACK: a genuine clear (insolvent:false) fires exactly one success.
    await act(async () => {
      socket.serverSends({ jsonrpc: '2.0', method: 'delta', params: { subscriptionId: 'sub-finance-1', tick: 180, seq: 4,
        patch: { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: 0, insolvent: false } } });
    });
    await act(async () => {});
    const finalEntries = [...container.querySelectorAll('.news-feed-entry')].map((li) => ({
      severity: [...li.classList].find((c) => c.startsWith('news-') && c !== 'news-feed-entry') ?? '',
      text: li.querySelector('.news-feed-text')?.textContent ?? '',
    }));
    assert.equal(finalEntries.length, 2, `expected exactly 2 entries after a genuine clear, got ${finalEntries.length}: ${JSON.stringify(finalEntries)}`);
    assert.equal(finalEntries[0].severity, 'news-success');

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
