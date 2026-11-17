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


async function withFullKeyhandler(base: any) {
  const h = await mount(base);
  const c = { setView: 0, zoom: 0, cancel: 0, power: 0, dispatch: [] as any[] };
  const handler = makeKeydownHandler({
    dispatch: (a: any) => c.dispatch.push(a),
    getState: () => ({ speed: 1 }),
    setView: () => { c.setView++; },
    clampView: (v: any) => v,
    nudgeZoom: () => { c.zoom++; },
    setShowWater: () => {}, setShowPower: () => { c.power++; }, setShowLines: () => {}, setShowRefs: () => {},
    setHelpOpen: () => {}, helpOpen: false,
    view: { zoom: 1, cx: 10, cy: 10 } as any, size: { w: 100, h: 100 },
    MAP_W: 100, MAP_H: 100, MIN_ZOOM: 1,
    isTextInput: (t: any) => t instanceof h.dom.window.HTMLInputElement || t instanceof h.dom.window.HTMLTextAreaElement,
    cancelToSelect: () => { c.cancel++; },
  } as any);
  h.dom.window.addEventListener('keydown', handler);
  const key = async (el: any, k: string) => h.act(async () => { el.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true })); });
  return { h, c, key };
}

const CONSUMED = ['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'Enter'];
const SEL: Record<string, string> = {
  domain: '.bt-domain > .bt-head',
  family: '.bt-family > .bt-head',
  spec: '.bt-spec',
};

for (const kind of Object.keys(SEL)) {
  for (const k of CONSUMED) {
    test(`R2-A ${kind} row: consumed key ${k} reaches no global action`, async () => {
      const { h, c, key } = await withFullKeyhandler({ ...initialState(), funds: 1e12 });
      const el = h.container.querySelector(SEL[kind]);
      assert.ok(el, `no ${kind} row rendered`);
      await h.act(async () => el.focus());
      await key(el, k);
      assert.equal(c.setView, 0, `${kind}/${k} moved camera`);
      assert.equal(c.dispatch.length, 0, `${kind}/${k} global dispatch ${JSON.stringify(c.dispatch)}`);
      assert.equal(c.cancel + c.zoom + c.power, 0);
      await h.act(async () => h.root.unmount());
    });
  }
}

test('R2-B search box: arrows/Home/End/Enter reach no camera action', async () => {
  const { h, c, key } = await withFullKeyhandler(initialState());
  for (const k of CONSUMED) await key(h.container.querySelector('.bt-input'), k);
  assert.equal(c.setView, 0);
  assert.equal(c.dispatch.length, 0);
  await h.act(async () => h.root.unmount());
});

for (const kind of Object.keys(SEL)) {
  test(`R2-C ${kind} row: unconsumed keys p, Space, 1, +, Esc still reach globals`, async () => {
    const { h, c, key } = await withFullKeyhandler({ ...initialState(), funds: 1e12 });
    const el = h.container.querySelector(SEL[kind]);
    await h.act(async () => el.focus());
    await key(el, 'p');
    assert.equal(c.power, 1, 'p');
    await key(el, ' ');
    assert.ok(c.dispatch.some((a) => a.type === 'speed'), 'Space');
    await key(el, '1');
    assert.ok(c.dispatch.some((a) => a.type === 'tool'), '1');
    await key(el, '+');
    assert.equal(c.zoom, 1, '+');
    await key(el, 'Escape');
    assert.equal(c.cancel, 1, 'Esc');
    await h.act(async () => h.root.unmount());
  });
}
