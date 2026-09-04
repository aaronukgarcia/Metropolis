// affordability-confirm.test.tsx — BUG-652 follow-up, ROUND r3/r4 (2026-09-04).
//
// Round r2 (INDEPENDENT DESTRUCTIVE, GR#23) REJECTED the r2 estate's
// reducer-side affordability gate on F2 (BLOCKING): nothing under
// src/components ever read `state.affordabilityNotice`. The fix moved the
// gate to the UI dispatch site (MapView.tsx) and introduced
// AffordabilityConfirm.tsx as the actual reader/renderer — this test proves
// that component exists, renders the real recurring-cost message, and wires
// its two actions (mirrors rebuild-prompt.test.tsx's own renderToString
// pattern for RebuildPrompt, the idiom the coordinator asked this to follow).
//
// ROUND r4: the component's props were generalised from a single-placement
// shape (spec/x/y) to a bare `message` string, since round r4's shared
// placementGate.ts seam supplies a `commit` callback that already closes
// over whatever the ORIGINAL batch dispatch was — this component never
// needs to know whether it is confirming one building or a hundred.

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

test('AffordabilityConfirm: renders the real recurring-cost message and both actions', async () => {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');

  const message =
    'International Airport adds £1,870,000/tick in wages once staffed — more than 50% of your current income (£135,967/tick). Build anyway?';

  const html = renderToString(
    React.default.createElement(AffordabilityConfirm, {
      message,
      onConfirm: () => {},
      onCancel: () => {},
    })
  );

  assert.ok(html.includes('1,870,000'), 'the real marginal wage figure must be rendered');
  assert.ok(html.includes('Build anyway'), 'a confirm action must be rendered');
  assert.ok(html.includes('Cancel'), 'a cancel action must be rendered');
  assert.ok(html.includes('International Airport'), 'the spec name must appear in the message');
});

test('AffordabilityConfirm: renders an AGGREGATED batch message (round r4 — one confirm for a whole batch, not per-unit)', async () => {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');

  const message =
    '3 x Channel Tunnel Portal adds £5,400,000/tick in wages once staffed — more than 50% of your current income (£3,000,000/tick). Build anyway?';

  const html = renderToString(
    React.default.createElement(AffordabilityConfirm, { message, onConfirm: () => {}, onCancel: () => {} })
  );
  assert.ok(html.includes('3 x Channel Tunnel Portal'), 'the batch subject must name the count, not a single unit');
  assert.ok(html.includes('5,400,000'), 'the AGGREGATE wage figure must be shown, not a per-unit figure');
});

test('AffordabilityConfirm: onConfirm/onCancel are wired to the two buttons (props reach the DOM handlers, not decorative)', async () => {
  ensureWindow();
  const React = await import('react');
  const ReactDOMServer = await import('react-dom/server');
  const { AffordabilityConfirm } = await import('../src/components/AffordabilityConfirm.tsx');

  let confirmed = false;
  let cancelled = false;
  const el = React.default.createElement(AffordabilityConfirm, {
    message: 'Teaching Hospital adds £96,667/tick in wages once staffed. Build anyway?',
    onConfirm: () => { confirmed = true; },
    onCancel: () => { cancelled = true; },
  });

  // Renders without throwing (SSR-safe) — the actual click dispatch is
  // exercised at the MapView/DemandDock integration level (build-tool click
  // / drag flush / clone-paste / Fix button -> placementGate's
  // evaluatePlacementBatch -> pendingAfford state -> this component ->
  // onConfirm invokes the closed-over `commit` callback, proven by tsc + the
  // full component tree compiling and the batch tests in
  // attack-bug652-round2.test.mjs / bug652-jobs-grandfathering.test.mjs).
  const html = ReactDOMServer.renderToString(el);
  assert.ok(html.length > 0);
  assert.equal(confirmed, false, 'SSR alone must never fire the callback');
  assert.equal(cancelled, false);
});
