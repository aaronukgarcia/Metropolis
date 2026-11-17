// bug1160-notification-layout.test.tsx — BUG-1160: the placement-confirm dialog
// and the Fix All toast (the NewsFeed ticker) stacked on the same rectangle,
// truncated. Cure: every transient overlay occupies a slot in ONE flow column
// (NotificationHost). jsdom has no layout engine, so "no overlap" is proven
// structurally: every overlay is a distinct direct child of ONE flex-column
// stack with a distinct order, the shipped CSS makes that stack a column flex
// that wraps (never ellipsises) its text, and the dialog panel has a readable
// width. Flex siblings in a column cannot share a rectangle.
//
// RED PROOF: mutating NotificationSlot to render inline (the unfixed
// per-overlay anchoring) and mutating .notif-stack to display:block each turn
// this file red (see the lane report for the scratch-copy procedure).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';

const here = path.dirname(fileURLToPath(import.meta.url));

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

const LONG_TOAST =
  'Fix All: built 3 of 7 shortfalls (power, water, housing) - click Fix All again for the rest; ' +
  'this is a deliberately long toast so truncation would be visible if the layout clipped it.';

async function mountAll() {
  const dom = installJsdom();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { NotificationProvider, NotificationSlot } = await import('../src/components/NotificationHost.tsx');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner.tsx');
  const { LineageRecoveryBanner } = await import('../src/components/LineageRecoveryBanner.tsx');
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  const h = React.createElement;
  await act(async () => {
    root.render(
      h(
        NotificationProvider,
        null,
        // the Fix All toast (the NewsFeed ticker registers as kind 'toast')
        h(NotificationSlot, { id: 'news-feed', kind: 'toast', children: h('div', { className: 'news-feed' }, LONG_TOAST) }),
        h(AffordabilityConfirm, {
          message: 'International Airport adds 1,870,000/tick in wages. Build anyway?',
          onConfirm: () => {},
          onCancel: () => {},
        }),
        h(StaleBuildBannerView, { result: { stale: true, runningSha: 'abc1234', diskSha: 'def5678' } as any }),
        h(LineageRecoveryBanner, { lineages: [], onAdopt: () => {}, onDismiss: () => {}, note: 'Found another city' })
      )
    );
  });
  return { dom, root, act, container };
}

test('BUG-1160: the dialog and the Fix All toast (plus banners) occupy distinct slots of ONE stack', async () => {
  const { dom, root, act } = await mountAll();
  try {
    const doc = dom.window.document;
    const stacks = doc.querySelectorAll('[data-notif-stack]');
    assert.equal(stacks.length, 1, 'exactly one notification stack');
    const stack = stacks[0];
    const slots = Array.from(stack.children) as HTMLElement[];
    const ids = slots.map((s) => s.getAttribute('data-notif-id')).sort();
    assert.deepEqual(ids, ['lineage-recovery', 'news-feed', 'stale-build']);
    const dlgSlot = doc.querySelector('[data-notif-id="affordability-confirm"]')!;
    assert.ok(dlgSlot.closest('[data-notif-modal-host]'), 'r4 H1: the dialog is in the modal host, not the stack');
    for (const id of ids) {
      const el = doc.querySelector(`[data-notif-id="${id}"]`)!;
      assert.equal(el.parentElement, stack, `${id} must be a direct child of the stack`);
    }
    const orders = slots.map((s) => Number(s.style.order));
    assert.equal(new Set(orders).size, orders.length, 'every slot has a distinct order');
    const orderOf = (id: string) => Number((doc.querySelector(`[data-notif-id="${id}"]`) as HTMLElement).style.order);
    assert.ok(orderOf('lineage-recovery') < orderOf('news-feed'), 'banner before toast');
    assert.ok(doc.querySelector('[role="dialog"]'), 'dialog rendered');
    assert.ok(doc.querySelector('.news-feed')!.textContent!.includes('click Fix All again for the rest'));
  } finally {
    await act(async () => root.unmount());
  }
});

test('BUG-1160: shipped CSS makes the stack a wrapping flex column with readable width (no ellipsis, no clipping)', async () => {
  const css = await fs.readFile(path.resolve(here, '../src/styles.css'), 'utf8');
  const body = (sel: string) => {
    const m = css.match(new RegExp('(?:^|\\n)' + sel.replace(/[.]/g, '\\.') + '\\s*\\{([^}]*)\\}'));
    assert.ok(m, `${sel} rule must exist`);
    return m![1];
  };
  const stack = body('.notif-stack');
  assert.match(stack, /display:\s*flex/);
  assert.match(stack, /flex-direction:\s*column/);
  assert.match(stack, /position:\s*absolute/);
  assert.match(stack, /overflow-y:\s*auto/, 'column scrolls instead of clipping');
  const w = stack.match(/width:\s*min\((\d+)px/);
  assert.ok(w && Number(w[1]) >= 400, 'stack is at least 400px wide at desktop width (readable)');
  assert.match(body('.notif-slot .news-feed-ticker-text'), /white-space:\s*normal/);
  assert.doesNotMatch(body('.notif-slot .news-feed-ticker-text'), /text-overflow:\s*ellipsis/);
  assert.match(body('.notif-slot .news-feed'), /position:\s*static/, 'slotted toast neutralises its own anchoring');
  assert.match(body('.notif-slot .lineage-recovery-banner'), /position:\s*static/);
  assert.match(body('.notif-slot .stale-build-banner'), /position:\s*static/);
});

test('BUG-1160: the dialog panel is wide enough to read and stays clickable inside the stack', async () => {
  const { dom, root, act } = await mountAll();
  try {
    const panel = dom.window.document.querySelector('[role="dialog"]') as HTMLElement;
    const maxW = parseInt(panel.style.maxWidth, 10);
    assert.ok(maxW >= 400, `dialog maxWidth ${maxW} must be >= 400`);
    assert.equal(panel.style.width, '100%', 'dialog is bounded by the stack, not the viewport');
    assert.equal(panel.style.pointerEvents, 'auto');
  } finally {
    await act(async () => root.unmount());
  }
});

test('BUG-1160: standalone (no provider) overlays render inline, unchanged', async () => {
  installJsdom();
  const React = (await import('react')).default;
  const { renderToString } = await import('react-dom/server');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const html = renderToString(
    React.createElement(AffordabilityConfirm, { message: 'x', onConfirm: () => {}, onCancel: () => {} })
  );
  assert.ok(html.includes('role="dialog"') && !html.includes('data-notif'));
});

test('BUG-1160: every real overlay component is wrapped in a NotificationSlot', async () => {
  for (const [file, id] of [
    ['../src/components/NewsFeed.tsx', 'news-feed'],
    ['../src/components/StaleBuildBanner.tsx', 'stale-build'],
    ['../src/components/LineageRecoveryBanner.tsx', 'lineage-recovery'],
    ['../src/components/AffordabilityConfirm.tsx', '{slotId}'],
    ['../src/components/MapView.tsx', 'advisor'],
    ['../src/sim/liveVersion.tsx', 'version-upgrade'],
  ] as const) {
    const src = await fs.readFile(path.resolve(here, file), 'utf8');
    assert.ok(src.includes(id === '{slotId}' ? '<NotificationSlot id={slotId}' : `<NotificationSlot id="${id}"`), `${file} must register slot ${id}`);
  }
  const app = await fs.readFile(path.resolve(here, '../src/App.tsx'), 'utf8');
  assert.ok(app.includes('<NotificationProvider anchored>'));
});
