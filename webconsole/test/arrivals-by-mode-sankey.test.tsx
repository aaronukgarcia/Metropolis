// arrivals-by-mode-sankey.test.tsx — FEAT-1972079926 render coverage for
// ArrivalsByModeSankey.tsx. The data-shaping logic (arrivalsByModeSankeyModel)
// is unit-tested independently in test/arrivals-by-mode.test.mjs (plain
// .mjs, no JSX); this file proves the COMPONENT actually renders that data,
// including the honest empty state (GR#15: never a fabricated split).
// Mirrors population-sankey.test.tsx's structure.

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('ArrivalsByModeSankey: empty history renders the honest empty state, no fabricated numbers', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { ArrivalsByModeSankey } = await import('../src/components/ArrivalsByModeSankey.tsx');

  const html = renderToString(React.default.createElement(ArrivalsByModeSankey, { history: [] }));

  assert.ok(html.includes('No arrivals-by-mode history recorded yet'), 'shows the honest empty-state message');
  assert.ok(!html.includes('<svg'), 'does not render a fake Sankey chart with no data');
});

test('ArrivalsByModeSankey: with recorded history, renders the SVG bands + mode labels', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { ArrivalsByModeSankey } = await import('../src/components/ArrivalsByModeSankey.tsx');

  const history = [
    { tick: 30, road: 40, railLow: 15, railHs: 8, sea: 2, plane: 1 },
  ];
  const html = renderToString(React.default.createElement(ArrivalsByModeSankey, { history }));

  assert.ok(html.includes('<svg'), 'renders the Sankey SVG once history exists');
  assert.ok(html.includes('Road'), 'renders the Road node label');
  assert.ok(html.includes('Low-speed rail'), 'renders the Low-speed rail node label');
  assert.ok(html.includes('HS rail'), 'renders the HS rail node label');
  assert.ok(html.includes('Sea'), 'renders the Sea node label');
  assert.ok(html.includes('Plane'), 'renders the Plane node label');
  assert.ok(html.includes('Move-ins'), 'renders the Move-ins sink node label');
});

test('ArrivalsByModeSankey: a zero-value mode is not rendered as a left band', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { ArrivalsByModeSankey } = await import('../src/components/ArrivalsByModeSankey.tsx');

  // Only road has a nonzero value — nothing built for the other modes.
  const history = [{ tick: 30, road: 25, railLow: 0, railHs: 0, sea: 0, plane: 0 }];
  const html = renderToString(React.default.createElement(ArrivalsByModeSankey, { history }));

  assert.ok(html.includes('Road'), 'the available/nonzero mode renders');
  assert.ok(!html.includes('Plane 0'), 'a zero-value mode is filtered out of the left bands, not shown as a fake zero flow');
});

test('ArrivalsByModeSankey: window toggle buttons render for both month and year', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { ArrivalsByModeSankey } = await import('../src/components/ArrivalsByModeSankey.tsx');

  const history = [{ tick: 30, road: 10, railLow: 2, railHs: 0, sea: 0, plane: 0 }];
  const html = renderToString(React.default.createElement(ArrivalsByModeSankey, { history }));

  assert.ok(html.includes('Last month'), 'month window control present');
  assert.ok(html.includes('Last 12 months'), 'year window control present');
});
