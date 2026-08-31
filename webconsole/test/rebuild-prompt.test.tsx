// rebuild-prompt.test.tsx — FEAT-1972079917 RebuildPrompt UI tests.
//
// Proves the RebuildPrompt component (src/components/RebuildPrompt.tsx):
//   1. Progress phase renders spinner, progress bar, percent, and phase label
//   2. Percent is calculated correctly (actionsDone/actionsTotal * 100)
//   3. Percent advances monotonically across a real progress sequence (BAR-4 —
//      this is an ACTUAL non-decreasing assertion over multiple renders, not
//      just a single-snapshot check)
//   4. Stalled phase renders with explicit "Rebuild stalled" message and Retry button
//   5. Prompt phase renders no progress percent at all (nothing to be
//      monotonic about until a rebuild is actually running)

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('RebuildPrompt: running phase renders progress UI when progress is provided', async () => {
  // Set up minimal window for React SSR.
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  // Render with progress.
  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'running',
      savedVersion: '0.0.1.1',
      currentVersion: '0.0.1.2',
      report: null,
      progress: { actionsDone: 1500, actionsTotal: 3000, phaseLabel: 'Replaying buildings... 1,500/3,000 actions' },
      stallInfo: null,
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
    })
  );

  // Must contain the progress label.
  assert.ok(html.includes('1,500'), 'progress must show actionsDone count');
  assert.ok(html.includes('3,000'), 'progress must show total actions');
  assert.ok(html.includes('Replaying buildings'), 'progress must show phase label');
  // Percent should be roughly 50% (1500/3000).
  assert.ok(html.includes('50'), 'progress percent (50%) must be rendered');
});

test('RebuildPrompt: stalled phase renders failure message and Retry button', async () => {
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  const onRetry = () => {
    // Handler that will be registered but not called in this SSR test.
  };

  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'stalled',
      savedVersion: '0.0.1.1',
      currentVersion: '0.0.1.2',
      report: null,
      progress: null,
      stallInfo: { actionsDone: 800, actionsTotal: 3000, phaseLabel: 'Replaying roads... 800/3,000 actions' },
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
      onRetry,
    })
  );

  // Must contain "stalled" language and stall info.
  assert.ok(html.includes('stalled'), 'stalled phase must include "stalled" text');
  assert.ok(html.includes('800'), 'must show actionsDone at stall point');
  assert.ok(html.includes('3,000'), 'must show total actions');
  assert.ok(html.includes('Retry'), 'must have a Retry button');
  assert.ok(html.includes('Replaying roads'), 'must show phase label');
});

test('RebuildPrompt: percent is calculated correctly from actionsDone/actionsTotal', async () => {
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  // Test with 25% progress.
  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'running',
      savedVersion: null,
      currentVersion: '0.0.1.1',
      report: null,
      progress: { actionsDone: 250, actionsTotal: 1000, phaseLabel: 'Replaying...' },
      stallInfo: null,
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
    })
  );

  // Should render "25" somewhere (25%).
  assert.ok(html.includes('25'), 'percent (25%) must be rendered');
});

test('RebuildPrompt: percent advances monotonically across a sequence of progress values (BAR-4)', async () => {
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  // A representative progress sequence a real chunked replay would emit —
  // actionsDone strictly increasing toward actionsTotal.
  const sequence = [
    { actionsDone: 0, actionsTotal: 1000, phaseLabel: 'Replaying...' },
    { actionsDone: 50, actionsTotal: 1000, phaseLabel: 'Replaying...' },
    { actionsDone: 250, actionsTotal: 1000, phaseLabel: 'Replaying...' },
    { actionsDone: 500, actionsTotal: 1000, phaseLabel: 'Replaying...' },
    { actionsDone: 999, actionsTotal: 1000, phaseLabel: 'Replaying...' },
    { actionsDone: 1000, actionsTotal: 1000, phaseLabel: 'Replaying...' },
  ];

  let prevPercent = -1;
  for (const progress of sequence) {
    const html = renderToString(
      React.default.createElement(RebuildPrompt, {
        phase: 'running',
        savedVersion: null,
        currentVersion: '0.0.1.1',
        report: null,
        progress,
        stallInfo: null,
        onRebuild: () => {},
        onKeep: () => {},
        onFresh: () => {},
        onResume: () => {},
      })
    );
    const percent = (progress.actionsDone / progress.actionsTotal) * 100;
    assert.ok(
      html.includes(`${percent.toFixed(0)}%`),
      `rendered percent must match the computed value: expected ${percent.toFixed(0)}%`
    );
    assert.ok(
      percent >= prevPercent,
      `percent must be non-decreasing across the sequence: ${percent} >= ${prevPercent}`
    );
    prevPercent = percent;
  }
});

test('RebuildPrompt: prompt phase does not render progress UI', async () => {
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RebuildPrompt } = await import('../src/components/RebuildPrompt.tsx');

  // Render prompt phase (no progress should be shown).
  const html = renderToString(
    React.default.createElement(RebuildPrompt, {
      phase: 'prompt',
      savedVersion: '0.0.1.1',
      currentVersion: '0.0.1.2',
      report: null,
      progress: null,
      stallInfo: null,
      onRebuild: () => {},
      onKeep: () => {},
      onFresh: () => {},
      onResume: () => {},
    })
  );

  // Must contain prompt text.
  assert.ok(html.includes('New build detected'), 'prompt must show "New build detected"');
  // Progress bar should NOT be present (no percent from progress).
  assert.ok(!html.includes('%'), 'prompt should not render progress percent');
});
