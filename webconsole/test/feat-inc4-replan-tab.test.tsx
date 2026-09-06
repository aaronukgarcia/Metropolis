// feat-inc4-replan-tab.test.tsx — FEAT-2326609779 inc4: the consolidator
// tab's "Re-plan: N/M steps, ports K/K" line (the lead ruling's own wording).
// Rendered for real via react-dom/server, mirroring the estate's existing
// hud-inc2-tab-tree.test.tsx idiom.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { renderReplanSection } from '../src/components/left/tabs/consolidatorTab.tsx';
import type { SimState } from '../src/sim/types.ts';

const REPLAN = {
  planKey: '16,32,16,16',
  planTiles: 40,
  tilesDone: 12,
  stepsTotal: 37,
  stepsDone: 9,
  portsTotal: 3,
  portsVerified: 3,
  converged: false,
  executedThisPass: 3,
};

// The renderer reads ONLY `consolidatorLog`, so a partial state is the honest
// fixture here — cast at the single boundary rather than fabricating 29
// unrelated SimState fields that would go stale the moment the type changes.
function stateWith(passes: unknown[]): SimState {
  return { consolidatorLog: passes } as unknown as SimState;
}

test('renders the Re-plan line with steps and ports from the newest reporting pass', async () => {
  const { renderToString } = await import('react-dom/server');
  const html = renderToString(
    renderReplanSection(stateWith([{ id: 9, tick: 300, transactions: [], skipped: [], replan: REPLAN }])),
  );
  assert.ok(html.includes('Re-plan:'), 'the line is present');
  assert.ok(html.includes('9'), 'stepsDone rendered');
  assert.ok(html.includes('37'), 'stepsTotal rendered');
  assert.ok(html.includes('ports'), 'the ports clause is present');
  assert.ok(html.includes('16,32,16,16'), 'the box position is shown');
  assert.ok(html.includes('12'), 'tilesDone rendered');
  assert.ok(!html.includes('converged'), 'an unconverged job does not claim convergence');
});

test('a converged job says so', async () => {
  const { renderToString } = await import('react-dom/server');
  const html = renderToString(
    renderReplanSection(
      stateWith([{ id: 10, tick: 400, transactions: [], skipped: [], replan: { ...REPLAN, converged: true } }]),
    ),
  );
  assert.ok(html.includes('converged'), 'convergence is surfaced');
});

test('it reads the NEWEST pass carrying a replan block, skipping passes without one', async () => {
  const { renderToString } = await import('react-dom/server');
  const html = renderToString(
    renderReplanSection(
      stateWith([
        { id: 12, tick: 500, transactions: [], skipped: [] },
        { id: 11, tick: 450, transactions: [], skipped: [], replan: { ...REPLAN, stepsDone: 21, planKey: '1,2,3,4' } },
        { id: 10, tick: 400, transactions: [], skipped: [], replan: REPLAN },
      ]),
    ),
  );
  assert.ok(html.includes('1,2,3,4'), 'the NEWEST reporting pass wins');
  assert.ok(html.includes('21'), 'its stepsDone is the one shown');
});

test('renders NOTHING (not a misleading zero) when no pass ever reported a re-plan', () => {
  assert.equal(renderReplanSection(stateWith([{ id: 1, tick: 10, transactions: [], skipped: [] }])), null);
  assert.equal(renderReplanSection(stateWith([])), null);
  assert.equal(renderReplanSection({} as unknown as SimState), null, 'an old save with no consolidatorLog at all is safe (GR#16)');
});
