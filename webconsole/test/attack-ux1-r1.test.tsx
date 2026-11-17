// attack-ux1-r1.test.tsx — round r1 attacker suite for FEAT-9995385591 (UX-1).
// Global-keyhandler interaction attacks kept as a permanent regression.
// R1-A (old-vs-new BottomBar equivalence over all 199 specs x 3 states) is NOT carried:
// it needs a scratch copy of the origin/main BottomBar (BottomBarOld.tsx).
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { initialState } from '../src/sim/engine.ts';
import { makeKeydownHandler } from '../src/sim/keyhandler.ts';

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
  const g = globalThis as any;
  g.window = dom.window;
  g.document = dom.window.document;
  Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true, writable: true });
  g.HTMLElement = dom.window.HTMLElement;
  g.HTMLInputElement = dom.window.HTMLInputElement;
  g.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  g.requestAnimationFrame = (cb: any) => setTimeout(cb, 0);
  g.cancelAnimationFrame = clearTimeout;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  return dom as any;
}

async function mount(base: any) {
  const dom = await installJsdom();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const Comp = (await import('../src/components/bottom/BottomBar.tsx')).BottomBar;
  const actions: any[] = [];
  let setSt: any = null;
  const Wrap = () => {
    const [st, set] = React.useState(base);
    setSt = set;
    const dispatch = (a: any) => {
      actions.push(a);
      if (a.type === 'tool') set((s: any) => ({ ...s, tool: a.tool }));
    };
    return React.createElement(SimContext.Provider, { value: { state: st, dispatch } as any }, React.createElement(Comp));
  };
  const container = dom.window.document.getElementById('root');
  const root = createRoot(container);
  await act(async () => root.render(React.createElement(Wrap)));
  const qa = (sel: string) => Array.from(container.querySelectorAll(sel)) as any[];
  const click = async (el: any) => act(async () => { el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true })); });
  const reset = async () => act(async () => setSt((s: any) => ({ ...s, tool: { mode: 'select' } })));
  return { dom, act, container, qa, click, reset, actions, root };
}

async function withKeyhandler(base: any) {
  const h = await mount(base);
  const calls = { setView: 0, cancel: 0, dispatch: [] as any[], speed: 0 };
  const handler = makeKeydownHandler({
    dispatch: (a: any) => calls.dispatch.push(a),
    getState: () => ({ speed: 1 }),
    setView: () => { calls.setView++; },
    clampView: (v: any) => v,
    nudgeZoom: () => {},
    setShowWater: () => {}, setShowPower: () => {}, setShowLines: () => {}, setShowRefs: () => {},
    setHelpOpen: () => {},
    helpOpen: false,
    view: { zoom: 1, cx: 10, cy: 10 } as any,
    size: { w: 100, h: 100 },
    MAP_W: 100, MAP_H: 100, MIN_ZOOM: 1,
    isTextInput: (t: any) => t instanceof h.dom.window.HTMLInputElement || t instanceof h.dom.window.HTMLTextAreaElement,
    cancelToSelect: () => { calls.cancel++; },
  } as any);
  h.dom.window.addEventListener('keydown', handler);
  const key = async (el: any, k: string) => h.act(async () => { el.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true })); });
  return { h, calls, key };
}

test('R1-B: Esc in a non-empty search clears it WITHOUT cancelling the active tool; Esc in an empty search still cancels (old behaviour)', async () => {
  const { h, calls, key } = await withKeyhandler(initialState());
  const input = h.container.querySelector('.bt-input');
  await h.act(async () => {
    const setter = Object.getOwnPropertyDescriptor(h.dom.window.HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, 'road');
    input.dispatchEvent(new h.dom.window.Event('input', { bubbles: true }));
  });
  await key(input, 'Escape');
  assert.equal(h.container.querySelector('.bt-input').value, '');
  assert.equal(calls.cancel, 0, 'Esc with a query must not reach the global cancel');
  await key(h.container.querySelector('.bt-input'), 'Escape');
  assert.equal(calls.cancel, 1, 'Esc with empty query falls through to the global cancel');
  // digits typed into search never quick-pick
  await key(h.container.querySelector('.bt-input'), '1');
  assert.equal(calls.dispatch.length, 0, 'typing 1 in search dispatches nothing');
  await h.act(async () => h.root.unmount());
});

test('R1-C: arrow/Home navigation inside the tree must not ALSO pan/reset the map camera', async () => {
  const { h, calls, key } = await withKeyhandler(initialState());
  const first = h.container.querySelector('.bt-tree [tabindex="0"]');
  await h.act(async () => first.focus());
  for (const k of ['ArrowDown', 'ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'End', 'Home']) {
    await key(h.dom.window.document.activeElement, k);
  }
  console.log(`[R1-C] setView calls during tree navigation: ${calls.setView}`);
  assert.equal(calls.setView, 0, `tree navigation also moved the map camera ${calls.setView} times`);
  await h.act(async () => h.root.unmount());
});
