// config-confirm.test.tsx
//
// BUG-575 (P1 data loss): ConfigMenu's "Clear named cities" and "Clear
// autosave slots" buttons destroyed the player's saved cities / autosave
// rotation IMMEDIATELY on click, sitting right next to genuinely harmless
// clears (journal / pre-wipe archives / debug queue). A misclick permanently
// destroyed saves with no confirm and no undo.
//
// Fix: both destructive actions are now armed by a first click (which swaps
// the button for an inline "Delete ALL...? / Yes, delete / Cancel" confirm,
// mirroring the state-driven confirm idiom FileMenu already uses for BUG-445's
// save/rename collision, WITHOUT a blocking window.confirm() native dialog)
// and only actually clear storage on the explicit second click.
//
// Uses the same jsdom + react-dom/client + act() mounting recipe as
// bug512-bug513-save-error-robustness.test.tsx. ConfigMenu reads/writes
// window.localStorage directly and does not use useSim/SimProvider, so it is
// mounted standalone here.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const MOUNTED_TEST_TIMEOUT_MS = 20_000;

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

function findButtonByText(container: HTMLElement, text: string): HTMLButtonElement {
  const buttons = Array.from(container.querySelectorAll('button'));
  const btn = buttons.find((b) => b.textContent?.trim() === text);
  assert.ok(btn, `expected to find a button with text "${text}"`);
  return btn as HTMLButtonElement;
}

async function mountConfigMenu() {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { ConfigMenu } = await import('../src/components/ConfigMenu.tsx');
  return { React, createRoot, act, ConfigMenu };
}

test(
  'BUG-575: "Clear named cities" requires an explicit confirm before it clears storage',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const { NAMED_SAVES_INDEX_KEY, NAMED_SAVE_SLOT_PREFIX } = await import('../src/sim/namedsaves.ts');
      const { React, createRoot, act, ConfigMenu } = await mountConfigMenu();

      dom.window.localStorage.setItem(NAMED_SAVES_INDEX_KEY, JSON.stringify(['fixture-city']));
      dom.window.localStorage.setItem(`${NAMED_SAVE_SLOT_PREFIX}fixture-city`, JSON.stringify({ name: 'Fixture City' }));

      const container = dom.window.document.getElementById('root')!;
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(ConfigMenu));
        });
        await act(async () => {
          findButtonByText(container, 'Config').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });

        // First click ARMS the confirm — storage must be untouched.
        await act(async () => {
          findButtonByText(container, 'Clear named cities').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });
        assert.ok(
          dom.window.localStorage.getItem(NAMED_SAVES_INDEX_KEY) !== null,
          'BUG-575: the first click must NOT clear named cities — it only arms the confirm',
        );
        assert.ok(
          dom.window.localStorage.getItem(`${NAMED_SAVE_SLOT_PREFIX}fixture-city`) !== null,
          'BUG-575: the named-city slot must survive the arming click',
        );

        // Cancel must leave storage untouched and re-show the plain button.
        await act(async () => {
          findButtonByText(container, 'Cancel').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });
        assert.ok(dom.window.localStorage.getItem(NAMED_SAVES_INDEX_KEY) !== null, 'Cancel must not clear anything');
        findButtonByText(container, 'Clear named cities'); // still present, un-armed

        // Arm again, then confirm — NOW storage must clear.
        await act(async () => {
          findButtonByText(container, 'Clear named cities').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });
        await act(async () => {
          findButtonByText(container, 'Yes, delete').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });
        assert.equal(
          dom.window.localStorage.getItem(NAMED_SAVES_INDEX_KEY),
          null,
          'BUG-575: confirming must clear the named-saves index',
        );
        assert.equal(
          dom.window.localStorage.getItem(`${NAMED_SAVE_SLOT_PREFIX}fixture-city`),
          null,
          'BUG-575: confirming must clear the named-city slot',
        );
      } finally {
        await act(async () => {
          root.unmount();
        });
      }
    } finally {
      dom.window.close();
    }
  },
);

test(
  'BUG-575: "Clear autosave slots" requires an explicit confirm before it clears storage',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const { SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
      const { React, createRoot, act, ConfigMenu } = await mountConfigMenu();

      dom.window.localStorage.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify({ tick: 1 }));

      const container = dom.window.document.getElementById('root')!;
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(ConfigMenu));
        });
        await act(async () => {
          findButtonByText(container, 'Config').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });

        await act(async () => {
          findButtonByText(container, 'Clear autosave slots').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });
        assert.ok(
          dom.window.localStorage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`) !== null,
          'BUG-575: the first click must NOT clear autosave slots — it only arms the confirm',
        );

        await act(async () => {
          findButtonByText(container, 'Yes, delete').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });
        assert.equal(
          dom.window.localStorage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`),
          null,
          'BUG-575: confirming must clear the autosave slot',
        );
      } finally {
        await act(async () => {
          root.unmount();
        });
      }
    } finally {
      dom.window.close();
    }
  },
);

test(
  'BUG-575 non-regression: the harmless clears (journal / pre-wipe archives / debug queue) remain single-click, no confirm gate',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const { JOURNAL_KEY } = await import('../src/sim/journal.ts');
      const { PREWIPE_ARCHIVE_KEY } = await import('../src/sim/captureBeforeWipe.ts');
      const { QUEUE_KEY } = await import('../src/sim/commitqueue.ts');
      const { React, createRoot, act, ConfigMenu } = await mountConfigMenu();

      dom.window.localStorage.setItem(JOURNAL_KEY, JSON.stringify({ entries: [] }));
      dom.window.localStorage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify([]));
      dom.window.localStorage.setItem(QUEUE_KEY, JSON.stringify([]));

      const container = dom.window.document.getElementById('root')!;
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(ConfigMenu));
        });
        await act(async () => {
          findButtonByText(container, 'Config').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });

        await act(async () => {
          findButtonByText(container, 'Clear journal').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
        });
        assert.equal(dom.window.localStorage.getItem(JOURNAL_KEY), null, 'Clear journal must still be one-click');

        await act(async () => {
          findButtonByText(container, 'Clear pre-wipe archives').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });
        assert.equal(
          dom.window.localStorage.getItem(PREWIPE_ARCHIVE_KEY),
          null,
          'Clear pre-wipe archives must still be one-click',
        );

        await act(async () => {
          findButtonByText(container, 'Clear debug queue').dispatchEvent(
            new dom.window.MouseEvent('click', { bubbles: true }),
          );
        });
        assert.equal(dom.window.localStorage.getItem(QUEUE_KEY), null, 'Clear debug queue must still be one-click');
      } finally {
        await act(async () => {
          root.unmount();
        });
      }
    } finally {
      dom.window.close();
    }
  },
);
