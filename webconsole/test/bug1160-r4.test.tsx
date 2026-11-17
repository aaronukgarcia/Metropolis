// bug1160-r4.test.tsx - BUG-1160 round-3 rework (H1 modal host, H2 corner
// clearance). Dialogs are modals, not notifications: a dialog slot renders in
// the viewport-fixed MODAL HOST (never in the map-anchored stack / .map-wrap)
// and OK takes focus. jsdom has no layout, so geometry is pinned structurally
// here and proven in a real browser separately.

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

test('H1 (b): .notif-modal-host is position:fixed, viewport-centred, at a registered z between NOTIF_STACK and the blocking modals', async () => {
  const css = await read('../src/styles.css');
  const b = ruleBody(css, '.notif-modal-host');
  assert.match(b, /position:\s*fixed/);
  assert.match(b, /inset:\s*0/);
  assert.match(b, /align-items:\s*center/);
  assert.match(b, /justify-content:\s*center/);
  assert.ok(!/transform|filter|contain\s*:|will-change/.test(b), 'no containing-block trap for the fixed backdrop');
  const z = Number(/z-index:\s*(\d+)/.exec(b)?.[1]);
  const { Z_INDEX } = await import('../src/components/overlayLayers.ts');
  assert.equal(z, Z_INDEX.NOTIF_MODAL_HOST, 'styles.css z equals the registered constant');
  assert.ok(Z_INDEX.NOTIF_MODAL_HOST > Z_INDEX.NOTIF_STACK, 'above the notification stack');
  for (const k of ['INFO_DIALOG', 'INSOLVENCY_POPUP', 'DECLINE_SCREEN', 'REBUILD_PROMPT'] as const) {
    assert.ok(Z_INDEX.NOTIF_MODAL_HOST < Z_INDEX[k], `below blocking modal ${k}`);
  }
});

test('H1: the modal host is NOT inside .map-wrap or any transformed ancestor (App renders it from the provider)', async () => {
  const mv = await read('../src/components/MapView.tsx');
  assert.ok(!/NotificationModalHost/.test(mv), 'MapView must not host the modal host');
  const host = await read('../src/components/NotificationHost.tsx');
  assert.match(host, /<NotificationModalHost \/>/);
});

async function mount(withDialog: boolean, extra?: () => void) {
  const dom = new JSDOM('<!doctype html><html><body><button id="opener">open</button><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  extra?.();
  window.document.getElementById('opener')!.focus();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { NotificationProvider, NotificationSlot, NotificationStack } = await import('../src/components/NotificationHost.tsx');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const h = React.createElement;
  const root = createRoot(window.document.getElementById('root')!);
  const render = async (dialog: boolean) =>
    act(async () => {
      root.render(
        h(
          NotificationProvider as any,
          { anchored: true },
          h('div', { className: 'map-wrap', id: 'map' }, h(NotificationStack, null)),
          h(NotificationSlot as any, { id: 'advisor', kind: 'banner' }, h('div', { className: 'advisor' }, 'adv')),
          dialog ? h(AffordabilityConfirm, { message: 'm', onConfirm: () => {}, onCancel: () => {} }) : null
        )
      );
    });
  await render(withDialog);
  return { doc: window.document, root, act, render };
}

test('H1 (a): a dialog slot renders inside the modal host (not .map-wrap, not the stack) and OK gets focus; focus is restored on close', async () => {
  const { doc, root, act, render } = await mount(true);
  try {
    const dlg = doc.querySelector('[role="dialog"]') as HTMLElement;
    assert.ok(dlg, 'dialog rendered');
    assert.ok(dlg.closest('[data-notif-modal-host]'), 'dialog is inside the modal host');
    assert.ok(!dlg.closest('.map-wrap'), 'dialog is not inside .map-wrap');
    assert.ok(!dlg.closest('[data-notif-stack]'), 'dialog is not inside the notification stack');
    assert.ok(doc.querySelector('[data-notif-modal-host] [data-affordability-backdrop]'), 'backdrop is in the host');
    assert.ok(doc.querySelector('[data-notif-stack] .advisor'), 'banner still lives in the stack');
    const ok = doc.querySelector('[role="dialog"] button') as HTMLElement;
    assert.equal(ok.textContent, 'Build anyway');
    assert.ok(doc.activeElement === ok, 'OK (primary) button holds focus on open');
    assert.ok(doc.querySelector('[data-notif-stack] [inert] .advisor'), 'stack slots are inert while the dialog is open');
    await render(false);
    assert.equal(doc.activeElement?.id, 'opener', 'focus restored to the opener on close');
  } finally {
    await act(async () => root.unmount());
  }
});

test('H2 (c): the stack starts below the .map-coord/.map-block corner (top >= map-block bottom) so it can never cover them', async () => {
  const css = await read('../src/styles.css');
  const px = (body: string, prop: string) => Number(new RegExp('(?:^|[\\s;])' + prop + ':\\s*(\\d+)px').exec(body)?.[1]);
  const stack = ruleBody(css, '.notif-stack');
  const coord = ruleBody(css, '.map-coord');
  const block = ruleBody(css, '.map-block');
  const stackTop = px(stack, 'top');
  const blockTop = px(block, 'top');
  assert.ok(Number.isFinite(stackTop) && Number.isFinite(blockTop));
  // .map-block is one 11px line + 2px padding + 2px border = 22px tall.
  assert.ok(stackTop >= blockTop + 22, `stack top ${stackTop} must clear .map-block (top ${blockTop} + 22)`);
  assert.ok(px(coord, 'top') < blockTop, 'coord sits above block');
  assert.match(stack, /max-height:\s*calc\(100%\s*-\s*\d+px\)/);
  assert.ok(Number(/max-height:\s*calc\(100%\s*-\s*(\d+)px\)/.exec(stack)![1]) >= stackTop, 'bottom stays inside the map');
});
