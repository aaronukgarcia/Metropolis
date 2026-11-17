// attack-bug1160-r1.test.tsx — round r1 attacker suite for BUG-1160 (the ONE
// notification layout manager). Real StrictMode mount (BUG-756 lesson):
// double-invoked layout effects must not leave duplicate or orphan slots.

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
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

async function setup() {
  const dom = installJsdom();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { NotificationProvider } = await import('../src/components/NotificationHost.tsx');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner.tsx');
  const h = React.createElement;
  const root = createRoot(dom.window.document.getElementById('root')!);
  const doc = dom.window.document;
  const render = (open: boolean, onCancel: () => void = () => {}) =>
    act(async () => {
      root.render(
        h(
          React.StrictMode,
          null,
          h(
            NotificationProvider,
            null,
            h(StaleBuildBannerView, { result: { stale: true, runningSha: 'a', diskSha: 'b' } as any }),
            open ? h(AffordabilityConfirm, { message: 'Build it?', onConfirm: () => {}, onCancel }) : null
          )
        )
      );
    });
  return { doc, root, act, render };
}

test('r1 StrictMode: one dialog slot, one backdrop, critical stays ahead of dialog', async () => {
  const { doc, root, act, render } = await setup();
  try {
    await render(true);
    assert.equal(doc.querySelectorAll('[data-notif-id="affordability-confirm"]').length, 1);
    assert.equal(doc.querySelectorAll('[data-affordability-backdrop]').length, 1);
    assert.equal(doc.querySelectorAll('[role="dialog"]').length, 1);
    const ord = (id: string) => Number((doc.querySelector(`[data-notif-id="${id}"]`) as HTMLElement).style.order);
    assert.ok(ord('stale-build') < ord('affordability-confirm'), 'critical must lead the dialog under StrictMode');
  } finally {
    await act(async () => root.unmount());
  }
});

test('r1 StrictMode: 25 rapid open/close cycles leave no orphan slot or backdrop', async () => {
  const { doc, root, act, render } = await setup();
  try {
    for (let i = 0; i < 25; i++) {
      await render(true);
      await render(false);
    }
    assert.equal(doc.querySelectorAll('[data-notif-id="affordability-confirm"]').length, 0);
    assert.equal(doc.querySelectorAll('[data-affordability-backdrop]').length, 0);
    assert.equal(doc.querySelectorAll('[data-notif-id="stale-build"]').length, 1);
    await render(true);
    assert.equal(doc.querySelectorAll('[data-notif-id="affordability-confirm"]').length, 1);
  } finally {
    await act(async () => root.unmount());
  }
  assert.equal(doc.querySelectorAll('[data-notif-id]').length, 0, 'unmount removes every slot');
});

test('r1: portaled dialog buttons still fire their handlers (click through the portal)', async () => {
  const { doc, root, act, render } = await setup();
  let cancelled = 0;
  try {
    await render(true, () => cancelled++);
    const btn = Array.from(doc.querySelectorAll('[role="dialog"] button')).find((b) => b.textContent === 'Cancel') as HTMLButtonElement;
    assert.ok(btn.closest('[data-notif-modal-host]'), 'Cancel lives inside the modal host (portaled)');
    await act(async () => btn.click());
    assert.equal(cancelled, 1);
  } finally {
    await act(async () => root.unmount());
  }
});
