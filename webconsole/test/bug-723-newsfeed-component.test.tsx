// bug-723-newsfeed-component.test.tsx — BUG-723 round finding F1
// (opus-round-bug723 REJECT): "the unit tests alone are not a surface".
// This file proves the end-to-end wiring at the COMPONENT level — a real
// client-mounted <NewsFeed /> (react-dom/client createRoot, real DOM via
// jsdom, real act()), reading the REAL financeStatusTracker.ts singleton
// (the seam LiveEngineBadge.tsx's onDelta writes to — see that file's
// BUG-723 comment) rather than calling sim/newsFeed.ts's observeNews()
// directly. A real starve->clear sequence of decoded "f2.finance" patch
// shapes is pushed through financeStatusTracker.setPayrollShortfall (the
// exact call LiveEngineBadge.tsx makes after decodeFinanceBalanceSheetPatch)
// and the rendered DOM is asserted to show exactly one warning entry then
// exactly one success entry — proving NewsFeed.tsx's sourcesChanged gate and
// its call into observeNews both actually include payrollShortfall, not
// just that the pure function CAN handle it in isolation.

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
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame?.bind(window) ?? ((cb: any) => setTimeout(cb, 0));
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame?.bind(window) ?? ((id: any) => clearTimeout(id));
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

/** Minimal SimContextValue — NewsFeed only reads state.{notice,
 *  milestoneNotice, placeNotice, tick} off it; the other fields are never
 *  touched by this component, so stub functions are enough. */
function makeSimCtx(tick: number) {
  return {
    state: { notice: null, milestoneNotice: null, placeNotice: null, tick },
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
}

test('COMPONENT: a real <NewsFeed/> mount surfaces payrollShortfall via financeStatusTracker end-to-end (BUG-723 F1)', async () => {
  const dom = installJsdom();
  let financeStatusTracker: typeof import('../src/sim/financeStatusTracker.ts').financeStatusTracker | undefined;
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimContext } = await import('../src/sim/simContext.ts');
    const { NewsFeed } = await import('../src/components/NewsFeed.tsx');
    ({ financeStatusTracker } = await import('../src/sim/financeStatusTracker.ts'));
    const tracker = financeStatusTracker;

    // The tracker is a module-level singleton shared with whatever else
    // imported it earlier in the process — reset to a known "no live
    // data" baseline so this test is not order-dependent on other suites.
    tracker.reset();

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    function renderAt(tick: number) {
      return act(async () => {
        root.render(
          React.default.createElement(
            SimContext.Provider,
            { value: makeSimCtx(tick) as any },
            React.default.createElement(NewsFeed)
          )
        );
      });
    }

    function expandPanel() {
      const ticker = container.querySelector('.news-feed-ticker') as HTMLButtonElement | null;
      assert.ok(ticker, 'precondition: the news-feed-ticker button must be present');
      return act(async () => {
        ticker!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
      });
    }

    function entryTexts(): { severity: string; text: string }[] {
      return [...container.querySelectorAll('.news-feed-entry')].map((li) => ({
        severity: [...li.classList].find((c) => c.startsWith('news-') && c !== 'news-feed-entry') ?? '',
        text: li.querySelector('.news-feed-text')?.textContent ?? '',
      }));
    }

    // Initial mount: no live-engine data at all yet.
    await renderAt(1);
    assert.match(container.textContent ?? '', /No news yet/, 'precondition: empty feed before any finance data arrives');

    // Clean months first (real "0 shortfall" readings, matching what a
    // healthy connected engine actually publishes every tick — never
    // omitted/undefined) — must NOT produce any entry.
    await act(async () => {
      tracker.setPayrollShortfall({ month: 1, amountMicropounds: 0, months: 0 });
    });
    await renderAt(2);
    await act(async () => {
      tracker.setPayrollShortfall({ month: 2, amountMicropounds: 0, months: 0 });
    });
    await renderAt(3);
    assert.match(
      container.textContent ?? '',
      /No news yet/,
      'clean payrollShortfall readings (amount 0) must not produce a feed entry'
    );

    // Starve: a real decoded "f2.finance" patch section, exactly the
    // shape LiveEngineBadge.tsx hands to financeStatusTracker.
    await act(async () => {
      tracker.setPayrollShortfall({ month: 3, amountMicropounds: 200_000, months: 1 });
    });
    await renderAt(4);
    await expandPanel();
    let entries = entryTexts();
    assert.equal(entries.length, 1, `expected exactly one entry after the starve, got ${entries.length}: ${JSON.stringify(entries)}`);
    assert.equal(entries[0].severity, 'news-warning');
    assert.match(entries[0].text, /shortfall/i);
    assert.match(entries[0].text, /£200\b/, 'the amount renders at the CURRENT 1,000-micropounds-per-pound scale');

    // A second starved month (amount/months both change) must not add a
    // second entry — collapse the panel and reopen it to also prove the
    // ring survives an expand/collapse cycle.
    await expandPanel(); // collapse
    await act(async () => {
      tracker.setPayrollShortfall({ month: 4, amountMicropounds: 350_000, months: 2 });
    });
    await renderAt(5);
    await expandPanel(); // expand again
    entries = entryTexts();
    assert.equal(entries.length, 1, 'a persisting shortfall must not append a second warning entry');

    // Recover: exactly one CLEAR (success) entry appears, newest-first.
    await act(async () => {
      tracker.setPayrollShortfall({ month: 5, amountMicropounds: 0, months: 0 });
    });
    await renderAt(6);
    entries = entryTexts();
    assert.equal(entries.length, 2, `expected exactly one warning + one success entry after recovery, got ${entries.length}: ${JSON.stringify(entries)}`);
    assert.equal(entries[0].severity, 'news-success', 'the newest (recovery) entry must be first — newest-first ring');
    assert.match(entries[0].text, /recovered/i);
    assert.equal(entries[1].severity, 'news-warning', 'the original start entry must still be present, second');

    // A subsequent clean month must not append yet another clear entry.
    await act(async () => {
      tracker.setPayrollShortfall({ month: 6, amountMicropounds: 0, months: 0 });
    });
    await renderAt(7);
    entries = entryTexts();
    assert.equal(entries.length, 2, 'a subsequent clean reading must not append another entry');

    await act(async () => {
      root.unmount();
    });
  } finally {
    financeStatusTracker?.reset();
    dom.window.close();
  }
});
