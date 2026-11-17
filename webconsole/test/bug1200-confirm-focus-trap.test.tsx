// bug1200-confirm-focus-trap.test.tsx - BUG-1200: the affordability confirm is
// a modal, so Tab / Shift+Tab must cycle inside it (never reach controls behind
// the backdrop), Esc must close it exactly like Cancel (and never confirm),
// and focus must return to the opener on close. Real React mount in jsdom.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

async function mount(calls: { confirm: number; cancel: number }) {
  const dom = new JSDOM(
    '<!doctype html><html><body><button id="topbar-a">File</button><button id="opener">open</button><button id="topbar-b">Config</button><div id="root"></div></body></html>',
    { url: 'http://localhost/', pretendToBeVisual: true }
  );
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  window.document.getElementById('opener')!.focus();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');
  const root = createRoot(window.document.getElementById('root')!);
  const render = async (open: boolean) =>
    act(async () => {
      root.render(
        open
          ? React.createElement(AffordabilityConfirm, {
              message: 'm',
              onConfirm: () => {
                calls.confirm++;
              },
              // Like the real owner, cancel unmounts the dialog.
              onCancel: () => {
                calls.cancel++;
                root.render(null);
              },
            })
          : null
      );
    });
  await render(true);
  const key = async (k: string) =>
    act(async () => {
      window.document.activeElement!.dispatchEvent(new window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
    });
  // jsdom does not implement default Tab navigation, so assert on
  // defaultPrevented too: that is what proves the trap took the key.
  const tab = async (shiftKey = false) => {
    let prevented = false;
    await act(async () => {
      const ev = new window.KeyboardEvent('keydown', { key: 'Tab', shiftKey, bubbles: true, cancelable: true });
      window.document.activeElement!.dispatchEvent(ev);
      prevented = ev.defaultPrevented;
    });
    return prevented;
  };
  return { doc: window.document, root, act, render, key, tab };
}

test('BUG-1200: Tab and Shift+Tab cycle inside the dialog (last wraps to first, first wraps back, stray focus pulled in)', async () => {
  const calls = { confirm: 0, cancel: 0 };
  const { doc, root, act, tab } = await mount(calls);
  try {
    const [ok, cancel] = Array.from(doc.querySelectorAll('[role="dialog"] button')) as HTMLElement[];
    assert.ok(doc.activeElement === ok, 'OK focused on open');
    cancel.focus();
    assert.equal(await tab(false), true, 'Tab on the last control is intercepted');
    assert.ok(doc.activeElement === ok, 'Tab from Cancel wraps to OK, not to the top bar');
    assert.equal(await tab(true), true, 'Shift+Tab on the first control is intercepted');
    assert.ok(doc.activeElement === cancel, 'Shift+Tab from OK wraps to Cancel, not to the top bar');
    // Focus that escaped to <body> is pulled back in.
    (doc.activeElement as HTMLElement).blur();
    assert.ok(doc.activeElement === doc.body, 'focus parked on body');
    await tab(false);
    assert.ok(doc.activeElement === ok, 'Tab from outside the dialog lands on the first dialog control');
    assert.equal(calls.confirm + calls.cancel, 0, 'tabbing never confirms or cancels');
  } finally {
    await act(async () => root.unmount());
  }
});

test('BUG-1200: Esc closes the dialog as Cancel and does NOT confirm; focus returns to the opener', async () => {
  const calls = { confirm: 0, cancel: 0 };
  const { doc, root, act, key } = await mount(calls);
  try {
    assert.ok(doc.querySelector('[role="dialog"]'), 'dialog open');
    await key('Escape');
    assert.equal(calls.cancel, 1, 'Esc invokes onCancel exactly once');
    assert.equal(calls.confirm, 0, 'Esc never confirms');
    assert.equal(doc.querySelector('[role="dialog"]'), null, 'dialog closed');
    assert.equal(doc.activeElement?.id, 'opener', 'focus restored to the opener');
  } finally {
    await act(async () => root.unmount());
  }
});

test('BUG-1200: after the dialog closes, Esc and Tab are no longer intercepted', async () => {
  const calls = { confirm: 0, cancel: 0 };
  const { root, act, key, tab, render } = await mount(calls);
  try {
    await render(false);
    await key('Escape');
    assert.equal(calls.cancel, 0, 'listener removed on unmount');
    assert.equal(await tab(false), false, 'Tab not intercepted once closed');
  } finally {
    await act(async () => root.unmount());
  }
});

test('BUG-1200 r2: Esc on the confirm cancels the confirm ONLY; a MapView-style window keydown handler never sees it, and sees Esc again after close', async () => {
  const calls = { confirm: 0, cancel: 0 };
  const { doc, root, act, key } = await mount(calls);
  // Registered exactly like MapView: window, bubble phase.
  const seen: string[] = [];
  const winHandler = (e: Event) => {
    seen.push((e as KeyboardEvent).key);
  };
  (globalThis as any).window.addEventListener('keydown', winHandler);
  try {
    await key('Escape');
    assert.equal(calls.cancel, 1, 'confirm cancelled once');
    assert.deepEqual(seen, [], 'window handler did NOT receive the Esc that closed the confirm');
    assert.equal(doc.querySelector('[role="dialog"]'), null, 'dialog closed');
    await key('Escape');
    assert.deepEqual(seen, ['Escape'], 'after close the window handler receives Esc again');
    assert.equal(calls.cancel, 1, 'closed confirm does not cancel again');
  } finally {
    (globalThis as any).window.removeEventListener('keydown', winHandler);
    await act(async () => root.unmount());
  }
});

test('BUG-1200 r2: Tab inside the open confirm is not seen by a window handler either', async () => {
  const calls = { confirm: 0, cancel: 0 };
  const { root, act, tab } = await mount(calls);
  const seen: string[] = [];
  const winHandler = (e: Event) => {
    seen.push((e as KeyboardEvent).key);
  };
  (globalThis as any).window.addEventListener('keydown', winHandler);
  try {
    await tab(false);
    await tab(true);
    assert.deepEqual(seen, [], 'window handler saw no Tab while the confirm is open');
  } finally {
    (globalThis as any).window.removeEventListener('keydown', winHandler);
    await act(async () => root.unmount());
  }
});
