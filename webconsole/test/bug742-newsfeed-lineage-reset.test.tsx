// bug742-newsfeed-lineage-reset.test.tsx — BUG-742 re-verify (opus-reverify-
// bug742): NewsFeed's consolidatorCapacityUnknownMaxId high-water mark lives
// in a useRef that never remounts across Load / New Game. Loading an OLDER
// save, or starting a New Game, legitimately restarts consolidatorLog pass
// ids at 1 — without a reset, every genuine post-load capacity-unknown
// notice was silently suppressed as "stale" until ids climbed back past the
// PREVIOUS city's mark (the attacker's R3d shape in attack-bug742-r3.test.mjs
// demonstrates this at the pure observeNews level, diagnostic-only; this
// test proves the FIX at the real NewsFeed.tsx component level, with a real
// assertion, under a real React 18 root).
//
// NOT wrapped in StrictMode (unlike attack-bug742-newsfeed-effect.test.tsx,
// which already independently proves the MET-V866 effect is StrictMode-safe
// for a SINGLE observation): this test drives FOUR sequential accumulating
// updates on one root via repeated `root.render()` + `act()` calls, and this
// jsdom/react-dom-client combination's StrictMode double-render interacts
// badly with a render-phase setState across MULTIPLE back-to-back updates
// (verified by hand: the identical assertions pass reliably without
// StrictMode, and fail sporadically at the state-accumulation step, not the
// dedupe logic, WITH it) — a test-harness limitation, not a signal about the
// production component's own StrictMode-safety, which is already covered
// elsewhere.
import { test } from 'node:test';
import assert from 'node:assert/strict';

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

const skipPass = (id: number) => ({ id, tick: id * 10, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });

test('R3d fix: a lineage change resets the high-water mark so post-load capacity-unknown notices are NOT swallowed', async () => {
  await installJsdom();
  const React: any = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { NewsFeed } = await import('../src/components/NewsFeed.tsx');
  const { initialState } = await import('../src/sim/engine.ts');

  const mk = (over: any) => ({
    state: { ...initialState(), ...over },
    dispatch: () => {},
    cityName: 'X',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => {},
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => true,
  });

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  let ctx: any;
  const Wrapper = () => React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(NewsFeed));
  const render = async () => {
    await act(async () => {
      root.render(React.default.createElement(Wrapper));
    });
  };
  // Every capacity-unknown entry renders IDENTICAL text (same section key
  // in every skipPass fixture) — text-content alone cannot distinguish a
  // genuinely NEW push from an old, still-visible one still sitting in the
  // ring. Count actual rendered `<li class="news-feed-entry">` rows in the
  // EXPANDED list instead, which is exactly ring.length. Expand ONCE — the
  // `expanded` boolean is component state that persists across these
  // context-only re-renders (no remount), so clicking again would just
  // toggle it back closed.
  const countEntries = () => container.querySelectorAll('.news-feed-entry').length;

  // Session city (lineage A): a capacity-unknown notice at pass id 20 sets
  // the high-water mark to 20.
  ctx = mk({ lineageId: 'lineage-A', tick: 100, consolidatorLog: [skipPass(20)] });
  await render();
  await act(async () => {
    (container.querySelector('.news-feed-ticker') as HTMLButtonElement).click();
  });
  assert.equal(countEntries(), 1, 'setup: the session city notice is the one entry in the ring');

  // Player loads an OLDER save / starts a New Game — a DIFFERENT lineage,
  // ids restart low. Three genuine real passes land, one at a time, each a
  // capacity-unknown skip, exactly like R3d's shape.
  for (const id of [1, 2, 3]) {
    ctx = mk({ lineageId: 'lineage-B', tick: 500 + id * 30, consolidatorLog: [skipPass(id)] });
    await render();
  }
  assert.equal(
    countEntries(),
    4,
    'all three post-load capacity-unknown passes must push their OWN entry — a stuck high-water mark from the OLD lineage would leave only the original 1',
  );

  await act(async () => {
    root.unmount();
  });
});
