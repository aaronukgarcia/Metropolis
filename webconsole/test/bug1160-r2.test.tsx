// bug1160-r2.test.tsx — BUG-1160 round-1 rework (F1..F5). jsdom has no layout
// engine, so geometry claims are proven structurally (DOM structure + the
// shipped styles.css text), exactly like bug1160-notification-layout.test.tsx.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { Z_INDEX } from '../src/components/overlayLayers';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => fs.readFile(path.resolve(here, rel), 'utf8');

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
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

function ruleBody(css: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\:]/g, '\\$&');
  const m = new RegExp(`(?:^|\\n)${esc}\\s*\\{([^}]*)\\}`).exec(css);
  assert.ok(m, `styles.css must declare ${selector}`);
  return m[1];
}

// ---- F1: the provider-free path never loads react-dom ---------------------
// (the behavioural proof is bug704-store-wiring + bug755-restore-refusal-loud-
// redproof, whose DOM-less mutant children load store.tsx; a plain-node probe
// does NOT fail on react-dom load, so only the import-graph scan lives here.)

test('F1: only the provider file imports react-dom; components import the react-dom-free slot module', async () => {
  const slot = await read('../src/components/notificationSlot.tsx');
  assert.ok(!/from 'react-dom/.test(slot), 'notificationSlot.tsx must not import react-dom');
  for (const f of [
    '../src/components/AffordabilityConfirm.tsx',
    '../src/components/LineageRecoveryBanner.tsx',
    '../src/components/NewsFeed.tsx',
    '../src/components/StaleBuildBanner.tsx',
    '../src/components/MapView.tsx',
    '../src/sim/liveVersion.tsx',
  ]) {
    assert.ok(!(await read(f)).includes('/NotificationHost'), `${f} must not import NotificationHost (react-dom)`);
  }
});

// ---- F2: z registered below the modals; stack below the top bar -----------

test('F2: .notif-stack z is the registered NOTIF_STACK, below every blocking modal, and no 10001 survives', async () => {
  const css = await read('../src/styles.css');
  const body = ruleBody(css, '.notif-stack');
  assert.equal(Number(/z-index:\s*(\d+)/.exec(body)![1]), Z_INDEX.NOTIF_STACK);
  for (const k of ['INFO_DIALOG', 'INSOLVENCY_POPUP', 'DECLINE_SCREEN', 'REBUILD_PROMPT'] as const) {
    assert.ok(Z_INDEX.NOTIF_STACK < Z_INDEX[k], `NOTIF_STACK must sit under ${k}`);
  }
  assert.ok(!/10001/.test(css), 'the unregistered z 10001 must be gone');
  assert.ok(!/transform\s*:/.test(body), 'the stack must have no transform (it would capture the confirm backdrop)');
});

test('F2 (r3): the stack lives inside the map element, so it can never reach the top bar', async () => {
  const css = await read('../src/styles.css');
  assert.match(ruleBody(css, '.notif-stack'), /position:\s*absolute/);
});

// ---- F3: advisor routed through a slot ------------------------------------

test('F3: the MapView advisor prompt is a banner slot', async () => {
  const src = await read('../src/components/MapView.tsx');
  assert.match(src, /<NotificationSlot id="advisor" kind="banner">\s*<div\s+className=\{`advisor/);
  const css = await read('../src/styles.css');
  assert.match(ruleBody(css, '.notif-slot .advisor'), /position:\s*static/);
});

// ---- F4 / F5: real mount ---------------------------------------------------

async function mount(withConfirms: Array<string | undefined>, withBanner = true) {
  const dom = installJsdom();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { NotificationProvider } = await import('../src/components/NotificationHost.tsx');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner.tsx');
  const h = React.createElement;
  const root = createRoot(dom.window.document.getElementById('root')!);
  await act(async () => {
    root.render(
      h(
        NotificationProvider,
        null,
        withBanner ? h(StaleBuildBannerView, { result: { stale: true, runningSha: 'a', diskSha: 'b' } as any }) : null,
        ...withConfirms.map((slotId, i) =>
          h(AffordabilityConfirm, { key: i, slotId, message: `m${i}`, onConfirm: () => {}, onCancel: () => {} })
        )
      )
    );
  });
  return { doc: dom.window.document, root, act };
}

test('F4: an open confirm renders its own backdrop inside the modal host (r4 H1); the stack is has-dialog; banners go inert', async () => {
  const { doc, root, act } = await mount([undefined]);
  try {
    const stack = doc.querySelector('[data-notif-stack]') as HTMLElement;
    assert.ok(stack.classList.contains('has-dialog'));
    const bd = doc.querySelector('[data-notif-modal-host] [data-affordability-backdrop]') as HTMLElement;
    assert.ok(bd, 'the confirm must render its backdrop (removing it must fail this test)');
    assert.equal(bd.style.zIndex, '0');
    assert.equal(bd.style.pointerEvents, 'auto');
    assert.equal(bd.style.position, 'fixed');
    const css = await read('../src/styles.css');
    assert.match(ruleBody(css, '.notif-slot[inert], .notif-slot[inert] *'), /pointer-events:\s*none\s*!important/);
    assert.ok(stack.querySelector('.notif-slot:not(.notif-dialog)')!.hasAttribute('inert'), 'non-dialog slots carry inert');
    assert.equal(stack.querySelectorAll('.notif-slot:not(.notif-dialog)').length, 1, 'the banner shares the stack');
    assert.equal(stack.querySelectorAll('.notif-dialog').length, 0, 'dialogs are never in the stack');
  } finally {
    await act(async () => root.unmount());
  }
});

test('F4: with no confirm there is no backdrop and no has-dialog', async () => {
  const { doc, root, act } = await mount([]);
  try {
    const stack = doc.querySelector('[data-notif-stack]') as HTMLElement;
    assert.ok(!stack.classList.contains('has-dialog'));
    assert.equal(doc.querySelectorAll('[data-affordability-backdrop]').length, 0);
  } finally {
    await act(async () => root.unmount());
  }
});

test('F5: two simultaneous confirms are two slots - distinct ids, and even the SAME id never collapses', async () => {
  for (const ids of [
    ['affordability-confirm', 'affordability-confirm-demand'],
    [undefined, undefined],
  ]) {
    const { doc, root, act } = await mount(ids, false);
    try {
      const slots = Array.from(doc.querySelectorAll('.notif-dialog')) as HTMLElement[];
      assert.equal(slots.length, 2, `both owners render (${JSON.stringify(ids)})`);
      assert.notEqual(slots[0].style.order, slots[1].style.order, 'distinct slot orders');
    } finally {
      await act(async () => root.unmount());
    }
  }
  const demand = await read('../src/components/left/DemandDock.tsx');
  assert.match(demand, /slotId="affordability-confirm-demand"/);
});
