// population-sankey.test.tsx — FEAT-1972079925 render coverage for
// PopulationSankey.tsx. The data-shaping logic (demographicSankeyModel) is
// unit-tested independently in test/demographic-flows.test.mjs (plain .mjs,
// no JSX); this file proves the COMPONENT actually renders that data,
// including the honest empty state (GR#15: never a fabricated split).

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('PopulationSankey: empty history renders the honest empty state, no fabricated numbers', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { PopulationSankey } = await import('../src/components/PopulationSankey.tsx');

  const html = renderToString(React.default.createElement(PopulationSankey, { history: [] }));

  assert.ok(html.includes('No demographic history recorded yet'), 'shows the honest empty-state message');
  assert.ok(!html.includes('<svg'), 'does not render a fake Sankey chart with no data');
});

test('PopulationSankey: with recorded history, renders the SVG bands + real numbers', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { PopulationSankey } = await import('../src/components/PopulationSankey.tsx');

  const history = [
    { tick: 30, population: 1000, births: 20, deaths: 5, moveIns: 40, moveOuts: 15 },
  ];
  const html = renderToString(React.default.createElement(PopulationSankey, { history }));

  assert.ok(html.includes('<svg'), 'renders the Sankey SVG once history exists');
  assert.ok(html.includes('Births'), 'renders the Births node label');
  assert.ok(html.includes('Move-ins'), 'renders the Move-ins node label');
  assert.ok(html.includes('Deaths'), 'renders the Deaths node label');
  assert.ok(html.includes('Move-outs'), 'renders the Move-outs node label');
  // Real recorded totals: in = 20+40 = 60, out = 5+15 = 20.
  assert.ok(html.includes('60'), 'shows the real total-in figure derived from history');
  assert.ok(html.includes('20'), 'shows a real figure derived from history (deaths or total-out)');
});

test('PopulationSankey: window toggle buttons render for both month and year', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { PopulationSankey } = await import('../src/components/PopulationSankey.tsx');

  const history = [{ tick: 30, population: 100, births: 2, deaths: 1, moveIns: 3, moveOuts: 1 }];
  const html = renderToString(React.default.createElement(PopulationSankey, { history }));

  assert.ok(html.includes('Last month'), 'month window control present');
  assert.ok(html.includes('Last 12 months'), 'year window control present');
});
