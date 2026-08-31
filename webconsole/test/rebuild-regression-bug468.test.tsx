// rebuild-regression-bug468.test.tsx — BUG-468: the prompt handles a REGRESSION
// (saved build NEWER than the running build) without pushing the player toward an
// endless rebuild. It flips the copy and makes "Keep" the primary action.

import { test } from 'node:test';
import assert from 'node:assert/strict';

function ensureWindow() {
  if (typeof globalThis.window === 'undefined') {
    globalThis.window = {
      localStorage: {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
        clear: () => {},
        key: () => null,
        length: 0,
      },
      performance: { now: () => 0 },
    } as any;
  }
}

test('RebuildPrompt: regression (saved>running) flips to keep-primary copy', async () => {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'prompt',
      savedVersion: 'v0.3.0.193', // NEWER than running
      currentVersion: 'v0.3.0.191',
      report: null,
      progress: null,
      stallInfo: null,
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
    })
  );

  assert.ok(html.includes('Save from a newer build'), 'regression heading must be shown');
  assert.ok(html.includes('newer'), 'copy must explain the save is newer than the running build');
  assert.ok(html.includes('Keep my city'), 'Keep is offered as the primary action');
  assert.ok(html.includes('anyway'), 'rebuilding-on-older is offered but demoted ("anyway")');
});

test('RebuildPrompt: normal upgrade (saved<running) keeps the rebuild-primary copy', async () => {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'prompt',
      savedVersion: 'v0.3.0.189', // OLDER than running
      currentVersion: 'v0.3.0.191',
      report: null,
      progress: null,
      stallInfo: null,
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
    })
  );

  // NOTE: React SSR inserts an empty <!-- --> comment between static text and an
  // interpolated value, so assert on the stable substrings, not the joined string.
  assert.ok(html.includes('New build detected'), 'upgrade keeps the standard heading');
  assert.ok(html.includes('Rebuild on '), 'Rebuild stays the primary action');
  assert.ok(!html.includes('anyway'), 'upgrade must NOT demote rebuild to "anyway"');
  assert.ok(html.includes('Keep old snapshot'), 'Keep is still offered');
});
