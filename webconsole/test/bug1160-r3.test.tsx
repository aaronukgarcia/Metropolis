// bug1160-r3.test.tsx — BUG-1160 round-2 rework (G1 map anchoring, G2 inert
// slots, M3 width bound). jsdom has no layout engine, so geometry is pinned
// structurally here and proven in a real browser separately.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => fs.readFile(path.resolve(here, rel), 'utf8');

function ruleBody(css: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = css.match(new RegExp('(?:^|\\n)' + esc + '\\s*\\{([^}]*)\\}'));
  assert.ok(m, `${selector} rule must exist`);
  return m![1];
}

test('G1: MapView renders the stack INSIDE .map-wrap, and App uses the anchored provider', async () => {
  const mv = await read('../src/components/MapView.tsx');
  assert.match(mv, /<div className="map-wrap" ref=\{wrapRef\}>\s*<NotificationStack \/>/);
  const app = await read('../src/App.tsx');
  assert.ok(app.includes('<NotificationProvider anchored>'));
});

test('G1/M3: the stack is absolutely positioned and capped to the map column, never to the viewport', async () => {
  const css = await read('../src/styles.css');
  const b = ruleBody(css, '.notif-stack');
  assert.match(b, /position:\s*absolute/);
  assert.ok(!/\d+vw\b/.test(b), 'no viewport-width unit may size the anchored stack');
  const w = /(?:^|[\s;])width:\s*min\((\d+)px,\s*calc\(100%\s*-\s*(\d+)px\)\)/.exec(b);
  assert.ok(w, 'width must be min(<=700px, calc(100% - gutter)) so it is bounded by .map-wrap');
  assert.ok(Number(w![1]) <= 700, 'cap must stay modest');
  assert.match(b, /max-width:\s*calc\(100%\s*-\s*\d+px\)/, 'hard max-width bound to the map column');
  assert.ok(!/transform|filter|contain\s*:/.test(b), 'no containing-block trap for the fixed backdrop');
  const wrap = css.match(/\n\.map-wrap\s*\{[^}]*position:\s*relative[^}]*\}/g);
  assert.ok(wrap && wrap.length, '.map-wrap is the positioned ancestor');
  assert.ok(!wrap!.some((r) => /transform|filter|contain\s*:|will-change/.test(r)), '.map-wrap must not trap position:fixed');
});

test('G2: slots are click-through; only genuinely interactive children take the pointer', async () => {
  const css = await read('../src/styles.css');
  assert.match(ruleBody(css, '.notif-slot'), /pointer-events:\s*none/);
});

async function mountAnchored(withDialog: boolean) {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { NotificationProvider, NotificationSlot, NotificationStack } = await import('../src/components/NotificationHost.tsx');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner.tsx');
  const h = React.createElement;
  const root = createRoot(window.document.getElementById('root')!);
  await act(async () => {
    root.render(
      h(
        NotificationProvider as any,
        { anchored: true },
        h('div', { className: 'map-wrap', id: 'map' }, h(NotificationStack, null)),
        h(NotificationSlot as any, { id: 'advisor', kind: 'banner' }, h('div', { className: 'advisor clickable' }, h('button', { id: 'adv' }, 'place'))),
        h(StaleBuildBannerView, { result: { stale: true, runningSha: 'a', diskSha: 'b' } as any }),
        withDialog ? h(AffordabilityConfirm, { message: 'm', onConfirm: () => {}, onCancel: () => {} }) : null
      )
    );
  });
  return { doc: window.document, root, act };
}

test('G1: the anchored provider portals every slot into the stack inside .map-wrap (no stack of its own)', async () => {
  const { doc, root, act } = await mountAnchored(false);
  try {
    const stacks = doc.querySelectorAll('[data-notif-stack]');
    assert.equal(stacks.length, 1);
    assert.ok(stacks[0].closest('.map-wrap'), 'stack is inside the map element');
    assert.ok(stacks[0].querySelector('.advisor'), 'advisor slot lives in the stack');
    assert.ok(!stacks[0].querySelector('[inert]'), 'nothing inert without a dialog');
  } finally {
    await act(async () => root.unmount());
  }
});

test('G2: with a confirm open the advisor and the stale banner (and ALL their controls) sit under an inert slot; the dialog does not', async () => {
  const { doc, root, act } = await mountAnchored(true);
  try {
    const adv = doc.querySelector('#adv') as HTMLElement;
    assert.ok(adv.closest('[inert]'), 'advisor button is inert');
    const reload = doc.querySelector('.stale-build-banner-reload') as HTMLElement;
    assert.ok(reload.closest('[inert]'), 'Reload button is inert');
    const ok = doc.querySelector('[role="dialog"] button') as HTMLElement;
    assert.ok(ok && !ok.closest('[inert]'), 'dialog controls stay live');
    assert.equal(doc.querySelectorAll('.notif-dialog[inert]').length, 0);
  } finally {
    await act(async () => root.unmount());
  }
});
