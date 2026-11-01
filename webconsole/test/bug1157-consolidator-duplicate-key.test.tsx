// bug1157-consolidator-duplicate-key.test.tsx — BUG-1157: React "Encountered
// two children with the same key" on Aaron's 2026-10-31 level-15 city.
//
// ROOT CAUSE: consolidatorTab.tsx's renderOpportunityTable keyed consolidate
// rows `c-${sectionKey}-${fromSpec}-${toSpec}`. findCityWideOpportunities
// (consolidator.ts) emits one opportunity PER GROUP for city-wide families
// (kindergartens, nurseries, ...) and anchors each group to the section
// holding most of its members — at scale (>= 2 groups of one rung) two groups
// share anchor section + fromSpec + toSpec, so the key collides. The tier
// audit rows `${sectionKey}-${tier}` collide whenever one section has more
// than one plan for the same tier.
//
// Mechanical check: walk the returned element tree and assert sibling keys
// are unique, plus renderToString with a console.error backstop. Pure
// functions of props, no state: StrictMode-safe (BUG-756).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { renderOpportunityTable, renderTierAuditSection } from '../src/components/left/tabs/consolidatorTab';

function duplicateSiblingKeys(node: any, out: string[] = []): string[] {
  if (Array.isArray(node)) {
    const seen = new Set<string>();
    for (const c of node) {
      if (c && typeof c === 'object' && !Array.isArray(c) && c.key != null) {
        if (seen.has(String(c.key))) out.push(String(c.key));
        seen.add(String(c.key));
      }
      duplicateSiblingKeys(c, out);
    }
  } else if (node && typeof node === 'object' && node.props) {
    duplicateSiblingKeys(node.props.children, out);
  }
  return out;
}

function trapConsoleError<T>(fn: () => T): { result: T; errors: string[] } {
  const errors: string[] = [];
  const orig = console.error;
  console.error = (...a: unknown[]) => {
    errors.push(a.map(String).join(' '));
  };
  try {
    return { result: fn(), errors };
  } finally {
    console.error = orig;
  }
}

const consolidate = (ids: number[]) => ({
  kind: 'consolidate' as const,
  rank: 0,
  sectionKey: 7,
  fromSpec: 'edu_kindergarten',
  toSpec: 'edu_kindergarten_city',
  groupCount: ids.length,
  buildingIds: ids,
  buildCost: 1,
  scrapRecovered: 1,
  netCost: 0,
  capacityGain: 5,
  buildingCountReduction: ids.length - 1,
});

test('BUG-1157: two city-wide groups anchored to one section render unique opportunity row keys', () => {
  const top = [consolidate([1, 2, 3]), consolidate([4, 5, 6])].map((o, i) => ({ ...o, rank: i + 1 })) as any;
  const el = renderOpportunityTable(top, 'none');
  assert.deepEqual(duplicateSiblingKeys(el), [], 'duplicate sibling keys in opportunity table');
  const { errors } = trapConsoleError(() => renderToString(el as React.ReactElement));
  assert.deepEqual(errors.filter((e) => e.includes('same key')), []);
});

test('BUG-1157: two tier plans for the same section+tier render unique audit row keys', () => {
  const ta = { tier: 1, actuallyPlaced: true, actualTiles: [], actualCost: 0 };
  const state: any = {
    consolidatorLog: [
      { id: 1, tick: 1, tierLayout: [{ sectionKey: 3, tierAudit: [ta, ta] }, { sectionKey: 3, tierAudit: [ta] }] },
    ],
  };
  const el = renderTierAuditSection(state);
  assert.deepEqual(duplicateSiblingKeys(el), []);
  const { errors } = trapConsoleError(() => renderToString(el as React.ReactElement));
  assert.deepEqual(errors.filter((e) => e.includes('same key')), []);
});
