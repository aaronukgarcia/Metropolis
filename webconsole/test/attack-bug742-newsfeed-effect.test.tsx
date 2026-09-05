// R3 re-verify: the NewsFeed.tsx useEffect that records MET-V866, under a
// REAL React 18 StrictMode client mount (double-invoked effects), including
// an entry buried under later pushes.
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

test('StrictMode double-effect records MET-V866 exactly once per pass, and a buried entry is still recorded', async () => {
  await installJsdom();
  const React: any = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { NewsFeed } = await import('../src/components/NewsFeed.tsx');
  const { initialState } = await import('../src/sim/engine.ts');
  const { recentErrors } = await import('../src/sim/backend.ts');

  const v866 = () => recentErrors().filter((e: any) => e.code === 'MET-V866').reduce((n: number, e: any) => n + e.count, 0);

  const base: any = { ...initialState(), tick: 100 };
  const mk = (over: any) => ({
    state: { ...base, ...over }, dispatch: () => {}, cityName: 'X',
    listSaves: () => [], listRecent: () => [], saveGame: async () => true,
    saveGameAs: async () => {}, loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => true,
  });

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  const render = async (ctx: any) => {
    await act(async () => {
      root.render(React.default.createElement(React.default.StrictMode, null,
        React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(NewsFeed))));
    });
  };

  const before = v866();
  await render(mk({ consolidatorLog: [skipPass(3)] }));
  const afterFirst = v866();
  console.log(`V866 after one StrictMode mount with a capacity-unknown pass: ${afterFirst - before}`);
  assert.equal(afterFirst - before, 1, 'StrictMode double-invoked effect records exactly once');
  console.log('DOM:', container.textContent?.slice(0, 140));
  await act(async () => { root.unmount(); });

  // A SECOND, independent mount observing a DIFFERENT pass records again
  // (proves the ref-Set guard is per-mount and not a permanent silencer).
  const container2 = (globalThis as any).document.createElement('div');
  (globalThis as any).document.body.appendChild(container2);
  const root2 = createRoot(container2);
  await act(async () => {
    root2.render(React.default.createElement(React.default.StrictMode, null,
      React.default.createElement(SimContext.Provider, { value: mk({ consolidatorLog: [skipPass(9)] }) }, React.default.createElement(NewsFeed))));
  });
  const afterSecond = v866();
  console.log(`V866 after a second mount with a different pass: ${afterSecond - afterFirst}`);
  assert.equal(afterSecond - afterFirst, 1);
  await act(async () => { root2.unmount(); });
});
